package winutil

import (
	"bytes"
	"strings"
	"unicode/utf16"
)

// DecodeConsoleOutput 把控制台程序的原始输出解码为 Go 字符串。
//
// wsl.exe 自身的输出（如 -l -q 列表）是 UTF-16LE，直接按字节转字符串会得到夹杂 NUL 的乱码；
// netstat、tasklist 等则是当前控制台码页，其中我们关心的端口、PID、地址均为 ASCII，可直接透传。
// 因此这里只做 UTF-16LE 的识别与转换，不引入额外的码页转换依赖。
//
// [参数] raw: 命令输出的原始字节
// [返回] 去除 BOM 与 CR 后的文本
// 最近修改时间: 2026-09-12
func DecodeConsoleOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	// 1. 识别 UTF-16LE：显式 BOM，或奇数字节位存在大量 NUL（ASCII 文本在 UTF-16LE 下的典型特征）
	if isUTF16LE(raw) {
		raw = bytes.TrimPrefix(raw, []byte{0xFF, 0xFE})
		units := make([]uint16, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		return normalize(string(utf16.Decode(units)))
	}

	return normalize(string(raw))
}

// isUTF16LE 判断原始字节是否为 UTF-16LE 编码。
//
// [参数] raw: 命令输出的原始字节
// [返回] true 表示应按 UTF-16LE 解码
// 最近修改时间: 2026-09-12
func isUTF16LE(raw []byte) bool {
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		return true
	}
	if len(raw) < 4 || len(raw)%2 != 0 {
		return false
	}

	// 只抽样前若干字节，避免大输出时做全量扫描
	sample := raw
	if len(sample) > 512 {
		sample = sample[:512]
	}

	var nulAtOdd int
	for i := 1; i < len(sample); i += 2 {
		if sample[i] == 0x00 {
			nulAtOdd++
		}
	}
	// 半数以上奇数位为 NUL 才判定为 UTF-16LE，避免误伤含少量二进制的普通输出
	return nulAtOdd*2 > len(sample)/2
}

// normalize 统一换行并去掉首尾空白，便于上层按行解析。
//
// [参数] s: 已解码的文本
// [返回] 规整后的文本
// 最近修改时间: 2026-09-12
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.TrimSpace(s)
}
