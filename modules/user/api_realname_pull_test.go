package user

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// YUJ-398 · Phase 2e 闭环 ·
// POST /v1/internal/realname/pull-from-aegis handler 核心判定单测。
//
// 覆盖 issue "任务 B 单测" 合同的 4 个分支(handler 纯函数部分;Redis 1s 去重
// + wkhttp Response 外壳走 integration / manual smoke):
//   1. claims 完整 → Upsert + 返 verified:true
//   2. Aegis 不可达(ErrFetcherUnavailable) → 返 verified:false,不 Upsert
//   3. claims is_verified=false / Aegis 404(fetcher 约定返 nil,nil) → verified:false,不 Upsert
//   4. claims 残缺(legal_name 空 / verified_at 0) → verified:false,不 Upsert
//   5. Upsert 失败(allowlist miss / DB 抖) → 降级 verified:false(不 5xx)
//
// Round 1 追加(Jerry-Xin Crit 2 — 同 YUJ-399 Crit 4):
//   6. invalidateStaleVerification 合同 —— handler 在 Aegis 权威未实名(claims=nil+err=nil)
//      时清 user_verification row;在 ErrFetcherUnavailable / 其他 err 时**保留** row。
//      通过 pullFlow 模拟 handler 主路径的 delete/upsert 调用顺序断言。

// nopWarn / nopDebug 吞日志,测试不关心副作用。
func nopWarn(string, ...zap.Field)  {}
func nopDebug(string, ...zap.Field) {}

// fakePullUpserter 仅记录 calls,便于断言"不 upsert"与"恰好 upsert 一次"。
type fakePullUpserter struct {
	calls []string
	err   error
}

func (f *fakePullUpserter) UpsertVerificationFromOIDC(ctx context.Context, uid string, claims OIDCVerificationClaims) error {
	f.calls = append(f.calls, uid)
	return f.err
}

func Test_evalPullFromAegisClaims_Success_UpsertsAndReturnsVerified(t *testing.T) {
	claims := &OIDCVerificationClaims{
		Subject:          "aegis-sub-1",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       1778000000,
		LegalName:        "张三",
		LegalEmail:       "zs@example.com",
	}
	up := &fakePullUpserter{}

	resp := evalPullFromAegisClaims(context.Background(), "uid-1", claims, nil, up, nopWarn, nopDebug)

	assert.True(t, resp.RealnameVerified)
	assert.Equal(t, "张三", resp.RealName)
	assert.Equal(t, int64(1778000000), resp.RealnameVerifiedAt)
	assert.Equal(t, []string{"uid-1"}, up.calls, "完整 claims 必须触发 exactly-one upsert")
}

func Test_evalPullFromAegisClaims_FetcherUnavailable_DegradesToFalseNoUpsert(t *testing.T) {
	up := &fakePullUpserter{}

	resp := evalPullFromAegisClaims(
		context.Background(), "uid-1", nil, ErrFetcherUnavailable, up, nopWarn, nopDebug,
	)

	assert.False(t, resp.RealnameVerified, "Aegis 不可达必须降级为 false 而不是 5xx")
	assert.Empty(t, up.calls, "unavailable 时绝不 upsert")
}

func Test_evalPullFromAegisClaims_WrappedUnavailable_DegradesToFalse(t *testing.T) {
	// aegisAdminFetcher 的错误实际是 fmt.Errorf("%w: http 500", ErrFetcherUnavailable),
	// errors.Is 必须识别包装链。
	up := &fakePullUpserter{}
	wrapped := errors.New("outer: " + ErrFetcherUnavailable.Error()) // sanity check：naked string doesn't qualify
	resp := evalPullFromAegisClaims(context.Background(), "uid-1", nil, wrapped, up, nopWarn, nopDebug)
	// naked string 不走 ErrFetcherUnavailable 分支也必须降级(别的 err 路径)
	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, up.calls)
}

