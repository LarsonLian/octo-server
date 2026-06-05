package report

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	wkutil "github.com/Mininglamp-OSS/octo-server/pkg/util"
	"go.uber.org/zap"
)

// Manager 举报管理
type Manager struct {
	ctx       *config.Context
	managerDB *managerDB
	log.Log
	userDB  *user.DB
	db      *db
	groupDB *group.DB
}

// NewManager 创建一个举报对象
func NewManager(ctx *config.Context) *Manager {
	return &Manager{
		ctx:       ctx,
		Log:       log.NewTLog("reportManager"),
		managerDB: newManagerDB(ctx),
		userDB:    user.NewDB(ctx),
		db:        newDB(ctx),
		groupDB:   group.NewDB(ctx),
	}
}

// Route 配置路由规则
func (m *Manager) Route(l *wkhttp.WKHttp) {

	auth := l.Group("/v1/manager", l.AuthMiddleware(m.ctx.Cache(), m.ctx.GetConfig().Cache.TokenCachePrefix))
	{
		auth.GET("/report/list", m.reportList) // 举报列表
	}
}

// 举报列表
func (m *Manager) reportList(c *wkhttp.Context) {
	err := c.CheckLoginRole()
	if err != nil {
		httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
		return
	}
	pageIndex, pageSize := c.GetPage()
	channelType := c.Query("channel_type")
	if channelType == "" {
		respondReportRequestInvalid(c, "channel_type")
		return
	}
	queryChannelType := wkutil.AtoiOrDefault(channelType, 0)
	list, err := m.managerDB.list(uint64(pageSize), uint64(pageIndex), queryChannelType)
	if err != nil {
		m.Error("查询举报列表错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
		return
	}
	count, err := m.managerDB.queryReportCount(queryChannelType)
	if err != nil {
		m.Error("查询举报总数量错误", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
		return
	}
	result := make([]*managerReportResp, 0)
	if len(list) > 0 {
		uids := make([]string, 0)
		reportUserUIDs := make([]string, 0)
		reportGroupIDs := make([]string, 0)
		for _, report := range list {
			uids = append(uids, report.UID)
			if report.ChannelType == common.ChannelTypeGroup.Uint8() {
				reportGroupIDs = append(reportGroupIDs, report.ChannelID)
			} else {
				reportUserUIDs = append(reportUserUIDs, report.ChannelID)
			}
		}
		users, err := m.userDB.QueryByUIDs(uids)
		if err != nil {
			m.Error("查询用户信息错误", zap.Error(err))
			httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
			return
		}
		reprotUsers, err := m.userDB.QueryByUIDs(reportUserUIDs)
		if err != nil {
			m.Error("查询举报用户集合错误", zap.Error(err))
			httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
			return
		}
		reprotGroups, err := m.groupDB.QueryGroupsWithGroupNos(reportGroupIDs)
		if err != nil {
			m.Error("查询举报群集合错误", zap.Error(err))
			httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
			return
		}
		for _, report := range list {
			var username string
			var channelName string
			for _, user := range users {
				if user.UID == report.UID {
					username = user.Name
				}
			}
			if report.ChannelType == common.ChannelTypeGroup.Uint8() {
				for _, group := range reprotGroups {
					if group.GroupNo == report.ChannelID {
						channelName = group.Name
					}
				}
			} else {
				for _, user := range reprotUsers {
					if user.UID == report.ChannelID {
						channelName = user.Name
					}
				}
			}
			imgs := make([]string, 0)
			if report.Imgs != "" {
				imgs = strings.Split(report.Imgs, ",")
			}
			result = append(result, &managerReportResp{
				UID:          report.UID,
				Name:         username,
				Imgs:         imgs,
				ChannelID:    report.ChannelID,
				ChannelType:  report.ChannelType,
				ChannelName:  channelName,
				Remark:       report.Remark,
				CategoryName: report.CategoryName,
				CreateAt:     report.CreatedAt.String(),
			})
		}

	}
	c.Response(map[string]interface{}{
		"count": count,
		"list":  result,
	})
}

type managerReportResp struct {
	UID          string   `json:"uid"`
	Name         string   `json:"name"` //举报者名称
	ChannelID    string   `json:"channel_id"`
	ChannelType  uint8    `json:"channel_type"`
	ChannelName  string   `json:"channel_name"` //被举报的名称 群名称｜用户名
	CategoryName string   `json:"category_name"`
	Imgs         []string `json:"imgs"`   // 举报图片内容
	Remark       string   `json:"remark"` // 举报备注
	CreateAt     string   `json:"create_at"`
}
