package tgtransfer

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"windows-util-gui/internal/tools/tgtransfer/tdlbin"
)

// Config 封装 Telegram 转发工具的核心运行配置。
type Config struct {
	TdlPath   string // tdl.exe 的绝对路径
	BaseDir   string // tg 工具的主工作目录
	DataDir   string // 存放导出数据与批次文件的目录
	LogDir    string // 存放日志文件的目录
	TdlHome   string // .tdl 会话及 Bolt 存储主目录
	Namespace string // tdl 命令使用的 namespace
	Proxy     string // 代理地址，如 socks5://127.0.0.1:7890
}

// DefaultConfig 生成默认配置实例，并自动探测 tdl.exe 与初始化工作目录。
//
// [返回] 初始配置对象
// 最近修改时间: 2026-09-13
func DefaultConfig() *Config {
	// 1. 数据目录统一锚定到用户本地应用数据目录（%LOCALAPPDATA%），
	//    exe 可能位于 Program Files 等只读位置，普通权限写 exe 目录会失败；
	//    且发布后用户会移动 exe，数据跟着 exe 走不符合"用户数据"的语义
	baseDir := defaultBaseDir()

	cfg := &Config{
		BaseDir:   baseDir,
		DataDir:   filepath.Join(baseDir, "data"),
		LogDir:    filepath.Join(baseDir, "logs"),
		TdlHome:   filepath.Join(baseDir, ".tdl"),
		Namespace: "default",
	}

	// 2. 旧版本把数据锚定在 exe 目录下，首次运行新版本时做一次性搬迁，
	//    保证登录会话、导出记录与转发历史不因迁移丢失
	legacy := filepath.Join(appRootDir(), "data", "tgtransfer")
	if err := MigrateLegacyDataDir(legacy, baseDir); err != nil {
		// 迁移失败不阻断启动：新位置会按空数据初始化，仅丢失旧会话与历史
		_ = err
	}

	// 3. 优先把编译期嵌入的 tdl.exe 释放到数据目录（仅首次运行时落盘一次）
	_, _ = cfg.EnsureBundledTdl()

	// 4. 自动探查 tdl.exe
	cfg.TdlPath = FindTdlPath()
	return cfg
}

// defaultBaseDir 返回 TG 数据的默认根目录。
//
// 优先使用 %LOCALAPPDATA%\windows-util-gui\tgtransfer（用户级统一临时/数据位置）；
// LOCALAPPDATA 缺失时回退到系统配置目录（跨平台兼容）；再失败才退回 exe 目录。
//
// [返回] 数据根目录绝对路径
// 最近修改时间: 2026-09-13
func defaultBaseDir() string {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "windows-util-gui", "tgtransfer")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, "windows-util-gui", "tgtransfer")
	}
	return filepath.Join(appRootDir(), "data", "tgtransfer")
}

// MigrateLegacyDataDir 把旧版 exe 目录下的数据整体搬迁到新数据根目录。
//
// 仅当旧目录存在且新目录尚无 tdl 释放产物时执行（避免覆盖新版本已产生的数据）；
// 同卷走 rename（瞬时完成），跨卷 rename 失败时降级为复制+删除。
//
// [参数] legacyDir: 旧数据目录；newBase: 新数据根目录
// [返回] 无需迁移或迁移成功返回 nil；部分条目搬迁失败返回 error
// 最近修改时间: 2026-09-13
func MigrateLegacyDataDir(legacyDir, newBase string) error {
	// 1. 旧目录不存在（首次安装）或已迁移过（新位置已有 tdl 释放产物）时无需处理
	if fi, err := os.Stat(legacyDir); err != nil || !fi.IsDir() {
		return nil
	}

	// 2. 逐个顶层条目处理：bin（tdl 释放产物）、data（导出/记录）、logs、.tdl（登录会话）。
	//    目标已存在时不能整体跳过——老 exe 曾在 exe 目录旁建过 data 目录，只搬"新位置缺的"，
	//    并把旧释放产物直接清掉（嵌入内容以新位置为准），否则桌面会残留空壳 data 目录
	if err := os.MkdirAll(newBase, 0755); err != nil {
		return fmt.Errorf("创建新数据目录 %s 失败: %w", newBase, err)
	}
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, entry := range entries {
		src := filepath.Join(legacyDir, entry.Name())
		dst := filepath.Join(newBase, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			// 目标已存在：旧 bin 副本无保留价值（嵌入内容会重新释放到新位置），直接删除；
			// 其余重名条目（如两边都有 data）保留不覆盖，避免丢新位置已产生的数据
			if entry.Name() == "bin" {
				if err := os.RemoveAll(src); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			continue
		}
		if err := moveDir(src, dst); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 3. 处理完的旧目录若已清空则删除，不留桌面空壳；仍有残留说明有重名数据未决，保留待查。
	//    旧结构是 data/tgtransfer 两层，tgtransfer 清空后把空的 data 父目录也一并移除
	if remaining, readErr := os.ReadDir(legacyDir); readErr == nil && len(remaining) == 0 {
		if err := os.Remove(legacyDir); err == nil {
			_ = os.Remove(filepath.Dir(legacyDir))
		}
	}
	return firstErr
}

// moveDir 移动单个目录：优先同卷 rename，跨卷失败时降级为复制+删除。
//
// [参数] src: 源目录；dst: 目标目录
// [返回] 成功返回 nil
// 最近修改时间: 2026-09-13
func moveDir(src, dst string) error {
	// 1. 同卷时 rename 瞬时完成，是首选路径
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// 2. 跨卷降级：递归复制到目标后删除源目录；
	//    复制失败保留源目录（数据不丢），删除失败不影响已复制的数据
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("创建目标目录 %s 失败: %w", dst, err)
	}
	copyErr := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0644)
	})
	if copyErr != nil {
		return copyErr
	}
	return os.RemoveAll(src)
}

