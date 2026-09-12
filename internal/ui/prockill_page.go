package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"windows-util-gui/internal/tools/prockill"
	"windows-util-gui/internal/winutil"
)

// queryKinds 是查询方式下拉框的选项，顺序与 prockill.Kind 的取值一一对应。
var queryKinds = []prockill.Kind{prockill.KindPort, prockill.KindPID, prockill.KindName}

// kindHints 是各查询方式对应的输入提示与默认值。
var kindHints = map[prockill.Kind]struct {
	label       string // 输入框前的标签
	placeholder string // 输入框内的灰字提示
}{
	prockill.KindPort: {label: "端口号：", placeholder: "如 8080"},
	prockill.KindPID:  {label: "PID：", placeholder: "如 1234"},
	prockill.KindName: {label: "进程名：", placeholder: "模糊匹配，如 node"},
}

// processModel 是查询结果列表的表格数据源。
type processModel struct {
	walk.TableModelBase
	items []prockill.Process
}

// RowCount 返回表格行数。
//
// [返回] 当前结果条数
// 最近修改时间: 2026-09-12
func (m *processModel) RowCount() int {
	return len(m.items)
}

// Value 返回指定单元格的展示内容。
//
// [参数] row: 行号；col: 列号
// [返回] 单元格文本
// 最近修改时间: 2026-09-12
func (m *processModel) Value(row, col int) interface{} {
	item := m.items[row]
	switch col {
	case 0:
		return item.Origin()
	case 1:
		return item.PID
	case 2:
		return item.Name
	case 3:
		return item.Listening()
	case 4:
		return item.State()
	}
	return ""
}

// reset 用新的查询结果替换表格内容并通知界面刷新。
//
// 注意只调用本方法不足以刷新界面，必须由调用方配合 TableView.Invalidate，原因见 setItems。
//
// [参数] items: 新的进程列表
// 最近修改时间: 2026-09-12
func (m *processModel) reset(items []prockill.Process) {
	m.items = items
	m.PublishRowsReset()
}

// ProcKillTool 是"进程占用查杀"工具，同时覆盖 Windows 宿主与 WSL 发行版。
type ProcKillTool struct {
	mw        *walk.MainWindow
	kindBox   *walk.ComboBox
	valueEdit *walk.LineEdit
	valueTip  *walk.Label
	tableView *walk.TableView
	logView   *walk.TextEdit
	killBtn   *walk.PushButton
	allBtn    *walk.PushButton
	queryBtn  *walk.PushButton
	model     *processModel
}

// NewProcKillTool 创建进程查杀工具实例。
//
// [返回] 工具实例
// 最近修改时间: 2026-09-12
func NewProcKillTool() *ProcKillTool {
	return &ProcKillTool{model: &processModel{}}
}

// Name 返回工具名称。
//
// [返回] 工具在菜单中的展示名
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) Name() string {
	return "进程占用查杀"
}

// Description 返回工具说明。
//
// [返回] 工具页顶部的一句话说明
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) Description() string {
	return "按端口、PID 或进程名查找 Windows 与 WSL 中的进程并结束它们"
}

