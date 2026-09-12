package winutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// NoElevateFlag 是跳过自动提权的命令行开关。
//
// manifest 里不写 requireAdministrator、改为运行时自提权，是为了让 exe 能被普通权限进程拉起
// （VS Code 调试器启动 requireAdministrator 程序会直接失败）。相应地，调试时需要这个开关
// 阻止程序把自己重新拉起为独立的提权进程，否则调试会话会立刻脱离。
const NoElevateFlag = "-no-elevate"

// ShouldSkipElevate 判断命令行是否要求跳过自动提权。
//
// [返回] true 表示不执行自提权
// 最近修改时间: 2026-09-12
func ShouldSkipElevate() bool {
	for _, arg := range os.Args[1:] {
		if arg == NoElevateFlag || arg == "-"+NoElevateFlag {
			return true
		}
	}
	return false
}

// RelaunchAsAdmin 以管理员身份重新启动当前程序。
//
// 通过 ShellExecute 的 runas 动词触发 UAC：系统会弹出确认框，用户同意后启动一个新的提权进程。
// 调用方在成功后应立即退出当前的非提权实例，避免同时出现两个窗口。
//
// [返回] 用户拒绝 UAC 或提权失败时返回 error
// 最近修改时间: 2026-09-12
func RelaunchAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}

	// 1. 原样传递命令行参数，保证提权后的实例与当前实例行为一致
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return fmt.Errorf("转换提权动词失败: %w", err)
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return fmt.Errorf("转换程序路径失败: %w", err)
	}
	argsPtr, err := windows.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	if err != nil {
		return fmt.Errorf("转换命令行参数失败: %w", err)
	}
	// 工作目录取 exe 所在目录而非当前进程的工作目录：从资源管理器双击启动时两者才一致
	cwdPtr, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return fmt.Errorf("转换工作目录失败: %w", err)
	}

	// 2. 触发 UAC；用户点"否"时系统返回 ERROR_CANCELLED
	if err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return fmt.Errorf("以管理员身份启动失败: %w", err)
	}
	return nil
}
