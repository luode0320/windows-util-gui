package tgtransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ForwardOptions 聚合转发执行所需的完整参数。
type ForwardOptions struct {
	ExportPath          string        // 待转发的导出 JSON 路径
	Target              string        // 目标群组/频道 ID 或 username
	Mode                string        // direct (保留原始来源) 或 clone (复制内容)
	DryRun              bool          // 是否为试转发 (不实际发送消息)
	Silent              bool          // 是否静默发送
	Total               int           // 本次最多转发条数 (<=0 表示全部可用消息)
	BatchSize           int           // 每批消息条数 (默认 10)
	DelaySeconds        int           // 每批之间的间隔秒数 (默认 1)
	SkipHistory         bool          // 是否跳过已经转发到该目标的历史消息
	Reverse             bool          // 是否反转消息顺序
	SegmentSize         int           // 分段大小 (默认 1000)
	SegmentDelaySeconds int           // 分段间休息秒数 (默认 10)
	BatchTimeout        time.Duration // 单批执行最长等待时间 (默认 180s)
}

// LoadForwardHistory 读取 forward-history.json 中的全局历史。
//
// [参数] cfg: 运行配置
// [返回] 键为 "源ID|目标"，值为已转发消息 ID 切片的映射表
// 最近修改时间: 2026-09-13
func LoadForwardHistory(cfg *Config) (map[string][]string, error) {
	historyFile := cfg.ForwardHistoryPath()
	data, err := os.ReadFile(historyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string][]string), nil
		}
		return nil, err
	}

	history := make(map[string][]string)
	if err := json.Unmarshal(data, &history); err != nil {
		return make(map[string][]string), nil
	}
	return history, nil
}

// SaveForwardHistory 持久化已转发记录到 forward-history.json。
//
// [参数] cfg: 运行配置；history: 历史映射
// [返回] 保存失败返回 error
// 最近修改时间: 2026-09-13
func SaveForwardHistory(cfg *Config, history map[string][]string) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ForwardHistoryPath(), data, 0644)
}

// ClearForwardHistory 清除指定源到指定目标的转发历史。
//
// [参数] cfg: 运行配置；sourceID: 来源 ID；target: 目标聊天
// [返回] 清除失败返回 error
// 最近修改时间: 2026-09-13
func ClearForwardHistory(cfg *Config, sourceID, target string) error {
	history, err := LoadForwardHistory(cfg)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s|%s", sourceID, target)
	delete(history, key)
	return SaveForwardHistory(cfg, history)
}

