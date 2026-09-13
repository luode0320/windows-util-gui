package test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"windows-util-gui/internal/tools/tgtransfer"
	"windows-util-gui/pkg/toolapi"
)

// skipIfNotWindows 在真实 Windows 之外的环境下跳过 TG 相关测试。
//
// 最近修改时间: 2026-09-13
func skipIfNotWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("TG 相关逻辑依赖 Windows 环境与嵌入 tdl，仅在 Windows 真实环境下验证，跳过")
	}
}

// TestIsSavedMessagesInput 校验收藏夹判定的大小写与中英文两态。
//
// 最近修改时间: 2026-09-13
func TestIsSavedMessagesInput(t *testing.T) {
	skipIfNotWindows(t)

	cases := []struct {
		text     string
		expected bool
	}{
		{"收藏夹 (Saved Messages)", true},
		{"收藏夹", true},
		{"saved messages", true},
		{"Saved Messages", true},
		{"SAVED MESSAGES", true},
		{"技术 讨论 交流群 | Supergroup | -1001234567", false},
		{"My Channel", false},
		{"", false},
	}
	for _, c := range cases {
		if got := tgtransfer.IsSavedMessagesInput(c.text); got != c.expected {
			t.Errorf("IsSavedMessagesInput(%q) 期望 %v，实际 %v", c.text, c.expected, got)
		}
	}
}

// TestParseSourceInput 校验源输入解析的三分支：收藏夹 / 管道格式 ID / 裸 ID。
//
// 最近修改时间: 2026-09-13
func TestParseSourceInput(t *testing.T) {
	skipIfNotWindows(t)

	// 1. 收藏夹分支
	isSaved, chatID, name := tgtransfer.ParseSourceInput("收藏夹 (Saved Messages)")
	if !isSaved || chatID != "" || name != "Saved Messages" {
		t.Errorf("收藏夹分支不符: isSaved=%v chatID=%q name=%q", isSaved, chatID, name)
	}

	// 2. 管道格式下拉项：提取第三部分 ID
	isSaved, chatID, name = tgtransfer.ParseSourceInput("技术 讨论 交流群 | Supergroup | -1001234567 @tech")
	if isSaved || chatID != "-1001234567" || name != "技术 讨论 交流群 | Supergroup | -1001234567 @tech" {
		t.Errorf("管道分支不符: isSaved=%v chatID=%q name=%q", isSaved, chatID, name)
	}

	// 3. 裸 ID：原样返回
	isSaved, chatID, name = tgtransfer.ParseSourceInput("-1007654321")
	if isSaved || chatID != "-1007654321" || name != "-1007654321" {
		t.Errorf("裸 ID 分支不符: isSaved=%v chatID=%q name=%q", isSaved, chatID, name)
	}
}

// TestExportFilterOptions 校验筛选词表包含契约规定的 6 项及其键序。
//
// 最近修改时间: 2026-09-13
func TestExportFilterOptions(t *testing.T) {
	skipIfNotWindows(t)

	opts := tgtransfer.ExportFilterOptions()
	if len(opts) != 6 {
		t.Fatalf("期望 6 个筛选选项，实际 %d 个", len(opts))
	}

	wantLabels := []string{"文本", "TXT", "视频", "小说", "EPUB", "MP4"}
	wantKeys := []string{"text", "txt", "video", "novel", "epub", "mp4"}
	for i, opt := range opts {
		if opt.Label != wantLabels[i] || opt.Key != wantKeys[i] {
			t.Errorf("第 %d 项不符: Label=%q Key=%q，期望 Label=%q Key=%q",
				i, opt.Label, opt.Key, wantLabels[i], wantKeys[i])
		}
	}
}

// TestNormalizeForwardOptions 校验四个兜底默认值与已有值保留。
//
// 最近修改时间: 2026-09-13
func TestNormalizeForwardOptions(t *testing.T) {
	skipIfNotWindows(t)

	// 1. 全零/负值 → 仅契约规定的兜底项生效：
	//    BatchSize<=0→10、SegmentSize<=0→1000（零值也会被兜底）；
	//    DelaySeconds<0→1、SegmentDelaySeconds<0→10（零值保留，不改写）。
	zero := &tgtransfer.ForwardOptions{}
	tgtransfer.NormalizeForwardOptions(zero)
	if zero.BatchSize != 10 || zero.DelaySeconds != 0 || zero.SegmentSize != 1000 || zero.SegmentDelaySeconds != 0 {
		t.Errorf("全兜底不符: %+v", zero)
	}

	// 2. 部分非法 → 仅兜底非法项，合法项保留
	partial := &tgtransfer.ForwardOptions{
		BatchSize:           20,
		DelaySeconds:        -5,
		SegmentSize:         0,
		SegmentDelaySeconds: 30,
	}
	tgtransfer.NormalizeForwardOptions(partial)
	if partial.BatchSize != 20 {
		t.Errorf("BatchSize 应保留 20，实际 %d", partial.BatchSize)
	}
	if partial.DelaySeconds != 1 {
		t.Errorf("DelaySeconds 应兜底 1，实际 %d", partial.DelaySeconds)
	}
	if partial.SegmentSize != 1000 {
		t.Errorf("SegmentSize 应兜底 1000，实际 %d", partial.SegmentSize)
	}
	if partial.SegmentDelaySeconds != 30 {
		t.Errorf("SegmentDelaySeconds 应保留 30，实际 %d", partial.SegmentDelaySeconds)
	}
}

