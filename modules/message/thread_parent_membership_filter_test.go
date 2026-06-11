package message

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/stretchr/testify/assert"
)

// fakeMembershipGroupService 是 group.IService 的最小替身，只实现 ExistMembersActive，
// 用来验证 YUJ-4185 P0-3 / P1-4 的子区父群成员过滤（active-only，排除黑名单）。
type fakeMembershipGroupService struct {
	group.IService
	memberOf map[string]bool // groupNo -> 当前 uid 是否仍是活跃成员（非黑名单）
	err      error
}

func (f *fakeMembershipGroupService) ExistMembersActive(groupNos []string, uid string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, 0, len(groupNos))
	for _, no := range groupNos {
		if f.memberOf[no] {
			out = append(out, no)
		}
	}
	return out, nil
}

func convV2(channelID string, ct uint8) *config.SyncUserConversationResp {
	return &config.SyncUserConversationResp{ChannelID: channelID, ChannelType: ct}
}

func chID(c *config.SyncUserConversationResp) string  { return c.ChannelID }
func chType(c *config.SyncUserConversationResp) uint8 { return c.ChannelType }

// TestFilterThreadConvsByParentMembership_DropsNonMemberThreads 验证被移除者
// （已不是父群成员）的子区会话被剔除，而仍是成员的子区 + 非子区会话保留。
func TestFilterThreadConvsByParentMembership_DropsNonMemberThreads(t *testing.T) {
	gs := &fakeMembershipGroupService{memberOf: map[string]bool{
		"gMember": true,  // 仍是成员
		"gKicked": false, // 已被移除
	}}
	ct := common.ChannelTypeCommunityTopic.Uint8()
	gt := common.ChannelTypeGroup.Uint8()
	pt := common.ChannelTypePerson.Uint8()

	convs := []*config.SyncUserConversationResp{
		convV2("gMember____th1", ct), // 保留：仍是父群成员
		convV2("gKicked____th2", ct), // 剔除：已被移除
		convV2("gMember", gt),        // 保留：群会话不受影响（GROUP 校验在别处）
		convV2("u_peer", pt),         // 保留：DM 不受影响
		convV2("bad-thread-id", ct),  // 剔除：channelID 解析失败 fail-closed
	}

	out := filterThreadConvsByParentMembership(convs, chID, chType, "uX", gs)

	got := make([]string, 0, len(out))
	for _, c := range out {
		got = append(got, c.ChannelID)
	}
	assert.ElementsMatch(t, []string{"gMember____th1", "gMember", "u_peer"}, got,
		"非父群成员的子区会话 + 非法 channelID 子区必须被剔除")
}

// TestFilterThreadConvsByParentMembership_FailClosedOnError 验证 ExistMembers 查询
// 失败时所有子区会话被 fail-closed 丢弃（非子区不受影响）。
func TestFilterThreadConvsByParentMembership_FailClosedOnError(t *testing.T) {
	gs := &fakeMembershipGroupService{err: errors.New("db down")}
	ct := common.ChannelTypeCommunityTopic.Uint8()
	gt := common.ChannelTypeGroup.Uint8()

	convs := []*config.SyncUserConversationResp{
		convV2("gAny____th1", ct),
		convV2("gAny", gt),
	}
	out := filterThreadConvsByParentMembership(convs, chID, chType, "uX", gs)
	got := make([]string, 0, len(out))
	for _, c := range out {
		got = append(got, c.ChannelID)
	}
	assert.ElementsMatch(t, []string{"gAny"}, got,
		"查询失败时子区会话 fail-closed 丢弃，非子区会话保留")
}

// TestFilterThreadConvsByParentMembership_NoThreadsPassthrough 无子区会话时原样返回。
func TestFilterThreadConvsByParentMembership_NoThreadsPassthrough(t *testing.T) {
	gs := &fakeMembershipGroupService{memberOf: map[string]bool{}}
	gt := common.ChannelTypeGroup.Uint8()
	convs := []*config.SyncUserConversationResp{convV2("g1", gt)}
	out := filterThreadConvsByParentMembership(convs, chID, chType, "uX", gs)
	assert.Len(t, out, 1)
}
