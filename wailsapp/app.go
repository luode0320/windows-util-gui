package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"windows-util-gui/pkg/toolapi"
)

// App 是暴露给前端的绑定对象，前端通过 window.go.main.App.<方法名> 调用。
type App struct {
	ctx context.Context
}

// NewApp 创建绑定实例。
//
// [返回] 绑定实例
// 最近修改时间: 2026-09-13
func NewApp() *App {
	return &App{}
}

// startup 保存 Wails 运行上下文，后续需要向前端推送事件时使用。
//
// 最近修改时间: 2026-09-13
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 权限状态进窗口标题，与 walk 版标题行为一致，让用户一眼确认提权结果
	suffix := "（普通权限）"
	if toolapi.IsElevated() {
		suffix = "（管理员）"
	}
	runtime.WindowSetTitle(ctx, "Windows 工具箱"+suffix)
}

// ProcRow 是结果表格一行的展示数据，直接透传桥接层的 Row。
type ProcRow = toolapi.Row

// KillOutcome 是一条进程结束请求的执行结果，直接透传桥接层的同名结构。
type KillOutcome = toolapi.KillOutcome

// Query 是按端口/ PID/ 进程名查询占用进程的公开绑定方法，统一透传桥接层。
//
// 这里不做任何入参校验：kind 白名单与 value 合法性都已在桥接层与业务层完成，
// 前端参数原样交付即可，避免两处重复校验导致规则漂移后行为不一致。
//
// [参数] kind: 查询方式（"port"/"pid"/"name"）；value: 查询值
// [返回] 进程行列表；查询失败时返回 error
// 最近修改时间: 2026-09-13
func (a *App) Query(kind, value string) ([]ProcRow, error) {
	rows, warnings, err := toolapi.Query(kind, value)
	if err != nil {
		return nil, err
	}
	// PoC 阶段先不展示 warnings（wslrelay 双侧提示等），完整迁移时随日志区一起补回
	_ = warnings
	return rows, nil
}

// IsElevated 返回当前进程是否以管理员身份运行，用于前端权限徽章展示。
//
// [返回] true 表示管理员权限
// 最近修改时间: 2026-09-13
func (a *App) IsElevated() bool {
	return toolapi.IsElevated()
}

// Kill 批量结束进程，统一透传桥接层。
//
// 不做任何入参校验：Source/Distro/PID/Name 的合法性由业务层 prockill.Kill 内部判定
// （含保护名单、权限检查），桥接层与绑定层都只做透传，避免两处重复规则导致行为漂移。
// 返回值与输入一一对应，逐条记录成功与否与失败原因，便于前端逐行展示。
//
// [参数] rows: 待结束的进程行列表
// [返回] 与输入顺序一致的逐条执行结果
// 最近修改时间: 2026-09-13
func (a *App) Kill(rows []ProcRow) []KillOutcome {
	return toolapi.Kill(rows)
}

// errToStr 把 error 转成可下发的字符串，nil 时返回空串。
func errToStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// tgLog 把业务层日志行经 Wails 事件推送到前端日志区。
func (a *App) tgLog(line string) {
	runtime.EventsEmit(a.ctx, "tg:log", line)
}

// tgOpDone 统一向前端推送长任务完成事件：op 标识任务类型，ok 标识成败，error 为失败原因。
func (a *App) tgOpDone(op string, err error) {
	runtime.EventsEmit(a.ctx, "tg:opDone", map[string]interface{}{
		"op":    op,
		"ok":    err == nil,
		"error": errToStr(err),
	})
}

// TgGetState 返回 TG 工具当前状态快照（登录态/tdl 路径/嵌入状态/代理/聊天/记录）。
//
// [返回] 状态快照
// 最近修改时间: 2026-09-13
func (a *App) TgGetState() toolapi.TgState {
	return toolapi.TgGetState()
}

// TgSetProxy 写入代理配置并即时做网络预检。
//
// [参数] proxy: 代理地址
// [返回] 代理非法或不可达时返回 error
// 最近修改时间: 2026-09-13
func (a *App) TgSetProxy(proxy string) error {
	return toolapi.TgSetProxy(proxy)
}

// TgValidateNetwork 按当前代理配置探测网络连通性。
//
// [返回] 网络不可用时返回具体说明
// 最近修改时间: 2026-09-13
func (a *App) TgValidateNetwork() error {
	return toolapi.TgValidateNetwork()
}

