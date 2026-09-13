package tgtransfer

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IsSavedMessagesInput 判定来源选择文本是否指向收藏夹 (Saved Messages)。
//
// 大小写不敏感地检测文本是否包含"收藏夹"或"Saved Messages"关键词，
// 用于区分导出/转发任务是针对收藏夹还是普通聊天。
//
// [参数] text: 源选择框的文本内容
// [返回] 命中收藏夹关键词返回 true
// 最近修改时间: 2026-09-13
func IsSavedMessagesInput(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "收藏夹") || strings.Contains(lower, "saved messages")
}

// ParseSourceInput 解析源选择文本，分离出收藏夹标记、聊天 ID 与展示名。
//
// 命中收藏夹时返回 isSaved=true、chatID 为空、displayName 固定为 "Saved Messages"；
// 否则用 ExtractChatID 从文本中提取聊天 ID，displayName 保留原始输入文本。
//
// [参数] text: 源选择框的文本内容
// [返回] isSaved: 是否为收藏夹；chatID: 提取出的聊天 ID（收藏夹时为空）；displayName: 展示名
// 最近修改时间: 2026-09-13
func ParseSourceInput(text string) (isSaved bool, chatID string, displayName string) {
	if IsSavedMessagesInput(text) {
		return true, "", "Saved Messages"
	}
	return false, ExtractChatID(text), text
}

// ExportFilterOption 是导出类型筛选下拉框的单个选项，Label 为界面展示文案，Key 为 tdl 筛选键。
type ExportFilterOption struct {
	Label string
	Key   string
}

// ExportFilterOptions 返回导出类型筛选的固定词表。
//
// 词表顺序对齐原 UI 页 exportTypeOptions：文本/text、TXT/txt、视频/video、小说/novel、EPUB/epub、MP4/mp4。
//
// [返回] 筛选选项切片
// 最近修改时间: 2026-09-13
func ExportFilterOptions() []ExportFilterOption {
	return []ExportFilterOption{
		{Label: "文本", Key: "text"},
		{Label: "TXT", Key: "txt"},
		{Label: "视频", Key: "video"},
		{Label: "小说", Key: "novel"},
		{Label: "EPUB", Key: "epub"},
		{Label: "MP4", Key: "mp4"},
	}
}

// NormalizeForwardOptions 把转发参数中的可选字段补齐为安全的兜底默认值。
//
// BatchSize<=0→10、DelaySeconds<0→1、SegmentSize<=0→1000、SegmentDelaySeconds<0→10，
// 对齐原 UI 页 862-868/895-896 的兜底逻辑，使前端未填或误填时仍有合理执行参数。
//
// [参数] opt: 待归一化的转发选项指针（原地修改）
// [返回] 无
// 最近修改时间: 2026-09-13
func NormalizeForwardOptions(opt *ForwardOptions) {
	if opt == nil {
		return
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = 10
	}
	if opt.DelaySeconds < 0 {
		opt.DelaySeconds = 1
	}
	if opt.SegmentSize <= 0 {
		opt.SegmentSize = 1000
	}
	if opt.SegmentDelaySeconds < 0 {
		opt.SegmentDelaySeconds = 10
	}
}

// FormatExportRecordSummary 把一条导出记录拼装为界面展示用的单行摘要。
//
// 形如"时间 | 来源名 | 模式 | N 条 (文件名)"，对齐原 UI 页 467-468 行拼装格式。
//
// [参数] r: 导出记录
// [返回] 摘要文本
// 最近修改时间: 2026-09-13
func FormatExportRecordSummary(r ExportRecord) string {
	return fmt.Sprintf("%s | %s | %s | %d条 (%s)",
		r.CreatedAt, r.SourceName, r.ExportMode, r.MessageCount, filepath.Base(r.Path))
}
