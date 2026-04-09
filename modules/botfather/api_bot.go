package botfather

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/gocraft/dbr/v2"
	"go.uber.org/zap"
)

// syncMessages 同步频道历史消息
func (bf *BotFather) syncMessages(c *wkhttp.Context) {
	var req BotSyncMessagesReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("invalid request body"))
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}
	if req.ChannelType == 0 {
		c.ResponseError(errors.New("channel_type不能为空"))
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 200 {
		req.Limit = 200
	}

	robotID := getRobotIDFromContext(c)

	// 群聊场景：验证 bot 是否在群内
	if req.ChannelType == common.ChannelTypeGroup.Uint8() {
		var count int
		_, err := bf.db.session.SelectBySql(
			"SELECT COUNT(*) FROM group_member WHERE group_no=? AND uid=? AND is_deleted=0",
			req.ChannelID, robotID,
		).Load(&count)
		if err != nil {
			bf.Error("failed to query group members", zap.Error(err))
			c.ResponseError(errors.New("failed to query group members"))
			return
		}
		if count == 0 {
			c.ResponseError(errors.New("bot is not a member of this group"))
			return
		}
	}

	channelID := bf.resolveSpaceChannelID(robotID, req.ChannelID, req.ChannelType)
	syncReq := config.SyncChannelMessageReq{
		LoginUID:        robotID,
		ChannelID:       channelID,
		ChannelType:     req.ChannelType,
		StartMessageSeq: req.StartMessageSeq,
		EndMessageSeq:   req.EndMessageSeq,
		Limit:           req.Limit,
		PullMode:        config.PullMode(req.PullMode),
	}
	resp, err := bf.ctx.IMSyncChannelMessage(syncReq)
	if err != nil {
		bf.Error("同步消息失败", zap.Error(err))
		c.ResponseError(errors.New("同步消息失败"))
		return
	}

	c.Response(resp)
}

// getGroups 获取机器人所在的群组列表
func (bf *BotFather) getGroups(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if robotID == "" {
		c.ResponseError(errors.New("robot_id not found"))
		return
	}

	type GroupInfo struct {
		GroupNo string `json:"group_no"`
		Name    string `json:"name"`
		SpaceID string `json:"space_id,omitempty"`
	}

	spaceID := c.Query("space_id")
	var groups []GroupInfo
	var err error
	if spaceID != "" {
		_, err = bf.ctx.DB().SelectBySql(
			"SELECT gm.group_no, g.name, g.space_id FROM group_member gm INNER JOIN `group` g ON gm.group_no = g.group_no WHERE gm.uid = ? AND gm.is_deleted = 0 AND g.space_id = ?",
			robotID, spaceID,
		).Load(&groups)
	} else {
		_, err = bf.ctx.DB().SelectBySql(
			"SELECT gm.group_no, g.name, g.space_id FROM group_member gm INNER JOIN `group` g ON gm.group_no = g.group_no WHERE gm.uid = ? AND gm.is_deleted = 0",
			robotID,
		).Load(&groups)
	}
	if err != nil {
		bf.Error("查询机器人群组失败", zap.Error(err))
		c.ResponseError(errors.New("查询群组失败"))
		return
	}

	c.JSON(http.StatusOK, groups)
}

// getGroupInfo 获取群信息
func (bf *BotFather) getGroupInfo(c *wkhttp.Context) {
	robotID := c.GetString("robot_id")
	groupNo := c.Param("group_no")

	// Verify bot is a member of this group
	var count int
	_, err := bf.db.session.SelectBySql("SELECT COUNT(*) FROM group_member WHERE group_no=? AND uid=? AND is_deleted=0", groupNo, robotID).Load(&count)
	if err != nil || count == 0 {
		c.ResponseError(errors.New("bot is not a member of this group"))
		return
	}

	var group struct {
		GroupNo   string `db:"group_no"`
		Name      string `db:"name"`
		Notice    string `db:"notice"`
		Creator   string `db:"creator"`
		Status    int    `db:"status"`
		CreatedAt string `db:"created_at"`
	}
	_, err = bf.db.session.Select("group_no, name, IFNULL(notice,'') notice, IFNULL(creator,'') creator, status, created_at").
		From("`group`").Where("group_no=?", groupNo).Load(&group)
	if err != nil {
		c.ResponseError(errors.New("group not found"))
		return
	}

	c.Response(map[string]interface{}{
		"group_no":   group.GroupNo,
		"name":       group.Name,
		"notice":     group.Notice,
		"creator":    group.Creator,
		"status":     group.Status,
		"created_at": group.CreatedAt,
	})
}

