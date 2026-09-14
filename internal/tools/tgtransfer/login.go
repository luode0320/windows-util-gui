package tgtransfer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// StartQrLogin 启动扫码登录，并在关键节点通过回调通知界面。
//
// 状态流转：二维码就绪 → onQrReady（界面显示二维码）；手机确认授权、会话写入本地
// → onConfirmed（界面切换为"正在完成登录"）并立即终止 tdl 进程；随后返回 nil 表示登录成功。
//
// 登录成功以「会话文件被 tdl 写入」为准，而非等待 tdl 进程自然退出：
// 部分 tdl 版本在授权成功后仍驻留进程，若只等进程结束，界面会停在二维码上无法收尾。
//
// [参数] parentCtx: 父上下文；onQrReady: 二维码图片生成回调；onConfirmed: 授权确认回调；logCb: 实时日志回调
// [返回] 登录成功返回 nil；超时、被取消或失败返回 error
// 最近修改时间: 2026-09-14
func (s *QrLoginSession) StartQrLogin(parentCtx context.Context, onQrReady func(imgPath string), onConfirmed func(), logCb func(string)) error {
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel
	defer cancel()

	if err := s.cfg.EnsureDirectories(); err != nil {
		return err
	}

	qrPath := s.cfg.QrImagePath()
	_ = os.Remove(qrPath)

	// 授权判定说明：不能用"会话文件体积增长"直接判定——
	// tdl 启动阶段自己就会写 Bolt 存储（建库、写配置），在二维码出现前就已有体积变化，
	// 若此时判定成功，会出现"二维码都没出现就显示已登录、弹窗自动关闭"的假阳性。
	// 因此以「二维码就绪」为语义锚点：授权只可能发生在扫码之后，
	// 二维码就绪时重新取一次稳定基线，其后出现的稳定增长才判定为授权成功。
	sessionFile := s.cfg.SessionPath()

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

	// 2. 授权结果判定（单状态机：等二维码 → 重新取基线 → 稳定增长/日志关键词）
	var confirmedMu sync.Mutex
	confirmed := false
	var qrShown atomic.Bool
	confirmAuthorized := func(why string) {
		confirmedMu.Lock()
		if confirmed {
			confirmedMu.Unlock()
			return
		}
		confirmed = true
		confirmedMu.Unlock()

		if logCb != nil {
			logCb("已确认授权（" + why + "），正在完成登录…")
		}
		if onConfirmed != nil {
			onConfirmed()
		}
		// tdl 授权后可能继续驻留，主动结束进程让流程收尾
		cancel()
	}

	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		var baseline int64 = -1 // 二维码就绪后的稳定基线；-1 表示尚未取到
		var lastSize int64 = -1
		var grownSize int64 = -1 // 增长后的待确认体积

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 2.1 阶段一：等二维码落盘，作为"授权可能发生"的时间锚点
				if !qrShown.Load() {
					if fi, err := os.Stat(qrPath); err == nil && fi.Size() > 0 {
						if onQrReady != nil {
							onQrReady(qrPath)
						}
						qrShown.Store(true)
					}
					continue
				}

				// 2.2 阶段二：二维码就绪后重新取基线——此刻 tdl 的初始化写入已结束，
				// 连续两次读到同一体积才认账，避免把初始化尾巴当增长
				fi, statErr := os.Stat(sessionFile)
				if statErr != nil || fi.IsDir() {
					lastSize = -1
					continue
				}
				size := fi.Size()
				if baseline < 0 {
					if size == lastSize {
						baseline = size
					} else {
						lastSize = size
					}
					continue
				}

				// 2.3 阶段三：基线之上增长，且新体积连续两次一致（写入已落定）才判定授权
				if size <= baseline {
					continue
				}
				if size == grownSize {
					confirmAuthorized("会话已写入")
					return
				}
				grownSize = size
			}
		}
	}()

	var wg sync.WaitGroup
	readLines := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := CleanLogLine(scanner.Text())
			if line == "" {
				continue
			}
			if logCb != nil {
				logCb(line)
			}
			// 次要信号：tdl 明确报告登录成功时同样判定为已授权；
			// 同样以二维码就绪为前提，避免初始化阶段的无关输出触发误判
			if qrShown.Load() && IsLoginSuccessLine(line) {
				confirmAuthorized("登录进程报告成功")
			}
		}
	}

	wg.Add(2)
	go readLines(stdoutPipe)
	go readLines(stderrPipe)

	wg.Wait()
	cmdErr := cmd.Wait()
	_ = os.Remove(qrPath)

	// 4. 收尾判定：已确认授权优先（此时 ctx 是我们主动取消的，不算失败）
	confirmedMu.Lock()
	wasConfirmed := confirmed
	confirmedMu.Unlock()
	if wasConfirmed {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cmdErr != nil {
		return fmt.Errorf("登录进程退出: %w", cmdErr)
	}
	return nil
}

// IsLoginSuccessLine 判断 tdl 输出行是否为"登录成功"信号。
//
// tdl 不同版本输出文案不一致，这里只保留确定性较高的关键词，
// 避免把"未登录""登录中"等中间态误判为成功。
//
// [参数] line: tdl 的单行输出
// [返回] 命中登录成功关键词返回 true
// 最近修改时间: 2026-09-14
func IsLoginSuccessLine(line string) bool {
	lower := strings.ToLower(line)
	keywords := []string{
		"登录成功",
		"successfully logged in",
		"logged in successfully",
		"login success",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// Stop 停止当前正在进行的扫码登录。
//
// 最近修改时间: 2026-09-13
func (s *QrLoginSession) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}
