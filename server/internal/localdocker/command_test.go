// 本文件验证共享命令端口的期限、输出上限和告警识别，不接触Docker资源。
package localdocker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBoundedOutputKeepsFailure 排空超限数据但保留有界内存与失败标记。
func TestBoundedOutputKeepsFailure(t *testing.T) {
	var output boundedBuffer
	if _, err := output.Write(make([]byte, 1024*1024+1)); err != nil || !output.exceeded || output.data.Len() != 1024*1024 {
		t.Fatal("输出边界未生效")
	}
}

// TestExplicitTimeoutPolicy 不允许无期限或突破最大期限的外部命令。
func TestExplicitTimeoutPolicy(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, 11 * time.Minute} {
		_, err := Run(context.Background(), Policy{Timeout: duration}, "--version")
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "DOCKER_POLICY_INVALID" {
			t.Fatal("无效期限触发了外部执行或未返回稳定诊断")
		}
	}
}

// TestWarningsStayFailures Docker、Compose与Buildx标准告警均不得静默视为成功。
func TestWarningsStayFailures(t *testing.T) {
	for _, value := range []string{"WARNING: controlled", "WARN: controlled", "WARN[0000] controlled", "level=warning", "level=warn"} {
		if !hasWarning(value) {
			t.Fatal("标准工具告警未被识别")
		}
	}
	if hasWarning("ordinary progress") {
		t.Fatal("普通进度被误报为告警")
	}
}
