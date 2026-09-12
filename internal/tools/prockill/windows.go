package prockill

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"windows-util-gui/internal/winutil"
)

// collectWindows 采集 Windows 宿主上的全部进程及其监听端点。
//
// [返回] 进程列表；进程快照或 netstat 执行失败时返回 error
// 最近修改时间: 2026-09-12
func collectWindows() ([]Process, error) {
	names, err := enumWindowsProcesses()
	if err != nil {
		return nil, err
	}

	endpoints, err := collectWindowsEndpoints()
	if err != nil {
		return nil, err
	}

	// 快照可能因权限漏掉个别进程，而 netstat 仍能报出它们的 PID；
	// 这里逐个补齐名称，避免正在占用端口的进程从结果里消失
	for pid := range endpoints {
		if _, ok := names[pid]; ok {
			continue
		}
		name, nameErr := winutil.ProcessName(pid)
		if nameErr != nil {
			name = "（无法读取进程名，可能需要管理员权限）"
		}
		names[pid] = name
	}

	result := make([]Process, 0, len(names))
	for pid, name := range names {
		result = append(result, Process{
			Source:    SourceWindows,
			PID:       pid,
			Name:      name,
			Endpoints: endpoints[pid],
		})
	}
	return result, nil
}

// enumWindowsProcesses 枚举当前全部进程的 PID 与进程名。
//
// 走 Toolhelp 快照而不是解析 tasklist 输出：既避开控制台码页导致的中文乱码，也少起一个子进程。
//
// [返回] PID 到进程名的映射；创建快照失败时返回 error
// 最近修改时间: 2026-09-12
func enumWindowsProcesses() (map[uint32]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("创建进程快照失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	result := make(map[uint32]string)
	// 遍历到末尾时返回 ERROR_NO_MORE_FILES，属于正常结束而非错误
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		result[entry.ProcessID] = windows.UTF16ToString(entry.ExeFile[:])
	}
	return result, nil
}

// collectWindowsEndpoints 采集 Windows 宿主上处于监听状态的端点。
//
// 只取 TCP 的 LISTENING 与全部 UDP：占用端口的是监听方，
// ESTABLISHED、TIME_WAIT 这些连接态条目数量庞大且不构成占用，纳入只会淹没有效信息。
// netstat 的表头在中文系统下是本地化文案，但数据行的协议、地址、PID 均为 ASCII，可稳定解析。
//
// [返回] PID 到监听端点的映射；netstat 执行失败时返回 error
// 最近修改时间: 2026-09-12
func collectWindowsEndpoints() (map[uint32][]Endpoint, error) {
	output, err := winutil.RunHidden("netstat", "-ano")
	if err != nil && output == "" {
		return nil, fmt.Errorf("执行 netstat 失败: %w", err)
	}

	result := make(map[uint32][]Endpoint)
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		// 数据行至少包含 协议、本地地址、外部地址、PID 四列
		if len(fields) < 4 {
			continue
		}

		// 1. 只处理 TCP / UDP 数据行，借此跳过本地化表头与空行
		protocol := strings.ToUpper(fields[0])
		if protocol != "TCP" && protocol != "UDP" {
			continue
		}

		// 2. TCP 行比 UDP 行多一列连接状态，只保留监听态
		state := ""
		if protocol == "TCP" {
			if len(fields) < 5 {
				continue
			}
			state = fields[3]
			if !strings.EqualFold(state, "LISTENING") {
				continue
			}
		}

		// 3. PID 恒为末列
		pid64, convErr := strconv.ParseUint(fields[len(fields)-1], 10, 32)
		if convErr != nil {
			continue
		}

		// 4. 同一进程可能在 IPv4/IPv6 上重复出现，按 PID 加协议加地址去重
		localAddr := fields[1]
		key := fields[len(fields)-1] + "|" + protocol + "|" + localAddr
		if seen[key] {
			continue
		}
		seen[key] = true

		pid := uint32(pid64)
		result[pid] = append(result[pid], Endpoint{Protocol: protocol, Address: localAddr, State: state})
	}

	sortEndpoints(result)
	return result, nil
}

// sortEndpoints 对每个进程的端点做固定排序，保证多次查询的展示顺序一致。
//
// [参数] endpoints: PID 到端点列表的映射，原地排序
// 最近修改时间: 2026-09-12
func sortEndpoints(endpoints map[uint32][]Endpoint) {
	for _, items := range endpoints {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Protocol != items[j].Protocol {
				return items[i].Protocol < items[j].Protocol
			}
			return items[i].Address < items[j].Address
		})
	}
}

// killWindows 结束 Windows 宿主上的指定进程。
//
// [参数] pid: 目标进程 ID
// [返回] 结束失败时返回 error
// 最近修改时间: 2026-09-12
func killWindows(pid uint32) error {
	return winutil.TerminateProcess(pid)
}