// getGroupMembers 获取群成员列表
func (bf *BotFather) getGroupMembers(c *wkhttp.Context) {
	robotID := c.GetString("robot_id")
	groupNo := c.Param("group_no")

	// Verify bot is a member
	var count int
	_, err := bf.db.session.SelectBySql("SELECT COUNT(*) FROM group_member WHERE group_no=? AND uid=? AND is_deleted=0", groupNo, robotID).Load(&count)
	if err != nil || count == 0 {
		c.ResponseError(errors.New("bot is not a member of this group"))
		return
	}

	type member struct {
		UID       string `db:"uid" json:"uid"`
		Name      string `db:"name" json:"name"`
		Role      int    `db:"role" json:"role"`
		Robot     int    `db:"robot" json:"robot"`
		CreatedAt string `db:"created_at" json:"created_at"`
	}

	var members []member
	_, err = bf.db.session.SelectBySql(`
		SELECT gm.uid, IFNULL(u.name,'') name, gm.role, IFNULL(u.robot,0) robot, gm.created_at 
		FROM group_member gm 
		LEFT JOIN user u ON gm.uid = u.uid 
		WHERE gm.group_no = ? AND gm.is_deleted = 0
		ORDER BY gm.role DESC, gm.created_at ASC
	`, groupNo).Load(&members)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.Response(members)
}

// getGroupMd returns GROUP.md content for a bot
func (bf *BotFather) getGroupMd(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if robotID == "" {
		c.ResponseError(errors.New("robot_id not found"))
		return
	}
	groupNo := c.Param("group_no")

	// Verify bot is a group member
	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil {
		bf.Error("check group membership failed", zap.Error(err))
		c.ResponseError(errors.New("check group membership failed"))
		return
	}
	if !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a member of this group", "status": 403})
		return
	}

	result, err := bf.groupService.GetGroupMd(groupNo)
	if err != nil {
		bf.Error("query GROUP.md failed", zap.Error(err))
		c.ResponseError(errors.New("query GROUP.md failed"))
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

// updateGroupMd updates GROUP.md content by a bot
func (bf *BotFather) updateGroupMd(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if robotID == "" {
		c.ResponseError(errors.New("robot_id not found"))
		return
	}
	groupNo := c.Param("group_no")

	// Verify bot is a group member
	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil {
		bf.Error("check group membership failed", zap.Error(err))
		c.ResponseError(errors.New("check group membership failed"))
		return
	}
	if !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a member of this group", "status": 403})
		return
	}

	// Verify bot_admin
	isBotAdmin, err := bf.groupService.IsBotAdmin(groupNo, robotID)
	if err != nil {
		bf.Error("check bot admin failed", zap.Error(err))
		c.ResponseError(errors.New("check bot admin failed"))
		return
	}
	if !isBotAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a bot_admin in this group", "status": 403})
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("invalid request body"))
		return
	}

	maxSize := group.GetGroupMdMaxSize()
	if len(req.Content) > maxSize {
		c.ResponseError(fmt.Errorf("GROUP.md content exceeds max size %d bytes", maxSize))
		return
	}

	newVersion, err := bf.groupService.UpdateGroupMd(groupNo, req.Content, robotID)
	if err != nil {
		bf.Error("update GROUP.md failed", zap.Error(err))
		c.ResponseError(errors.New("update GROUP.md failed"))
		return
	}

	// Async send notification
	go func() {
		defer func() {
			if r := recover(); r != nil {
				bf.Error("sendGroupMdNotification panic", zap.Any("recover", r))
			}
		}()
		bf.sendGroupMdNotification(groupNo, robotID, newVersion)
	}()

	c.JSON(http.StatusOK, gin.H{
		"version": newVersion,
	})
}

// ========== Space Members API ==========

