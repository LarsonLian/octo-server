package user

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/gocraft/dbr/v2"
)

// verificationModel 对应 user_verification 表。
//
// ⚠️ 此表是 Aegis identity_verification claims 的 **local read-through cache**,
// 不是 source of truth。权威源永远是 Aegis IdP,本表只缓存最近一次拉取到的快照,
// 供 OCTO profile 接口着色徽章用(避免每次 profile 查询都同步打 Aegis admin API)。
//
// 写入触发点(2026-05-10 起 Aegis OIDC 直切之后):
//  1. OIDC 登录 callback (modules/oidc/api.go) —— 用户走 IdP 登录时顺带同步
//  2. POST /v1/internal/realname/pull-from-aegis (YUJ-398) —— 前端主动 pull,
//     覆盖「去认证」新窗回跳 + didMount opportunistic refresh 两个场景
//
// (YUJ-399 TTL refresh worker 方案曾是 3rd 写入点,2026-05-10 归档,低变化
// 频率数据不宜用 TTL 轮询范式;follow-up YUJ-368 已立 Aegis webhook push,
// blocked-external。见 scope cleanup commit 36279c34。)
//
// 读取路径默认可能 stale,最大滞后取决于上面 2 个写入触发点的触发频率;
// "别人看我徽章"用户最差情况下要等对方或自己再次触发 pull / OIDC 重登才会刷新。
// 需要强一致读(比如"用户刚认证完立刻要徽章亮")的场景应走 pull-from-aegis
// 强刷一次,而不是依赖本表缓存。
//
// 自 2026-05-10 起（YUJ-382 / Aegis OIDC Phase 1),OIDC callback(modules/oidc/api.go)
// 首次成为 user_verification 表的写入方,权威源从 dmwork-verify-service 迁移到 Aegis IdP。
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

// DeleteByUID 删除单个用户的实名记录(YUJ-398 Round 1 Jerry-Xin Crit 2 + YUJ-399 Round 3 Crit 4)。
//
// 背景:user_verification 是 local read-through cache,Aegis 才是权威源。
// 当 Aegis 侧用户"取消实名" / "账号注销" / "is_verified=false" 权威态时,
// 如果不清 local row,service.go::GetUserDetail 只要查到任意行就标 RealnameVerified=true,
// 会造成**徽章永久假阳**(Aegis 说未实名,OCTO 徽章仍亮)。
//
// 调用合同(严格限定,任何一点错都会造成误删):
//   必须在 Aegis **权威确认**用户未实名时才调,具体是:
//     1. pull-from-aegis / refresh worker 从 Aegis admin API 拿到 2xx 响应且 is_verified=false
//     2. pull-from-aegis / refresh worker 从 Aegis admin API 拿到 404
//   严禁在以下场景调:
//     - ErrFetcherUnavailable(Aegis 整体不可达 / 5xx / token 拿不到)→ 保守保留旧 row
//     - JSON 解析失败 / 配置错 → 同上
//     - DB 查询错误 → 不触及
//   误删代价:某一次 Aegis 短暂抖动,所有 pulled 用户 cache 被清,下次 pull 又 upsert 回来 ——
//   但中间这段窗口 OCTO 徽章会误显示"未实名",用户发 support ticket。保守是这里的默认。
//
// 语义:
//   - uid 空串 → no-op + nil err(防御编程错误,不让 DELETE FROM ... WHERE user_id='' 误删)
//   - 行不存在 → 仍 nil err(幂等);调用方不依赖"是否删掉了"的返回值
//   - DB error → 原样返回给调用方记 warn
func (d *verificationDB) DeleteByUID(uid string) error {
	if strings.TrimSpace(uid) == "" {
		return nil
	}
	_, err := d.session.DeleteFrom("user_verification").Where("user_id=?", uid).Exec()
	return err
}

// LookupAegisSubjectByUID 查 user_oidc_identity 表拿某个本地 uid 对应的
// Aegis(OIDC IdP)subject。YUJ-398 Round 2 Crit B + YUJ-403(PR #1367 R5)
// Jerry + lml2468 共识 Critical:subject provenance gate。
//
// 背景(R5 收敛):
//   本地 uid 由 util.GenerUUID() 生成(见 modules/oidc/user_adapter.go:45),
//   **不等于** Aegis IdP 侧的 sub claim。pull-from-aegis 需要一个"可信的"
//   Aegis subject 作为 /admin/users/{key} 的 key。
//
//   之前(R4 前)的实现先读 user_verification.source_sub,但该字段 **不一定是
//   Aegis subject** —— 原表给 dmwork-verify-service callback 用,historical
//   value 可能是 CAS user_id / 企业微信 corp_id:user_id / 飞书 open_id / 其他
//   legacy IdP subject。直接把 legacy sub 当 Aegis key 传会让 Aegis 返 404,
//   fetcher 归一成 (nil,nil) → handler 误走 authoritative_unverified 分支,
//   把用户 **正确的** user_verification 记录清掉 → 徽章瞬间熄灭,直到下次
//   OIDC 重登才恢复。
//
//   修法:本函数只查 issuer = Aegis OIDC provider issuer 的 identity 行。
//   查到 → 返的 subject 一定是 Aegis 权威 subject(provenance: trusted);
//   查不到 → 返 ""(调用方再退到 legacy source_sub / uid fallback,但要以
//   trusted=false 语义对待后续的 404 信号)。
//
// 为什么不复用 oidc.DB.QueryIdentitiesByUID:
//   modules/oidc 已经 import modules/user(UpsertVerificationFromOIDC),
//   user → oidc 会形成循环。同一张表直接 session.Select 不算 "跨模块拷贝语义",
//   查询极小(单条 LIMIT 1 + WHERE uid+issuer 复合索引),维护成本可忽略。
//
// 行为:
//   - 空 uid → ("", nil),不查库
//   - 空 issuer → ("", nil),不查库(调用方降级为 trusted=false,保守不清 cache)
//   - 多条 identity(同 uid+issuer)→ 取 last_login_at DESC 最近登录的一条
//     (同一 issuer 下同 uid 多行理论上不会,但历史 schema 没强制约束,防御性 LIMIT 1)。
//   - 无匹配行 → ("", nil),调用方走 legacy / uid fallback(但 trusted=false)
//   - DB error → ("", err),调用方记 warn 后继续(trusted=false)
func (d *verificationDB) LookupAegisSubjectByUID(uid, issuer string) (string, error) {
	if strings.TrimSpace(uid) == "" {
		return "", nil
	}
	if strings.TrimSpace(issuer) == "" {
		// issuer 缺失(DM_OIDC_PROVIDER_ISSUER 未配)→ 降级为 "查不到 Aegis
		// identity",调用方走 trusted=false 保守策略,**不**清 cache。
		return "", nil
	}
	var subjects []string
	_, err := d.session.Select("IFNULL(subject,'')").From("user_oidc_identity").
		Where("uid=? AND issuer=?", uid, issuer).
		OrderBy("COALESCE(last_login_at, linked_at) DESC").
		Limit(1).
		Load(&subjects)
	if err != nil {
		return "", fmt.Errorf("lookup aegis subject by uid=%q issuer=%q: %w", uid, issuer, err)
	}
	if len(subjects) == 0 {
		return "", nil
	}
	return subjects[0], nil
}
