// Package ui 负责工具箱主窗口与各工具页面的构建。
package ui

import (
	"github.com/lxn/walk"
)

// Tool 表示工具箱中的一个工具。
//
// 新增工具只需实现该接口并注册到主窗口的工具列表，无需改动主窗口的布局与切换逻辑。
type Tool interface {
	// Name 返回在左侧工具列表与顶部菜单中展示的名称。
	Name() string

	// Description 返回展示在工具页顶部的一句话说明。
	Description() string

	// Build 在指定父容器中构建该工具的页面。
	//
	// [参数] mw: 主窗口，耗时操作回到 UI 线程时需要用它的 Synchronize；parent: 页面父容器
	// [返回] 页面根容器，主窗口通过控制其可见性完成工具切换；构建失败时返回 error
	Build(mw *walk.MainWindow, parent walk.Container) (*walk.Composite, error)
}
