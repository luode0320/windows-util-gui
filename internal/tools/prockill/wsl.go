package prockill

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"windows-util-gui/internal/winutil"
)

// processPattern 匹配 ss 输出中 users:(("名称",pid=数字,fd=数字)) 里的进程名与 PID。
// 多个进程共享同一监听 fd 时该片段会重复出现，因此按全局匹配逐个提取。
var processPattern = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

// outputSplitter 用来在一次 wsl.exe 调用里分隔两条命令的输出。
// 每启动一次 wsl.exe 都有可观的固定开销，合并执行比分两次调用快得多。
const outputSplitter = "---PROCKILL-SPLIT---"

// listDistros 列出当前可查询的 WSL 发行版。
//
// 优先只列运行中的发行版：目标进程必然处于运行状态，而查询未运行的发行版会触发冷启动，
// 既慢又会产生副作用。旧版 wsl.exe 不支持 --running 时回退到全量列表。
//
// [返回] 发行版名称列表；WSL 不可用时返回 error
// 最近修改时间: 2026-09-12
func listDistros() ([]string, error) {
	output, err := winutil.RunHidden("wsl.exe", "-l", "-q", "--running")
	if err != nil || output == "" {
		output, err = winutil.RunHidden("wsl.exe", "-l", "-q")
		if err != nil && output == "" {
			return nil, fmt.Errorf("执行 wsl.exe 失败，可能未安装 WSL: %w", err)
		}
	}

	var distros []string
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			distros = append(distros, name)
		}
	}
	return distros, nil
}

// collectWSL 采集所有运行中 WSL 发行版的进程及其监听端点。
//
// 各发行版之间相互独立，并发采集以缩短整体等待；单个发行版失败只记为警告，不影响其余结果。
//
// [返回] 进程列表、采集过程中的警告信息
// 最近修改时间: 2026-09-12
func collectWSL() ([]Process, []string) {
	distros, err := listDistros()
	if err != nil {
		return nil, []string{err.Error()}
	}
	if len(distros) == 0 {
		return nil, []string{"未发现运行中的 WSL 发行版，已跳过 WSL 侧查询"}
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		result   []Process
		warnings []string
	)

	for _, distro := range distros {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			items, collectErr := collectDistro(name)

			mu.Lock()
			defer mu.Unlock()
			if collectErr != nil {
				warnings = append(warnings, fmt.Sprintf("WSL 发行版 %s 查询失败: %v", name, collectErr))
				return
			}
			result = append(result, items...)
		}(distro)
	}
	wg.Wait()

	return result, warnings
}

// collectDistro 采集单个 WSL 发行版的进程及其监听端点。
//
// 以 root 身份执行，否则看不到其它用户进程的 PID 与名称，端口会显示为"占用但无主"。
//
// [参数] distro: 发行版名称
// [返回] 进程列表；命令执行失败时返回 error
// 最近修改时间: 2026-09-12
func collectDistro(distro string) ([]Process, error) {
	script := fmt.Sprintf("ps -eo pid=,comm=; echo %s; ss -lntup", outputSplitter)
	output, err := winutil.RunHidden("wsl.exe", "-d", distro, "-u", "root", "-e", "sh", "-c", script)
	if output == "" {
		if err != nil {
			return nil, fmt.Errorf("执行 ps/ss 失败，请确认发行版内已安装 procps 与 iproute2: %w", err)
		}
		return nil, nil
	}

	// 1. 切开两条命令的输出；缺少分隔符说明 ps 阶段就失败了
	sections := strings.SplitN(output, outputSplitter, 2)
	if len(sections) != 2 {
		return nil, fmt.Errorf("命令输出格式异常，无法解析进程列表")
	}

	names := parseProcessList(sections[0])
	endpoints := parseListeningSockets(sections[1])

	// 2. ss 可能报出 ps 没覆盖到的 PID（采集存在时间差），补一条占位记录避免遗漏
	for pid, items := range endpoints {
		if _, ok := names[pid]; !ok && len(items) > 0 {
			names[pid] = items[0].ownerName
		}
	}

	result := make([]Process, 0, len(names))
	for pid, name := range names {
		process := Process{
			Source: SourceWSL,
			Distro: distro,
			PID:    pid,
			Name:   name,
		}
		for _, item := range endpoints[pid] {
			process.Endpoints = append(process.Endpoints, item.Endpoint)
		}
		result = append(result, process)
	}
	return result, nil
}

// ownedEndpoint 是带进程名的监听端点，进程名用于补齐 ps 没覆盖到的 PID。
type ownedEndpoint struct {
	Endpoint
	ownerName string
}

// parseProcessList 解析 `ps -eo pid=,comm=` 的输出。
//
// [参数] section: ps 的输出文本
// [返回] PID 到进程名的映射
// 最近修改时间: 2026-09-12
func parseProcessList(section string) map[uint32]string {
	result := make(map[uint32]string)
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 进程名本身可能带空格，因此只按第一个空白切一刀，剩余部分整体作为名称
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		pid64, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			continue
		}
		result[uint32(pid64)] = strings.TrimSpace(parts[1])
	}
	return result
}

// parseListeningSockets 解析 `ss -lntup` 的输出。
//
// [参数] section: ss 的输出文本
// [返回] PID 到监听端点的映射
// 最近修改时间: 2026-09-12
func parseListeningSockets(section string) map[uint32][]ownedEndpoint {
	result := make(map[uint32][]ownedEndpoint)

	for _, line := range strings.Split(section, "\n") {
		fields := strings.Fields(line)
		// ss 数据行固定为 Netid State Recv-Q Send-Q Local Peer [Process]
		if len(fields) < 6 {
			continue
		}

		// 1. 跳过表头行
		protocol := strings.ToUpper(fields[0])
		if protocol != "TCP" && protocol != "UDP" {
			continue
		}

		// 2. 从 users:((...)) 片段提取进程名与 PID；权限不足时该列缺失，只能整行放弃
		matches := processPattern.FindAllStringSubmatch(strings.Join(fields[6:], " "), -1)
		for _, match := range matches {
			pid64, err := strconv.ParseUint(match[2], 10, 32)
			if err != nil {
				continue
			}
			pid := uint32(pid64)
			result[pid] = append(result[pid], ownedEndpoint{
				Endpoint:  Endpoint{Protocol: protocol, Address: fields[4], State: fields[1]},
				ownerName: match[1],
			})
		}
	}
	return result
}

// killWSL 结束指定 WSL 发行版内的进程。
//
// [参数] distro: 发行版名称；pid: 发行版内的进程 ID
// [返回] 结束失败时返回 error
// 最近修改时间: 2026-09-12
func killWSL(distro string, pid uint32) error {
	if pid == 0 {
		return fmt.Errorf("该记录没有可用的 PID，无法结束进程")
	}

	output, err := winutil.RunHidden("wsl.exe", "-d", distro, "-u", "root", "-e",
		"kill", "-9", strconv.FormatUint(uint64(pid), 10))
	if err != nil {
		return fmt.Errorf("在 %s 中结束进程 %d 失败: %s", distro, pid, strings.TrimSpace(output))
	}
	return nil
}