// StartForward 执行分批转发任务，支持断点续传、超时重试与优雅终止。
//
// [参数] ctx: 上下文；cfg: 配置；opt: 转发选项；logCb: 实时日志回调
// [返回] 执行完毕返回 nil，被取消或出错返回 error
// 最近修改时间: 2026-09-13
func StartForward(ctx context.Context, cfg *Config, opt ForwardOptions, logCb func(string)) error {
	if err := cfg.EnsureDirectories(); err != nil {
		return err
	}

	// 1. 读取并校验导出文件
	data, err := os.ReadFile(opt.ExportPath)
	if err != nil {
		return fmt.Errorf("读取导出 JSON 失败: %w", err)
	}

	var payload TelegramExportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析导出 JSON 失败: %w", err)
	}

	if len(payload.Messages) == 0 {
		return fmt.Errorf("导出文件包含 0 条消息，无法转发")
	}

	sourceKey := fmt.Sprintf("%v", payload.ID)
	historyKey := fmt.Sprintf("%s|%s", sourceKey, opt.Target)

	// 2. 读取并处理历史记录过滤
	history, _ := LoadForwardHistory(cfg)
	alreadyForwardedSet := make(map[string]bool)
	if opt.SkipHistory {
		if list, ok := history[historyKey]; ok {
			for _, idStr := range list {
				alreadyForwardedSet[idStr] = true
			}
		}
	}

	total := opt.Total
	if total <= 0 || total > len(payload.Messages) {
		total = len(payload.Messages)
	}

	candidateMessages := payload.Messages[:total]
	var pendingMessages []TelegramExportMessage
	historyHitCount := 0

	for _, msg := range candidateMessages {
		idStr := fmt.Sprintf("%v", msg.ID)
		if opt.SkipHistory && alreadyForwardedSet[idStr] {
			historyHitCount++
			continue
		}
		pendingMessages = append(pendingMessages, msg)
	}

	if len(pendingMessages) == 0 {
		if logCb != nil {
			logCb(fmt.Sprintf("跳过已转发已开启，命中 %d 条历史记录，没有剩余待转发消息。", historyHitCount))
		}
		return nil
	}

	// 3. 消息反转支持
	if opt.Reverse {
		for i, j := 0, len(pendingMessages)-1; i < j; i, j = i+1, j-1 {
			pendingMessages[i], pendingMessages[j] = pendingMessages[j], pendingMessages[i]
		}
	}

	// 4. 规格参数归一化
	batchSize := opt.BatchSize
	if batchSize <= 0 {
		batchSize = 10
	}
	segmentSize := opt.SegmentSize
	if segmentSize <= 0 {
		segmentSize = 1000
	}
	segmentDelay := opt.SegmentDelaySeconds
	if segmentDelay < 0 {
		segmentDelay = 10
	}
	batchTimeout := opt.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = 180 * time.Second
	}

	batchCount := int(math.Ceil(float64(len(pendingMessages)) / float64(batchSize)))
	segmentCount := int(math.Ceil(float64(len(pendingMessages)) / float64(segmentSize)))

	batchRoot := filepath.Join(cfg.DataDir, fmt.Sprintf("batch-forward-%s", time.Now().Format("20060102-150405")))
	_ = os.MkdirAll(batchRoot, 0755)

	modeDesc := "试转发 (dry-run)"
	if !opt.DryRun {
		modeDesc = fmt.Sprintf("正式转发 (%s 模式)", opt.Mode)
	}

	if logCb != nil {
		logCb("=================== 转发计划 ===================")
		logCb(fmt.Sprintf("来源文件: %s", opt.ExportPath))
		logCb(fmt.Sprintf("目标地址: %s", opt.Target))
		logCb(fmt.Sprintf("请求条数: %d | 历史命中跳过: %d | 实际待转发: %d", total, historyHitCount, len(pendingMessages)))
		logCb(fmt.Sprintf("每批条数: %d | 总批次: %d | 批次间隔: %d 秒", batchSize, batchCount, opt.DelaySeconds))
		logCb(fmt.Sprintf("自动分段: 每 %d 条分段，共 %d 段，段间休息 %d 秒", segmentSize, segmentCount, segmentDelay))
		logCb(fmt.Sprintf("执行模式: %s", modeDesc))
		logCb(fmt.Sprintf("批次目录: %s", batchRoot))
		logCb("================================================")
	}

	globalBatchIndex := 0
	for segIdx := 0; segIdx < segmentCount; segIdx++ {
		segStart := segIdx * segmentSize
		segEnd := (segIdx + 1) * segmentSize
		if segEnd > len(pendingMessages) {
			segEnd = len(pendingMessages)
		}

		if logCb != nil {
			logCb(fmt.Sprintf("--- 第 %d/%d 段开始：处理记录 %d - %d ---", segIdx+1, segmentCount, segStart+1, segEnd))
		}

		segSlice := pendingMessages[segStart:segEnd]
		segBatchCount := int(math.Ceil(float64(len(segSlice)) / float64(batchSize)))

		for bIdx := 0; bIdx < segBatchCount; bIdx++ {
			// 响应外部中止
			if ctx.Err() != nil {
				return ctx.Err()
			}

			bStart := bIdx * batchSize
			bEnd := (bIdx + 1) * batchSize
			if bEnd > len(segSlice) {
				bEnd = len(segSlice)
			}
			chunk := segSlice[bStart:bEnd]
			globalBatchIndex++

			// 5. 生成当前批次独立 JSON
			batchFileName := fmt.Sprintf("batch-%04d-of-%04d.json", globalBatchIndex, batchCount)
			batchFilePath := filepath.Join(batchRoot, batchFileName)
			batchPayload := TelegramExportPayload{
				ID:       payload.ID,
				Messages: chunk,
			}
			batchBytes, _ := json.MarshalIndent(batchPayload, "", "  ")
			if err := os.WriteFile(batchFilePath, batchBytes, 0644); err != nil {
				return fmt.Errorf("生成批次文件 %s 失败: %w", batchFileName, err)
			}

			if logCb != nil {
				logCb(fmt.Sprintf("[第 %d/%d 批] 处理中，记录 %d-%d (包含 %d 条消息)",
					globalBatchIndex, batchCount, segStart+bStart+1, segStart+bEnd, len(chunk)))
			}

			// 6. 构造 tdl forward 命令参数
			subArgs := []string{
				"forward",
				"--from", batchFilePath,
				"--to", opt.Target,
				"--mode", opt.Mode,
			}
			if opt.DryRun {
				subArgs = append(subArgs, "--dry-run")
			} else if opt.Silent {
				subArgs = append(subArgs, "--silent")
			}
			args := BuildTdlArgs(cfg, subArgs...)

			// 7. 单批超时重试执行
			retryCount := 0
			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				batchCtx, batchCancel := context.WithTimeout(ctx, batchTimeout)
				runErr := RunTdlStreaming(batchCtx, cfg, args, func(line string) {
					// 过滤普通进度刷屏，只保留关键日志
					if strings.Contains(line, "error") || strings.Contains(line, "fail") || strings.Contains(line, "FLOOD_WAIT") {
						if logCb != nil {
							logCb("  [tdl] " + line)
						}
					}
				})
				batchCancel()

				if runErr == nil {
					break
				}

				// 判断是否为超时触发重试
				if batchCtx.Err() == context.DeadlineExceeded {
					retryCount++
					if logCb != nil {
						logCb(fmt.Sprintf("  警告: 第 %d 批执行超过 %v 未响应，正在自动重试第 %d 次……",
							globalBatchIndex, batchTimeout, retryCount))
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(3 * time.Second):
					}
					continue
				}

				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("第 %d 批转发失败: %w", globalBatchIndex, runErr)
			}

			// 8. 成功后记录到历史 (非 dry-run)
			if !opt.DryRun {
				for _, m := range chunk {
					idStr := fmt.Sprintf("%v", m.ID)
					alreadyForwardedSet[idStr] = true
				}
				var updatedList []string
				for idStr := range alreadyForwardedSet {
					updatedList = append(updatedList, idStr)
				}
				history[historyKey] = updatedList
				_ = SaveForwardHistory(cfg, history)
			}

			if logCb != nil {
				logCb(fmt.Sprintf("✓ 第 %d/%d 批转发成功", globalBatchIndex, batchCount))
			}

			// 批次间间隔
			if opt.DelaySeconds > 0 && globalBatchIndex < batchCount {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(opt.DelaySeconds) * time.Second):
				}
			}
		}

		// 段间休息
		if segmentDelay > 0 && segIdx < segmentCount-1 {
			if logCb != nil {
				logCb(fmt.Sprintf("第 %d 段完成，段间休息 %d 秒后继续下一段……", segIdx+1, segmentDelay))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(segmentDelay) * time.Second):
			}
		}
	}

	if logCb != nil {
		if opt.DryRun {
			logCb("试转发全部执行完毕，未产生实际发送。")
		} else {
			logCb(fmt.Sprintf("全部分批转发执行完毕！已更新历史记录: %s", cfg.ForwardHistoryPath()))
		}
	}
	return nil
}
