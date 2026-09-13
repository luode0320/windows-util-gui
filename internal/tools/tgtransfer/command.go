package tgtransfer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"windows-util-gui/internal/winutil"
)

var (
	// ansiRegex 用于剥离 tdl 终端进度条产生的 ANSI 控制码。
	ansiRegex = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// controlCharsRegex 移除 ASCII 控制符，保留换行与制表符。
	controlCharsRegex = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F]`)
)

// CleanLogLine 清理命令行输出中的 ANSI 控制符与特殊不可见字符。
//
// [参数] raw: 原始输出文本行
// [返回] 清洗后的安全文本
// 最近修改时间: 2026-09-13
func CleanLogLine(raw string) string {
	s := ansiRegex.ReplaceAllString(raw, "")
	s = controlCharsRegex.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// BuildTdlArgs 根据当前配置组装完整的基础命令行参数。
//
// [参数] cfg: 运行配置；subArgs: 具体子命令与参数
// [返回] 带有存储、命名空间、代理等完整标志的参数切片
// 最近修改时间: 2026-09-13
func BuildTdlArgs(cfg *Config, subArgs ...string) []string {
	args := []string{
		"--storage", cfg.StorageArg(),
		"--ns", cfg.Namespace,
	}

	if proxy := strings.TrimSpace(cfg.Proxy); proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	args = append(args, subArgs...)
	return args
}

// PrepareTdlCommand 构造带有隐藏窗口和特定环境变量的 tdl 命令对象。
//
// [参数] ctx: 上下文；cfg: 运行配置；args: 命令行参数
// [返回] 准备就绪的 exec.Cmd 实例
// 最近修改时间: 2026-09-13
func PrepareTdlCommand(ctx context.Context, cfg *Config, args ...string) *exec.Cmd {
	tdlExe := cfg.TdlPath
	if tdlExe == "" {
		tdlExe = FindTdlPath()
	}

	// 1. tdl 通过相对路径调用时以 exe 目录为解析锚点；裸命令名则依赖系统 PATH 查找
	cmd := exec.CommandContext(ctx, tdlExe, args...)
	if filepath.IsAbs(tdlExe) {
		cmd.Dir = cfg.BaseDir
	}

	// 1. 设置无黑窗口标志
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	// 2. 注入运行所需环境变量，确保 tdl 确定根目录
	env := os.Environ()
	vol := filepath.VolumeName(cfg.BaseDir)
	env = append(env,
		"TDL_ROOT="+cfg.BaseDir,
		"USERPROFILE="+cfg.BaseDir,
	)
	if vol != "" {
		env = append(env,
			"HOMEDRIVE="+vol,
			"HOMEPATH="+strings.TrimPrefix(cfg.BaseDir, vol),
		)
	}
	cmd.Env = env

	return cmd
}

// RunTdlStreaming 执行 tdl 命令，并在标准输出/错误产生新行时实时回调 logCb。
//
// [参数] ctx: 用于控制超时或中断的上下文；cfg: 配置；args: 命令参数；logCb: 每行日志的回调函数
// [返回] 执行成功返回 nil，非零退出码或超时返回 error
// 最近修改时间: 2026-09-13
func RunTdlStreaming(ctx context.Context, cfg *Config, args []string, logCb func(string)) error {
	cmd := PrepareTdlCommand(ctx, cfg, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("打开标准输出管道失败: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("打开标准错误管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 tdl.exe 失败 (路径: %s): %w", cfg.TdlPath, err)
	}

	var wg sync.WaitGroup
	readLines := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := CleanLogLine(scanner.Text())
			if line != "" && logCb != nil {
				logCb(line)
			}
		}
	}

	wg.Add(2)
	go readLines(stdoutPipe)
	go readLines(stderrPipe)

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("tdl 命令执行退出异常: %w", err)
	}
	return nil
}

// RunTdlOutput 完整执行一次 tdl 命令并收集返回标准输出解码内容。
//
// [参数] ctx: 上下文；cfg: 配置；args: 命令行参数
// [返回] 解码后的标准输出；出错时返回 error
// 最近修改时间: 2026-09-13
func RunTdlOutput(ctx context.Context, cfg *Config, args []string) (string, error) {
	cmd := PrepareTdlCommand(ctx, cfg, args...)

	outputBytes, err := cmd.CombinedOutput()
	outputStr := winutil.DecodeConsoleOutput(outputBytes)
	if err != nil {
		if ctx.Err() != nil {
			return outputStr, ctx.Err()
		}
		return outputStr, fmt.Errorf("命令退出码异常: %w, 详细输出: %s", err, outputStr)
	}
	return outputStr, nil
}
