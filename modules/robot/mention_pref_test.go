package robot

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
)

// TestDecideOwnership covers the creator-ownership decision used by the
// owner-scoped mention_pref endpoints (octo-server#237). The ownership reject
// paths (404 not-found, 403 forbidden) are the acceptance-required coverage.
func TestDecideOwnership(t *testing.T) {
	cases := []struct {
		name       string
		creatorUID string
		loginUID   string
		want       ownershipResult
	}{
		{"creator matches → OK", "owner_1", "owner_1", ownershipOK},
		{"robot missing (empty creator) → 404", "", "owner_1", ownershipNotFound},
		{"empty creator + empty login → 404", "", "", ownershipNotFound},
		{"different user → 403", "owner_1", "intruder_2", ownershipForbidden},
		{"creator set, login empty → 403", "owner_1", "", ownershipForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideOwnership(tc.creatorUID, tc.loginUID))
		})
	}
}

// TestClampGroupsLimit verifies default (30), upper cap (100), and floor handling.
func TestClampGroupsLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", groupsListDefaultLimit},
		{"abc", groupsListDefaultLimit},
		{"0", groupsListDefaultLimit},
		{"-5", groupsListDefaultLimit},
		{"15", 15},
		{"30", 30},
		{"100", 100},
		{"101", groupsListMaxLimit},
		{"99999", groupsListMaxLimit},
		{"  20  ", 20},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			assert.Equal(t, tc.want, clampGroupsLimit(tc.raw))
		})
	}
}

// TestGroupsCursorRoundTrip verifies the opaque cursor encodes/decodes and that
// blank/garbage decodes to 0 (first page) — keeping the cursor opaque to clients.
func TestGroupsCursorRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 42, 1_000_000, 9_223_372_036_854_775_807} {
		enc := encodeGroupsCursor(id)
		assert.NotEqual(t, "", enc)
		assert.Equal(t, id, decodeGroupsCursor(enc))
	}

	// Blank and non-base64 / non-numeric inputs fall back to 0 (first page).
	assert.Equal(t, int64(0), decodeGroupsCursor(""))
	assert.Equal(t, int64(0), decodeGroupsCursor("   "))
	assert.Equal(t, int64(0), decodeGroupsCursor("!!!not-base64!!!"))
	// Valid base64 of a non-numeric string also falls back to 0.
	assert.Equal(t, int64(0), decodeGroupsCursor("YWJj")) // base64("abc")
}

// TestBuildMentionPrefPayload verifies the mention_pref_updated event payload
// the owner write/delete endpoints push to the adapter (octo-server#242). The
// adapter keys cache invalidation off event.type + event.group_no + the
// message channel, and the bot only receives it via mention.uids — so those
// fields are the contract under test.
func TestBuildMentionPrefPayload(t *testing.T) {
	for _, noMention := range []int{0, 1} {
		p := buildMentionPrefPayload("bot_42", "g_100", noMention)

		// Top-level type is Text so it rides the same group-message path as
		// GROUP.md events.
		assert.Equal(t, common.Text, p["type"])

		event, ok := p["event"].(map[string]interface{})
		assert.True(t, ok, "event must be a map")
		assert.Equal(t, "mention_pref_updated", event["type"])
		assert.Equal(t, "g_100", event["group_no"])
		assert.Equal(t, noMention, event["no_mention"])

		// Targeted (non-broadcast): only the affected bot is mentioned, so the
		// robot dispatcher routes the event to that bot's queue alone.
		mention, ok := p["mention"].(map[string]interface{})
		assert.True(t, ok, "mention must be a map")
		assert.Equal(t, []string{"bot_42"}, mention["uids"])
	}
}
