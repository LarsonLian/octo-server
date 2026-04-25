package space

import (
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/pkg/db"
	"github.com/stretchr/testify/assert"
)

func seedOwnerInvite(t *testing.T, hash, email, createdBy string, status int, expiresAt *time.Time) int64 {
	t.Helper()
	m := &spaceEmailInviteModel{
		TokenHash:       hash,
		InviteType:      EmailInviteTypeOwner,
		Email:           email,
		PlannedName:     "计划空间",
		PlannedMaxUsers: 100,
		Status:          status,
		CreatedBy:       createdBy,
	}
	if expiresAt != nil {
		t := db.Time(*expiresAt)
		m.ExpiresAt = &t
	}
	id, err := testSpaceDB.insertEmailInvite(m)
	assert.NoError(t, err)
	return id
}

func seedMemberInvite(t *testing.T, hash, email, spaceId, createdBy string, role, status int) int64 {
	t.Helper()
	id, err := testSpaceDB.insertEmailInvite(&spaceEmailInviteModel{
		TokenHash:  hash,
		InviteType: EmailInviteTypeMember,
		Email:      email,
		SpaceId:    spaceId,
		Role:       role,
		Status:     status,
		CreatedBy:  createdBy,
	})
	assert.NoError(t, err)
	return id
}

func TestEmailInvite_InsertAndQueryByTokenHash(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	id := seedOwnerInvite(t, "hash-abc", "a@x.com", "admin-1", EmailInviteStatusPending, nil)
	assert.Greater(t, id, int64(0))

	got, err := testSpaceDB.queryEmailInviteByTokenHash("hash-abc")
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, id, got.Id)
	assert.Equal(t, EmailInviteTypeOwner, got.InviteType)
	assert.Equal(t, "a@x.com", got.Email)
	assert.Equal(t, "计划空间", got.PlannedName)
	assert.Equal(t, EmailInviteStatusPending, got.Status)

	miss, err := testSpaceDB.queryEmailInviteByTokenHash("hash-missing")
	assert.NoError(t, err)
	assert.Nil(t, miss)
}

func TestEmailInvite_QueryByID(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	id := seedOwnerInvite(t, "hash-id", "b@x.com", "admin-1", EmailInviteStatusPending, nil)
	got, err := testSpaceDB.queryEmailInviteByID(id)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, id, got.Id)

	miss, err := testSpaceDB.queryEmailInviteByID(99999)
	assert.NoError(t, err)
	assert.Nil(t, miss)
}

func TestEmailInvite_ListByCreator_FiltersTypeAndStatus(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	// admin-1: 两条 owner pending + 一条 owner revoked
	seedOwnerInvite(t, "h1", "a@x.com", "admin-1", EmailInviteStatusPending, nil)
	seedOwnerInvite(t, "h2", "b@x.com", "admin-1", EmailInviteStatusPending, nil)
	seedOwnerInvite(t, "h3", "c@x.com", "admin-1", EmailInviteStatusRevoked, nil)
	// member 类型（不同 creator），不应出现在 admin-1 的 owner 列表
	seedMemberInvite(t, "h4", "d@x.com", "space-1", "owner-1", EmailInviteRoleMember, EmailInviteStatusPending)
	// 另一个 admin
	seedOwnerInvite(t, "h5", "e@x.com", "admin-2", EmailInviteStatusPending, nil)

	list, count, err := testSpaceDB.listEmailInvitesByCreator("admin-1", EmailInviteTypeOwner, -1, 20, 0)
	assert.NoError(t, err)
	assert.EqualValues(t, 3, count)
	assert.Len(t, list, 3)

	pendingList, pendingCount, err := testSpaceDB.listEmailInvitesByCreator("admin-1", EmailInviteTypeOwner, EmailInviteStatusPending, 20, 0)
	assert.NoError(t, err)
	assert.EqualValues(t, 2, pendingCount)
	assert.Len(t, pendingList, 2)
}

