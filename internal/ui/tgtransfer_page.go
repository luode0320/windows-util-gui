package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"windows-util-gui/internal/tools/tgtransfer"
)

// exportTypeOption 定义导出类型筛选的展示与标识。
type exportTypeOption struct {
	label string
	key   string
}

var exportTypeOptions = []exportTypeOption{
	{label: "全部类型", key: ""},
	{label: "纯文本 (无附件)", key: "text"},
	{label: "TXT 文本文件", key: "txt"},
	{label: "视频文件 (MP4/MKV等)", key: "video"},
	{label: "小说/合集 (含小说关键词)", key: "novel"},
	{label: "EPUB 电子书", key: "epub"},
	{label: "MP4 视频", key: "mp4"},
}

// chatTableModel 是聊天列表弹窗的数据模型。
type chatTableModel struct {
	walk.TableModelBase
	items []tgtransfer.Chat
}

func (m *chatTableModel) RowCount() int {
	return len(m.items)
}

func (m *chatTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return ""
	}
	item := m.items[row]
	switch col {
	case 0:
		return item.ID
	case 1:
		return item.Type
	case 2:
		return item.VisibleName
	case 3:
		if item.Username == "" {
			return "-"
		}
		return "@" + item.Username
	case 4:
		return item.Topics
	}
	return ""
}

func (m *chatTableModel) reset(items []tgtransfer.Chat) {
	m.items = items
	m.PublishRowsReset()
}

// TgTransferTool 是 "Telegram 消息转发" 工具，实现 ui.Tool 接口。
type TgTransferTool struct {
	mw  *walk.MainWindow
	cfg *tgtransfer.Config

	// 任务取消控制
	cancelTask context.CancelFunc
	activeName string

	// 基础设置与登录
	proxyEdit    *walk.LineEdit
	tdlPathEdit  *walk.LineEdit
	loginState   *walk.Label
	loginBtn     *walk.PushButton
	qrDialog     *walk.Dialog
	qrImageView  *walk.ImageView
	loginSession *tgtransfer.QrLoginSession

	// 聊天与导出控件
	refreshChatBtn *walk.PushButton
	viewChatBtn    *walk.PushButton
	sourceCombo    *walk.ComboBox
	exportModeBox  *walk.ComboBox
	lastCountEdit  *walk.LineEdit
	filterTypeBox  *walk.ComboBox
	exportBtn      *walk.PushButton

	// 转发控制控件
	exportContentBox *walk.ComboBox
	clearExportBtn   *walk.PushButton
	targetCombo      *walk.ComboBox
	forwardModeBox   *walk.ComboBox
	dryRunCheck      *walk.CheckBox
	skipHistoryCheck *walk.CheckBox
	reverseCheck     *walk.CheckBox
	batchSizeEdit    *walk.LineEdit
	delaySecEdit     *walk.LineEdit
	forwardBtn       *walk.PushButton
	stopBtn          *walk.PushButton
	clearHistoryBtn  *walk.PushButton

	// 底部状态与日志
	statusLabel *walk.Label
	logView     *walk.TextEdit

	// 内存缓存数据
	chats         []tgtransfer.Chat
	exportRecords []tgtransfer.ExportRecord
}

// NewTgTransferTool 创建 TG 消息转发工具实例。
//
// [返回] 工具实例
// 最近修改时间: 2026-09-13
func NewTgTransferTool() *TgTransferTool {
	return &TgTransferTool{
		cfg: tgtransfer.DefaultConfig(),
	}
}

// Name 返回工具在菜单与列表中的展示名称。
//
// [返回] 工具名称
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) Name() string {
	return "TG 消息转发"
}

// Description 返回工具的一句话功能说明。
//
// [返回] 功能说明
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) Description() string {
	return "Telegram 收藏夹与群组/频道历史消息导出与分批自动化转发"
}

