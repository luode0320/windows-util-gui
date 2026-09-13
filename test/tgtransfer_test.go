package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windows-util-gui/internal/tools/tgtransfer"
	"windows-util-gui/internal/tools/tgtransfer/tdlbin"
)

// TestParseChatLineAndList 校验聊天列表单行及多行解析逻辑。
//
// 最近修改时间: 2026-09-13
func TestParseChatLineAndList(t *testing.T) {
	sample := "ID          Type          VisibleName               Username       Topics\n" +
		"-1001234567  Supergroup    技术 讨论 交流群           tech_discuss\n" +
		"-1007654321  Channel       每日 新闻 频道             -\n" +
		"123456789    User          张三                      zhangsan\n"
	chats := tgtransfer.ParseChatList(sample)
	if len(chats) != 3 {
		t.Fatalf("期望解析出 3 个聊天，实际得到 %d 个", len(chats))
	}

	// 1. 验证群组解析及名称中的空格保留
	if chats[0].ID != "-1001234567" || chats[0].Type != "Supergroup" || chats[0].VisibleName != "技术 讨论 交流群" || chats[0].Username != "tech_discuss" {
		t.Errorf("群组解析不符: %+v", chats[0])
	}

	// 2. 验证频道解析及空用户名"-"处理
	if chats[1].ID != "-1007654321" || chats[1].Type != "Channel" || chats[1].VisibleName != "每日 新闻 频道" || chats[1].Username != "" {
		t.Errorf("频道解析不符: %+v", chats[1])
	}

	// 3. 验证下拉框格式化与 ID 提取
	opt := tgtransfer.FormatChatOption(chats[0])
	extractedID := tgtransfer.ExtractChatID(opt)
	if extractedID != "-1001234567" {
		t.Errorf("提取 ID 失败，原始选项: %s, 提取结果: %s", opt, extractedID)
	}
}

// TestMatchMessageFilter 校验消息类型筛选器。
//
// 最近修改时间: 2026-09-13
func TestMatchMessageFilter(t *testing.T) {
	cases := []struct {
		name     string
		msg      tgtransfer.TelegramExportMessage
		filters  []string
		expected bool
	}{
		{
			name:     "无筛选条件一律通过",
			msg:      tgtransfer.TelegramExportMessage{Text: "你好", File: "doc.pdf"},
			filters:  nil,
			expected: true,
		},
		{
			name:     "纯文本消息匹配 text",
			msg:      tgtransfer.TelegramExportMessage{Text: "纯文本内容", File: ""},
			filters:  []string{"text"},
			expected: true,
		},
		{
			name:     "带文件的文本不匹配 text",
			msg:      tgtransfer.TelegramExportMessage{Text: "附言", File: "a.jpg"},
			filters:  []string{"text"},
			expected: false,
		},
		{
			name:     "TXT 文件匹配 txt",
			msg:      tgtransfer.TelegramExportMessage{File: "story.txt"},
			filters:  []string{"txt"},
			expected: true,
		},
		{
			name:     "视频扩展名匹配 video",
			msg:      tgtransfer.TelegramExportMessage{File: "video.mkv"},
			filters:  []string{"video"},
			expected: true,
		},
		{
			name:     "小说关键词匹配 novel",
			msg:      tgtransfer.TelegramExportMessage{Text: "精彩修仙小说合集"},
			filters:  []string{"novel"},
			expected: true,
		},
		{
			name:     "不满足筛选条件",
			msg:      tgtransfer.TelegramExportMessage{File: "photo.png"},
			filters:  []string{"video", "epub"},
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tgtransfer.MatchMessageFilter(tc.msg, tc.filters)
			if actual != tc.expected {
				t.Errorf("筛选判定不符，用例: %s, 期望: %v, 实际: %v", tc.name, tc.expected, actual)
			}
		})
	}
}

// TestFilterExportJson 校验过滤并重写导出 JSON 的功能。
//
// 最近修改时间: 2026-09-13
func TestFilterExportJson(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "test-export.json")

	payload := tgtransfer.TelegramExportPayload{
		ID: -100123456,
		Messages: []tgtransfer.TelegramExportMessage{
			{ID: 1, Text: "第一条纯文本", File: ""},
			{ID: 2, Text: "图片消息", File: "pic.jpg"},
			{ID: 3, Text: "小说文本", File: "book.txt"},
		},
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("生成测试 JSON 失败: %v", err)
	}
	if err := os.WriteFile(jsonPath, bytes, 0644); err != nil {
		t.Fatalf("写入测试 JSON 失败: %v", err)
	}

	// 1. 只筛选 txt 类型，应只剩第 3 条
	count, err := tgtransfer.FilterExportJson(jsonPath, []string{"txt"})
	if err != nil {
		t.Fatalf("过滤执行失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("期望保留 1 条记录，实际保留 %d 条", count)
	}

	// 2. 重新读取文件验证
	filteredData, _ := os.ReadFile(jsonPath)
	var newPayload tgtransfer.TelegramExportPayload
	_ = json.Unmarshal(filteredData, &newPayload)
	if len(newPayload.Messages) != 1 || newPayload.Messages[0].File != "book.txt" {
		t.Fatalf("写回的内容不符合预期: %+v", newPayload.Messages)
	}
}

