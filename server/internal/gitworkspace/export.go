// 本文件从完整commit的Git对象导出普通文件，不执行filter、hook或archive替换。
package gitworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hailingu/twine-realm/server/internal/project"
)

// treeFile 是有界ls-tree协议，不把其名字或内容拼接成可执行命令。
type treeFile struct {
	name, object string
	size         int64
	executable   bool
}

// ExportCommit 只写调用方拥有的全新私有目录，拒绝任何仓库范围内的目标。
func (inspector *Inspector) ExportCommit(ctx context.Context, repository, commit, destination string) (material project.SourceMaterial, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := inspector.VerifyCommit(ctx, repository, commit); err != nil {
		return material, err
	}
	files, tree, err := inspector.exportFiles(ctx, repository, commit)
	if err != nil {
		return material, err
	}
	gitVersion, err := inspector.command(ctx, repository, "version")
	if err != nil || !strings.HasPrefix(gitVersion, "git version ") || len(gitVersion) > 128 || strings.ContainsAny(gitVersion, "\r\n") {
		return material, project.Failure("SOURCE_TOOL_UNAVAILABLE", "Git对象导出工具版本未确认")
	}
	root, err := inspector.exportDirectory(destination)
	if err != nil {
		return material, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	objects, err := inspector.openObjects(ctx, repository)
	if err != nil {
		return material, err
	}
	defer func() { err = errors.Join(err, objects.finish(err == nil)) }()
	material = project.SourceMaterial{Commit: commit, Tree: tree, Files: []project.SourceFile{}, GitVersion: gitVersion}
	for _, file := range files {
		value, err := exportFile(root, objects, file)
		if err != nil {
			return project.SourceMaterial{}, err
		}
		material.Files = append(material.Files, value)
	}
	data, err := json.Marshal(material.Files)
	if err != nil {
		return project.SourceMaterial{}, err
	}
	hash := sha256.Sum256(data)
	material.Digest = "sha256:" + hex.EncodeToString(hash[:])
	return material, nil
}

// exportFiles 先有界核实整个清单，子模块、链接、控制字符和超限不能产生执行材料。
func (inspector *Inspector) exportFiles(ctx context.Context, repository, commit string) ([]treeFile, string, error) {
	tree, err := inspector.command(ctx, repository, "rev-parse", commit+"^{tree}")
	if err != nil || !commitPattern.MatchString(tree) {
		return nil, "", project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "固定源码树不可读取")
	}
	command := inspector.newCommand(ctx, repository, "ls-tree", "-r", "-z", "-l", "--full-tree", commit)
	output := gitOutput{limit: 8 * 1024 * 1024}
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return nil, "", project.Failure("SOURCE_MATERIAL_UNAVAILABLE", "固定源码清单不可读取或超限")
	}
	files, err := parseTree(output.buffer.String())
	return files, tree, err
}

// parseTree 校验原始Git对象清单，不从工作区或gitattributes推断导出内容。
func parseTree(data string) ([]treeFile, error) {
	files, bytes := []treeFile{}, int64(0)
	for _, row := range strings.Split(data, "\x00") {
		if row == "" {
			continue
		}
		file, err := treeEntry(row)
		if err != nil {
			return nil, err
		}
		if file.size > project.SourceExportBytes-bytes || len(files) >= project.SourceExportFiles {
			return nil, project.Failure("SOURCE_LIMIT", "固定源码超出首版构建的文件或字节上限")
		}
		bytes += file.size
		files = append(files, file)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].name < files[b].name })
	return files, nil
}

// treeEntry 限制Git返回的单项路径、对象种类与大小，外部对象不变为文件系统指令。
func treeEntry(row string) (treeFile, error) {
	metadata, name, found := strings.Cut(row, "\t")
	fields := strings.Fields(metadata)
	if !found || len(fields) != 4 || !sourcePath(name) || !commitPattern.MatchString(fields[2]) {
		return treeFile{}, project.Failure("SOURCE_MATERIAL_INVALID", "固定源码包含无效或不受支持的路径")
	}
	if fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
		return treeFile{}, project.Failure("SOURCE_KIND_UNSUPPORTED", "首版构建不接受链接、子模块或特殊对象")
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 0 {
		return treeFile{}, project.Failure("SOURCE_MATERIAL_INVALID", "固定源码对象大小无效")
	}
	return treeFile{name, fields[2], size, fields[0] == "100755"}, nil
}

// sourcePath 只接受可移植普通相对路径，拒绝.git和会改变工具协议的控制字符。
func sourcePath(name string) bool {
	if !utf8.ValidString(name) || !fs.ValidPath(name) || len(name) > 512 || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

// exportDirectory 不修改任何原服务仓库，也不覆盖既有或部分导出现场。
func (inspector *Inspector) exportDirectory(destination string) (*os.Root, error) {
	if !filepath.IsAbs(destination) {
		return nil, project.Failure("SOURCE_EXPORT_BOUNDARY", "源码导出须使用明确的全新私有目录")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil || inspector.allowed(parent) {
		return nil, project.Failure("SOURCE_EXPORT_BOUNDARY", "源码副本必须在原仓库允许范围之外")
	}
	if err := os.Mkdir(filepath.Join(parent, filepath.Base(destination)), 0700); err != nil {
		return nil, project.Failure("SOURCE_EXPORT_EXISTS", "源码导出目标已存在或不可创建，原内容保留")
	}
	root, err := os.OpenRoot(filepath.Join(parent, filepath.Base(destination)))
	if err != nil {
		return nil, project.Failure("SOURCE_EXPORT_UNAVAILABLE", "新源码副本不可访问，保留部分现场")
	}
	return root, nil
}

// exportFile 独占写入原始字节并记录SHA256，不应用Git过滤器或内容替换。
func exportFile(root *os.Root, objects *objectReader, source treeFile) (value project.SourceFile, err error) {
	if err := root.MkdirAll(path.Dir(source.name), 0755); err != nil {
		return value, project.Failure("SOURCE_EXPORT_UNAVAILABLE", "源码副本目录无法创建")
	}
	mode := fs.FileMode(0644)
	if source.executable {
		mode = 0755
	}
	file, err := root.OpenFile(source.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return value, project.Failure("SOURCE_EXPORT_UNAVAILABLE", "源码副本无法独占写入，原内容保留")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	if err := objects.copyBlob(source.object, source.size, io.MultiWriter(file, hash)); err != nil {
		return value, err
	}
	if err := file.Sync(); err != nil {
		return value, project.Failure("SOURCE_EXPORT_UNAVAILABLE", "源码副本同步失败，保留现场")
	}
	return project.SourceFile{Path: source.name, Size: source.size, Executable: source.executable, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}
