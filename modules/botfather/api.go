package botfather

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TangSengDaoDao/TangSengDaoDaoServer/modules/base/app"
	"github.com/TangSengDaoDao/TangSengDaoDaoServer/modules/user"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/common"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/config"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/log"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/util"
	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// BotFather BotFather模块
type BotFather struct {
	ctx              *config.Context
	db               *botfatherDB
	cmdHandler       *commandHandler
	userService      user.IService
	appService       app.IService
	robotEventPrefix string
	log.Log
}

// New 创建BotFather实例
func New(ctx *config.Context) *BotFather {
	bf := &BotFather{
		ctx:              ctx,
		db:               newBotfatherDB(ctx),
		cmdHandler:       newCommandHandler(ctx),
		userService:      user.NewService(ctx),
		appService:       app.NewService(ctx),
		robotEventPrefix: "robotEvent:",
		Log:              log.NewTLog("BotFather"),
	}

	// 注册消息监听器
	ctx.AddMessagesListener(bf.messagesListen)

	return bf
}

// Route 路由配置
func (bf *BotFather) Route(r *wkhttp.WKHttp) {
	// skill.md 端点（无需认证）
	r.GET("/v1/bot/skill.md", bf.skillMD)

	// register 端点（只需bot token，不走authBot中间件组）
	r.POST("/v1/bot/register", bf.register)

	// Bot API 端点（使用bot token认证）
	botAPI := r.Group("/v1/bot", bf.authBot())
	{
		botAPI.POST("/sendMessage", bf.sendMessage)
		botAPI.POST("/typing", bf.typing)
		botAPI.POST("/readReceipt", bf.readReceipt)
		botAPI.POST("/events", bf.getEvents)
		botAPI.POST("/events/:event_id/ack", bf.eventAck)
		botAPI.POST("/stream/start", bf.streamStart)
		botAPI.POST("/stream/end", bf.streamEnd)
		botAPI.POST("/heartbeat", bf.heartbeat)
	}

	// 初始化BotFather系统用户
	bf.initBotFatherUser()
}

// skillMD 返回skill.md文档
func (bf *BotFather) skillMD(c *wkhttp.Context) {
	cfg := bf.ctx.GetConfig()
	apiURL := cfg.External.BaseURL
	if strings.TrimSpace(apiURL) == "" {
		apiURL = fmt.Sprintf("http://%s:8090", cfg.External.IP)
	}
	wsURL := deriveWSURL(cfg)
	content := generateSkillMD(apiURL, wsURL)
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	c.String(http.StatusOK, content)
}

// ========== 消息监听 ==========

func (bf *BotFather) messagesListen(messages []*config.MessageResp) {
	for _, message := range messages {
		if message.ChannelType != common.ChannelTypePerson.Uint8() {
			continue
		}

		// 检查是否是发给BotFather的DM
		toUID := common.GetToChannelIDWithFakeChannelID(message.ChannelID, message.FromUID)
		if toUID != BotFatherUID {
			continue
		}

		// 解析消息内容
		payloadValue := gjson.ParseBytes(message.Payload)
		if !payloadValue.Exists() {
			continue
		}
		contentType := payloadValue.Get("type").Int()
		if contentType != int64(common.Text) {
			continue
		}
		content := payloadValue.Get("content").String()
		if content == "" {
			continue
		}

		// 处理命令
		go bf.cmdHandler.HandleMessage(message.FromUID, content)
	}
}

// ========== BotFather用户初始化 ==========