// Build 构建 TG 消息转发工具的完整 UI 页面。
//
// [参数] mw: 主窗口指针；parent: 页面挂载容器
// [返回] 页面根复合容器指针；失败返回 error
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) Build(mw *walk.MainWindow, parent walk.Container) (*walk.Composite, error) {
	t.mw = mw
	t.loginSession = tgtransfer.NewQrLoginSession(t.cfg)

	filterNames := make([]string, 0, len(exportTypeOptions))
	for _, opt := range exportTypeOptions {
		filterNames = append(filterNames, opt.label)
	}

	var root *walk.Composite
	page := d.Composite{
		AssignTo: &root,
		Layout:   d.VBox{MarginsZero: false},
		Children: []d.Widget{
			// 1. 顶部一句话说明
			d.Label{
				Text:      t.Description(),
				TextColor: walk.RGB(90, 90, 90),
				MaxSize:   d.Size{Height: 18},
			},

			// 2. 基础配置与登录分组
			d.GroupBox{
				Title:  "运行环境与登录状态",
				Layout: d.HBox{},
				Children: []d.Widget{
					d.Label{Text: "代理地址："},
					d.LineEdit{
						AssignTo:  &t.proxyEdit,
						CueBanner: "如 socks5://127.0.0.1:7890 (直连可留空)",
						Text:      t.cfg.Proxy,
						MaxSize:   d.Size{Width: 200},
						OnTextChanged: func() {
							t.cfg.Proxy = strings.TrimSpace(t.proxyEdit.Text())
						},
					},
					d.Label{Text: "内置 TDL（已编译进本程序）："},
					d.LineEdit{
						AssignTo:  &t.tdlPathEdit,
						Text:      t.cfg.TdlPath,
						CueBanner: "tdl.exe 可执行文件路径",
						OnTextChanged: func() {
							t.cfg.TdlPath = strings.TrimSpace(t.tdlPathEdit.Text())
						},
					},
					d.PushButton{
						Text:      "浏览...",
						MaxSize:   d.Size{Width: 60},
						OnClicked: t.onBrowseTdlPath,
					},
					d.HSpacer{Size: 10},
					d.Label{
						AssignTo: &t.loginState,
						Text:     "正在检测登录状态……",
					},
					d.PushButton{
						AssignTo:  &t.loginBtn,
						Text:      "扫码登录",
						MaxSize:   d.Size{Width: 100},
						OnClicked: t.onLoginClick,
					},
				},
			},

			// 3. 消息导出控制分组
			d.GroupBox{
				Title:  "消息导出 (支持 Saved Messages 收藏夹与各公开/私有群组频道)",
				Layout: d.VBox{},
				Children: []d.Widget{
					d.Composite{
						Layout: d.HBox{MarginsZero: true},
						Children: []d.Widget{
							d.PushButton{
								AssignTo:  &t.refreshChatBtn,
								Text:      "刷新聊天列表",
								OnClicked: t.onRefreshChats,
							},
							d.PushButton{
								AssignTo:  &t.viewChatBtn,
								Text:      "查看聊天列表",
								OnClicked: t.onViewChatList,
							},
							d.Label{Text: "源聊天："},
							d.ComboBox{
								AssignTo: &t.sourceCombo,
								Editable: true,
								MinSize:  d.Size{Width: 240},
							},
							d.Label{Text: "模式："},
							d.ComboBox{
								AssignTo:     &t.exportModeBox,
								Model:        []string{"全部消息", "最近 N 条"},
								CurrentIndex: 0,
								MaxSize:      d.Size{Width: 100},
								OnCurrentIndexChanged: func() {
									isLast := t.exportModeBox.CurrentIndex() == 1
									t.lastCountEdit.SetEnabled(isLast)
								},
							},
							d.Label{Text: "条数："},
							d.LineEdit{
								AssignTo:  &t.lastCountEdit,
								Text:      "1000",
								CueBanner: "数量",
								Enabled:   false,
								MaxSize:   d.Size{Width: 60},
							},
							d.Label{Text: "类型筛选："},
							d.ComboBox{
								AssignTo:     &t.filterTypeBox,
								Model:        filterNames,
								CurrentIndex: 0,
								MaxSize:      d.Size{Width: 140},
							},
							d.PushButton{
								AssignTo:  &t.exportBtn,
								Text:      "开始导出",
								OnClicked: t.onStartExport,
							},
							d.HSpacer{},
						},
					},
				},
			},

			// 4. 转发控制分组
			d.GroupBox{
				Title:  "消息转发控制 (从导出的 JSON 分批、去重、顺序控制并发往目标群)",
				Layout: d.VBox{},
				Children: []d.Widget{
					// 转发目标行
					d.Composite{
						Layout: d.HBox{MarginsZero: true},
						Children: []d.Widget{
							d.Label{Text: "转发内容："},
							d.ComboBox{
								AssignTo: &t.exportContentBox,
								Editable: false,
								MinSize:  d.Size{Width: 280},
							},
							d.PushButton{
								AssignTo:  &t.clearExportBtn,
								Text:      "清空所选导出",
								OnClicked: t.onClearExportContent,
							},
							d.Label{Text: "目标群组/频道："},
							d.ComboBox{
								AssignTo: &t.targetCombo,
								Editable: true,
								MinSize:  d.Size{Width: 220},
							},
							d.HSpacer{},
						},
					},
					// 转发参数行
					d.Composite{
						Layout: d.HBox{MarginsZero: true},
						Children: []d.Widget{
							d.Label{Text: "转发模式："},
							d.ComboBox{
								AssignTo:     &t.forwardModeBox,
								Model:        []string{"保留来源 (direct)", "复制内容 (clone)"},
								CurrentIndex: 0,
								MaxSize:      d.Size{Width: 140},
							},
							d.CheckBox{
								AssignTo: &t.dryRunCheck,
								Text:     "试转发 (不真正发送)",
								Checked:  true,
							},
							d.CheckBox{
								AssignTo: &t.skipHistoryCheck,
								Text:     "跳过已转发",
								Checked:  true,
							},
							d.CheckBox{
								AssignTo: &t.reverseCheck,
								Text:     "反转阅读顺序",
								Checked:  false,
							},
							d.Label{Text: "每批条数："},
							d.LineEdit{
								AssignTo: &t.batchSizeEdit,
								Text:     "10",
								MaxSize:  d.Size{Width: 50},
							},
							d.Label{Text: "间隔(秒)："},
							d.LineEdit{
								AssignTo: &t.delaySecEdit,
								Text:     "1",
								MaxSize:  d.Size{Width: 40},
							},
							d.PushButton{
								AssignTo:  &t.forwardBtn,
								Text:      "开始转发",
								OnClicked: t.onStartForward,
							},
							d.PushButton{
								AssignTo:  &t.stopBtn,
								Text:      "停止任务",
								Enabled:   false,
								OnClicked: t.onStopTask,
							},
							d.PushButton{
								AssignTo:  &t.clearHistoryBtn,
								Text:      "清空历史",
								OnClicked: t.onClearHistory,
							},
							d.HSpacer{},
						},
					},
				},
			},

			// 5. 底部状态与日志区
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.Label{Text: "运行状态："},
					d.Label{
						AssignTo: &t.statusLabel,
						Text:     "就绪 (空闲)",
					},
					d.HSpacer{},
				},
			},
			d.TextEdit{
				AssignTo: &t.logView,
				ReadOnly: true,
				VScroll:  true,
				MinSize:  d.Size{Height: 160},
			},
		},
	}

	if err := page.Create(d.NewBuilder(parent)); err != nil {
		return nil, fmt.Errorf("构建 TG 消息转发页面失败: %w", err)
	}

	// 6. 初始化页面数据与状态
	t.refreshComboOptions()
	t.updateLoginState()
	t.loadInitialData()

	t.logf("Telegram 消息转发助手已加载。tdl.exe 已编译进本程序，上方路径仅为首次运行时的自动释放位置。")
	if fi, err := os.Stat(t.cfg.TdlPath); err == nil && !fi.IsDir() {
		// 界面同步显示实际生效路径，避免输入框停留在裸 "tdl.exe" 造成误读
		t.tdlPathEdit.SetText(t.cfg.TdlPath)
		t.logf("已确认释放产物可用: %s（删除后会在下次启动时自动恢复）", t.cfg.TdlPath)
	} else {
		t.logf("提示: 内置 tdl.exe 释放失败或未找到，请点击【浏览...】手动选择 tdl.exe。")
	}

	return root, nil
}

