// 本文件通过一个有界Git只读进程读取原始blob，不为每个文件运行独立命令。
package gitworkspace

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// objectReader 拥有Git子进程、输入和读取协议；导出结束必须结清该进程。
type objectReader struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	cancel  context.CancelFunc
}

// openObjects 只使用同一规范仓库和已隔离的Git环境，额外输出不进入日志。
func (inspector *Inspector) openObjects(ctx context.Context, repository string) (*objectReader, error) {
	ctx, cancel := context.WithCancel(ctx)
	command := inspector.newCommand(ctx, repository, "cat-file", "--batch")
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.Join(err, input.Close())
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, errors.Join(err, input.Close(), output.Close())
	}
	return &objectReader{command, input, bufio.NewReaderSize(output, 1024), cancel}, nil
}

// copyBlob 只接受请求的确切对象/类型/大小，正文只复制已核实的字节数。
func (objects *objectReader) copyBlob(id string, size int64, target io.Writer) error {
	if _, err := io.WriteString(objects.input, id+"\n"); err != nil {
		return project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "Git对象读取请求未完成")
	}
	header, err := objects.output.ReadSlice('\n')
	if err != nil || len(header) > 256 {
		return project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "Git对象读取结果缺失或超限")
	}
	fields := strings.Fields(string(header))
	if len(fields) != 3 || fields[0] != id || fields[1] != "blob" || fields[2] != strconv.FormatInt(size, 10) {
		return project.Failure("SOURCE_MATERIAL_INVALID", "Git对象与原清单身份或大小不一致")
	}
	if _, err := io.CopyN(target, objects.output, size); err != nil {
		return project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "Git对象或源码副本未完整读取写入")
	}
	ending, err := objects.output.ReadByte()
	if err != nil || ending != '\n' {
		return project.Failure("SOURCE_MATERIAL_INVALID", "Git对象边界无效")
	}
	return nil
}

// finish 失败时先终止读取进程；成功时确认Git正常退出后才允许使用导出证据。
func (objects *objectReader) finish(success bool) error {
	if !success {
		objects.cancel()
	}
	err := objects.input.Close()
	wait := objects.command.Wait()
	objects.cancel()
	if success && (err != nil || wait != nil) {
		return project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "Git读取结束未确认，导出不能作为候选")
	}
	return nil
}
