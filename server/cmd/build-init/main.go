// 本机构建初始化仅核实工具环境并建立全新私有根，不运行服务或自动拉取镜像。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hailingu/twine-realm/server/internal/privatefiles"
	"github.com/hailingu/twine-realm/server/internal/servicebuild"
)

// main 所有错误保持非零，部分新目录保留以便核实。
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 在工具、范围与全新路径明确后初始化，输出只含非敏感安装身份。
func run(ctx context.Context, args []string, output io.Writer) (err error) {
	flags := flag.NewFlagSet("build-init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "全新私有构建根")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !filepath.IsAbs(*directory) {
		return fmt.Errorf("用法：build-init --directory /绝对路径/全新私有构建目录")
	}
	var roots []string
	if json.Unmarshal([]byte(os.Getenv("TWINE_REPOSITORY_ROOTS")), &roots) != nil || len(roots) == 0 {
		return fmt.Errorf("请先配置非空TWINE_REPOSITORY_ROOTS数组")
	}
	forbidden, err := privatefiles.LocalForbidden(roots, os.Getenv("TWINE_WEB_DIRECTORY"))
	if err != nil {
		return err
	}
	if runtime := os.Getenv("TWINE_RUNTIME_DIRECTORY"); runtime != "" {
		forbidden = append(forbidden, runtime)
	}
	environment, err := servicebuild.DiscoverEnvironment(ctx)
	if err != nil {
		return err
	}
	if err := servicebuild.InitializeDirectory(*directory, forbidden, environment); err != nil {
		return err
	}
	driver, err := servicebuild.OpenDriver(*directory, forbidden)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, driver.Close()) }()
	return json.NewEncoder(output).Encode(struct {
		SchemaVersion  int                      `json:"schemaVersion"`
		InstallationID string                   `json:"installationId"`
		Adapter        string                   `json:"adapter"`
		Environment    servicebuild.Environment `json:"environment"`
	}{1, driver.InstallationID(), "local-go-container-v1", environment})
}