// updateLoginState 检查本地会话文件并刷新界面文字。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) updateLoginState() {
	isLogged := tgtransfer.IsLoggedIn(t.cfg)
	if isLogged {
		t.loginState.SetText("状态: 已检测到本地登录")
		t.loginState.SetTextColor(walk.RGB(0, 140, 0))
		t.loginBtn.SetText("重新扫码登录")
	} else {
		t.loginState.SetText("状态: 未检测到登录会话")
		t.loginState.SetTextColor(walk.RGB(200, 80, 0))
		t.loginBtn.SetText("扫码登录")
	}
}

// loadInitialData 异步装载本地缓存的聊天列表与导出记录。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) loadInitialData() {
	go func() {
		chats, _ := tgtransfer.LoadCachedChats(t.cfg)
		records := tgtransfer.GetAllAvailableExportRecords(t.cfg)

		t.mw.Synchronize(func() {
			t.chats = chats
			t.exportRecords = records
			t.refreshComboOptions()
		})
	}()
}

// refreshComboOptions 刷新源聊天、目标聊天与导出内容下拉框。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) refreshComboOptions() {
	// 1. 组装源聊天的列表项
	sourceItems := []string{"收藏夹 (Saved Messages)"}
	targetItems := make([]string, 0, len(t.chats))
	for _, c := range t.chats {
		opt := tgtransfer.FormatChatOption(c)
		sourceItems = append(sourceItems, opt)
		targetItems = append(targetItems, opt)
	}

	_ = t.sourceCombo.SetModel(sourceItems)
	if len(sourceItems) > 0 && t.sourceCombo.CurrentIndex() < 0 {
		t.sourceCombo.SetCurrentIndex(0)
	}

	_ = t.targetCombo.SetModel(targetItems)
	if len(targetItems) > 0 && t.targetCombo.CurrentIndex() < 0 {
		t.targetCombo.SetCurrentIndex(0)
	}

	// 2. 组装导出记录下拉项
	recordItems := make([]string, 0, len(t.exportRecords))
	for _, r := range t.exportRecords {
		desc := fmt.Sprintf("%s | %s | %s | %d条 (%s)",
			r.CreatedAt, r.SourceName, r.ExportMode, r.MessageCount, filepath.Base(r.Path))
		recordItems = append(recordItems, desc)
	}
	_ = t.exportContentBox.SetModel(recordItems)
	if len(recordItems) > 0 && t.exportContentBox.CurrentIndex() < 0 {
		t.exportContentBox.SetCurrentIndex(0)
	}

	if len(t.chats) > 0 {
		t.viewChatBtn.SetText(fmt.Sprintf("查看聊天列表 (%d)", len(t.chats)))
	} else {
		t.viewChatBtn.SetText("查看聊天列表")
	}
}

