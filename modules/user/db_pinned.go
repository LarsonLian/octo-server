package user

import (
	"errors"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/gocraft/dbr/v2"
	"go.uber.org/zap"
)

// PinnedChannelModel 用户置顶频道模型
type PinnedChannelModel struct {
	ID          int64     `db:"id"`
	UID         string    `db:"uid"`
	SpaceID     string    `db:"space_id"`
	ChannelID   string    `db:"channel_id"`
	ChannelType uint8     `db:"channel_type"`
	SortOrder   int       `db:"sort_order"`
	CreatedAt   time.Time `db:"created_at"`
}

// PinnedDB 置顶频道数据库操作
type PinnedDB struct {
	session *dbr.Session
	ctx     *config.Context
	log.Log
}

// NewPinnedDB 创建 PinnedDB
func NewPinnedDB(ctx *config.Context) *PinnedDB {
	return &PinnedDB{
		session: ctx.DB(),
		ctx:     ctx,
		Log:     log.NewTLog("PinnedDB"),
	}
}

// 错误定义
var (
	ErrPinnedLimitExceeded = errors.New("置顶数量已达上限")
	ErrPinnedAlreadyExists = errors.New("该频道已置顶")
)

// Add 添加置顶频道（使用事务 + INSERT IGNORE 防止竞态）
func (d *PinnedDB) Add(uid, spaceID, channelID string, channelType uint8, maxLimit int) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	// 检查当前数量（带锁）
	var count int
	_, err = tx.SelectBySql(
		"SELECT COUNT(*) FROM user_pinned_channel WHERE uid=? AND space_id=? FOR UPDATE",
		uid, spaceID,
	).Load(&count)
	if err != nil {
		return err
	}
	if count >= maxLimit {
		return ErrPinnedLimitExceeded
	}

	// 获取当前最大 sort_order
	var maxSort int
	if _, err := tx.SelectBySql(
		"SELECT IFNULL(MAX(sort_order), 0) FROM user_pinned_channel WHERE uid=? AND space_id=?",
		uid, spaceID,
	).Load(&maxSort); err != nil {
		return err
	}

	// 使用 INSERT IGNORE 防止重复
	result, err := tx.InsertBySql(
		"INSERT IGNORE INTO user_pinned_channel (uid, space_id, channel_id, channel_type, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		uid, spaceID, channelID, channelType, maxSort+1, time.Now(),
	).Exec()
	if err != nil {
		return err
	}

	// 检查是否实际插入（rows affected = 0 表示已存在）
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrPinnedAlreadyExists
	}

	return tx.Commit()
}

// Remove 移除置顶频道
func (d *PinnedDB) Remove(uid, spaceID, channelID string, channelType uint8) error {
	_, err := d.session.DeleteFrom("user_pinned_channel").
		Where("uid=? AND space_id=? AND channel_id=? AND channel_type=?", uid, spaceID, channelID, channelType).
		Exec()
	return err
}

// List 获取用户置顶频道列表
func (d *PinnedDB) List(uid, spaceID string) ([]*PinnedChannelModel, error) {
	var list []*PinnedChannelModel
	_, err := d.session.Select("channel_id", "channel_type", "sort_order").
		From("user_pinned_channel").
		Where("uid=? AND space_id=?", uid, spaceID).
		OrderBy("sort_order ASC").
		Load(&list)
	return list, err
}

// PinnedSortItem 排序项
type PinnedSortItem struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	SortOrder   int    `json:"sort_order"`
}

// UpdateSort 更新排序
func (d *PinnedDB) UpdateSort(uid, spaceID string, items []PinnedSortItem) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	for _, item := range items {
		_, err = tx.Update("user_pinned_channel").
			Set("sort_order", item.SortOrder).
			Where("uid=? AND space_id=? AND channel_id=? AND channel_type=?", uid, spaceID, item.ChannelID, item.ChannelType).
			Exec()
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RemoveByChannel 根据频道删除所有用户的置顶（用于频道删除/群解散时清理）
func (d *PinnedDB) RemoveByChannel(channelID string, channelType uint8) error {
	_, err := d.session.DeleteFrom("user_pinned_channel").
		Where("channel_id=? AND channel_type=?", channelID, channelType).
		Exec()
	return err
}

// RemoveByUIDSpaceChannel 删除用户在指定 Space 指定频道的置顶（用于退群时清理）
func (d *PinnedDB) RemoveByUIDSpaceChannel(uid, spaceID, channelID string, channelType uint8) error {
	_, err := d.session.DeleteFrom("user_pinned_channel").
		Where("uid=? AND space_id=? AND channel_id=? AND channel_type=?", uid, spaceID, channelID, channelType).
		Exec()
	return err
}

// RemoveByUIDAndChannel 删除用户在所有 Space 下指定频道的置顶（用于删好友时清理）
// 注意：此方法会跨 Space 删除，仅用于全局性操作（如删好友）
func (d *PinnedDB) RemoveByUIDAndChannel(uid, channelID string, channelType uint8) error {
	_, err := d.session.DeleteFrom("user_pinned_channel").
		Where("uid=? AND channel_id=? AND channel_type=?", uid, channelID, channelType).
		Exec()
	return err
}

// 全局 PinnedDB 实例，供其他模块调用清理方法
var globalPinnedDB *PinnedDB
var globalPinnedDBOnce sync.Once

// InitGlobalPinnedDB 初始化全局 PinnedDB（在 user 模块初始化时调用）
func InitGlobalPinnedDB(ctx *config.Context) {
	globalPinnedDBOnce.Do(func() {
		globalPinnedDB = NewPinnedDB(ctx)
	})
}

// RemovePinnedForUserInSpace 清理用户在指定 Space 指定频道的置顶（供其他模块调用）
func RemovePinnedForUserInSpace(uid, spaceID, channelID string, channelType uint8) {
	if globalPinnedDB == nil {
		return
	}
	if err := globalPinnedDB.RemoveByUIDSpaceChannel(uid, spaceID, channelID, channelType); err != nil {
		globalPinnedDB.Warn("清理用户置顶失败",
			zap.String("uid", uid),
			zap.String("spaceID", spaceID),
			zap.String("channelID", channelID),
			zap.Uint8("channelType", channelType),
			zap.Error(err))
	}
}

// RemovePinnedForUser 清理用户在所有 Space 下指定频道的置顶（供其他模块调用）
// 注意：此方法会跨 Space 删除，仅用于全局性操作（如删好友）
func RemovePinnedForUser(uid, channelID string, channelType uint8) {
	if globalPinnedDB == nil {
		return
	}
	if err := globalPinnedDB.RemoveByUIDAndChannel(uid, channelID, channelType); err != nil {
		globalPinnedDB.Warn("清理用户置顶失败",
			zap.String("uid", uid),
			zap.String("channelID", channelID),
			zap.Uint8("channelType", channelType),
			zap.Error(err))
	}
}

// RemovePinnedForChannel 清理频道的所有置顶（供其他模块调用）
func RemovePinnedForChannel(channelID string, channelType uint8) {
	if globalPinnedDB == nil {
		return
	}
	if err := globalPinnedDB.RemoveByChannel(channelID, channelType); err != nil {
		globalPinnedDB.Warn("清理频道置顶失败",
			zap.String("channelID", channelID),
			zap.Uint8("channelType", channelType),
			zap.Error(err))
	}
}
