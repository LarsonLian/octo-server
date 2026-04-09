package category

import "github.com/Mininglamp-OSS/octo-lib/pkg/db"

// CategoryModel 群组类别（用户个人视图）
type CategoryModel struct {
	CategoryID string
	SpaceID    string
	UID        string
	Name       string
	Sort       int
	Status     int
	db.BaseModel
}

// groupSettingCategoryRow group_setting 表 category 相关字段投影
type groupSettingCategoryRow struct {
	Id           int64
	GroupNo      string
	UID          string
	CategoryID   *string
	CategorySort int
}

// userGroupInfo 用户在 Space 内群组信息（含 category 分配）
type userGroupInfo struct {
	GroupNo      string
	GroupName    string
	CategoryID   *string
	CategorySort int
}

// ---------- Request ----------

type createCategoryReq struct {
	Name string `json:"name"`
}

type updateCategoryReq struct {
	Name string `json:"name"`
}

type sortCategoriesReq struct {
	CategoryIDs []string `json:"category_ids"`
}

type moveGroupToCategoryReq struct {
	CategoryID string `json:"category_id"` // 空字符串表示移出分类
}

// ---------- Response ----------

type categoryResp struct {
	CategoryID *string                `json:"category_id"`
	Name       string                 `json:"name"`
	Sort       int                    `json:"sort"`
	Groups     []groupInCategoryResp  `json:"groups"`
}

type groupInCategoryResp struct {
	GroupNo      string `json:"group_no"`
	Name         string `json:"name"`
	CategorySort int    `json:"category_sort"`
}
