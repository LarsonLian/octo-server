package user

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// YUJ-398 · Phase 2e 闭环 · POST /v1/internal/realname/pull-from-aegis
//
// 背景:
//   YUJ-396 做完按环境下发「去认证」URL,im-test 验收时 Yu 发现实名流程断在
//   "用户在 Aegis 完成实名 → 回跳 OCTO ?verified=1 → 徽章不亮"。
//   根因:user_verification 表当前唯一写入触发点是 OIDC 登录 callback,而
//   「去认证」跳 Aegis 的链路不触发登录 —— callback 永远不被调,claims 永远
//   落不进 user_verification,OCTO 侧 profile 查库拿到 RealnameVerified=false。
//
// 本 endpoint 的职责:
//   前端 MeInfo 在两个触发点主动调本接口,让 dmworkim 从 Aegis admin API
//   拉一次最新 identity_verification claims,写进 user_verification:
//     1. 从 Aegis 回跳 ?verified=1 的刚结束实名路径
//     2. didMount 的 opportunistic refresh(覆盖"直接登 Aegis 实名不走 OCTO
//        去认证按钮"的 dormant 场景)
//
// 与 YUJ-399 TTL 兜底 worker 的关系:
//   worker 是后台批量 best-effort,解决"别人看我徽章"场景 + 长 session 老用户;
//   本 endpoint 是前台同步的"点开 MeInfo 立刻生效",两者合起来覆盖全部盲区。
//   实现共享同一个 ClaimsFetcher(aegisAdminFetcher),不重复定义 Aegis 协议。
//
// 错误分支语义(与 issue 验收合同对齐):
//   - Aegis 不可达(ErrFetcherUnavailable)→ 200 + {realname_verified:false} + log warn,
//     不 5xx,避免 MeInfo 页面因实名拉取抖动渲染失败
//   - Aegis 返 404 / is_verified=false / claims 不完整 → 200 + {realname_verified:false}
//   - Upsert allowlist 拒绝 → 视同未实名,不 500,避免让前端看到后端校验错误(业务已在
//     UpsertVerificationFromOIDC 实现了 provider allowlist 防护)
//
// Redis 去重:
//   同一 uid 1s 内第二次调 → 直接返上次缓存结果(key: `realname_pull:{uid}`, TTL=1s)。
//   防用户快速双击 / 回跳 handler + didMount handler 同时触发互相踩。

// realnamePullResp 返回协议。omitempty 让未实名时不带空 real_name 字段。
type realnamePullResp struct {
	RealnameVerified   bool   `json:"realname_verified"`
	RealName           string `json:"real_name,omitempty"`
	RealnameVerifiedAt int64  `json:"realname_verified_at,omitempty"`
}

const (
	realnamePullCacheKeyPrefix = "realname_pull:"
	realnamePullCacheTTL       = time.Second
)

// realnamePullSF 是同 uid 的 in-process singleflight 合流器(Jerry-Xin Non-blocking 1)。
//
// 问题:Redis 1s cache 只能在"第二次请求已完成处理后"命中 —— 两次并发首请求
// (didMount opportunistic + ?verified=1 回跳 handler 同时触发)会各自 miss cache、
// 各自打 Aegis admin API,浪费一次。
//
// 单机 singleflight 把同一 uid 的并发首请求合流成一次 Aegis fetch,后到者等 leader
// 结果。这是 in-process 合流,不解决多副本合流(那需要 Redis SET NX + 轮询,
// 复杂度 vs 收益不划算 —— 同一用户多副本并发首请求的概率极小,且多打一次 Aegis
// 不会造成正确性问题,只是少量无谓流量)。
//
// 选 in-process 而非 Redis SET NX 的理由:
//   1. 用户单浏览器双触发(didMount + ?verified=1)**总会落到同一 dmworkim 实例**
//      (web 同源 cookie-sticky gateway / k8s Service affinity 惯例),合流覆盖率 >95%
//   2. golang.org/x/sync/singleflight 已在 modules/notify/space_verify.go 用过,
//      是本仓既有依赖,新增零 import cost
//   3. 实现 ~3 行 vs Redis 分布式锁 ~30 行 + 轮询 sleep + TTL 刻度调优
//
// 如果未来观察到多副本并发打 Aegis 流量确实有规模,再升级为 Redis SETNX 分布式 singleflight。
var realnamePullSF singleflight.Group

