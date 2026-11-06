// 本文件复现containerd的实际本地ID与工具内容证明绑定，不能以第三摘要替代。
package servicebuild

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestContainerdManifestIdentity 只接受已检查manifest的实际本地ID，不放宽到任意第三摘要。
func TestContainerdManifestIdentity(t *testing.T) {
	boundary := newRecoveryBoundary(t)
	_, complete := validMetadata(t)
	image := complete.Candidate.Image
	image.ID = image.ManifestDigest
	image.IDKind = "manifest"
	var rows []map[string]any
	if err := json.Unmarshal(loadedImageJSON(t, boundary, image.ConfigDigest, "valid"), &rows); err != nil {
		t.Fatal(err)
	}
	rows[0]["ID"] = image.ID
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	boundary.driver.execute = func(ctx context.Context, duration time.Duration, metadata bool, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "--context local-test image inspect "+image.Tag {
			return data, nil
		}
		return boundary.execute(ctx, duration, metadata, args...)
	}
	if err := boundary.driver.verifyImage(context.Background(), boundary.box, image); err != nil {
		t.Fatal("已检查的containerd manifest本地ID被误判为外来镜像", err)
	}
	image.ID = ContentDigest([]byte("foreign-image"))
	if err := boundary.driver.verifyImage(context.Background(), boundary.box, image); err == nil {
		t.Fatal("原记录与实际镜像不一致仍被接受")
	}
}

// TestCompletionKeepsActualManifestID 完整候选不能被错误重写为config-ID，第三摘要仍拒绝。
func TestCompletionKeepsActualManifestID(t *testing.T) {
	lease, complete := validMetadata(t)
	complete.Candidate.Image.ID = complete.Candidate.Image.ManifestDigest
	complete.Candidate.Image.IDKind = "manifest"
	if err := ValidateCompletion(lease, complete); err != nil {
		t.Fatal("完整manifest本地ID候选被拒绝", err)
	}
	complete.Candidate.Image.ID = ContentDigest([]byte("foreign-image"))
	if err := ValidateCompletion(lease, complete); err == nil {
		t.Fatal("第三摘要被当成本次候选")
	}
}

// TestCompletionWithObservedProofs 成功持久边界接受实际工具证据组合，缺失摘要在JSON中也不得出现。
func TestCompletionWithObservedProofs(t *testing.T) {
	for _, kind := range []string{"config", "manifest", "index"} {
		lease, complete := validMetadata(t)
		image := &complete.Candidate.Image
		image.IDKind = kind
		if kind == "config" {
			image.ExportDigest = image.ConfigDigest
			image.ManifestDigest, image.ManifestMediaType = "", ""
		} else {
			image.ID, image.ConfigDigest = image.ManifestDigest, ""
			if kind == "index" {
				image.ManifestMediaType = "application/vnd.oci.image.index.v1+json"
			}
		}
		if err := ValidateCompletion(lease, complete); err != nil {
			t.Fatal("实际证据组合不能持久化", kind, err)
		}
		data, err := json.Marshal(image)
		if err != nil || strings.Contains(string(data), `:""`) {
			t.Fatal("缺失摘要被填为空字符串", err)
		}
	}
}

// TestEmptyMetadataWarningObject 是Buildx文档定义的无告警对象形态，不把空对象误报为真实告警。
func TestEmptyMetadataWarningObject(t *testing.T) {
	config := ContentDigest([]byte("config"))
	data := []byte(`{"containerimage.digest":"` + config + `","containerimage.config.digest":"` + config + `","buildx.build.warnings":{}}`)
	image, err := imageMetadata(data)
	if err != nil || image.ManifestDigest != "" || image.ConfigDigest != config {
		t.Fatal("无告警元数据被误判为失败", err)
	}
}
