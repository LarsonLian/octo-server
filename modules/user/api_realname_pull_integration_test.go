package user

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-lib/server"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/gocraft/dbr/v2"
	"github.com/stretchr/testify/assert"
)

// YUJ-403 (PR #1367 R5) · handler 集成测试(Jerry R5 次 Critical)。
//
// 背景:api_realname_pull_test.go 里现有的 *_test 都是对纯函数
// (evalPullFromAegisClaims / resolvePullFromAegisSub / pullFlow)的断言。
// Jerry R5 + lml2468 R1 评审指出这不足以保证"handler 真实 HTTP 路径"正确,
// 漏网场景包括 AuthMiddleware 401/200、Redis 1s dedupe、singleflight 合流、
// QueryByUID → LookupAegisSubjectByUID → FetchClaims → upsert/delete/skip 串联。
//
// 本文件用 testutil.NewTestServer 起真 MySQL + Redis,通过 register.GetModuleByName
// 拿到 module 注册的 *User 实例注入 mock ClaimsFetcher,走 s.GetRoute().ServeHTTP,
// 覆盖 7 条硬性合同:
//   1. Integration_UpsertSuccess
//   2. Integration_TrustedSubject_404_Deletes
//   3. Integration_LegacySourceSub_404_DoesNotDelete
//   4. Integration_FetcherUnavailable_DoesNotDelete
//   5. Integration_Dedupe_1s_Per_Uid
//   6. Integration_Singleflight_ConcurrentMerge
//   7. Integration_AuthMiddleware_401_Unauth
//
// 关键实现细节(踩过的坑):
//   testutil.NewTestServer() 内部调 module.Setup(ctx) 已经会调 User.Route() 注册
//   /v1/internal/realname/pull-from-aegis。测试 Code 里**绝不能**再手动 u.Route() —
//   gin 会 panic "handlers are already registered"。
//
//   同时 register.GetModules 用 sync.Once 缓存 module list:全部测试进程里 *User
//   实例只构造一次,跨 subtest 共享。因此每个 test 退出前必须 Cleanup reset
//   realnameFetcher,避免下一个 test 拿到脏 fetcher。

// mockClaimsFetcher 是 handler 集成测试里的 ClaimsFetcher 替身。
// 行为由 handleFn 闭包决定(可模拟 404 / 5xx / 成功 / 慢速响应)。
type mockClaimsFetcher struct {
	handleFn func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error)
	calls    int32
}

func (m *mockClaimsFetcher) FetchClaims(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
	atomic.AddInt32(&m.calls, 1)
	if m.handleFn == nil {
		return nil, nil
	}
	return m.handleFn(ctx, uid, sub)
}

func (m *mockClaimsFetcher) Calls() int32 { return atomic.LoadInt32(&m.calls) }

// pullFromAegisTestEnv 是集成测试的最小脚手架。
type pullFromAegisTestEnv struct {
	s    *server.Server
	ctx  *config.Context
	u    *User // module-registered User 实例(跨 subtest 共享,通过 register.GetModuleByName 取回)
	mock *mockClaimsFetcher
	db   *dbr.Session
}

// newPullFromAegisTestEnv 起一个干净 DB + Redis 的测试 server,注入 mock fetcher。
//
// 使用模式:
//     env := newPullFromAegisTestEnv(t, mockHandleFn)
//     env.seedAegisIdentity(testutil.UID, "aegis-sub")
//     env.serve(w, newPullFromAegisRequest(true))
func newPullFromAegisTestEnv(
	t *testing.T,
	fetch func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error),
) *pullFromAegisTestEnv {
	t.Helper()
	s, ctx := testutil.NewTestServer()
	// module.Setup 已经 route 完毕;此处通过 register 拿回 module 注册的 *User 实例。
	modUser, ok := register.GetModuleByName("user", ctx).SetupAPI().(*User)
	assert.True(t, ok, "module-registered user APIRouter 必须是 *User 实例")

	mock := &mockClaimsFetcher{handleFn: fetch}
	prev := modUser.realnameFetcher
	modUser.realnameFetcher = mock
	t.Cleanup(func() {
		// 恢复 fetcher,避免污染下一个 subtest / Test 函数。
		modUser.realnameFetcher = prev
	})

	return &pullFromAegisTestEnv{
		s:    s,
		ctx:  ctx,
		u:    modUser,
		mock: mock,
		db:   ctx.DB().NewSession(nil),
	}
}

