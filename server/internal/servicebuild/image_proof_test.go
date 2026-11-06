// 本文件以真实Buildx两种输出形态复现摘要不能猜测或补齐的问题。
package servicebuild

import (
	"encoding/json"
	"testing"
)

// TestObservedDescriptorWithoutConfig 接受Linux实际返回的描述符，不伪造缺失的配置摘要。
func TestObservedDescriptorWithoutConfig(t *testing.T) {
	digest := ContentDigest([]byte("manifest"))
	data := []byte(`{"containerimage.digest":"` + digest + `","containerimage.descriptor":{"digest":"` + digest + `","mediaType":"application/vnd.oci.image.manifest.v1+json"}}`)
	image, err := imageMetadata(data)
	if err != nil || image.ExportDigest != digest || image.ManifestDigest != digest || image.ConfigDigest != "" || imageIDKind(digest, image) != "manifest" {
		t.Fatal("真实描述符被拒绝或缺失配置摘要被伪造", err)
	}
}

// TestObservedClassicConfigOnly 不把Mac实际返回的同值配置摘要冒称为独立manifest。
func TestObservedClassicConfigOnly(t *testing.T) {
	digest := ContentDigest([]byte("config"))
	data := []byte(`{"containerimage.digest":"` + digest + `","containerimage.config.digest":"` + digest + `"}`)
	image, err := imageMetadata(data)
	if err != nil || image.ExportDigest != digest || image.ManifestDigest != "" || image.ManifestMediaType != "" || image.ConfigDigest != digest || imageIDKind(digest, image) != "config" {
		t.Fatal("classic配置摘要被冒称为独立manifest", err)
	}
}

// TestImageEvidenceVariants 覆盖各受支持描述符与实际本地ID，并拒绝缺失、未知和矛盾证明。
func TestImageEvidenceVariants(t *testing.T) {
	manifest, config := ContentDigest([]byte("manifest")), ContentDigest([]byte("config"))
	for _, mediaType := range []string{"application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json", "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"} {
		for _, configPresent := range []bool{false, true} {
			assertImageVariant(t, manifest, config, mediaType, configPresent)
		}
	}
}

// assertImageVariant 核实一种真实工具证据组合，分别断言描述符、配置与未知ID。
func assertImageVariant(t *testing.T, manifest, config, mediaType string, configPresent bool) {
	t.Helper()
	value := map[string]any{"containerimage.digest": manifest, "containerimage.descriptor": map[string]any{"digest": manifest, "mediaType": mediaType}}
	if configPresent {
		value["containerimage.config.digest"] = config
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	image, err := imageMetadata(data)
	if err != nil || imageIDKind(manifest, image) != descriptorKind(mediaType) || imageIDKind(ContentDigest(nil), image) != "" {
		t.Fatal("合法描述符被拒绝或第三方ID被接受", err)
	}
	if configPresent && imageIDKind(config, image) != "config" {
		t.Fatal("实际配置身份未绑定描述符证明")
	}
}

// TestImageProofContradictions 持久镜像证明不能接受矛盾的身份、导出摘要或描述符。
func TestImageProofContradictions(t *testing.T) {
	manifest, config := ContentDigest([]byte("manifest")), ContentDigest([]byte("config"))
	valid := Image{ID: config, IDKind: "config", ConfigDigest: config, ExportDigest: config}
	changes := []func(*Image){
		func(i *Image) { i.IDKind = "manifest" }, func(i *Image) { i.ExportDigest = manifest },
		func(i *Image) { i.ConfigDigest = "" }, func(i *Image) { i.ManifestMediaType = "unknown" },
		func(i *Image) {
			i.ManifestDigest, i.ManifestMediaType = config, "application/vnd.oci.image.manifest.v1+json"
		},
	}
	for _, change := range changes {
		image := valid
		change(&image)
		if boundImageID(image.ID, image) {
			t.Fatal("矛盾或伪造的候选证明被接受")
		}
	}
}

// TestMetadataConflictsRejected 真实描述符仍须与导出摘要一致，未知类型或显式空字段不降级。
func TestMetadataConflictsRejected(t *testing.T) {
	digest := ContentDigest([]byte("manifest"))
	config := ContentDigest([]byte("config"))
	for _, raw := range []string{
		`{"containerimage.digest":"` + digest + `"}`,
		`{"containerimage.digest":"` + digest + `","containerimage.config.digest":"` + config + `"}`,
		`{"containerimage.digest":"` + digest + `","containerimage.descriptor":{"digest":"` + config + `","mediaType":"application/vnd.oci.image.manifest.v1+json"}}`,
		`{"containerimage.digest":"` + digest + `","containerimage.descriptor":{"digest":"` + digest + `","mediaType":"unknown"}}`,
		`{"containerimage.digest":"` + digest + `","containerimage.config.digest":null}`,
		`{"containerimage.digest":"` + config + `","containerimage.config.digest":"` + config + `","containerimage.descriptor":null}`,
	} {
		if _, err := imageMetadata([]byte(raw)); err == nil {
			t.Fatal("缺失或冲突的镜像证明被接受", raw)
		}
	}
}
