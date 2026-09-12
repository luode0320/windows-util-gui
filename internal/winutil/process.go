package winutil

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// IsElevated 判断当前进程是否已获得管理员权限。
//
// 杀掉系统服务或其它用户的进程需要提权，界面据此给出明确提示而不是静默失败。
//
// [返回] true 表示当前进程已提权
// 最近修改时间: 2026-09-12
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// ProcessName 查询指定 PID 的可执行文件名。
//
// 走 Win32 API 而不是解析 tasklist 输出，既避免控制台码页导致的中文乱码，也少起一个子进程。
//
// [参数] pid: 目标进程 ID
// [返回] 可执行文件名（不含目录）；查询失败时返回空串与 error
// 最近修改时间: 2026-09-12
func ProcessName(pid uint32) (string, error) {
	// 0 号与 4 号是系统伪进程，无法打开句柄，直接给出固定名称
	switch pid {
	case 0:
		return "System Idle Process", nil
	case 4:
		return "System", nil
	}

	// 1. 只申请查询权限，避免对受保护进程因权限过高而直接失败
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("打开进程 %d 失败: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	// 2. 取完整映像路径后只保留文件名，列表里展示更紧凑
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", fmt.Errorf("查询进程 %d 映像路径失败: %w", pid, err)
	}
	return filepath.Base(windows.UTF16ToString(buf[:size])), nil
}

// TerminateProcess 强制结束指定 PID 的进程。
//
// [参数] pid: 目标进程 ID
// [返回] 结束失败时返回 error，权限不足是最常见原因
// 最近修改时间: 2026-09-12
func TerminateProcess(pid uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("打开进程 %d 失败（可能权限不足或进程已退出）: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	if err := windows.TerminateProcess(handle, 1); err != nil {
		return fmt.Errorf("结束进程 %d 失败: %w", pid, err)
	}
	return nil
}