// seedAegisIdentity 往 user_oidc_identity 塞一行 issuer=Aegis 的 trusted 映射。
func (e *pullFromAegisTestEnv) seedAegisIdentity(t *testing.T, uid, issuer, subject string) {
	t.Helper()
	_, err := e.db.InsertBySql(
		"INSERT INTO user_oidc_identity "+
			"(uid, issuer, subject, email, email_verified, phone, phone_verified) "+
			"VALUES (?, ?, ?, ?, 0, ?, 0)",
		uid, issuer, subject, "", "",
	).Exec()
	assert.NoError(t, err, "seed user_oidc_identity")
}

// seedVerification 往 user_verification 塞一行 legacy row。
func (e *pullFromAegisTestEnv) seedVerification(t *testing.T, uid, sourceSub, realName string) {
	t.Helper()
	_, err := e.db.InsertBySql(
		"INSERT INTO user_verification "+
			"(user_id, real_name, source, source_sub, email, verified_at) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		uid, realName, "cas.example.com", sourceSub, "", time.Now(),
	).Exec()
	assert.NoError(t, err, "seed user_verification")
}

// hasVerificationRow 查 user_verification 是否还有某 uid 的 row。
func (e *pullFromAegisTestEnv) hasVerificationRow(t *testing.T, uid string) bool {
	t.Helper()
	var count int
	_, err := e.db.SelectBySql("SELECT COUNT(*) FROM user_verification WHERE user_id=?", uid).Load(&count)
	assert.NoError(t, err)
	return count > 0
}

// clearDedupe 清理 Redis 1s 去重 key,subtest 之间防串扰。
func (e *pullFromAegisTestEnv) clearDedupe(uid string) {
	conn := e.ctx.GetRedisConn()
	_ = conn.SetAndExpire(realnamePullCacheKeyPrefix+uid, "", time.Millisecond)
	// 同时让 in-process singleflight 的 key 释放 —— singleflight.Do 完成后即释放,
	// 但若上一 test panic 了可能残留 Forget 一次。
	realnamePullSF.Forget(uid)
	time.Sleep(10 * time.Millisecond)
}

// serve 执行一次 HTTP 请求。
func (e *pullFromAegisTestEnv) serve(w http.ResponseWriter, req *http.Request) {
	e.s.GetRoute().ServeHTTP(w, req)
}

// route 暴露底层 router,用来保证类型签名(未来如果 wkhttp 换签名会一眼识破)。
var _ = (*wkhttp.WKHttp)(nil)

// newPullFromAegisRequest 构造带合法 token 的 POST /v1/internal/realname/pull-from-aegis 请求。
func newPullFromAegisRequest(withToken bool) *http.Request {
	req, _ := http.NewRequest(
		"POST", "/v1/internal/realname/pull-from-aegis",
		bytes.NewReader([]byte("{}")),
	)
	if withToken {
		req.Header.Set("token", testutil.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// --- Integration tests ---------------------------------------------------

func TestIntegration_pullFromAegisHandler_UpsertSuccess(t *testing.T) {
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		return &OIDCVerificationClaims{
			Subject:          sub,
			VerifiedProvider: "cas.example.com",
			VerifiedAt:       1778000000,
			LegalName:        "张三",
			LegalEmail:       "zs@example.com",
		}, nil
	})
	env.clearDedupe(testutil.UID)
	env.seedAegisIdentity(t, testutil.UID, "https://accounts.imocto.cn", "aegis-sub-1")

	w := httptest.NewRecorder()
	env.serve(w, newPullFromAegisRequest(true))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `"realname_verified":true`)
	assert.Contains(t, body, `"real_name":"张三"`)
	assert.Contains(t, body, `"realname_verified_at":1778000000`)
	assert.True(t, env.hasVerificationRow(t, testutil.UID),
		"upsert 成功后 user_verification row 必须存在")
	assert.Equal(t, int32(1), env.mock.Calls())
}

