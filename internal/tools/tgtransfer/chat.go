package tgtransfer

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Chat 表示 Telegram 中的单个聊天、群组或频道对象。
type Chat struct {
	ID          string // 聊天数字 ID (如 -10012345678)
	Type        string // 类型: User, Group, Channel, Supergroup 等
	VisibleName string // 界面显示名称
	Username    string // 用户名 (不带 @，无则为空)
	Topics      string // 主题或附加描述
}

var multiSpaceRegex = regexp.MustCompile(`\s{2,}`)

// ParseChatLine 将 chats.txt 中的单行文本解析为 Chat 结构体。
//
// [参数] line: chats.txt 的单行内容
// [返回] 解析后的 Chat 指针；若是空行或表头则返回 nil
// 最近修改时间: 2026-09-13
func ParseChatLine(line string) *Chat {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}

	// 1. 过滤表头行
	if strings.HasPrefix(trimmed, "ID ") && strings.Contains(trimmed, "Type") && strings.Contains(trimmed, "VisibleName") {
		return nil
	}

	// 2. 按两个及以上空格切分，保留群名中的单个英文空格
	parts := multiSpaceRegex.Split(trimmed, -1)
	if len(parts) < 2 {
		return nil
	}

	chat := &Chat{
		ID:   strings.TrimSpace(parts[0]),
		Type: strings.TrimSpace(parts[1]),
	}

	if len(parts) >= 3 {
		chat.VisibleName = strings.TrimSpace(parts[2])
	}
	if len(parts) >= 4 {
		u := strings.TrimSpace(parts[3])
		if u != "-" {
			chat.Username = strings.TrimPrefix(u, "@")
		}
	}
	if len(parts) >= 5 {
		chat.Topics = strings.TrimSpace(strings.Join(parts[4:], "  "))
	}

	return chat
}

// ParseChatList 从完整文本内容中批量解析聊天条目。
//
// [参数] content: chats.txt 的文本全文
// [返回] 聊天列表切片
// 最近修改时间: 2026-09-13
func ParseChatList(content string) []Chat {
	lines := strings.Split(content, "\n")
	var chats []Chat
	for _, line := range lines {
		if c := ParseChatLine(line); c != nil {
			chats = append(chats, *c)
		}
	}
	return chats
}

// FetchChats 执行 tdl chat ls 获取最新聊天列表，并缓存到 chats.txt。
//
// [参数] ctx: 上下文；cfg: 运行配置；logCb: 日志回调
// [返回] 最新聊天列表切片；出错返回 error
// 最近修改时间: 2026-09-13
func FetchChats(ctx context.Context, cfg *Config, logCb func(string)) ([]Chat, error) {
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, err
	}

	args := BuildTdlArgs(cfg, "chat", "ls")
	if logCb != nil {
		logCb("正在执行 tdl chat ls 获取聊天、群组与频道列表……")
	}

	output, err := RunTdlOutput(ctx, cfg, args)
	if err != nil {
		return nil, fmt.Errorf("刷新聊天列表失败: %w", err)
	}

	// 1. 将拉取到的结果缓存写入 chats.txt
	chatsFile := cfg.ChatListPath()
	if writeErr := os.WriteFile(chatsFile, []byte(output), 0644); writeErr != nil {
		if logCb != nil {
			logCb(fmt.Sprintf("警告: 写入 chats.txt 失败: %v", writeErr))
		}
	}

	chats := ParseChatList(output)
	if logCb != nil {
		logCb(fmt.Sprintf("聊天列表刷新成功，共解析出 %d 个聊天。", len(chats)))
	}
	return chats, nil
}

// LoadCachedChats 从本地 chats.txt 读取已有的聊天列表。
//
// [参数] cfg: 运行配置
// [返回] 缓存的聊天列表；若文件不存在则返回空切片
// 最近修改时间: 2026-09-13
func LoadCachedChats(cfg *Config) ([]Chat, error) {
	chatsFile := cfg.ChatListPath()
	data, err := os.ReadFile(chatsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseChatList(string(data)), nil
}

// FormatChatOption 生成在下拉框中易于识别展示的格式化文案。
//
// 格式: 名称 | 类型 | ID (@username)
//
// [参数] c: 聊天对象
// [返回] 格式化后的单行展示文本
// 最近修改时间: 2026-09-13
func FormatChatOption(c Chat) string {
	name := c.VisibleName
	if name == "" {
		name = "-"
	}
	usernamePart := ""
	if c.Username != "" && c.Username != "-" {
		usernamePart = " @" + c.Username
	}
	return fmt.Sprintf("%s | %s | %s%s", name, c.Type, c.ID, usernamePart)
}

// ExtractChatID 从下拉框选中文案或手动输入中提取真实的聊天 ID。
//
// [参数] input: 用户输入的文本或下拉项
// [返回] 提取出的 Telegram ID 或原始输入值
// 最近修改时间: 2026-09-13
func ExtractChatID(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.Contains(trimmed, " | ") {
		return trimmed
	}
	parts := strings.Split(trimmed, " | ")
	if len(parts) >= 3 {
		idPart := strings.TrimSpace(parts[2])
		// 移除可能附加的 @username
		fields := strings.Fields(idPart)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return trimmed
}