// Build 构建进程查杀工具的页面。
//
// [参数] mw: 主窗口；parent: 页面父容器
// [返回] 页面根容器；构建失败时返回 error
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) Build(mw *walk.MainWindow, parent walk.Container) (*walk.Composite, error) {
	t.mw = mw

	kindNames := make([]string, 0, len(queryKinds))
	for _, kind := range queryKinds {
		kindNames = append(kindNames, "按"+kind.String())
	}

	var root *walk.Composite
	page := d.Composite{
		AssignTo: &root,
		Layout:   d.VBox{MarginsZero: false},
		Children: []d.Widget{
			d.Label{
				Text:      t.Description(),
				TextColor: walk.RGB(90, 90, 90),
				MaxSize:   d.Size{Height: 20},
			},
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.Label{Text: "查询方式："},
					d.ComboBox{
						AssignTo:              &t.kindBox,
						Model:                 kindNames,
						CurrentIndex:          0,
						MinSize:               d.Size{Width: 110},
						MaxSize:               d.Size{Width: 110},
						OnCurrentIndexChanged: t.onKindChanged,
					},
					d.Label{
						AssignTo: &t.valueTip,
						Text:     kindHints[prockill.KindPort].label,
					},
					// 用 LineEdit 而不是 NumberEdit：后者在清空、全选覆盖、粘贴时会把输入拼接到旧值上
					// 并因越界静默回退，用户改内容时经常改不动；这里统一为纯文本输入 + 提交时校验
					d.LineEdit{
						AssignTo:  &t.valueEdit,
						CueBanner: kindHints[prockill.KindPort].placeholder,
						MaxLength: 64,
						MinSize:   d.Size{Width: 160},
						MaxSize:   d.Size{Width: 160},
						OnKeyDown: func(key walk.Key) {
							if key == walk.KeyReturn {
								t.onQuery()
							}
						},
					},
					d.PushButton{
						AssignTo:  &t.queryBtn,
						Text:      "查询",
						OnClicked: t.onQuery,
					},
					d.PushButton{
						AssignTo:  &t.killBtn,
						Text:      "结束选中进程",
						OnClicked: t.onKillSelected,
					},
					d.PushButton{
						AssignTo:  &t.allBtn,
						Text:      "结束全部结果",
						OnClicked: t.onKillAll,
					},
					d.HSpacer{},
				},
			},
			d.TableView{
				AssignTo:         &t.tableView,
				Model:            t.model,
				MultiSelection:   true,
				AlternatingRowBG: true,
				MinSize:          d.Size{Height: 220},
				Columns: []d.TableViewColumn{
					{Title: "来源", Width: 150},
					{Title: "PID", Width: 80},
					{Title: "进程名", Width: 220},
					{Title: "监听端点", Width: 260},
					{Title: "状态", Width: 100},
				},
			},
			d.Label{Text: "执行日志："},
			d.TextEdit{
				AssignTo: &t.logView,
				ReadOnly: true,
				VScroll:  true,
				MinSize:  d.Size{Height: 140},
			},
		},
	}

	if err := page.Create(d.NewBuilder(parent)); err != nil {
		return nil, fmt.Errorf("构建进程查杀页面失败: %w", err)
	}

	// 权限状态在页面就绪时提示一次，避免用户在操作失败后才发现是权限问题
	if winutil.IsElevated() {
		t.logf("已以管理员身份运行，可结束系统级进程。")
	} else {
		t.logf("警告：当前不是管理员身份，结束系统级进程可能失败，建议以管理员身份重新启动。")
	}
	t.logf("提示：WSL2 中监听的端口会由 wslrelay 中转到 Windows 侧，两侧可能各出现一条记录，需要一并处理才算真正释放。")

	return root, nil
}

// onKindChanged 在切换查询方式时同步输入框的标签与提示。
//
// 同时清空上一次的输入与结果：端口号、PID、进程名之间互不通用，
// 留着上一种方式的内容只会让人以为当前结果仍然有效。
//
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) onKindChanged() {
	hint := kindHints[t.currentKind()]
	t.valueTip.SetText(hint.label)
	t.valueEdit.SetCueBanner(hint.placeholder)
	t.valueEdit.SetText("")
	t.setItems(nil)
}

// currentKind 返回当前选中的查询方式。
//
// [返回] 查询方式；下拉框状态异常时回退到按端口查询
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) currentKind() prockill.Kind {
	index := t.kindBox.CurrentIndex()
	if index < 0 || index >= len(queryKinds) {
		return prockill.KindPort
	}
	return queryKinds[index]
}

// onQuery 处理"查询"按钮点击。
//
// 查询会执行 netstat 与 wsl.exe，耗时可能到秒级，因此放在后台协程执行，
// 结果通过 Synchronize 回到 UI 线程刷新，避免界面假死。
//
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) onQuery() {
	criteria := prockill.Criteria{
		Kind:  t.currentKind(),
		Value: strings.TrimSpace(t.valueEdit.Text()),
	}

	// 先清空上一次的结果：查询要跑 netstat 与 wsl.exe，耗时可达数秒，
	// 期间若继续展示上一次的记录，很容易被当成当前条件的查询结果
	t.setItems(nil)

	t.setBusy(true)
	t.logf("正在按%s查询……", criteria.Describe())

	go func() {
		items, warnings, queryErr := prockill.Query(criteria)

		t.mw.Synchronize(func() {
			defer t.setBusy(false)

			if queryErr != nil {
				// 查询失败时清空表格，避免旧结果被误读成当前条件的查询结果
				t.setItems(nil)
				t.logf("查询失败：%v", queryErr)
				return
			}
			for _, warning := range warnings {
				t.logf("提醒：%s", warning)
			}

			t.setItems(items)
			if len(items) == 0 {
				t.logf("没有找到匹配%s的进程。", criteria.Describe())
				return
			}
			t.logf("共找到 %d 个匹配%s的进程。", len(items), criteria.Describe())
		})
	}()
}

