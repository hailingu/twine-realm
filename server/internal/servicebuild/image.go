// 本文件只把核实的静态二进制放入平台固定scratch镜像，不执行服务Dockerfile或发布镜像。
package servicebuild

import (
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

// fixedDockerfile 的整个构建上下文仅包含本文件与已冻结二进制。
const fixedDockerfile = "FROM scratch\nCOPY --chmod=0555 service /service\nUSER 65532:65532\nENTRYPOINT [\"/service\"]\nCMD [\"serve\"]\n"

// buildImage 先暂停全部沙箱进程，再复制核实输出，避免后台进程继续改写构建材料。
func (driver *Driver) buildImage(ctx context.Context, box sandbox, id string) (value Image, err error) {
	value, err = driver.frozenBinary(ctx, box, id)
	if err != nil {
		return value, err
	}
	value.Tag = box.name + "-candidate:local"
	args := []string{dockerContextOption, driver.Environment().Context, "buildx", "build", "--builder", box.target.Environment.BuilderName, "--network", "none", "--pull=false", "--no-cache", "--load", "--provenance=false", "--platform", "linux/" + box.target.Environment.Architecture, "--metadata-file", filepath.Join(box.directory, "image-metadata.json"), "--tag", value.Tag}
	for _, key := range sortedKeys(box.labels()) {
		args = append(args, "--label", key+"="+box.labels()[key])
	}
	args = append(args, "--file", filepath.Join(box.directory, "image", "Dockerfile"), filepath.Join(box.directory, "image"))
	if err := driver.verifyBoundary(ctx); err != nil {
		return value, err
	}
	if _, err := driver.execute(ctx, 180*time.Second, true, args...); err != nil {
		return value, err
	}
	metadata, err := producedFile(filepath.Join(box.directory, "image-metadata.json"), 65536)
	if err != nil {
		return value, err
	}
	value.ManifestDigest, value.ConfigDigest, err = imageMetadata(metadata)
	if err != nil {
		return value, err
	}
	value.ID, err = driver.loadedImageID(ctx, box, value)
	return value, err
}

// producedFile 只接收本进程新建私有目录内的普通有限文件，不跟随链接或设备。
func producedFile(path string, limit int64) (data []byte, err error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, failure("BUILD_ARTIFACT_INVALID", "构建输出不可安全读取")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, failure("BUILD_ARTIFACT_INVALID", "构建输出不是受限的普通文件")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		return nil, failure("BUILD_ARTIFACT_INVALID", "构建输出文件不属于当前用户")
	}
	if err := file.Chmod(0600); err != nil {
		return nil, err
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, failure("BUILD_ARTIFACT_INVALID", "构建输出读取超限或未完成")
	}
	return data, nil
}

// verifyBinary 核实真实ELF架构和无动态解释器，源码测试不提供可执行证明。
func verifyBinary(path, architecture string, limit int64) (digest string, err error) {
	data, err := producedFile(path, limit)
	if err != nil {
		return "", err
	}
	file, err := elf.Open(path)
	if err != nil {
		return "", failure("BUILD_BINARY_INVALID", "构建输出不是支持的Linux ELF二进制")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	expected := map[string]elf.Machine{"arm64": elf.EM_AARCH64, "amd64": elf.EM_X86_64}[architecture]
	if file.Machine != expected || file.Class != elf.ELFCLASS64 || file.Type != elf.ET_EXEC {
		return "", failure("BUILD_BINARY_INVALID", "构建二进制的架构或类型不一致")
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return "", failure("BUILD_BINARY_DYNAMIC", "首个scratch组合必须提供静态二进制")
		}
	}
	return ContentDigest(data), nil
}