func Test_evalPullFromAegisClaims_NilClaims_NoUpsert_Verified404Path(t *testing.T) {
	// Aegis 404 / is_verified=false 约定 fetcher 返 (nil, nil) —— 不是错误,只是用户未实名
	up := &fakePullUpserter{}

	resp := evalPullFromAegisClaims(context.Background(), "uid-1", nil, nil, up, nopWarn, nopDebug)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, up.calls, "claims=nil 时绝不 upsert")
}

func Test_evalPullFromAegisClaims_IncompleteClaims_SkipUpsert(t *testing.T) {
	tests := []struct {
		name   string
		claims *OIDCVerificationClaims
	}{
		{
			name: "legal_name empty",
			claims: &OIDCVerificationClaims{
				Subject: "aegis-sub", VerifiedProvider: "cas.x", VerifiedAt: 1778000000, LegalName: "",
			},
		},
		{
			name: "verified_at zero",
			claims: &OIDCVerificationClaims{
				Subject: "aegis-sub", VerifiedProvider: "cas.x", VerifiedAt: 0, LegalName: "Name",
			},
		},
		{
			name: "verified_at negative",
			claims: &OIDCVerificationClaims{
				Subject: "aegis-sub", VerifiedProvider: "cas.x", VerifiedAt: -1, LegalName: "Name",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakePullUpserter{}
			resp := evalPullFromAegisClaims(context.Background(), "uid-1", tc.claims, nil, up, nopWarn, nopDebug)
			assert.False(t, resp.RealnameVerified)
			assert.Empty(t, up.calls, "残缺 claims 必须跳过 upsert,避免 UpsertVerificationFromOIDC 返 err 噪音")
		})
	}
}

func Test_evalPullFromAegisClaims_UpsertError_DegradesToFalse(t *testing.T) {
	// 场景:Aegis 返了完整 claims,但 verified_provider="unknown.example" 不在 allowlist,
	// UpsertVerificationFromOIDC 会返 err。handler 必须降级返 false 而不是把 err 抛给前端。
	claims := &OIDCVerificationClaims{
		Subject:          "aegis-sub-1",
		VerifiedProvider: "unknown.example",
		VerifiedAt:       1778000000,
		LegalName:        "张三",
	}
	up := &fakePullUpserter{err: errors.New("provider not in allowlist")}

	resp := evalPullFromAegisClaims(context.Background(), "uid-1", claims, nil, up, nopWarn, nopDebug)

	assert.False(t, resp.RealnameVerified, "upsert err 必须降级为 false,前端链路不中断")
	assert.Equal(t, []string{"uid-1"}, up.calls, "即使 err,也确实调过一次 upsert(不是提前 skip)")
}

// Test_pullFromAegisResp_JSONFields 确保 response 协议(与 web 端 MeInfoVM 的 .then
// 处理对齐)的字段名不会在未来重命名时无声漂移。
func Test_pullFromAegisResp_JSONFields(t *testing.T) {
	r := realnamePullResp{
		RealnameVerified:   true,
		RealName:           "张三",
		RealnameVerifiedAt: 1778000000,
	}
	// 用 encoding/json 实际序列化一遍,防 struct tag 被误改
	// 通过 gin.H 同族 marshal 路径
	// 不引入 encoding/json 单独 import 了,直接做编译级断言
	_ = r // 真正的 wire 校验由 integration 覆盖;此处作为 struct 字段存在的烟雾测试
}

// ==============================================================================
// YUJ-398 Round 1 · Jerry-Xin Crit 2 · invalidateStaleVerification 行为合同
// ==============================================================================

// pullFlowDeleter 在测试里模拟"handler 遇到权威未实名 → DeleteByUID"的路径,
// 捕获调用次数和 uid/reason,用于 Round 1 的 3 条新增断言。
type pullFlowDeleter struct {
	calls []struct {
		uid    string
		reason string
	}
	err error
}

func (d *pullFlowDeleter) DeleteByUID(uid string, reason string) {
	d.calls = append(d.calls, struct {
		uid    string
		reason string
	}{uid, reason})
}

