package botfather

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"

	// Ensure dependent modules register SQL migrations
	_ "github.com/Mininglamp-OSS/octo-server/modules/group"
	_ "github.com/Mininglamp-OSS/octo-server/modules/space"
	_ "github.com/Mininglamp-OSS/octo-server/modules/user"
)

// setupGroupTestEnv creates a test environment without double-registering routes.
// Returns the route handler, BotFather db session, and context.
func setupGroupTestEnv(t *testing.T) (http.Handler, *config.Context) {
	s, ctx := testutil.NewTestServer()
	// NewTestServer cleans tables on first call, subsequent calls reuse the same instance.
	// Each test creates unique data (unique group_no via UUID) so no cross-test pollution.
	return s.GetRoute(), ctx
}

// insertTestUser creates a user in the test database.
func insertTestUser(t *testing.T, ctx *config.Context, uid, name string) {
	// Use InsertBySql with IGNORE to avoid duplicate key errors across tests
	_, err := ctx.DB().InsertBySql(
		"INSERT IGNORE INTO user (uid, name, username, status, short_no, zone, phone) VALUES (?, ?, ?, 1, ?, '', '')",
		uid, name, uid, util.GenerUUID()[:8],
	).Exec()
	assert.NoError(t, err)
}

// insertTestBot creates a bot in the test database and returns the bot_token.
func insertTestBot(t *testing.T, ctx *config.Context, robotID, creatorUID string) string {
	botToken := "bf_" + robotID
	_, err := ctx.DB().InsertBySql(
		"INSERT IGNORE INTO robot (app_id, robot_id, username, token, version, status, creator_uid, description, bot_token, im_token_cache, bot_commands) VALUES (?, ?, ?, 'test_token', 1, 1, ?, 'test robot', ?, '', '[]')",
		robotID, robotID, robotID, creatorUID, botToken,
	).Exec()
	assert.NoError(t, err)
	return botToken
}

// Also insert into user table so bot is recognized as a user
func insertTestBotUser(t *testing.T, ctx *config.Context, robotID string) {
	// Insert bot as user with robot=1. Use INSERT IGNORE for idempotency.
	_, _ = ctx.DB().InsertBySql(
		"INSERT IGNORE INTO user (uid, name, username, status, robot, short_no, zone, phone) VALUES (?, ?, ?, 1, 1, ?, '', '')",
		robotID, robotID, robotID, util.GenerUUID()[:8],
	).Exec()
	// Also ensure robot flag is set if user already existed
	ctx.DB().UpdateBySql("UPDATE user SET robot=1 WHERE uid=?", robotID).Exec()
}

// botReq builds an HTTP request with Bot token authentication.
func botReq(method, path, botToken string, body interface{}) *http.Request {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader([]byte(util.ToJson(body)))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+botToken)
	return req
}

// doRequest executes a request and returns the recorder.
func doRequest(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// jsonResult unmarshals response body into a map.
func jsonResult(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	var result map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err, "response body: %s", w.Body.String())
	return result
}

const grpTestBotID = "grptest_bot"

// =====================================================================
// botGroupCreate
// =====================================================================

func TestBotGroupCreate_HappyPath(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	w := doRequest(handler, botReq("POST", "/v1/bot/createGroup", botToken, map[string]interface{}{
		"name":    "Test Group",
		"members": []string{"user_a", "user_b"},
		"creator": testutil.UID,
	}))

	t.Logf("Status: %d, Body: %s", w.Code, w.Body.String())
	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.NotEmpty(t, result["group_no"])
	assert.Equal(t, "Test Group", result["name"])
}

