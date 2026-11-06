// 本文件验证输出冻结与镜像加载的拒绝边界，不能以pause返回成功或可变标签代替内容证明。
package servicebuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// prepareFrozenFailure 只为负例准备私有目录和受控暂停结果，不执行用户二进制。
func prepareFrozenFailure(t *testing.T, boundary *recoveryBoundary, mode string) string {
	t.Helper()
	boundary.container.NetworkSettings.Networks = map[string]any{}
	writeRecoveryProof(t, boundary, nil)
	id := boundary.container.ID
	switch mode {
	case "inspect-error":
		boundary.fail = "container inspect"
	case "missing":
		boundary.present = false
	case "foreign-id":
		id = strings.Repeat("b", 64)
	case "network-present":
		boundary.container.NetworkSettings.Networks = map[string]any{"bridge": true}
	case "existing-directory":
		if err := boundary.driver.files.Mkdir("operations/" + boundary.box.operationID + "/image"); err != nil {
			t.Fatal(err)
		}
	case "cp-failed":
		boundary.volume = &outputVolume{Name: boundary.box.name + "-out", Driver: "local", CreatedAt: "2026-11-06T00:00:00Z", Labels: boundary.box.labels(), Options: boundary.box.volumeOptions()}
	}
	boundary.driver.execute = func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "container pause") {
			boundary.mutations = append(boundary.mutations, "pause")
			if mode == "pause-error" {
				return nil, errors.New("受控暂停失败")
			}
			boundary.container.State.Paused = mode != "pause-unconfirmed"
			return nil, nil
		}
		return boundary.execute(ctx, duration, metadata, args...)
	}
	return id
}

// TestUnconfirmedFreezeCannotProduceImage 每个失败边界都禁止继续发布二进制或镜像证明。
func TestUnconfirmedFreezeCannotProduceImage(t *testing.T) {
	for _, mode := range []string{"inspect-error", "missing", "foreign-id", "network-present", "pause-error", "pause-unconfirmed", "existing-directory", "output-volume-missing", "cp-failed"} {
		t.Run(mode, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			id := prepareFrozenFailure(t, boundary, mode)
			image, err := boundary.driver.frozenBinary(context.Background(), boundary.box, id)
			if err == nil || image.BinaryDigest != "" || image.ID != "" || image.ManifestDigest != "" {
				t.Fatal("未确认冻结仍产生了镜像候选证据")
			}
			for _, command := range boundary.mutations {
				if command != "pause" {
					t.Fatal("负例边界之后执行了额外资源操作")
				}
			}
		})
	}
}

// loadedImageJSON 保留真实inspect字段形态，每个负例仅改变明确的镜像身份或启动约束。
func loadedImageJSON(t *testing.T, boundary *recoveryBoundary, digest, mode string) []byte {
	t.Helper()
	config := map[string]any{"User": "65532:65532", "Labels": boundary.box.labels(), "Entrypoint": []string{"/service"}, "Cmd": []string{"serve"}}
	image := map[string]any{"ID": digest, "Os": "linux", "Architecture": boundary.box.target.Environment.Architecture, "Config": config}
	switch mode {
	case "id":
		image["ID"] = ContentDigest(nil)
	case "os":
		image["Os"] = "unsupported"
	case "architecture":
		image["Architecture"] = "unsupported"
	case "user":
		config["User"] = "0"
	case "labels":
		config["Labels"] = map[string]string{"io.twine-realm.build.installation": "foreign"}
	case "entrypoint":
		config["Entrypoint"] = []string{"/unverified"}
	case "command":
		config["Cmd"] = []string{"migrate"}
	case "broken":
		return []byte("{}")
	}
	data, err := json.Marshal([]map[string]any{image})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestLoadedImageRequiresFullIdentity 同名tag、错误架构或越权启动配置不能冒充本次不可变候选。
func TestLoadedImageRequiresFullIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "id", "os", "architecture", "user", "labels", "entrypoint", "command", "broken"} {
		t.Run(mode, func(t *testing.T) {
			boundary := newRecoveryBoundary(t)
			digest := ContentDigest([]byte("loaded-config"))
			data := loadedImageJSON(t, boundary, digest, mode)
			boundary.driver.execute = func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
				if strings.Join(args, " ") == "--context local-test image inspect test-candidate:local" {
					return data, nil
				}
				return boundary.execute(ctx, duration, metadata, args...)
			}
			err := boundary.driver.verifyImage(context.Background(), boundary.box, Image{ID: digest, IDKind: "config", ExportDigest: digest, ConfigDigest: digest, Tag: "test-candidate:local"})
			if (mode == "valid") != (err == nil) {
				t.Fatal("已加载镜像身份核实错误", err)
			}
			if len(boundary.mutations) != 0 {
				t.Fatal("核实镜像意外改变了资源")
			}
		})
	}
}
