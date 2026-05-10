package user

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/gocraft/dbr/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpsertVerificationFromOIDC_DoesNotOverwriteEmpDeptMobile 覆盖
// Mininglamp-OSS/octo-server#1334 / YUJ-390 Phase 1 NULL overwrite 热修:
//
// 场景:之前(verify-service 链路 / 首次 OIDC 带齐 claims 时)已往 user_verification
// 写入过完整一行,包含 emp_id / dept / mobile 以及非空 source_sub。现在用户走
// Aegis OIDC 再登录一次,id_token / userinfo 的 claims 中恰好没有 emp/dept/mobile
// (scope 配置差异 / 上游 IdP 暂不返回),并且 sub 缺失。
//
// 期望:再登录产生的 upsert 只刷新 real_name / source / verified_at 等"claims 权威"
// 字段,不把历史填好的 emp_id / dept / mobile / source_sub 冲成 NULL/空串。
//
// 实现:真 DB 集成测试,依赖 testutil.NewTestServer() 起的本地 MySQL —— 必须跑
// 真实的 ON DUPLICATE KEY UPDATE 才能验证 COALESCE / IF 分支行为(sqlmock 无法
// 模拟 MySQL 语义)。
func TestUpsertVerificationFromOIDC_DoesNotOverwriteEmpDeptMobile(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	svc, ok := NewService(ctx).(*Service)
	require.True(t, ok, "NewService must return *Service")

	uid := "u-relogin-empdept-1"

	// --- 1. seed row:旧 verify-service / 首次完整 OIDC 已写入的完整记录 ---
	seed := &verificationModel{
		UserID:    uid,
		RealName:  "张三",
		Source:    "cas",
		SourceSub: "cas-sub-old-42",
		EmpID: dbr.NullString{
			NullString: sql.NullString{String: "E12345", Valid: true},
		},
		Dept: dbr.NullString{
			NullString: sql.NullString{String: "研发部/IM 组", Valid: true},
		},
		Email: dbr.NullString{
			NullString: sql.NullString{String: "zhangsan@old.example", Valid: true},
		},
		Mobile: dbr.NullString{
			NullString: sql.NullString{String: "13800000000", Valid: true},
		},
		VerifiedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, svc.verificationDB.Upsert(seed), "seed upsert")

	// --- 2. 触发 OIDC 再登录:claims 只带 legal_name / provider / verified_at ---
	// emp / dept / mobile 在 UpsertVerificationFromOIDC 里永远写 NullString{} = NULL,
	// Subject="" 模拟 Aegis 未返回 sub 的降级场景(service 允许空兜底)。
	claims := OIDCVerificationClaims{
		LegalName:        "张三 (CAS)",
		LegalEmail:       "",
		Subject:          "",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC).Unix(),
	}
	require.NoError(t,
		svc.UpsertVerificationFromOIDC(context.Background(), uid, claims),
		"re-login upsert must succeed",
	)

	// --- 3. 断言 ---
	got, err := svc.verificationDB.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got, "row should exist after re-login upsert")

	// claims 权威字段:按最新值刷新
	assert.Equal(t, "张三 (CAS)", got.RealName, "real_name should refresh from claims")
	assert.Equal(t, "cas", got.Source, "source should refresh from claims")
	assert.WithinDuration(t,
		time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		got.VerifiedAt.UTC(),
		time.Second,
		"verified_at should refresh from claims",
	)

	// ===== 本次热修的核心断言:4 个字段必须保留旧值,不能被 NULL/空串覆盖 =====
	assert.Equal(t, "cas-sub-old-42", got.SourceSub,
		"source_sub: empty-sub re-login must not overwrite existing value")

	assert.True(t, got.EmpID.Valid, "emp_id must stay non-NULL after empty re-login")
	assert.Equal(t, "E12345", got.EmpID.String, "emp_id value preserved")

	assert.True(t, got.Dept.Valid, "dept must stay non-NULL after empty re-login")
	assert.Equal(t, "研发部/IM 组", got.Dept.String, "dept value preserved")

	assert.True(t, got.Mobile.Valid, "mobile must stay non-NULL after empty re-login")
	assert.Equal(t, "13800000000", got.Mobile.String, "mobile value preserved")
}