// onKillSelected 处理"结束选中进程"按钮点击。
//
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) onKillSelected() {
	indexes := t.tableView.SelectedIndexes()
	if len(indexes) == 0 {
		walk.MsgBox(t.mw, "未选中记录", "请先在列表中选择要结束的进程。", walk.MsgBoxIconInformation)
		return
	}

	targets := make([]prockill.Process, 0, len(indexes))
	for _, index := range indexes {
		targets = append(targets, t.model.items[index])
	}
	t.confirmAndKill(targets)
}

// onKillAll 处理"结束全部结果"按钮点击。
//
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) onKillAll() {
	if len(t.model.items) == 0 {
		walk.MsgBox(t.mw, "没有可结束的进程", "请先执行一次查询。", walk.MsgBoxIconInformation)
		return
	}
	t.confirmAndKill(append([]prockill.Process(nil), t.model.items...))
}

// confirmAndKill 弹出二次确认后结束目标进程。
//
// 结束进程不可撤销，且按进程名模糊匹配很容易一次选中多个同名进程，
// 因此必须逐条列出目标让用户确认。
//
// [参数] targets: 待结束的进程
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) confirmAndKill(targets []prockill.Process) {
	// 1. 把目标逐条展示在确认框中，让用户看清到底要杀什么
	var detail strings.Builder
	detail.WriteString("即将强制结束以下进程，该操作不可撤销：\n\n")
	for _, item := range targets {
		detail.WriteString(fmt.Sprintf("· [%s] PID %d  %s  (%s)\n",
			item.Origin(), item.PID, item.Name, item.Listening()))
	}
	detail.WriteString("\n确认继续？")

	if walk.MsgBox(t.mw, "确认结束进程", detail.String(),
		walk.MsgBoxIconWarning|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		t.logf("已取消结束进程操作。")
		return
	}

	// 2. 后台逐条结束并统计结果，避免逐次弹窗打断用户
	t.setBusy(true)
	go func() {
		type outcome struct {
			item prockill.Process
			err  error
		}
		outcomes := make([]outcome, 0, len(targets))
		for _, item := range targets {
			outcomes = append(outcomes, outcome{item: item, err: prockill.Kill(item)})
		}

		t.mw.Synchronize(func() {
			var failed int
			for _, res := range outcomes {
				if res.err != nil {
					failed++
					t.logf("失败：[%s] PID %d %s —— %v", res.item.Origin(), res.item.PID, res.item.Name, res.err)
					continue
				}
				t.logf("已结束：[%s] PID %d %s", res.item.Origin(), res.item.PID, res.item.Name)
			}
			t.logf("结束进程完成，成功 %d 条，失败 %d 条。", len(outcomes)-failed, failed)
			t.setBusy(false)

			// 3. 结束后自动重查，直观确认进程是否真正消失
			t.onQuery()
		})
	}()
}

// setItems 用新的查询结果替换表格内容并强制重绘。
//
// walk 响应 PublishRowsReset 时只发了一条带 LVSICF_NOINVALIDATEALL 的 LVM_SETITEMCOUNT
// （见 tableview.go 的 rowsReset 处理与 setItemCount），全程没有任何 invalidate。
// 该标志的语义就是"不要让控件的所有项失效"，于是虚拟列表会继续沿用已绘制行的缓存：
// 换一批数据后行数虽然对了，屏幕上却残留着上一次查询的行内容——
// 表现为查完条件 A 再查条件 B，表格里还挂着 A 的记录，只有末尾少数行是新的。
//
// [参数] items: 新的查询结果
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) setItems(items []prockill.Process) {
	t.model.reset(items)
	if err := t.tableView.Invalidate(); err != nil {
		t.logf("表格重绘失败，显示的可能不是最新结果：%v", err)
	}
}

// setBusy 在后台任务执行期间禁用操作按钮，避免重复触发。
//
// [参数] busy: true 表示进入忙碌状态
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) setBusy(busy bool) {
	t.queryBtn.SetEnabled(!busy)
	t.killBtn.SetEnabled(!busy)
	t.allBtn.SetEnabled(!busy)
}

// logf 向日志区追加一行带时间戳的记录。
//
// [参数] format: 格式串；args: 格式参数
// 最近修改时间: 2026-09-12
func (t *ProcKillTool) logf(format string, args ...interface{}) {
	line := fmt.Sprintf("[%s] %s\r\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	t.logView.AppendText(line)
}
