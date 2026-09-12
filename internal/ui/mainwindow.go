package ui

import (
	"fmt"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"windows-util-gui/internal/winutil"
)

// appTitle 是主窗口标题。
const appTitle = "Windows 工具箱"

// App 是工具箱主窗口，负责工具注册、页面构建与切换。
type App struct {
	mw       *walk.MainWindow
	toolList *walk.ListBox
	content  *walk.Composite
	tools    []Tool
	pages    []*walk.Composite
}

// NewApp 创建主窗口实例并注册全部工具。
//
// 工具在此集中注册：新增工具时只需在 tools 切片追加实现，菜单、列表与页面切换会自动生效。
//
// [返回] 主窗口实例
// 最近修改时间: 2026-09-12
func NewApp() *App {
	return &App{
		tools: []Tool{
			NewProcKillTool(),
		},
	}
}

// Run 构建并运行主窗口，直到用户关闭窗口后返回。
//
// [返回] 窗口创建失败时返回 error
// 最近修改时间: 2026-09-12
func (a *App) Run() error {
	// 1. 先创建窗口骨架：左侧工具列表 + 右侧空白内容区
	if err := a.buildShell(); err != nil {
		return err
	}

	// 2. 内容区就绪后再逐个构建工具页面，构建完成即隐藏，由 selectTool 控制显示
	if err := a.buildPages(); err != nil {
		return err
	}

	// 3. 默认选中第一个工具，避免打开后是空白页
	if len(a.tools) > 0 {
		a.toolList.SetCurrentIndex(0)
	}

	a.mw.Run()
	return nil
}

// buildShell 创建主窗口骨架，包含菜单栏、左侧工具列表与右侧内容区。
//
// [返回] 创建失败时返回 error
// 最近修改时间: 2026-09-12
func (a *App) buildShell() error {
	names := make([]string, 0, len(a.tools))
	for _, tool := range a.tools {
		names = append(names, tool.Name())
	}

	window := d.MainWindow{
		AssignTo:  &a.mw,
		Title:     a.windowTitle(),
		MinSize:   d.Size{Width: 960, Height: 620},
		Size:      d.Size{Width: 1040, Height: 680},
		Layout:    d.HBox{},
		MenuItems: a.buildMenu(names),
		Children: []d.Widget{
			d.ListBox{
				AssignTo:              &a.toolList,
				Model:                 names,
				MinSize:               d.Size{Width: 170},
				MaxSize:               d.Size{Width: 170},
				OnCurrentIndexChanged: a.onToolChanged,
			},
			// StretchFactor 必须显著大于工具列表，否则 HBox 会按等比分配宽度，
			// 列表被 MaxSize 截断后留下的空白不会让给内容区
			d.Composite{
				AssignTo:      &a.content,
				StretchFactor: 10,
				Layout:        d.HBox{MarginsZero: true},
			},
		},
	}

	if err := window.Create(); err != nil {
		return fmt.Errorf("创建主窗口失败: %w", err)
	}
	return nil
}

// buildMenu 构造顶部菜单，把工具列表同步暴露为菜单项。
//
// [参数] names: 工具名称列表
// [返回] 菜单项声明
// 最近修改时间: 2026-09-12
func (a *App) buildMenu(names []string) []d.MenuItem {
	toolActions := make([]d.MenuItem, 0, len(names)+2)
	for i, name := range names {
		index := i // 闭包需要按值捕获索引，否则所有菜单项都会指向最后一个工具
		toolActions = append(toolActions, d.Action{
			Text: name,
			OnTriggered: func() {
				a.toolList.SetCurrentIndex(index)
			},
		})
	}
	toolActions = append(toolActions, d.Separator{}, d.Action{
		Text:        "退出",
		OnTriggered: func() { a.mw.Close() },
	})

	return []d.MenuItem{
		d.Menu{Text: "工具(&T)", Items: toolActions},
		d.Menu{Text: "帮助(&H)", Items: []d.MenuItem{
			d.Action{Text: "关于", OnTriggered: a.showAbout},
		}},
	}
}

// buildPages 为每个工具构建页面并默认隐藏。
//
// [返回] 任一工具页面构建失败时返回 error
// 最近修改时间: 2026-09-12
func (a *App) buildPages() error {
	a.pages = make([]*walk.Composite, 0, len(a.tools))
	for _, tool := range a.tools {
		page, err := tool.Build(a.mw, a.content)
		if err != nil {
			return err
		}
		page.SetVisible(false)
		a.pages = append(a.pages, page)
	}
	return nil
}

// onToolChanged 在左侧列表选中项变化时切换右侧页面。
//
// 最近修改时间: 2026-09-12
func (a *App) onToolChanged() {
	current := a.toolList.CurrentIndex()
	for i, page := range a.pages {
		page.SetVisible(i == current)
	}
	// walk 未导出布局刷新入口，这里手动触发一次 WM_SIZE，
	// 让内容区按当前可见页面重算布局，否则切换后新页面不会填满内容区
	bounds := a.content.ClientBoundsPixels()
	a.content.SendMessage(win.WM_SIZE, 0,
		uintptr(uint32(bounds.Height)<<16|uint32(bounds.Width)&0xFFFF))
}

// showAbout 展示关于信息。
//
// 最近修改时间: 2026-09-12
func (a *App) showAbout() {
	walk.MsgBox(a.mw, "关于",
		appTitle+"\n\n面向 Windows + WSL 的本地运维小工具集合。\n当前收录工具：进程占用查杀。",
		walk.MsgBoxIconInformation)
}

// windowTitle 生成带权限状态的窗口标题。
//
// [返回] 窗口标题文本
// 最近修改时间: 2026-09-12
func (a *App) windowTitle() string {
	if winutil.IsElevated() {
		return appTitle + "（管理员）"
	}
	return appTitle + "（普通权限）"
}
