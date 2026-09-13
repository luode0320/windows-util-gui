package tgtransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ExportMode 表示导出范围模式。
type ExportMode string

const (
	ExportModeAll   ExportMode = "all"   // 导出全部消息
	ExportModeLast  ExportMode = "last"  // 导出最近 N 条
	ExportModeRange ExportMode = "range" // 按时间范围导出
)

// ExportOptions 聚合导出消息时的参数选项。
type ExportOptions struct {
	IsSavedMessages bool       // 是否导出收藏夹 (Saved Messages)
	ChatID          string     // 若非收藏夹，目标的聊天 ID 或 username
	ChatName        string     // 来源展示名
	Mode            ExportMode // 导出模式
	LastCount       int        // 最近 N 条数量
	StartTime       time.Time  // 时间范围开始时间
	EndTime         time.Time  // 时间范围结束时间
	Filters         []string   // 类型筛选: text, txt, video, novel, epub, mp4
	OutputFileName  string     // 输出文件名，如留空则自动生成
}

// ExportRecord 记录单次导出的元数据，便于转发时快速选用。
type ExportRecord struct {
	CreatedAt    string `json:"CreatedAt"`
	SourceType   string `json:"SourceType"`
	SourceName   string `json:"SourceName"`
	SourceId     string `json:"SourceId"`
	ExportMode   string `json:"ExportMode"`
	MessageCount int    `json:"MessageCount"`
	Path         string `json:"Path"`
}

// TelegramExportMessage 表示导出的 JSON 文件中的单条消息结构。
type TelegramExportMessage struct {
	ID   interface{} `json:"id"`
	Date int64       `json:"date,omitempty"`
	Text string      `json:"text,omitempty"`
	File string      `json:"file,omitempty"`
}

// TelegramExportPayload 表示导出的 JSON 文件顶层根对象。
type TelegramExportPayload struct {
	ID       interface{}             `json:"id"`
	Messages []TelegramExportMessage `json:"messages"`
}

var novelRegex = regexp.MustCompile(`(?i)小说|novel`)

// MatchMessageFilter 判断一条消息是否满足所选的类型过滤条件。
//
// [参数] msg: 消息对象；filters: 筛选条件列表
// [返回] 满足任一条件返回 true；filters 为空时默认返回 true
// 最近修改时间: 2026-09-13
func MatchMessageFilter(msg TelegramExportMessage, filters []string) bool {
	if len(filters) == 0 {
		return true
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(msg.File), "."))
	for _, f := range filters {
		switch f {
		case "text":
			if strings.TrimSpace(msg.Text) != "" && msg.File == "" {
				return true
			}
		case "txt":
			if ext == "txt" {
				return true
			}
		case "video":
			switch ext {
			case "mp4", "mkv", "mov", "avi", "wmv", "flv", "webm", "m4v":
				return true
			}
		case "novel":
			if novelRegex.MatchString(msg.File) || novelRegex.MatchString(msg.Text) {
				return true
			}
		case "epub":
			if ext == "epub" {
				return true
			}
		case "mp4":
			if ext == "mp4" {
				return true
			}
		}
	}
	return false
}

// FilterExportJson 读取导出的 JSON 文件，按指定类型过滤消息并写回。
//
// [参数] filePath: JSON 文件路径；filters: 过滤标签列表
// [返回] 过滤后剩余的消息条数；读取或写回失败返回 error
// 最近修改时间: 2026-09-13
func FilterExportJson(filePath string, filters []string) (int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}

	var payload TelegramExportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, fmt.Errorf("解析导出 JSON 失败: %w", err)
	}

	if len(filters) == 0 {
		return len(payload.Messages), nil
	}

	filtered := make([]TelegramExportMessage, 0, len(payload.Messages))
	for _, m := range payload.Messages {
		if MatchMessageFilter(m, filters) {
			filtered = append(filtered, m)
		}
	}
	payload.Messages = filtered

	outBytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("序列化过滤后 JSON 失败: %w", err)
	}

	if err := os.WriteFile(filePath, outBytes, 0644); err != nil {
		return 0, fmt.Errorf("写回过滤后 JSON 失败: %w", err)
	}
	return len(filtered), nil
}

