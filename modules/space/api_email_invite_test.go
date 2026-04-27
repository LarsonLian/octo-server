package space

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

// seedSpaceWithMemberRole 在空间里塞入 testutil.UID 成员并指定 role。
func seedSpaceWithMemberRole(t *testing.T, spaceId, creator string, callerRole int) {
	t.Helper()
	err := testSpaceDB.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "测试空间",
		Creator: creator,
		Status:  SpaceStatusNormal,
	})
	assert.NoError(t, err)
	// creator 作为 owner
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId, UID: creator, Role: 2, Status: 1,
	})
	assert.NoError(t, err)
	// 如果 callerUID 不是 creator，加入 callerRole
	if creator != testutil.UID {
		err = testSpaceDB.insertMemberNoTx(&MemberModel{
			SpaceId: spaceId, UID: testutil.UID, Role: callerRole, Status: 1,
		})
		assert.NoError(t, err)
	}
}

func TestSpace_CreateMemberEmailInvite_Success(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-1"
	// testutil.UID 作为 owner
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
		"email": "newmember@example.com",
		"role":  EmailInviteRoleMember,
	})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		ID         int64  `json:"id"`
		Email      string `json:"email"`
		Token      string `json:"token"`
		Status     int    `json:"status"`
		InviteType int    `json:"invite_type"`
		SpaceId    string `json:"space_id"`
		Role       int    `json:"role"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Greater(t, resp.ID, int64(0))
	assert.Equal(t, "newmember@example.com", resp.Email)
	// Phase 6 起 raw token 仅通过邮件投递，API 不再返回。
	assert.Empty(t, resp.Token, "raw token 不应出现在创建响应中")
	assert.Equal(t, EmailInviteTypeMember, resp.InviteType)
	assert.Equal(t, spaceId, resp.SpaceId)
	assert.Equal(t, EmailInviteRoleMember, resp.Role)

	got, err := testSpaceDB.queryEmailInviteByID(resp.ID)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, spaceId, got.SpaceId)
}

func TestSpace_CreateMemberEmailInvite_AdminRoleAllowed(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-admin"
	// creator 是另一个人，testutil.UID 是 admin (role=1)
	seedSpaceWithMemberRole(t, spaceId, "owner-x", 1)

	w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
		"email": "a@x.com",
		"role":  EmailInviteRoleAdmin,
	})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestSpace_CreateMemberEmailInvite_RegularMemberForbidden(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-noperm"
	// testutil.UID 只是 role=0 普通成员
	seedSpaceWithMemberRole(t, spaceId, "owner-y", 0)

	w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
		"email": "a@x.com", "role": 0,
	})
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestSpace_CreateMemberEmailInvite_NonMemberForbidden(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-stranger"
	// 空间不包含 testutil.UID
	err = testSpaceDB.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId, Name: "x", Creator: "owner-z", Status: SpaceStatusNormal,
	})
	assert.NoError(t, err)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{SpaceId: spaceId, UID: "owner-z", Role: 2, Status: 1})
	assert.NoError(t, err)

	w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
		"email": "a@x.com", "role": 0,
	})
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestSpace_CreateMemberEmailInvite_InactiveSpace(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-disbanded"
	err = testSpaceDB.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId, Name: "x", Creator: testutil.UID, Status: SpaceStatusDisbanded,
	})
	assert.NoError(t, err)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{SpaceId: spaceId, UID: testutil.UID, Role: 2, Status: 1})
	assert.NoError(t, err)

	w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
		"email": "a@x.com", "role": 0,
	})
	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestSpace_CreateMemberEmailInvite_Validation(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-val"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"bad email", map[string]interface{}{"email": "no-at", "role": 0}},
		{"invalid role 2", map[string]interface{}{"email": "a@x.com", "role": 2}},
		{"invalid role -1", map[string]interface{}{"email": "a@x.com", "role": -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, tc.body)
			assert.NotEqual(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

func TestSpace_CreateMemberEmailInvite_AllowsDuplicatePending(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-inv-dup"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	for i := 0; i < 2; i++ {
		w := postJSON(t, srv, "/v1/space/"+spaceId+"/email-invites", testutil.Token, map[string]interface{}{
			"email": "dup@x.com", "role": 0,
		})
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	list, count, err := testSpaceDB.listEmailInvitesBySpace(spaceId, EmailInviteStatusPending, 10, 0)
	assert.NoError(t, err)
	assert.EqualValues(t, 2, count)
	assert.Len(t, list, 2)
}

func TestSpace_ListMemberEmailInvites(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-list"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	// 同空间 owner 自己发 + 另一个 admin 发，都应在列表中
	seedMemberInvite(t, "h-owner", "a@x.com", spaceId, testutil.UID, 0, EmailInviteStatusPending)
	seedMemberInvite(t, "h-other-admin", "b@x.com", spaceId, "admin-x", 0, EmailInviteStatusPending)
	// 其他空间的不出现
	seedMemberInvite(t, "h-other-space", "c@x.com", "other-space", testutil.UID, 0, EmailInviteStatusPending)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/space/"+spaceId+"/email-invites?page_index=1&page_size=20", nil)
	req.Header.Set("token", testutil.Token)
	srv.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Count int64 `json:"count"`
		List  []struct {
			Email string `json:"email"`
			Token string `json:"token"`
		} `json:"list"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 2, resp.Count)
	for _, it := range resp.List {
		assert.Empty(t, it.Token)
	}
}

