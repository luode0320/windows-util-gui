// Package test 存放项目的全部测试，统一在真实 Windows + WSL 环境下验证行为。
package test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"windows-util-gui/internal/tools/prockill"
)

// TestQueryRejectsInvalidCriteria 校验非法查询条件会被直接拒绝，不会真的去跑外部命令。
//
// 最近修改时间: 2026-09-12
func TestQueryRejectsInvalidCriteria(t *testing.T) {
	cases := []struct {
		name     string
		criteria prockill.Criteria
	}{
		{"端口为空", prockill.Criteria{Kind: prockill.KindPort, Value: "  "}},
		{"端口非数字", prockill.Criteria{Kind: prockill.KindPort, Value: "80a"}},
		{"端口为零", prockill.Criteria{Kind: prockill.KindPort, Value: "0"}},
		{"端口越界", prockill.Criteria{Kind: prockill.KindPort, Value: "65536"}},
		{"PID 非数字", prockill.Criteria{Kind: prockill.KindPID, Value: "abc"}},
		{"PID 为零", prockill.Criteria{Kind: prockill.KindPID, Value: "0"}},
		{"进程名为空", prockill.Criteria{Kind: prockill.KindName, Value: ""}},
		{"进程名过短", prockill.Criteria{Kind: prockill.KindName, Value: "s"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := prockill.Query(tc.criteria); err == nil {
				t.Fatalf("条件 %+v 应当被拒绝，但查询成功了", tc.criteria)
			}
		})
	}
}

// TestQueryByPortFindsWindowsListener 在 Windows 侧真实监听一个端口，验证按端口能查到本进程。
//
// 最近修改时间: 2026-09-12
func TestQueryByPortFindsWindowsListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听失败: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	self := uint32(os.Getpid())

	items, warnings, err := prockill.Query(prockill.Criteria{
		Kind:  prockill.KindPort,
		Value: strconv.Itoa(port),
	})
	if err != nil {
		t.Fatalf("查询端口 %d 失败: %v", port, err)
	}
	logWarnings(t, warnings)

	for _, item := range items {
		if item.Source == prockill.SourceWindows && item.PID == self {
			// 按端口查询会把端点收窄到命中的那一个，这里顺带校验收窄结果确实指向目标端口
			if !strings.HasSuffix(item.Listening(), ":"+strconv.Itoa(port)) {
				t.Fatalf("端点未收窄到目标端口，实际为 %s", item.Listening())
			}
			return
		}
	}
	t.Fatalf("端口 %d 由当前进程(PID %d)监听，但查询结果中没有它：%s", port, self, describe(items))
}

// TestQueryByPIDFindsSelf 验证按 PID 能查到当前测试进程，且即使它没有监听端口也会出现在结果中。
//
// 最近修改时间: 2026-09-12
func TestQueryByPIDFindsSelf(t *testing.T) {
	self := uint32(os.Getpid())

	items, warnings, err := prockill.Query(prockill.Criteria{
		Kind:  prockill.KindPID,
		Value: strconv.FormatUint(uint64(self), 10),
	})
	if err != nil {
		t.Fatalf("按 PID 查询失败: %v", err)
	}
	logWarnings(t, warnings)

	for _, item := range items {
		if item.Source == prockill.SourceWindows && item.PID == self {
			if item.Name == "" {
				t.Fatal("查到了当前进程但进程名为空")
			}
			return
		}
	}
	t.Fatalf("按 PID %d 查询没有找到当前进程：%s", self, describe(items))
}

// TestQueryByNameMatchesFuzzily 验证按进程名的模糊匹配：用当前进程名的一个片段也能查到它。
//
// 最近修改时间: 2026-09-12
func TestQueryByNameMatchesFuzzily(t *testing.T) {
	self := uint32(os.Getpid())

	// 1. 先按 PID 拿到当前进程在 Windows 侧的真实进程名，避免硬编码测试二进制的文件名
	found, _, err := prockill.Query(prockill.Criteria{
		Kind:  prockill.KindPID,
		Value: strconv.FormatUint(uint64(self), 10),
	})
	if err != nil {
		t.Fatalf("按 PID 查询失败: %v", err)
	}

	var name string
	for _, item := range found {
		if item.Source == prockill.SourceWindows && item.PID == self {
			name = item.Name
			break
		}
	}
	if name == "" {
		t.Skip("未能取得当前进程名，跳过模糊匹配验证")
	}

	// 2. 截取进程名中间一段作为关键字，确保走的是包含匹配而不是全等匹配
	keyword := strings.TrimSuffix(name, ".exe")
	if len([]rune(keyword)) > 4 {
		keyword = string([]rune(keyword)[1:4])
	}

	items, warnings, err := prockill.Query(prockill.Criteria{
		Kind:  prockill.KindName,
		Value: keyword,
	})
	if err != nil {
		t.Fatalf("按进程名 %q 查询失败: %v", keyword, err)
	}
	logWarnings(t, warnings)

	for _, item := range items {
		if item.Source == prockill.SourceWindows && item.PID == self {
			return
		}
	}
	t.Fatalf("用关键字 %q 没有匹配到当前进程 %s(PID %d)：%s", keyword, name, self, describe(items))
}

