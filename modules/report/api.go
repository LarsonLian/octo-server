package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/util"
	"go.uber.org/zap"
)

// Report 举报
type Report struct {
	ctx *config.Context
	db  *db
	log.Log
}

// New 创建一个举报对象
func New(ctx *config.Context) *Report {
	return &Report{
		ctx: ctx,
		db:  newDB(ctx),
		Log: log.NewTLog("report"),
	}
}

// Route 配置路由规则
func (r *Report) Route(l *wkhttp.WKHttp) {
	v := l.Group("/v1/report")
	{
		v.GET("/categories", r.categoies)
		v.GET("/html", r.reportHTML)
		v.POST("/session/resolve", r.resolveSession)
	}
	auth := l.Group("/v1/reports", r.ctx.AuthMiddleware(l))
	{
		auth.POST("", r.report)

	}
}

const reportSessionPrefix = "report_session:"

func (r *Report) reportHTML(c *wkhttp.Context) {

	mode := c.Query("mode")
	if mode == "" {
		mode = "light"
	}

	// Store sensitive token in a temporary session to avoid URL exposure
	uid := c.Query("uid")
	token := c.Query("token")

	redirectURL, _ := url.Parse(r.ctx.GetConfig().External.H5BaseURL)
	redirectURL.Path = "/report.html"
	q := redirectURL.Query()
	q.Set("lang", c.Query("lang"))

	if uid != "" && token != "" {
		sessionID := util.GenerUUID()
		data, _ := json.Marshal(map[string]string{"uid": uid, "token": token})
		_ = r.ctx.GetRedisConn().SetAndExpire(
			fmt.Sprintf("%s%s", reportSessionPrefix, sessionID),
			string(data),
			5*time.Minute,
		)
		q.Set("session", sessionID)
	}

	q.Set("channel_id", c.Query("channel_id"))
	q.Set("channel_type", c.Query("channel_type"))
	q.Set("mode", mode)
	redirectURL.RawQuery = q.Encode()
	c.Redirect(http.StatusMovedPermanently, redirectURL.String())
}

// resolveSession exchanges a temporary session ID for uid and token
func (r *Report) resolveSession(c *wkhttp.Context) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		r.Error("请求数据格式有误", zap.Error(err))
		respondReportRequestInvalid(c, "")
		return
	}
	if req.SessionID == "" {
		respondReportRequestInvalid(c, "session_id")
		return
	}

	key := fmt.Sprintf("%s%s", reportSessionPrefix, req.SessionID)
	data, err := r.ctx.GetRedisConn().GetString(key)
	if err != nil {
		r.Error("查询举报会话失败", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
		return
	}
	if data == "" {
		httperr.ResponseErrorL(c, errcode.ErrReportSessionInvalid, nil, nil)
		return
	}

	// Delete after use (one-time)
	_ = r.ctx.GetRedisConn().Del(key)

	var session struct {
		UID   string `json:"uid"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(data), &session); err != nil || session.UID == "" || session.Token == "" {
		r.Warn("举报会话数据无效", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportSessionInvalid, nil, nil)
		return
	}

	c.Response(map[string]string{
		"uid":   session.UID,
		"token": session.Token,
	})
}

// 举报
func (r *Report) report(c *wkhttp.Context) {
	var req reportReq
	if err := c.BindJSON(&req); err != nil {
		r.Error("请求数据格式有误", zap.Error(err))
		respondReportRequestInvalid(c, "")
		return
	}
	if field := req.invalidField(); field != "" {
		respondReportRequestInvalid(c, field)
		return
	}

	imgsStr := ""
	if len(req.Imgs) > 0 {
		imgsStr = strings.Join(req.Imgs, ",")
	}

	err := r.db.insert(&model{
		UID:         c.GetLoginUID(),
		CategoryNo:  req.CategoryNo,
		Imgs:        imgsStr,
		Remark:      req.Remark,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
	})
	if err != nil {
		r.Error("添加举报数据失败", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportStoreFailed, nil, nil)
		return
	}

	c.ResponseOK()

}

// 举报类别
func (r *Report) categoies(c *wkhttp.Context) {
	lang := c.Query("lang")
	if lang == "" {
		lang = c.GetHeader("Accept-Language")
	}

	en := false
	if strings.Contains(lang, "en") {
		en = true
	}

	categoryModels, err := r.db.queryCategoryAll()
	if err != nil {
		r.Error("查询举报类别失败", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrReportQueryFailed, nil, nil)
		return
	}

	rootCategories := r.findRootCategories(en, categoryModels)
	if len(rootCategories) > 0 {
		for _, rootCategory := range rootCategories {
			r.fillParentCategory(en, rootCategory, categoryModels, 0)
		}
	}
	c.Response(rootCategories)
}

// 填充父类
func (r *Report) fillParentCategory(en bool, parent *categoryResp, categories []*categoryModel, depth int) {
	if len(categories) == 0 {
		return
	}
	if depth > 100 {
		return
	}
	for _, category := range categories {
		if parent.CategoryNo == category.ParentCategoryNo && parent.CategoryNo != category.CategoryNo {
			if parent.Children == nil {
				parent.Children = make([]*categoryResp, 0)
			}
			categoryNode := newCategoryResp(en, category)
			parent.Children = append(parent.Children, categoryNode)
			r.fillParentCategory(en, categoryNode, categories, depth+1)
		}
	}
}

// 获取根元素
func (r *Report) findRootCategories(en bool, categories []*categoryModel) []*categoryResp {
	if len(categories) > 0 {
		categoryResps := []*categoryResp{}
		for _, category := range categories {
			if category.ParentCategoryNo == "" {
				categoryResps = append(categoryResps, newCategoryResp(en, category))
			}
		}
		return categoryResps
	}

	return nil
}

type categoryResp struct {
	CategoryNo       string          `json:"category_no"`
	CategoryName     string          `json:"category_name"`
	ParentCategoryNo string          `json:"parent_category_no"`
	Children         []*categoryResp `json:"children,omitempty"`
}

func newCategoryResp(en bool, m *categoryModel) *categoryResp {
	categoryName := m.CategoryName
	if en {
		categoryName = m.CategoryEname
	}
	return &categoryResp{
		CategoryNo:       m.CategoryNo,
		CategoryName:     categoryName,
		ParentCategoryNo: m.ParentCategoryNo,
	}
}

type reportReq struct {
	ChannelID   string   `json:"channel_id"`   // 频道id
	ChannelType uint8    `json:"channel_type"` // 频道类型
	CategoryNo  string   `json:"category_no"`  // 类别编号
	Imgs        []string `json:"imgs"`         // 举报图片内容
	Remark      string   `json:"remark"`       // 举报备注
}

func (r reportReq) invalidField() string {
	if r.ChannelID == "" {
		return "channel_id"
	}
	if r.ChannelType <= 0 {
		return "channel_type"
	}
	if r.CategoryNo == "" {
		return "category_no"
	}
	return ""
}

func (r reportReq) check() error {
	switch r.invalidField() {
	case "channel_id":
		return errors.New("频道ID不能为空！")
	case "channel_type":
		return errors.New("频道类型不能为空！")
	case "category_no":
		return errors.New("举报类别不能为空！")
	default:
		return nil
	}
}
