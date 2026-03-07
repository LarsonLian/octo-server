package space

import (
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/gocraft/dbr/v2"
)

type DB struct {
	ctx     *config.Context
	session *dbr.Session
}

func NewDB(ctx *config.Context) *DB {
	return &DB{
		ctx:     ctx,
		session: ctx.DB(),
	}
}

// isSpaceActive 检查空间是否处于活跃状态
func (d *DB) isSpaceActive(spaceId string) (bool, error) {
	var count int
	_, err := d.session.SelectBySql("SELECT COUNT(*) FROM space WHERE space_id=? AND status=1", spaceId).Load(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ---------- Space CRUD ----------

func (d *DB) insertSpace(m *SpaceModel, tx *dbr.Tx) error {
	_, err := tx.InsertInto("space").Columns(util.AttrToUnderscore(m)...).Record(m).Exec()
	return err
}

func (d *DB) querySpaceByID(spaceId string) (*SpaceModel, error) {
	var m SpaceModel
	_, err := d.session.Select("*").From("space").Where("space_id=? and status=1", spaceId).Load(&m)
	if m.SpaceId == "" {
		return nil, nil
	}
	return &m, err
}

func (d *DB) updateSpace(spaceId string, name string, description string, logo string) error {
	_, err := d.session.Update("space").
		Set("name", name).
		Set("description", description).
		Set("logo", logo).
		Set("updated_at", time.Now()).
		Where("space_id=?", spaceId).Exec()
	return err
}

func (d *DB) disbandSpace(spaceId string) error {
	_, err := d.session.Update("space").Set("status", 0).Set("updated_at", time.Now()).Where("space_id=?", spaceId).Exec()
	return err
}

// queryMySpaces 查询用户加入的所有空间（带角色和成员数）
func (d *DB) queryMySpaces(uid string) ([]*SpaceDetailModel, error) {
	var models []*SpaceDetailModel
	_, err := d.session.SelectBySql(`
		SELECT s.*, sm.role,
			(SELECT COUNT(*) FROM space_member WHERE space_id=s.space_id AND status=1) as member_count
		FROM space s
		INNER JOIN space_member sm ON s.space_id = sm.space_id
		WHERE sm.uid=? AND sm.status=1 AND s.status=1
		ORDER BY s.created_at DESC
	`, uid).Load(&models)
	return models, err
}

// querySpaceDetail 查询空间详情（带当前用户角色和成员数）
func (d *DB) querySpaceDetail(spaceId string, uid string) (*SpaceDetailModel, error) {
	var m SpaceDetailModel
	_, err := d.session.SelectBySql(`
		SELECT s.*, IFNULL(sm.role, -1) as role,
			(SELECT COUNT(*) FROM space_member WHERE space_id=s.space_id AND status=1) as member_count
		FROM space s
		LEFT JOIN space_member sm ON s.space_id = sm.space_id AND sm.uid=? AND sm.status=1
		WHERE s.space_id=? AND s.status=1
	`, uid, spaceId).Load(&m)
	if m.SpaceId == "" {
		return nil, nil
	}
	return &m, err
}

// ---------- Member CRUD ----------

func (d *DB) insertMember(m *MemberModel, tx *dbr.Tx) error {
	_, err := tx.InsertInto("space_member").Columns(util.AttrToUnderscore(m)...).Record(m).Exec()
	return err
}

func (d *DB) insertMemberNoTx(m *MemberModel) error {
	_, err := d.session.InsertInto("space_member").Columns(util.AttrToUnderscore(m)...).Record(m).Exec()
	return err
}

func (d *DB) queryMember(spaceId string, uid string) (*MemberModel, error) {
	var m MemberModel
	_, err := d.session.Select("*").From("space_member").
		Where("space_id=? and uid=? and status=1", spaceId, uid).Load(&m)
	if m.UID == "" {
		return nil, nil
	}
	return &m, err
}

// queryMemberIncludeRemoved 查询成员（包括已移除的），用于判断是否曾经加入过
func (d *DB) queryMemberIncludeRemoved(spaceId string, uid string) (*MemberModel, error) {
	var m MemberModel
	_, err := d.session.Select("*").From("space_member").
		Where("space_id=? and uid=?", spaceId, uid).Load(&m)
	if m.UID == "" {
		return nil, nil
	}
	return &m, err
}

func (d *DB) queryMembers(spaceId string, page uint64, limit uint64) ([]*MemberDetailModel, error) {
	var models []*MemberDetailModel
	_, err := d.session.SelectBySql(`
		SELECT sm.*, IFNULL(u.name,'') as name
		FROM space_member sm
		LEFT JOIN user u ON u.uid=sm.uid
		WHERE sm.space_id=? AND sm.status=1
		ORDER BY sm.role DESC, sm.created_at ASC
		LIMIT ? OFFSET ?
	`, spaceId, limit, (page-1)*limit).Load(&models)
	return models, err
}

func (d *DB) removeMember(spaceId string, uid string) error {
	_, err := d.session.Update("space_member").Set("status", 0).
		Set("updated_at", time.Now()).
		Where("space_id=? and uid=?", spaceId, uid).Exec()
	return err
}

func (d *DB) reactivateMember(spaceId string, uid string, role int) error {
	_, err := d.session.Update("space_member").
		Set("status", 1).Set("role", role).
		Set("updated_at", time.Now()).
		Where("space_id=? and uid=?", spaceId, uid).Exec()
	return err
}

func (d *DB) updateMemberRole(spaceId string, uid string, role int) error {
	_, err := d.session.Update("space_member").Set("role", role).
		Set("updated_at", time.Now()).
		Where("space_id=? and uid=? and status=1", spaceId, uid).Exec()
	return err
}

func (d *DB) updateMemberRoleTx(tx *dbr.Tx, spaceId string, uid string, role int) error {
	_, err := tx.Update("space_member").Set("role", role).
		Set("updated_at", time.Now()).
		Where("space_id=? and uid=? and status=1", spaceId, uid).Exec()
	return err
}

// queryCoMemberUIDs 查询与指定用户同在至少一个空间的所有用户UID
func (d *DB) queryCoMemberUIDs(uid string) ([]string, error) {
	var uids []string
	_, err := d.session.SelectBySql(`
		SELECT DISTINCT sm2.uid
		FROM space_member sm1
		INNER JOIN space_member sm2 ON sm1.space_id = sm2.space_id
		INNER JOIN space s ON s.space_id = sm1.space_id AND s.status=1
		WHERE sm1.uid=? AND sm1.status=1 AND sm2.status=1 AND sm2.uid!=?
	`, uid, uid).Load(&uids)
	return uids, err
}

// GetCoMemberUIDs 包级别函数，供其他模块调用，查询与指定用户同在至少一个空间的所有用户UID
func GetCoMemberUIDs(ctx *config.Context, uid string) ([]string, error) {
	db := NewDB(ctx)
	return db.queryCoMemberUIDs(uid)
}

// ---------- Invitation CRUD ----------

func (d *DB) insertInvitation(m *InvitationModel) error {
	_, err := d.session.InsertInto("space_invitation").Columns(util.AttrToUnderscore(m)...).Record(m).Exec()
	return err
}

func (d *DB) queryInvitationByCode(code string) (*InvitationModel, error) {
	var m InvitationModel
	_, err := d.session.Select("*").From("space_invitation").
		Where("invite_code=? and status=1", code).Load(&m)
	if m.InviteCode == "" {
		return nil, nil
	}
	return &m, err
}

func (d *DB) incrementInviteUsedCount(code string) error {
	_, err := d.session.UpdateBySql("UPDATE space_invitation SET used_count=used_count+1 WHERE invite_code=?", code).Exec()
	return err
}
