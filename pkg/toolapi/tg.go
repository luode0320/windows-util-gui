package toolapi

import (
	"context"
	"sync"

	"windows-util-gui/internal/tools/tgtransfer"
)

// Chat 透传业务层聊天结构，供绑定层与前端直接使用而不触碰 internal。
// Chat 是聊天条目的前端 DTO：JSON 键为小驼峰，与冻结的前端契约一致，
// 不直接透传业务层结构体（其无 json 标签，键名是大写导出字段）。
type Chat struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Topic    string `json:"topic"`
}

// ExportRecord 是导出记录的前端 DTO：键名小驼峰对齐前端契约。
type ExportRecord struct {
	CreatedAt    string `json:"createdAt"`
	SourceType   string `json:"sourceType"`
	SourceName   string `json:"sourceName"`
	SourceID     string `json:"sourceId"`
	ExportMode   string `json:"exportMode"`
	MessageCount int    `json:"messageCount"`
	Path         string `json:"path"`
}

// newChatDTO 把业务层聊天结构转换为前端 DTO。
//
// [参数] c: 业务层聊天条目
// [返回] 前端聊天 DTO
// 最近修改时间: 2026-09-13
func newChatDTO(c tgtransfer.Chat) Chat {
	return Chat{ID: c.ID, Type: c.Type, Name: c.VisibleName, Username: c.Username, Topic: c.Topics}
}

// newRecordDTO 把业务层导出记录转换为前端 DTO。
//
// [参数] r: 业务层导出记录
// [返回] 前端导出记录 DTO
// 最近修改时间: 2026-09-13
func newRecordDTO(r tgtransfer.ExportRecord) ExportRecord {
	return ExportRecord{
		CreatedAt: r.CreatedAt, SourceType: r.SourceType, SourceName: r.SourceName,
		SourceID: r.SourceId, ExportMode: r.ExportMode,
		MessageCount: r.MessageCount, Path: r.Path,
	}
}

// tgOnce 惰性初始化 tgtransfer 默认配置，避免包加载期即触发 tdl.exe 释放等副作用。
var tgOnce sync.Once
var tgCfg *tgtransfer.Config

// tgMu 保护当前长任务的 ctx 取消槽与登录会话引用，保证多长任务互斥安全。
var tgMu sync.Mutex

// tgSlot 是当前长任务的可取消 ctx 引用，用指针身份区分不同任务，
// 避免 context.CancelFunc 不可比较导致无法判断"结束的是否仍是当前槽"。
type tgSlot struct {
	cancel context.CancelFunc
}

var tgTaskRef *tgSlot
var tgLoginSession *tgtransfer.QrLoginSession

// tgConfig 返回（惰性创建）tgtransfer 默认配置单例。
func tgConfig() *tgtransfer.Config {
	tgOnce.Do(func() {
		tgCfg = tgtransfer.DefaultConfig()
	})
	return tgCfg
}

