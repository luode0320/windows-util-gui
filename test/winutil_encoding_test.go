package test

import (
	"testing"
	"unicode/utf16"

	"windows-util-gui/internal/winutil"
)

// encodeUTF16LE 把字符串编码为 UTF-16LE 字节序列，用于模拟 wsl.exe 的原始输出。
//
// [参数] s: 源字符串；withBOM: 是否带 BOM 前缀
// [返回] UTF-16LE 字节序列
// 最近修改时间: 2026-09-12
func encodeUTF16LE(s string, withBOM bool) []byte {
	var raw []byte
	if withBOM {
		raw = append(raw, 0xFF, 0xFE)
	}
	for _, unit := range utf16.Encode([]rune(s)) {
		raw = append(raw, byte(unit), byte(unit>>8))
	}
	return raw
}

// TestDecodeConsoleOutput 覆盖控制台输出解码的三类输入：UTF-16LE 带 BOM、UTF-16LE 无 BOM、普通 ASCII。
//
// wsl.exe 列举发行版时输出 UTF-16LE 且不一定带 BOM，误判会直接导致 WSL 侧查询整体失效，
// 因此三种形态都必须能还原为同一份文本。
//
// 最近修改时间: 2026-09-12
func TestDecodeConsoleOutput(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "UTF-16LE 带 BOM",
			raw:  encodeUTF16LE("Ubuntu-24.04\r\ndocker-desktop\r\n", true),
			want: "Ubuntu-24.04\ndocker-desktop",
		},
		{
			name: "UTF-16LE 无 BOM",
			raw:  encodeUTF16LE("Ubuntu-24.04\r\n", false),
			want: "Ubuntu-24.04",
		},
		{
			name: "普通 ASCII 输出原样透传",
			raw:  []byte("  TCP    0.0.0.0:8080    0.0.0.0:0    LISTENING    1234\r\n"),
			want: "TCP    0.0.0.0:8080    0.0.0.0:0    LISTENING    1234",
		},
		{
			name: "空输入",
			raw:  nil,
			want: "",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := winutil.DecodeConsoleOutput(item.raw); got != item.want {
				t.Fatalf("解码结果不符\n期望: %q\n实际: %q", item.want, got)
			}
		})
	}
}