// botSpaceMembers 查询 Bot 所在 Space 的成员列表，支持按名称搜索
// GET /v1/bot/space/members?keyword=xxx&space_id=xxx&limit=50
func (bf *BotFather) botSpaceMembers(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if robotID == "" {
		c.ResponseError(errors.New("robot_id not found"))
		return
	}

	keyword := strings.TrimSpace(c.Query("keyword"))
	spaceID := strings.TrimSpace(c.Query("space_id"))
	limitStr := c.Query("limit")
	limit := 50
	if l, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil || l == 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	type MemberInfo struct {
		UID   string `json:"uid"`
		Name  string `json:"name"`
		Robot int    `json:"robot"`
	}

	var members []MemberInfo
	var err error

	if spaceID == "" {
		// 查找 bot 所在的所有 Space
		var spaceIDs []string
		_, err = bf.ctx.DB().SelectBySql(
			"SELECT space_id FROM space_member WHERE uid=? AND status=1", robotID,
		).Load(&spaceIDs)
		if err != nil || len(spaceIDs) == 0 {
			c.JSON(http.StatusOK, []MemberInfo{})
			return
		}
		spaceID = spaceIDs[0]
	}

	if keyword != "" {
		_, err = bf.ctx.DB().SelectBySql(
			"SELECT sm.uid, IFNULL(u.name,'') as name, IFNULL(u.robot,0) as robot FROM space_member sm LEFT JOIN user u ON sm.uid=u.uid WHERE sm.space_id=? AND sm.status=1 AND u.name LIKE ? LIMIT ?",
			spaceID, "%"+keyword+"%", limit,
		).Load(&members)
	} else {
		_, err = bf.ctx.DB().SelectBySql(
			"SELECT sm.uid, IFNULL(u.name,'') as name, IFNULL(u.robot,0) as robot FROM space_member sm LEFT JOIN user u ON sm.uid=u.uid WHERE sm.space_id=? AND sm.status=1 LIMIT ?",
			spaceID, limit,
		).Load(&members)
	}
	if err != nil {
		bf.Error("query space members failed", zap.Error(err))
		c.ResponseError(errors.New("failed to query space members"))
		return
	}

	c.JSON(http.StatusOK, members)
}

// ========== Bot Group Management APIs ==========

