// 本文件验证默认关闭运行能力时不读取秘密或生成工作器。
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hailingu/twine-realm/server/internal/catalog"
	"github.com/hailingu/twine-realm/server/internal/config"
)

// TestDisabledRuntimeDoesNotCreateWorker 未启用执行器仍能发布能力说明，无需打开秘密目录或绑定安装。
func TestDisabledRuntimeDoesNotCreateWorker(t *testing.T) {
	definitions, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	value, err := configureRuntime(context.Background(), config.Config{}, nil, definitions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || value.driver != nil || value.worker != nil || value.service == nil {
		t.Fatal("未配置运行能力仍产生外部所有者")
	}
	if capability := value.service.Capability(); capability.Enabled || capability.Code != "RUNTIME_DISABLED" {
		t.Fatal("未配置运行能力被宣称已启用")
	}
	if err := value.close(); err != nil {
		t.Fatal("未启用能力仍要求秘密目录清理")
	}
}
