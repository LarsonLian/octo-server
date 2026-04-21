package botfather

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// validateBotGroupAccess 验证 bot 对群的访问权限
// 返回 robotID, groupNo, ok；如果 ok=false，已向客户端返回错误响应
func (bf *BotFather) validateBotGroupAccess(c *wkhttp.Context) (robotID, groupNo string, ok bool) {
	robotID = getRobotIDFromContext(c)
	groupNo = c.Param("group_no")

	if !thread.IsValidGroupNo(groupNo) {
		c.ResponseError(errors.New("invalid group_no format"))
		return "", "", false
	}

	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil {
		bf.Error("检查群成员失败", zap.Error(err))
		c.ResponseError(errors.New("check group membership failed"))
		return "", "", false
	}
	if !isMember {
		c.ResponseError(errors.New("bot is not a member of this group"))
		return "", "", false
	}

	return robotID, groupNo, true
}

// validateBotThreadAccess 验证 bot 对子区的访问权限
// 返回 robotID, groupNo, shortID, ok；如果 ok=false，已向客户端返回错误响应
func (bf *BotFather) validateBotThreadAccess(c *wkhttp.Context) (robotID, groupNo, shortID string, ok bool) {
	robotID, groupNo, ok = bf.validateBotGroupAccess(c)
	if !ok {
		return "", "", "", false
	}

	shortID = c.Param("short_id")
	if !thread.IsValidShortID(shortID) {
		c.ResponseError(errors.New("invalid short_id format"))
		return "", "", "", false
	}

	return robotID, groupNo, shortID, true
}