func (bf *BotFather) initBotFatherUser() {
	// 检查BotFather用户是否存在
	userResp, err := bf.userService.GetUserWithUsername(BotFatherUID)
	if err != nil {
		bf.Error("查询BotFather用户失败", zap.Error(err))
	}
	if userResp == nil {
		// 创建BotFather用户
		err = bf.userService.AddUser(&user.AddUserReq{
			UID:      BotFatherUID,
			Username: BotFatherUID,
			Name:     BotFatherName,
		})
		if err != nil {
			bf.Error("创建BotFather用户失败", zap.Error(err))
			return
		}
		bf.Info("BotFather用户创建成功")
	}

	// 确保BotFather在robot表中有记录
	robot, err := bf.db.queryRobotByRobotID(BotFatherUID)
	if err != nil {
		bf.Error("查询BotFather机器人记录失败", zap.Error(err))
	}
	if robot == nil {
		// 创建App
		appResp, err := bf.appService.CreateApp(app.Req{AppID: BotFatherUID})
		if err != nil {
			bf.Error("创建BotFather App失败", zap.Error(err))
			return
		}

		tx, err := bf.db.session.Begin()
		if err != nil {
			bf.Error("开启事务失败", zap.Error(err))
			return
		}
		defer func() {
			if err := recover(); err != nil {
				tx.Rollback()
				panic(err)
			}
		}()

		err = bf.db.insertRobotTx(&robotModel{
			AppID:    appResp.AppID,
			RobotID:  BotFatherUID,
			Username: BotFatherUID,
			Token:    appResp.AppKey,
			Version:  bf.ctx.GenSeq(common.RobotSeqKey),
			Status:   1,
		}, tx)
		if err != nil {
			tx.Rollback()
			bf.Error("插入BotFather机器人记录失败", zap.Error(err))
			return
		}
		err = tx.Commit()
		if err != nil {
			bf.Error("提交事务失败", zap.Error(err))
			return
		}
		bf.Info("BotFather机器人记录创建成功")
	}

	// 确保BotFather与所有用户建立好友关系
	bf.ensureBotFatherFriends()
}

// ensureBotFatherFriends 批量为缺少BotFather好友关系的用户添加
func (bf *BotFather) ensureBotFatherFriends() {
	_, err := bf.db.session.InsertBySql(`
		INSERT IGNORE INTO friend (uid, to_uid, version)
		SELECT u.uid, ?, 1 FROM user u
		WHERE u.uid NOT IN (?, 'u_10000', 'fileHelper')
		AND u.status = 1
		AND NOT EXISTS (
			SELECT 1 FROM friend f WHERE f.uid = u.uid AND f.to_uid = ?
		)
	`, BotFatherUID, BotFatherUID, BotFatherUID).Exec()
	if err != nil {
		bf.Warn("批量添加BotFather好友关系失败", zap.Error(err))
	}
}

// ========== Bot Token 认证中间件 ==========

func (bf *BotFather) authBot() wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {
		token := extractBotToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "缺少Authorization头或token无效"})
			return
		}

		robot, err := bf.db.queryRobotByBotToken(token)
		if err != nil {
			bf.Error("查询机器人失败", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "认证失败"})
			return
		}
		if robot == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "无效的bot token"})
			return
		}

		// 将robot信息存入上下文
		c.Set("robot_id", robot.RobotID)
		c.Set("robot", robot)
		c.Next()
	}
}

