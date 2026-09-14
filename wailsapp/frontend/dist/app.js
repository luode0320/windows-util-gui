// ===== 进程占用查杀：前端逻辑 =====
// 绑定契约（冻结，调用方式不得更改）：
//   window.go.main.App.Query(kind, value)  -> Promise<[{origin,pid,name,listening,state}]>
//   window.go.main.App.IsElevated()       -> Promise<boolean>
// 仅在 Wails 打包的 exe 内存在 window.go；浏览器直接打开时做兜底。

(function () {
  "use strict";

  // 简化 DOM 取值
  const $ = (id) => document.getElementById(id);
  const input = $("search-input");
  const btn = $("query-btn");
  const tbody = $("tbody");
  const errBox = $("error-box");
  const statusLine = $("status-line");
  const countChip = $("count-chip");
  const logBody = $("log-body");
  const segs = Array.from(document.querySelectorAll("#segmented .seg"));

  // 勾选 / 结束相关 DOM
  const selectAllCb = $("select-all");
  const killSelectedBtn = $("kill-selected-btn");
  const killAllBtn = $("kill-all-btn");
  const killOverlay = $("kill-overlay");
  const killList = $("kill-list");
  const killCount = $("kill-count");
  const killElevWarn = $("kill-elev-warn");
  const killCancelBtn = $("kill-cancel");
  const killConfirmBtn = $("kill-confirm");

  // 三种查询方式对应的占位符与中文标签
  const PLACEHOLDERS = {
    port: "输入端口号，如 8080",
    pid: "输入 PID，如 1234",
    name: "模糊匹配，如 node",
  };
  const KIND_LABEL = { port: "端口", pid: "PID", name: "进程名" };

  let current = "port"; // 当前查询方式
  let lastRows = [];     // 最近一次查询返回的完整 Row 对象数组（原样保留，含 source/distro）
  let selected = new Set(); // 当前勾选的行索引集合
  let elevated = false;  // 是否管理员，驱动非管理员橙色提示
  let killBusy = false;  // 结束进行中，防重复点击
  let pendingKill = null; // 待结束的 Row 对象数组（弹窗确认时锁定）

  // ===== 安全取用 Go 侧绑定对象 =====
  function getApp() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  // ===== 文本转义，避免来源/进程名中的尖括号破坏结构 =====
  function escapeHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  // ===== 执行日志：带 HH:MM:SS 时间戳，最多保留 50 行 =====
  function logLine(text, isErr) {
    const now = new Date();
    const ts = [now.getHours(), now.getMinutes(), now.getSeconds()]
      .map((n) => String(n).padStart(2, "0")).join(":");
    const div = document.createElement("div");
    div.className = "log-line" + (isErr ? " err" : "");
    const t = document.createElement("span");
    t.className = "log-time";
    t.textContent = ts;
    div.appendChild(t);
    div.appendChild(document.createTextNode(text));
    logBody.appendChild(div);
    while (logBody.children.length > 50) logBody.removeChild(logBody.firstChild);
    logBody.scrollTop = logBody.scrollHeight;
  }

  // ===== 清空结果区（切换方式时调用）=====
  function clearResults() {
    errBox.style.display = "none";
    countChip.textContent = "共 0 条";
    lastRows = [];
    selected.clear(); // 清空勾选，同步按钮与全选态
    tbody.innerHTML = '<tr><td colspan="6"><div class="empty">选择查询方式并输入条件开始查询</div></td></tr>';
    syncSelectAll();
    updateKillButtons();
  }

  // ===== 分段切换：高亮 + 占位符 + 清空输入与结果 =====
  function selectKind(kind) {
    current = kind;
    segs.forEach((s) => s.classList.toggle("active", s.dataset.kind === kind));
    input.placeholder = PLACEHOLDERS[kind];
    input.value = "";
    clearResults();
    statusLine.textContent = "";
  }

  // ===== 来源徽章：WSL: 前缀 -> 「WSL · 发行版」，否则视为 Windows =====
  function originBadge(origin) {
    if (typeof origin === "string" && origin.startsWith("WSL:")) {
      return { cls: "src-wsl", text: "WSL · " + origin.slice(4) };
    }
    return { cls: "src-win", text: origin || "Windows" };
  }

  // ===== 渲染结果行 =====
  function renderRows(rows) {
    lastRows = rows || [];
    selected.clear(); // 每次重查 / 重新渲染后清空勾选
    countChip.textContent = "共 " + lastRows.length + " 条";
    if (!lastRows.length) {
      tbody.innerHTML = '<tr><td colspan="6"><div class="empty">没有找到匹配 ' +
        escapeHtml(input.value.trim()) + ' 的进程</div></td></tr>';
      syncSelectAll();
      updateKillButtons();
      return;
    }
    tbody.innerHTML = lastRows.map((r, i) => {
      const b = originBadge(r.origin);
      return '<tr>' +
        '<td class="col-check"><input type="checkbox" class="row-check" data-idx="' + i + '"></td>' +
        '<td><span class="src-chip ' + b.cls + '">' + escapeHtml(b.text) + '</span></td>' +
        '<td class="pid">' + escapeHtml(r.pid) + '</td>' +
        '<td class="mono">' + escapeHtml(r.name) + '</td>' +
        '<td class="mono">' + escapeHtml(r.listening) + '</td>' +
        '<td><span class="state-ok">' + escapeHtml(r.state || "-") + '</span></td>' +
        '</tr>';
    }).join("");
    syncSelectAll();
    updateKillButtons();
  }

  // ===== 结束按钮可用态：无勾选/无结果时禁用 =====
  function updateKillButtons() {
    killSelectedBtn.disabled = selected.size === 0;
    killAllBtn.disabled = lastRows.length === 0;
  }

  // ===== 全选框三态联动 =====
  function syncSelectAll() {
    const n = lastRows.length;
    selectAllCb.checked = n > 0 && selected.size === n;
    selectAllCb.indeterminate = selected.size > 0 && selected.size < n;
  }

  // ===== 取行的来源徽章文案（日志与弹窗复用）=====
  function badgeText(row) {
    return originBadge(row && row.origin).text;
  }

  // ===== 执行查询 =====
  async function doQuery() {
    const value = input.value.trim();
    if (!value) {
      statusLine.textContent = "请输入查询条件后重试";
      input.focus();
      return;
    }
    const app = getApp();
    errBox.style.display = "none";
    btn.disabled = true;
    btn.textContent = "查询中…";
    statusLine.textContent = "正在采集 netstat 与全部 WSL 发行版的进程信息，耗时可达数秒…";
    const start = performance.now();
    logLine("开始查询 · 按" + KIND_LABEL[current] + "「" + value + "」");

    try {
      // 契约调用：Query(kind, value)
      if (!app || typeof app.Query !== "function") {
        throw "仅在 exe 内可执行查询";
      }
      const rows = await app.Query(current, value);
      const ms = Math.round(performance.now() - start);
      statusLine.textContent = "查询完成，耗时 " + ms + " ms";
      renderRows(rows || []);
      logLine("完成 " + (rows ? rows.length : 0) + " 条 · 耗时 " + ms + "ms");
    } catch (e) {
      // reject 时错误为字符串
      const msg = (typeof e === "string") ? e : (e && e.message ? e.message : String(e));
      errBox.textContent = msg;
      errBox.style.display = "block";
      statusLine.textContent = "";
      logLine("查询失败：" + msg, true);
    } finally {
      btn.disabled = false;
      btn.textContent = "查询";
    }
  }

  // ===== 权限状态：IsElevated 同时驱动侧栏卡片与头部 chip =====
  function applyElevated(elev) {
    elevated = !!elev; // 记录管理员态，供弹窗橙色提示使用
    $("perm-dot").className = elevated ? "dot" : "dot gray";
    $("perm-text").textContent = elevated ? "管理员权限" : "普通权限";
    $("perm-note").textContent = elevated
      ? "可结束系统级进程、访问 WSL root"
      : "结束系统级进程会失败";
    $("elev-text").textContent = elevated ? "已提权 · 管理员" : "普通权限";
    $("elev-chip").className = elevated ? "chip ok" : "chip warn";
  }

  // ===== 打开结束确认弹窗：targets 为待杀 Row 对象数组 =====
  function openKillModal(targets) {
    pendingKill = targets;
    killCount.textContent = targets.length;
    killList.innerHTML = targets.map((r) => {
      const b = originBadge(r.origin);
      return '<div class="kill-item">' +
        '<span class="src-chip ' + b.cls + '">' + escapeHtml(b.text) + '</span>' +
        '<span class="kill-pid">' + escapeHtml(r.pid) + '</span>' +
        '<span class="kill-name mono">' + escapeHtml(r.name) + '</span>' +
        '<span class="kill-listen mono">' + escapeHtml(r.listening) + '</span>' +
        '</div>';
    }).join("");
    // 非管理员时显示橙色提示，管理员时隐藏
    killElevWarn.hidden = elevated;
    killOverlay.hidden = false;
  }

  // ===== 关闭弹窗：恢复按钮态 =====
  function closeKillModal() {
    killOverlay.hidden = true;
    killBusy = false;
    killConfirmBtn.disabled = false;
    killConfirmBtn.textContent = "确认结束";
    pendingKill = null;
  }

  // ===== 确认结束：调用 Kill 并逐条写日志，最后自动重查 =====
  async function confirmKill() {
    if (killBusy || !pendingKill || !pendingKill.length) return;
    const targets = pendingKill;
    killBusy = true;
    killConfirmBtn.disabled = true;
    killConfirmBtn.textContent = "结束中…";
    const app = getApp();
    try {
      // 契约调用：Kill(rows)，rows 为原始 Row 对象数组（原样回传，不裁剪字段）
      if (!app || typeof app.Kill !== "function") {
        throw "仅在 exe 内可执行结束进程";
      }
      const res = await app.Kill(targets);
      (res || []).forEach((r) => {
        const row = r.row || {};
        const label = "[" + badgeText(row) + "] PID " + row.pid + " " + (row.name || "");
        if (r.success) {
          logLine("已结束：" + label);
        } else {
          // 失败行使用错误色（.log-line.err）
          logLine("失败：" + label + " —— " + (r.error || "未知错误"), true);
        }
      });
    } catch (e) {
      const msg = (typeof e === "string") ? e : (e && e.message ? e.message : String(e));
      logLine("结束进程失败：" + msg, true);
    } finally {
      closeKillModal();
      doQuery(); // 复用现有查询流程刷新结果
    }
  }

  function initElevated() {
    const app = getApp();
    if (!app || typeof app.IsElevated !== "function") return; // 缺失时保持「检测中…」
    app.IsElevated().then(applyElevated).catch(function () {});
  }

  // ===== 事件绑定 =====
  segs.forEach((s) => s.addEventListener("click", () => selectKind(s.dataset.kind)));
  btn.addEventListener("click", doQuery);
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") doQuery(); });

  // 行级 checkbox：事件委托，切换勾选集合
  tbody.addEventListener("change", (e) => {
    const cb = e.target.closest(".row-check");
    if (!cb) return;
    const idx = Number(cb.dataset.idx);
    if (cb.checked) selected.add(idx); else selected.delete(idx);
    syncSelectAll();
    updateKillButtons();
  });

  // 全选 / 取消全选：联动所有行勾选
  selectAllCb.addEventListener("change", () => {
    if (selectAllCb.checked) lastRows.forEach((_, i) => selected.add(i));
    else selected.clear();
    tbody.querySelectorAll(".row-check").forEach((c) => { c.checked = selectAllCb.checked; });
    updateKillButtons();
  });

  // 结束选中进程：仅对勾选行
  killSelectedBtn.addEventListener("click", () => {
    if (killBusy) return;
    const targets = Array.from(selected).sort((a, b) => a - b).map((i) => lastRows[i]);
    if (!targets.length) return;
    openKillModal(targets);
  });

  // 结束全部结果：对当前全部行
  killAllBtn.addEventListener("click", () => {
    if (killBusy || !lastRows.length) return;
    openKillModal(lastRows.slice());
  });

  // 弹窗关闭：取消按钮 / 点遮罩 / Esc（进行中禁止关闭）
  killCancelBtn.addEventListener("click", () => { if (!killBusy) closeKillModal(); });
  killOverlay.addEventListener("click", (e) => { if (e.target === killOverlay && !killBusy) closeKillModal(); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !killOverlay.hidden && !killBusy) closeKillModal();
  });

  // 确认结束
  killConfirmBtn.addEventListener("click", confirmKill);

  // 初始化：默认按端口 + 拉取权限
  selectKind("port");
  initElevated();
})();

