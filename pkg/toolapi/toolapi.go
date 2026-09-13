// Package toolapi 是 internal/tools 的公开桥接层。
//
// Go 规定 internal/ 包只允许同模块内导入，而 Wails 前端绑定工程是独立模块，
// 因此跨模块复用必须经由一个公开门面。业务实现仍全部留在 internal/，
// 这里只做类型转换与透传，保证"业务层零改动"的同时不把内部结构暴露给外部模块。
package toolapi

import (
	"fmt"

	"windows-util-gui/internal/tools/prockill"
	"windows-util-gui/internal/winutil"
)

// kindMap 将对外暴露的字符串查询方式映射到业务层 Kind，白名单集中在此，
// 既避免校验逻辑散落在调用处，也让"哪些 kind 合法"有一处权威来源。
var kindMap = map[string]prockill.Kind{
	"port": prockill.KindPort,
	"pid":  prockill.KindPID,
	"name": prockill.KindName,
}

// Row 是一次查询结果行的公开展示结构，字段与前端表格列一一对应。
type Row struct {
	Origin    string `json:"origin"`    // 来源：Windows 或 WSL:<发行版>（由 Process.Origin 派生，保持现有逻辑不变）
	Source    string `json:"source"`    // 原始来源标识：Windows 或 WSL（透传 prockill.Process.Source）
	Distro    string `json:"distro"`    // WSL 发行版名称；Windows 侧为空（透传 prockill.Process.Distro）
	PID       uint32 `json:"pid"`       // 进程 ID
	Name      string `json:"name"`      // 进程名
	Listening string `json:"listening"` // 监听端点汇总文本
	State     string `json:"state"`     // 首个端点状态
}

// Query 是统一的公开查询入口，按 kind 支持端口/ PID/ 进程名三种方式。
//
// 桥接层只对 kind 做白名单校验，value 与 warnings 的合法性完全交给业务层
// prockill 统一处理（含端口范围、PID 正整数、进程名最短长度等），
// 这里不重复实现任何校验逻辑，保证校验规则只有一处、业务层零改动。
//
// [参数] kind: 查询方式，仅接受 "port"/"pid"/"name"；value: 查询值（端口号/ PID/ 进程名）
// [返回] 结果行、提醒信息（如 wslrelay 双侧提示）、kind 非法时的 error
// 最近修改时间: 2026-09-13
func Query(kind, value string) ([]Row, []string, error) {
	mapped, ok := kindMap[kind]
	if !ok {
		return nil, nil, fmt.Errorf("不支持的查询方式: %s", kind)
	}

	items, warnings, err := prockill.Query(prockill.Criteria{
		Kind:  mapped,
		Value: value,
	})
	if err != nil {
		return nil, nil, err
	}

	rows := make([]Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, Row{
			Origin:    item.Origin(),
			Source:    item.Source,
			Distro:    item.Distro,
			PID:       item.PID,
			Name:      item.Name,
			Listening: item.Listening(),
			State:     item.State(),
		})
	}
	return rows, warnings, nil
}

// KillOutcome 是一条进程结束请求的执行结果，用于把"逐条成败"回传给前端。
//
// 批次结束采用"一条失败不中断整批"的策略：每条记录无论成败都生成一个 KillOutcome，
// 前端据此逐行显示成功/失败，而不是只要有一条失败就整批报错。
type KillOutcome struct {
	Row    Row   `json:"row"`            // 本次尝试结束的进程行，原样回传便于前端定位
	Success bool  `json:"success"`       // 是否成功结束
	Error  string `json:"error,omitempty"` // 失败原因；成功时省略
}

// Kill 批量结束进程，逐条把对外 Row 还原回业务层 prockill.Process 后调用 prockill.Kill。
//
// 还原时只搬运 Source/Distro/PID/Name 四个字段：这四项是结束进程所需的全部标识，
// 其余展示字段（监听端点等）不参与结束逻辑，故不反向写回 Process，保持桥接层职责单一。
// 采用"逐条记录、不中断"语义——某条失败（如受保护进程、权限不足）只记入该条 Error，
// 后续条目继续处理，让调用方拿到完整批次结果。
//
// [参数] rows: 待结束的进程行列表
// [返回] 与 rows 一一对应的执行结果；顺序与输入一致
// 最近修改时间: 2026-09-13
func Kill(rows []Row) []KillOutcome {
	outcomes := make([]KillOutcome, 0, len(rows))
	for _, r := range rows {
		process := prockill.Process{
			Source: r.Source,
			Distro: r.Distro,
			PID:    r.PID,
			Name:   r.Name,
		}
		if err := prockill.Kill(process); err != nil {
			outcomes = append(outcomes, KillOutcome{Row: r, Success: false, Error: err.Error()})
			continue
		}
		outcomes = append(outcomes, KillOutcome{Row: r, Success: true})
	}
	return outcomes
}

// IsElevated 返回当前进程是否以管理员身份运行。
//
// [返回] true 表示管理员权限
// 最近修改时间: 2026-09-13
func IsElevated() bool {
	return winutil.IsElevated()
}

// ShouldSkipElevate 判断本次启动是否要求跳过自提权（命令行带 -no-elevate）。
//
// 调试场景下以普通权限直接运行、不把自己重新拉起成独立提权进程时使用。
//
// [返回] true 表示跳过自提权
// 最近修改时间: 2026-09-13
func ShouldSkipElevate() bool {
	return winutil.ShouldSkipElevate()
}

// RelaunchAsAdmin 通过 ShellExecute runas 拉起一个管理员实例。
//
// [返回] 拉起成功返回 nil，调用方应立即退出当前实例；用户拒绝 UAC 等失败场景返回 error
// 最近修改时间: 2026-09-13
func RelaunchAsAdmin() error {
	return winutil.RelaunchAsAdmin()
}