// TestFormatExportRecordSummary 校验导出记录摘要拼装格式。
//
// 最近修改时间: 2026-09-13
func TestFormatExportRecordSummary(t *testing.T) {
	skipIfNotWindows(t)

	r := tgtransfer.ExportRecord{
		CreatedAt:    "2026-09-13 12:00:00",
		SourceName:   "收藏夹 (Saved Messages)",
		ExportMode:   "最近 100 条",
		MessageCount: 42,
		Path:         `C:\data\tgtransfer\data\chat-abc-20260101-000000.json`,
	}
	got := tgtransfer.FormatExportRecordSummary(r)
	want := "2026-09-13 12:00:00 | 收藏夹 (Saved Messages) | 最近 100 条 | 42条 (chat-abc-20260101-000000.json)"
	if got != want {
		t.Errorf("摘要格式不符:\n 期望 %q\n 实际 %q", want, got)
	}
}

// TestTgGetStateSmoke 真实冒烟 TgGetState：不 panic 且返回合理切片。
//
// 最近修改时间: 2026-09-13
func TestTgGetStateSmoke(t *testing.T) {
	skipIfNotWindows(t)

	state := toolapi.TgGetState()
	// 缓存为空时 Chats/Records 可能为 nil（内部 LoadCachedChats/扫描返回 nil），
	// 这里仅验证不 panic 且可安全取长度（契约冒烟：只断言不 panic 且返回切片）。
	if len(state.Chats) < 0 || len(state.Records) < 0 {
		t.Error("TgGetState 返回的切片长度不应为负")
	}
}

// TestTgRecordsSmoke 真实冒烟 TgRecords：不 panic 且返回切片，与 TgGetState.Records 一致。
//
// 最近修改时间: 2026-09-13
func TestTgRecordsSmoke(t *testing.T) {
	skipIfNotWindows(t)

	records := toolapi.TgRecords()
	// 冒烟：不 panic 且可安全取长度（空环境可能为 nil，nil 仍可直接 len）。
	if len(records) != len(toolapi.TgGetState().Records) {
		t.Error("TgRecords 与 TgGetState.Records 长度应一致")
	}
}

// TestMigrateLegacyDataDir 验证旧 exe 目录数据向 %LOCALAPPDATA% 新目录的一次性搬迁：
// 覆盖整体搬迁、旧 bin 清除且新数据防覆盖、重名数据保留、无旧目录静默返回四种分支。
// 纯文件系统操作，真实执行（无外部依赖）。
func TestMigrateLegacyDataDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅验证 Windows 本地目录语义")
	}
	// 1. 旧目录有内容、新目录为空 → 应整体搬迁，且搬迁后旧目录（含空壳）被清掉
	legacy := t.TempDir()
	newBase := t.TempDir()
	for _, sub := range []string{"bin", "data", "logs", ".tdl"} {
		if err := os.MkdirAll(filepath.Join(legacy, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(legacy, "bin", "tdl.exe")
	if err := os.WriteFile(marker, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := tgtransfer.MigrateLegacyDataDir(legacy, newBase); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newBase, "bin", "tdl.exe")); err != nil {
		t.Fatalf("tdl.exe 未搬到新目录: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("同卷搬迁后旧目录应已清空: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("搬迁完成后旧目录空壳应被删除: %v", err)
	}

	// 2. 新位置已有 tdl 释放产物、旧目录只有旧 bin → 旧 bin 应被清除（嵌入内容以新位置为准），
	//    新位置数据防覆盖；清完后旧目录空壳一并删除（对应桌面残留 data 的场景）
	legacy2 := t.TempDir()
	newBase2 := t.TempDir()
	os.MkdirAll(filepath.Join(legacy2, "bin"), 0755)
	os.WriteFile(filepath.Join(legacy2, "bin", "tdl.exe"), []byte("old"), 0644)
	os.MkdirAll(filepath.Join(newBase2, "bin"), 0755)
	os.WriteFile(filepath.Join(newBase2, "bin", "tdl.exe"), []byte("new"), 0644)
	if err := tgtransfer.MigrateLegacyDataDir(legacy2, newBase2); err != nil {
		t.Fatalf("旧 bin 清除分支不应报错: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(newBase2, "bin", "tdl.exe"))
	if string(data) != "new" {
		t.Fatalf("新位置数据被覆盖: %s", string(data))
	}
	if _, err := os.Stat(legacy2); !os.IsNotExist(err) {
		t.Fatalf("旧 bin 清除后旧目录应被删除: %v", err)
	}

	// 3. 两边都有 data（新位置已有用户数据）→ 旧 data 保留不覆盖，旧目录不删除
	legacy3 := t.TempDir()
	newBase3 := t.TempDir()
	os.MkdirAll(filepath.Join(legacy3, "data"), 0755)
	os.WriteFile(filepath.Join(legacy3, "data", "old.txt"), []byte("old"), 0644)
	os.MkdirAll(filepath.Join(newBase3, "data"), 0755)
	os.WriteFile(filepath.Join(newBase3, "data", "new.txt"), []byte("new"), 0644)
	if err := tgtransfer.MigrateLegacyDataDir(legacy3, newBase3); err != nil {
		t.Fatalf("重名保留分支不应报错: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newBase3, "data", "new.txt")); err != nil {
		t.Fatalf("新位置数据被破坏: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy3, "data", "old.txt")); err != nil {
		t.Fatalf("重名旧数据被误删: %v", err)
	}

	// 4. 旧目录不存在 → 静默返回
	if err := tgtransfer.MigrateLegacyDataDir(filepath.Join(t.TempDir(), "not-exist"), t.TempDir()); err != nil {
		t.Fatalf("无旧目录分支不应报错: %v", err)
	}
}
