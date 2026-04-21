package message

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
)

// TestAuthorizeMutualDelete 覆盖 issue #1063：
// 非 Person/Group 的频道类型必须默认拒绝（fail-closed）；CommunityTopic
// 需校验用户在父群中的身份。
func TestAuthorizeMutualDelete(t *testing.T) {
	const (
		me    = "uidA"
		other = "uidB"
	)

	tests := []struct {
		name                 string
		channelType          uint8
		fromUID              string
		loginUID             string
		isGroupMember        bool
		isGroupManager       bool
		isParentGroupMember  bool
		isParentGroupManager bool
		wantErr              bool
	}{
		{
			name:        "person 自己发的消息可删",
			channelType: common.ChannelTypePerson.Uint8(),
			fromUID:     me,
			loginUID:    me,
			wantErr:     false,
		},
		{
			name:        "person 别人发的消息不可删",
			channelType: common.ChannelTypePerson.Uint8(),
			fromUID:     other,
			loginUID:    me,
			wantErr:     true,
		},
		{
			name:          "group 成员可删自己消息",
			channelType:   common.ChannelTypeGroup.Uint8(),
			fromUID:       me,
			loginUID:      me,
			isGroupMember: true,
			wantErr:       false,
		},
		{
			name:          "group 已退群用户不可删自己历史消息",
			channelType:   common.ChannelTypeGroup.Uint8(),
			fromUID:       me,
			loginUID:      me,
			isGroupMember: false,
			wantErr:       true,
		},
		{
			name:           "group 管理员可删他人消息",
			channelType:    common.ChannelTypeGroup.Uint8(),
			fromUID:        other,
			loginUID:       me,
			isGroupMember:  true,
			isGroupManager: true,
			wantErr:        false,
		},
		{
			name:           "group 非管理员成员不可删他人消息",
			channelType:    common.ChannelTypeGroup.Uint8(),
			fromUID:        other,
			loginUID:       me,
			isGroupMember:  true,
			isGroupManager: false,
			wantErr:        true,
		},
		{
			name:                "community topic 非父群成员拒绝",
			channelType:         common.ChannelTypeCommunityTopic.Uint8(),
			fromUID:             me,
			loginUID:            me,
			isParentGroupMember: false,
			wantErr:             true,
		},
		{
			name:                "community topic 父群成员删自己消息允许",
			channelType:         common.ChannelTypeCommunityTopic.Uint8(),
			fromUID:             me,
			loginUID:            me,
			isParentGroupMember: true,
			wantErr:             false,
		},
		{
			name:                "community topic 父群成员不可删他人消息",
			channelType:         common.ChannelTypeCommunityTopic.Uint8(),
			fromUID:             other,
			loginUID:            me,
			isParentGroupMember: true,
			wantErr:             true,
		},
		{
			name:                 "community topic 父群管理员可删他人消息",
			channelType:          common.ChannelTypeCommunityTopic.Uint8(),
			fromUID:              other,
			loginUID:             me,
			isParentGroupMember:  true,
			isParentGroupManager: true,
			wantErr:              false,
		},
		{
			name:        "未知频道类型默认拒绝",
			channelType: 99,
			fromUID:     me,
			loginUID:    me,
			wantErr:     true,
		},
		{
			name:        "其他未覆盖频道类型默认拒绝",
			channelType: 4,
			fromUID:     me,
			loginUID:    me,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := authorizeMutualDelete(
				tt.channelType,
				tt.fromUID,
				tt.loginUID,
				tt.isGroupMember,
				tt.isGroupManager,
				tt.isParentGroupMember,
				tt.isParentGroupManager,
			)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestVerifyRevokeMessageID 覆盖 issue #1048：
// 用户传入的 message_id 必须与 clientMsgNo 反查到的 messageID 一致。
func TestVerifyRevokeMessageID(t *testing.T) {
	tests := []struct {
		name              string
		reqMessageID      string
		resolvedMessageID int64
		wantErr           bool
	}{
		{
			name:              "空 message_id 允许通过（老客户端兼容）",
			reqMessageID:      "",
			resolvedMessageID: 12345,
			wantErr:           false,
		},
		{
			name:              "一致则通过",
			reqMessageID:      "12345",
			resolvedMessageID: 12345,
			wantErr:           false,
		},
		{
			name:              "不一致则拒绝",
			reqMessageID:      "99999",
			resolvedMessageID: 12345,
			wantErr:           true,
		},
		{
			name:              "非数字 message_id 拒绝",
			reqMessageID:      "abc",
			resolvedMessageID: 12345,
			wantErr:           true,
		},
		{
			name:              "溢出 int64 范围拒绝",
			reqMessageID:      "99999999999999999999",
			resolvedMessageID: 12345,
			wantErr:           true,
		},
		{
			name:              "负数与正数不一致则拒绝",
			reqMessageID:      "-1",
			resolvedMessageID: 12345,
			wantErr:           true,
		},
		{
			name:              "前导零数值等价仍视为匹配",
			reqMessageID:      "012345",
			resolvedMessageID: 12345,
			wantErr:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyRevokeMessageID(tt.reqMessageID, tt.resolvedMessageID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
