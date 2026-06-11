package group

import (
	"fmt"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleGroupDisbandEvent_CleansThreadMembers 回归 YUJ-4185 P1-1：群解散原本
// 只 IMDelChannel 父群 + 清父群 pinned，不遍历子区 —— 子区频道仍存活、成员订阅未摘，
// 解散后成员仍能经子区频道收/拉历史（越权读）。修复后解散遍历所有非删除子区，
// 删除子区 IM 频道并清成员订阅 / 置顶。DB-level 断言：thread_member 被清空。
func TestHandleGroupDisbandEvent_CleansThreadMembers(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	s := svc.(*Service)
	f := New(s.ctx)
	ensureThreadTables(t, f)

	insertTestUsers(t, userDB, "db_owner", "db_member")

	spaceID := "space_disband"
	groupNo := "g_disband_thread"
	require.NoError(t, f.db.Insert(&Model{
		GroupNo: groupNo, Name: "disband-thread", Creator: "db_owner",
		SpaceID: spaceID, Status: 1,
	}))
	require.NoError(t, f.db.InsertMember(&MemberModel{
		GroupNo: groupNo, UID: "db_owner", Role: MemberRoleCreator,
		Status: 1, Version: 1, Vercode: fmt.Sprintf("%s@1", util.GenerUUID()),
	}))
	require.NoError(t, f.db.InsertMember(&MemberModel{
		GroupNo: groupNo, UID: "db_member", Role: MemberRoleCommon,
		Status: 1, Version: 1, Vercode: fmt.Sprintf("%s@1", util.GenerUUID()),
	}))

	// 两个子区（active + archived），都要被清理
	resActive, err := f.ctx.DB().InsertInto("thread").
		Columns("short_id", "group_no", "name", "creator_uid", "status", "version").
		Values("disband_active", groupNo, "active-sub", "db_owner", 1, 1).Exec()
	require.NoError(t, err)
	activeID, err := resActive.LastInsertId()
	require.NoError(t, err)
	resArchived, err := f.ctx.DB().InsertInto("thread").
		Columns("short_id", "group_no", "name", "creator_uid", "status", "version").
		Values("disband_archived", groupNo, "archived-sub", "db_owner", 2, 1).Exec()
	require.NoError(t, err)
	archivedID, err := resArchived.LastInsertId()
	require.NoError(t, err)

	for _, tid := range []int64{activeID, archivedID} {
		_, err = f.ctx.DB().InsertInto("thread_member").
			Columns("thread_id", "uid", "role", "version").
			Values(tid, "db_member", 0, 1).Exec()
		require.NoError(t, err)
	}

	payload := config.MsgGroupDisband{
		GroupNo:      groupNo,
		Operator:     "db_owner",
		OperatorName: "owner",
	}
	var commitErr error
	committed := false
	f.handleGroupDisbandEvent([]byte(util.ToJson(payload)), func(err error) {
		committed = true
		commitErr = err
	})
	require.True(t, committed, "handler 必须调用 commit")
	require.NoError(t, commitErr, "群解散处理不应报错")

	// 核心断言：所有子区成员记录被清理（active + archived）
	var postCount int
	_, err = f.ctx.DB().Select("count(*)").From("thread_member").
		Where("uid=? AND thread_id IN (SELECT id FROM thread WHERE group_no=?)", "db_member", groupNo).
		Load(&postCount)
	require.NoError(t, err)
	assert.Equal(t, 0, postCount, "群解散必须清理所有子区成员/订阅（YUJ-4185 P1-1）")
}
