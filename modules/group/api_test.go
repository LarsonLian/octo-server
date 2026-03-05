package group

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGroupCreate(t *testing.T) {

	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := f.userDB.Insert(&user.Model{
		UID:  "10009",
		Name: "张九",
	})
	assert.NoError(t, err)
	err = f.userDB.Insert(&user.Model{
		UID:  "10010",
		Name: "李十",
	})
	assert.NoError(t, err)
	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/group/create", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"name":    "群组1",
		"members": []string{"10009", "10010"},
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"name":"群组1"`)
	time.Sleep(time.Millisecond * 200)
}

func TestGroupGet(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo:            "1",
		Name:               "test",
		Creator:            testutil.UID,
		Version:            1,
		Status:             1,
		ForbiddenAddFriend: 1,
	})
	assert.NoError(t, err)
	err = f.settingDB.InsertSetting(&Setting{
		GroupNo:         "1",
		UID:             "10000",
		Mute:            1,
		Save:            1,
		ShowNick:        1,
		Top:             1,
		ChatPwdOn:       1,
		JoinGroupRemind: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/groups/1", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"group_no":"1"`, `"name":"test"`, `"chat_pwd":1`, `"mute":1`, `"top":1`, `"show_nick":1`, `"save":1`)

	time.Sleep(time.Millisecond * 200)
}

func TestGroupMemberAdd(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "10009",
		Name: "张九",
	})
	assert.NoError(t, err)
	err = f.userDB.Insert(&user.Model{
		UID:  "10010",
		Name: "李十",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/groups/1/members", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"members": []string{"10009", "10010"},
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

}

func TestGroupMemberRemove(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "10009",
		Name: "张九",
	})
	assert.NoError(t, err)
	err = f.userDB.Insert(&user.Model{
		UID:  "10010",
		Name: "李十",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("DELETE", "/v1/groups/1/members", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"members": []string{"10009", "10010"},
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

}

func TestSyncMembers(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "10009",
		Name: "张九",
	})
	assert.NoError(t, err)
	err = f.userDB.Insert(&user.Model{
		UID:  "10010",
		Name: "李十",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "1",
		UID:     "10009",
		Version: 2,
	})
	assert.NoError(t, err)
	err = f.db.InsertMember(&MemberModel{
		GroupNo: "1",
		UID:     "10010",
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/groups/1/membersync?version=1", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	b := w.Body.String()
	assert.Contains(t, b, `"uid":"10009"`)
	assert.NotContains(t, b, `"uid":"10010"`)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGroupSettingUpdate(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)
	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Version: 1,
		Status:  1,
	})
	assert.NoError(t, err)
	err = f.db.InsertMember(&MemberModel{
		UID:     testutil.UID,
		GroupNo: "1",
		Role:    1,
	})
	assert.NoError(t, err)
	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/groups/1/setting", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"mute":      1,
		"top":       1,
		"save":      1,
		"show_nick": 1,
		"chat_pwd":  1,
		"forbidden": 1,
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGroupUpdate(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Version: 1,
		Status:  1,
	})
	assert.NoError(t, err)
	err = f.db.InsertMember(&MemberModel{
		GroupNo: "1",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/groups/1", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"name": "test2",
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

}
func TestList(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 先清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "1",
		Name:    "test",
		Creator: testutil.UID,
		Version: 1,
		Status:  1,
	})
	assert.NoError(t, err)
	err = f.settingDB.InsertSetting(&Setting{
		UID:     testutil.UID,
		GroupNo: "1",
		Save:    1,
	})
	assert.NoError(t, err)
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/group/my", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, true, strings.Contains(w.Body.String(), `"group_no":`))
	assert.Equal(t, true, strings.Contains(w.Body.String(), `"name":`))

}

// TestGroupExit 测试退出群聊
func TestGroupExit(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建群和成员
	err = f.db.Insert(&Model{
		GroupNo: "exit_group",
		Name:    "exit test",
		Creator: "creator_uid",
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "exit_group",
		UID:     testutil.UID,
		Role:    MemberRoleCommon,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/groups/exit_group/exit", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGroupDisband 测试解散群组
func TestGroupDisband(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "disband_group",
		Name:    "disband test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "disband_group",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("DELETE", "/v1/groups/disband_group", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGroupManagerAdd 测试添加管理员
func TestGroupManagerAdd(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "new_manager",
		Name: "新管理员",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "mgr_group",
		Name:    "manager test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "mgr_group",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
		Version: 1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "mgr_group",
		UID:     "new_manager",
		Role:    MemberRoleCommon,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/groups/mgr_group/managers", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"members": []string{"new_manager"},
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGroupManagerRemove 测试移除管理员
func TestGroupManagerRemove(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "mgr_to_remove",
		Name: "待移除管理员",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "mgr_group2",
		Name:    "manager remove test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "mgr_group2",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
		Version: 1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "mgr_group2",
		UID:     "mgr_to_remove",
		Role:    MemberRoleManager,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("DELETE", "/v1/groups/mgr_group2/managers", bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
		"members": []string{"mgr_to_remove"},
	}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGroupTransfer 测试群主转让
func TestGroupTransfer(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{
		UID:  "new_owner",
		Name: "新群主",
	})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "transfer_group",
		Name:    "transfer test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "transfer_group",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
		Version: 1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "transfer_group",
		UID:     "new_owner",
		Role:    MemberRoleCommon,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/groups/transfer_group/transfer/new_owner", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGroupForbidden 测试群组全员禁言
func TestGroupForbidden(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "forbidden_group",
		Name:    "forbidden test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "forbidden_group",
		UID:     testutil.UID,
		Role:    MemberRoleCreator,
		Version: 1,
	})
	assert.NoError(t, err)

	// 开启全员禁言
	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/v1/groups/forbidden_group/forbidden/1", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 关闭全员禁言
	w2 := httptest.NewRecorder()
	req2, err := http.NewRequest("POST", "/v1/groups/forbidden_group/forbidden/0", nil)
	req2.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

// TestGroupMembersGet 测试获取群成员列表
func TestGroupMembersGet(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	err = f.userDB.Insert(&user.Model{UID: "member1", Name: "成员一"})
	assert.NoError(t, err)
	err = f.userDB.Insert(&user.Model{UID: "member2", Name: "成员二"})
	assert.NoError(t, err)

	err = f.db.Insert(&Model{
		GroupNo: "members_group",
		Name:    "get members test",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	err = f.db.InsertMember(&MemberModel{
		GroupNo: "members_group",
		UID:     "member1",
		Role:    MemberRoleCommon,
		Version: 1,
	})
	assert.NoError(t, err)
	err = f.db.InsertMember(&MemberModel{
		GroupNo: "members_group",
		UID:     "member2",
		Role:    MemberRoleCommon,
		Version: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/groups/members_group/members", nil)
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"uid":"member1"`)
	assert.Contains(t, w.Body.String(), `"uid":"member2"`)
}
