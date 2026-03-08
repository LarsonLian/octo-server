package channel

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-server/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

const (
	// SegmentGapMinutes 对话段落切分间隔（5分钟无新消息切分）
	SegmentGapMinutes = 5
	// DefaultPageSize 默认分页大小
	DefaultPageSize = 20
	// MaxPageSize 最大分页大小
	MaxPageSize = 100
)

// StorylineSegment 对话段落
type StorylineSegment struct {
	ID             string   `json:"id"`               // 段落唯一ID
	StartTime      string   `json:"start_time"`       // 开始时间 (ISO8601)
	EndTime        string   `json:"end_time"`         // 结束时间 (ISO8601)
	MessageCount   int      `json:"message_count"`    // 消息条数
	Participants   []string `json:"participants"`     // 参与者列表
	FirstMessageID string   `json:"first_message_id"` // 首条消息ID（用于跳转定位）
}

// StorylineResp 故事线响应
type StorylineResp struct {
	Segments []*StorylineSegment `json:"segments"`
	Cursor   string              `json:"cursor,omitempty"` // 下一页游标
}

// storylineMessage 简化的消息结构，用于 storyline 处理
type storylineMessage struct {
	MessageID int64
	FromUID   string
	Timestamp int64
}

// getStoryline 获取群聊个人故事线
// GET /v1/channels/:channel_id/:channel_type/storyline
func (ch *Channel) getStoryline(c *wkhttp.Context) {
	loginUID := c.GetLoginUID()
	channelID := c.Param("channel_id")
	channelTypeI64 := util.ParseInt64OrDefault(c.Param("channel_type"), 0)
	channelType := uint8(channelTypeI64)

	if channelID == "" {
		c.ResponseError(errors.New("频道ID不能为空"))
		return
	}

	// 仅支持群聊
	if channelType != common.ChannelTypeGroup.Uint8() {
		c.ResponseError(errors.New("故事线功能仅支持群聊"))
		return
	}

	// 验证用户是群成员
	isMember, err := ch.groupService.ExistMember(channelID, loginUID)
	if err != nil {
		ch.Error("查询群成员信息错误", zap.Error(err))
		c.ResponseError(errors.New("查询群成员信息错误"))
		return
	}
	if !isMember {
		c.ResponseError(errors.New("非群成员无法查询故事线"))
		return
	}

	// 解析查询参数
	filter := c.DefaultQuery("filter", "all") // all | with_ai | with_user:{uid}
	pageSizeStr := c.DefaultQuery("page_size", strconv.Itoa(DefaultPageSize))
	cursor := c.Query("cursor") // 分页游标（基于时间戳）

	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	// 解析 cursor 为起始时间
	var startTime int64 = 0
	if cursor != "" {
		startTime, _ = strconv.ParseInt(cursor, 10, 64)
	}

	// 从 WuKongIM 同步该频道的消息
	syncResp, err := ch.ctx.IMSyncChannelMessage(config.SyncChannelMessageReq{
		LoginUID:    loginUID,
		ChannelID:   channelID,
		ChannelType: channelType,
		Limit:       500, // 查询最近500条消息
		PullMode:    1,   // 向下拉取
	})
	if err != nil {
		ch.Error("同步频道消息失败", zap.Error(err), zap.String("channel_id", channelID))
		c.ResponseError(errors.New("同步频道消息失败"))
		return
	}

	if syncResp == nil || len(syncResp.Messages) == 0 {
		c.JSON(http.StatusOK, &StorylineResp{
			Segments: []*StorylineSegment{},
		})
		return
	}

	// 将 WuKongIM 消息转换为简化结构
	messages := make([]*storylineMessage, 0, len(syncResp.Messages))
	for _, msg := range syncResp.Messages {
		messages = append(messages, &storylineMessage{
			MessageID: msg.MessageID,
			FromUID:   msg.FromUID,
			Timestamp: int64(msg.Timestamp),
		})
	}

	// 过滤用户参与的消息
	filteredMessages := filterStorylineMessages(messages, loginUID, filter)
	if len(filteredMessages) == 0 {
		c.JSON(http.StatusOK, &StorylineResp{
			Segments: []*StorylineSegment{},
		})
		return
	}

	// 按时间窗口聚合成 segments
	segments := aggregateStorylineSegments(filteredMessages, startTime)

	// 分页处理
	var nextCursor string
	if len(segments) > pageSize {
		segments = segments[:pageSize]
		lastSegment := segments[len(segments)-1]
		// 使用最后一个 segment 的结束时间作为下一页游标
		endTimeT, _ := time.Parse(time.RFC3339, lastSegment.EndTime)
		nextCursor = strconv.FormatInt(endTimeT.Unix(), 10)
	}

	// 获取参与者名称（批量查询避免 N+1 问题）
	userService := user.NewService(ch.ctx)
	// 收集所有唯一的 UID
	uidSet := make(map[string]struct{})
	for _, seg := range segments {
		for _, uid := range seg.Participants {
			uidSet[uid] = struct{}{}
		}
	}
	uids := make([]string, 0, len(uidSet))
	for uid := range uidSet {
		uids = append(uids, uid)
	}
	// 批量获取用户信息
	uidToName := make(map[string]string)
	if len(uids) > 0 {
		users, err := userService.GetUsers(uids)
		if err == nil {
			for _, u := range users {
				uidToName[u.UID] = u.Name
			}
		}
	}
	// 替换 UID 为名称
	for _, seg := range segments {
		names := make([]string, 0, len(seg.Participants))
		for _, uid := range seg.Participants {
			if name, ok := uidToName[uid]; ok && name != "" {
				names = append(names, name)
			} else {
				names = append(names, uid)
			}
		}
		seg.Participants = names
	}

	c.JSON(http.StatusOK, &StorylineResp{
		Segments: segments,
		Cursor:   nextCursor,
	})
}

