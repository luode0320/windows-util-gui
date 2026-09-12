package winutil

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

// defaultCommandTimeout 外部命令的默认超时时间。
// netstat、wsl.exe 在极端情况下会长时间无响应，必须有超时兜底，否则会卡死 UI 线程。
const defaultCommandTimeout = 20 * time.Second

// RunHidden 以隐藏窗口的方式执行外部命令并返回解码后的标准输出。
//
// GUI 程序调用控制台程序时默认会闪出黑色命令行窗口，这里通过 CREATE_NO_WINDOW 抑制。
// 输出统一走 DecodeConsoleOutput 解码，屏蔽 wsl.exe 的 UTF-16LE 与控制台 GBK 差异。
//
// [参数] name: 可执行文件名或路径；args: 命令行参数
// [返回] 解码后的标准输出文本；命令启动失败或超时时返回 error
// 最近修改时间: 2026-09-12
func RunHidden(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 1. 执行命令；部分工具（如 ss 在无匹配时）会返回非零退出码，此处仍需保留已有输出
	err := cmd.Run()

	// 2. 优先返回标准输出；若标准输出为空则把标准错误一并解码，便于上层展示失败原因
	out := DecodeConsoleOutput(stdout.Bytes())
	if out == "" && stderr.Len() > 0 {
		out = DecodeConsoleOutput(stderr.Bytes())
	}
	return out, err
}