// botGroupCreate 创建群 (POST /v1/bot/groups/create)
func (bf *BotFather) botGroupCreate(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if robotID == "" {
		c.ResponseError(errors.New("robot_id not found"))
		return
	}

	var req struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
		Creator string   `json:"creator"`
		SpaceID string   `json:"space_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("invalid request body"))
		return
	}
	if len(req.Members) == 0 {
		c.ResponseError(errors.New("members is required"))
		return
	}
	// creator 可选，不传则默认 members[0] 为群主
	if req.Creator == "" {
		req.Creator = req.Members[0]
	}

	// 如果没传 space_id，自动使用 Bot 所在的第一个 Space
	if req.SpaceID == "" {
		var spaceIDs []string
		bf.ctx.DB().SelectBySql(
			"SELECT space_id FROM space_member WHERE uid=? AND status=1 LIMIT 1", robotID,
		).Load(&spaceIDs)
		if len(spaceIDs) > 0 {
			req.SpaceID = spaceIDs[0]
		}
	}

	// 查询 creator 用户信息
	creatorUser, err := bf.userDB.QueryByUID(req.Creator)
	if err != nil {
		bf.Error("query creator info failed", zap.Error(err))
		c.ResponseError(errors.New("failed to query creator info"))
		return
	}
	if creatorUser == nil {
		c.ResponseError(errors.New("creator user not found"))
		return
	}

	// 查询所有成员信息
	memberUsers, err := bf.userDB.QueryByUIDs(req.Members)
	if err != nil {
		bf.Error("query member info failed", zap.Error(err))
		c.ResponseError(errors.New("failed to query member info"))
		return
	}

	// 生成群编号和版本号
	groupNo := util.GenerUUID()
	version, err := bf.ctx.GenSeq(common.GroupSeqKey)
	if err != nil {
		bf.Error("generate group version failed", zap.Error(err))
		c.ResponseError(errors.New("failed to create group"))
		return
	}

	// 群名称：如果为空则用成员名拼接
	groupName := strings.TrimSpace(req.Name)
	if groupName == "" {
		names := []string{creatorUser.Name}
		for _, m := range memberUsers {
			names = append(names, m.Name)
			if len(names) >= 3 {
				break
			}
		}
		groupName = strings.Join(names, "、")
	}
	// 限制群名长度
	nameRunes := []rune(groupName)
	if len(nameRunes) > 20 {
		groupName = string(nameRunes[:20])
	}

	// 开启事务
	tx, err := bf.ctx.DB().Begin()
	if err != nil {
		bf.Error("begin transaction failed", zap.Error(err))
		c.ResponseError(errors.New("failed to create group"))
		return
	}
	defer tx.RollbackUnlessCommitted()

	// 插入群记录
	err = bf.groupDB.InsertTx(&group.Model{
		GroupNo: groupNo,
		Name:    groupName,
		Creator: req.Creator,
		Status:  group.GroupStatusNormal,
		Version: version,
		SpaceID: req.SpaceID,
	}, tx)
	if err != nil {
		bf.Error("insert group record failed", zap.Error(err))
		c.ResponseError(errors.New("failed to create group"))
		return
	}

	// 插入创建者为群主
	creatorMemberVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)
	err = bf.groupDB.InsertMemberTx(&group.MemberModel{
		GroupNo:   groupNo,
		UID:       req.Creator,
		Role:      group.MemberRoleCreator,
		Version:   creatorMemberVersion,
		Status:    int(common.GroupMemberStatusNormal),
		InviteUID: req.Creator,
		Robot:     creatorUser.Robot,
		Vercode:   fmt.Sprintf("%s@%d", util.GenerUUID(), common.GroupMember),
	}, tx)
	if err != nil {
		bf.Error("insert creator member failed", zap.Error(err))
		c.ResponseError(errors.New("failed to create group"))
		return
	}

	// 插入其他成员
	// TODO: batch INSERT for better performance with large member lists
	allMemberUIDs := []string{req.Creator}
	memberVos := []*config.UserBaseVo{{UID: req.Creator, Name: creatorUser.Name}}
	for _, memberUser := range memberUsers {
		if memberUser.UID == req.Creator {
			continue // 跳过创建者（已添加）
		}
		if memberUser.UID == robotID {
			continue // 跳过 Bot 自己（下面单独处理）
		}
		memberVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)
		err = bf.groupDB.InsertMemberTx(&group.MemberModel{
			GroupNo:   groupNo,
			UID:       memberUser.UID,
			Role:      group.MemberRoleCommon,
			Version:   memberVersion,
			Status:    int(common.GroupMemberStatusNormal),
			InviteUID: req.Creator,
			Robot:     memberUser.Robot,
			Vercode:   fmt.Sprintf("%s@%d", util.GenerUUID(), common.GroupMember),
		}, tx)
		if err != nil {
			bf.Error("insert member failed", zap.Error(err), zap.String("uid", memberUser.UID))
			c.ResponseError(errors.New("failed to create group"))
			return
		}
		allMemberUIDs = append(allMemberUIDs, memberUser.UID)
		memberVos = append(memberVos, &config.UserBaseVo{UID: memberUser.UID, Name: memberUser.Name})
	}

	// Bot 自动加入群并设为 bot_admin
	botMemberVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)
	err = bf.groupDB.InsertMemberTx(&group.MemberModel{
		GroupNo:   groupNo,
		UID:       robotID,
		Role:      group.MemberRoleCommon,
		Version:   botMemberVersion,
		Status:    int(common.GroupMemberStatusNormal),
		InviteUID: req.Creator,
		Robot:     1,
		Vercode:   fmt.Sprintf("%s@%d", util.GenerUUID(), common.GroupMember),
	}, tx)
	if err != nil {
		bf.Error("insert bot member failed", zap.Error(err))
	} else {
		allMemberUIDs = append(allMemberUIDs, robotID)
		memberVos = append(memberVos, &config.UserBaseVo{UID: robotID, Name: robotID})
	}
	// botNeedAdmin 标记：commit 后设置 bot_admin
	botInGroup := err == nil

	// 提交事务
	if err := tx.Commit(); err != nil {
		bf.Error("commit transaction failed", zap.Error(err))
		c.ResponseError(errors.New("failed to create group"))
		return
	}

	// 在 WuKongIM 创建频道（必须在发通知之前，放在 commit 之后避免孤儿频道）
	err = bf.ctx.IMCreateOrUpdateChannel(&config.ChannelCreateReq{
		ChannelID:   groupNo,
		ChannelType: common.ChannelTypeGroup.Uint8(),
		Subscribers: allMemberUIDs,
	})
	if err != nil {
		bf.Error("failed to create IM channel (group created in DB, channel can be retried)", zap.Error(err))
	}

	// 设置 Bot 为 bot_admin（在 commit 后，因为 UpdateBotAdmin 不支持事务）
	if botInGroup {
		botAdminVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)
		if err := bf.groupDB.UpdateBotAdmin(groupNo, robotID, 1, botAdminVersion); err != nil {
			bf.Error("set bot_admin failed", zap.Error(err))
		}
	}

	// 频道就绪后发布群创建通知（通知客户端同步会话列表）
	bf.ctx.SendGroupCreate(&config.MsgGroupCreateReq{
		Creator:     req.Creator,
		CreatorName: creatorUser.Name,
		GroupNo:     groupNo,
		Version:     version,
		Members:     memberVos,
	})

	c.Response(map[string]interface{}{
		"group_no": groupNo,
		"name":     groupName,
	})
}

// botGroupUpdate 编辑群信息 (PUT /v1/bot/groups/:group_no)
func (bf *BotFather) botGroupUpdate(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	groupNo := c.Param("group_no")

	// 权限检查：Bot 必须是群成员
	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil || !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a member of this group"})
		return
	}

	// 权限检查：Bot 必须是 bot_admin
	isBotAdmin, err := bf.groupService.IsBotAdmin(groupNo, robotID)
	if err != nil || !isBotAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a bot_admin in this group"})
		return
	}

	var req struct {
		Name   *string `json:"name"`
		Notice *string `json:"notice"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("invalid request body"))
		return
	}
	if req.Name == nil && req.Notice == nil {
		c.ResponseError(errors.New("at least one of name or notice is required"))
		return
	}

	// 查询群信息
	groupModel, err := bf.groupDB.QueryWithGroupNo(groupNo)
	if err != nil || groupModel == nil {
		c.ResponseError(errors.New("group not found"))
		return
	}
	if groupModel.Status == group.GroupStatusDisband {
		c.ResponseError(errors.New("group has been disbanded"))
		return
	}

	// 生成新版本号
	version, err := bf.ctx.GenSeq(common.GroupSeqKey)
	if err != nil {
		c.ResponseError(errors.New("failed to update group"))
		return
	}
	groupModel.Version = version

	// 更新字段
	if req.Name != nil {
		nameRunes := []rune(*req.Name)
		if len(nameRunes) > 20 {
			*req.Name = string(nameRunes[:20])
		}
		groupModel.Name = *req.Name
	}
	if req.Notice != nil {
		groupModel.Notice = *req.Notice
	}

	// 事务更新
	tx, err := bf.ctx.DB().Begin()
	if err != nil {
		c.ResponseError(errors.New("failed to update group"))
		return
	}
	defer tx.RollbackUnlessCommitted()

	err = bf.groupDB.UpdateTx(groupModel, tx)
	if err != nil {
		bf.Error("failed to update group", zap.Error(err))
		c.ResponseError(errors.New("failed to update group"))
		return
	}

	if err := tx.Commit(); err != nil {
		c.ResponseError(errors.New("failed to update group"))
		return
	}

	// 发布群更新事件（name 和 notice 分开发送，避免 attrKey 只保留最后一个）
	if req.Name != nil {
		bf.ctx.SendGroupUpdate(&config.MsgGroupUpdateReq{
			GroupNo:      groupNo,
			Operator:     robotID,
			OperatorName: robotID,
			Attr:         common.GroupAttrKeyName,
			Data:         map[string]string{"name": *req.Name},
		})
	}
	if req.Notice != nil {
		bf.ctx.SendGroupUpdate(&config.MsgGroupUpdateReq{
			GroupNo:      groupNo,
			Operator:     robotID,
			OperatorName: robotID,
			Attr:         common.GroupAttrKeyNotice,
			Data:         map[string]string{"notice": *req.Notice},
		})
	}

	// 通知客户端刷新频道信息（群名/公告变更）
	bf.ctx.SendChannelUpdateToGroup(groupNo)

	c.Response(map[string]interface{}{"ok": true})
}