func TestSpace_RevokeMemberEmailInvite(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-revoke"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	// 另一个 admin 发出的邀请，当前 owner 也应能 revoke（只要是该 space admin/owner）
	id := seedMemberInvite(t, "h-rv", "a@x.com", spaceId, "admin-x", 0, EmailInviteStatusPending)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE",
		fmt.Sprintf("/v1/space/%s/email-invites/%d", spaceId, id), nil)
	req.Header.Set("token", testutil.Token)
	srv.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, _ := testSpaceDB.queryEmailInviteByID(id)
	assert.Equal(t, EmailInviteStatusRevoked, got.Status)
}

func TestSpace_RevokeMemberEmailInvite_WrongSpace(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-a"
	otherSpaceId := "sp-b"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)
	err = testSpaceDB.insertSpaceNoTx(&SpaceModel{
		SpaceId: otherSpaceId, Name: "x", Creator: "owner-x", Status: SpaceStatusNormal,
	})
	assert.NoError(t, err)

	// 邀请属于 sp-b
	id := seedMemberInvite(t, "h-wrong", "a@x.com", otherSpaceId, "owner-x", 0, EmailInviteStatusPending)

	// 走 sp-a 的 URL 应拒绝
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE",
		fmt.Sprintf("/v1/space/%s/email-invites/%d", spaceId, id), nil)
	req.Header.Set("token", testutil.Token)
	srv.GetRoute().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)

	got, _ := testSpaceDB.queryEmailInviteByID(id)
	assert.Equal(t, EmailInviteStatusPending, got.Status)
}

func TestSpace_RevokeMemberEmailInvite_RejectsOwnerType(t *testing.T) {
	srv, _, err := setup(t)
	assert.NoError(t, err)
	spaceId := "sp-owner-guard"
	seedSpaceWithMemberRole(t, spaceId, testutil.UID, 2)

	// 混入一条 owner 类型邀请（不应被 space 端点识别）
	id := seedOwnerInvite(t, "h-owner-guard", "a@x.com", "admin-1", EmailInviteStatusPending, nil)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE",
		fmt.Sprintf("/v1/space/%s/email-invites/%d", spaceId, id), nil)
	req.Header.Set("token", testutil.Token)
	srv.GetRoute().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)

	got, _ := testSpaceDB.queryEmailInviteByID(id)
	assert.Equal(t, EmailInviteStatusPending, got.Status)
}