func TestIntegration_pullFromAegisHandler_TrustedSubject_404_Deletes(t *testing.T) {
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		return nil, nil // Aegis 404 / is_verified=false
	})
	env.clearDedupe(testutil.UID)
	// trusted: 有 Aegis identity 行
	env.seedAegisIdentity(t, testutil.UID, "https://accounts.imocto.cn", "aegis-sub-1")
	env.seedVerification(t, testutil.UID, "aegis-sub-1", "旧名字")
	assert.True(t, env.hasVerificationRow(t, testutil.UID), "pre-seed")

	w := httptest.NewRecorder()
	env.serve(w, newPullFromAegisRequest(true))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"realname_verified":false`)
	assert.False(t, env.hasVerificationRow(t, testutil.UID),
		"🔴 trusted Aegis subject + Aegis 404 必须删 user_verification row(权威未实名)")
}

func TestIntegration_pullFromAegisHandler_LegacySourceSub_404_DoesNotDelete(t *testing.T) {
	// YUJ-403 核心回归:legacy source_sub(非 Aegis subject)+ Aegis 404 → 保守保留 cache
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	receivedSub := ""
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		receivedSub = sub
		return nil, nil // Aegis 404 模拟 —— Aegis 不认识 legacy CAS sub
	})
	env.clearDedupe(testutil.UID)
	// **不** seed Aegis identity — 仅 legacy source_sub
	env.seedVerification(t, testutil.UID, "legacy-cas-id", "李四")
	assert.True(t, env.hasVerificationRow(t, testutil.UID), "pre-seed")

	w := httptest.NewRecorder()
	env.serve(w, newPullFromAegisRequest(true))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"realname_verified":false`)
	assert.Equal(t, "legacy-cas-id", receivedSub,
		"legacy 路径必须把 source_sub 传给 fetcher")
	assert.True(t, env.hasVerificationRow(t, testutil.UID),
		"🔴 YUJ-403:legacy sub + Aegis 404 必须**保留** user_verification row")
}

