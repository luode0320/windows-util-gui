# 项目规则：Windows 工具箱

## 项目定位

面向 Windows + WSL 的本地运维小工具集合，纯 Go + walk 的原生 GUI，编译为单个 exe。
项目本身就是 Windows 程序，**必须在 Windows 侧编译运行**，不要放进 WSL 编译。

## 技术栈约束

- GUI 库固定为 `github.com/lxn/walk`（纯 Go，无 CGO 依赖）。本机没有 gcc，**不要引入需要 CGO 的库**（Fyne、Gio 的 CGO 后端等）。
- `golang.org/x/sys` 锁在 v0.28.0：更高版本要求 go >= 1.26，而本机是 go 1.24.5。升级前先确认 Go 版本。
- `rsrc_windows_amd64.syso` 必须随仓库提交。walk 依赖 manifest 声明的 comctl32 v6，缺少它程序启动即失败。
- **发布构建必须带 `-ldflags "-H=windowsgui"`**：Go 默认生成 CONSOLE 子系统程序，双击 exe 会多弹一个黑色控制台窗口。
  构建任务见 `.vscode/tasks.json`，不要绕过它直接 `go build` 出发布包。

## 提权机制

manifest 里的 `requestedExecutionLevel` **必须保持 `asInvoker`**，不要改回 `requireAdministrator`。

`requireAdministrator` 会让非提权进程启动本程序时直接收到 `ERROR_ELEVATION_REQUIRED`，
VS Code 按 F5 启动会失败。提权改由 `internal/winutil/elevate.go` 在运行时用 `ShellExecute` 的
`runas` 动词完成：非提权实例拉起管理员实例后立即退出，对用户而言仍是"启动即弹 UAC"。

调试时必须传 `-no-elevate`，否则程序会把自己重新拉起为独立的管理员进程，调试器只剩空壳、断点全失效。
`.vscode/launch.json` 的调试配置已经带上该参数。

## walk 使用注意

以下都是本项目踩过的坑，改 UI 时注意：

- **不要用 `NumberEdit`**：declarative 会先于 `MinValue`/`MaxValue` 应用 `Value`，非零初值必然越界报错；
  且它在清空、全选覆盖、粘贴时会把输入拼到旧值上再因越界静默回退，用户改不动。用 `LineEdit` + 提交时校验。
- **HBox 里固定宽度的侧栏**要给内容区显著更大的 `StretchFactor`，否则侧栏被 `MaxSize` 截断后留下的空白不会让给内容区。
- **耗时操作必须放后台协程**，结果通过 `mainWindow.Synchronize` 回到 UI 线程，否则界面假死。
- **`TableView` 换数据后必须显式 `Invalidate()`**：walk 响应 `PublishRowsReset` 时只发一条带
  `LVSICF_NOINVALIDATEALL` 的 `LVM_SETITEMCOUNT`（见 walk `tableview.go` 的 `rowsReset` 处理与
  `setItemCount`），全程没有 invalidate。虚拟列表会沿用已绘制行的缓存，屏幕上残留上一批数据，
  表现为"查完 A 再查 B，表格里还挂着 A 的记录"。同理，发起新查询时应先清空表格，
  避免耗时期间旧结果被误读。

## 外部命令调用

- 一律走 `internal/winutil.RunHidden`：它统一处理隐藏控制台窗口（`CREATE_NO_WINDOW`）、超时兜底和输出解码。
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

- 测试代码统一放根目录 `test/`，不与生产代码混放。
- 优先做真实环境验证：真跑 `netstat`、真调 `wsl.exe`、真杀进程，不要 mock 掉这些边界。
- 依赖 WSL 的用例在无 WSL 环境要 `t.Skip` 而不是失败。
- **`wsl.exe` 会话退出会连带杀掉该会话内的后台进程**，`nohup`、`setsid` 都挡不住。
  测试里需要 WSL 内长驻进程时，保持 `wsl.exe` 子进程存活（`exec.Command` + `Start`），用完再 `Kill`。
- WSL 冷启动耗时不固定，等待服务就绪要用轮询，不要固定 `sleep`。

## 代码风格

- 注释一律中文。函数注释写清 `[参数]`/`[返回]`/`最近修改时间`；字段与行内注释只写一句话作用。
- 注释解释**为什么**这么写（尤其是上面那些坑），不要复述代码在做什么。
- 方法体内的多步流程用 `// 1.` `// 2.` 编号标注。
