// Command windows-util-gui 是面向 Windows + WSL 的本地运维工具箱图形界面。
//
// 结束系统级进程与访问 WSL 的 root 权限都需要管理员身份，因此程序启动时会先自提权。
// 提权走运行时的 ShellExecute runas，而不是在 manifest 里声明 requireAdministrator：
// 后者会让 exe 无法被普通权限进程拉起，VS Code 按 F5 直接启动失败。
package main

import (
	"fmt"
	"os"

	"github.com/lxn/walk"

	"windows-util-gui/internal/ui"
	"windows-util-gui/internal/winutil"
)

// main 启动工具箱主窗口。
//
// 最近修改时间: 2026-09-12
func main() {
	// 1. 非提权启动时先尝试拉起管理员实例，成功后当前实例立即退出，避免出现两个窗口
	if !winutil.ShouldSkipElevate() && !winutil.IsElevated() {
		if err := winutil.RelaunchAsAdmin(); err == nil {
			return
		}
		// 用户拒绝 UAC 或提权失败时降级为普通权限继续运行：
		// 查询端口占用本身不需要管理员，界面也会明确提示结束进程可能失败
		fmt.Fprintln(os.Stderr, "未取得管理员权限，将以普通权限运行，结束系统级进程可能失败。")
	}

	// 2. 运行主窗口，直到用户关闭
	if err := ui.NewApp().Run(); err != nil {
		// 从控制台启动时错误要能被看到，否则只剩一个无法复制的弹窗，排查困难
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)

		// 窗口尚未建立时无法用主窗口做父级，这里以桌面为父级弹出错误提示
		walk.MsgBox(nil, "启动失败", err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}
}