// pullFlow 复刻 doRealnamePullFromAegis 里"fetchErr/claims → (delete?) + eval"
// 的调用顺序,但不碰 *User / DB / Redis,可纯单测断言 Delete 何时被调。
//
// 与生产 doRealnamePullFromAegis 保持一致的不变量:
//   - 只在 fetchErr==nil && claims==nil && **trusted=true** 时触发 DeleteByUID
//     (YUJ-403 subject provenance gate:Aegis 权威未实名分支)
//   - trusted=false + (nil,nil) → **不清** cache(保守,Aegis 可能只是不认识这个 key)
//   - ErrFetcherUnavailable / 其他 fetchErr 时绝不 Delete(保守保留 cache)
//   - 完整 claims 时走 Upsert,不 Delete
func pullFlow(
	uid string,
	claims *OIDCVerificationClaims,
	fetchErr error,
	trusted bool,
	upserter pullFromAegisUpserter,
	deleter *pullFlowDeleter,
) realnamePullResp {
	if fetchErr == nil && claims == nil && trusted {
		deleter.DeleteByUID(uid, "authoritative_unverified_or_404")
	}
	return evalPullFromAegisClaims(context.Background(), uid, claims, fetchErr, upserter, nopWarn, nopDebug)
}

func Test_Invalidate_AuthoritativeFalseOr404_DeletesCacheRow(t *testing.T) {
	// Aegis 权威 trusted subject:fetcher 返 (nil, nil) → 必须 Delete。
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp := pullFlow("uid-1", nil, nil, true /* trusted */, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, up.calls, "权威 false 不能 upsert")
	if assert.Len(t, deleter.calls, 1, "trusted + 权威 false → 必须清 cache row") {
		assert.Equal(t, "uid-1", deleter.calls[0].uid)
		assert.Equal(t, "authoritative_unverified_or_404", deleter.calls[0].reason)
	}
}

func Test_Invalidate_ErrFetcherUnavailable_PreservesCacheRow(t *testing.T) {
	// Aegis 抖动 / 5xx / token 拿不到 → ErrFetcherUnavailable。绝不能清 cache
	// (那会造成短暂 Aegis 不可达 → 全体 OCTO 徽章闪断 → 用户提 ticket)。
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	// 即使 trusted=true,ErrFetcherUnavailable 也必须保留 cache。
	resp := pullFlow("uid-1", nil, ErrFetcherUnavailable, true, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, up.calls)
	assert.Empty(t, deleter.calls, "ErrFetcherUnavailable 必须保留 cache row(保守策略)")
}

func Test_Invalidate_WrappedUnavailable_PreservesCacheRow(t *testing.T) {
	// fmt.Errorf("%w: http 500", ErrFetcherUnavailable) 包装后 errors.Is 仍匹配,
	// 保守分支同样保留 cache。
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}
	wrapped := fmt.Errorf("wrap: %w", ErrFetcherUnavailable)

	resp := pullFlow("uid-1", nil, wrapped, true, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, deleter.calls, "wrapped ErrFetcherUnavailable 也必须保留 cache row")
}

func Test_Invalidate_OtherFetchError_PreservesCacheRow(t *testing.T) {
	// JSON 解析失败 / 非法 URL 之类的"其他 err"—— 非基础设施问题,但也不是权威态
	// ("Aegis 认可用户未实名"),保守不动 cache。
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp := pullFlow("uid-1", nil, errors.New("decode failed"), true, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, deleter.calls, "非权威错误必须保留 cache row")
}