// TgRefreshChats 拉取最新聊天列表并写缓存，同步返回最新列表。
//
// [返回] 最新聊天列表；失败返回 error
// 最近修改时间: 2026-09-13
func (a *App) TgRefreshChats() ([]toolapi.Chat, error) {
	return toolapi.TgRefreshChats(a.tgLog)
}

// TgRecords 返回所有可用导出记录。
//
// [返回] 导出记录切片
// 最近修改时间: 2026-09-13
func (a *App) TgRecords() []toolapi.ExportRecord {
	return toolapi.TgRecords()
}

// TgDeleteRecord 删除指定导出文件及其登记记录。
//
// [参数] path: 导出 JSON 文件绝对路径
// [返回] 删除失败返回 error
// 最近修改时间: 2026-09-13
func (a *App) TgDeleteRecord(path string) error {
	return toolapi.TgDeleteRecord(path)
}

// TgStartLogin 启动扫码登录长任务，立即返回：二维码经 tg:qr 推送，日志经 tg:log，结束经 tg:opDone。
//
// [返回] 网络预检失败返回 error；其余情况经事件异步回传
// 最近修改时间: 2026-09-13
func (a *App) TgStartLogin() error {
	return toolapi.TgStartLogin(
		func(imgPath string) {
			// WebView2 页面运行在 http://wails.localhost 源下，禁止加载 file:/// 本地文件，
			// 必须把二维码转成 base64 data URL 推送，前端才能正常渲染
			if dataURL := a.qrImageDataURL(imgPath); dataURL != "" {
				runtime.EventsEmit(a.ctx, "tg:qr", dataURL)
			}
		},
		a.tgLog,
		func(err error) {
			a.tgOpDone("login", err)
		},
	)
}

// qrImageDataURL 读取二维码图片并转为 base64 data URL。
//
// tdl 写盘与回调之间存在时序差，图片可能尚未写完，
// 因此带有限次重试：每次检查文件存在且非空，最多等 2 秒。
//
// [参数] path: 二维码 PNG 文件路径
// [返回] data:image/png;base64 形式的 data URL；读取失败返回空串
// 最近修改时间: 2026-09-13
func (a *App) qrImageDataURL(path string) string {
	var data []byte
	for attempt := 0; attempt < 7; attempt++ {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() && fi.Size() > 0 {
			data, err = os.ReadFile(path)
			if err == nil && len(data) > 0 {
				return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	a.tgLog(fmt.Sprintf("读取二维码图片失败：%s", path))
	return ""
}

// TgCancel 取消当前正在进行的登录/导出/转发长任务。
//
// 最近修改时间: 2026-09-13
func (a *App) TgCancel() {
	toolapi.TgCancel()
}

// TgExport 启动消息导出长任务，立即返回：进度与结果经事件异步推送。
//
// [参数] in: 前端传入的导出参数
// [返回] 始终返回 nil（错误经 tg:opDone 异步回传）
// 最近修改时间: 2026-09-13
func (a *App) TgExport(in toolapi.TgExportInput) error {
	opt := toolapi.NewExportOptions(in)
	return toolapi.TgExport(opt, a.tgLog, func(err error) {
		a.tgOpDone("export", err)
	})
}

// TgForward 启动分批转发长任务，立即返回：进度与结果经事件异步推送。
//
// [参数] in: 前端传入的转发参数
// [返回] 始终返回 nil（错误经 tg:opDone 异步回传）
// 最近修改时间: 2026-09-13
func (a *App) TgForward(in toolapi.TgForwardInput) error {
	opt := toolapi.NewForwardOptions(in)
	return toolapi.TgForward(opt, a.tgLog, func(err error) {
		a.tgOpDone("forward", err)
	})
}

// TgClearHistory 清除指定源到目标的转发历史记录。
//
// [参数] sourceID: 来源 ID；target: 目标聊天
// [返回] 清除失败返回 error
// 最近修改时间: 2026-09-13
func (a *App) TgClearHistory(sourceID, target string) error {
	return toolapi.TgClearHistory(sourceID, target)
}

// TgClearSession 清除登录标记与会话文件，配合"重新扫码登录"使用。
//
// [返回] 清除失败返回 error
// 最近修改时间: 2026-09-13
func (a *App) TgClearSession() error {
	return toolapi.TgClearSession()
}
