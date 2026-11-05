// 工程包命令核实受控规范或导出全新模板，不执行服务部署或 AI 开发。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/hailingu/twine-realm/server/internal/engineering"
)

// main 将每次失败输出为机器可读结果；不回显外部文件或秘密。
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		failure := &engineering.Error{Code: "ENGINEERING_COMMAND_FAILED", Message: "工程包命令未完成"}
		var known *engineering.Error
		if errors.As(err, &known) {
			failure = known
		}
		if encodeErr := json.NewEncoder(os.Stderr).Encode(map[string]any{"error": failure}); encodeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// run 的命令集合固定，目录仅为显式参数，不接受文档中的任意执行要求。
func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return &engineering.Error{Code: "ENGINEERING_USAGE", Message: "命令须为 inspect、export 或 validate"}
	}
	catalog, err := engineering.Load()
	if err != nil {
		return err
	}
	if args[0] == "inspect" && len(args) == 1 {
		return json.NewEncoder(output).Encode(map[string]any{"identity": catalog.Identity(), "profile": catalog.Profile()})
	}
	if args[0] != "export" && args[0] != "validate" {
		return &engineering.Error{Code: "ENGINEERING_USAGE", Message: "工程包命令或参数无效"}
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "服务绝对目录")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *directory == "" {
		return &engineering.Error{Code: "ENGINEERING_USAGE", Message: "须提供明确的 --directory 且没有其他参数"}
	}
	var result engineering.Validation
	if args[0] == "export" {
		result, err = catalog.Export(*directory)
	} else {
		result, err = catalog.Validate(*directory)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
