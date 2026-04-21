package search

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

func TestCollectChannelIDs_ThreadMessage(t *testing.T) {
	tests := []struct {
		name            string
		messages        []*config.MessageResp
		expectGroupIDs  []string
		expectUIDs      []string
		expectFromUIDs  []string
		expectThreadMap map[string]string
	}{
		{
			name: "private_message",
			messages: []*config.MessageResp{
				{ChannelID: "uid_a", ChannelType: common.ChannelTypePerson.Uint8(), FromUID: "uid_b"},
			},
			expectGroupIDs:  []string{},
			expectUIDs:      []string{"uid_a"},
			expectFromUIDs:  []string{"uid_b"},
			expectThreadMap: map[string]string{},
		},
		{
			name: "group_message",
			messages: []*config.MessageResp{
				{ChannelID: "group123", ChannelType: common.ChannelTypeGroup.Uint8(), FromUID: "uid_a"},
			},
			expectGroupIDs:  []string{"group123"},
			expectUIDs:      []string{},
			expectFromUIDs:  []string{"uid_a"},
			expectThreadMap: map[string]string{},
		},
		{
			name: "thread_message_extracts_parent_group",
			messages: []*config.MessageResp{
				{ChannelID: "group123____2044239261124792320", ChannelType: common.ChannelTypeCommunityTopic.Uint8(), FromUID: "uid_a"},
			},
			expectGroupIDs:  []string{"group123"},
			expectUIDs:      []string{},
			expectFromUIDs:  []string{"uid_a"},
			expectThreadMap: map[string]string{"group123____2044239261124792320": "group123"},
		},
		{
			name: "thread_invalid_format_skipped",
			messages: []*config.MessageResp{
				{ChannelID: "no_separator", ChannelType: common.ChannelTypeCommunityTopic.Uint8(), FromUID: "uid_a"},
			},
			expectGroupIDs:  []string{},
			expectUIDs:      []string{},
			expectFromUIDs:  []string{"uid_a"},
			expectThreadMap: map[string]string{},
		},
		{
			name: "mixed_messages",
			messages: []*config.MessageResp{
				{ChannelID: "uid_x", ChannelType: common.ChannelTypePerson.Uint8(), FromUID: "uid_y"},
				{ChannelID: "grp1", ChannelType: common.ChannelTypeGroup.Uint8(), FromUID: "uid_z"},
				{ChannelID: "grp2____20441234", ChannelType: common.ChannelTypeCommunityTopic.Uint8(), FromUID: "uid_w"},
			},
			expectGroupIDs:  []string{"grp1", "grp2"},
			expectUIDs:      []string{"uid_x"},
			expectFromUIDs:  []string{"uid_y", "uid_z", "uid_w"},
			expectThreadMap: map[string]string{"grp2____20441234": "grp2"},
		},
		{
			name:            "empty_messages",
			messages:        []*config.MessageResp{},
			expectGroupIDs:  []string{},
			expectUIDs:      []string{},
			expectFromUIDs:  []string{},
			expectThreadMap: map[string]string{},
		},
		{
			name: "from_uid_empty_not_collected",
			messages: []*config.MessageResp{
				{ChannelID: "uid_a", ChannelType: common.ChannelTypePerson.Uint8(), FromUID: ""},
			},
			expectGroupIDs:  []string{},
			expectUIDs:      []string{"uid_a"},
			expectFromUIDs:  []string{},
			expectThreadMap: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupIDs, uids, fromUIDs, threadMap := collectChannelIDs(tt.messages)
			assert.Equal(t, tt.expectGroupIDs, groupIDs)
			assert.Equal(t, tt.expectUIDs, uids)
			assert.Equal(t, tt.expectFromUIDs, fromUIDs)
			assert.Equal(t, tt.expectThreadMap, threadMap)
		})
	}
}
