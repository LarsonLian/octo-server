package botfather

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/server"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/stretchr/testify/assert"

	// 导入依赖模块以确保迁移按正确顺序执行
	_ "github.com/Mininglamp-OSS/octo-server/modules/robot"
	_ "github.com/Mininglamp-OSS/octo-server/modules/thread"
	_ "github.com/Mininglamp-OSS/octo-server/modules/user"
)

// setupBotThreadTestData 创建 Bot Thread 测试所需的基础数据：robot + group + membership
func setupBotThreadTestData(t *testing.T) (s *server.Server, bf *BotFather, robotID, groupNo, botToken string) {
	t.Helper()
	s, bf = setupTestBotFather(t)

	robotID = "thread_test_bot"
	botToken = "bf_" + robotID
	ownerUID := "group_owner_001"

	createTestUser(t, bf, ownerUID, "群主")
	createTestRobot(t, bf, robotID, ownerUID, 0)

	// robot 也需要 user 记录（thread service 查 member name 时需要）
	_, err := bf.db.session.InsertInto("user").Columns(
		"uid", "name", "username", "short_no", "status", "robot",
	).Values(
		robotID, "TestBot", robotID, "sn_"+robotID, 1, 1,
	).Exec()
	assert.NoError(t, err)

	// 创建群
	groupNo = strings.ReplaceAll(util.GenerUUID(), "-", "")
	groupDB := group.NewDB(bf.ctx)
	err = groupDB.Insert(&group.Model{
		GroupNo: groupNo,
		Name:    "测试群",
		Creator: ownerUID,
		Status:  1,
		Version: 1,
	})
	assert.NoError(t, err)

	// 群主
	err = groupDB.InsertMember(&group.MemberModel{
		GroupNo: groupNo,
		UID:     ownerUID,
		Role:    group.MemberRoleCreator,
		Status:  1,
		Version: 1,
		Vercode: util.GenerUUID(),
	})
	assert.NoError(t, err)

	// bot 作为群成员
	err = groupDB.InsertMember(&group.MemberModel{
		GroupNo: groupNo,
		UID:     robotID,
		Role:    group.MemberRoleCommon,
		Status:  1,
		Version: 1,
		Vercode: util.GenerUUID(),
	})
	assert.NoError(t, err)

	return
}

// botRequest 发起 Bot API 请求
func botRequest(t *testing.T, s *server.Server, method, path, botToken string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req, _ = http.NewRequest(method, path, bytes.NewReader([]byte(util.ToJson(body))))
	} else {
		req, _ = http.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	w := httptest.NewRecorder()
	s.GetRoute().ServeHTTP(w, req)
	return w
}

// createBotThread 通过 Bot API 创建子区，返回 short_id
func createBotThread(t *testing.T, s *server.Server, groupNo, botToken, name string) string {
	t.Helper()
	w := botRequest(t, s, "POST", "/v1/bot/groups/"+groupNo+"/threads", botToken, map[string]interface{}{
		"name": name,
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	util.ReadJsonByByte(w.Body.Bytes(), &resp)
	return resp["short_id"].(string)
}

// ==================== 创建子区 ====================

func TestBotCreateThread(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	w := botRequest(t, s, "POST", "/v1/bot/groups/"+groupNo+"/threads", botToken, map[string]interface{}{
		"name": "Bot创建的子区",
	})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"name":"Bot创建的子区"`)
	assert.Contains(t, w.Body.String(), `"short_id"`)
	assert.Contains(t, w.Body.String(), `"channel_type":5`)
}

func TestBotCreateThread_NotGroupMember(t *testing.T) {
	s, bf, _, _, _ := setupBotThreadTestData(t)

	// 创建一个不在群内的 bot
	outsiderID := "outsider_bot"
	outsiderToken := "bf_" + outsiderID
	createTestRobot(t, bf, outsiderID, "group_owner_001", 0)

	w := botRequest(t, s, "POST", "/v1/bot/groups/"+"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"+"/threads", outsiderToken, map[string]interface{}{
		"name": "不应该创建成功",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not a member")
}

func TestBotCreateThread_EmptyName(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	w := botRequest(t, s, "POST", "/v1/bot/groups/"+groupNo+"/threads", botToken, map[string]interface{}{
		"name": "",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ==================== 列出子区 ====================

func TestBotListThreads(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	// 创建两个子区
	createBotThread(t, s, groupNo, botToken, "话题A")
	createBotThread(t, s, groupNo, botToken, "话题B")

	w := botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads", botToken, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"话题A"`)
	assert.Contains(t, w.Body.String(), `"话题B"`)
}

// ==================== 获取子区详情 ====================

func TestBotGetThread(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	shortID := createBotThread(t, s, groupNo, botToken, "详情测试")

	w := botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads/"+shortID, botToken, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"name":"详情测试"`)
	assert.Contains(t, w.Body.String(), `"short_id":"`+shortID+`"`)
}

func TestBotGetThread_InvalidShortID(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	w := botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads/invalid", botToken, nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid short_id format")
}

