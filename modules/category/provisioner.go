package category

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
)

// EnsureDefaultCategory 为 (uid, spaceID) 组合幂等地确保默认分类存在。
//
// 背景（GH octo-server#1228）：Phase 1 遗漏 — 创建 space / 加入 space 时未自动创建默认
// 分类，导致新建空间 GET /v1/spaces/{id}/categories 返回 []，前端无法渲染会话列表。
//
// 并发安全：依赖表 group_category 上的唯一索引 uk_uid_space_is_default，insertDefaultCategory
// 使用 INSERT IGNORE，多次调用 / 并发调用均安全。
//
// 调用方应将其视为非关键路径（catch error 后以 warn 记录、不中断业务），list 端还有
// 兜底补偿；因此本函数不包装 transaction，直接调用 db 层。
func EnsureDefaultCategory(ctx *config.Context, uid, spaceID string) error {
	if ctx == nil || uid == "" || spaceID == "" {
		return nil
	}
	db := newCategoryDB(ctx)
	existing, err := db.queryDefaultCategory(uid, spaceID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	maxSort, err := db.maxSortByUIDAndSpaceID(uid, spaceID)
	if err != nil {
		return err
	}
	return db.insertDefaultCategory(&CategoryModel{
		CategoryID: util.GenerUUID(),
		SpaceID:    spaceID,
		UID:        uid,
		Name:       defaultCategoryNamePlaceholder,
		Sort:       maxSort + 1,
	})
}
