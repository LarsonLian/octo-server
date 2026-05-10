package user

import (
	"database/sql"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/gocraft/dbr/v2"
)

// verificationModel 对应 user_verification 表。
//
// 自 2026-05-10 起（YUJ-382 / Aegis OIDC Phase 1),OIDC callback(modules/oidc/api.go)
// 是 user_verification 表的唯一写入方,权威源从 dmwork-verify-service 迁移到 Aegis IdP。
// 历史:此前由 dmwork-verify-service 经 HMAC POST /v1/internal/verification/complete
//       写入,该链路已随 Aegis OIDC 直切方案废弃;api_verification.go 整个文件被删除。
//
// 表 schema 不变:迁移期 OCTO 侧继续基于本表给 profile 着色,前端协议无感知。
type verificationModel struct {
	UserID     string         `db:"user_id"`
	RealName   string         `db:"real_name"`
	Source     string         `db:"source"`
	SourceSub  string         `db:"source_sub"`
	EmpID      dbr.NullString `db:"emp_id"`
	Dept       dbr.NullString `db:"dept"`
	Email      dbr.NullString `db:"email"`
	Mobile     dbr.NullString `db:"mobile"`
	VerifiedAt time.Time      `db:"verified_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

// verificationDB 封装 user_verification 表访问。
type verificationDB struct {
	session *dbr.Session
	ctx     *config.Context
}

func newVerificationDB(ctx *config.Context) *verificationDB {
	return &verificationDB{
		session: ctx.DB(),
		ctx:     ctx,
	}
}

// QueryByUID 查询单个用户的实名记录；无记录返回 (nil, nil)。
func (d *verificationDB) QueryByUID(uid string) (*verificationModel, error) {
	var m *verificationModel
	_, err := d.session.Select("*").From("user_verification").Where("user_id=?", uid).Load(&m)
	return m, err
}

// QueryByUIDs 批量查询实名记录，返回 uid → model 的映射。
// 用于批量详情接口避免 N+1。
func (d *verificationDB) QueryByUIDs(uids []string) (map[string]*verificationModel, error) {
	result := make(map[string]*verificationModel, len(uids))
	if len(uids) == 0 {
		return result, nil
	}
	var list []*verificationModel
	_, err := d.session.Select("*").From("user_verification").Where("user_id IN ?", uids).Load(&list)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		result[m.UserID] = m
	}
	return result, nil
}

// Upsert 按 user_id 幂等写入。存在则更新,不存在则插入。
// OIDC callback(modules/oidc/api.go)是唯一写入方,对同一用户每次 OIDC 再登录都会被调用。
//
// 🚨 Phase 1 NULL overwrite 热修(Mininglamp-OSS/octo-server#1334 / YUJ-390,2026-05-10):
// 旧版 SQL 对所有列无条件 `col = VALUES(col)`,会把 OIDC claims 里未返回的
// emp_id / dept / mobile(NullString{}) 以及空 sub 全部冲掉历史值,造成再登录
// 一次原先由 verify-service 写入的工号/部门/手机号/来源 sub 全部变 NULL。
//
// 修复语义(与字段是否 NOT NULL 对齐):
//   - emp_id / dept / mobile(DEFAULT NULL):`COALESCE(VALUES(col), col)` —
//     新值为 NULL 时保留旧值,新值非 NULL 时正常覆盖。
//   - source_sub(NOT NULL VARCHAR,空串合法但表示"上游未提供"):
//     `IF(VALUES(source_sub)='', source_sub, VALUES(source_sub))` — 空串视为
//     "保留旧值"。COALESCE 在这里不适用(空串不是 NULL)。
//   - real_name / source / email / verified_at:继续 VALUES(col) 直接覆盖 —
//     这些都是每次 OIDC callback 明确给出的权威字段,允许再登录刷新。
//     email 目前不在保护列表,若未来 claims 允许"已注册但隐藏邮箱"再加保护。
func (d *verificationDB) Upsert(m *verificationModel) error {
	if m == nil || m.UserID == "" {
		return nil
	}
	// dbr 的 InsertStmt 不暴露 Suffix,这里用 InsertBySql + ON DUPLICATE KEY UPDATE 完成 upsert。
	// 列顺序与占位符对齐;updated_at 走列默认 ON UPDATE CURRENT_TIMESTAMP 自动更新。
	_, err := d.session.InsertBySql(
		"INSERT INTO user_verification "+
			"(user_id, real_name, source, source_sub, emp_id, dept, email, mobile, verified_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE "+
			"real_name=VALUES(real_name), "+
			"source=VALUES(source), "+
			"source_sub=IF(VALUES(source_sub)='', source_sub, VALUES(source_sub)), "+
			"emp_id=COALESCE(VALUES(emp_id), emp_id), "+
			"dept=COALESCE(VALUES(dept), dept), "+
			"email=VALUES(email), "+
			"mobile=COALESCE(VALUES(mobile), mobile), "+
			"verified_at=VALUES(verified_at)",
		m.UserID, m.RealName, m.Source, m.SourceSub,
		m.EmpID, m.Dept, m.Email, m.Mobile, m.VerifiedAt,
	).Exec()
	return err
}

// nullableVerificationString 封装 "" → SQL NULL 的惯用转换。
//
// 原先这个 helper 叫 nullableString、住在已删除的 api_verification.go 里;
// 随该文件删除后沿用相同语义(TrimSpace 后空 → NULL)搬到本文件,避免 OIDC
// 路径写库时把字面空串落到 emp_id / dept / email / mobile 等允许为 NULL 的列上。
func nullableVerificationString(s string) dbr.NullString {
	if strings.TrimSpace(s) == "" {
		return dbr.NullString{}
	}
	return dbr.NullString{NullString: sql.NullString{String: s, Valid: true}}
}
