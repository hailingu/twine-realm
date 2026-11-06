// 本文件只为受信数据库协议提供有界交互，敏感输入/输出不进入错误诊断。
package localdocker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

// Session 由单个调用者串行读写并负责Close；上下文结束会中断宿主Docker客户端。
type Session struct {
	command        *exec.Cmd
	input          io.WriteCloser
	output         io.ReadCloser
	stderr         boundedBuffer
	cancel         context.CancelFunc
	inputBytes     int
	outputBytes    int
	inputExceeded  bool
	outputExceeded bool
	closed         bool
	closeErr       error
}

// OpenSession 启动固定Docker参数的交互通道，不继承秘密、元数据开关或执行shell。
func OpenSession(ctx context.Context, policy Policy, args ...string) (*Session, error) {
	if policy.BuildMetadata {
		return nil, failure("DOCKER_POLICY_INVALID", "敏感交互不接受构建元数据策略", false, false)
	}
	ctx, cancel := context.WithTimeout(ctx, policy.Timeout)
	command, err := dockerProcess(ctx, policy, args)
	if err != nil {
		cancel()
		return nil, err
	}
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, failure("DOCKER_SESSION_UNAVAILABLE", "无法建立受控输入通道", false, false)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.Join(failure("DOCKER_SESSION_UNAVAILABLE", "无法建立受控输出通道", false, false), closePipe(input))
	}
	value := &Session{command: command, input: input, output: output, cancel: cancel}
	command.Stderr = &value.stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, errors.Join(failure("DOCKER_SESSION_UNAVAILABLE", "受控交互进程未启动", true, false), closePipe(input), closePipe(output))
	}
	return value, nil
}

// Write 累计输入最多1MiB；上限前拒绝整次写入，不把秘密加到argv或错误。
func (session *Session) Write(value []byte) (int, error) {
	if session.closed {
		return 0, io.ErrClosedPipe
	}
	if len(value) > 1024*1024-session.inputBytes {
		session.inputExceeded = true
		session.cancel()
		return 0, failure("DOCKER_INPUT_INVALID", "交互输入超出受控上限，须核实原结果", false, true)
	}
	n, err := session.input.Write(value)
	session.inputBytes += n
	return n, err
}

// Read 累计标准输出最多1MiB；一字节越界探测不把超限数据交给协议解析器。
func (session *Session) Read(value []byte) (int, error) {
	if session.closed {
		return 0, io.ErrClosedPipe
	}
	if len(value) == 0 {
		return 0, nil
	}
	remaining := 1024*1024 - session.outputBytes
	if len(value) > remaining+1 {
		value = value[:remaining+1]
	}
	n, err := session.output.Read(value)
	if n > remaining {
		session.outputExceeded = true
		session.cancel()
		return 0, failure("DOCKER_OUTPUT_INVALID", "交互输出超出受控上限，须核实原结果", false, true)
	}
	session.outputBytes += n
	return n, err
}

// Interrupt 可与串行读写并发，只取消受控进程；回收仍由原拥有者调用Close。
func (session *Session) Interrupt() { session.cancel() }

// Close 先关闭输入并回收进程，再释放期限；告警或失败不包含任何原始交互字节。
func (session *Session) Close() error {
	if session.closed {
		return session.closeErr
	}
	session.closed = true
	defer session.cancel()
	inputErr := closePipe(session.input)
	waitErr := session.command.Wait()
	outputErr := closePipe(session.output)
	var result error
	switch {
	case session.inputExceeded:
		result = failure("DOCKER_INPUT_INVALID", "交互输入超出受控上限，须核实原结果", false, true)
	case session.outputExceeded || session.stderr.exceeded:
		result = failure("DOCKER_OUTPUT_INVALID", "交互输出超出受控上限，须核实原结果", false, true)
	case waitErr != nil:
		result = failure("DOCKER_RESULT_UNKNOWN", "交互进程未确认，须核实原结果", true, true)
	case hasWarning(session.stderr.data.String()):
		result = failure("DOCKER_WARNING", "交互进程报告告警，须处理后核实", false, true)
	}
	session.closeErr = errors.Join(result, inputErr, outputErr)
	return session.closeErr
}

// closePipe 将已由子进程回收的管道视为已关闭，其他清理失败仍保留。
func closePipe(pipe io.Closer) error {
	err := pipe.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