func extractBotToken(c *wkhttp.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func getRobotFromContext(c *wkhttp.Context) *robotModel {
	v, exists := c.Get("robot")
	if !exists {
		return nil
	}
	return v.(*robotModel)
}

func getRobotIDFromContext(c *wkhttp.Context) string {
	v, _ := c.Get("robot_id")
	if v == nil {
		return ""
	}
	return v.(string)
}

// ========== Bot Register API ==========

func (bf *BotFather) register(c *wkhttp.Context) {
	token := extractBotToken(c)
	if token == "" {
		c.ResponseError(errors.New("缺少Authorization头"))
		return
	}

	robot, err := bf.db.queryRobotByBotToken(token)
	if err != nil {
		bf.Error("查询机器人失败", zap.Error(err))
		c.ResponseError(errors.New("认证失败"))
		return
	}
	if robot == nil {
		c.ResponseError(errors.New("无效的bot token"))
		return
	}

	// 获取或创建 IM Token
	// force_refresh=true 时忽略缓存，强制从 WuKongIM 重新申请（适用于 WuKongIM 重启后 token 失效场景）
	forceRefresh := c.Query("force_refresh") == "true"
	imToken := robot.IMTokenCache
	if forceRefresh || strings.TrimSpace(imToken) == "" {
		imToken, err = bf.getOrCreateIMToken(robot.RobotID)
		if err != nil {
			bf.Error("获取IM Token失败", zap.Error(err))
			c.ResponseError(errors.New("获取IM Token失败"))
			return
		}
		// 缓存IM Token
		bf.db.updateRobotIMTokenCache(robot.RobotID, imToken)
	}

	cfg := bf.ctx.GetConfig()
	apiURL := cfg.External.BaseURL
	if strings.TrimSpace(apiURL) == "" {
		apiURL = fmt.Sprintf("http://%s:8090", cfg.External.IP)
	}
	wsURL := deriveWSURL(cfg)

	c.Response(&BotRegisterResp{
		RobotID:        robot.RobotID,
		IMToken:        imToken,
		WSURL:          wsURL,
		APIURL:         apiURL,
		OwnerUID:       robot.CreatorUID,
		OwnerChannelID: robot.CreatorUID,
	})
}

func (bf *BotFather) getOrCreateIMToken(robotID string) (string, error) {
	token := util.GenerUUID()
	resp, err := bf.ctx.UpdateIMToken(config.UpdateIMTokenReq{
		UID:         robotID,
		Token:       token,
		DeviceFlag:  config.APP,
		DeviceLevel: config.DeviceLevelMaster,
	})
	if err != nil {
		return "", err
	}
	if resp.Status != config.UpdateTokenStatusSuccess {
		return "", fmt.Errorf("更新IM Token状态异常: %d", resp.Status)
	}
	return token, nil
}

// ========== Bot Send Message API ==========

func (bf *BotFather) sendMessage(c *wkhttp.Context) {
	var req BotSendMessageReq
	if err := c.BindJSON(&req); err != nil {
		bf.Error("数据格式有误", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误"))
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
	if len(req.Payload) == 0 {
		c.ResponseError(errors.New("payload不能为空"))
		return
	}

	robotID := getRobotIDFromContext(c)
	result, err := bf.ctx.SendMessageWithResult(&config.MsgSendReq{
		Header: config.MsgHeader{
			RedDot: 1,
		},
		StreamNo:    req.StreamNo,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		FromUID:     robotID,
		Payload:     []byte(util.ToJson(req.Payload)),
	})
	if err != nil {
		bf.Error("发送消息失败", zap.Error(err))
		c.ResponseError(errors.New("发送消息失败"))
		return
	}
	c.Response(result)
}

// ========== Bot Typing API ==========

func (bf *BotFather) typing(c *wkhttp.Context) {
	var req BotTypingReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("数据格式有误"))
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

	robotID := getRobotIDFromContext(c)
	// DM 场景：param.channel_id 必须是 bot 自身 UID（与正常客户端一致），
	// 因为客户端用 param.channel_id 匹配会话，用户与 bot 的会话 channel_id = robotID
	paramChannelID := req.ChannelID
	if req.ChannelType == uint8(common.ChannelTypePerson) {
		paramChannelID = robotID
	}
	err := bf.ctx.SendCMD(config.MsgCMDReq{
		NoPersist:   true,
		CMD:         common.CMDTyping,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		Param: map[string]interface{}{
			"from_uid":     robotID,
			"channel_id":   paramChannelID,
			"channel_type": req.ChannelType,
		},
	})
	if err != nil {
		bf.Error("发送typing失败", zap.Error(err))
		c.ResponseError(errors.New("发送typing失败"))
		return
	}
	c.ResponseOK()
}

// ========== Bot Read Receipt API ==========

func (bf *BotFather) readReceipt(c *wkhttp.Context) {
	var req BotReadReceiptReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("数据格式有误"))
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}

	robotID := getRobotIDFromContext(c)
	channelID := req.ChannelID
	channelType := uint8(common.ChannelTypePerson)
	if req.ChannelType > 0 {
		channelType = req.ChannelType
	}

	// 1. 清除会话未读角标
	err := bf.ctx.IMClearConversationUnread(config.ClearConversationUnreadReq{
		UID:         robotID,
		ChannelID:   channelID,
		ChannelType: channelType,
		Unread:      0,
	})
	if err != nil {
		bf.Warn("清除未读计数失败", zap.Error(err))
	}

	// 2. 消息级已读回执：写入 member_readed + Redis 缓存，触发发送者看到"已读"
	if len(req.MessageIDs) > 0 {
		// 解析字符串消息 ID 为 int64（避免 JS 大数精度丢失）
		messageIDs := make([]int64, 0, len(req.MessageIDs))
		for _, idStr := range req.MessageIDs {
			mid, parseErr := strconv.ParseInt(idStr, 10, 64)
			if parseErr != nil {
				bf.Warn("解析消息ID失败", zap.String("id", idStr), zap.Error(parseErr))
				continue
			}
			messageIDs = append(messageIDs, mid)
		}
		if len(messageIDs) == 0 {
			c.ResponseOK()
			return
		}

		fakeChannelID := channelID
		if channelType == common.ChannelTypePerson.Uint8() {
			fakeChannelID = common.GetFakeChannelIDWith(channelID, robotID)
		}

		// 查询消息详情（需要 FromUID、MessageSeq）
		// DM 场景：用户发给 bot 的消息存储在 channel_id=robotID，
		// 但 adapter 传入的 channel_id 是用户 UID（回复目标），需要交换为 robotID 来搜索
		searchChannelID := channelID
		if channelType == common.ChannelTypePerson.Uint8() {
			searchChannelID = robotID
		}
		syncMsg, err := bf.ctx.IMSearchMessages(&config.MsgSearchReq{
			ChannelID:   searchChannelID,
			ChannelType: channelType,
			MessageIds:  messageIDs,
			LoginUID:    robotID,
		})
		if err != nil {
			bf.Warn("查询消息失败", zap.Error(err))
		} else if syncMsg != nil && len(syncMsg.Messages) > 0 {
			// 批量插入 member_readed
			valueStrings := make([]string, 0, len(syncMsg.Messages))
			valueArgs := make([]interface{}, 0, len(syncMsg.Messages)*4)
			for _, msg := range syncMsg.Messages {
				valueStrings = append(valueStrings, "(?, ?, ?, ?)")
				valueArgs = append(valueArgs, msg.MessageID, fakeChannelID, channelType, robotID)
			}
			stmt := fmt.Sprintf(`INSERT INTO member_readed (message_id, channel_id, channel_type, uid) VALUES %s ON DUPLICATE KEY UPDATE message_id=VALUES(message_id)`,
				strings.Join(valueStrings, ","))
			_, err = bf.db.session.InsertBySql(stmt, valueArgs...).Exec()
			if err != nil {
				bf.Warn("插入已读记录失败", zap.Error(err))
			}

			// 写入 Redis 缓存，定时任务会聚合并通知发送者
			go func() {
				for _, msg := range syncMsg.Messages {
					messageIDStr := strconv.FormatInt(msg.MessageID, 10)
					cacheData := map[string]interface{}{
						"MessageID":      msg.MessageID,
						"MessageIDStr":   messageIDStr,
						"MessageSeq":     msg.MessageSeq,
						"FromUID":        msg.FromUID,
						"ChannelID":      fakeChannelID,
						"ChannelType":    channelType,
						"LoginUID":       robotID,
						"ReqChannelID":   channelID,
						"ReqChannelType": channelType,
					}
					jsonStr, err := json.Marshal(cacheData)
					if err != nil {
						bf.Error("序列化消息已读缓存失败", zap.Error(err))
						continue
					}
					err = bf.ctx.GetRedisConn().SetAndExpire(
						fmt.Sprintf("readedCount:%s", messageIDStr),
						string(jsonStr),
						time.Hour*24*7,
					)
					if err != nil {
						bf.Error("写入已读缓存失败", zap.Error(err), zap.Int64("messageID", msg.MessageID))
					}
				}
			}()
		}
	}

	c.ResponseOK()
}