// ==================== 删除子区 ====================

func TestBotDeleteThread(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	shortID := createBotThread(t, s, groupNo, botToken, "待删除")

	w := botRequest(t, s, "DELETE", "/v1/bot/groups/"+groupNo+"/threads/"+shortID, botToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// 验证已被删除（获取详情应失败）
	w = botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads/"+shortID, botToken, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBotDeleteThread_NotCreator(t *testing.T) {
	s, bf, _, groupNo, botToken := setupBotThreadTestData(t)

	// 用另一个 bot 创建子区
	otherBotID := "other_bot"
	otherToken := "bf_" + otherBotID
	createTestRobot(t, bf, otherBotID, "group_owner_001", 0)
	_, err := bf.db.session.InsertInto("user").Columns(
		"uid", "name", "username", "short_no", "status", "robot",
	).Values(
		otherBotID, "OtherBot", otherBotID, "sn_"+otherBotID, 1, 1,
	).Exec()
	assert.NoError(t, err)

	groupDB := group.NewDB(bf.ctx)
	err = groupDB.InsertMember(&group.MemberModel{
		GroupNo: groupNo,
		UID:     otherBotID,
		Role:    group.MemberRoleCommon,
		Status:  1,
		Version: 1,
		Vercode: util.GenerUUID(),
	})
	assert.NoError(t, err)

	// other_bot 创建子区
	shortID := createBotThread(t, s, groupNo, otherToken, "别人的子区")

	// thread_test_bot 尝试删除（非创建者、非管理员）
	w := botRequest(t, s, "DELETE", "/v1/bot/groups/"+groupNo+"/threads/"+shortID, botToken, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ==================== 成员列表 ====================

func TestBotListThreadMembers(t *testing.T) {
	s, _, _, groupNo, botToken := setupBotThreadTestData(t)

	shortID := createBotThread(t, s, groupNo, botToken, "成员测试")

	w := botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads/"+shortID+"/members", botToken, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	// 创建者自动成为成员
	assert.Contains(t, w.Body.String(), `"thread_test_bot"`)
}

// ==================== 加入/离开子区 ====================

func TestBotJoinAndLeaveThread(t *testing.T) {
	s, bf, _, groupNo, botToken := setupBotThreadTestData(t)

	// 用另一个 bot 创建子区
	otherBotID := "join_test_bot"
	otherToken := "bf_" + otherBotID
	createTestRobot(t, bf, otherBotID, "group_owner_001", 0)
	_, err := bf.db.session.InsertInto("user").Columns(
		"uid", "name", "username", "short_no", "status", "robot",
	).Values(
		otherBotID, "JoinTestBot", otherBotID, "sn_"+otherBotID, 1, 1,
	).Exec()
	assert.NoError(t, err)

	groupDB := group.NewDB(bf.ctx)
	err = groupDB.InsertMember(&group.MemberModel{
		GroupNo: groupNo,
		UID:     otherBotID,
		Role:    group.MemberRoleCommon,
		Status:  1,
		Version: 1,
		Vercode: util.GenerUUID(),
	})
	assert.NoError(t, err)

	shortID := createBotThread(t, s, groupNo, otherToken, "加入离开测试")

	// thread_test_bot 加入子区
	w := botRequest(t, s, "POST", "/v1/bot/groups/"+groupNo+"/threads/"+shortID+"/join", botToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// 验证成员列表包含两个 bot
	w = botRequest(t, s, "GET", "/v1/bot/groups/"+groupNo+"/threads/"+shortID+"/members", botToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"thread_test_bot"`)
	assert.Contains(t, w.Body.String(), `"join_test_bot"`)

	// thread_test_bot 离开子区
	w = botRequest(t, s, "POST", "/v1/bot/groups/"+groupNo+"/threads/"+shortID+"/leave", botToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ==================== 认证测试 ====================

func TestBotThreadAPI_Unauthorized(t *testing.T) {
	s, _, _, groupNo, _ := setupBotThreadTestData(t)

	// 无 token
	req, _ := http.NewRequest("GET", "/v1/bot/groups/"+groupNo+"/threads", nil)
	w := httptest.NewRecorder()
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
