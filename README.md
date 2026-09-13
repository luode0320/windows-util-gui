# Windows 工具箱

面向 Windows + WSL 的本地运维小工具集合，纯 Go 实现的原生 GUI（基于 [walk](https://github.com/lxn/walk)），
编译后是单个 exe，无需安装运行时。

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

实际效果：

| 来源 | PID | 进程名 | 监听端点 | 状态 |
| --- | --- | --- | --- | --- |
| WSL:Ubuntu-24.04 | 516025 | python3 | TCP 0.0.0.0:45321 | LISTEN |
| Windows | 15632 | wslrelay.exe | TCP 127.0.0.1:45321 | LISTENING |

实现方式：

- Windows 侧：Toolhelp 快照（`CreateToolhelp32Snapshot`）枚举全部进程名，`netstat -ano` 取监听端点，
  结束进程走 `TerminateProcess`。全程避开控制台码页导致的中文乱码。
- WSL 侧：一次 `wsl.exe -d <发行版> -u root -e sh -c "ps ...; ss -lntup"` 同时取进程与端点
  （每起一次 `wsl.exe` 都有可观的固定开销，合并执行明显更快），`kill -9` 结束。
  `wsl.exe` 自身输出为 UTF-16LE，已统一解码处理。

监听端点只收 TCP 的 `LISTENING` 与全部 UDP：占用端口的是监听方，
`ESTABLISHED`、`TIME_WAIT` 这些连接态条目数量庞大且不构成占用，纳入只会淹没有效信息。

**系统核心进程会被拒绝结束**：Windows 侧的 `csrss.exe`、`lsass.exe`、`services.exe`、`winlogon.exe` 等，
以及 WSL 发行版内的 1 号进程（init/systemd）。按进程名模糊搜索配合「结束全部结果」极易把它们一并选中，
而它们几乎不可能是清理目标。注意 `svchost.exe` 不在名单里 —— 它承载大量普通服务，确实存在需要结束的正当场景。

### TG 消息转发

基于 Telegram 命令行工具（tdl）实现 Telegram 消息批量导出与分批自动化转发助手。
**tdl.exe 已通过 `go:embed` 编译进主程序**：单文件分发、零外部依赖，
首次运行时自动释放到 exe 同级的 `data\tgtransfer\bin\tdl.exe`（字节校验一致则不重复释放），
与原 tg-tdl-transfer 项目完全解耦，原项目可独立废弃：

- **会话与登录**：支持检测本地登录会话；未登录时可在界面点击“扫码登录”，弹窗展示登录二维码并监控手机 App 扫码状态，支持随时清理并重新扫码授权；
- **环境探测**：支持配置 SOCKS5/HTTP 代理，并在操作前执行网络前置连通性测试；支持多级智能探测与自定义选择 `tdl.exe` 路径；
- **聊天与频道管理**：一键刷新获取当前可见的全部群组与频道并持久化，提供独立的聊天列表表格窗口（展示 ID、类型、名称、用户名），支持一键复制 ID 或快速设置为源/目标；
- **消息导出**：支持 Saved Messages（收藏夹）或指定群组/频道历史消息导出，可选择导出全部或最近 N 条；支持文本、TXT、视频、小说、EPUB、MP4 等多种类型过滤；导出元数据持久化记录并可回溯选用；
- **分批转发控制**：从导出的 JSON 文件自动分批转发至目标群组/频道，支持保留原始来源（direct）与复制内容（clone）两种模式；默认启用试转发（dry-run）防止误触，提供跳过已转发记录去重（可按源与目标清除历史）、消息顺序反转、自定义批次大小与间隔秒数；支持任务中途手动安全停止。

## 运行

直接双击 `windows-util-gui.exe`，**启动时会弹出 UAC 提权**，以便结束系统级进程与访问 WSL 的 root 权限。
同意后程序以管理员身份重新启动，窗口标题会显示「管理员」；拒绝则降级为普通权限继续运行，
查询功能不受影响，但结束系统级进程会失败。

提权是程序启动时用 `ShellExecute` 的 `runas` 动词完成的，manifest 里写的是 `asInvoker`。
这么做是因为 `requireAdministrator` 会让 exe 无法被普通权限进程拉起，VS Code 按 F5 启动会直接失败。

VS Code 里按 **F5** 即可打包并启动（配置在 `.vscode/launch.json`）。需要断点调试时，
改选「调试源码（跳过提权）」——它带 `-no-elevate` 参数，程序不会把自己重新拉起为独立的管理员进程，
代价是该会话没有管理员权限。

## 构建

VS Code 里按 `Ctrl+Shift+B` 即可打包（任务定义在 `.vscode/tasks.json`），也可以直接跑命令：

```bash
go build -trimpath -ldflags "-s -w -H=windowsgui" -o bin/windows-util-gui.exe .
```

`-H=windowsgui` 不能省：Go 默认生成 CONSOLE 子系统程序，双击 exe 会额外弹出一个黑色控制台窗口。

想在终端里看启动错误时，用不带该参数的开发版构建：

```bash
go build -o bin/windows-util-gui-dev.exe .
```

仓库中已包含 `rsrc_windows_amd64.syso`（由 `build/app.manifest` 生成），Go 会自动把它链接进 exe。
**这个文件不能删**：walk 依赖 manifest 里声明的 comctl32 v6，缺少它程序会启动失败。

修改 `build/app.manifest` 后需重新生成：

```bash
go run github.com/akavel/rsrc@latest -manifest build/app.manifest -arch amd64 -o rsrc_windows_amd64.syso
```

## 测试

```bash
go test ./test/... -v
```

测试跑的是真实环境：真实执行 `netstat`、真实调用 `wsl.exe`、真实结束进程。
未安装 WSL 的机器上，WSL 相关用例会自动跳过而不是判失败。

## 新增工具

1. 在 `internal/tools/<工具名>/` 下实现业务逻辑；
2. 在 `internal/ui/` 下实现 `ui.Tool` 接口；
3. 在 `internal/ui/mainwindow.go` 的 `NewApp` 中把它加进 `tools` 切片。

左侧列表、顶部菜单与页面切换会自动生效。

## 目录结构

```
├── main.go                          程序入口
├── build/app.manifest               提权与 comctl32 v6 声明
├── rsrc_windows_amd64.syso          由 manifest 生成的资源文件（勿删）
├── internal/
│   ├── ui/                          主窗口与工具页面
│   ├── winutil/                     Windows 通用能力：进程、命令执行、编码
│   └── tools/
│       ├── prockill/                进程占用查杀
│       └── tgtransfer/              TG 消息导出与分批转发
├── resources/bin/                   本地运行工具依赖 (tdl.exe)
└── test/                            测试代码
```

## 改动日志

2026-09-12 14:29:54 feat: [进程占用查杀] 新增 Windows 工具箱 GUI 与进程查杀工具，支持端口、PID 与进程名查询
2026-09-13 16:30:00 feat: [TG消息转发] 整合 tg-tdl-transfer 功能模块至工具箱，新增原生 TG 消息转发菜单项与 UI，支持扫码登录、聊天列表管理、消息导出及自动化分批转发
2026-09-13 17:20:00 fix: [TG消息转发] 路径解析锚定到 exe 自身目录，移除对原 tg-tdl-transfer 项目的路径依赖，原项目可独立废弃
2026-09-13 17:45:00 feat: [TG消息转发] tdl.exe 通过 go:embed 编译进主程序，实现单文件免依赖分发，首次运行自动释放
2026-09-13 17:50:00 feat: [TG消息转发] tdl.exe 及其 LICENSE 纳入 Git 仓库版本管理，嵌入目录 tdlbin 随仓库提交