// onBrowseTdlPath 打开文件选择器定位 tdl.exe。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onBrowseTdlPath() {
	dlg := new(walk.FileDialog)
	dlg.Title = "选择 tdl.exe 可执行程序"
	dlg.Filter = "可执行文件 (*.exe)|*.exe|全部文件 (*.*)|*.*"
	if ok, err := dlg.ShowOpen(t.mw); err == nil && ok {
		t.tdlPathEdit.SetText(dlg.FilePath)
		t.cfg.TdlPath = dlg.FilePath
		t.logf("TDL 路径已更改为: %s", dlg.FilePath)
	}
}

// onLoginClick 点击扫码登录按钮，支持确认清除旧会话与弹窗展示登录二维码。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onLoginClick() {
	if tgtransfer.IsLoggedIn(t.cfg) {
		msg := "已检测到本地登录会话，通常可直接操作。\n若需更换账号或重新授权，请确认清除现有会话并重新扫码。"
		if walk.MsgBox(t.mw, "重新扫码登录", msg, walk.MsgBoxIconQuestion|walk.MsgBoxYesNo) != walk.DlgCmdYes {
			return
		}
		if err := tgtransfer.ClearSession(t.cfg); err != nil {
			t.logf("清除本地会话失败: %v", err)
			return
		}
		t.updateLoginState()
		t.logf("已清除本地会话。")
	}

	// 提前校验网络连通性
	if err := tgtransfer.ValidateNetwork(t.cfg.Proxy); err != nil {
		walk.MsgBox(t.mw, "网络不可用", err.Error(), walk.MsgBoxIconWarning)
		t.logf("网络检测未通过: %v", err)
		return
	}

	t.setBusy(true, "正在准备扫码登录...")
	t.logf("启动扫码登录，正在等待 tdl 生成二维码……")

	ctx, cancel := context.WithCancel(context.Background())
	t.cancelTask = cancel

	go func() {
		defer t.mw.Synchronize(func() {
			t.setBusy(false, "")
			t.updateLoginState()
			if t.qrDialog != nil {
				t.qrDialog.Close(0)
				t.qrDialog = nil
			}
		})

		loginErr := t.loginSession.StartQrLogin(ctx, func(imgPath string) {
			t.mw.Synchronize(func() {
				t.showQrDialog(imgPath)
			})
		}, func(line string) {
			t.mw.Synchronize(func() {
				t.logf("[tdl login] %s", line)
			})
		})

		t.mw.Synchronize(func() {
			if loginErr != nil {
				if ctx.Err() != nil {
					t.logf("扫码登录已取消。")
				} else {
					t.logf("扫码登录结束: %v", loginErr)
				}
			} else {
				t.logf("✓ 扫码登录成功！会话已就绪。")
			}
		})
	}()
}

