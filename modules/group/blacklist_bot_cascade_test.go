package group

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- #354 Gap 2 · 拉黑级联 bot ---
//
// blacklist HTTP handler 在本测试环境里会被 IMBlacklistAdd 的前置检查挡住（无 IM 服务），
// 与 bot_cascade_test.go 中 QuitGroup_* 用例同因。此处按 handler 的真实序列在 DB 层组装：
// expandBlacklistTargetsWithOwnedBots → updateMembersStatus，再用 ExistMemberActive
// 验证门禁语义（#343/#345 加固线的同一查询）。

// TestQueryBotUIDsOwnedByUIDs_BasicMatch 验证拉黑级联 SQL 的命中口径：
// 只挑出「属于目标用户、robot.status=1、仍在群内」的 bot；孤儿/禁用/他人 bot 不命中。
func TestQueryBotUIDsOwnedByUIDs_BasicMatch(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2"},
		Name:    "blacklist-cascade-query",
	})
	require.NoError(t, err)
	groupNo := resp.GroupNo

	s := svc.(*Service)
	seedBotMember(t, s, groupNo, "bot_m1_x", "bm1x", "m1")
	seedBotMember(t, s, groupNo, "bot_m1_y", "bm1y", "m1")
	seedBotMember(t, s, groupNo, "bot_m2", "bm2", "m2")
	seedBotMember(t, s, groupNo, "bot_orphan", "borphan", "")
	// 禁用 bot：robot.status=0，不视为任何人的 bot
	seedBotMember(t, s, groupNo, "bot_disabled", "bdis", "m1")
	_, err = s.ctx.DB().UpdateBySql("UPDATE robot SET status=0 WHERE robot_id=?", "bot_disabled").Exec()
	require.NoError(t, err)

	uids, err := s.db.QueryBotUIDsOwnedByUIDs(groupNo, []string{"m1"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"bot_m1_x", "bot_m1_y"}, uids)

	// 多 owner 批量
	uids, err = s.db.QueryBotUIDsOwnedByUIDs(groupNo, []string{"m1", "m2"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"bot_m1_x", "bot_m1_y", "bot_m2"}, uids)

	// 无 bot 的 owner / 空参数 → 空结果，不报错
	uids, err = s.db.QueryBotUIDsOwnedByUIDs(groupNo, []string{"nobody"})
	require.NoError(t, err)
	assert.Empty(t, uids)
	uids, err = s.db.QueryBotUIDsOwnedByUIDs("", []string{"m1"})
	require.NoError(t, err)
	assert.Nil(t, uids)
	uids, err = s.db.QueryBotUIDsOwnedByUIDs(groupNo, nil)
	require.NoError(t, err)
	assert.Nil(t, uids)
}