// ========== Bot Events API (轮询消息) ==========

func (bf *BotFather) getEvents(c *wkhttp.Context) {
	var req BotEventsReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("数据格式有误"))
		return
	}

	robotID := getRobotIDFromContext(c)
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	results, err := bf.getEventsResult(robotID, req.EventID, limit)
	if err != nil {
		c.Response(gin.H{
			"status": 0,
			"msg":    err.Error(),
		})
		return
	}
	c.Response(gin.H{
		"status":  1,
		"results": results,
	})
}

func (bf *BotFather) getEventsResult(robotID string, eventID int64, limit int64) ([]*eventResp, error) {
	key := fmt.Sprintf("%s%s", bf.robotEventPrefix, robotID)
	robotEventJsons, err := bf.ctx.GetRedisConn().ZRangeByScore(key, redis.ZRangeBy{
		Max:   "+inf",
		Min:   fmt.Sprintf("(%d", eventID),
		Count: limit,
	})
	if err != nil {
		return nil, err
	}

	results := make([]*eventResp, 0)
	if len(robotEventJsons) > 0 {
		type robotEvent struct {
			EventID   int64                  `json:"event_id,omitempty"`
			Message   *config.MessageResp    `json:"message,omitempty"`
			EventType string                 `json:"event_type,omitempty"`
			EventData map[string]interface{} `json:"event_data,omitempty"`
			Expire    int64                  `json:"expire,omitempty"`
		}

		events := make([]*robotEvent, 0)
		for _, jsonStr := range robotEventJsons {
			var ev robotEvent
			err = util.ReadJsonByByte([]byte(jsonStr), &ev)
			if err != nil {
				bf.Error("解码事件失败", zap.Error(err))
				continue
			}
			events = append(events, &ev)
		}

		sort.Slice(events, func(i, j int) bool {
			return events[i].EventID < events[j].EventID
		})

		for _, ev := range events {
			resp := &eventResp{
				EventID: ev.EventID,
			}
			if ev.Message != nil {
				resp.Message = &messageResp{
					MessageID:  ev.Message.MessageID,
					MessageSeq: ev.Message.MessageSeq,
					FromUID:    ev.Message.FromUID,
					Timestamp:  ev.Message.Timestamp,
				}
				if ev.Message.ChannelType != common.ChannelTypePerson.Uint8() {
					resp.Message.ChannelID = ev.Message.ChannelID
					resp.Message.ChannelType = ev.Message.ChannelType
				}
				var payloadMap map[string]interface{}
				if err := util.ReadJsonByByte(ev.Message.Payload, &payloadMap); err == nil {
					resp.Message.Payload = payloadMap
				}
			}
			if ev.EventType != "" {
				resp.EventType = ev.EventType
				resp.EventData = ev.EventData
			}
			results = append(results, resp)
		}
	}
	return results, nil
}