func TestEmailInvite_ListBySpace(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	seedMemberInvite(t, "m1", "a@x.com", "space-1", "owner-1", EmailInviteRoleMember, EmailInviteStatusPending)
	seedMemberInvite(t, "m2", "b@x.com", "space-1", "owner-1", EmailInviteRoleAdmin, EmailInviteStatusConsumed)
	seedMemberInvite(t, "m3", "c@x.com", "space-2", "owner-2", EmailInviteRoleMember, EmailInviteStatusPending)
	// owner 类型不应命中 space 列表
	seedOwnerInvite(t, "m4", "d@x.com", "admin-1", EmailInviteStatusPending, nil)

	list, count, err := testSpaceDB.listEmailInvitesBySpace("space-1", -1, 20, 0)
	assert.NoError(t, err)
	assert.EqualValues(t, 2, count)
	assert.Len(t, list, 2)

	pendingList, pendingCount, err := testSpaceDB.listEmailInvitesBySpace("space-1", EmailInviteStatusPending, 20, 0)
	assert.NoError(t, err)
	assert.EqualValues(t, 1, pendingCount)
	assert.Len(t, pendingList, 1)
	assert.Equal(t, "a@x.com", pendingList[0].Email)
}

func TestEmailInvite_Revoke_OnlyPending(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	pendingID := seedOwnerInvite(t, "rv-pend", "a@x.com", "admin-1", EmailInviteStatusPending, nil)
	consumedID := seedOwnerInvite(t, "rv-cons", "b@x.com", "admin-1", EmailInviteStatusConsumed, nil)

	affected, err := testSpaceDB.revokeEmailInvite(pendingID)
	assert.NoError(t, err)
	assert.EqualValues(t, 1, affected)

	got, _ := testSpaceDB.queryEmailInviteByID(pendingID)
	assert.Equal(t, EmailInviteStatusRevoked, got.Status)

	// 已 consumed 的邀请不允许 revoke
	affected2, err := testSpaceDB.revokeEmailInvite(consumedID)
	assert.NoError(t, err)
	assert.EqualValues(t, 0, affected2)

	// 重复 revoke 幂等返回 0
	affected3, err := testSpaceDB.revokeEmailInvite(pendingID)
	assert.NoError(t, err)
	assert.EqualValues(t, 0, affected3)
}

func TestEmailInvite_ConsumeTx_BlocksExpiredAndNonPending(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	future := time.Now().Add(1 * time.Hour)
	past := time.Now().Add(-1 * time.Hour)

	validID := seedOwnerInvite(t, "cs-valid", "a@x.com", "admin-1", EmailInviteStatusPending, &future)
	expiredID := seedOwnerInvite(t, "cs-exp", "b@x.com", "admin-1", EmailInviteStatusPending, &past)
	revokedID := seedOwnerInvite(t, "cs-rv", "c@x.com", "admin-1", EmailInviteStatusRevoked, &future)

	tx, err := testSpaceDB.session.Begin()
	assert.NoError(t, err)
	defer tx.RollbackUnlessCommitted()

	aff, err := testSpaceDB.consumeEmailInviteTx(tx, validID, "recipient-1")
	assert.NoError(t, err)
	assert.EqualValues(t, 1, aff)

	aff, err = testSpaceDB.consumeEmailInviteTx(tx, expiredID, "recipient-1")
	assert.NoError(t, err)
	assert.EqualValues(t, 0, aff)

	aff, err = testSpaceDB.consumeEmailInviteTx(tx, revokedID, "recipient-1")
	assert.NoError(t, err)
	assert.EqualValues(t, 0, aff)

	assert.NoError(t, tx.Commit())

	got, _ := testSpaceDB.queryEmailInviteByID(validID)
	assert.Equal(t, EmailInviteStatusConsumed, got.Status)
	assert.Equal(t, "recipient-1", got.ConsumedBy)
	assert.NotNil(t, got.ConsumedAt)
}

func TestEmailInvite_ConsumeTx_RollbackLeavesPending(t *testing.T) {
	_, _, err := setup(t)
	assert.NoError(t, err)

	id := seedOwnerInvite(t, "cs-rb", "a@x.com", "admin-1", EmailInviteStatusPending, nil)

	tx, err := testSpaceDB.session.Begin()
	assert.NoError(t, err)

	aff, err := testSpaceDB.consumeEmailInviteTx(tx, id, "recipient-1")
	assert.NoError(t, err)
	assert.EqualValues(t, 1, aff)

	assert.NoError(t, tx.Rollback())

	got, _ := testSpaceDB.queryEmailInviteByID(id)
	assert.Equal(t, EmailInviteStatusPending, got.Status)
	assert.Equal(t, "", got.ConsumedBy)
}
