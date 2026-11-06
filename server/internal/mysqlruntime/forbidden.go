// 本文件把平台仓库禁止范围交给共享私有目录边界，保留MySQL对外错误语义。
package mysqlruntime

import "github.com/hailingu/twine-realm/server/internal/privatefiles"

// LocalForbidden 合并仓库、平台源码及静态目录，不修改或创建这些位置。
func LocalForbidden(repositories []string, webDirectory string) ([]string, error) {
	values, err := privatefiles.LocalForbidden(repositories, webDirectory)
	return values, privateFileFailure(err)
}
