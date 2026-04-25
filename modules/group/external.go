package group

import "github.com/Mininglamp-OSS/octo-lib/config"

// MsgGroupMemberScanJoinExt 在 config.MsgGroupMemberScanJoin 基础上扩展 is_external 字段，
// 供事件 handler 区分内部/外部成员加入时的系统消息文案。
type MsgGroupMemberScanJoinExt struct {
	config.MsgGroupMemberScanJoin
	IsExternal int `json:"is_external"`
}