// botGroupMemberAdd 添加群成员 (POST /v1/bot/groups/:group_no/members/add)
func (bf *BotFather) botGroupMemberAdd(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	groupNo := c.Param("group_no")

	// 权限检查：Bot 必须是群成员
	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil || !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a member of this group"})
		return
	}

	var req struct {
		Members []string `json:"members"`
	}
	if err := c.BindJSON(&req); err != nil || len(req.Members) == 0 {
		c.ResponseError(errors.New("members is required"))
		return
	}

	// 查询群信息
	groupModel, err := bf.groupDB.QueryWithGroupNo(groupNo)
	if err != nil || groupModel == nil {
		c.ResponseError(errors.New("group not found"))
		return
	}
	if groupModel.Status == group.GroupStatusDisband {
		c.ResponseError(errors.New("group has been disbanded"))
		return
	}

	// 去重
	uidSet := make(map[string]bool)
	var uniqueUIDs []string
	for _, uid := range req.Members {
		uid = strings.TrimSpace(uid)
		if uid != "" && !uidSet[uid] {
			uidSet[uid] = true
			uniqueUIDs = append(uniqueUIDs, uid)
		}
	}
	if len(uniqueUIDs) == 0 {
		c.ResponseError(errors.New("no valid members after deduplication"))
		return
	}

	// 查询用户信息
	memberUsers, err := bf.userDB.QueryByUIDs(uniqueUIDs)
	if err != nil {
		c.ResponseError(errors.New("failed to query member info"))
		return
	}

	// 过滤已在群内的成员
	existingMembers, err := bf.groupDB.QueryMembersWithUids(uniqueUIDs, groupNo)
	if err != nil {
		c.ResponseError(errors.New("failed to query group members"))
		return
	}
	existingSet := make(map[string]bool)
	for _, m := range existingMembers {
		if m.IsDeleted == 0 {
			existingSet[m.UID] = true
		}
	}

	// 过滤黑名单
	blacklistMembers, _ := bf.groupDB.QueryMembersWithStatus(groupNo, int(common.GroupMemberStatusBlacklist))
	blacklistSet := make(map[string]bool)
	for _, m := range blacklistMembers {
		blacklistSet[m.UID] = true
	}

	tx, err := bf.ctx.DB().Begin()
	if err != nil {
		c.ResponseError(errors.New("failed to add members"))
		return
	}
	defer tx.RollbackUnlessCommitted()

	// TODO: batch INSERT for better performance with large member lists
	var addedUIDs []string
	var addedVos []*config.UserBaseVo
	for _, memberUser := range memberUsers {
		if existingSet[memberUser.UID] || blacklistSet[memberUser.UID] {
			continue
		}
		memberVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)

		// 检查是否之前被删除过（需要恢复）
		existDelete, _ := bf.groupDB.ExistMemberDelete(memberUser.UID, groupNo)
		if existDelete {
			// 恢复已删除的成员
			_, err = tx.Update("group_member").SetMap(map[string]interface{}{
				"role":       group.MemberRoleCommon,
				"version":    memberVersion,
				"is_deleted": 0,
				"invite_uid": robotID,
				"created_at": dbr.Expr("Now()"),
			}).Where("group_no=? and uid=?", groupNo, memberUser.UID).Exec()
		} else {
			err = bf.groupDB.InsertMemberTx(&group.MemberModel{
				GroupNo:   groupNo,
				UID:       memberUser.UID,
				Role:      group.MemberRoleCommon,
				Version:   memberVersion,
				Status:    int(common.GroupMemberStatusNormal),
				InviteUID: robotID,
				Robot:     memberUser.Robot,
				Vercode:   fmt.Sprintf("%s@%d", util.GenerUUID(), common.GroupMember),
			}, tx)
		}
		if err != nil {
			bf.Error("add group member failed", zap.Error(err), zap.String("uid", memberUser.UID))
			continue
		}
		addedUIDs = append(addedUIDs, memberUser.UID)
		addedVos = append(addedVos, &config.UserBaseVo{UID: memberUser.UID, Name: memberUser.Name})
	}

	if err := tx.Commit(); err != nil {
		c.ResponseError(errors.New("failed to add members"))
		return
	}

	// 添加 IM 订阅
	if len(addedUIDs) > 0 {
		bf.ctx.IMAddSubscriber(&config.SubscriberAddReq{
			ChannelID:   groupNo,
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Subscribers: addedUIDs,
		})

		// 发布成员添加事件
		bf.ctx.SendGroupMemberAdd(&config.MsgGroupMemberAddReq{
			Operator:     robotID,
			OperatorName: robotID,
			GroupNo:      groupNo,
			Members:      addedVos,
		})

		// 发送群成员更新 CMD，通知客户端刷新成员列表
		bf.ctx.SendCMD(config.MsgCMDReq{
			ChannelID:   groupNo,
			ChannelType: common.ChannelTypeGroup.Uint8(),
			CMD:         common.CMDGroupMemberUpdate,
			Param: map[string]interface{}{
				"group_no": groupNo,
			},
		})
	}

	c.Response(map[string]interface{}{"ok": true, "added": len(addedUIDs)})
}

