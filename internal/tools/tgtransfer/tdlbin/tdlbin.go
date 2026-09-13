// Package tdlbin 提供 tdl.exe 嵌入字节的受控读取入口。
package tdlbin

import (
	_ "embed"
	"os"
	"path/filepath"
)

// EmbeddedTdl 是编译期嵌入的 tdl.exe 原始字节（随主程序构建自动更新）。
//
//go:embed tdl.exe
var EmbeddedTdl []byte

// ReadEmbedded 优先返回嵌入字节；嵌入包为空时（测试环境裁剪）回退读取源文件。
//
// [返回] 嵌入的 tdl.exe 字节；两者都不可用时返回 error
// 最近修改时间: 2026-09-13
func ReadEmbedded() ([]byte, error) {
	if len(EmbeddedTdl) > 0 {
		return EmbeddedTdl, nil
	}
	// go test 产物位于临时目录，嵌入数据仍会链接进来；此回退仅防御异常构建
	return os.ReadFile(filepath.Join("tdlbin", "tdl.exe"))
}