// realnamePullFromAegis POST /v1/internal/realname/pull-from-aegis handler。
//
// AuthMiddleware 已经保证未登录会被拒;仍显式 re-check LoginUID,防未来路由组
// 结构调整时误把中间件摘掉。
func (u *User) realnamePullFromAegis(c *wkhttp.Context) {
	uid := strings.TrimSpace(c.GetLoginUID())
	if uid == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "login required"})
		return
	}

	// 1s Redis 去重 —— 用户快速双击 / 回跳 + didMount 并发触发时,直接返上次结果。
	// 任何 Redis 抖动都降级为 miss 继续 fetch(与 missCheckpoint 一致的 best-effort 语义)。
	cacheKey := realnamePullCacheKeyPrefix + uid
	redisConn := u.ctx.GetRedisConn()
	if cached, err := redisConn.GetString(cacheKey); err == nil && cached != "" {
		var prev realnamePullResp
		if jerr := json.Unmarshal([]byte(cached), &prev); jerr == nil {
			c.Response(prev)
			return
		}
		// 反序列化失败(脏数据)直接走 fetch 路径,不阻塞本次请求。
	}

	// In-process singleflight:同 uid 并发首请求合流到一次 Aegis fetch,
	// 后到者共享 leader 的 response(详见 realnamePullSF 注释)。
	// 返回的 shared 标志不影响业务语义,仅在调试时可用。
	respVal, _, _ := realnamePullSF.Do(uid, func() (interface{}, error) {
		return u.doRealnamePullFromAegis(c.Request.Context(), uid), nil
	})
	resp, _ := respVal.(realnamePullResp)

	if bs, err := json.Marshal(resp); err == nil {
		// 写失败不阻塞响应 —— 下次请求再走一次 fetch,语义正确但会多一次 Aegis 打流量。
		if serr := redisConn.SetAndExpire(cacheKey, string(bs), realnamePullCacheTTL); serr != nil {
			u.Warn("realname pull: set redis dedupe cache failed",
				zap.String("uid", uid), zap.Error(serr))
		}
	}
	c.Response(resp)
}