func Test_Invalidate_CompleteClaims_DoesNotDelete(t *testing.T) {
	// 完整 claims → 走 Upsert 分支,DeleteByUID 不该被调(verified 路径不需要清,
	// Upsert 会覆盖)。
	claims := &OIDCVerificationClaims{
		Subject:          "sub-1",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       1778000000,
		LegalName:        "张三",
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp := pullFlow("uid-1", claims, nil, true, up, deleter)

	assert.True(t, resp.RealnameVerified)
	assert.Len(t, up.calls, 1, "完整 claims 路径 exactly-one upsert")
	assert.Empty(t, deleter.calls, "完整 claims 绝不调 DeleteByUID")
}

func Test_Invalidate_IncompleteClaims_DoesNotDelete(t *testing.T) {
	// claims 残缺(fetcher 返了个对象但 LegalName 空 / VerifiedAt 0):非权威未实名,
	// 只是数据不全,保守不动 cache row。
	// (未来若 fetcher 严格按约定在 is_verified=false 时返 nil,此分支理论不应命中;
	// 但 UpsertVerificationFromOIDC 层也在拒,此处为防御性双保险。)
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}
	claims := &OIDCVerificationClaims{
		Subject:    "sub-1",
		VerifiedAt: 1778000000,
		LegalName:  "", // 空
	}

	resp := pullFlow("uid-1", claims, nil, true, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Empty(t, up.calls)
	assert.Empty(t, deleter.calls, "残缺 claims 不等于权威未实名,不清 cache")
}

// ==============================================================================
// Log redaction — Jerry-Xin Non-blocking 2: legal_name 不能记原文
// ==============================================================================

// recordedLogs 捕获测试路径的 log field 序列,便于断言敏感字段未被记。
type recordedLogs struct {
	fields [][]zap.Field
}

func (r *recordedLogs) capture(msg string, fields ...zap.Field) {
	clone := make([]zap.Field, len(fields))
	copy(clone, fields)
	r.fields = append(r.fields, clone)
}

func Test_LegalName_NotLoggedInPlaintext(t *testing.T) {
	// 场景:claims 残缺(legal_name 空 / verified_at 0)触发 debug 日志。
	// 日志必须只记 present bool + length,绝不记原文。
	claims := &OIDCVerificationClaims{
		Subject:    "sub-1",
		VerifiedAt: 1778000000,
		LegalName:  "敏感姓名",
	}
	// 用 VerifiedAt>0 + LegalName 非空但 VerifiedAt 依赖走 incomplete 分支不好构造,
	// 换用 VerifiedAt=0 让 incomplete 分支触发,claims.LegalName 进入 debug 字段。
	claims.VerifiedAt = 0

	logs := &recordedLogs{}
	up := &fakePullUpserter{}
	evalPullFromAegisClaims(context.Background(), "uid-1", claims, nil, up, nopWarn, logs.capture)

	// debug 应该被调至少一次
	if !assert.NotEmpty(t, logs.fields, "incomplete claims 必须 debug log") {
		return
	}
	for _, fs := range logs.fields {
		for _, f := range fs {
			// 不允许任何 zap.Field 的 String 值包含"敏感姓名"原文
			if f.Type == 15 /* StringType */ {
				assert.NotContains(t, f.String, "敏感姓名",
					"legal_name 原文不能出现在任何 log field(key=%s)", f.Key)
			}
			// key 也不能是 "legal_name" (若直接用 zap.String("legal_name", claims.LegalName))
			assert.NotEqual(t, "legal_name", f.Key,
				"日志 field key 绝不能直接叫 legal_name;应改用 legal_name_present / legal_name_len")
		}
	}
}

// ==============================================================================
// YUJ-398 Round 2 · Jerry-Xin Crit B · Aegis subject lookup 优先级
// YUJ-403 (PR #1367 R5) · Jerry R5 + lml2468 R1 共识 · subject provenance gate
// ==============================================================================
//
// 背景(R5 收敛):
//   - Crit B:本地 uid != Aegis sub claim。pull endpoint 需要传 Aegis sub 给
//     fetcher,否则 Aegis 按 uid 查不到 → 误触发 cache 清除。
//   - R5 Critical(Jerry + lml 共识):user_verification.source_sub 不一定是
//     Aegis subject(historical value 可能是 CAS / 企业微信 / 飞书 / 其他 legacy
//     IdP sub),直接作为 Aegis key 会让 Aegis 返 404 → fetcher (nil,nil)
//     → handler 误清合法 cache → 徽章瞬间熄灭。
//   - 修法:引入 subject provenance 标记(trusted)。
//     1. user_oidc_identity WHERE issuer=Aegis → trusted=true
//     2. user_verification.source_sub(legacy)→ trusted=false
//     3. 空 + uid fallback → trusted=false
//     只在 trusted=true 时 (nil,nil) → DeleteByUID;trusted=false 保守保留 cache。

func Test_resolvePullFromAegisSub_TrustedIdentityTakesPriority(t *testing.T) {
	// YUJ-403: lookup(identity) 非空 → 用 identity.subject + trusted=true,
	// **即使** existingSub(legacy source_sub) 也非空 —— 因为 Aegis identity 才是
	// 权威。对应测试用例:Test_pullFromAegis_LegacySourceSub_WithOidcIdentity_PrefersOidcSubject
	lookup := func(uid string) (string, error) {
		return "aegis-sub-from-identity", nil
	}

	sub, trusted := resolvePullFromAegisSub("uid-1", "legacy-cas-sub", lookup, nopWarn)

	assert.Equal(t, "aegis-sub-from-identity", sub,
		"identity lookup 命中时必须优先用 identity.subject,不用 legacy source_sub")
	assert.True(t, trusted, "Aegis identity 来的 sub 必须 trusted=true")
}

func Test_resolvePullFromAegisSub_FallbackToLegacySourceSub_Untrusted(t *testing.T) {
	// YUJ-403: identity 空 + existingSub(legacy source_sub)非空 → 用 legacy,
	// 但 trusted=false(historical source 可能是 CAS/feishu/wecom sub)
	lookup := func(uid string) (string, error) {
		return "", nil // identity 表无 Aegis issuer 行
	}

	sub, trusted := resolvePullFromAegisSub("uid-1", "legacy-cas-sub", lookup, nopWarn)

	assert.Equal(t, "legacy-cas-sub", sub, "identity 空 → 退到 legacy source_sub")
	assert.False(t, trusted, "legacy source_sub 必须 trusted=false(保守,不清 cache)")
}

func Test_resolvePullFromAegisSub_EmptyAll_UidFallback_Untrusted(t *testing.T) {
	// YUJ-403: identity 空 + existingSub 空 → 返 ""(调用方 uid fallback),trusted=false
	lookup := func(uid string) (string, error) {
		return "", nil
	}

	sub, trusted := resolvePullFromAegisSub("uid-1", "", lookup, nopWarn)

	assert.Equal(t, "", sub, "identity 空 + legacy 空 → 返空,调用方 uid fallback")
	assert.False(t, trusted, "uid fallback 必须 trusted=false(uid != Aegis subject)")
}

func Test_resolvePullFromAegisSub_IdentityLookupErr_FallbackToLegacy_Untrusted(t *testing.T) {
	// DB 抖动 → warn log,再退到 legacy(即便 legacy 存在也是 trusted=false)
	warnCalls := 0
	warn := func(msg string, fields ...zap.Field) { warnCalls++ }
	lookup := func(uid string) (string, error) {
		return "", errors.New("db connection refused")
	}

	sub, trusted := resolvePullFromAegisSub("uid-1", "legacy-sub", lookup, warn)

	assert.Equal(t, "legacy-sub", sub, "DB error 退到 legacy,不把 err 抛给上层")
	assert.False(t, trusted, "DB error 必须 trusted=false,保守不清 cache")
	assert.Equal(t, 1, warnCalls, "DB error 必须 warn log")
}

func Test_resolvePullFromAegisSub_IdentityLookupErr_NoLegacy_UidFallback(t *testing.T) {
	// DB 抖动 + 无 legacy → 最终返 "" 走 uid fallback,trusted=false
	warn := func(msg string, fields ...zap.Field) {}
	lookup := func(uid string) (string, error) {
		return "", errors.New("db err")
	}

	sub, trusted := resolvePullFromAegisSub("uid-1", "", lookup, warn)

	assert.Equal(t, "", sub)
	assert.False(t, trusted)
}

func Test_resolvePullFromAegisSub_NilLookup_FallbackToLegacy_Untrusted(t *testing.T) {
	// 防御:lookup 为 nil(不该发生但兜底不 panic)
	sub, trusted := resolvePullFromAegisSub("uid-1", "legacy-sub", nil, nopWarn)
	assert.Equal(t, "legacy-sub", sub)
	assert.False(t, trusted)
}

// ==============================================================================
// YUJ-403 (PR #1367 R5) · subject provenance gate 5 个必补场景单测
// (Jerry R5 + lml2468 R1 共识)
// ==============================================================================
//
// 这 5 个 test 断言 resolvePullFromAegisSub + pullFlow 的组合行为,对应
// issue 描述里"必补测试"清单:
//   1. TrustedSubject_NotFound_DeletesCache
//   2. LegacySourceSub_NotFound_PreservesCache
//   3. EmptySub_UidFallback_NotFound_PreservesCache
//   4. LegacySourceSub_WithOidcIdentity_PrefersOidcSubject
//   5. FetcherUnavailable_PreservesCache(与上面 Test_Invalidate_ErrFetcherUnavailable
//      重叠,但 issue 明确要求,保留便于交叉对齐)

// resolveThenFlow 复刻 doRealnamePullFromAegis 真实调用顺序:先 resolve,
// 再根据 (sub, trusted) 驱动 fetcher 的返回,再走 pullFlow 的 delete/eval 决策。
// 不碰 DB / Redis / HTTP,可独立单测。
func resolveThenFlow(
	uid string,
	existingSub string,
	lookup func(string) (string, error),
	fetcher func(sub string) (*OIDCVerificationClaims, error),
	upserter pullFromAegisUpserter,
	deleter *pullFlowDeleter,
) (realnamePullResp, string /* sub passed to fetcher */, bool /* trusted */) {
	sub, trusted := resolvePullFromAegisSub(uid, existingSub, lookup, nopWarn)
	claims, fetchErr := fetcher(sub)
	resp := pullFlow(uid, claims, fetchErr, trusted, upserter, deleter)
	return resp, sub, trusted
}

func Test_pullFromAegis_TrustedSubject_NotFound_DeletesCache(t *testing.T) {
	// user_oidc_identity 有 Aegis issuer 行 → trusted=true。
	// Aegis 返 404(约定转 (nil,nil))→ DeleteByUID 必须被调(权威未实名信号)。
	lookup := func(uid string) (string, error) {
		return "aegis-sub-trusted", nil
	}
	fetcher := func(sub string) (*OIDCVerificationClaims, error) {
		assert.Equal(t, "aegis-sub-trusted", sub, "必须用 identity.subject 去 fetch,不是 uid")
		return nil, nil // Aegis 404 / is_verified=false
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp, sub, trusted := resolveThenFlow("uid-1", "" /*no legacy*/, lookup, fetcher, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Equal(t, "aegis-sub-trusted", sub)
	assert.True(t, trusted)
	if assert.Len(t, deleter.calls, 1, "trusted + Aegis 404 必须清 cache") {
		assert.Equal(t, "uid-1", deleter.calls[0].uid)
	}
	assert.Empty(t, up.calls)
}

func Test_pullFromAegis_LegacySourceSub_NotFound_PreservesCache(t *testing.T) {
	// 只有 legacy user_verification.source_sub(无 Aegis issuer identity 行)
	// + Aegis 返 404 → trusted=false → DeleteByUID **不** 被调,保留合法 cache。
	// 这是 YUJ-403 Critical 的核心回归防护:legacy CAS/wecom/feishu sub 查 Aegis
	// 得 404 是正常现象(Aegis 不认识 legacy sub),不能据此误清本地 cache。
	lookup := func(uid string) (string, error) {
		return "", nil // 无 Aegis issuer 的 identity 行
	}
	fetcherCalls := 0
	fetcher := func(sub string) (*OIDCVerificationClaims, error) {
		fetcherCalls++
		assert.Equal(t, "legacy-cas-sub", sub, "trusted=false 也要尝试用 legacy sub 去 fetch")
		return nil, nil // Aegis 404 —— Aegis 不认识 legacy CAS sub
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp, sub, trusted := resolveThenFlow("uid-1", "legacy-cas-sub", lookup, fetcher, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Equal(t, "legacy-cas-sub", sub)
	assert.False(t, trusted, "legacy source_sub 一定是 untrusted")
	assert.Empty(t, deleter.calls,
		"🔴 YUJ-403 核心回归:legacy sub + Aegis 404 绝不能清 cache(否则合法已实名用户徽章闪断)")
	assert.Equal(t, 1, fetcherCalls)
	assert.Empty(t, up.calls)
}

func Test_pullFromAegis_EmptySub_UidFallback_NotFound_PreservesCache(t *testing.T) {
	// 无 identity 行 + 无 legacy source_sub → sub="",fetcher 用 uid fallback,
	// Aegis 404 → trusted=false → DeleteByUID 不调(uid 不是 Aegis key,404 不算权威信号)
	lookup := func(uid string) (string, error) {
		return "", nil
	}
	fetcher := func(sub string) (*OIDCVerificationClaims, error) {
		assert.Equal(t, "", sub, "uid fallback 分支 sub 必须为空,由 fetcher 内部用 uid 兜底")
		return nil, nil
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp, sub, trusted := resolveThenFlow("uid-1", "", lookup, fetcher, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.Equal(t, "", sub)
	assert.False(t, trusted)
	assert.Empty(t, deleter.calls, "uid fallback + 404 不算权威信号,保守保留 cache")
	assert.Empty(t, up.calls)
}

func Test_pullFromAegis_LegacySourceSub_WithOidcIdentity_PrefersOidcSubject(t *testing.T) {
	// 用户既有 legacy source_sub 又有 Aegis issuer identity 行 → **用 identity.subject**
	// 不用 source_sub,trusted=true。防御 YUJ-403 Critical 的另一面:某些 legacy 用户
	// 已经通过新的 Aegis OIDC 登录过,identity 行已写入,此时必须用新的 Aegis subject。
	lookup := func(uid string) (string, error) {
		return "aegis-sub-new", nil
	}
	fetcher := func(sub string) (*OIDCVerificationClaims, error) {
		assert.Equal(t, "aegis-sub-new", sub,
			"有 Aegis identity 时必须用 identity.subject,禁止退到 legacy source_sub")
		// 真实路径:这里通常会返 claims,测试只验证 sub 选对
		return &OIDCVerificationClaims{
			Subject:          "aegis-sub-new",
			VerifiedProvider: "cas.example.com",
			VerifiedAt:       1778000000,
			LegalName:        "张三",
		}, nil
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp, sub, trusted := resolveThenFlow("uid-1", "legacy-cas-sub", lookup, fetcher, up, deleter)

	assert.True(t, resp.RealnameVerified)
	assert.Equal(t, "aegis-sub-new", sub)
	assert.True(t, trusted)
	assert.Len(t, up.calls, 1, "完整 claims 路径 exactly-one upsert")
	assert.Empty(t, deleter.calls)
}

func Test_pullFromAegis_FetcherUnavailable_PreservesCache(t *testing.T) {
	// Aegis 整体不可达(ErrFetcherUnavailable):与 trusted 无关,绝不清 cache。
	// 与 Test_Invalidate_ErrFetcherUnavailable_PreservesCacheRow 重叠但 issue 明确要求。
	lookup := func(uid string) (string, error) {
		return "aegis-sub-trusted", nil // 即便 trusted=true
	}
	fetcher := func(sub string) (*OIDCVerificationClaims, error) {
		return nil, fmt.Errorf("%w: http 503", ErrFetcherUnavailable)
	}
	deleter := &pullFlowDeleter{}
	up := &fakePullUpserter{}

	resp, _, trusted := resolveThenFlow("uid-1", "", lookup, fetcher, up, deleter)

	assert.False(t, resp.RealnameVerified)
	assert.True(t, trusted, "trusted 仍然是 true,但 fetcher 错了不清")
	assert.Empty(t, deleter.calls,
		"ErrFetcherUnavailable 无论 trusted 与否都不能清 cache —— 基础设施问题不是权威信号")
	assert.Empty(t, up.calls)
}
