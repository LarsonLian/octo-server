package search

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

func TestShouldIncludeGroupForSpace(t *testing.T) {
	tests := []struct {
		name          string
		groupSpaceID  string
		searchSpaceID string
		groupNo       string
		externalMap   map[string]string
		want          bool
	}{
		{"no_space_context_excludes_all", "spaceA", "", "g1", nil, false},
		{"no_space_context_excludes_groups_without_space", "", "", "g1", nil, false},
		{"same_space_included", "spaceA", "spaceA", "g1", nil, true},
		{"different_space_excluded", "spaceB", "spaceA", "g1", nil, false},
		{"group_without_space_excluded_when_filtering", "", "spaceA", "g1", nil, false},
		{"external_group_visible_in_source_space", "spaceA", "spaceB", "g1", map[string]string{"g1": "spaceB"}, true},
		{"external_group_hidden_in_unrelated_space", "spaceA", "spaceC", "g1", map[string]string{"g1": "spaceB"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIncludeGroupForSpace(tt.groupSpaceID, tt.searchSpaceID, tt.groupNo, tt.externalMap)
			assert.Equal(t, tt.want, got)
		})
	}
}

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