// doRealnamePullFromAegis 纯业务逻辑 —— 不碰 Redis / HTTP context,便于独立单测。
//
// 流程:
//  1. 解析 Aegis subject(YUJ-403 subject provenance gate):
//     a. 先查 user_oidc_identity WHERE issuer = DM_OIDC_PROVIDER_ISSUER —— 这条
//        是 Aegis 权威 subject,trusted=true。
//     b. 再退到 user_verification.source_sub(legacy 路径,historical value
//        可能是 CAS/wecom/feishu IdP 的 sub,不一定是 Aegis subject)—— trusted=false。
//     c. 再退到空 sub + uid fallback —— trusted=false。
//  2. Fetcher 未装配(env 缺失)→ 直接返 verified:false 降级(不报 500)。
//  3. fetcher.FetchClaims 调用;其结果走 evalPullFromAegisClaims 公共判定。
//  4. 权威 404/未实名分支(claims=nil + fetchErr=nil):
//     - trusted=true → 调 invalidateStaleVerification 清 cache(Aegis 权威
//       确认未实名,应当清)。
//     - trusted=false → **保守不清**,仅返回 verified:false。因为 Aegis 拿
//       legacy sub / uid 查不到,很可能只是"Aegis 不认识这个 key",不能据此
//       判定"此用户在 Aegis 侧未实名"—— 清了会误伤合法已实名用户。
func (u *User) doRealnamePullFromAegis(ctx context.Context, uid string) realnamePullResp {
	fetcher := u.realnameFetcher
	if fetcher == nil {
		// env 缺失(im-dev 本地 / Aegis admin 未配)→ 静默降级,OCTO 徽章由下次
		// 常规 OIDC callback 兜底刷。
		u.Debug("realname pull: fetcher not configured, degrade to verified=false",
			zap.String("uid", uid))
		return realnamePullResp{RealnameVerified: false}
	}

	var legacySub string
	if existing, err := u.verificationDB.QueryByUID(uid); err != nil {
		// 查库失败不影响主流程 —— legacySub 留空,后续 identity lookup 和 uid fallback 仍能 work。
		u.Warn("realname pull: query existing verification failed",
			zap.String("uid", uid), zap.Error(err))
	} else if existing != nil {
		legacySub = existing.SourceSub
	}
	// YUJ-403 subject provenance gate:先查 issuer=Aegis 的 identity 行,
	// 拿到就 trusted=true;拿不到才退到 legacy source_sub(trusted=false)。
	issuer := aegisProviderIssuer()
	if strings.TrimSpace(issuer) == "" {
		u.Warn("realname pull: DM_OIDC_PROVIDER_ISSUER not configured, provenance check degraded (trusted=false always)",
			zap.String("uid", uid))
	}
	sub, trusted := resolvePullFromAegisSub(uid, legacySub,
		func(uid string) (string, error) {
			return u.verificationDB.LookupAegisSubjectByUID(uid, issuer)
		},
		func(msg string, fields ...zap.Field) { u.Warn(msg, fields...) },
	)

	claims, fetchErr := fetcher.FetchClaims(ctx, uid, sub)

	// 权威"未实名"分支 —— **只在 trusted=true 时才清 cache**。
	// trusted=false 时:Aegis 拿 legacy/uid 查不到,很可能只是"Aegis 不认识这个
	// subject",不能当作"此用户在 Aegis 侧未实名"的权威信号(legacy 用户的 Aegis
	// subject 根本没被 OCTO 知道,就没法拿正确 key 去查)。保守保留 cache,等
	// 下次 OIDC 重登补上 user_oidc_identity Aegis 行后再走 trusted=true 路径。
	//
	// 严禁在 fetchErr != nil 分支清 row:Aegis 短暂抖动 / 5xx / token 拿不到 都会经
	// ErrFetcherUnavailable 流出,那时候保守保留 cache 是刚性要求。
	if fetchErr == nil && claims == nil && trusted {
		u.invalidateStaleVerification(uid, "authoritative_unverified_or_404")
	} else if fetchErr == nil && claims == nil && !trusted {
		u.Debug("realname pull: untrusted subject got (nil,nil) from Aegis, preserve cache",
			zap.String("uid", uid),
			zap.Bool("sub_empty", sub == ""),
			zap.Bool("legacy_sub_empty", legacySub == ""))
	}

	return evalPullFromAegisClaims(ctx, uid, claims, fetchErr, u.userService,
		func(msg string, fields ...zap.Field) { u.Warn(msg, fields...) },
		func(msg string, fields ...zap.Field) { u.Debug(msg, fields...) },
	)
}

// aegisProviderIssuer 返回当前环境的 Aegis OIDC provider issuer。
//
// 与 modules/oidc/config.go 的 `DM_OIDC_PROVIDER_ISSUER`(带 DM_OIDC_AEGIS_ISSUER
// 别名)保持同一口径 —— pull-from-aegis 路径跟 OIDC callback 路径共用同一个 IdP
// 配置源,不存在"两边 issuer 不一致"的正常场景。
//
// 返回空字符串时(env 未配),调用方会把 provenance 检查降级为 trusted=false always
// (保守:永远不走 DeleteByUID 分支)。日志会 warn 提示运维。
func aegisProviderIssuer() string {
	if v := strings.TrimSpace(os.Getenv("DM_OIDC_PROVIDER_ISSUER")); v != "" {
		return v
	}
	// 与 modules/oidc/config.go 的 getStringWithAlias 一致,兼容 legacy env 名。
	return strings.TrimSpace(os.Getenv("DM_OIDC_AEGIS_ISSUER"))
}