// filterStorylineMessages 过滤用户参与的消息
func filterStorylineMessages(messages []*storylineMessage, loginUID string, filter string) []*storylineMessage {
	filtered := make([]*storylineMessage, 0)

	for _, msg := range messages {
		// 检查是否是用户发送的消息
		isFromUser := msg.FromUID == loginUID

		switch filter {
		case "all":
			// 用户发送的消息
			if isFromUser {
				filtered = append(filtered, msg)
			}
		case "with_ai":
			// 与 AI 的对话
			// TODO(Jerry-Xin): #269 实现 Bot 检测逻辑
			if isFromUser {
				filtered = append(filtered, msg)
			}
		default:
			// with_user:{uid} 格式
			if len(filter) > 10 && filter[:10] == "with_user:" {
				targetUID := filter[10:]
				if isFromUser || msg.FromUID == targetUID {
					filtered = append(filtered, msg)
				}
			} else {
				if isFromUser {
					filtered = append(filtered, msg)
				}
			}
		}
	}

	return filtered
}

// aggregateStorylineSegments 按时间窗口聚合消息为 segments
// startTimestamp 为 0 时，处理所有消息；非 0 时，跳过早于该时间戳的消息（用于分页）
func aggregateStorylineSegments(messages []*storylineMessage, startTimestamp int64) []*StorylineSegment {
	if len(messages) == 0 {
		return []*StorylineSegment{}
	}

	segments := make([]*StorylineSegment, 0)
	segmentGap := time.Duration(SegmentGapMinutes) * time.Minute

	var currentSegment *StorylineSegment
	var currentEndTime time.Time
	participantSet := make(map[string]bool)

	for _, msg := range messages {
		msgTime := time.Unix(msg.Timestamp, 0)

		// 如果指定了 startTimestamp，跳过之前的消息
		if startTimestamp > 0 && msg.Timestamp < startTimestamp {
			continue
		}

		if currentSegment == nil {
			// 创建新的 segment
			currentSegment = &StorylineSegment{
				ID:             strconv.FormatInt(msg.MessageID, 10),
				StartTime:      msgTime.Format(time.RFC3339),
				EndTime:        msgTime.Format(time.RFC3339),
				MessageCount:   1,
				FirstMessageID: strconv.FormatInt(msg.MessageID, 10),
			}
			currentEndTime = msgTime
			participantSet = map[string]bool{msg.FromUID: true}
		} else {
			// 检查时间间隔
			if msgTime.Sub(currentEndTime) > segmentGap {
				// 间隔超过5分钟，保存当前 segment 并创建新的
				currentSegment.Participants = mapKeysToSlice(participantSet)
				segments = append(segments, currentSegment)

				currentSegment = &StorylineSegment{
					ID:             strconv.FormatInt(msg.MessageID, 10),
					StartTime:      msgTime.Format(time.RFC3339),
					EndTime:        msgTime.Format(time.RFC3339),
					MessageCount:   1,
					FirstMessageID: strconv.FormatInt(msg.MessageID, 10),
				}
				currentEndTime = msgTime
				participantSet = map[string]bool{msg.FromUID: true}
			} else {
				// 继续累加到当前 segment
				currentSegment.EndTime = msgTime.Format(time.RFC3339)
				currentSegment.MessageCount++
				currentEndTime = msgTime
				participantSet[msg.FromUID] = true
			}
		}
	}

	// 保存最后一个 segment
	if currentSegment != nil {
		currentSegment.Participants = mapKeysToSlice(participantSet)
		segments = append(segments, currentSegment)
	}

	return segments
}

// mapKeysToSlice 将 map 的 keys 转换为 slice
func mapKeysToSlice(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