// botGroupMemberRemove 移除群成员 (POST /v1/bot/groups/:group_no/members/remove)
func (bf *BotFather) botGroupMemberRemove(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	groupNo := c.Param("group_no")

	// 权限检查：Bot 必须是群成员
	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil || !isMember {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a member of this group"})
		return
	}

	// 权限检查：Bot 必须是 bot_admin
	isBotAdmin, err := bf.groupService.IsBotAdmin(groupNo, robotID)
	if err != nil || !isBotAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"msg": "bot is not a bot_admin in this group"})
		return
	}

	var req struct {
		Members []string `json:"members"`
	}
	if err := c.BindJSON(&req); err != nil || len(req.Members) == 0 {
		c.ResponseError(errors.New("members is required"))
		return
	}

	// 查询群信息
	groupModel, err := bf.groupDB.QueryWithGroupNo(groupNo)
	if err != nil || groupModel == nil {
		c.ResponseError(errors.New("group not found"))
		return
	}
	if groupModel.Status == group.GroupStatusDisband {
		c.ResponseError(errors.New("group has been disbanded"))
		return
	}

	// 查询待移除成员的角色信息
	targetMembers, err := bf.groupDB.QueryMembersWithUids(req.Members, groupNo)
	if err != nil {
		c.ResponseError(errors.New("failed to query member info"))
		return
	}

	tx, err := bf.ctx.DB().Begin()
	if err != nil {
		c.ResponseError(errors.New("failed to remove members"))
		return
	}
	defer tx.RollbackUnlessCommitted()

	var removedUIDs []string
	var removedVos []*config.UserBaseVo
	for _, m := range targetMembers {
		if m.IsDeleted == 1 {
			continue // 已删除的跳过
		}
		if m.UID == robotID {
			continue // 不能移除自己
		}
		// Bot 只能移除普通成员（role=0），不能移除群主（role=1）或管理员（role=2）
		if m.Role == group.MemberRoleCreator || m.Role == group.MemberRoleManager {
			continue
		}
		memberVersion, _ := bf.ctx.GenSeq(common.GroupMemberSeqKey)
		err = bf.groupDB.DeleteMemberTx(groupNo, m.UID, memberVersion, tx)
		if err != nil {
			bf.Error("remove group member failed", zap.Error(err), zap.String("uid", m.UID))
			continue
		}
		removedUIDs = append(removedUIDs, m.UID)
		// 查询用户名
		memberUser, _ := bf.userDB.QueryByUID(m.UID)
		name := m.UID
		if memberUser != nil {
			name = memberUser.Name
		}
		removedVos = append(removedVos, &config.UserBaseVo{UID: m.UID, Name: name})
	}

	if err := tx.Commit(); err != nil {
		c.ResponseError(errors.New("failed to remove members"))
		return
	}

	if len(removedUIDs) > 0 {
		// 移除 IM 订阅
		bf.ctx.IMRemoveSubscriber(&config.SubscriberRemoveReq{
			ChannelID:   groupNo,
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Subscribers: removedUIDs,
		})

		// 发布成员移除事件
		bf.ctx.SendGroupMemberBeRemove(&config.MsgGroupMemberRemoveReq{
			Operator:     robotID,
			OperatorName: robotID,
			GroupNo:      groupNo,
			Members:      removedVos,
		})

		// 发送群成员更新 CMD，通知客户端刷新成员列表
		bf.ctx.SendCMD(config.MsgCMDReq{
			ChannelID:   groupNo,
			ChannelType: common.ChannelTypeGroup.Uint8(),
			CMD:         common.CMDGroupMemberUpdate,
			Param: map[string]interface{}{
				"group_no": groupNo,
			},
		})
	}

	c.Response(map[string]interface{}{"ok": true, "removed": len(removedUIDs)})
}