// showQrDialog 弹出展示登录二维码图片的对话框。
//
// [参数] imgPath: 二维码图片文件路径
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) showQrDialog(imgPath string) {
	if t.qrDialog != nil {
		return
	}

	img, err := walk.NewImageFromFile(imgPath)
	if err != nil {
		t.logf("加载二维码图片失败: %v", err)
		return
	}

	dlg := d.Dialog{
		AssignTo: &t.qrDialog,
		Title:    "请用 Telegram 手机端扫描二维码",
		MinSize:  d.Size{Width: 320, Height: 380},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.Label{
				Text:      "请使用 Telegram 手机 App -> 设置 -> 设备 -> 扫码关联",
				Alignment: d.Alignment2D(walk.AlignHCenterVCenter),
			},
			d.ImageView{
				AssignTo: &t.qrImageView,
				Image:    img,
				MinSize:  d.Size{Width: 260, Height: 260},
				Margin:   10,
			},
			d.PushButton{
				Text: "取消并关闭",
				OnClicked: func() {
					if t.cancelTask != nil {
						t.cancelTask()
					}
					t.qrDialog.Close(0)
				},
			},
		},
	}

	if err := dlg.Create(t.mw); err == nil {
		t.qrDialog.Show()
	}
}

// onRefreshChats 刷新当前账号可见的全部群组与频道列表。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onRefreshChats() {
	if err := tgtransfer.ValidateNetwork(t.cfg.Proxy); err != nil {
		walk.MsgBox(t.mw, "网络检测未通过", err.Error(), walk.MsgBoxIconWarning)
		return
	}

	t.setBusy(true, "正在刷新聊天列表...")
	ctx, cancel := context.WithCancel(context.Background())
	t.cancelTask = cancel

	go func() {
		defer t.mw.Synchronize(func() {
			t.setBusy(false, "")
		})

		chats, err := tgtransfer.FetchChats(ctx, t.cfg, func(line string) {
			t.mw.Synchronize(func() {
				t.logf("%s", line)
			})
		})

		t.mw.Synchronize(func() {
			if err != nil {
				t.logf("刷新聊天列表失败: %v", err)
				walk.MsgBox(t.mw, "刷新失败", fmt.Sprintf("获取聊天列表失败：%v", err), walk.MsgBoxIconError)
				return
			}
			t.chats = chats
			t.refreshComboOptions()
			t.logf("已刷新并载入 %d 个聊天，已同步更新到源与目标选择框。", len(chats))
		})
	}()
}

