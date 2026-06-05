package statistics

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"go.uber.org/zap"
)

// Statistics 统计
type Statistics struct {
	ctx *config.Context
	log.Log
	userService  user.IService
	groupService group.IService
}

// NewStatistics 统计
func NewStatistics(ctx *config.Context) *Statistics {
	return &Statistics{
		ctx:          ctx,
		Log:          log.NewTLog("Statistics"),
		userService:  user.NewService(ctx),
		groupService: group.NewService(ctx),
	}
}

// Route 路由配置
func (s *Statistics) Route(r *wkhttp.WKHttp) {
	v := r.Group("/v1/statistics", s.ctx.AuthMiddleware(r))
	{
		v.GET("/countnum", s.countNum)                                                // 统计数量
		v.GET("/registeruser/:start_date/:end_date", s.registerUserListWithDateSpace) // 某个时间区间的注册统计数据
		v.GET("/createdgroup/:start_date/:end_date", s.createGroupWithDateSpace)      // 某个时间段的建群数据
	}
}

// 统计数量
func (s *Statistics) countNum(c *wkhttp.Context) {
	err := c.CheckLoginRole()
	if err != nil {
		httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
		return
	}
	date := c.Query("date")
	// 获取总用户数
	totalUserCount, err := s.userService.GetAllUserCount()
	if err != nil {
		s.Error("查询用户数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	// 查询某天注册量
	registerCount, err := s.userService.GetRegisterWithDate(date)
	if err != nil {
		s.Error("查询某天用户注册量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	// 查询总群数
	totalGroupCount, err := s.groupService.GetAllGroupCount()
	if err != nil {
		s.Error("查询总群数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	// 查询某天的新建群数量
	groupCreatedCount, err := s.groupService.GetCreatedCountWithDate(date)
	if err != nil {
		s.Error("查询某天群新建数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	// 查询总在线数量
	onlineCount, err := s.userService.GetOnlineCount()
	if err != nil {
		s.Error("查询总在线用户数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	// 查询机器人总数
	var botTotalCount int64
	err = s.ctx.DB().Select("count(*)").From("robot").Where("status=1").LoadOne(&botTotalCount)
	if err != nil {
		s.Error("查询机器人总数错误", zap.Error(err))
		botTotalCount = 0
	}
	c.Response(&countNum{
		UserTotalCount:   totalUserCount,
		RegisterCount:    registerCount,
		GroupTotalCount:  totalGroupCount,
		GroupCreateCount: groupCreatedCount,
		OnlineTotalCount: onlineCount,
		BotTotalCount:    botTotalCount,
	})
}

// 某个时间区间的注册数据
func (s *Statistics) registerUserListWithDateSpace(c *wkhttp.Context) {
	err := c.CheckLoginRole()
	if err != nil {
		httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
		return
	}
	startDate := c.Param("start_date")
	endDate := c.Param("end_date")
	if startDate == "" || endDate == "" {
		respondStatisticsRequestInvalid(c, "date")
		return
	}
	list, err := s.userService.GetRegisterCountWithDateSpace(startDate, endDate)
	if err != nil {
		s.Error("查询注册用户数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	c.Response(list)
}

// 获取某个时间段的建群数量
func (s *Statistics) createGroupWithDateSpace(c *wkhttp.Context) {
	err := c.CheckLoginRole()
	if err != nil {
		httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
		return
	}
	startDate := c.Param("start_date")
	endDate := c.Param("end_date")
	if startDate == "" || endDate == "" {
		respondStatisticsRequestInvalid(c, "date")
		return
	}
	list, err := s.groupService.GetGroupWithDateSpace(startDate, endDate)
	if err != nil {
		s.Error("查询建群数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil)
		return
	}
	c.Response(list)
}

type countNum struct {
	UserTotalCount   int64 `json:"user_total_count"`   // 用户总数
	RegisterCount    int64 `json:"register_count"`     // 注册数量
	GroupTotalCount  int64 `json:"group_total_count"`  // 群总数
	GroupCreateCount int64 `json:"group_create_count"` // 群创建数量
	OnlineTotalCount int64 `json:"online_total_count"` // 总在线数量
	BotTotalCount    int64 `json:"bot_total_count"`    // 机器人总数
}