func TestIntegration_pullFromAegisHandler_FetcherUnavailable_DoesNotDelete(t *testing.T) {
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		return nil, fmt.Errorf("%w: http 503", ErrFetcherUnavailable)
	})
	env.clearDedupe(testutil.UID)
	// 即使 trusted,infra 问题仍必须保留 cache
	env.seedAegisIdentity(t, testutil.UID, "https://accounts.imocto.cn", "aegis-sub-1")
	env.seedVerification(t, testutil.UID, "aegis-sub-1", "王五")

	w := httptest.NewRecorder()
	env.serve(w, newPullFromAegisRequest(true))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"realname_verified":false`)
	assert.True(t, env.hasVerificationRow(t, testutil.UID),
		"ErrFetcherUnavailable 无论 trusted 与否都必须保留 cache row")
}

func TestIntegration_pullFromAegisHandler_Dedupe_1s_Per_Uid(t *testing.T) {
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		return &OIDCVerificationClaims{
			Subject:          sub,
			VerifiedProvider: "cas.example.com",
			VerifiedAt:       1778000000,
			LegalName:        "张三",
		}, nil
	})
	env.clearDedupe(testutil.UID)
	env.seedAegisIdentity(t, testutil.UID, "https://accounts.imocto.cn", "aegis-sub-1")

	// 第一次请求 → fetcher 调 1 次
	w1 := httptest.NewRecorder()
	env.serve(w1, newPullFromAegisRequest(true))
	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, w1.Body.String(), `"realname_verified":true`)
	firstCalls := env.mock.Calls()

	// 紧跟第二次请求(< 1s)→ 命中 Redis 缓存
	w2 := httptest.NewRecorder()
	env.serve(w2, newPullFromAegisRequest(true))
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), `"realname_verified":true`)
	assert.Equal(t, firstCalls, env.mock.Calls(),
		"1s 内第二次请求必须命中 Redis dedupe,fetcher 不该再被调")

	// 等 >1s TTL 过期后再请求,fetcher 应被再次调(证明 TTL 控制生效,不是永久粘)
	time.Sleep(1100 * time.Millisecond)
	w3 := httptest.NewRecorder()
	env.serve(w3, newPullFromAegisRequest(true))
	assert.Equal(t, http.StatusOK, w3.Code)
	assert.Greater(t, env.mock.Calls(), firstCalls,
		"TTL 到期后 fetcher 必须被再次调用,否则退化为永久缓存")
}

func TestIntegration_pullFromAegisHandler_Singleflight_ConcurrentMerge(t *testing.T) {
	// 同 uid 并发首请求合流成一次 fetch(in-process singleflight)。
	// 用 channel 让 fetcher 在 leader 到达后阻塞,保证 follower 进来时 leader 还没返回。
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		// 只有 leader 会走到这里;follower 在 singleflight.Do 里等。
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return &OIDCVerificationClaims{
			Subject:          sub,
			VerifiedProvider: "cas.example.com",
			VerifiedAt:       1778000000,
			LegalName:        "张三",
		}, nil
	})
	env.clearDedupe(testutil.UID)
	env.seedAegisIdentity(t, testutil.UID, "https://accounts.imocto.cn", "aegis-sub-1")

	const n = 5
	var wg sync.WaitGroup
	codes := make([]int, n)
	bodies := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			env.serve(w, newPullFromAegisRequest(true))
			codes[i] = w.Code
			bodies[i] = w.Body.String()
		}()
	}

	// 等 leader 到达 fetcher
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		wg.Wait()
		t.Fatal("leader 没在 3s 内进 fetcher —— singleflight 或路由出问题了")
	}

	// 给 followers 一小段时间进入 singleflight.Do 等待
	time.Sleep(50 * time.Millisecond)

	// 让 leader 释放 —— followers 会共享 leader 结果
	close(release)
	wg.Wait()

	for i, c := range codes {
		assert.Equal(t, http.StatusOK, c, "req %d: %s", i, bodies[i])
		assert.Contains(t, bodies[i], `"realname_verified":true`, "req %d", i)
	}
	// n 个并发首请求合流 → fetcher 只被 leader 调一次
	// (Redis 1s dedupe 也会短路部分 follower,两个机制叠加确保 ≤1)
	assert.LessOrEqual(t, env.mock.Calls(), int32(1),
		"singleflight 必须把 %d 个并发首请求合流到 ≤1 次 fetcher 调用,实际 %d 次",
		n, env.mock.Calls())
}

func TestIntegration_pullFromAegisHandler_AuthMiddleware_401_Unauth(t *testing.T) {
	t.Setenv("DM_OIDC_PROVIDER_ISSUER", "https://accounts.imocto.cn")
	fetched := int32(0)
	env := newPullFromAegisTestEnv(t, func(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
		atomic.AddInt32(&fetched, 1)
		return nil, nil
	})

	// 不带 token
	w := httptest.NewRecorder()
	env.serve(w, newPullFromAegisRequest(false))

	assert.NotEqual(t, http.StatusOK, w.Code,
		"未登录访问 /v1/internal/realname/pull-from-aegis 必须被 AuthMiddleware 拦截")
	assert.True(t, w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden,
		"期望 401/403,实际 %d (%s)", w.Code, strings.TrimSpace(w.Body.String()))
	assert.Equal(t, int32(0), atomic.LoadInt32(&fetched),
		"未鉴权请求绝不能调到 fetcher")
}