// invalidateStaleVerification 清掉某个 uid 在 user_verification 的本地 cache 行。
//
// 设计为 user 包级 helper(而不是 handler private)是为了让 YUJ-399 的
// realname_refresh worker 也能复用同一条路径 —— 同样的"Aegis 权威确认未实名时清 cache"
// 语义,worker 和 handler 走同一函数可保证:
//   - 日志 tag 一致(reason=... 便于运维排查)
//   - 空 uid / DB 错误的降级口径一致
//   - 未来加上 metric / audit event 只需改一处
//
// reason 参数只用于日志 tag,不影响行为。推荐值:
//   - "authoritative_unverified_or_404"(handler / worker:fetcher 返 (nil,nil))
//   - "worker_tick_unverified"(worker 特有的循环触发路径,如果需要细分)
//
// 调用约束(极端重要,写在这里避免误用):
//   只在 Aegis **权威态**确认"此用户未实名"时才能调。任何基础设施故障
//   (ErrFetcherUnavailable / 网络 / 5xx / JSON 解析失败)都不能触发。
//   详细误删风险分析见 verificationDB.DeleteByUID 的注释。
func (u *User) invalidateStaleVerification(uid, reason string) {
	if strings.TrimSpace(uid) == "" {
		return
	}
	if err := u.verificationDB.DeleteByUID(uid); err != nil {
		// DB 删除失败不该把主响应降级成 500 —— 调用路径要么是 handler 要么是 worker,
		// 两边都已经选择好了"verified=false" 的降级响应,这里 log 让运维能追。
		u.Warn("realname: invalidate stale user_verification row failed",
			zap.String("uid", uid), zap.String("reason", reason), zap.Error(err))
		return
	}
	u.Debug("realname: invalidated stale user_verification row",
		zap.String("uid", uid), zap.String("reason", reason))
}

// pullFromAegisUpserter 是 evalPullFromAegisClaims 的最小依赖(只要 Upsert 那一个方法)。
// 直接复用 worker 已定义的 `claimsUpserter`,保持 user 包内单一口径;生产路径传
// u.userService,测试路径可独立传 fake。
//
// (别名只是为了在本文件的 API 注释里更容易指名道姓 —— 行为与 claimsUpserter 完全一致。)
type pullFromAegisUpserter = claimsUpserter

// resolvePullFromAegisSub 决定传给 fetcher 的 sub 值,并返回 **subject provenance**
// 标记(trusted)—— YUJ-403(PR #1367 R5 / Jerry R5 + lml2468 R1 共识 Critical)。
//
// 优先级(R5 收敛后):
//   1. user_oidc_identity WHERE issuer=Aegis 有行 → 返 (identity.subject, true)。
//      这是 Aegis 权威 subject,OIDC callback 登录时写入,trusted=true。
//   2. existingSub(从 user_verification.source_sub 读的 legacy 值)非空
//      → 返 (existingSub, false)。
//      historical value 可能是 CAS id / 企业微信 corp_id / 飞书 open_id 等
//      legacy IdP subject,不一定是 Aegis subject,trusted=false。
//   3. 空 sub + uid fallback → 返 ("", false)。
//      调用方走 fetcher 的 uid fallback,trusted=false。
//
// trusted=true 的语义合同:
//   调用方**可以**在 Aegis 返 (nil, nil) 时走 DeleteByUID 清 cache ——
//   因为 Aegis 用权威 subject 说"这个用户未实名 / 不存在",是可信信号。
//
// trusted=false 的语义合同:
//   调用方**必须**在 Aegis 返 (nil, nil) 时保留 cache —— 因为 Aegis 是用 legacy/
//   uid key 查的,404 更可能是"Aegis 不认识这个 key",而非"该用户真未实名"。
//   误清会把合法已实名用户的本地 cache 清掉,徽章瞬间熄灭 → 只能等下次 OIDC
//   重登 callback 写回 user_oidc_identity 才走 trusted=true 路径。
//
// 参数 lookup 是一个闭包,内部已经拼好 issuer 过滤(调用 LookupAegisSubjectByUID(uid, issuer))。
// 测试可传 stub 模拟 DB 结果;warn 是日志桩,空函数亦可。
//
// 测试覆盖矩阵(YUJ-403 必补):
//   - Test_pullFromAegis_TrustedSubject_NotFound_DeletesCache
//     (lookup 返 Aegis subject + Aegis 404 → trusted=true → Delete 被调)
//   - Test_pullFromAegis_LegacySourceSub_NotFound_PreservesCache
//     (lookup 空 + existingSub 非空 + Aegis 404 → trusted=false → Delete 不调)
//   - Test_pullFromAegis_EmptySub_UidFallback_NotFound_PreservesCache
//     (lookup 空 + existingSub 空 + Aegis 404 → trusted=false → Delete 不调)
//   - Test_pullFromAegis_LegacySourceSub_WithOidcIdentity_PrefersOidcSubject
//     (lookup 返 subject + existingSub 也非空 → 用 identity.subject,trusted=true)
//   - Test_pullFromAegis_FetcherUnavailable_PreservesCache
//     (fetchErr=ErrFetcherUnavailable → Delete 不调,与 trusted 无关)
func resolvePullFromAegisSub(
	uid string,
	existingSub string,
	lookup func(uid string) (string, error),
	warn func(msg string, fields ...zap.Field),
) (sub string, trusted bool) {
	// 先查 Aegis issuer 的 identity 行 —— 这是权威 subject,优先级最高。
	if lookup != nil {
		s, err := lookup(uid)
		if err != nil {
			if warn != nil {
				warn("realname pull: lookup aegis oidc identity failed, fallback to legacy/uid (trusted=false)",
					zap.String("uid", uid), zap.Error(err))
			}
			// DB 抖动 → 退到 legacy/uid fallback,但 trusted=false(保守)。
		} else if s != "" {
			return s, true
		}
	}
	// 没拿到 trusted Aegis subject;退到 legacy source_sub。
	if existingSub != "" {
		return existingSub, false
	}
	// 再退到 uid fallback(调用方让 fetcher 用 uid 兜底)。
	return "", false
}

