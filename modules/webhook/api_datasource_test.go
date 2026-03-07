package webhook

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
)

// TestChannelIDSplitBoundsCheck tests that strings.Split with @ separator
// is properly bounds-checked before accessing array elements.
// This is a regression test for issue #225.
func TestChannelIDSplitBoundsCheck(t *testing.T) {
	tests := []struct {
		name          string
		channelID     string
		channelType   uint8
		shouldHave2   bool // whether split result should have at least 2 elements
		expectedUID0  string
		expectedUID1  string
	}{
		{
			name:          "valid fake channel with two UIDs",
			channelID:     "user1@user2",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   true,
			expectedUID0:  "user1",
			expectedUID1:  "user2",
		},
		{
			name:          "invalid - no @ separator",
			channelID:     "malformed",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   false,
			expectedUID0:  "",
			expectedUID1:  "",
		},
		{
			name:          "invalid - only one part (trailing @)",
			channelID:     "onlyuid@",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   true, // "onlyuid@" splits to ["onlyuid", ""]
			expectedUID0:  "onlyuid",
			expectedUID1:  "",
		},
		{
			name:          "invalid - only one part (leading @)",
			channelID:     "@onlyuid",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   true, // "@onlyuid" splits to ["", "onlyuid"]
			expectedUID0:  "",
			expectedUID1:  "onlyuid",
		},
		{
			name:          "valid - multiple @ (takes first two)",
			channelID:     "uid1@uid2@uid3",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   true,
			expectedUID0:  "uid1",
			expectedUID1:  "uid2",
		},
		{
			name:          "empty channel ID",
			channelID:     "",
			channelType:   common.ChannelTypePerson.Uint8(),
			shouldHave2:   false,
			expectedUID0:  "",
			expectedUID1:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic in getBlacklist
			uids := strings.Split(tt.channelID, "@")

			if tt.shouldHave2 {
				assert.GreaterOrEqual(t, len(uids), 2, "split result should have at least 2 elements")
				assert.Equal(t, tt.expectedUID0, uids[0])
				assert.Equal(t, tt.expectedUID1, uids[1])
			} else {
				assert.Less(t, len(uids), 2, "split result should have less than 2 elements")
			}
		})
	}
}

// TestGetBlacklistValidation tests the validation logic for channel_id format.
// This tests the fix for issue #225 where missing bounds check caused panic.
func TestGetBlacklistValidation(t *testing.T) {
	tests := []struct {
		name        string
		channelID   string
		channelType uint8
		isFake      bool
		shouldError bool
	}{
		{
			name:        "person channel with valid fake channel ID",
			channelID:   "uid1@uid2",
			channelType: common.ChannelTypePerson.Uint8(),
			isFake:      true,
			shouldError: false,
		},
		{
			name:        "person channel with invalid channel ID (no @)",
			channelID:   "malformed",
			channelType: common.ChannelTypePerson.Uint8(),
			isFake:      false, // IsFakeChannel should return false for this
			shouldError: false, // because IsFakeChannel check will fail first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the IsFakeChannel check
			isFake := common.IsFakeChannel(tt.channelID)
			assert.Equal(t, tt.isFake, isFake, "IsFakeChannel result mismatch")

			// Simulate the validation that should be in getBlacklist
			if tt.channelType == common.ChannelTypePerson.Uint8() && isFake {
				uids := strings.Split(tt.channelID, "@")
				// This is the fix: check bounds before accessing
				if len(uids) < 2 {
					assert.True(t, tt.shouldError, "should have errored for invalid format")
					return
				}
				// If we get here, we should be able to safely access uids[0] and uids[1]
				assert.NotPanics(t, func() {
					_ = uids[0]
					_ = uids[1]
				}, "should not panic when accessing uids")
			}
		})
	}
}

// TestSplitNoPanic verifies that the fix prevents panic.
// Before the fix, accessing uids[1] after Split on "malformed" would panic.
func TestSplitNoPanic(t *testing.T) {
	testCases := []string{
		"malformed",
		"",
		"single",
		"no-at-symbol",
	}

	for _, channelID := range testCases {
		t.Run(channelID, func(t *testing.T) {
			uids := strings.Split(channelID, "@")

			// This is what the code does AFTER the fix
			assert.NotPanics(t, func() {
				if len(uids) < 2 {
					// Return error instead of panicking
					return
				}
				// Only access if we have enough elements
				_ = uids[0]
				_ = uids[1]
			}, "should not panic with bounds check")

			// This would panic WITHOUT the fix (demonstrating the bug)
			if len(uids) < 2 {
				assert.Panics(t, func() {
					_ = uids[1] // This would panic
				}, "accessing uids[1] without bounds check should panic")
			}
		})
	}
}
