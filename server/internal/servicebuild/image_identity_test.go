// 本文件复现containerd的本地ID为manifest的真实协议形态，config与manifest摘要仍必须独立可查。
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
	if err := ValidateCompletion(lease, complete); err != nil {
		t.Fatal("完整manifest本地ID候选被拒绝", err)
	}
	complete.Candidate.Image.ID = ContentDigest([]byte("foreign-image"))
	if err := ValidateCompletion(lease, complete); err == nil {
		t.Fatal("第三摘要被当成本次候选")
	}
}

// TestEmptyMetadataWarningObject 是Buildx文档定义的无告警对象形态，不把空对象误报为真实告警。
func TestEmptyMetadataWarningObject(t *testing.T) {
	manifest, config := ContentDigest([]byte("manifest")), ContentDigest([]byte("config"))
	data := []byte(`{"containerimage.digest":"` + manifest + `","containerimage.config.digest":"` + config + `","buildx.build.warnings":{}}`)
	actualManifest, actualConfig, err := imageMetadata(data)
	if err != nil || actualManifest != manifest || actualConfig != config {
		t.Fatal("无告警元数据被误判为失败", err)
	}
}
