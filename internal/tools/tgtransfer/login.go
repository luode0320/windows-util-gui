package tgtransfer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// IsLoggedIn 检测本地当前 namespace 是否存在有效的登录会话。
//
// 判定 = 登录成功标记存在 且 会话文件非空：tdl 的 login qr 即使未完成扫码确认，
// 也会初始化一个非空的 Bolt 存储文件，仅靠会话文件存在会误报"已登录"，
// 因此以扫码成功后写入的标记文件为准。
//
// [参数] cfg: 运行配置
// [返回] 标记与会话同时有效时返回 true
// 最近修改时间: 2026-09-13
func IsLoggedIn(cfg *Config) bool {
	marker := filepath.Join(cfg.BaseDir, "login-active.flag")
	if fi, err := os.Stat(marker); err != nil || fi.IsDir() {
		return false
	}
	sessionFile := cfg.SessionPath()
	fi, err := os.Stat(sessionFile)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Size() > 0
}

// MarkLoginActive 在扫码登录成功后写入登录标记文件。
//
// [参数] cfg: 运行配置
// [返回] 写入失败返回 error
// 最近修改时间: 2026-09-13
func MarkLoginActive(cfg *Config) error {
	return os.WriteFile(filepath.Join(cfg.BaseDir, "login-active.flag"),
		[]byte(time.Now().Format(time.RFC3339)), 0644)
}

// ClearSession 清除本地登录会话与登录标记，重置登录状态。
//
// [参数] cfg: 运行配置
// [返回] 清除失败时返回 error
// 最近修改时间: 2026-09-13
func ClearSession(cfg *Config) error {
	if err := os.Remove(filepath.Join(cfg.BaseDir, "login-active.flag")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清除登录标记失败: %w", err)
	}
	sessionFile := cfg.SessionPath()
	if err := os.Remove(sessionFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清除登录会话文件失败: %w", err)
	}
	return nil
}

// QrLoginSession 负责控制二维码扫码登录的全过程。
type QrLoginSession struct {
	cfg    *Config
	cancel context.CancelFunc
}

// NewQrLoginSession 创建登录控制器。
//
// [参数] cfg: 运行配置
// [返回] 登录控制器实例
// 最近修改时间: 2026-09-13
func NewQrLoginSession(cfg *Config) *QrLoginSession {
	return &QrLoginSession{cfg: cfg}
}

// StartQrLogin 启动扫码登录，并在二维码图片就绪后通过 onQrReady 回调通知界面。
//
// [参数] parentCtx: 父上下文；onQrReady: 二维码图片生成回调；logCb: 实时日志回调
// [返回] 登录流程结束时返回 nil，若超时、中断或登录失败则返回 error
// 最近修改时间: 2026-09-13
func (s *QrLoginSession) StartQrLogin(parentCtx context.Context, onQrReady func(imgPath string), logCb func(string)) error {
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel
	defer cancel()

	if err := s.cfg.EnsureDirectories(); err != nil {
		return err
	}

	qrPath := s.cfg.QrImagePath()
	_ = os.Remove(qrPath)

	args := BuildTdlArgs(s.cfg, "login", "-T", "qr")
	cmd := PrepareTdlCommand(ctx, s.cfg, args...)

	// 1. 设置环境变量，指示 tdl 将登录二维码输出为图片文件
	cmd.Env = append(cmd.Env, "TDL_QR_IMAGE_PATH="+qrPath)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建标准输出管道失败: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建标准错误管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 tdl 二维码登录进程失败: %w", err)
	}

	// 2. 轮询监控二维码图片生成状态
	donePoll := make(chan struct{})
	go func() {
		defer close(donePoll)
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if fi, err := os.Stat(qrPath); err == nil && fi.Size() > 0 {
					if onQrReady != nil {
						onQrReady(qrPath)
					}
					return
				}
			}
		}
	}()

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
	cmdErr := cmd.Wait()
	_ = os.Remove(qrPath)

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cmdErr != nil {
		return fmt.Errorf("登录进程退出: %w", cmdErr)
	}
	return nil
}

// Stop 停止当前正在进行的扫码登录。
//
// 最近修改时间: 2026-09-13
func (s *QrLoginSession) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}