// tgBeginTask 为一次新长任务申请可取消 ctx，并取消/替换上一项仍在进行的任务，
// 返回 ctx 与"本任务结束时调用"的释放闭包（按指针身份判断是否是当前槽）。
func tgBeginTask() (context.Context, func()) {
	tgMu.Lock()
	defer tgMu.Unlock()
	if tgTaskRef != nil {
		tgTaskRef.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ref := &tgSlot{cancel: cancel}
	tgTaskRef = ref
	return ctx, func() {
		tgMu.Lock()
		defer tgMu.Unlock()
		if tgTaskRef == ref {
			tgTaskRef = nil
		}
	}
}

// tgEndTask 为 tgBeginTask 返回的释放闭包别名，任务结束时清空当前槽。
func tgEndTask(release func()) {
	release()
}

// TgState 描述 TG 工具当前运行状态的公开快照，供前端一次性渲染。
type TgState struct {
	LoggedIn   bool       `json:"loggedIn"`
	TdlPath    string     `json:"tdlPath"`
	TdlBundled bool       `json:"tdlBundled"`
	Proxy      string     `json:"proxy"`
	Chats      []Chat     `json:"chats"`
	Records    []ExportRecord `json:"records"`
}

// TgGetState 收集 TG 工具当前状态：登录态、tdl 路径、嵌入释放状态、代理、聊天缓存与导出记录。
//
// 内部调用 EnsureBundledTdl 确认嵌入 tdl 已释放；LoadCachedChats 容错（读不到缓存返回空切片）；
// GetAllAvailableExportRecords 合并持久化与数据目录下的可用 JSON。
//
// [返回] 当前状态快照
// 最近修改时间: 2026-09-13
func TgGetState() TgState {
	cfg := tgConfig()
	_, bundlingErr := cfg.EnsureBundledTdl()
	chats, _ := tgtransfer.LoadCachedChats(cfg)
	records := tgtransfer.GetAllAvailableExportRecords(cfg)
	dtos := make([]ExportRecord, 0, len(records))
	for _, r := range records {
		dtos = append(dtos, newRecordDTO(r))
	}
	chatDtos := make([]Chat, 0, len(chats))
	for _, c := range chats {
		chatDtos = append(chatDtos, newChatDTO(c))
	}
	return TgState{
		LoggedIn:   tgtransfer.IsLoggedIn(cfg),
		TdlPath:    cfg.TdlPath,
		TdlBundled: bundlingErr == nil,
		Proxy:      cfg.Proxy,
		Chats:      chatDtos,
		Records:    dtos,
	}
}

// TgSetProxy 写入代理配置并即时做网络预检，代理非法或不可达时返回 error。
//
// [参数] proxy: 代理地址，如 socks5://127.0.0.1:7890
// [返回] 校验或网络探测失败返回 error
// 最近修改时间: 2026-09-13
// TgSetProxy 写入代理配置并即时做网络预检；代理非法或不可达时返回 error。
//
// 入参允许只填「IP:端口」：归一化（补 socks5:// 协议头）后再落库与校验，
// 保证界面显示值与实际传给 tdl 的 --proxy 参数一致。
//
// [参数] proxy: 代理地址，如 127.0.0.1:7890 或 socks5://127.0.0.1:7890
// [返回] 校验或网络探测失败返回 error
// 最近修改时间: 2026-09-14
func TgSetProxy(proxy string) error {
	cfg := tgConfig()
	normalized := tgtransfer.NormalizeProxy(proxy)
	cfg.Proxy = normalized
	return tgtransfer.ValidateNetwork(normalized)
}

// TgValidateNetwork 按给定代理探测连通性，返回可读的结果说明。
//
// 优先使用入参（界面输入框当前值）：用户刚填完地址就点"网络测试"时，
// 测的应当是他填的这个地址，而不要求先保存；入参为空时回退到已保存配置，
// 两者都为空则执行直连 Telegram 探测。
//
// [参数] proxy: 待测代理，允许只填 IP:端口；为空时用已保存配置
// [返回] 连通时返回可读说明；不通或地址非法时返回 error
// 最近修改时间: 2026-09-14
func TgValidateNetwork(proxy string) (string, error) {
	target := tgtransfer.NormalizeProxy(proxy)
	if target == "" {
		target = tgConfig().Proxy
	}
	if err := tgtransfer.ValidateNetwork(target); err != nil {
		return "", err
	}
	if target == "" {
		return "直连 api.telegram.org:443 连通正常", nil
	}
	return "代理 " + target + " 连通正常", nil
}

// TgStartLogin 启动扫码登录长任务：先做网络预检，失败立即返回 error；
// 通过则申请可取消 ctx 并异步执行 StartQrLogin——二维码就绪经 onQr 回调、
// 手机确认授权经 onConfirmed 回调、进度经 logCb、结束经 onDone。
//
// [参数] onQr: 二维码图片路径就绪回调；onConfirmed: 手机确认授权回调；logCb: 实时日志回调；onDone: 任务结束回调（err 为 nil 表示成功）
// [返回] 网络预检失败返回 error；其余情况（含登录失败）经 onDone 异步回传
// 最近修改时间: 2026-09-14
func TgStartLogin(onQr func(imgPath string), onConfirmed func(), logCb func(string), onDone func(err error)) error {
	cfg := tgConfig()
	if err := tgtransfer.ValidateNetwork(cfg.Proxy); err != nil {
		return err
	}

	ctx, cancel := tgBeginTask()
	session := tgtransfer.NewQrLoginSession(cfg)
	tgMu.Lock()
	tgLoginSession = session
	tgMu.Unlock()

	go func() {
		defer tgEndTask(cancel)
		err := session.StartQrLogin(ctx, onQr, onConfirmed, logCb)
		if err == nil {
			// 扫码确认成功后才写登录标记：IsLoggedIn 以标记 + 会话文件双条件判定，
			// 避免把 tdl 初始化的空 Bolt 存储误报为"已登录"
			_ = tgtransfer.MarkLoginActive(cfg)
		}
		if onDone != nil {
			onDone(err)
		}
	}()
	return nil
}

// TgClearSession 清除登录标记与会话文件，用于"重新扫码登录"前的状态重置。
//
// [返回] 清除失败返回 error
// 最近修改时间: 2026-09-13
func TgClearSession() error {
	return tgtransfer.ClearSession(tgConfig())
}

// TgCancel 取消当前正在进行的长任务（登录/导出/转发共用一个可取消 ctx 槽）。
//
// 最近修改时间: 2026-09-13
func TgCancel() {
	tgMu.Lock()
	defer tgMu.Unlock()
	if tgTaskRef != nil {
		tgTaskRef.cancel()
		tgTaskRef = nil
	}
	if tgLoginSession != nil {
		tgLoginSession.Stop()
		tgLoginSession = nil
	}
}

// TgRefreshChats 执行 tdl chat ls 拉取最新聊天列表并写缓存，返回最新列表。
//
// [参数] logCb: 实时日志回调
// [返回] 最新聊天列表；失败返回 error
// 最近修改时间: 2026-09-13
func TgRefreshChats(logCb func(string)) ([]Chat, error) {
	chats, err := tgtransfer.FetchChats(context.Background(), tgConfig(), logCb)
	if err != nil {
		return nil, err
	}
	dtos := make([]Chat, 0, len(chats))
	for _, c := range chats {
		dtos = append(dtos, newChatDTO(c))
	}
	return dtos, nil
}

// TgRecords 返回所有可用导出记录（合并持久化与数据目录下 JSON）。
//
// [返回] 导出记录切片
// 最近修改时间: 2026-09-13
func TgRecords() []ExportRecord {
	records := tgtransfer.GetAllAvailableExportRecords(tgConfig())
	dtos := make([]ExportRecord, 0, len(records))
	for _, r := range records {
		dtos = append(dtos, newRecordDTO(r))
	}
	return dtos
}

// TgDeleteRecord 删除指定导出文件及其登记记录。
//
// [参数] path: 导出 JSON 文件绝对路径
// [返回] 删除失败返回 error
// 最近修改时间: 2026-09-13
func TgDeleteRecord(path string) error {
	return tgtransfer.DeleteExportRecord(tgConfig(), path)
}

// TgExport 启动消息导出长任务：申请可取消 ctx 并异步执行 ExportMessages（内部已登记 ExportRecord）。
//
// [参数] opts: 导出参数；logCb: 实时日志回调；onDone: 任务结束回调
// [返回] 始终返回 nil（错误经 onDone 异步回传）
// 最近修改时间: 2026-09-13
func TgExport(opts tgtransfer.ExportOptions, logCb func(string), onDone func(err error)) error {
	cfg := tgConfig()
	ctx, cancel := tgBeginTask()
	go func() {
		defer tgEndTask(cancel)
		_, err := tgtransfer.ExportMessages(ctx, cfg, opts, logCb)
		if onDone != nil {
			onDone(err)
		}
	}()
	return nil
}

// TgForward 启动分批转发长任务：先归一化默认值，再异步执行 StartForward。
//
// [参数] opts: 转发参数（会被原地归一化）；logCb: 实时日志回调；onDone: 任务结束回调
// [返回] 始终返回 nil（错误经 onDone 异步回传）
// 最近修改时间: 2026-09-13
func TgForward(opts tgtransfer.ForwardOptions, logCb func(string), onDone func(err error)) error {
	tgtransfer.NormalizeForwardOptions(&opts)
	cfg := tgConfig()
	ctx, cancel := tgBeginTask()
	go func() {
		defer tgEndTask(cancel)
		err := tgtransfer.StartForward(ctx, cfg, opts, logCb)
		if onDone != nil {
			onDone(err)
		}
	}()
	return nil
}

// TgExportInput 是前端调用 TgExport 时传入的导出参数结构，字段对齐冻结的前端绑定契约。
type TgExportInput struct {
	IsSavedMessages bool     `json:"isSavedMessages"`
	ChatID          string   `json:"chatId"`
	ChatName        string   `json:"chatName"`
	Mode            string   `json:"mode"`
	LastCount       int      `json:"lastCount"`
	Filters         []string `json:"filters"`
}

// TgForwardInput 是前端调用 TgForward 时传入的转发参数结构，字段对齐冻结的前端绑定契约。
type TgForwardInput struct {
	ExportPath    string `json:"exportPath"`
	Target        string `json:"target"`
	Mode          string `json:"mode"`
	DryRun        bool   `json:"dryRun"`
	SkipForwarded bool   `json:"skipForwarded"`
	ReverseOrder  bool   `json:"reverseOrder"`
	BatchSize     int    `json:"batchSize"`
	DelaySeconds  int    `json:"delaySeconds"`
}

// NewExportOptions 把前端传入的导出参数映射为业务层 ExportOptions。
//
// [参数] in: 前端传入的导出参数
// [返回] 业务层导出选项
// 最近修改时间: 2026-09-13
func NewExportOptions(in TgExportInput) tgtransfer.ExportOptions {
	return tgtransfer.ExportOptions{
		IsSavedMessages: in.IsSavedMessages,
		ChatID:          in.ChatID,
		ChatName:        in.ChatName,
		Mode:            tgtransfer.ExportMode(in.Mode),
		LastCount:       in.LastCount,
		Filters:         in.Filters,
	}
}

// NewForwardOptions 把前端传入的转发参数映射为业务层 ForwardOptions。
//
// 字段名按契约对齐：skipForwarded→SkipHistory、reverseOrder→Reverse。
//
// [参数] in: 前端传入的转发参数
// [返回] 业务层转发选项
// 最近修改时间: 2026-09-13
func NewForwardOptions(in TgForwardInput) tgtransfer.ForwardOptions {
	return tgtransfer.ForwardOptions{
		ExportPath:   in.ExportPath,
		Target:       in.Target,
		Mode:         in.Mode,
		DryRun:       in.DryRun,
		SkipHistory:  in.SkipForwarded,
		Reverse:      in.ReverseOrder,
		BatchSize:    in.BatchSize,
		DelaySeconds: in.DelaySeconds,
	}
}

// TgClearHistory 清除指定源到目标的转发历史记录。
//
// [参数] sourceID: 来源 ID；target: 目标聊天
// [返回] 清除失败返回 error
// 最近修改时间: 2026-09-13
func TgClearHistory(sourceID, target string) error {
	// 与 walk 版行为对齐：记录里的 SourceId 可能是完整展示文本，
	// 清历史前统一提取纯 ID，保证与转发历史写入时的 key 格式一致
	return tgtransfer.ClearForwardHistory(tgConfig(),
		tgtransfer.ExtractChatID(sourceID), tgtransfer.ExtractChatID(target))
}