// TestKillRefusesProtectedProcess 验证系统核心进程与 WSL 的 init 进程会被拒绝结束。
//
// 这里刻意不真的去杀任何进程：构造记录直接走保护判断，测试本身必须是安全的。
//
// 最近修改时间: 2026-09-12
func TestKillRefusesProtectedProcess(t *testing.T) {
	cases := []struct {
		name    string
		process prockill.Process
	}{
		{"Windows 核心进程", prockill.Process{Source: prockill.SourceWindows, PID: 4, Name: "csrss.exe"}},
		{"大小写不敏感", prockill.Process{Source: prockill.SourceWindows, PID: 4, Name: "LSASS.EXE"}},
		{"WSL 的 init", prockill.Process{Source: prockill.SourceWSL, Distro: "Ubuntu", PID: 1, Name: "systemd"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, protected := prockill.IsProtected(tc.process); !protected {
				t.Fatalf("%s 应当被保护，但判定为可结束", tc.process.Name)
			}
			if err := prockill.Kill(tc.process); err == nil {
				t.Fatalf("%s 应当拒绝结束，但 Kill 返回成功", tc.process.Name)
			}
		})
	}

	// svchost.exe 承载大量普通服务，确实存在需要结束的正当场景，不应进保护名单
	if _, protected := prockill.IsProtected(prockill.Process{
		Source: prockill.SourceWindows, PID: 100, Name: "svchost.exe",
	}); protected {
		t.Fatal("svchost.exe 不应被列入保护名单")
	}
}

// TestKillWSLListener 在 WSL 中真实起一个监听进程，验证按端口查到后能把它结束掉。
//
// 最近修改时间: 2026-09-12
func TestKillWSLListener(t *testing.T) {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("未找到 wsl.exe，跳过 WSL 侧验证")
	}

	const port = "18931"

	// wsl.exe 会话退出时会连带结束该会话内启动的进程，nohup / setsid 都挡不住，
	// 因此必须让这个 wsl.exe 子进程一直活着，测试结束时再显式结束它
	cmd := exec.Command("wsl.exe", "-u", "root", "-e", "python3", "-m", "http.server", port)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		t.Skipf("无法在 WSL 中启动监听进程: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// WSL 冷启动耗时波动很大，用轮询代替固定等待
	var target *prockill.Process
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)

		items, _, err := prockill.Query(prockill.Criteria{Kind: prockill.KindPort, Value: port})
		if err != nil {
			t.Fatalf("查询端口 %s 失败: %v", port, err)
		}
		for i := range items {
			if items[i].Source == prockill.SourceWSL {
				target = &items[i]
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		t.Skip("未在 WSL 侧发现监听进程，可能发行版内没有 python3 或缺少 iproute2，跳过")
	}

	if err := prockill.Kill(*target); err != nil {
		t.Fatalf("结束 WSL 进程失败: %v", err)
	}

	// 结束后回查，确认端口确实在 WSL 侧释放了
	for i := 0; i < 6; i++ {
		time.Sleep(500 * time.Millisecond)

		items, _, err := prockill.Query(prockill.Criteria{Kind: prockill.KindPort, Value: port})
		if err != nil {
			t.Fatalf("回查端口 %s 失败: %v", port, err)
		}

		var alive bool
		for _, item := range items {
			if item.Source == prockill.SourceWSL && item.PID == target.PID {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
	}
	t.Fatalf("已调用 Kill，但 WSL 进程 %d 仍然占用端口 %s", target.PID, port)
}

// logWarnings 把查询过程中的警告输出到测试日志，便于定位环境问题。
//
// [参数] t: 测试上下文；warnings: 查询返回的警告
// 最近修改时间: 2026-09-12
func logWarnings(t *testing.T, warnings []string) {
	t.Helper()
	for _, warning := range warnings {
		t.Logf("查询警告：%s", warning)
	}
}

// describe 把进程列表整理成便于排查的文本。
//
// [参数] items: 进程列表
// [返回] 单行汇总文本
// 最近修改时间: 2026-09-12
func describe(items []prockill.Process) string {
	if len(items) == 0 {
		return "（结果为空）"
	}

	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("[%s] PID %d %s %s",
			item.Origin(), item.PID, item.Name, item.Listening()))
	}
	return strings.Join(parts, "; ")
}