func (bf *BotFather) eventAck(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	eventID, _ := strconv.ParseInt(c.Param("event_id"), 10, 64)

	key := fmt.Sprintf("%s%s", bf.robotEventPrefix, robotID)
	err := bf.ctx.GetRedisConn().ZRemRangeByScore(key, fmt.Sprintf("%d", eventID), fmt.Sprintf("%d", eventID))
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// ========== Bot Stream API ==========

func (bf *BotFather) streamStart(c *wkhttp.Context) {
	var req BotStreamStartReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("数据格式有误"))
		return
	}

	robotID := getRobotIDFromContext(c)
	streamNo, err := bf.ctx.IMStreamStart(config.MessageStreamStartReq{
		Header: config.MsgHeader{
			RedDot: 1,
		},
		FromUID:     robotID,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		Payload:     req.Payload,
	})
	if err != nil {
		bf.Error("stream start失败", zap.Error(err))
		c.ResponseError(errors.New("stream start失败"))
		return
	}
	c.Response(gin.H{
		"stream_no": streamNo,
	})
}

func (bf *BotFather) streamEnd(c *wkhttp.Context) {
	var req BotStreamEndReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.New("数据格式有误"))
		return
	}

	err := bf.ctx.IMStreamEnd(config.MessageStreamEndReq{
		StreamNo:    req.StreamNo,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
	})
	if err != nil {
		bf.Error("stream end失败", zap.Error(err))
		c.ResponseError(errors.New("stream end失败"))
		return
	}
	c.ResponseOK()
}

// ========== Bot Heartbeat API ==========

func (bf *BotFather) heartbeat(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	key := fmt.Sprintf("%s%s", heartbeatKeyPrefix, robotID)
	err := bf.ctx.GetRedisConn().SetAndExpire(key, "1", time.Second*heartbeatTTL)
	if err != nil {
		bf.Error("设置心跳失败", zap.Error(err))
		c.ResponseError(errors.New("设置心跳失败"))
		return
	}
	c.ResponseOK()
}

// ========== 响应模型 ==========

type eventResp struct {
	EventID   int64                  `json:"event_id"`
	Message   *messageResp           `json:"message,omitempty"`
	EventType string                 `json:"event_type,omitempty"` // 自定义事件类型（如 bot_joined_group）
	EventData map[string]interface{} `json:"event_data,omitempty"` // 自定义事件数据
}

type messageResp struct {
	MessageID   int64       `json:"message_id"`
	MessageSeq  uint32      `json:"message_seq"`
	FromUID     string      `json:"from_uid"`
	ChannelID   string      `json:"channel_id,omitempty"`
	ChannelType uint8       `json:"channel_type,omitempty"`
	Timestamp   int32       `json:"timestamp"`
	Payload     interface{} `json:"payload"`
}
