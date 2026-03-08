package space

import "testing"

func TestBuildChannelID(t *testing.T) {
	tests := []struct {
		spaceID, peerID, want string
	}{
		{"", "user123", "user123"},
		{"sp1", "user123", "ssp1_user123"},
		{"42", "bot_abc", "s42_bot_abc"},
		{"minglue_default", "botfather", "sminglue_default_botfather"},
	}
	for _, tt := range tests {
		got := BuildChannelID(tt.spaceID, tt.peerID)
		if got != tt.want {
			t.Errorf("BuildChannelID(%q, %q) = %q, want %q", tt.spaceID, tt.peerID, got, tt.want)
		}
	}
}

func TestParseChannelID(t *testing.T) {
	// 注册已知 spaceId
	RegisterSpaceIDs([]string{"sp1", "42", "minglue_default", "myspace"})

	tests := []struct {
		channelID, wantSpace, wantPeer string
	}{
		{"user123", "", "user123"},
		{"ssp1_user123", "sp1", "user123"},
		{"s42_bot_abc", "42", "bot_abc"},
		{"notspace", "", "notspace"},
		// P0 bug: 含下划线的 spaceId
		{"sminglue_default_botfather", "minglue_default", "botfather"},
		{"sminglue_default_test_1_bot", "minglue_default", "test_1_bot"},
		{"sminglue_default_xuhao", "minglue_default", "xuhao"},
	}
	for _, tt := range tests {
		gotSpace, gotPeer := ParseChannelID(tt.channelID)
		if gotSpace != tt.wantSpace || gotPeer != tt.wantPeer {
			t.Errorf("ParseChannelID(%q) = (%q, %q), want (%q, %q)",
				tt.channelID, gotSpace, gotPeer, tt.wantSpace, tt.wantPeer)
		}
	}
}

func TestParseChannelID_Fallback(t *testing.T) {
	// 清空已知 spaceId，测试 LastIndex 回退
	RegisterSpaceIDs(nil)

	// 无已知列表时用 LastIndex 回退
	gotSpace, gotPeer := ParseChannelID("sunknown_user123")
	if gotSpace != "unknown" || gotPeer != "user123" {
		t.Errorf("fallback: got (%q, %q), want (%q, %q)", gotSpace, gotPeer, "unknown", "user123")
	}
}

func TestRoundTrip(t *testing.T) {
	RegisterSpaceIDs([]string{"myspace", "minglue_default"})

	cases := []struct{ spaceID, peerID string }{
		{"myspace", "user456"},
		{"minglue_default", "botfather"},
		{"minglue_default", "test_1_bot"},
	}
	for _, tt := range cases {
		channelID := BuildChannelID(tt.spaceID, tt.peerID)
		gotSpace, gotPeer := ParseChannelID(channelID)
		if gotSpace != tt.spaceID || gotPeer != tt.peerID {
			t.Errorf("roundtrip(%q, %q) failed: channelID=%q, got (%q, %q)",
				tt.spaceID, tt.peerID, channelID, gotSpace, gotPeer)
		}
	}
}
