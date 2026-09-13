// Command windows-util-gui-wails 是 Windows 工具箱的 Wails v2 前端宿主。
//
// 结束系统级进程需要管理员身份，因此启动时先自提权：与根 main.go 逻辑一致，
// 非提权时通过 ShellExecute runas 拉起提权实例后退出当前实例；用户拒绝 UAC 时降级为普通权限继续运行。
//
// 注意：wails build 生成绑定时会以 bindings tag 编译并无参数执行本包一次，
// 此时绝不能触发自提权（会弹 UAC 卡死构建），由 bindingGen 常量按构建标签守卫。
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"windows-util-gui/pkg/toolapi"
)

// assets 内嵌前端静态资源，编译后无需随包携带任何文件
//
//go:embed all:frontend/dist
var assets embed.FS

// main 启动 Wails 窗口。
//
// 启动前先做自提权：用 -no-elevate 可跳过（对齐 winutil 的判定），
// 便于在调试器里以普通权限直接运行而不把自己重新拉起成独立提权进程。
// bindingGen 守卫见文件头注释。
//
// 最近修改时间: 2026-09-13
func main() {
	// 1. 非提权启动时先尝试拉起管理员实例，成功后当前实例立即退出，避免出现两个窗口；
	//    绑定生成阶段（bindings tag）跳过，否则 wails build 会被 UAC 卡死
	if !bindingGen && !toolapi.ShouldSkipElevate() && !toolapi.IsElevated() {
		if err := toolapi.RelaunchAsAdmin(); err == nil {
			return
		}
		// 用户拒绝 UAC 或提权失败时降级为普通权限继续运行：
		// 查询端口占用本身不需要管理员，界面也会明确提示结束进程可能失败
		fmt.Fprintln(os.Stderr, "未取得管理员权限，将以普通权限运行，结束系统级进程可能失败。")
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Windows 工具箱 · Wails PoC",
		Width:     1200,
		Height:    760,
		MinWidth:  960,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 与理想设计稿的画布底色一致 (#F5F5F7)
		BackgroundColour: &options.RGBA{R: 245, G: 245, B: 247, A: 255},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		// Wails 尚未创建窗口时无法弹框，输出到控制台便于排查
		println("启动失败:", err.Error())
	}
}