// onViewChatList 弹出独立窗口展示完整的聊天表格并支持一键复制与选用。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onViewChatList() {
	if len(t.chats) == 0 {
		chats, _ := tgtransfer.LoadCachedChats(t.cfg)
		if len(chats) > 0 {
			t.chats = chats
		}
	}

	if len(t.chats) == 0 {
		walk.MsgBox(t.mw, "提示", "当前本地暂无聊天记录，请先点击【刷新聊天列表】。", walk.MsgBoxIconInformation)
		return
	}

	model := &chatTableModel{items: t.chats}
	var dlg *walk.Dialog
	var tv *walk.TableView

	dUI := d.Dialog{
		AssignTo: &dlg,
		Title:    fmt.Sprintf("全部聊天列表 (共 %d 项)", len(t.chats)),
		MinSize:  d.Size{Width: 760, Height: 480},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.TableView{
				AssignTo:         &tv,
				Model:            model,
				AlternatingRowBG: true,
				Columns: []d.TableViewColumn{
					{Title: "ID", Width: 110},
					{Title: "类型", Width: 90},
					{Title: "名称", Width: 220},
					{Title: "用户名", Width: 140},
					{Title: "主题/备注", Width: 160},
				},
			},
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.PushButton{
						Text: "复制选中 ID",
						OnClicked: func() {
							idx := tv.CurrentIndex()
							if idx >= 0 && idx < len(t.chats) {
								c := t.chats[idx]
								walk.Clipboard().SetText(c.ID)
								t.logf("已将聊天 ID [%s] 复制到剪贴板", c.ID)
							}
						},
					},
					d.PushButton{
						Text: "设为源聊天",
						OnClicked: func() {
							idx := tv.CurrentIndex()
							if idx >= 0 && idx < len(t.chats) {
								c := t.chats[idx]
								t.sourceCombo.SetText(tgtransfer.FormatChatOption(c))
								dlg.Close(0)
							}
						},
					},
					d.PushButton{
						Text: "设为目标群组",
						OnClicked: func() {
							idx := tv.CurrentIndex()
							if idx >= 0 && idx < len(t.chats) {
								c := t.chats[idx]
								t.targetCombo.SetText(tgtransfer.FormatChatOption(c))
								dlg.Close(0)
							}
						},
					},
					d.HSpacer{},
					d.PushButton{
						Text: "关闭",
						OnClicked: func() {
							dlg.Close(0)
						},
					},
				},
			},
		},
	}

	if err := dUI.Create(t.mw); err == nil {
		dlg.Run()
	}
}

// onStartExport 执行消息导出。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onStartExport() {
	if err := tgtransfer.ValidateNetwork(t.cfg.Proxy); err != nil {
		walk.MsgBox(t.mw, "网络检测未通过", err.Error(), walk.MsgBoxIconWarning)
		return
	}

	sourceText := strings.TrimSpace(t.sourceCombo.Text())
	if sourceText == "" {
		walk.MsgBox(t.mw, "提示", "请选择或输入源聊天。", walk.MsgBoxIconInformation)
		return
	}

	opt := tgtransfer.ExportOptions{}
	if strings.Contains(sourceText, "收藏夹") || strings.EqualFold(sourceText, "Saved Messages") {
		opt.IsSavedMessages = true
		opt.ChatName = "收藏夹"
	} else {
		opt.ChatID = tgtransfer.ExtractChatID(sourceText)
		opt.ChatName = sourceText
	}

	// 导出模式
	if t.exportModeBox.CurrentIndex() == 1 {
		opt.Mode = tgtransfer.ExportModeLast
		count, _ := strconv.Atoi(strings.TrimSpace(t.lastCountEdit.Text()))
		if count <= 0 {
			walk.MsgBox(t.mw, "提示", "请输入有效的最近条数。", walk.MsgBoxIconWarning)
			return
		}
		opt.LastCount = count
	} else {
		opt.Mode = tgtransfer.ExportModeAll
	}

	// 类型筛选
	filterIdx := t.filterTypeBox.CurrentIndex()
	if filterIdx >= 0 && filterIdx < len(exportTypeOptions) {
		k := exportTypeOptions[filterIdx].key
		if k != "" {
			opt.Filters = []string{k}
		}
	}

	t.setBusy(true, "正在导出消息...")
	ctx, cancel := context.WithCancel(context.Background())
	t.cancelTask = cancel

	go func() {
		defer t.mw.Synchronize(func() {
			t.setBusy(false, "")
		})

		rec, err := tgtransfer.ExportMessages(ctx, t.cfg, opt, func(line string) {
			t.mw.Synchronize(func() {
				t.logf("%s", line)
			})
		})

		t.mw.Synchronize(func() {
			if err != nil {
				t.logf("导出执行失败: %v", err)
				walk.MsgBox(t.mw, "导出失败", fmt.Sprintf("%v", err), walk.MsgBoxIconError)
				return
			}
			t.exportRecords = tgtransfer.GetAllAvailableExportRecords(t.cfg)
			t.refreshComboOptions()
			walk.MsgBox(t.mw, "导出完成",
				fmt.Sprintf("导出成功！\n有效记录：%d 条\n文件：%s", rec.MessageCount, rec.Path),
				walk.MsgBoxIconInformation)
		})
	}()
}

