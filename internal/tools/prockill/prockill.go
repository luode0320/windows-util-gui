// Package prockill 提供进程查询与清理能力，同时覆盖 Windows 宿主与 WSL 发行版。
//
// 支持按端口、PID、进程名三种方式定位进程。三者共用同一份采集结果：
// 先把一侧的全部进程与监听端点取回来，再按条件过滤，这样按 PID 或进程名查到的进程
// 也能顺带看到它监听了哪些端口——决定要不要结束它时，这是最关键的信息。
//
// WSL2 运行在独立网络命名空间中，发行版内监听的端口会由 wslrelay/wslhost 中转到 Windows 侧，
// 因此同一个端口经常在两侧各出现一条记录，必须两边都查、两边都能杀，才算真正释放端口。
package prockill

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 进程来源标识，Windows 侧固定为空发行版名。
const (
	SourceWindows = "Windows"
	SourceWSL     = "WSL"
)

// minNameLength 是进程名模糊搜索的最短长度。
// 单个字符能匹配到几乎所有进程，配合"结束全部"极易误伤，因此不接受。
const minNameLength = 2

// Kind 是查询方式。
type Kind int

const (
	KindPort Kind = iota // 按监听端口查询
	KindPID              // 按进程 ID 查询
	KindName             // 按进程名模糊查询
)

// String 返回查询方式的中文名称，用于日志与提示。
//
// [返回] 查询方式名称
// 最近修改时间: 2026-09-12
func (k Kind) String() string {
	switch k {
	case KindPort:
		return "端口"
	case KindPID:
		return "PID"
	case KindName:
		return "进程名"
	}
	return "未知条件"
}

// Endpoint 表示进程的一个监听端点。
type Endpoint struct {
	Protocol string // TCP / UDP
	Address  string // 本地监听地址
	State    string // 连接状态，UDP 通常为空
}

// String 返回端点的单行展示形式。
//
// [返回] 形如 "TCP 0.0.0.0:8080" 的文本
// 最近修改时间: 2026-09-12
func (e Endpoint) String() string {
	return fmt.Sprintf("%s %s", e.Protocol, e.Address)
}

// Process 表示一个可被结束的进程及其监听端点。
type Process struct {
	Source    string     // 来源：Windows 或 WSL
	Distro    string     // WSL 发行版名称；Windows 侧为空
	PID       uint32     // 进程 ID
	Name      string     // 进程名
	Endpoints []Endpoint // 监听端点，没有监听任何端口时为空
}

// Origin 返回用于界面展示的来源描述。
//
// [返回] Windows 侧返回 "Windows"，WSL 侧返回 "WSL:<发行版名>"
// 最近修改时间: 2026-09-12
func (p Process) Origin() string {
	if p.Source == SourceWSL {
		return fmt.Sprintf("%s:%s", SourceWSL, p.Distro)
	}
	return SourceWindows
}

// Listening 返回监听端点的汇总文本。
//
// [返回] 多个端点用顿号连接；没有监听端口时返回 "-"
// 最近修改时间: 2026-09-12
func (p Process) Listening() string {
	if len(p.Endpoints) == 0 {
		return "-"
	}

	items := make([]string, 0, len(p.Endpoints))
	for _, endpoint := range p.Endpoints {
		items = append(items, endpoint.String())
	}
	return strings.Join(items, "、")
}

// State 返回首个端点的连接状态，供表格单独成列展示。
//
// [返回] 首个端点的状态；没有监听端口时返回空串
// 最近修改时间: 2026-09-12
func (p Process) State() string {
	if len(p.Endpoints) == 0 {
		return ""
	}
	return p.Endpoints[0].State
}

// Criteria 是一次查询的条件。
type Criteria struct {
	Kind  Kind   // 查询方式
	Value string // 查询值：端口号、PID 或进程名片段
}

// Describe 返回查询条件的中文描述，用于日志与确认提示。
//
// [返回] 形如 "端口 8080" 的文本
// 最近修改时间: 2026-09-12
func (c Criteria) Describe() string {
	return fmt.Sprintf("%s %s", c.Kind, c.Value)
}

