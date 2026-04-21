package group

import (
	"fmt"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/stretchr/testify/assert"
)

func setupServiceTest(t *testing.T) (IService, *user.DB) {
	t.Helper()
	_, ctx := testutil.NewTestServer()
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)
	userDB := user.NewDB(ctx)
	svc := NewService(ctx)
	return svc, userDB
}

func insertTestUsers(t *testing.T, userDB *user.DB, uids ...string) {
	t.Helper()
	for i, uid := range uids {
		err := userDB.Insert(&user.Model{
			UID:     uid,
			Name:    "user_" + uid,
			ShortNo: fmt.Sprintf("sn_%s_%d", uid, i),
		})
		assert.NoError(t, err)
	}
}

func TestCreateGroup_Success(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2"},
		Name:    "测试群",
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.GroupNo)

	// 验证群和成员已创建
	s := svc.(*Service)
	model, err := s.db.QueryWithGroupNo(resp.GroupNo)
	assert.NoError(t, err)
	assert.Equal(t, "测试群", model.Name)
	assert.Equal(t, testutil.UID, model.Creator)

	members, err := s.db.QueryMembersFirstNine(resp.GroupNo)
	assert.NoError(t, err)
	assert.Len(t, members, 3) // creator + 2 members
}

func TestCreateGroup_AutoGenerateName(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1"},
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Name)
	assert.Contains(t, resp.Name, "user_")
}

func TestCreateGroup_DeduplicateMembers(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m1", testutil.UID},
	})
	assert.NoError(t, err)

	s := svc.(*Service)
	members, err := s.db.QueryMembersFirstNine(resp.GroupNo)
	assert.NoError(t, err)
	assert.Len(t, members, 2) // creator + m1, no duplicates
}

func TestCreateGroup_EventNilSafe(t *testing.T) {
	// ctx.Event is nil in test env — verify CreateGroup doesn't panic
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	assert.NotPanics(t, func() {
		resp, err := svc.CreateGroup(&CreateGroupServiceReq{
			Creator: testutil.UID,
			Members: []string{"m1", "m2"},
			Name:    "nil-event-safe",
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, resp.GroupNo)
	})
}

func TestCreateGroup_EmptyCreator(t *testing.T) {
	svc, _ := setupServiceTest(t)
	_, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: "",
		Members: []string{"m1"},
	})
	assert.Error(t, err)
}

func TestCreateGroup_EmptyMembers(t *testing.T) {
	svc, _ := setupServiceTest(t)
	_, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{},
	})
	assert.Error(t, err)
}

func TestRemoveGroupMembers_EventNilSafe(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2"},
		Name:    "踢人测试群",
	})
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		removeResp, err := svc.RemoveGroupMembers(&RemoveGroupMembersServiceReq{
			GroupNo:      resp.GroupNo,
			Members:      []string{"m1"},
			OperatorUID:  testutil.UID,
			OperatorName: "创建者",
		})
		assert.NoError(t, err)
		assert.Equal(t, 1, removeResp.Removed)
	})

	// 验证成员已移除
	s := svc.(*Service)
	members, err := s.db.QueryMembersFirstNine(resp.GroupNo)
	assert.NoError(t, err)
	assert.Len(t, members, 2) // creator + m2
}

func TestRemoveGroupMembers_MemberCountDecrease(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1", "m2", "m3")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1", "m2", "m3"},
		Name:    "踢人验证群",
	})
	assert.NoError(t, err)

	// 踢掉两个成员
	removeResp, err := svc.RemoveGroupMembers(&RemoveGroupMembersServiceReq{
		GroupNo:      resp.GroupNo,
		Members:      []string{"m1", "m2"},
		OperatorUID:  testutil.UID,
		OperatorName: "创建者",
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, removeResp.Removed)

	s := svc.(*Service)
	members, err := s.db.QueryMembersFirstNine(resp.GroupNo)
	assert.NoError(t, err)
	assert.Len(t, members, 2) // creator + m3
}

func TestRemoveGroupMembers_SkipCreator(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1"},
		Name:    "踢群主测试",
	})
	assert.NoError(t, err)

	// 尝试踢群主，应静默跳过
	removeResp, err := svc.RemoveGroupMembers(&RemoveGroupMembersServiceReq{
		GroupNo:      resp.GroupNo,
		Members:      []string{testutil.UID},
		OperatorUID:  testutil.UID,
		OperatorName: "创建者",
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, removeResp.Removed)
}