// sendGroupMdNotification sends GROUP.md event notification from bot
func (bf *BotFather) sendGroupMdNotification(groupNo string, updatedBy string, version int64) {
	botUIDs, err := bf.groupService.GetBotMemberUIDs(groupNo)
	if err != nil {
		bf.Error("query bot member UIDs failed", zap.Error(err))
		return
	}

	payload := map[string]interface{}{
		"type":    common.Text,
		"content": "GROUP.md updated",
		"event": map[string]interface{}{
			"type":       "group_md_updated",
			"version":    version,
			"updated_by": updatedBy,
		},
	}
	if len(botUIDs) > 0 {
		payload["mention"] = map[string]interface{}{
			"uids": botUIDs,
		}
	}

	err = bf.ctx.SendMessage(&config.MsgSendReq{
		Header: config.MsgHeader{
			RedDot: 0,
		},
		ChannelID:   groupNo,
		ChannelType: common.ChannelTypeGroup.Uint8(),
		FromUID:     updatedBy,
		Payload:     []byte(util.ToJson(payload)),
	})
	if err != nil {
		bf.Error("send GROUP.md notification failed", zap.Error(err))
	}
}

// spaceUIDPattern matches space-prefixed UIDs: s{digits}_{baseUID}
var spaceUIDPattern = regexp.MustCompile(`^s\d+_(.+)$`)