// =====================================================================
// TG 消息转发页：独立 IIFE，与进程页逻辑作用域隔离（不污染既有变量）
// 绑定契约（冻结，调用方式不得更改）：
//   TgGetState()                -> {loggedIn,tdlPath,tdlBundled,proxy,chats[],records[]}
//   TgSetProxy(proxy)           -> void
//   TgValidateNetwork(proxy)    -> 可读结果文本（失败抛错）
//   TgRefreshChats()            -> 聊天数组
//   TgRecords()                 -> [...]
//   TgDeleteRecord(path)        -> void
//   TgStartLogin()              -> void
//   TgCancel()                  -> void
//   TgExport(opts)              -> void
//   TgForward(opts)             -> void
//   TgClearHistory(sourceId,target) -> void
//   TgClearSession()               -> void
// 事件（window.runtime.EventsOn）：tg:log{line} / tg:qr{dataURL} / tg:loginConfirmed / tg:opDone{op,ok,error}
// 浏览器直接打开（无 window.go / window.runtime）时全部走兜底提示，不白屏。
// =====================================================================

(function () {
  "use strict";

  // ===== 简化取值 =====
  const $ = (id) => document.getElementById(id);

  // 页面容器与导航
  const navProcess = $("nav-process");
  const navTg = $("nav-tg");
  const pageProcess = $("page-process");
  const pageTg = $("page-tg");

  // 卡片①
  const tgLoginDot = $("tg-login-dot");
  const tgLoginText = $("tg-login-text");
  const tgLoginBtn = $("tg-login-btn");
  const tgNetBtn = $("tg-net-btn");
  const tgProxyInput = $("tg-proxy-input");
  const tgProxyBtn = $("tg-proxy-btn");
  // 卡片②
  const tgSrcSelect = $("tg-src-select");
  const tgChatsBtn = $("tg-chats-btn");
  const tgRangeSeg = $("tg-range-seg");
  const tgLastCount = $("tg-last-count");
  const tgFilterChips = $("tg-filter-chips");
  const tgExportBtn = $("tg-export-btn");
  // 卡片③
  const tgRecordSelect = $("tg-record-select");
  const tgDelRecordBtn = $("tg-del-record-btn");
  let tgRecordList = []; // 最近一次 TgRecords 返回的完整记录（含 sourceId/path，供转发与清历史使用）
  const tgClearHistoryBtn = $("tg-clear-history-btn");
  const tgTargetSelect = $("tg-target-select");
  const tgSetTargetBtn = $("tg-set-target-btn");
  const tgFwdModeSeg = $("tg-fwd-mode-seg");
  const tgDryRun = $("tg-dry-run");
  const tgSkipFwd = $("tg-skip-fwd");
  const tgReverse = $("tg-reverse");
  const tgBatchSize = $("tg-batch-size");
  const tgDelaySec = $("tg-delay-sec");
  const tgForwardBtn = $("tg-forward-btn");
  const tgStopBtn = $("tg-stop-btn");
  // 底部署性 / 日志
  const tgBundleChip = $("tg-bundle-chip");
  const tgBundleText = $("tg-bundle-text");
  const tgLogBody = $("tg-log-body");
  const tgStatusLine = $("tg-status-line");
  // 模态
  const tgQrOverlay = $("tg-qr-overlay");
  const tgQrImg = $("tg-qr-img");
  const tgQrSpinner = $("tg-qr-spinner");
  const tgQrPlaceholder = $("tg-qr-placeholder");
  const tgQrHint = $("tg-qr-hint");
  const tgQrCancel = $("tg-qr-cancel");
  const tgChatsOverlay = $("tg-chats-overlay");
  const tgChatsSearch = $("tg-chats-search");
  const tgChatsCount = $("tg-chats-count");
  const tgChatsClose = $("tg-chats-close");
  const tgChatsTbody = $("tg-chats-tbody");
  const tgChatsCancel = $("tg-chats-cancel");

  // 本地状态
  let tgInited = false;        // 是否已惰性初始化
  let tgBusy = false;          // 长任务进行中
  let tgCurrentOp = "";        // 当前操作名（状态条展示）
  let tgLoggedIn = false;      // 当前登录态（fillLogin 维护，重登确认分支使用）
  let tgChatList = [];         // 最近一次拉取的聊天列表（搜索过滤的数据源）

  // ===== 安全取用 Go 侧绑定对象 =====
  function getApp() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  // ===== 文本转义 =====
  function escapeHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  // ===== TG 日志：带时间戳，最多 50 行 =====
  function logTg(text, isErr) {
    const now = new Date();
    const ts = [now.getHours(), now.getMinutes(), now.getSeconds()]
      .map((n) => String(n).padStart(2, "0")).join(":");
    const div = document.createElement("div");
    div.className = "log-line" + (isErr ? " err" : "");
    const t = document.createElement("span");
    t.className = "log-time";
    t.textContent = ts;
    div.appendChild(t);
    div.appendChild(document.createTextNode(text));
    tgLogBody.appendChild(div);
    while (tgLogBody.children.length > 50) tgLogBody.removeChild(tgLogBody.firstChild);
    tgLogBody.scrollTop = tgLogBody.scrollHeight;
  }

  // ===== 状态条：空闲 / 执行中 + 当前操作名 =====
  function setTgStatus(text, busy) {
    tgBusy = !!busy;
    tgCurrentOp = text || "";
    tgStatusLine.textContent = busy ? ("执行中 · " + tgCurrentOp) : (text || "空闲");
    // 忙碌时禁用主操作按钮，避免重复触发
    tgExportBtn.disabled = busy;
    tgForwardBtn.disabled = busy;
  }

  // ===== 安全调用契约方法：无 window.go 时抛友好错误 =====
  function callTg(method, ...args) {
    const app = getApp();
    if (!app || typeof app[method] !== "function") {
      throw "仅在 exe 内可执行该操作";
    }
    return app[method].apply(app, args);
  }

  // ===== 取分段控件当前选中 mode =====
  function segMode(segEl) {
    const active = segEl.querySelector(".seg.active");
    return active ? active.dataset.mode : "";
  }

  // ===== 填充源/目标聊天下拉（首项：收藏夹 Saved Messages）=====
  function fillChatSelects(chats) {
    const list = chats || [];
    const savedOpt = '<option value="saved" data-saved="1">收藏夹（Saved Messages）</option>';
    const opts = list.map((c) => {
      const id = (c && (c.id != null ? c.id : ""));
      const name = (c && (c.name || c.username || ("ID " + id))) || ("ID " + id);
      const esc = escapeHtml(String(name));
      return '<option value="' + escapeHtml(String(id)) + '">' + esc + '</option>';
    }).join("");
    const html = savedOpt + opts;
    tgSrcSelect.innerHTML = html || "<option>无聊天</option>";
    tgTargetSelect.innerHTML = html || "<option>无聊天</option>";
  }

  // ===== 填充登录状态 =====
  function fillLogin(state) {
    const loggedIn = !!(state && state.loggedIn);
    tgLoginDot.className = loggedIn ? "dot" : "dot gray";
    // 两种状态的按钮：未登录=扫码登录；已登录=重新登录（清会话后重扫，中途关闭即等同退出登录）
    if (loggedIn) {
      tgLoginText.textContent = "已登录";
      tgLoginBtn.disabled = false;
      tgLoginBtn.textContent = "重新登录";
    } else {
      tgLoginText.textContent = "未登录";
      tgLoginBtn.disabled = false;
      tgLoginBtn.textContent = "扫码登录";
    }
    tgLoggedIn = loggedIn;
  }

  // ===== 内置状态 chip =====
  function fillBundle(state) {
    const bundled = !!(state && state.tdlBundled);
    if (bundled) {
      tgBundleChip.className = "chip ok";
      tgBundleText.textContent = "tdl.exe 已内置";
    } else {
      tgBundleChip.className = "chip warn";
      tgBundleText.textContent = "tdl.exe 未内置";
    }
  }

  // ===== 填充导出记录下拉（摘要：时间|来源|条数）=====
  function fillRecords(records) {
    const list = records || [];
    tgRecordList = list;
    if (!list.length) {
      tgRecordSelect.innerHTML = '<option value="">暂无导出记录</option>';
      return;
    }
    // 记录 DTO 键为小驼峰（createdAt/sourceName/messageCount/path），option value 用索引
    // 便于清空历史时从 tgRecordList 取 sourceId
    tgRecordSelect.innerHTML = list.map((r, i) => {
      const time = r && r.createdAt ? r.createdAt : "";
      const src = r && r.sourceName ? r.sourceName : "未知来源";
      const cnt = r && r.messageCount != null ? r.messageCount : "?";
      const label = time + " | " + src + " | " + cnt + " 条";
      return '<option value="' + i + '">' + escapeHtml(label) + "</option>";
    }).join("");
  }

  // ===== 拉取整体状态（首次进入 / opDone=login 时调用）=====
  async function refreshTgState() {
    try {
      const state = await callTg("TgGetState");
      fillLogin(state);
      fillBundle(state);
      fillChatSelects(state && state.chats);
      if (state && typeof state.proxy === "string") tgProxyInput.value = state.proxy;
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      setTgStatus("状态获取失败：" + msg, false);
      logTg("获取 TG 状态失败：" + msg, true);
    }
  }

  // ===== 刷新导出记录下拉 =====
  async function refreshTgRecords() {
    try {
      const records = await callTg("TgRecords");
      fillRecords(records);
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("获取导出记录失败：" + msg, true);
    }
  }

  // ===== 事件订阅（仅 Wails 内）=====
  function bindTgEvents() {
    const runtime = window.runtime;
    if (!runtime || typeof runtime.EventsOn !== "function") return;
    // 日志行
    runtime.EventsOn("tg:log", function (payload) {
      const line = (payload && payload.line != null) ? payload.line : (typeof payload === "string" ? payload : "");
      if (line) logTg(line, false);
    });
    // 二维码图片：dataURL 直接引用，本地路径补 file:/// 前缀；加载失败回退占位
    runtime.EventsOn("tg:qr", function (payload) {
      const path = (payload && payload.imagePath != null) ? payload.imagePath : (typeof payload === "string" ? payload : "");
      showTgQr(path);
    });
    // 手机端已确认授权：二维码失效，切换到"正在完成登录"加载态，等 opDone 收尾
    runtime.EventsOn("tg:loginConfirmed", function () {
      showLoginConfirming();
    });
    // 操作完成：更新状态条，并按 op 刷新对应数据
    runtime.EventsOn("tg:opDone", function (payload) {
      const op = payload && payload.op;
      const ok = payload && payload.ok;
      const err = payload && payload.error;
      // 登录结束：无论成败都关闭二维码弹窗——成功时用户无需手动关闭并可立即看到登录态；
      // 失败时（网络不通/超时/用户取消）弹窗已无意义，原因写入日志卡便于排查
      if (op === "login") {
        closeQrModal();
      }
      if (ok) {
        if (op === "login") {
          setTgStatus("扫码登录成功，已刷新登录状态", false);
          logTg("扫码登录成功，登录状态已更新", false);
        } else {
          setTgStatus(("完成 · " + (op || "")), false);
          logTg((op || "操作") + " 完成", false);
        }
      } else {
        setTgStatus(("失败 · " + (op || "")), false);
        logTg((op || "操作") + " 失败：" + (err || "未知错误"), true);
      }
      if (op === "login") refreshTgState();
      else if (op === "export") refreshTgRecords();
      else if (op === "forward") refreshTgRecords();
    });
  }

  // ===== 二维码显示（base64 data URL；加载失败回退占位文案）=====
  function showTgQr(src) {
    if (!src) {
      tgQrSpinner.hidden = true;
      tgQrImg.hidden = true;
      tgQrPlaceholder.hidden = false;
      tgQrHint.textContent = "二维码加载失败，请关闭后重试";
      return;
    }
    tgQrSpinner.hidden = true;
    tgQrPlaceholder.hidden = true;
    tgQrImg.hidden = false;
    tgQrHint.textContent = "请用 Telegram 手机客户端扫描二维码";
    tgQrImg.onerror = function () {
      // 加载失败时回退占位文字，不阻塞流程
      tgQrImg.hidden = true;
      tgQrPlaceholder.hidden = false;
      tgQrHint.textContent = "二维码加载失败，请关闭后重试";
    };
    tgQrImg.src = src;
  }

  // ===== 手机已确认授权：二维码已失效，切回加载态等待收尾 =====
  function showLoginConfirming() {
    tgQrImg.hidden = true;
    tgQrPlaceholder.hidden = true;
    tgQrSpinner.hidden = false;
    tgQrHint.textContent = "已扫码，正在完成登录…";
  }

  // ===== 打开 / 关闭扫码登录模态 =====
  function openQrModal() {
    tgQrImg.hidden = true;
    tgQrImg.removeAttribute("src");
    // 打开即进入加载态：转圈 + 状态提示，等 tg:qr 事件送达后再切到二维码
    tgQrSpinner.hidden = false;
    tgQrPlaceholder.hidden = true;
    tgQrHint.textContent = "正在生成二维码…";
    tgQrOverlay.hidden = false;
  }
  function closeQrModal() {
    tgQrOverlay.hidden = true;
    tgQrSpinner.hidden = true;
    tgQrImg.hidden = true;
  }
  // 用户主动关闭弹窗 = 放弃本次扫码：顺手取消后台登录任务，
  // 避免 tdl 登录进程与轮询在弹窗消失后继续空转（无任务时 TgCancel 为无副作用空操作）
  function abortQrLogin() {
    closeQrModal();
    logTg("已关闭扫码窗口，取消本次登录", false);
    try {
      const p = callTg("TgCancel");
      if (p && typeof p.catch === "function") p.catch(function () { /* 忽略取消失败 */ });
    } catch (e) {
      // 浏览器环境无绑定：忽略即可
    }
  }

  // ===== 打开 / 关闭聊天列表模态 =====
  function openChatsModal() {
    tgChatsSearch.value = "";
    tgChatsOverlay.hidden = false;
  }
  function closeChatsModal() { tgChatsOverlay.hidden = true; }

  // ===== 聊天列表：类型徽章映射 =====
  function chatTypeClass(type) {
    const t = String(type || "").toLowerCase();
    if (t.indexOf("channel") >= 0) return "t-channel";
    if (t.indexOf("group") >= 0) return "t-group";
    if (t.indexOf("user") >= 0 || t.indexOf("private") >= 0) return "t-user";
    return "t-other";
  }
  function chatTypeLabel(type) {
    const t = String(type || "").toLowerCase();
    if (t.indexOf("supergroup") >= 0) return "超级群";
    if (t.indexOf("group") >= 0) return "群组";
    if (t.indexOf("channel") >= 0) return "频道";
    if (t.indexOf("user") >= 0 || t.indexOf("private") >= 0) return "私聊";
    return type ? String(type) : "其他";
  }

  // ===== 渲染聊天列表行（按搜索词过滤，统计实时更新）=====
  function renderChatRows() {
    const keyword = (tgChatsSearch.value || "").trim().toLowerCase();
    const list = keyword
      ? tgChatList.filter((c) => {
          const hay = [c.id, c.name, c.username, c.topic, chatTypeLabel(c.type)].join(" ").toLowerCase();
          return hay.indexOf(keyword) >= 0;
        })
      : tgChatList;

    tgChatsCount.textContent = keyword
      ? "匹配 " + list.length + " / 共 " + tgChatList.length + " 个"
      : "共 " + tgChatList.length + " 个";

    if (!list.length) {
      const tip = tgChatList.length ? "没有匹配的聊天" : "没有可用聊天，请先点击「刷新聊天列表」";
      tgChatsTbody.innerHTML = '<tr><td colspan="6"><div class="empty">' + tip + '</div></td></tr>';
      return;
    }

    tgChatsTbody.innerHTML = list.map((c) => {
      const id = (c && c.id != null) ? String(c.id) : "";
      const name = (c && c.name) ? c.name : "";
      const user = (c && c.username) ? c.username : "";
      const topic = (c && c.topic) ? c.topic : "";
      const escId = escapeHtml(id);
      return '<tr>' +
        '<td><span class="chats-type ' + chatTypeClass(c && c.type) + '">' + escapeHtml(chatTypeLabel(c && c.type)) + '</span></td>' +
        '<td><span class="chats-id tg-chat-id" data-id="' + escId + '" title="点击复制 ID">' + escId + '</span></td>' +
        '<td class="chats-name" title="' + escapeHtml(name) + '">' +
          (name ? escapeHtml(name) : '<span class="chats-empty-cell">未命名</span>') + '</td>' +
        '<td>' + (user ? '<span class="chats-user">@' + escapeHtml(user) + '</span>' : '<span class="chats-empty-cell">—</span>') + '</td>' +
        '<td title="' + escapeHtml(topic) + '">' + (topic ? escapeHtml(topic) : '<span class="chats-empty-cell">—</span>') + '</td>' +
        '<td class="chats-act">' +
          '<button class="tg-chat-btn" data-act="target" data-id="' + escId + '">设为目标</button>' +
          '<button class="tg-chat-btn" data-act="src" data-id="' + escId + '">设为源</button>' +
        '</td>' +
        '</tr>';
    }).join("");
  }

  // ===== 加载 / 错误态 =====
  function renderChatsLoading() {
    tgChatList = [];
    tgChatsCount.textContent = "加载中…";
    tgChatsTbody.innerHTML = '<tr><td colspan="6">' +
      '<div class="chats-loading"><span class="qr-spinner"></span>正在拉取聊天列表…</div></td></tr>';
  }
  function renderChatsError(msg) {
    tgChatList = [];
    tgChatsCount.textContent = "共 0 个";
    tgChatsTbody.innerHTML = '<tr><td colspan="6"><div class="empty">' +
      escapeHtml("加载失败：" + msg) + '</div></td></tr>';
  }

  // ===== 填充聊天列表（数据入口）=====
  function renderChatsTable(chats) {
    tgChatList = Array.isArray(chats) ? chats : [];
    renderChatRows();
  }

  // ===== 页面切换 =====
  function switchPage(which) {
    const toTg = which === "tg";
    navProcess.classList.toggle("active", !toTg);
    navTg.classList.toggle("active", toTg);
    pageProcess.classList.toggle("active", !toTg);
    pageTg.classList.toggle("active", toTg);
    pageProcess.hidden = toTg;
    pageTg.hidden = !toTg;
    // TG 页惰性初始化：首次进入才拉取状态
    if (toTg && !tgInited) {
      tgInited = true;
      refreshTgState();
      refreshTgRecords();
      bindTgEvents(); // 事件只需订阅一次
    }
  }

  // ===== 当前选中的源聊天（是否收藏夹）=====
  function selectedSource() {
    const opt = tgSrcSelect.selectedOptions[0];
    const isSaved = !!(opt && opt.dataset.saved);
    return {
      isSavedMessages: isSaved,
      chatId: isSaved ? 0 : (opt ? opt.value : ""),
      chatName: opt ? opt.textContent : "",
    };
  }

  // ===== 卡片② 开始导出 =====
  async function doTgExport() {
    if (tgBusy) return;
    const src = selectedSource();
    const mode = segMode(tgRangeSeg);
    const lastCount = mode === "last" ? (parseInt(tgLastCount.value, 10) || 0) : 0;
    const filters = Array.from(tgFilterChips.querySelectorAll(".tg-filter.on"))
      .map((b) => b.dataset.key);
    const opts = {
      isSavedMessages: src.isSavedMessages,
      chatId: src.chatId,
      chatName: src.chatName,
      mode: mode === "last" ? "last" : "all",
      lastCount: lastCount,
      filters: filters,
    };
    setTgStatus("导出中", true);
    logTg("开始导出：" + src.chatName + " · 模式=" + opts.mode + " · 筛选=" + (filters.join(",") || "全部"));
    try {
      await callTg("TgExport", opts);
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      setTgStatus("导出失败", false);
      logTg("导出失败：" + msg, true);
    }
  }

  // ===== 卡片③ 开始转发 =====
  async function doTgForward() {
    if (tgBusy) return;
    // 记录下拉的 value 是 tgRecordList 索引，真实 JSON 路径从记录对象取
    const rec = tgRecordList[parseInt(tgRecordSelect.value, 10)];
    const exportPath = rec ? rec.path : "";
    const target = tgTargetSelect.value;
    if (!exportPath) {
      setTgStatus("请先选择导出记录", false);
      logTg("未选择转发内容（导出记录）", true);
      return;
    }
    if (!target) {
      setTgStatus("请先选择目标聊天", false);
      logTg("未选择目标聊天", true);
      return;
    }
    const opts = {
      exportPath: exportPath,
      target: target,
      mode: segMode(tgFwdModeSeg) === "clone" ? "clone" : "direct",
      dryRun: tgDryRun.checked,
      skipForwarded: tgSkipFwd.checked,
      reverseOrder: tgReverse.checked,
      batchSize: parseInt(tgBatchSize.value, 10) || 10,
      delaySeconds: parseInt(tgDelaySec.value, 10) || 1,
    };
    setTgStatus("转发中", true);
    logTg("开始转发：" + exportPath + " → " + target +
      " · 模式=" + opts.mode + " · 每批=" + opts.batchSize + " · 间隔=" + opts.delaySeconds + "s");
    try {
      await callTg("TgForward", opts);
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      setTgStatus("转发失败", false);
      logTg("转发失败：" + msg, true);
    }
  }

  // ===== 事件绑定（导航 + 各控件）=====
  navProcess.addEventListener("click", () => switchPage("process"));
  navTg.addEventListener("click", () => switchPage("tg"));

  // 卡片①
  // 已登录时按钮变"重新扫码登录"：两步确认后先清会话再扫码（对齐 walk 版重登确认语义）
  // 两步确认工具：首次点击切换为确认文案，4 秒内再次点击才真正执行
  // （危险操作免弹窗但防误触，与「清空历史」一致的交互范式）
  function armConfirm(btn, confirmText, defaultText, action) {
    if (btn.dataset.armed !== "1") {
      btn.dataset.armed = "1";
      btn.textContent = confirmText;
      setTimeout(() => {
        btn.dataset.armed = "";
        btn.textContent = defaultText;
      }, 4000);
      return;
    }
    btn.dataset.armed = "";
    btn.textContent = defaultText;
    action();
  }

  // 打开扫码弹窗并发起登录（已登录时调用前需先清会话）
  async function startQrLogin() {
    openQrModal();
    logTg("请求扫码登录…");
    try {
      await callTg("TgStartLogin");
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("发起登录失败：" + msg, true);
    }
  }

  // 「扫码登录」（未登录）/「重新登录」（已登录，先清会话）
  tgLoginBtn.addEventListener("click", () => {
    if (tgBusy) return;
    if (!tgLoggedIn) {
      startQrLogin();
      return;
    }
    armConfirm(tgLoginBtn, "确认重新登录？", "重新登录", async () => {
      logTg("清除旧登录会话后重新登录…");
      try {
        await callTg("TgClearSession");
      } catch (e) {
        const msg = (typeof e === "string") ? e : String(e);
        logTg("清除旧会话失败：" + msg, true);
      }
      // 先刷新为未登录态（按钮组同步切换），再走全新扫码流程
      refreshTgState();
      startQrLogin();
    });
  });

  tgNetBtn.addEventListener("click", async () => {
    // 直接用输入框当前地址测试：避免"填了地址但忘了保存 → 测的是旧地址"的困惑
    const proxy = tgProxyInput.value.trim();
    logTg("开始网络测试" + (proxy ? "（" + proxy + "）" : "（直连）") + "…");
    tgNetBtn.disabled = true;
    try {
      const msg = await callTg("TgValidateNetwork", proxy);
      logTg(msg || "网络测试通过", false);
      setTgStatus(msg || "网络测试通过", false);
    } catch (e) {
      const errMsg = (typeof e === "string") ? e : String(e);
      logTg("网络测试失败：" + errMsg, true);
      setTgStatus("网络测试失败", false);
    } finally {
      tgNetBtn.disabled = false;
    }
  });
  async function submitProxy() {
    const proxy = tgProxyInput.value.trim();
    logTg("保存代理：" + (proxy || "（清空）"));
    try {
      await callTg("TgSetProxy", proxy);
      logTg("代理已保存并校验通过" + (proxy ? "：" + proxy : "（直连模式）"), false);
      // 回显后端归一化后的值（如 127.0.0.1:7890 → socks5://127.0.0.1:7890）
      refreshTgState();
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("保存代理失败：" + msg, true);
    }
  }
  tgProxyBtn.addEventListener("click", submitProxy);
  tgProxyInput.addEventListener("blur", submitProxy);

  // 卡片②
  tgChatsBtn.addEventListener("click", async () => {
    // 先弹出弹窗并显示加载态（拉取需要走网络，避免点击后长时间无反馈），再填充数据
    openChatsModal();
    renderChatsLoading();
    try {
      const chats = await callTg("TgRefreshChats");
      // 绑定返回的是聊天数组本身（不是 {chats} 包装对象）
      renderChatsTable(Array.isArray(chats) ? chats : []);
      logTg("聊天列表已更新，共 " + tgChatList.length + " 个", false);
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      renderChatsError(msg);
      logTg("刷新聊天列表失败：" + msg, true);
    }
  });

  // 搜索框实时过滤
  tgChatsSearch.addEventListener("input", renderChatRows);
  tgRangeSeg.addEventListener("click", (e) => {
    const seg = e.target.closest(".seg");
    if (!seg) return;
    tgRangeSeg.querySelectorAll(".seg").forEach((s) => s.classList.toggle("active", s === seg));
    tgLastCount.hidden = seg.dataset.mode !== "last";
  });
  tgFilterChips.addEventListener("click", (e) => {
    const chip = e.target.closest(".tg-filter");
    if (!chip) return;
    chip.classList.toggle("on");
  });
  tgExportBtn.addEventListener("click", doTgExport);

  // 卡片③
  tgDelRecordBtn.addEventListener("click", async () => {
    const rec = tgRecordList[parseInt(tgRecordSelect.value, 10)];
    if (!rec || !rec.path) { logTg("没有可清空的导出记录", true); return; }
    logTg("清空所选导出：" + rec.path);
    try {
      await callTg("TgDeleteRecord", rec.path);
      refreshTgRecords();
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("清空失败：" + msg, true);
    }
  });
  // 清空历史：两步点击确认（与 walk 版确认语义一致，免新增弹窗）
  tgClearHistoryBtn.addEventListener("click", async () => {
    if (tgClearHistoryBtn.dataset.confirming !== "1") {
      tgClearHistoryBtn.dataset.confirming = "1";
      tgClearHistoryBtn.textContent = "确认清空？再点一次";
      setTimeout(() => {
        tgClearHistoryBtn.dataset.confirming = "";
        tgClearHistoryBtn.textContent = "清空历史";
      }, 4000);
      return;
    }
    tgClearHistoryBtn.dataset.confirming = "";
    tgClearHistoryBtn.textContent = "清空历史";
    const rec = tgRecordList[parseInt(tgRecordSelect.value, 10)];
    const target = tgTargetSelect.value;
    if (!rec || !rec.sourceId || !target) { logTg("清空历史需要先选择导出记录与目标聊天", true); return; }
    logTg("清空转发历史：来源 " + rec.sourceId + " → 目标 " + target);
    try {
      await callTg("TgClearHistory", rec.sourceId, target);
      logTg("转发历史已清空");
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("清空历史失败：" + msg, true);
    }
  });
  tgSetTargetBtn.addEventListener("click", () => {
    // 「设为目标」联动：把当前源选择回填到目标下拉
    const src = selectedSource();
    const val = src.isSavedMessages ? "saved" : src.chatId;
    if (val !== "" && tgTargetSelect.querySelector('option[value="' + CSS.escape(String(val)) + '"]')) {
      tgTargetSelect.value = val;
      logTg("目标已设为：" + src.chatName);
    } else {
      logTg("目标聊天列表中没有对应项", true);
    }
  });
  tgFwdModeSeg.addEventListener("click", (e) => {
    const seg = e.target.closest(".seg");
    if (!seg) return;
    tgFwdModeSeg.querySelectorAll(".seg").forEach((s) => s.classList.toggle("active", s === seg));
  });
  tgForwardBtn.addEventListener("click", doTgForward);
  tgStopBtn.addEventListener("click", async () => {
    logTg("请求停止当前任务…");
    try {
      await callTg("TgCancel");
      setTgStatus("已发送停止请求", false);
    } catch (e) {
      const msg = (typeof e === "string") ? e : String(e);
      logTg("停止失败：" + msg, true);
    }
  });

  // 聊天列表模态：行内按钮（事件委托）
  tgChatsTbody.addEventListener("click", (e) => {
    const actBtn = e.target.closest(".tg-chat-btn");
    if (actBtn) {
      const id = actBtn.dataset.id;
      if (actBtn.dataset.act === "src") {
        if (tgSrcSelect.querySelector('option[value="' + CSS.escape(String(id)) + '"]')) {
          tgSrcSelect.value = id;
          logTg("源已设为 ID：" + id);
        }
      } else if (actBtn.dataset.act === "target") {
        if (tgTargetSelect.querySelector('option[value="' + CSS.escape(String(id)) + '"]')) {
          tgTargetSelect.value = id;
          logTg("目标已设为 ID：" + id);
        }
      }
      return;
    }
    const copyId = e.target.closest(".tg-chat-id");
    if (copyId) {
      const id = copyId.dataset.id;
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(String(id)).then(
          () => logTg("已复制聊天 ID：" + id),
          () => logTg("复制失败（剪贴板不可用）", true)
        );
      } else {
        logTg("当前环境不支持剪贴板", true);
      }
    }
  });

  // 模态关闭交互
  tgQrCancel.addEventListener("click", abortQrLogin);
  tgQrOverlay.addEventListener("click", (e) => { if (e.target === tgQrOverlay) abortQrLogin(); });
  tgChatsCancel.addEventListener("click", closeChatsModal);
  tgChatsClose.addEventListener("click", closeChatsModal);
  tgChatsOverlay.addEventListener("click", (e) => { if (e.target === tgChatsOverlay) closeChatsModal(); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (!tgQrOverlay.hidden) { abortQrLogin(); return; }
      if (!tgChatsOverlay.hidden) { closeChatsModal(); return; }
      if (!tgChatsOverlay.hidden) closeChatsModal();
    }
  });
})();
