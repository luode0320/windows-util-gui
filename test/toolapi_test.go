// Package test 存放项目的全部测试，统一在真实 Windows + WSL 环境下验证行为。
package test

import (
	"net"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"windows-util-gui/pkg/toolapi"
)

// TestToolAPIQueryPortSelf 在 Windows 侧真实监听一个随机端口，验证桥接层按端口能查到本进程。
//
// 用 net.Listen 起真实监听后调用 toolapi.Query，断言结果中存在一条来源为 Windows、
// 且监听端点窄化到目标端口的行：既验证桥接层透传正确，也验证底层 netstat 采集
// 能反映当前进程的实时监听状态，而不是只跑通了类型转换。
//
// 最近修改时间: 2026-09-13
func TestToolAPIQueryPortSelf(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows（含宿主侧 netstat）真实环境下验证，跳过")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听失败: %v", err)
	}
	defer listener.Close()

	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	rows, _, err := toolapi.Query("port", port)
	if err != nil {
		t.Fatalf("查询端口 %s 失败: %v", port, err)
	}

	for _, row := range rows {
		if row.Origin == "Windows" && strings.HasSuffix(row.Listening, ":"+port) {
			return
		}
	}
	t.Fatalf("端口 %s 由当前进程监听，但 toolapi.Query 结果中没有 Windows 侧命中行：%+v", port, rows)
}

// TestToolAPIQueryInvalidKind 验证非法查询方式会在桥接层被拒绝，根本不会进入业务层。
//
// 最近修改时间: 2026-09-13
func TestToolAPIQueryInvalidKind(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows 真实环境下验证，跳过")
	}

	if _, _, err := toolapi.Query("bogus", "x"); err == nil {
		t.Fatal("kind=\"bogus\" 应当被拒绝，但查询成功了")
	}
}

// TestToolAPIQueryNameNoError 验证按进程名查询至少能正常执行、不返回 error。
//
// 即使环境里恰好没有 node 进程（返回 0 行），只要底层采集成功就应通过，
// 重点验证桥接层对合法 kind + 合法 value 的透传路径自身不会报错。
//
// 最近修改时间: 2026-09-13
func TestToolAPIQueryNameNoError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows 真实环境下验证，跳过")
	}

	if _, _, err := toolapi.Query("name", "node"); err != nil {
		t.Fatalf("按进程名 node 查询不应返回 error，实际: %v", err)
	}
}
