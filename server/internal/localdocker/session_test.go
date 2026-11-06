// 本文件使用真实受控进程验证敏感交互的环境、限额、失败与回收，不接触Docker资源。
package localdocker

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sessionDocker 在测试私有PATH放入固定模拟进程，仅改变本次测试环境。
func sessionDocker(t *testing.T, script string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker"), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		t.Setenv(key, "")
	}
}

// openSessionFixture 即使断言提前失败也回收已启动的真实测试进程。
func openSessionFixture(t *testing.T, policy Policy, args ...string) *Session {
	t.Helper()
	session, err := OpenSession(context.Background(), policy, args...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !session.closed {
			if err := session.Close(); err != nil {
				t.Error("提前退出后的进程回收未确认", err)
			}
		}
	})
	return session
}

// TestSessionEnvironmentAndOwnership 真实往返不继承秘密；正常Close不因自身cancel被误判失败。
func TestSessionEnvironmentAndOwnership(t *testing.T) {
	sessionDocker(t, `[ -z "$TWINE_TEST_PRIVATE_VALUE" ] || exit 77
exec /bin/cat`)
	t.Setenv("TWINE_TEST_PRIVATE_VALUE", "synthetic-private-value")
	session := openSessionFixture(t, Policy{Timeout: 2 * time.Second}, "--context", "local-test", "exec", "--interactive", "owned-test", "mysql")
	if _, err := io.WriteString(session, "synthetic-private-input\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(session).ReadString('\n')
	if err != nil || line != "synthetic-private-input\n" {
		t.Fatal("真实交互往返未完成")
	}
	if err := session.Close(); err != nil {
		t.Fatal("正常关闭被错误判为取消或故障", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal("重复回收改变了原结果", err)
	}
	if _, err := session.Write([]byte("closed")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("已回收会话仍可写入")
	}
	if _, err := session.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("已回收会话仍可读取")
	}
}

// TestSessionFailuresRemainPrivate 退出错误、真实告警和标准错误超限均失败，但不保留敏感原文。
func TestSessionFailuresRemainPrivate(t *testing.T) {
	for _, value := range []struct{ name, script, code string }{
		{"进程失败", "printf 'synthetic-private-value' >&2\nexit 9", "DOCKER_RESULT_UNKNOWN"},
		{"真实告警", "printf 'WARNING: synthetic-private-value' >&2\nexit 0", "DOCKER_WARNING"},
		{"错误流超限", "exec /usr/bin/head -c 1048577 /dev/zero >&2", "DOCKER_OUTPUT_INVALID"},
	} {
		t.Run(value.name, func(t *testing.T) {
			sessionDocker(t, value.script)
			session := openSessionFixture(t, Policy{Timeout: 2 * time.Second})
			if _, err := io.Copy(io.Discard, session); err != nil {
				t.Fatal(err)
			}
			err := session.Close()
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != value.code || len(fault.Output) != 0 || strings.Contains(err.Error(), "synthetic-private-value") {
				t.Fatal("故障/告警未阻断或敏感原文进入诊断")
			}
		})
	}
}

// TestSessionInputAndOutputLimits 敏感输入先拒绝超限，输出不把第一个越界字节交给消费者。
func TestSessionInputAndOutputLimits(t *testing.T) {
	for _, input := range []bool{false, true} {
		t.Run(map[bool]string{false: "输出", true: "输入"}[input], func(t *testing.T) {
			script := "exec /usr/bin/head -c 1048577 /dev/zero"
			if input {
				script = "exec /bin/cat"
			}
			sessionDocker(t, script)
			session := openSessionFixture(t, Policy{Timeout: 2 * time.Second})
			var err error
			if input {
				_, err = session.Write(make([]byte, 1048577))
			} else {
				_, err = io.Copy(io.Discard, session)
			}
			if err == nil || session.Close() == nil {
				t.Fatal("超限未成为明确失败")
			}
		})
	}
}

// TestSessionExactOutputLimit 合法的边界大小仍能完成，不把精确上限误报成超限。
func TestSessionExactOutputLimit(t *testing.T) {
	sessionDocker(t, "exec /usr/bin/head -c 1048576 /dev/zero")
	session := openSessionFixture(t, Policy{Timeout: 2 * time.Second})
	n, err := io.Copy(io.Discard, session)
	if err != nil || n != 1048576 {
		t.Fatal("合法边界输出未完成", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal("精确上限被误报失败", err)
	}
}

// TestSessionCancellationAndPolicy 超时回收真实阻塞进程，非法策略不启动会话。
func TestSessionCancellationAndPolicy(t *testing.T) {
	sessionDocker(t, "exec /bin/sleep 10")
	session := openSessionFixture(t, Policy{Timeout: 50 * time.Millisecond})
	if _, err := io.Copy(io.Discard, session); err != nil {
		t.Fatal(err)
	}
	var fault *Fault
	if !errors.As(session.Close(), &fault) || fault.Code != "DOCKER_RESULT_UNKNOWN" {
		t.Fatal("期限结束未保留未知结果")
	}
	for _, policy := range []Policy{{}, {Timeout: 11 * time.Minute}, {Timeout: time.Second, BuildMetadata: true}} {
		if _, err := OpenSession(context.Background(), policy); err == nil {
			t.Fatal("非法策略启动了会话")
		}
	}
	t.Setenv("DOCKER_HOST", "tcp://invalid.test:2375")
	if _, err := OpenSession(context.Background(), Policy{Timeout: time.Second}); err == nil {
		t.Fatal("Docker环境覆盖未被拒绝")
	}
}
