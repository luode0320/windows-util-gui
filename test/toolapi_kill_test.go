// Package test 存放项目的全部测试，统一在真实 Windows + WSL 环境下验证行为。
package test

import (
	"encoding/csv"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"windows-util-gui/pkg/toolapi"
)

// TestToolAPIKillRealProcess 起一个真实子进程，验证桥接层能把它查到并真正结束掉。
//
// 用 cmd /c ping 起一个会持续 30 次的 ping 子进程，拿其 PID 走 toolapi.Query 取回 Row，
// 再调 toolapi.Kill 结束它：断言该行 Success 为真，并用 tasklist 回查确认进程确实已退出。
// 全程在普通权限下即可完成——结束的是自己的子进程，不需要管理员。
//
// 最近修改时间: 2026-09-13
func TestToolAPIKillRealProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows 真实环境下验证，跳过")
	}

	// 1. 起一个会长时间运行的真实子进程，拿到它的 PID
	cmd := exec.Command("cmd", "/c", "ping", "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动测试子进程失败: %v", err)
	}
	pid := uint32(cmd.Process.Pid)
	// 兜底清理：若 Kill 未成功（异常路径）也能保证测试进程不留驻
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// 2. 等待进程进入查询快照（Toolhelp 枚举有一点点延迟），最多重试 1 秒
	var target *toolapi.Row
	for i := 0; i < 10; i++ {
		rows, _, err := toolapi.Query("pid", strconv.Itoa(int(pid)))
		if err != nil {
			t.Fatalf("按 PID 查询子进程失败: %v", err)
		}
		for i := range rows {
			if rows[i].PID == pid {
				target = &rows[i]
				break
			}
		}
		if target != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if target == nil {
		t.Fatalf("未能通过 toolapi.Query 查到刚启动的子进程 PID=%d", pid)
	}

	// 3. 调用桥接层结束，断言成功
	outcomes := toolapi.Kill([]toolapi.Row{*target})
	if len(outcomes) != 1 {
		t.Fatalf("Kill 应返回 1 条结果，实际返回 %d 条", len(outcomes))
	}
	if !outcomes[0].Success {
		t.Fatalf("结束 PID=%d 应当成功，但返回失败: %s", pid, outcomes[0].Error)
	}

	// 4. 回查确认进程已退出：tasklist 不再列出该 PID
	for i := 0; i < 10; i++ {
		if !processAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("已调用 Kill 断言成功，但 PID=%d 的进程仍然存活", pid)
}

// TestToolAPIKillProtected 验证系统核心进程 csrss.exe 会被拒绝结束、且进程仍然存活。
//
// 用 tasklist 取到真实 csrss.exe 的 PID 后手工构造 Row 调 Kill：断言 Success 为假且 Error 非空，
// 并回查确认进程未被误杀。整个测试只走保护名单判断，不真的尝试强杀核心进程，保证安全。
//
// 最近修改时间: 2026-09-13
func TestToolAPIKillProtected(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows 真实环境下验证，跳过")
	}

	pid, ok := findCsrssPID()
	if !ok {
		t.Skip("未找到 csrss.exe，跳过保护进程验证")
	}

	// 手工构造 Row：Source/Name 触发 prockill 的保护名单判定
	row := toolapi.Row{Source: "Windows", PID: pid, Name: "csrss.exe"}
	outcomes := toolapi.Kill([]toolapi.Row{row})
	if len(outcomes) != 1 {
		t.Fatalf("Kill 应返回 1 条结果，实际返回 %d 条", len(outcomes))
	}
	if outcomes[0].Success {
		t.Fatalf("csrss.exe(PID=%d) 属于系统核心进程，应当拒绝结束，但 Kill 返回成功", pid)
	}
	if outcomes[0].Error == "" {
		t.Fatal("csrss.exe 被拒绝时应当给出非空失败原因，但实际为空")
	}

	// 确认受保护进程仍然存活，没有被误杀
	if !processAlive(pid) {
		t.Fatalf("csrss.exe(PID=%d) 不应被结束，但进程已不存在，提权逻辑可能误杀了核心进程", pid)
	}
}

// processAlive 用 tasklist 按 PID 精确判断进程是否仍存在。
//
// [参数] pid: 待判断的进程 ID
// [返回] true 表示进程仍在运行
// 最近修改时间: 2026-09-13
func processAlive(pid uint32) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(int(pid)), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// CSV 第二列即 PID，精确匹配避免子串误判
		fields, parseErr := csv.NewReader(strings.NewReader(line)).Read()
		if parseErr != nil || len(fields) < 2 {
			continue
		}
		if fields[1] == strconv.Itoa(int(pid)) {
			return true
		}
	}
	return false
}

// findCsrssPID 用 tasklist 取第一个 csrss.exe 的真实 PID。
//
// [返回] PID 与是否找到；csrss.exe 通常存在多个会话实例，取第一个即可验证保护逻辑
// 最近修改时间: 2026-09-13
func findCsrssPID() (uint32, bool) {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq csrss.exe", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields, parseErr := csv.NewReader(strings.NewReader(line)).Read()
		if parseErr != nil || len(fields) < 2 {
			continue
		}
		if strings.EqualFold(fields[0], "csrss.exe") {
			if pid, convErr := strconv.ParseUint(fields[1], 10, 32); convErr == nil {
				return uint32(pid), true
			}
		}
	}
	return 0, false
}