// onClearExportContent 删除当前选中的导出 JSON 及元数据记录。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onClearExportContent() {
	idx := t.exportContentBox.CurrentIndex()
	if idx < 0 || idx >= len(t.exportRecords) {
		walk.MsgBox(t.mw, "提示", "请先在【转发内容】下拉框中选择要清空的导出记录。", walk.MsgBoxIconInformation)
		return
	}

	targetRec := t.exportRecords[idx]
	msg := fmt.Sprintf("确认清空并删除选中的导出文件吗？\n\n文件路径: %s\n包含记录: %d 条", targetRec.Path, targetRec.MessageCount)
	if walk.MsgBox(t.mw, "确认清空导出", msg, walk.MsgBoxIconWarning|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		return
	}

	if err := tgtransfer.DeleteExportRecord(t.cfg, targetRec.Path); err != nil {
		t.logf("清空导出文件失败: %v", err)
		return
	}
	t.logf("已清空并删除导出文件: %s", targetRec.Path)
	t.exportRecords = tgtransfer.GetAllAvailableExportRecords(t.cfg)
	t.refreshComboOptions()
}

// onStartForward 处理开始转发（试转发或正式分批转发）。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onStartForward() {
	if err := tgtransfer.ValidateNetwork(t.cfg.Proxy); err != nil {
		walk.MsgBox(t.mw, "网络检测未通过", err.Error(), walk.MsgBoxIconWarning)
		return
	}

	idx := t.exportContentBox.CurrentIndex()
	if idx < 0 || idx >= len(t.exportRecords) {
		walk.MsgBox(t.mw, "提示", "请选择有效的待转发内容导出文件。", walk.MsgBoxIconInformation)
		return
	}
	exportFile := t.exportRecords[idx].Path

	targetText := strings.TrimSpace(t.targetCombo.Text())
	if targetText == "" {
		walk.MsgBox(t.mw, "提示", "请选择或输入目标群组/频道的 ID 或 username。", walk.MsgBoxIconInformation)
		return
	}
	targetID := tgtransfer.ExtractChatID(targetText)

	batchSize, _ := strconv.Atoi(strings.TrimSpace(t.batchSizeEdit.Text()))
	if batchSize <= 0 {
		batchSize = 10
	}
	delaySec, _ := strconv.Atoi(strings.TrimSpace(t.delaySecEdit.Text()))
	if delaySec < 0 {
		delaySec = 1
	}

	mode := "direct"
	if t.forwardModeBox.CurrentIndex() == 1 {
		mode = "clone"
	}

	isDryRun := t.dryRunCheck.Checked()
	if !isDryRun {
		// 正式发送二次确认
		confirmMsg := fmt.Sprintf("⚠️ 即将真正发送消息到目标群组/频道！\n\n目标: %s\n来源文件: %s\n转发模式: %s\n每批条数: %d\n\n确认现在执行正式转发？",
			targetID, filepath.Base(exportFile), mode, batchSize)
		if walk.MsgBox(t.mw, "确认正式转发", confirmMsg, walk.MsgBoxIconWarning|walk.MsgBoxYesNo) != walk.DlgCmdYes {
			t.logf("已取消正式转发。")
			return
		}
	}

	opt := tgtransfer.ForwardOptions{
		ExportPath:          exportFile,
		Target:              targetID,
		Mode:                mode,
		DryRun:              isDryRun,
		BatchSize:           batchSize,
		DelaySeconds:        delaySec,
		SkipHistory:         t.skipHistoryCheck.Checked(),
		Reverse:             t.reverseCheck.Checked(),
		SegmentSize:         1000,
		SegmentDelaySeconds: 10,
	}

	taskName := "正在执行分批转发..."
	if isDryRun {
		taskName = "正在执行试转发 (dry-run)..."
	}
	t.setBusy(true, taskName)

	ctx, cancel := context.WithCancel(context.Background())
	t.cancelTask = cancel

	go func() {
		defer t.mw.Synchronize(func() {
			t.setBusy(false, "")
		})

		err := tgtransfer.StartForward(ctx, t.cfg, opt, func(line string) {
			t.mw.Synchronize(func() {
				t.logf("%s", line)
			})
		})

		t.mw.Synchronize(func() {
			if err != nil {
				if ctx.Err() != nil {
					t.logf("转发任务已被手动中止。")
					walk.MsgBox(t.mw, "已停止", "转发任务已安全中止，已成功记录已写入历史。", walk.MsgBoxIconInformation)
				} else {
					t.logf("转发任务失败: %v", err)
					walk.MsgBox(t.mw, "转发异常", fmt.Sprintf("%v", err), walk.MsgBoxIconError)
				}
				return
			}
			if isDryRun {
				walk.MsgBox(t.mw, "试转发完成", "试转发模拟完成，未产生实际发送。", walk.MsgBoxIconInformation)
			} else {
				walk.MsgBox(t.mw, "正式转发完成", "选定的消息已全部分批发送完毕！", walk.MsgBoxIconInformation)
			}
		})
	}()
}