// appRootDir 返回当前可执行文件所在目录的绝对路径。
//
// 以 exe 位置作为相对路径解析锚点，保证 GUI 无论从何处启动（双击、终端、快捷方式），
// 资源目录与数据目录的定位行为都完全一致。
//
// [返回] exe 所在目录绝对路径；获取失败时回退到进程工作目录
// 最近修改时间: 2026-09-13
func appRootDir() string {
	exePath, err := os.Executable()
	if err != nil {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "."
		}
		return wd
	}
	// 解析符号链接，避免 mklink 场景下解析到链接文件所在目录
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return filepath.Dir(exePath)
}

// EnsureDirectories 确保相关工作目录已创建。
//
// [返回] 创建失败时返回 error
// 最近修改时间: 2026-09-13
func (c *Config) EnsureDirectories() error {
	dirs := []string{c.BaseDir, c.DataDir, c.LogDir, c.TdlHome}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}
	return nil
}

// StorageArg 返回 tdl 命令使用的 --storage 参数。
//
// [返回] 形如 type=bolt,path=... 的存储配置字符串
// 最近修改时间: 2026-09-13
func (c *Config) StorageArg() string {
	storagePath := filepath.Join(c.TdlHome, "data")
	return fmt.Sprintf("type=bolt,path=%s", storagePath)
}

// SessionPath 返回当前 namespace 对应的本地登录会话文件绝对路径。
//
// [返回] 会话文件路径
// 最近修改时间: 2026-09-13
func (c *Config) SessionPath() string {
	return filepath.Join(c.TdlHome, "data", c.Namespace)
}

// ForwardHistoryPath 返回已转发历史文件路径。
//
// [返回] forward-history.json 文件路径
// 最近修改时间: 2026-09-13
func (c *Config) ForwardHistoryPath() string {
	return filepath.Join(c.DataDir, "forward-history.json")
}

// ExportRecordsPath 返回导出记录元数据文件路径。
//
// [返回] export-records.json 文件路径
// 最近修改时间: 2026-09-13
func (c *Config) ExportRecordsPath() string {
	return filepath.Join(c.DataDir, "export-records.json")
}

// ChatListPath 返回聊天列表快照文件路径。
//
// [返回] chats.txt 文件路径
// 最近修改时间: 2026-09-13
func (c *Config) ChatListPath() string {
	return filepath.Join(c.DataDir, "chats.txt")
}

// QrImagePath 返回扫码登录二维码临时图片文件路径。
//
// [返回] login-qr.png 文件路径
// 最近修改时间: 2026-09-13
func (c *Config) QrImagePath() string {
	return filepath.Join(c.DataDir, "login-qr.png")
}

// BundledTdlPath 返回嵌入 tdl.exe 的释放目标路径。
//
// [返回] 数据根目录下 bin/tdl.exe 的绝对路径（默认在 %LOCALAPPDATA% 下）
// 最近修改时间: 2026-09-13
func (c *Config) BundledTdlPath() string {
	return filepath.Join(c.BaseDir, "bin", "tdl.exe")
}

