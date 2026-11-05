//go:build integration

// 本文件仅启动本次构建的模板二进制，明确隔离环境并等待自己的进程退出。
package integration_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// process 保存唯一子进程的完成屏障，读取日志只发生在退出后。
type process struct {
	command *exec.Cmd
	done    chan struct{}
	failure error
	logs    bytes.Buffer
	address string
}

// command 环境显式构造，管理 / 外层 Git / 模型凭据都不会继承到模板进程。
func (f *fixture) command(t *testing.T, mode, address string) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("TWINE_TEMPLATE_TEST_BINARY")
	if binary == "" {
		t.Fatal("模板集成检查缺少本次构建的二进制")
	}
	command := exec.Command(binary, mode)
	command.Dir = f.directory
	command.Env = []string{"TZ=UTC", "DB_HOST=" + f.host, "DB_PORT=" + f.port, "DB_NAME=" + f.name, "HTTP_ADDRESS=" + address, "SERVICE_NAME=独立初始化示例"}
	configuration := f.configuration(mode == "migrate")
	if mode == "migrate" {
		command.Env = append(command.Env, "MIGRATION_USER="+configuration.User, "MIGRATION_PASSWORD_FILE="+configuration.PasswordFile)
	} else {
		command.Env = append(command.Env, "DB_USER="+configuration.User, "DB_PASSWORD_FILE="+configuration.PasswordFile)
	}
	if directory := os.Getenv("TWINE_TEMPLATE_COVER_DIRECTORY"); directory != "" {
		command.Env = append(command.Env, "GOCOVERDIR="+directory)
	}
	return command
}

// execute 返回退出结果，错误输出只在核对不含秘密后才可用于诊断。
func (f *fixture) execute(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	command := f.command(t, mode, "127.0.0.1:8080")
	output, err := command.CombinedOutput()
	f.checkSecrets(t, output)
	return output, err
}

// start 以真实存活响应核实本机启动，不用固定睡眠代替成功信号。
func (f *fixture) start(t *testing.T) *process {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	value := &process{done: make(chan struct{}), address: "http://" + address}
	value.command = f.command(t, "serve", address)
	value.command.Stdout = &value.logs
	value.command.Stderr = &value.logs
	if err := value.command.Start(); err != nil {
		t.Fatal("模板测试进程无法启动")
	}
	go func() { value.failure = value.command.Wait(); close(value.done) }()
	t.Cleanup(func() {
		if err := value.stop(); err != nil {
			t.Error(err)
		}
		f.checkSecrets(t, value.logs.Bytes())
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-value.done:
			t.Fatal("模板进程未开放存活探针即退出")
		default:
		}
		response, err := (&http.Client{Timeout: 300 * time.Millisecond}).Get(value.address + "/livez")
		if err == nil {
			status := response.StatusCode
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if status == 200 {
				return value
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("模板进程未在期限内存活")
	return nil
}

// stop 只向本次拥有的进程发 SIGTERM；超时清理也明确报告失败。
func (value *process) stop() error {
	select {
	case <-value.done:
		return value.failure
	default:
	}
	if err := value.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("模板测试进程停止信号失败")
	}
	select {
	case <-value.done:
		return value.failure
	case <-time.After(15 * time.Second):
		if err := value.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("模板测试进程清理失败")
		}
		<-value.done
		return fmt.Errorf("模板进程未在期限内正常退出")
	}
}

// checkSecrets 不输出实际密码，即使发现泄露也只报告固定诊断。
func (f *fixture) checkSecrets(t *testing.T, output []byte) {
	t.Helper()
	text := string(output)
	if strings.Contains(text, f.appPassword) || strings.Contains(text, f.migrationPassword) || strings.Contains(text, "TWINE_TEMPLATE_ADMIN_DSN") {
		t.Fatal("模板进程输出暴露了秘密或管理配置")
	}
}

// request 核实实际 HTTP 状态并关闭响应，不把连接失败当就绪。
func (value *process) request(t *testing.T, path string, status int) []byte {
	t.Helper()
	response, err := (&http.Client{Timeout: 4 * time.Second}).Get(value.address + path)
	if err != nil {
		t.Fatal("模板 HTTP 请求失败")
	}
	data, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != status {
		t.Fatalf("模板 HTTP 结果不符：%d", response.StatusCode)
	}
	return data
}
