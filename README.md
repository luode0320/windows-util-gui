# Windows 工具箱

面向 Windows + WSL 的本地运维小工具集合，**Go + Wails v2** 实现的原生桌面 GUI
（WebView2 渲染，无 CGO、无 Node 构建链），编译后是单个 exe，无需安装运行时。

## 已收录工具

### 进程占用查杀

一次查出 **Windows 宿主**与**所有运行中 WSL 发行版**里的目标进程，并可选中结束。支持三种查询方式：

| 方式 | 输入 | 说明 |
| --- | --- | --- |
| 按端口 | `8080` | 查占用该端口的进程，结果只保留命中的那个端点 |
| 按 PID | `1234` | 精确定位单个进程 |
| 按进程名 | `node` | 模糊匹配，至少 2 个字符 |

三种方式共用同一份采集结果：先把两侧的全部进程与监听端点取回来再过滤，
所以按 PID 或进程名查到的进程也能顺带看到它监听了哪些端口 —— 决定要不要结束它时，这是最关键的信息。

WSL2 跑在独立网络命名空间中，发行版内监听的端口会由 `wslrelay` 中转到 Windows 侧，
所以同一个端口经常在两侧各出现一条记录 —— 两边都处理掉，端口才算真正释放。

**系统核心进程会被拒绝结束**：Windows 侧的 `csrss.exe`、`lsass.exe`、`services.exe`、`winlogon.exe` 等，
以及 WSL 发行版内的 1 号进程（init/systemd）。注意 `svchost.exe` 不在名单里 ——
它承载大量普通服务，确实存在需要结束的正当场景。

### TG 消息转发

基于 Telegram 命令行工具（tdl）实现 Telegram 消息批量导出与分批自动化转发助手。
**tdl.exe 已通过 `go:embed` 编译进主程序**：单文件分发、零外部依赖，
首次运行时自动释放到用户统一数据目录 `%LOCALAPPDATA%\windows-util-gui\tgtransfer\bin\tdl.exe`（字节校验一致则不重复释放；登录会话、导出记录、日志同在该目录下，旧版 exe 目录数据首次运行时自动搬迁）：

- **会话与登录**：检测本地登录会话；未登录时扫码登录（弹窗展示二维码并监控手机 App 扫码状态），支持清理会话重新授权；
- **环境探测**：SOCKS5/HTTP 代理配置 + 网络前置连通性测试；
- **聊天与频道管理**：刷新/持久化聊天列表，聊天列表弹窗支持一键复制 ID、设为源/目标；
- **消息导出**：Saved Messages 或指定群组/频道，全部或最近 N 条，文本/视频/EPUB 等类型筛选，导出元数据持久化可回溯；
- **分批转发**：direct（保留来源）/ clone（复制内容）两种模式，试转发防误触，跳过已转发去重，自定义批次与间隔，任务中途安全停止，支持清空转发历史。

## 运行

直接双击 `wailsapp\build\bin\windows-util-gui-wails.exe`，**启动时会弹出 UAC 提权**，
以便结束系统级进程与访问 WSL 的 root 权限。同意后程序以管理员身份重新启动（窗口标题显示「管理员」）；
拒绝则降级为普通权限继续运行，查询功能不受影响。调试时可加 `-no-elevate` 跳过自提权。

## 构建

VS Code 里 `Ctrl+Shift+B`（任务定义在 `.vscode/tasks.json`，内部调用 wails CLI），或手动：

```bash
cd wailsapp
wails build
# 产物：wailsapp/build/bin/windows-util-gui-wails.exe
```

日常改前端可用 `wails dev` 热重载（任务"开发模式（热重载）"）。

依赖：Go 1.24+、[wails CLI v2](https://wails.io/)（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`）、
WebView2 Runtime（Win10/11 一般已内置）。全程无 CGO、无 Node。

## 测试

```bash
go test ./test/... -v
```

测试跑的是真实环境：真实执行 `netstat`、真实调用 `wsl.exe`、真实结束进程。
非 Windows 环境相关用例自动跳过。

## 架构说明

```
├── wailsapp/                        # Wails UI 宿主（独立 Go 模块，replace 指向主工程）
│   ├── main.go / app.go             # 窗口入口与前端绑定（Query/Kill/Tg*）
│   └── frontend/dist/               # 纯 HTML/CSS/JS 前端（无构建链，go:embed 内嵌）
├── pkg/toolapi/                     # 公开门面：前端绑定 → 业务层的唯一通道
├── internal/
│   ├── tools/
│   │   ├── prockill/                # 进程查杀业务（Windows Toolhelp + WSL 合并采集）
│   │   └── tgtransfer/              # TG 导出/转发业务（tdl 封装、会话、历史）
│   └── winutil/                     # Windows 通用能力：提权、命令执行、编码
└── test/                            # 黑盒测试（真实环境，不 mock）
```

关键设计约束（详见 CLAUDE.md）：
- Go `internal/` 包不允许被独立模块导入，UI 宿主必须经 `pkg/toolapi` 公开门面访问业务层；
- `wails build` 生成绑定阶段会执行一次二进制，入口的自提权逻辑用 `bindingGen` 构建标签守卫；
- 长任务（登录/导出/转发）通过 `EventsEmit`（`tg:log` / `tg:qr` / `tg:opDone`）向前端推送进度。

## 改动日志

2026-09-12 14:29:54 feat: [进程占用查杀] 新增 Windows 工具箱 GUI 与进程查杀工具，支持端口、PID 与进程名查询
2026-09-13 16:30:00 feat: [TG消息转发] 整合 tg-tdl-transfer 功能模块至工具箱，新增原生 TG 消息转发菜单项与 UI，支持扫码登录、聊天列表管理、消息导出及自动化分批转发
2026-09-13 18:41:00 feat: [GUI迁移] Wails v2 PoC 验证通过：端口查询闭环 + 单文件 exe + 无 CGO 构建链验证
2026-09-13 19:32:00 feat: [GUI迁移] 进程查杀支持勾选结束与二次确认弹窗，接入自提权
2026-09-13 19:35:00 fix: [GUI迁移] 修复结果表格与 TG 页面内容超高被裁切的问题，补齐滚动与表头吸附
2026-09-13 20:20:00 refactor: [GUI迁移] UI 层从 walk 迁移到 Wails v2（WebView2），业务层零改动；退役 walk 相关代码与构建资源，构建/调试任务切换为 wails
2026-09-13 22:30:00 fix: [TG消息转发] tdl 数据目录迁移到 %LOCALAPPDATA% 用户统一位置，旧 exe 目录数据自动搬迁，避免只读目录写入失败
2026-09-13 22:40:00 fix: [构建] 打包任务改用项目内 tools/wails.exe，修复用户目录路径解析失败导致终端进程启动失败的问题
2026-09-13 23:02:00 fix: [TG消息转发] 清理旧版残留的 exe 目录数据空壳，桌面启动不再残留 data 文件夹
2026-09-13 23:40:00 fix: [TG消息转发] 二维码改为 base64 data URL 推送，修复 WebView2 禁止 file:// 加载导致的扫码登录裂图