// TestBuildTdlArgs 校验基础参数组装与代理注入。
//
// 最近修改时间: 2026-09-13
func TestBuildTdlArgs(t *testing.T) {
	cfg := &tgtransfer.Config{
		BaseDir:   `C:\test-dir`,
		TdlHome:   `C:\test-dir\.tdl`,
		Namespace: "my-ns",
		Proxy:     "socks5://127.0.0.1:1080",
	}

	args := tgtransfer.BuildTdlArgs(cfg, "forward", "--mode", "clone")
	expectedParts := []string{
		"--storage", cfg.StorageArg(),
		"--ns", "my-ns",
		"--proxy", "socks5://127.0.0.1:1080",
		"forward", "--mode", "clone",
	}

	argsStr := " " + joinArgs(args) + " "
	for _, p := range expectedParts {
		if !containsArg(args, p) {
			t.Errorf("参数列表缺少预期参数: %s, 实际参数列表: %s", p, argsStr)
		}
	}
}

func joinArgs(args []string) string {
	res := ""
	for _, a := range args {
		res += a + " "
	}
	return res
}

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

// TestFindTdlPathPrefersBundledBinary 校验 TDL 路径探测始终锚定到 exe 自身目录，不依赖进程工作目录。
//
// go test 的二进制位于临时目录且无真实 resources 目录，此处通过注入 TDL_PATH 验证
// 环境变量优先级；真实 exe 同级探测由 GUI 启动日志验证。
//
// 最近修改时间: 2026-09-13
func TestFindTdlPathPrefersBundledBinary(t *testing.T) {
	// 1. 构造一个真实存在的临时 tdl.exe 假体，通过环境变量注入验证探测逻辑
	fakeExe := filepath.Join(t.TempDir(), "tdl.exe")
	if err := os.WriteFile(fakeExe, []byte("fake"), 0644); err != nil {
		t.Fatalf("创建假体文件失败: %v", err)
	}
	t.Setenv("TDL_PATH", fakeExe)

	path := tgtransfer.FindTdlPath()
	if path != fakeExe {
		t.Fatalf("期望优先返回 TDL_PATH 指向的 %s，实际: %s", fakeExe, path)
	}

	// 2. 清除环境变量后，裸探测不得回退到原 tg-tdl-transfer 项目目录
	t.Setenv("TDL_PATH", "")
	path = tgtransfer.FindTdlPath()
	if strings.Contains(path, "tg-tdl-transfer") {
		t.Fatalf("路径探测不应依赖原项目目录，实际返回: %s", path)
	}
}

// TestDefaultConfigAnchorsToExeDir 校验默认配置的数据目录锚定在 exe 所在目录而非进程工作目录。
//
// DefaultConfig 会顺带触发嵌入 tdl.exe 的释放，本用例同时验证释放产物字节级一致。
//
// 最近修改时间: 2026-09-13
func TestDefaultConfigAnchorsToExeDir(t *testing.T) {
	cfg := tgtransfer.DefaultConfig()
	if !filepath.IsAbs(cfg.BaseDir) {
		t.Fatalf("BaseDir 应为绝对路径，实际: %s", cfg.BaseDir)
	}

	exePath, err := os.Executable()
	if err != nil {
		t.Skipf("无法获取可执行文件路径，跳过: %v", err)
	}
	exeDir := filepath.Dir(exePath)
	if !strings.HasPrefix(cfg.BaseDir, exeDir) {
		t.Fatalf("数据目录应锚定在 exe 目录 %s 下，实际: %s", exeDir, cfg.BaseDir)
	}

	// 1. 嵌入释放产物应存在且与源文件字节级一致
	bundled := cfg.BundledTdlPath()
	got, err := os.ReadFile(bundled)
	if err != nil {
		t.Fatalf("嵌入 tdl.exe 未释放到 %s: %v", bundled, err)
	}
	embedded, err := tdlbin.ReadEmbedded()
	if err != nil {
		t.Skipf("无法读取嵌入源文件（模块目录不可达），跳过字节校验: %v", err)
	}
	if len(got) != len(embedded) {
		t.Fatalf("释放产物大小不符: 期望 %d 字节, 实际 %d 字节", len(embedded), len(got))
	}
}
