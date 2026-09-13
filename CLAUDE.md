# 项目规则：Windows 工具箱

## 项目定位

面向 Windows + WSL 的本地运维小工具集合，**Go + Wails v2**（WebView2 渲染）桌面 GUI。
UI 宿主工程在 `wailsapp/`（独立 Go 模块），业务层在 `internal/`，编译为单个 exe。
项目本身就是 Windows 程序，**必须在 Windows 侧编译运行**，不要放进 WSL 编译。

## 技术栈约束

- UI 框架为 **Wails v2（WebView2 渲染）**，前端是 `wailsapp/frontend/dist/` 下的纯 HTML/CSS/JS，
  **禁止引入 Node 构建链、npm 依赖或 CDN 外链**（单文件离线分发是硬要求）。
- **全程无 CGO**：本机没有 gcc，任何需要 CGO 的库（Fyne、Gio 的 CGO 后端、Qt 等）一律不得引入。
- `golang.org/x/sys` 锁在 v0.28.0：更高版本要求更高 Go 版本，升级前先确认工具链。
- **Go `internal/` 可见性规则**：`wailsapp/` 是独立模块，不能 import 主工程的 `internal/`，
  跨模块访问必须走 `pkg/toolapi` 公开门面；DTO 的 JSON 键名（小驼峰）是前后端契约，
  改键名必须同步前端并回写实施总览。
- **wails build 绑定生成陷阱**：生成绑定阶段会以 `bindings` tag 编译并无参执行一次入口二进制，
  入口里的自提权等副作用必须用 `bindingGen` 构建标签守卫（见 `wailsapp/binding_guard*.go`），
  否则构建会被 UAC 弹窗卡死。
- 长任务（登录/导出/转发/查询）一律放后台 goroutine，进度经 `runtime.EventsEmit`
  推送到前端（`tg:log` / `tg:qr` / `tg:opDone`），禁止在绑定方法里同步阻塞。

## 提权机制

- 启动时自提权：`!ShouldSkipElevate() && !IsElevated()` 时用 `ShellExecute runas`
  拉起管理员实例后当前进程退出；用户拒绝 UAC 时降级为普通权限继续运行。
  **不要改回 `requireAdministrator`**：会让非提权进程拉不起本程序，VS Code 调试直接失败。
- 调试必须传 `-no-elevate`（winutil 识别该参数跳过自提权），
  否则程序把自己重新拉起为独立管理员进程，调试器只剩空壳。
- 历史经验：提权实例写过的 WebView2 用户数据目录，普通权限实例仍可正常启动（已实测），
  但若未来调整数据目录位置需重新做双顺序验证。

## 外部命令调用

- 一律走 `internal/winutil.RunHidden`：统一处理隐藏控制台窗口（`CREATE_NO_WINDOW`）、超时兜底和输出解码。
  GUI 程序直接 `exec.Command` 会闪黑框。
- `wsl.exe` 自身的输出（如 `-l -q`）是 **UTF-16LE**，`RunHidden` 已自动识别解码；Linux 命令的输出则是 UTF-8 透传。
- 解析 `netstat`、`tasklist` 等控制台输出时**只依赖 ASCII 字段**（端口、PID、协议）。
  需要进程名时走 Win32 API，不要解析本地化文案。
- **每起一次 `wsl.exe` 都有可观的固定开销**，同一发行版内要跑多条命令时用
  `sh -c "cmd1; echo <分隔符>; cmd2"` 合并成一次调用，再按分隔符切分输出。

## 破坏性操作约束

- 结束进程不可撤销，任何批量结束路径都必须先弹二次确认，并**逐条列出**目标进程。
- 系统核心进程走 `prockill.IsProtected` 拦截，改动保护名单要慎重：
  名单太松会让模糊搜索 + 「结束全部」直接搞崩系统，太严又会挡掉正当需求
  （`svchost.exe` 就是刻意排除在外的）。
- 模糊搜索类输入要有最短长度限制，单字符关键字会匹配到几乎所有进程。

## 测试约定

- 测试代码统一放根目录 `test/`，不与生产代码混放；黑盒测试按真实环境跑，
  不 mock 掉 `netstat`、`wsl.exe`、真实杀进程这些边界。
- 依赖 WSL 的用例在无 WSL 环境要 `t.Skip` 而不是失败；非 Windows 环境（GOOS 判断）同样 Skip。
- **`wsl.exe` 会话退出会连带杀掉该会话内的后台进程**，`nohup`、`setsid` 都挡不住。
  测试里需要 WSL 内长驻进程时，保持 `wsl.exe` 子进程存活（`exec.Command` + `Start`），用完再 `Kill`。
- WSL 冷启动耗时不固定，等待服务就绪要用轮询，不要固定 `sleep`。

## 代码风格

- 注释一律中文。函数注释写清 `[参数]`/`[返回]`/`最近修改时间`；字段与行内注释只写一句话作用。
- 注释解释**为什么**这么写（尤其是踩过的坑），不要复述代码在做什么。
- 方法体内的多步流程用 `// 1.` `// 2.` 编号标注。

## walk 时代经验（已退役，保留供遇到类似问题参考）

- walk 的 NumberEdit 在清空/粘贴时会把输入拼到旧值再静默回退，纯文本输入 + 提交时校验更可靠；
  TableView 换数据后必须显式 Invalidate，否则虚拟列表残留旧行——这类"控件行为怪癖"问题
  在 WebView 方案里不再存在，但"数据变更必须主动通知视图"的教训仍适用于前端渲染。
