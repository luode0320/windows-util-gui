package prockill

import (
	"fmt"
	"strings"
)

// protectedWindowsNames 是结束后会直接导致 Windows 崩溃或强制重启的核心进程。
//
// 这些进程是操作系统自身的骨架，几乎不可能是用户的清理目标，
// 而按进程名模糊搜索配合"结束全部"极易把它们一并选中，因此直接拒绝结束。
// 注意不要把 svchost.exe 放进来：它承载大量普通服务，确实存在需要结束的正当场景。
var protectedWindowsNames = map[string]bool{
	"system idle process": true,
	"system":              true,
	"registry":            true,
	"smss.exe":            true,
	"csrss.exe":           true,
	"wininit.exe":         true,
	"winlogon.exe":        true,
	"services.exe":        true,
	"lsass.exe":           true,
}

// IsProtected 判断进程是否属于禁止结束的系统核心进程。
//
// [参数] process: 待判断的进程
// [返回] 受保护时返回拒绝原因与 true
// 最近修改时间: 2026-09-12
func IsProtected(process Process) (string, bool) {
	if process.Source == SourceWSL {
		// WSL 发行版内的 1 号进程是 init/systemd，结束它等于关掉整个发行版
		if process.PID == 1 {
			return fmt.Sprintf("%s 的 1 号进程（%s）是发行版的 init，结束它会直接关闭整个发行版，已拒绝。",
				process.Distro, process.Name), true
		}
		return "", false
	}

	if protectedWindowsNames[strings.ToLower(process.Name)] {
		return fmt.Sprintf("%s 是 Windows 核心进程，结束它会导致系统崩溃或强制重启，已拒绝。", process.Name), true
	}
	return "", false
}