// imageMetadata 分别保存真实manifest与config摘要；Buildx警告同样导致失败。
func imageMetadata(data []byte) (string, string, error) {
	var metadata map[string]json.RawMessage
	if json.Unmarshal(data, &metadata) != nil {
		return "", "", invalidOutput()
	}
	warnings := strings.TrimSpace(string(metadata["buildx.build.warnings"]))
	if warnings != "" && warnings != "[]" && warnings != "{}" && warnings != "null" {
		return "", "", failure("BUILD_IMAGE_WARNING", "Buildx报告告警，候选不能通过")
	}
	var manifest, config string
	if json.Unmarshal(metadata["containerimage.digest"], &manifest) != nil || json.Unmarshal(metadata["containerimage.config.digest"], &config) != nil || !digestPattern.MatchString(manifest) || !digestPattern.MatchString(config) {
		return "", "", invalidOutput()
	}
	return manifest, config, nil
}

// verifyImage 同时核实原记录的实际本地ID和内容证明，tag改变不能沿用旧候选。
func (driver *Driver) verifyImage(ctx context.Context, box sandbox, image Image) error {
	id, err := driver.loadedImageID(ctx, box, image)
	if err != nil {
		return err
	}
	if id != image.ID {
		return failure("BUILD_IMAGE_IDENTITY_CONFLICT", "实际本地镜像身份与原候选不一致")
	}
	return nil
}

// boundImageID 适配classic的config-ID与containerd的manifest/index-ID，只接受本次已检查的内容摘要。
func boundImageID(id string, image Image) bool {
	return digestPattern.MatchString(id) && (id == image.ConfigDigest || id == image.ManifestDigest)
}

// loadedImageID 在保存候选前实际读取本地ID，不能假设所有Docker存储后端都使用config digest。
func (driver *Driver) loadedImageID(ctx context.Context, box sandbox, image Image) (string, error) {
	data, err := driver.run(ctx, "image", "inspect", image.Tag)
	if err != nil {
		return "", err
	}
	var values []struct {
		ID, Os, Architecture string
		Config               struct {
			User            string
			Labels          map[string]string
			Entrypoint, Cmd []string
		}
	}
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return "", invalidOutput()
	}
	value := values[0]
	if !boundImageID(value.ID, image) || value.Os != "linux" || value.Architecture != box.target.Environment.Architecture || value.Config.User != "65532:65532" || !reflect.DeepEqual(value.Config.Labels, box.labels()) || !reflect.DeepEqual(value.Config.Entrypoint, []string{"/service"}) || !reflect.DeepEqual(value.Config.Cmd, []string{"serve"}) {
		return "", failure("BUILD_IMAGE_IDENTITY_CONFLICT", "已加载镜像与固定构建摘要或配置不一致")
	}
	return value.ID, nil
}

// frozenBinary 冻结全部原沙箱进程，并从本次限额卷提取核实的静态输出。
func (driver *Driver) frozenBinary(ctx context.Context, box sandbox, id string) (value Image, err error) {
	state, err := driver.inspectSandbox(ctx, box)
	if err != nil {
		return value, err
	}
	if state == nil || state.ID != id || !state.State.Running || len(state.NetworkSettings.Networks) != 0 {
		return value, ownershipConflict()
	}
	if _, err := driver.run(ctx, "container", "pause", id); err != nil {
		return value, err
	}
	state, err = driver.inspectSandbox(ctx, box)
	if err != nil {
		return value, err
	}
	if state == nil || state.ID != id || !state.State.Paused {
		return value, failure("BUILD_FREEZE_UNCONFIRMED", "尚未确认沙箱输出冻结，不能生成镜像")
	}
	name := "operations/" + box.operationID + "/image"
	if err := driver.files.Mkdir(name); err != nil {
		return value, err
	}
	binary := filepath.Join(box.directory, "image", "service")
	if volume, err := driver.inspectOutputVolume(ctx, box); err != nil || volume == nil {
		return value, errors.Join(failure("BUILD_OUTPUT_VOLUME_UNCONFIRMED", "输出卷身份尚未确认"), err)
	}
	if _, err := driver.run(ctx, "container", "cp", id+":/output/service", binary); err != nil {
		return value, err
	}
	value.BinaryDigest, err = verifyBinary(binary, box.target.Environment.Architecture, box.target.Policy.BinaryBytes)
	if err != nil {
		return value, err
	}
	if err := driver.files.WriteNew(name+"/Dockerfile", []byte(fixedDockerfile)); err != nil {
		return value, err
	}
	return value, nil
}