// stripSpacePrefix extracts the base UID from a space-prefixed UID.
// "s14_abc123" → "abc123", "abc123" → "abc123" (unchanged)
func stripSpacePrefix(uid string) string {
	if m := spaceUIDPattern.FindStringSubmatch(uid); len(m) == 2 {
		return m[1]
	}
	return uid
}

// getUserInfo 查询用户基本信息 (GET /v1/bot/user/info?uid=xxx)
// Bot 通过 token 认证后，查询指定 UID 的用户 name 和 avatar。
// 用于 OpenClaw adapter DM 场景的 sender 名字解析。
func (bf *BotFather) getUserInfo(c *wkhttp.Context) {
	uid := strings.TrimSpace(c.Query("uid"))
	if uid == "" {
		c.ResponseError(errors.New("uid参数不能为空"))
		return
	}

	// Strip space prefix if present (WuKongIM adds s{spaceId}_ in WS layer,
	// but user table stores bare UIDs)
	bareUID := stripSpacePrefix(uid)

	userResp, err := bf.userService.GetUser(bareUID)
	if err != nil || userResp == nil {
		c.JSON(http.StatusNotFound, gin.H{"msg": "用户不存在"})
		return
	}

	cfg := bf.ctx.GetConfig()
	apiURL := cfg.External.BaseURL
	if strings.TrimSpace(apiURL) == "" {
		apiURL = fmt.Sprintf("http://%s:8090", cfg.External.IP)
	}

	c.JSON(http.StatusOK, gin.H{
		"uid":    userResp.UID,
		"name":   userResp.Name,
		"avatar": fmt.Sprintf("%s/users/%s/avatar", apiURL, userResp.UID),
	})
}