// TestExpandBlacklistTargetsWithOwnedBots 验证目标扩展：用户本人 + 名下 bot，按原序去重；
// 无 bot 时原样返回。
func TestExpandBlacklistTargetsWithOwnedBots(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2"},
		Name:    "blacklist-cascade-expand",
	})
	require.NoError(t, err)
	groupNo := resp.GroupNo

	s := svc.(*Service)
	seedBotMember(t, s, groupNo, "bot_m1", "bm1", "m1")

	// m1 带 1 个 bot → 扩展为 [m1, bot_m1]
	out, err := expandBlacklistTargetsWithOwnedBots(s.db, groupNo, []string{"m1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "bot_m1"}, out)

	// bot 已显式在目标列表里 → 去重，不重复出现
	out, err = expandBlacklistTargetsWithOwnedBots(s.db, groupNo, []string{"m1", "bot_m1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "bot_m1"}, out)

	// 无 bot 的用户 → 原样返回
	out, err = expandBlacklistTargetsWithOwnedBots(s.db, groupNo, []string{"m2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m2"}, out)
}

// TestBlacklistCascade_BotBlockedByExistMemberActive 核心安全断言（#354 Gap 2）：
// 拉黑用户后其 bot 必须过不了 ExistMemberActive（子区读写门禁直接拒绝），
// 解除拉黑后对称恢复。模拟 blacklist handler 的 add / remove 两分支序列。
func TestBlacklistCascade_BotBlockedByExistMemberActive(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2"},
		Name:    "blacklist-cascade-gate",
	})
	require.NoError(t, err)
	groupNo := resp.GroupNo

	s := svc.(*Service)
	seedBotMember(t, s, groupNo, "bot_m1", "bm1", "m1")
	seedBotMember(t, s, groupNo, "bot_m2", "bm2", "m2") // 旁观者，不应被波及

	// --- add 分支：targets = m1 + 其 bot，status → Blacklist ---
	targets, err := expandBlacklistTargetsWithOwnedBots(s.db, groupNo, []string{"m1"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"m1", "bot_m1"}, targets)

	version, err := s.ctx.GenSeq(common.GroupMemberSeqKey)
	require.NoError(t, err)
	require.NoError(t, s.db.updateMembersStatus(version, groupNo, int(common.GroupMemberStatusBlacklist), targets))

	// 被拉黑用户和其 bot 都过不了 ExistMemberActive（旧行为：bot 仍 Normal → 旁路读口子）
	for _, uid := range []string{"m1", "bot_m1"} {
		ok, err := s.db.ExistMemberActive(uid, groupNo)
		require.NoError(t, err)
		assert.False(t, ok, "%s 拉黑后必须被 ExistMemberActive 拒绝（#354）", uid)
	}
	// 仍是成员（is_deleted=0），拉黑可逆
	for _, uid := range []string{"m1", "bot_m1"} {
		ok, err := s.db.ExistMember(uid, groupNo)
		require.NoError(t, err)
		assert.True(t, ok, "%s 拉黑只翻 status，不删成员", uid)
	}
	// 他人和他人的 bot 不受波及
	for _, uid := range []string{"m2", "bot_m2"} {
		ok, err := s.db.ExistMemberActive(uid, groupNo)
		require.NoError(t, err)
		assert.True(t, ok, "%s 与本次拉黑无关，不应被误伤", uid)
	}

	// --- remove 分支：同一扩展对称恢复，status → Normal ---
	targets, err = expandBlacklistTargetsWithOwnedBots(s.db, groupNo, []string{"m1"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"m1", "bot_m1"}, targets,
		"解除拉黑时扩展必须仍命中 Blacklist 状态的 bot（查询不过滤 status）")

	version, err = s.ctx.GenSeq(common.GroupMemberSeqKey)
	require.NoError(t, err)
	require.NoError(t, s.db.updateMembersStatus(version, groupNo, int(common.GroupMemberStatusNormal), targets))

	for _, uid := range []string{"m1", "bot_m1"} {
		ok, err := s.db.ExistMemberActive(uid, groupNo)
		require.NoError(t, err)
		assert.True(t, ok, "%s 解除拉黑后必须恢复 ExistMemberActive（对称恢复）", uid)
	}
}

// TestRemoveGroupMembers_ManagerKickCascadesBots 验证 #354 Gap 1b：manager 不再豁免，
// 被踢的管理员连同其拉入的 bot 一并带走；creator 仍不可被踢（保底豁免回归）。
func TestRemoveGroupMembers_ManagerKickCascadesBots(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "mgr", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"mgr", "m2"},
		Name:    "kick-manager-cascade",
	})
	require.NoError(t, err)
	groupNo := resp.GroupNo

	s := svc.(*Service)
	// mgr 升为管理员
	version, err := s.ctx.GenSeq(common.GroupMemberSeqKey)
	require.NoError(t, err)
	require.NoError(t, s.db.UpdateMembersToManager(groupNo, []string{"mgr"}, version))
	mgrMember, err := s.db.QueryMemberWithUID("mgr", groupNo)
	require.NoError(t, err)
	require.Equal(t, MemberRoleManager, mgrMember.Role)

	seedBotMember(t, s, groupNo, "bot_mgr", "bmgr", "mgr")
	seedBotMember(t, s, groupNo, "bot_m2", "bm2", "m2") // 不应被波及

	removeResp, err := svc.RemoveGroupMembers(&RemoveGroupMembersServiceReq{
		GroupNo:      groupNo,
		Members:      []string{"mgr"},
		OperatorUID:  testutil.UID,
		OperatorName: "creator",
	})
	require.NoError(t, err)

	// manager 本人 + 其 bot 一并移除（#354：manager 例外已去掉）
	assert.Equal(t, 2, removeResp.Removed)
	assert.ElementsMatch(t, []string{"mgr", "bot_mgr"}, removeResp.RemovedUIDs)

	for _, uid := range []string{"mgr", "bot_mgr"} {
		exist, err := s.db.ExistMember(uid, groupNo)
		require.NoError(t, err)
		assert.False(t, exist, "%s 应已随踢人级联离群（#354）", uid)
	}
	exist, err := s.db.ExistMember("bot_m2", groupNo)
	require.NoError(t, err)
	assert.True(t, exist, "他人 bot 不应被误伤")

	// creator 仍不可被踢（豁免保底回归）
	removeResp, err = svc.RemoveGroupMembers(&RemoveGroupMembersServiceReq{
		GroupNo:      groupNo,
		Members:      []string{testutil.UID},
		OperatorUID:  testutil.UID,
		OperatorName: "creator",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, removeResp.Removed, "creator 永远不可被踢")
}
