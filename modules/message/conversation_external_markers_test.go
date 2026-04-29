package message

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/stretchr/testify/assert"
)

// YUJ-98 / YUJ-101: 会话同步 Recents 里的群消息必须回填 msg-level 外部字段，
// 保持与 /message/channel/sync 口径一致。

// fakeExternalGroupService 是对 group.IService 的最小化替身，只实现
// enrichConversationExternalMarkers 依赖的 GetMemberExternalMarkers。
type fakeExternalGroupService struct {
	group.IService
	markersByGroup map[string]map[string]group.MemberExternalMarker
	err            error
}

func (f *fakeExternalGroupService) GetMemberExternalMarkers(groupNo string) (map[string]group.MemberExternalMarker, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.markersByGroup[groupNo], nil
}

func newConversationForTest(gs group.IService) *Conversation {
	return &Conversation{
		Log:          log.NewTLog("conversation-external-markers-test"),
		groupService: gs,
	}
}

// Recents 里的群消息回填 from_is_external / from_source_space_name /
// from_home_space_id / from_home_space_name —— 与 /message/channel/sync 一致。
func TestEnrichConversationExternalMarkers_GroupRecentsPopulated(t *testing.T) {
	gs := &fakeExternalGroupService{
		markersByGroup: map[string]map[string]group.MemberExternalMarker{
			"G_EXT": {
				"ext-uid": {
					IsExternal:      1,
					SourceSpaceName: "ExampleCorp",
					HomeSpaceID:     "space_example",
					HomeSpaceName:   "ExampleCorp",
				},
				"internal-uid": {
					IsExternal:    0,
					HomeSpaceID:   "space_group_owner",
					HomeSpaceName: "GroupOwnerSpace",
				},
			},
		},
	}
	co := newConversationForTest(gs)

	resps := []*SyncUserConversationResp{
		{
			ChannelID:   "G_EXT",
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Recents: []*MsgSyncResp{
				{FromUID: "ext-uid"},
				{FromUID: "internal-uid"},
			},
		},
	}

	co.enrichConversationExternalMarkers(resps)

	assert.Equal(t, 1, resps[0].Recents[0].FromIsExternal)
	assert.Equal(t, "ExampleCorp", resps[0].Recents[0].FromSourceSpaceName)
	assert.Equal(t, "space_example", resps[0].Recents[0].FromHomeSpaceID)
	assert.Equal(t, "ExampleCorp", resps[0].Recents[0].FromHomeSpaceName)

	assert.Equal(t, 0, resps[0].Recents[1].FromIsExternal)
	assert.Equal(t, "", resps[0].Recents[1].FromSourceSpaceName)
	assert.Equal(t, "space_group_owner", resps[0].Recents[1].FromHomeSpaceID)
	assert.Equal(t, "GroupOwnerSpace", resps[0].Recents[1].FromHomeSpaceName)
}

// Person / Thread 频道不应触发群成员查询，避免 N+1 + 无意义 SQL。
func TestEnrichConversationExternalMarkers_PersonChannelSkipped(t *testing.T) {
	called := 0
	gs := &stubCountingGroupService{
		IService: (group.IService)(nil),
		onCall: func(groupNo string) (map[string]group.MemberExternalMarker, error) {
			called++
			return nil, nil
		},
	}
	co := newConversationForTest(gs)

	resps := []*SyncUserConversationResp{
		{
			ChannelID:   "peer-uid",
			ChannelType: common.ChannelTypePerson.Uint8(),
			Recents:     []*MsgSyncResp{{FromUID: "peer-uid"}},
		},
	}

	co.enrichConversationExternalMarkers(resps)

	assert.Equal(t, 0, called,
		"Person 会话不应查询群成员外部来源标识")
	assert.Equal(t, 0, resps[0].Recents[0].FromIsExternal,
		"Person 会话的消息不应被注入外部字段")
	assert.Equal(t, "", resps[0].Recents[0].FromHomeSpaceID)
}

// GetMemberExternalMarkers 返回错误时降级跳过，不阻断整个 sync 响应。
func TestEnrichConversationExternalMarkers_ErrorDegradesGracefully(t *testing.T) {
	gs := &fakeExternalGroupService{
		err: errors.New("db down"),
	}
	co := newConversationForTest(gs)

	resps := []*SyncUserConversationResp{
		{
			ChannelID:   "G_EXT",
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Recents:     []*MsgSyncResp{{FromUID: "ext-uid"}},
		},
	}

	assert.NotPanics(t, func() {
		co.enrichConversationExternalMarkers(resps)
	})
	// Recents 内消息保留默认值，未被污染
	assert.Equal(t, 0, resps[0].Recents[0].FromIsExternal)
	assert.Equal(t, "", resps[0].Recents[0].FromHomeSpaceID)
}

// 空输入 / Recents 为空时必须静默返回。
func TestEnrichConversationExternalMarkers_NoopCases(t *testing.T) {
	co := newConversationForTest(&fakeExternalGroupService{})

	// 空 slice
	co.enrichConversationExternalMarkers(nil)
	co.enrichConversationExternalMarkers([]*SyncUserConversationResp{})

	// 群会话但 Recents 为空 → 不触发 GetMemberExternalMarkers
	co.enrichConversationExternalMarkers([]*SyncUserConversationResp{
		{
			ChannelID:   "G_EXT",
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Recents:     nil,
		},
	})
}

// stubCountingGroupService 可以数 GetMemberExternalMarkers 调用次数。
type stubCountingGroupService struct {
	group.IService
	onCall func(groupNo string) (map[string]group.MemberExternalMarker, error)
}

func (s *stubCountingGroupService) GetMemberExternalMarkers(groupNo string) (map[string]group.MemberExternalMarker, error) {
	if s.onCall == nil {
		return nil, nil
	}
	return s.onCall(groupNo)
}