// botCreateThread 创建子区
// POST /v1/bot/groups/:group_no/threads
func (bf *BotFather) botCreateThread(c *wkhttp.Context) {
	robotID, groupNo, ok := bf.validateBotGroupAccess(c)
	if !ok {
		return
	}

	var req struct {
		Name            string `json:"name" binding:"required,max=100"`
		SourceMessageID *int64 `json:"source_message_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		bf.Error("参数错误", zap.Error(err))
		c.ResponseError(errors.New("invalid request: name is required"))
		return
	}

	// 获取 bot 的显示名称
	creatorName := robotID
	userResp, _ := bf.userService.GetUserWithUsername(robotID)
	if userResp != nil && userResp.Name != "" {
		creatorName = userResp.Name
	}

	resp, err := bf.threadService.CreateThread(&thread.CreateThreadReq{
		GroupNo:         groupNo,
		Name:            req.Name,
		CreatorUID:      robotID,
		CreatorName:     creatorName,
		SourceMessageID: req.SourceMessageID,
	})
	if err != nil {
		bf.Error("创建子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("robotID", robotID))
		c.ResponseError(err)
		return
	}
	c.Response(resp)
}

// botListThreads 列出群内所有子区
// GET /v1/bot/groups/:group_no/threads
func (bf *BotFather) botListThreads(c *wkhttp.Context) {
	_, groupNo, ok := bf.validateBotGroupAccess(c)
	if !ok {
		return
	}

	threads, err := bf.threadService.GetThreads(groupNo)
	if err != nil {
		bf.Error("获取子区列表失败", zap.Error(err), zap.String("groupNo", groupNo))
		c.ResponseError(err)
		return
	}
	c.Response(threads)
}

// botGetThread 获取子区详情
// GET /v1/bot/groups/:group_no/threads/:short_id
func (bf *BotFather) botGetThread(c *wkhttp.Context) {
	_, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	resp, err := bf.threadService.GetThread(groupNo, shortID)
	if err != nil {
		bf.Error("获取子区详情失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.Response(resp)
}

// botDeleteThread 删除子区
// DELETE /v1/bot/groups/:group_no/threads/:short_id
func (bf *BotFather) botDeleteThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	// DeleteThread 内部会检查是否为创建者或群管理员
	err := bf.threadService.DeleteThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("删除子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// botListThreadMembers 获取子区成员列表
// GET /v1/bot/groups/:group_no/threads/:short_id/members
func (bf *BotFather) botListThreadMembers(c *wkhttp.Context) {
	_, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	members, err := bf.threadService.GetMembers(groupNo, shortID)
	if err != nil {
		bf.Error("获取成员列表失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.Response(members)
}

// botJoinThread 加入子区
// POST /v1/bot/groups/:group_no/threads/:short_id/join
func (bf *BotFather) botJoinThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	err := bf.threadService.JoinThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("加入子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// botLeaveThread 离开子区
// POST /v1/bot/groups/:group_no/threads/:short_id/leave
func (bf *BotFather) botLeaveThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	err := bf.threadService.LeaveThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("离开子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// ==================== Bot Thread GROUP.md ====================

// botGetThreadMd Bot 获取子区 GROUP.md
// GET /v1/bot/groups/:group_no/threads/:short_id/md
func (bf *BotFather) botGetThreadMd(c *wkhttp.Context) {
	_, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	result, err := bf.threadService.GetThreadMd(groupNo, shortID)
	if err != nil {
		bf.Error("query thread GROUP.md failed", zap.Error(err))
		c.ResponseError(errors.New("query thread GROUP.md failed"))
		return
	}
	if result == nil {
		c.JSON(http.StatusOK, gin.H{
			"content":    "",
			"version":    0,
			"updated_at": nil,
			"updated_by": "",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"content":    result.Content,
		"version":    result.Version,
		"updated_at": result.UpdatedAt,
		"updated_by": result.UpdatedBy,
	})
}

// botUpdateThreadMd Bot 更新子区 GROUP.md
// PUT /v1/bot/groups/:group_no/threads/:short_id/md
func (bf *BotFather) botUpdateThreadMd(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	// 权限检查在 API Handler 层完成：验证 bot_admin
	isBotAdmin, err := bf.groupService.IsBotAdmin(groupNo, robotID)
	if err != nil {
		bf.Error("check bot admin failed", zap.Error(err))
		c.ResponseError(errors.New("check bot admin failed"))
		return
	}
	if !isBotAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"msg":    "bot is not a bot_admin in this group",
			"status": 403,
		})
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("invalid request body"))
		return
	}

	// 校验空内容
	if strings.TrimSpace(req.Content) == "" {
		c.ResponseError(errors.New("content must not be empty"))
		return
	}

	maxSize := group.GetGroupMdMaxSize()
	if len(req.Content) > maxSize {
		c.ResponseError(fmt.Errorf("GROUP.md content exceeds max size %d bytes", maxSize))
		return
	}

	// Service 层：纯数据操作透传（通过 threadService，而非直接调用 DB）
	newVersion, err := bf.threadService.UpdateThreadMd(groupNo, shortID, req.Content, robotID)
	if err != nil {
		bf.Error("update thread GROUP.md failed", zap.Error(err))
		c.ResponseError(errors.New("update thread GROUP.md failed"))
		return
	}

	// 异步发送通知到子区频道
	go func() {
		defer func() {
			if r := recover(); r != nil {
				bf.Error("sendThreadMdNotification panic", zap.Any("recover", r))
			}
		}()
		bf.sendThreadMdNotification(groupNo, shortID, robotID, newVersion, "thread_md_updated", "Thread GROUP.md updated")
	}()

	c.JSON(http.StatusOK, gin.H{
		"version": newVersion,
	})
}

// sendThreadMdNotification Bot 发送子区 GROUP.md 变更通知
func (bf *BotFather) sendThreadMdNotification(groupNo, shortID, updatedBy string, version int64, eventType, contentText string) {
	botUIDs, err := bf.groupService.GetBotMemberUIDs(groupNo)
	if err != nil {
		bf.Error("query bot member UIDs failed", zap.Error(err))
		return
	}

	payload := map[string]interface{}{
		"type":    common.Text,
		"content": contentText,
		"event": map[string]interface{}{
			"type":       eventType,
			"version":    version,
			"updated_by": updatedBy,
			"group_no":   groupNo,
			"short_id":   shortID,
		},
	}
	if len(botUIDs) > 0 {
		payload["mention"] = map[string]interface{}{
			"uids": botUIDs,
		}
	}

	channelID := thread.BuildChannelID(groupNo, shortID)
	err = bf.ctx.SendMessage(&config.MsgSendReq{
		Header: config.MsgHeader{
			RedDot: 0,
		},
		ChannelID:   channelID,
		ChannelType: common.ChannelTypeCommunityTopic.Uint8(),
		FromUID:     updatedBy,
		Payload:     []byte(util.ToJson(payload)),
	})
	if err != nil {
		bf.Error("send thread GROUP.md notification failed", zap.Error(err))
	}
}