// onStopTask 中止当前正在运行的长耗时后台任务。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onStopTask() {
	if t.cancelTask != nil {
		t.logf("正在请求停止任务，请稍候……")
		t.cancelTask()
	}
}

// onClearHistory 清空当前来源到当前目标群组的已发历史记录。
//
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) onClearHistory() {
	idx := t.exportContentBox.CurrentIndex()
	if idx < 0 || idx >= len(t.exportRecords) {
		walk.MsgBox(t.mw, "提示", "请选择待清空历史对应的导出内容。", walk.MsgBoxIconInformation)
		return
	}
	sourceID := t.exportRecords[idx].SourceId

	targetText := strings.TrimSpace(t.targetCombo.Text())
	if targetText == "" {
		walk.MsgBox(t.mw, "提示", "请选择或输入目标群组。", walk.MsgBoxIconInformation)
		return
	}
	targetID := tgtransfer.ExtractChatID(targetText)

	msg := fmt.Sprintf("确认清空历史吗？\n\n来源 ID: %s\n目标: %s\n清空后再次转发时将不再跳过旧消息。", sourceID, targetID)
	if walk.MsgBox(t.mw, "确认清空历史", msg, walk.MsgBoxIconWarning|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		return
	}

	if err := tgtransfer.ClearForwardHistory(t.cfg, sourceID, targetID); err != nil {
		t.logf("清空历史记录失败: %v", err)
		return
	}
	t.logf("✓ 已成功清空 [%s -> %s] 的转发历史。", sourceID, targetID)
	walk.MsgBox(t.mw, "提示", "转发历史已清空！", walk.MsgBoxIconInformation)
}

// setBusy 控制界面按钮可用性与执行状态文字。
//
// [参数] busy: 是否繁忙；statusText: 正在执行的描述文本
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) setBusy(busy bool, statusText string) {
	btns := []*walk.PushButton{
		t.loginBtn, t.refreshChatBtn, t.exportBtn,
		t.clearExportBtn, t.forwardBtn, t.clearHistoryBtn,
	}
	for _, b := range btns {
		if b != nil {
			b.SetEnabled(!busy)
		}
	}

	if t.stopBtn != nil {
		t.stopBtn.SetEnabled(busy)
	}

	if busy {
		t.statusLabel.SetText(statusText)
		t.statusLabel.SetTextColor(walk.RGB(0, 102, 204))
	} else {
		t.statusLabel.SetText("就绪 (空闲)")
		t.statusLabel.SetTextColor(walk.RGB(60, 60, 60))
		t.cancelTask = nil
	}
}

// logf 向日志窗口安全追加带时间戳的格式化日志。
//
// [参数] format: 格式化串；args: 格式化参数
// 最近修改时间: 2026-09-13
func (t *TgTransferTool) logf(format string, args ...interface{}) {
	if t.logView == nil {
		return
	}
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s\r\n", ts, fmt.Sprintf(format, args...))

	// 控制日志总长度，防止内存泄漏和渲染卡顿
	const maxLen = 80000
	curText := t.logView.Text()
	if len(curText)+len(line) > maxLen {
		curText = curText[len(curText)-50000:]
		t.logView.SetText(curText)
	}
	t.logView.AppendText(line)
}
