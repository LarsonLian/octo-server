package channel

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

func TestFormatSecondToDisplayTime(t *testing.T) {
	tests := []struct {
		name   string
		second int64
		want   string
	}{
		// 秒级别
		{"0 seconds", 0, "0秒"},
		{"1 second", 1, "1秒"},
		{"30 seconds", 30, "30秒"},
		{"59 seconds", 59, "59秒"},

		// 分钟级别
		{"1 minute", 60, "1分钟"},
		{"2 minutes", 120, "2分钟"},
		{"30 minutes", 1800, "30分钟"},
		{"59 minutes", 3540, "59分钟"},

		// 小时级别
		{"1 hour", 3600, "1小时"},
		{"2 hours", 7200, "2小时"},
		{"23 hours", 82800, "23小时"},

		// 天级别
		{"1 day", 86400, "1天"},
		{"7 days", 604800, "7天"},
		{"29 days", 2505600, "29天"},

		// 月级别
		{"1 month", 2592000, "1月"},
		{"6 months", 15552000, "6月"},
		{"11 months", 28512000, "11月"},

		// 年级别
		{"1 year", 31104000, "1年"},
		{"2 years", 62208000, "2年"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSecondToDisplayTime(tt.second)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatSecondToDisplayTime_Boundaries(t *testing.T) {
	// 测试边界值
	// 59秒 → 秒
	assert.Equal(t, "59秒", formatSecondToDisplayTime(59))
	// 60秒 → 分钟
	assert.Equal(t, "1分钟", formatSecondToDisplayTime(60))
	// 3599秒 → 分钟
	assert.Equal(t, "59分钟", formatSecondToDisplayTime(3599))
	// 3600秒 → 小时
	assert.Equal(t, "1小时", formatSecondToDisplayTime(3600))
	// 86399秒 → 小时
	assert.Equal(t, "23小时", formatSecondToDisplayTime(86399))
	// 86400秒 → 天
	assert.Equal(t, "1天", formatSecondToDisplayTime(86400))
}

// TestClearChannelMessages_PersonChannel_NoPermission tests that non-friend users cannot clear personal channel messages
func TestClearChannelMessages_PersonChannel_NoPermission(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	// Note: testutil.NewTestServer() already registers all module routes via module.Setup()

	// Create test user (the target of the personal channel)
	targetUID := "20001"
	userService := user.NewService(ctx)
	err := userService.AddUser(&user.AddUserReq{
		UID:  targetUID,
		Name: "Target User",
	})
	assert.NoError(t, err)

	// Create login user
	err = userService.AddUser(&user.AddUserReq{
		UID:  testutil.UID,
		Name: "Login User",
	})
	assert.NoError(t, err)

	// Note: We do NOT add friend relationship here
	// Try to clear personal channel messages without being friends
	w := httptest.NewRecorder()
	channelType := common.ChannelTypePerson.Uint8()
	req, err := http.NewRequest("POST",
		"/v1/channels/"+targetUID+"/"+fmt.Sprintf("%d", channelType)+"/message/clear",
		bytes.NewReader([]byte("{}")))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)

	s.GetRoute().ServeHTTP(w, req)

	// Should return error because not friends
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "没有权限操作此频道")
}

// TestClearChannelMessages_PersonChannel_SelfChannel tests that users cannot clear their own channel
func TestClearChannelMessages_PersonChannel_SelfChannel(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	// Note: testutil.NewTestServer() already registers all module routes via module.Setup()

	// Create login user
	userService := user.NewService(ctx)
	err := userService.AddUser(&user.AddUserReq{
		UID:  testutil.UID,
		Name: "Login User",
	})
	assert.NoError(t, err)

	// Try to clear personal channel with self as target (loginUID == channelID)
	w := httptest.NewRecorder()
	channelType := common.ChannelTypePerson.Uint8()
	req, err := http.NewRequest("POST",
		"/v1/channels/"+testutil.UID+"/"+fmt.Sprintf("%d", channelType)+"/message/clear",
		bytes.NewReader([]byte("{}")))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)

	s.GetRoute().ServeHTTP(w, req)

	// Should return error because channelID == loginUID
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "频道ID不合法")
}
