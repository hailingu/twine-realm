// 本文件限制只读 Git 子进程的环境与输出，不执行 shell 或暴露 Git 诊断。
package gitworkspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// gitOutput 拒绝无界工作区列表，写入失败会使本次观察成为明确未知结果。
type gitOutput struct{ buffer bytes.Buffer }

// Write 约束真实子进程输出，避免大量路径耗尽平台内存。
func (output *gitOutput) Write(data []byte) (int, error) {
	if output.buffer.Len()+len(data) > 512*1024 {
		return 0, errors.New("Git 输出超过观察上限")
	}
	return output.buffer.Write(data)
}

// command 仅供固定命令调用；外层 Git 定位、替换对象和配置覆盖均不继承。
func (inspector *Inspector) command(ctx context.Context, path, name string, arguments ...string) (string, error) {
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", path, name}, arguments...)
	command := exec.CommandContext(ctx, inspector.git, args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1"}
	var output gitOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.buffer.String()), nil
}