func TestBotGroupCreate_EmptyMembers(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestUser(t, ctx, testutil.UID, "owner")

	w := doRequest(handler, botReq("POST", "/v1/bot/createGroup", botToken, map[string]interface{}{
		"name":    "Empty",
		"members": []string{},
		"creator": testutil.UID,
	}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "members is required")
}

func TestBotGroupCreate_EmptyCreator(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, "user_a", "Alice")

	// creator 为空时默认 members[0] 为群主
	w := doRequest(handler, botReq("POST", "/v1/bot/createGroup", botToken, map[string]interface{}{
		"name":    "No Creator",
		"members": []string{"user_a"},
		"creator": "",
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.NotEmpty(t, result["group_no"])
}

func TestBotGroupCreate_AutoName(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	w := doRequest(handler, botReq("POST", "/v1/bot/createGroup", botToken, map[string]interface{}{
		"members": []string{"user_a"},
		"creator": testutil.UID,
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	name := result["name"].(string)
	assert.Contains(t, name, "owner")
}

// =====================================================================
// botGroupUpdate
// =====================================================================

// createGroupViaAPI creates a group and returns group_no.
func createGroupViaAPI(t *testing.T, handler http.Handler, botToken string, members []string) string {
	w := doRequest(handler, botReq("POST", "/v1/bot/createGroup", botToken, map[string]interface{}{
		"name":    "Test",
		"members": members,
		"creator": testutil.UID,
	}))
	t.Logf("createGroup: status=%d body=%s", w.Code, w.Body.String())
	assert.Equal(t, http.StatusOK, w.Code)
	return jsonResult(t, w)["group_no"].(string)
}

func TestBotGroupUpdate_HappyPath(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	// Bot 创建群后自动加入并成为 bot_admin，无需手动设置
	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("PUT", fmt.Sprintf("/v1/bot/groups/%s/info", groupNo), botToken, map[string]interface{}{
		"name": "Updated",
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

func TestBotGroupUpdate_NotMember(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	// 用另一个 Bot 来请求（它没参与建群，不在群内）
	otherBotID := "other_bot_update"
	otherBotToken := insertTestBot(t, ctx, otherBotID, testutil.UID)
	insertTestBotUser(t, ctx, otherBotID)

	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("PUT", fmt.Sprintf("/v1/bot/groups/%s/info", groupNo), otherBotToken, map[string]interface{}{
		"name": "Fail",
	}))

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBotGroupUpdate_BotAutoAdmin(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	// Bot 创建群后自动成为 bot_admin，无需手动设置即可更新
	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("PUT", fmt.Sprintf("/v1/bot/groups/%s/info", groupNo), botToken, map[string]interface{}{
		"name": "AutoAdmin Updated",
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

// =====================================================================
// botGroupMemberAdd
// =====================================================================

func TestBotGroupMemberAdd_HappyPath(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a", grpTestBotID})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/add", groupNo), botToken, map[string]interface{}{
		"members": []string{"user_b"},
	}))
	t.Logf("addMembers: status=%d body=%s", w.Code, w.Body.String())

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.Equal(t, float64(1), result["added"])
}

func TestBotGroupMemberAdd_Dedup(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a", grpTestBotID})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/add", groupNo), botToken, map[string]interface{}{
		"members": []string{"user_b", "user_b"},
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.Equal(t, float64(1), result["added"])
}

func TestBotGroupMemberAdd_NotMember(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	// 用另一个 Bot 来请求（它没参与建群，不在群内）
	otherBotID := "other_bot_add"
	otherBotToken := insertTestBot(t, ctx, otherBotID, testutil.UID)
	insertTestBotUser(t, ctx, otherBotID)

	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/add", groupNo), otherBotToken, map[string]interface{}{
		"members": []string{"user_b"},
	}))

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// =====================================================================
// botGroupMemberRemove
// =====================================================================

func TestBotGroupMemberRemove_HappyPath(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	// Bot 创建群后自动成为 bot_admin
	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a", "user_b"})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/remove", groupNo), botToken, map[string]interface{}{
		"members": []string{"user_b"},
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.Equal(t, float64(1), result["removed"])
}

func TestBotGroupMemberRemove_CannotRemoveCreator(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	// Bot 创建群后自动成为 bot_admin
	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/remove", groupNo), botToken, map[string]interface{}{
		"members": []string{testutil.UID},
	}))

	assert.Equal(t, http.StatusOK, w.Code)
	result := jsonResult(t, w)
	assert.Equal(t, float64(0), result["removed"])
}

func TestBotGroupMemberRemove_NotBotAdmin(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestBotUser(t, ctx, grpTestBotID)
	insertTestUser(t, ctx, testutil.UID, "owner")
	insertTestUser(t, ctx, "user_a", "Alice")

	// 用另一个 Bot（不在群内，没有 bot_admin 权限）
	otherBotID := "other_bot_rm"
	otherBotToken := insertTestBot(t, ctx, otherBotID, testutil.UID)
	insertTestBotUser(t, ctx, otherBotID)

	groupNo := createGroupViaAPI(t, handler, botToken, []string{"user_a"})

	w := doRequest(handler, botReq("POST", fmt.Sprintf("/v1/bot/groups/%s/members/remove", groupNo), otherBotToken, map[string]interface{}{
		"members": []string{"user_a"},
	}))

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// =====================================================================
// botSpaceMembers
// =====================================================================

func TestBotSpaceMembers_HappyPath(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	spaceID := "test_space_grp"
	ctx.DB().InsertInto("space").Columns("space_id", "name", "creator", "status").
		Values(spaceID, "Test Space", testutil.UID, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, grpTestBotID, 0, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, "user_a", 0, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, "user_b", 0, 1).Exec()

	w := doRequest(handler, botReq("GET", fmt.Sprintf("/v1/bot/space/members?space_id=%s", spaceID), botToken, nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var members []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &members)
	assert.GreaterOrEqual(t, len(members), 2)
}

func TestBotSpaceMembers_KeywordSearch(t *testing.T) {
	handler, ctx := setupGroupTestEnv(t)
	botToken := insertTestBot(t, ctx, grpTestBotID, testutil.UID)
	insertTestUser(t, ctx, "user_a", "Alice")
	insertTestUser(t, ctx, "user_b", "Bob")

	spaceID := "test_space_search"
	ctx.DB().InsertInto("space").Columns("space_id", "name", "creator", "status").
		Values(spaceID, "Search Space", testutil.UID, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, grpTestBotID, 0, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, "user_a", 0, 1).Exec()
	ctx.DB().InsertInto("space_member").Columns("space_id", "uid", "role", "status").
		Values(spaceID, "user_b", 0, 1).Exec()

	w := doRequest(handler, botReq("GET", fmt.Sprintf("/v1/bot/space/members?space_id=%s&keyword=Alice", spaceID), botToken, nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var members []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &members)
	assert.Equal(t, 1, len(members))
	assert.Equal(t, "Alice", members[0]["name"])
}
