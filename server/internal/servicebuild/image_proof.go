// 本文件限定真实本地内容证明的有限组合，不把导出摘要猜测成不存在的manifest或配置。
package servicebuild

import (
	"encoding/json"
	"strings"
)

// descriptorKind 仅识别OCI与Docker发布的镜像和索引描述符类型。
func descriptorKind(mediaType string) string {
	switch mediaType {
	case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
		return "manifest"
	case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
		return "index"
	default:
		return ""
	}
}

// validImageEvidence 配置单证据须与导出摘要相同；描述符证据须有已知类型且不能伪造同值配置。
func validImageEvidence(image Image) bool {
	if !digestPattern.MatchString(image.ExportDigest) || image.ConfigDigest != "" && !digestPattern.MatchString(image.ConfigDigest) {
		return false
	}
	if image.ManifestDigest == "" {
		return image.ManifestMediaType == "" && image.ConfigDigest == image.ExportDigest
	}
	return digestPattern.MatchString(image.ManifestDigest) && descriptorKind(image.ManifestMediaType) != "" && image.ExportDigest == image.ManifestDigest && image.ConfigDigest != image.ManifestDigest
}

// imageIDKind 只把实际本地ID绑定到本次工具已提供的具体内容摘要。
func imageIDKind(id string, image Image) string {
	if !digestPattern.MatchString(id) || !validImageEvidence(image) {
		return ""
	}
	if id == image.ConfigDigest {
		return "config"
	}
	if id == image.ManifestDigest {
		return descriptorKind(image.ManifestMediaType)
	}
	return ""
}

// boundImageID 持久候选的类型必须与已核实的实际本地ID及内容证据完全相符。
func boundImageID(id string, image Image) bool {
	kind := imageIDKind(id, image)
	return kind != "" && image.IDKind == kind
}

// imageMetadata 只保存工具实际给出的内容摘要；任何非空Buildx告警均拒绝。
func imageMetadata(data []byte) (Image, error) {
	var value Image
	var metadata map[string]json.RawMessage
	if json.Unmarshal(data, &metadata) != nil {
		return value, invalidOutput()
	}
	warnings := strings.TrimSpace(string(metadata["buildx.build.warnings"]))
	if warnings != "" && warnings != "[]" && warnings != "{}" && warnings != "null" {
		return value, failure("BUILD_IMAGE_WARNING", "Buildx报告告警，候选不能通过")
	}
	if json.Unmarshal(metadata["containerimage.digest"], &value.ExportDigest) != nil {
		return value, invalidOutput()
	}
	if raw, exists := metadata["containerimage.config.digest"]; exists {
		if json.Unmarshal(raw, &value.ConfigDigest) != nil || !digestPattern.MatchString(value.ConfigDigest) {
			return value, invalidOutput()
		}
	}
	if raw, exists := metadata["containerimage.descriptor"]; exists {
		var descriptor struct {
			Digest    string `json:"digest"`
			MediaType string `json:"mediaType"`
		}
		if json.Unmarshal(raw, &descriptor) != nil || !digestPattern.MatchString(descriptor.Digest) || descriptorKind(descriptor.MediaType) == "" {
			return value, invalidOutput()
		}
		value.ManifestDigest, value.ManifestMediaType = descriptor.Digest, descriptor.MediaType
	}
	if !validImageEvidence(value) {
		return value, invalidOutput()
	}
	return value, nil
}