// ExportMessages 根据选项执行 tdl 消息导出，并进行过滤和持久化登记。
//
// [参数] ctx: 上下文；cfg: 配置；opt: 导出参数；logCb: 实时日志回调
// [返回] 导出记录对象；失败返回 error
// 最近修改时间: 2026-09-13
func ExportMessages(ctx context.Context, cfg *Config, opt ExportOptions, logCb func(string)) (*ExportRecord, error) {
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, err
	}

	// 1. 计算输出文件名与路径
	fileName := strings.TrimSpace(opt.OutputFileName)
	if fileName == "" {
		timestamp := time.Now().Format("20060102-150405")
		if opt.IsSavedMessages {
			fileName = fmt.Sprintf("saved-messages-%s.json", timestamp)
		} else {
			safeChatID := strings.ReplaceAll(opt.ChatID, "-", "m")
			fileName = fmt.Sprintf("chat-%s-%s.json", safeChatID, timestamp)
		}
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".json") {
		fileName += ".json"
	}
	outputPath := filepath.Join(cfg.DataDir, fileName)

	// 2. 构造 tdl chat export 命令行参数
	subArgs := []string{"chat", "export"}
	if !opt.IsSavedMessages {
		subArgs = append(subArgs, "--chat", opt.ChatID)
	}

	modeDesc := "全部"
	switch opt.Mode {
	case ExportModeLast:
		count := opt.LastCount
		if count <= 0 {
			count = 100
		}
		subArgs = append(subArgs, "--type", "last", "--input", fmt.Sprintf("%d", count))
		modeDesc = fmt.Sprintf("最近 %d 条", count)
	case ExportModeRange:
		subArgs = append(subArgs,
			"--type", "time",
			"--input", fmt.Sprintf("%d", opt.StartTime.Unix()),
			"--until", fmt.Sprintf("%d", opt.EndTime.Unix()),
		)
		modeDesc = fmt.Sprintf("%s 至 %s", opt.StartTime.Format("2006-01-02 15:04"), opt.EndTime.Format("2006-01-02 15:04"))
	default:
		subArgs = append(subArgs, "--all")
	}

	subArgs = append(subArgs, "--with-content", "--output", outputPath)
	args := BuildTdlArgs(cfg, subArgs...)

	if logCb != nil {
		logCb(fmt.Sprintf("正在开始导出消息至: %s", outputPath))
		logCb(fmt.Sprintf("执行命令: tdl %s", strings.Join(subArgs, " ")))
	}

	// 3. 执行导出
	if err := RunTdlStreaming(ctx, cfg, args, logCb); err != nil {
		return nil, fmt.Errorf("导出消息失败: %w", err)
	}

	// 4. 应用类型过滤 (如有)
	msgCount, err := FilterExportJson(outputPath, opt.Filters)
	if err != nil {
		if logCb != nil {
			logCb(fmt.Sprintf("应用类型过滤时出错: %v，将保留原始导出内容", err))
		}
	} else if len(opt.Filters) > 0 {
		if logCb != nil {
			logCb(fmt.Sprintf("已按类型筛选消息，筛选后保留 %d 条有效记录。", msgCount))
		}
	}

	// 5. 登记导出记录
	sourceType := "普通群组/频道"
	sourceName := opt.ChatName
	sourceID := opt.ChatID
	if opt.IsSavedMessages {
		sourceType = "收藏夹"
		sourceName = "收藏夹 (Saved Messages)"
		sourceID = "Saved Messages"
	}
	if sourceName == "" {
		sourceName = sourceID
	}

	record := &ExportRecord{
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
		SourceType:   sourceType,
		SourceName:   sourceName,
		SourceId:     sourceID,
		ExportMode:   modeDesc,
		MessageCount: msgCount,
		Path:         outputPath,
	}

	if err := SaveExportRecord(cfg, record); err != nil && logCb != nil {
		logCb(fmt.Sprintf("警告: 保存导出历史记录失败: %v", err))
	}

	if logCb != nil {
		logCb(fmt.Sprintf("导出任务圆满完成！文件位置: %s，有效条数: %d", outputPath, msgCount))
	}
	return record, nil
}

