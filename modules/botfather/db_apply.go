package botfather

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/db"
	"github.com/gocraft/dbr/v2"
)

type robotApplyDB struct {
	session *dbr.Session
	ctx     *config.Context
}

func newRobotApplyDB(ctx *config.Context) *robotApplyDB {
	return &robotApplyDB{
		ctx:     ctx,
		session: ctx.DB(),
	}
}

type robotApplyModel struct {
	Id       int64
	UID      string
	RobotUID string
	OwnerUID string
	Remark   string
	SpaceID  string // 申请来源 Space
	Status   int    // 0=待处理 1=通过 2=拒绝
	db.BaseModel
}

const (
	ApplyStatusPending  = 0
	ApplyStatusApproved = 1
	ApplyStatusRejected = 2
)

// insert 创建申请记录
func (d *robotApplyDB) insert(m *robotApplyModel) error {
	_, err := d.session.InsertInto("robot_apply").Columns(
		"uid", "robot_uid", "owner_uid", "remark", "status", "space_id",
	).Values(
		m.UID, m.RobotUID, m.OwnerUID, m.Remark, m.Status, m.SpaceID,
	).Exec()
	return err
}

// queryByID 通过ID查询申请
func (d *robotApplyDB) queryByID(id int64) (*robotApplyModel, error) {
	var m *robotApplyModel
	_, err := d.session.Select("*").From("robot_apply").Where("id=?", id).Load(&m)
	return m, err
}

// queryPendingByUIDAndRobot 查询用户对某机器人的待处理申请
func (d *robotApplyDB) queryPendingByUIDAndRobot(uid, robotUID string) (*robotApplyModel, error) {
	var m *robotApplyModel
	_, err := d.session.Select("*").From("robot_apply").
		Where("uid=? AND robot_uid=? AND status=?", uid, robotUID, ApplyStatusPending).
		Load(&m)
	return m, err
}

// queryPendingByOwner 查询某个Owner的所有待审批申请
func (d *robotApplyDB) queryPendingByOwner(ownerUID string, limit, offset int) ([]*robotApplyModel, error) {
	var list []*robotApplyModel
	_, err := d.session.Select("*").From("robot_apply").
		Where("owner_uid=? AND status=?", ownerUID, ApplyStatusPending).
		OrderDir("created_at", false).
		Limit(uint64(limit)).
		Offset(uint64(offset)).
		Load(&list)
	return list, err
}

// queryPendingCountByOwner 查询某个Owner的待审批申请数量
func (d *robotApplyDB) queryPendingCountByOwner(ownerUID string) (int64, error) {
	var count int64
	err := d.session.Select("count(*)").From("robot_apply").
		Where("owner_uid=? AND status=?", ownerUID, ApplyStatusPending).
		LoadOne(&count)
	return count, err
}

// updateStatus 更新申请状态
func (d *robotApplyDB) updateStatus(id int64, status int) error {
	_, err := d.session.Update("robot_apply").
		Set("status", status).
		Where("id=?", id).
		Exec()
	return err
}

// deletePendingByUIDAndRobot 删除用户对某机器人的待处理申请（用于重新申请前清理）
func (d *robotApplyDB) deletePendingByUIDAndRobot(uid, robotUID string) error {
	_, err := d.session.DeleteFrom("robot_apply").
		Where("uid=? AND robot_uid=? AND status=?", uid, robotUID, ApplyStatusPending).
		Exec()
	return err
}

// queryByUIDAndRobot 查询用户对某机器人的最近申请（用于检查是否存在已通过的关系）
func (d *robotApplyDB) queryApprovedByUIDAndRobot(uid, robotUID string) (*robotApplyModel, error) {
	var m *robotApplyModel
	_, err := d.session.Select("*").From("robot_apply").
		Where("uid=? AND robot_uid=? AND status=?", uid, robotUID, ApplyStatusApproved).
		Load(&m)
	return m, err
}
