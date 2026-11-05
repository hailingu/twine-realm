// 本文件只向显式全新目录导出已核实模板，不覆盖或初始化既有仓库。
package engineering

import (
	"errors"
	"os"
	"path"
	"path/filepath"
)

// Export 保留失败时的部分新目录供用户核实，绝不删除并发加入的用户文件。
func (catalog *Catalog) Export(directory string) (value Validation, err error) {
	if !filepath.IsAbs(directory) {
		return value, failure("ENGINEERING_TARGET_INVALID", "导出目标须为明确的全新绝对目录")
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return value, failure("ENGINEERING_TARGET_EXISTS", "导出目标已存在或不可创建，未覆盖原内容")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return value, failure("ENGINEERING_TARGET_UNAVAILABLE", "新建导出目录不可访问，请核实部分结果")
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for _, file := range catalog.release.Files {
		if err := exportFile(root, file); err != nil {
			return value, err
		}
	}
	return catalog.Validate(directory)
}

// exportFile 用相对于新目录的操作及 O_EXCL 写入，不跟随外部替换路径。
func exportFile(root *os.Root, descriptor releaseFile) (err error) {
	data, err := assets.ReadFile(bundleRoot + "/_template/" + descriptor.Source)
	if err != nil || digest(data) != descriptor.Digest {
		return failure("ENGINEERING_BUNDLE_INVALID", "模板发布材料已改变")
	}
	if err := root.MkdirAll(path.Dir(descriptor.Path), 0755); err != nil {
		return failure("ENGINEERING_EXPORT_FAILED", "模板目录创建失败，请核实部分结果")
	}
	file, err := root.OpenFile(descriptor.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return failure("ENGINEERING_EXPORT_FAILED", "模板文件创建失败，未覆盖既有内容")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return failure("ENGINEERING_EXPORT_FAILED", "模板文件写入失败，请核实部分结果")
	}
	return nil
}