// LoadExportRecords 从 export-records.json 读取历史记录。
//
// [参数] cfg: 运行配置
// [返回] 历史记录切片
// 最近修改时间: 2026-09-13
func LoadExportRecords(cfg *Config) ([]ExportRecord, error) {
	recordFile := cfg.ExportRecordsPath()
	data, err := os.ReadFile(recordFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records []ExportRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// SaveExportRecord 追加或更新单条导出记录到 export-records.json。
//
// [参数] cfg: 运行配置；record: 待持久化的记录
// [返回] 保存失败返回 error
// 最近修改时间: 2026-09-13
func SaveExportRecord(cfg *Config, record *ExportRecord) error {
	records, _ := LoadExportRecords(cfg)

	// 1. 同一文件路径只保留最新一条记录
	newRecords := make([]ExportRecord, 0, len(records)+1)
	newRecords = append(newRecords, *record)
	for _, r := range records {
		if r.Path != record.Path {
			newRecords = append(newRecords, r)
		}
	}
	// 最多保留 100 条
	if len(newRecords) > 100 {
		newRecords = newRecords[:100]
	}

	data, err := json.MarshalIndent(newRecords, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ExportRecordsPath(), data, 0644)
}

// GetAllAvailableExportRecords 合并持久化记录与数据目录下已存在的其他 JSON 文件。
//
// [参数] cfg: 运行配置
// [返回] 按修改时间倒序排列的所有可用导出记录
// 最近修改时间: 2026-09-13
func GetAllAvailableExportRecords(cfg *Config) []ExportRecord {
	registered, _ := LoadExportRecords(cfg)
	seen := make(map[string]bool)
	var all []ExportRecord

	for _, r := range registered {
		if _, err := os.Stat(r.Path); err == nil {
			seen[filepath.Clean(r.Path)] = true
			all = append(all, r)
		}
	}

	// 扫描 data 目录下的其他 json
	entries, err := os.ReadDir(cfg.DataDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				continue
			}
			if e.Name() == "forward-history.json" || e.Name() == "export-records.json" {
				continue
			}

			fullPath := filepath.Join(cfg.DataDir, e.Name())
			if seen[filepath.Clean(fullPath)] {
				continue
			}

			// 尝试解析 message 数量
			count := 0
			sourceName := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if data, err := os.ReadFile(fullPath); err == nil {
				var p TelegramExportPayload
				if json.Unmarshal(data, &p) == nil {
					count = len(p.Messages)
				}
			}

			fi, _ := e.Info()
			modTime := time.Now()
			if fi != nil {
				modTime = fi.ModTime()
			}

			all = append(all, ExportRecord{
				CreatedAt:    modTime.Format("2006-01-02 15:04:05"),
				SourceType:   "已有文件",
				SourceName:   sourceName,
				SourceId:     "-",
				ExportMode:   "历史文件",
				MessageCount: count,
				Path:         fullPath,
			})
			seen[filepath.Clean(fullPath)] = true
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
	return all
}

// DeleteExportRecord 删除指定的导出 JSON 文件及其登记记录。
//
// [参数] cfg: 运行配置；filePath: 文件绝对路径
// [返回] 删除失败返回 error
// 最近修改时间: 2026-09-13
func DeleteExportRecord(cfg *Config, filePath string) error {
	cleanPath := filepath.Clean(filePath)
	_ = os.Remove(cleanPath)

	records, err := LoadExportRecords(cfg)
	if err != nil {
		return nil
	}

	var kept []ExportRecord
	for _, r := range records {
		if filepath.Clean(r.Path) != cleanPath {
			kept = append(kept, r)
		}
	}

	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ExportRecordsPath(), data, 0644)
}