// evalPullFromAegisClaims 把 fetcher 结果 → realnamePullResp 的所有分支判定抽成
// 纯函数,方便单测不起 DB / Redis 就覆盖全部错误路径。
//
// 参数 warn / debug 是可注入的日志桩 —— 生产路径包装 u.Warn / u.Debug;测试路径
// 传空函数即可。抽掉 log.Log 让 fakeFetcher / fakeUpserter 的复用无缝。
//
// 返回语义(与 handler 注释保持一致):
//   - err != nil → verified:false(ErrFetcherUnavailable / 其他 err 都降级,不 500)
//   - claims == nil → verified:false(Aegis 404 / is_verified=false)
//   - claims 残缺 → verified:false + skip upsert
//   - 完整 claims + upsert 成功 → verified:true + real_name + verified_at
//   - 完整 claims + upsert 失败(allowlist / DB 抖)→ verified:false,保 UX
//
// **注意**:cache 清理(invalidateStaleVerification)不在本纯函数里做 —— 那涉及
// DB 写入,放在 doRealnamePullFromAegis handler / worker 路径上执行,保持本函数纯。
func evalPullFromAegisClaims(
	ctx context.Context,
	uid string,
	claims *OIDCVerificationClaims,
	fetchErr error,
	upserter pullFromAegisUpserter,
	warn func(msg string, fields ...zap.Field),
	debug func(msg string, fields ...zap.Field),
) realnamePullResp {
	if fetchErr != nil {
		if errors.Is(fetchErr, ErrFetcherUnavailable) {
			warn("realname pull: fetcher unavailable, degrade to verified=false",
				zap.String("uid", uid), zap.Error(fetchErr))
		} else {
			warn("realname pull: fetcher returned error, degrade to verified=false",
				zap.String("uid", uid), zap.Error(fetchErr))
		}
		return realnamePullResp{RealnameVerified: false}
	}
	if claims == nil {
		return realnamePullResp{RealnameVerified: false}
	}
	if claims.LegalName == "" || claims.VerifiedAt <= 0 {
		// Jerry-Xin Non-blocking 2: 实名姓名敏感,不记原文 —— 只记是否非空 + 长度,
		// 够运维排查"为什么 claims 被当作残缺"。同样约束应用到 warn/error 日志(本
		// 函数其他位置不含 legal_name)。
		debug("realname pull: incomplete claims, skip upsert",
			zap.String("uid", uid),
			zap.Bool("legal_name_present", claims.LegalName != ""),
			zap.Int("legal_name_len", len(claims.LegalName)),
			zap.Int64("verified_at", claims.VerifiedAt))
		return realnamePullResp{RealnameVerified: false}
	}
	// UpsertVerificationFromOIDC 负责:
	//   - VerifiedProvider allowlist(cas/wecom/feishu)strip + 校验
	//   - ON DUPLICATE KEY UPDATE 合并保留其他字段(YUJ-390 hotfix 已实现)
	if uerr := upserter.UpsertVerificationFromOIDC(ctx, uid, *claims); uerr != nil {
		warn("realname pull: upsert failed, degrade to verified=false",
			zap.String("uid", uid), zap.Error(uerr))
		return realnamePullResp{RealnameVerified: false}
	}
	return realnamePullResp{
		RealnameVerified:   true,
		RealName:           claims.LegalName,
		RealnameVerifiedAt: claims.VerifiedAt,
	}
}