// EnsureBundledTdl 把编译期嵌入的 tdl.exe 释放到数据目录。
//
// 仅在目标不存在或大小与嵌入内容不一致时写盘：前者是首次运行，后者用于
// 覆盖旧版本嵌入产物；已提取且一致时不重复写，启动开销为一次文件 stat。
// （Windows 无法从自身进程映像直接执行内嵌数据，故保留"嵌入 -> 首次释放"两级结构。）
//
// [返回] 释放成功返回释放路径；嵌入内容为空或写盘失败返回 error
// 最近修改时间: 2026-09-13
func (c *Config) EnsureBundledTdl() (string, error) {
	if len(tdlbin.EmbeddedTdl) == 0 {
		return "", fmt.Errorf("程序未嵌入 tdl.exe（嵌入包为空）")
	}

	target := c.BundledTdlPath()
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() && fi.Size() == int64(len(tdlbin.EmbeddedTdl)) {
		return target, nil
	}

	// 1. 先写临时文件再原子重命名，避免释放中途被杀导致残留半截 exe
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", fmt.Errorf("创建 tdl 释放目录失败: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, tdlbin.EmbeddedTdl, 0755); err != nil {
		return "", fmt.Errorf("写入 tdl 临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("释放 tdl.exe 失败: %w", err)
	}
	return target, nil
}

// FindTdlPath 在多级候选路径中智能探测可用的 tdl.exe。
//
// 查找优先级：环境变量 TDL_PATH -> 嵌入释放目录 -> exe 同级 resources/bin 与 bin
// -> 向上最多 3 级父目录搜索 resources/bin（开发场景） -> 系统 PATH。
// 所有相对候选一律以可执行文件自身目录为锚点解析，不依赖进程工作目录。
//
// [返回] 找到的可执行文件路径；若未找到则返回裸命令名（依赖系统 PATH）
// 最近修改时间: 2026-09-13
func FindTdlPath() string {
	// 1. 优先读取环境变量，允许用户强制指定
	if envPath := os.Getenv("TDL_PATH"); envPath != "" {
		if fi, err := os.Stat(envPath); err == nil && !fi.IsDir() {
			return envPath
		}
	}

	// 2. 数据根目录下的嵌入释放产物（单文件分发的首要来源），
	//    兼容迁移前的旧位置（exe 目录 data/tgtransfer）
	for _, base := range []string{defaultBaseDir(), filepath.Join(appRootDir(), "data", "tgtransfer")} {
		bundledPath := (&Config{BaseDir: base}).BundledTdlPath()
		if fi, err := os.Stat(bundledPath); err == nil && !fi.IsDir() {
			return bundledPath
		}
	}

	// 3. exe 同级相对候选（兼容旧的外置 resources 布局）
	root := appRootDir()
	relativeCandidates := []string{
		filepath.Join("resources", "bin", "tdl.exe"),
		filepath.Join("bin", "tdl.exe"),
		"tdl.exe",
	}
	for _, cand := range relativeCandidates {
		abs := filepath.Join(root, cand)
		if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
			return abs
		}
	}

	// 4. 向上搜索最多 3 级父目录，覆盖 "源码根\bin\xxx.exe" 的开发启动场景
	searchDir := root
	for i := 0; i < 3; i++ {
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break
		}
		searchDir = parent
		cand := filepath.Join(searchDir, "resources", "bin", "tdl.exe")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}

	return "tdl.exe"
}

// TestNetworkFast 快速测试目标主机与端口的 TCP 连通性。
//
// [参数] host: 主机名或IP；port: 端口号；timeout: 超时限制
// [返回] 连通成功返回 nil，失败返回对应错误
// 最近修改时间: 2026-09-13
func TestNetworkFast(host string, port int, timeout time.Duration) error {
	// 1. 使用 net.JoinHostPort 拼接地址，保证 IPv6 地址也能被正确解析
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// ValidateNetwork 根据代理配置提前探测连通性，避免 tdl 长时间卡住。
//
// [参数] proxy: 代理地址字符串
// [返回] 网络可用返回 nil，不可用时返回具体说明
// 最近修改时间: 2026-09-13
func ValidateNetwork(proxy string) error {
	p := strings.TrimSpace(proxy)
	// 1. 未填代理时，尝试直连 Telegram API 服务器探测
	if p == "" {
		if err := TestNetworkFast("api.telegram.org", 443, 3*time.Second); err != nil {
			return fmt.Errorf("当前网络无法直连 Telegram (api.telegram.org:443)，请配置代理后再试")
		}
		return nil
	}

	// 2. 配置了代理时，检测代理主机端口是否连通
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	u, err := url.Parse(p)
	if err != nil {
		return fmt.Errorf("代理地址格式无效: %w", err)
	}

	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return fmt.Errorf("代理地址缺少主机或端口号 (如 socks5://127.0.0.1:7890)")
	}

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 {
		return fmt.Errorf("代理端口号不合法: %s", portStr)
	}

	if err := TestNetworkFast(host, port, 3*time.Second); err != nil {
		return fmt.Errorf("无法连接代理服务器 %s:%d，请确认代理软件已启动且端口正确: %w", host, port, err)
	}
	return nil
}