// normalize 校验并规整查询条件。
//
// [返回] 规整后的条件；条件非法时返回 error
// 最近修改时间: 2026-09-12
func (c Criteria) normalize() (Criteria, error) {
	value := strings.TrimSpace(c.Value)
	if value == "" {
		return c, fmt.Errorf("请先填写要查询的%s。", c.Kind)
	}

	switch c.Kind {
	case KindPort:
		port, err := strconv.Atoi(value)
		if err != nil {
			return c, fmt.Errorf("“%s”不是有效的端口号，请填写 1-65535 之间的数字。", value)
		}
		if port < 1 || port > 65535 {
			return c, fmt.Errorf("端口号 %d 超出范围，有效范围为 1-65535。", port)
		}
	case KindPID:
		pid, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return c, fmt.Errorf("“%s”不是有效的 PID，请填写正整数。", value)
		}
		if pid == 0 {
			return c, fmt.Errorf("PID 必须大于 0。")
		}
	case KindName:
		// 太短的关键字会匹配到大量无关进程，配合"结束全部"极易误伤
		if len([]rune(value)) < minNameLength {
			return c, fmt.Errorf("进程名关键字至少需要 %d 个字符，避免匹配到过多无关进程。", minNameLength)
		}
	default:
		return c, fmt.Errorf("不支持的查询方式。")
	}

	c.Value = value
	return c, nil
}

// matches 判断一个进程是否命中查询条件。
//
// 按端口查询时会同时收窄 Endpoints，只保留命中的端点，让"为什么这条会出现"一目了然；
// 按 PID 或进程名查询时保留全部端点，便于评估结束该进程的影响面。
//
// [参数] process: 待判断的进程
// [返回] 命中时返回收窄后的进程与 true
// 最近修改时间: 2026-09-12
func (c Criteria) matches(process Process) (Process, bool) {
	switch c.Kind {
	case KindPort:
		suffix := ":" + c.Value
		hit := make([]Endpoint, 0, len(process.Endpoints))
		for _, endpoint := range process.Endpoints {
			if strings.HasSuffix(endpoint.Address, suffix) {
				hit = append(hit, endpoint)
			}
		}
		if len(hit) == 0 {
			return process, false
		}
		process.Endpoints = hit
		return process, true

	case KindPID:
		return process, strconv.FormatUint(uint64(process.PID), 10) == c.Value

	case KindName:
		return process, strings.Contains(strings.ToLower(process.Name), strings.ToLower(c.Value))
	}
	return process, false
}

// Query 按条件查询 Windows 与所有 WSL 发行版中的进程。
//
// 两侧查询互不依赖，因此并发执行；任意一侧失败都不影响另一侧的结果，
// 失败信息以警告形式返回，由界面展示而不是直接中断整个查询。
//
// [参数] criteria: 查询条件
// [返回] 命中的进程列表、查询过程中的警告信息、条件非法时的 error
// 最近修改时间: 2026-09-12
func Query(criteria Criteria) ([]Process, []string, error) {
	criteria, err := criteria.normalize()
	if err != nil {
		return nil, nil, err
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		processes []Process
		warnings  []string
	)

	// 1. 收敛两侧查询结果，统一加锁写入，避免并发写切片
	collect := func(items []Process, warns []string) {
		mu.Lock()
		defer mu.Unlock()
		processes = append(processes, items...)
		warnings = append(warnings, warns...)
	}

	// 2. Windows 宿主侧采集
	wg.Add(1)
	go func() {
		defer wg.Done()
		items, err := collectWindows()
		if err != nil {
			collect(nil, []string{fmt.Sprintf("Windows 侧查询失败: %v", err)})
			return
		}
		collect(filter(items, criteria), nil)
	}()

	// 3. WSL 侧采集，内部会自行遍历全部运行中的发行版
	wg.Add(1)
	go func() {
		defer wg.Done()
		items, warns := collectWSL()
		collect(filter(items, criteria), warns)
	}()

	wg.Wait()

	// 4. 固定排序，保证同一查询多次执行的展示顺序一致
	sort.SliceStable(processes, func(i, j int) bool {
		if processes[i].Origin() != processes[j].Origin() {
			return processes[i].Origin() < processes[j].Origin()
		}
		return processes[i].PID < processes[j].PID
	})

	return processes, warnings, nil
}

// filter 按条件筛选采集到的进程。
//
// [参数] items: 采集到的全部进程；criteria: 已规整的查询条件
// [返回] 命中的进程列表
// 最近修改时间: 2026-09-12
func filter(items []Process, criteria Criteria) []Process {
	result := make([]Process, 0, len(items))
	for _, item := range items {
		if matched, ok := criteria.matches(item); ok {
			result = append(result, matched)
		}
	}
	return result
}

// Kill 结束指定进程。
//
// [参数] process: 待结束的进程
// [返回] 命中保护名单或结束失败时返回 error
// 最近修改时间: 2026-09-12
func Kill(process Process) error {
	if reason, protected := IsProtected(process); protected {
		return fmt.Errorf("%s", reason)
	}

	if process.Source == SourceWSL {
		return killWSL(process.Distro, process.PID)
	}
	return killWindows(process.PID)
}
