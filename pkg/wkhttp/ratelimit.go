package wkhttp

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	libwkhttp "github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const unknownIPKey = "__unknown_ip__"

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

// keyedLimiter 是按 key 维度的令牌桶限流器，IP 和 UID 限流共享此核心。
type keyedLimiter struct {
	limiters sync.Map
	rps      float64
	burst    int
}

// newKeyedLimiter 创建限流器并启动后台清理 goroutine。
// ctx 取消时 cleanupLoop 退出，避免长生命周期之外的 goroutine 泄漏（主要服务测试场景）。
func newKeyedLimiter(ctx context.Context, rps float64, burst int) *keyedLimiter {
	k := &keyedLimiter{rps: rps, burst: burst}
	go k.cleanupLoop(ctx)
	return k
}

func (k *keyedLimiter) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			k.limiters.Range(func(key, value any) bool {
				entry := value.(*limiterEntry)
				if time.Since(time.Unix(0, entry.lastSeen.Load())) > 10*time.Minute {
					k.limiters.Delete(key)
				}
				return true
			})
		}
	}
}

func (k *keyedLimiter) entryFor(key string) *limiterEntry {
	if val, ok := k.limiters.Load(key); ok {
		entry := val.(*limiterEntry)
		entry.lastSeen.Store(time.Now().UnixNano())
		return entry
	}
	entry := &limiterEntry{limiter: rate.NewLimiter(rate.Limit(k.rps), k.burst)}
	entry.lastSeen.Store(time.Now().UnixNano())
	actual, loaded := k.limiters.LoadOrStore(key, entry)
	if loaded {
		e := actual.(*limiterEntry)
		e.lastSeen.Store(time.Now().UnixNano())
		return e
	}
	return entry
}

// setRateLimitHeaders 写入标准限流响应头，allowed=false 时同时写 Retry-After。
//
// Tokens() 在 Allow() 扣减之后读取，非原子序列下存在两种可见的边界：
//  1. 并发请求间，Tokens() 可能读到其他请求刚扣减后的值，Remaining 数字小于预期但合理
//  2. Allow() 成功但 Tokens() 读到接近 0 或负值时 clamp 为 0，
//     表现为"请求成功但 Remaining=0"，客户端不应据此推断下一请求必被拒
//
// 这是性能与强一致的权衡，对防 DDoS 场景可接受。
// Retry-After 按 RFC 7231 用整秒表达，rps > 1 时总是返回 1（sub-second 不可表达）。
func setRateLimitHeaders(h http.Header, entry *limiterEntry, burst int, rps float64, allowed bool) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(burst))
	remaining := int(math.Floor(entry.limiter.Tokens()))
	if remaining < 0 {
		remaining = 0
	}
	h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		retryAfter := 1
		if rps > 0 {
			retryAfter = int(math.Ceil(1.0 / rps))
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
		h.Set("Retry-After", strconv.Itoa(retryAfter))
	}
}

// getClientIP 从请求头按优先级取客户端 IP。
// 生产架构为腾讯云 CLB 直连 Pod（pass-to-target），单层代理，XFF 只含客户端真实 IP。
// 若未来新增 CDN 或多层反代，需重新评估 rightmost XFF 的取值是否正确。
func getClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	if ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return ip
	}
	return ""
}

// RateLimitMiddleware 全局 per-IP 限流，作为 DDoS 底线（挂载点：UseGin）。
// ctx 取消时后台清理 goroutine 退出，生产通常传 context.Background()。
func RateLimitMiddleware(ctx context.Context, rps float64, burst int, excludePaths ...string) gin.HandlerFunc {
	kl := newKeyedLimiter(ctx, rps, burst)

	excludeSet := make(map[string]struct{}, len(excludePaths))
	for _, p := range excludePaths {
		excludeSet[p] = struct{}{}
	}

	var unknownIPWarnOnce sync.Once

	return func(c *gin.Context) {
		if _, ok := excludeSet[c.Request.URL.Path]; ok {
			c.Next()
			return
		}

		// fail-closed: 拿不到 IP 时走全局桶，不放行
		ip := getClientIP(c.Request)
		if ip == "" {
			unknownIPWarnOnce.Do(func() {
				log.Warn("rate limit: client IP unavailable, falling back to shared bucket; check reverse proxy / XFF configuration")
			})
			ip = unknownIPKey
		}

		entry := kl.entryFor(ip)
		allowed := entry.limiter.Allow()
		setRateLimitHeaders(c.Writer.Header(), entry, burst, rps, allowed)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"msg":    "请求过于频繁，请稍后再试",
				"status": http.StatusTooManyRequests,
			})
			return
		}

		c.Next()
	}
}

// StrictIPRateLimitMiddleware 端点级 per-IP 严格限流，挂在敏感端点（登录/注册/SMS/搜索）作为额外防护。
//
// 与全局 RateLimitMiddleware 区别：
//   - 全局：DDoS 底线，宽松阈值（数百 req/s），挂全局 UseGin
//   - 严格：暴力破解/枚举防御，紧阈值（数 req/min），挂在端点级 RouterGroup
//
// 同类端点（如所有登录端点）应共享同一个中间件实例，使同一 IP 的总配额受控，防攻击者跨端点分散：
//
//	loginLimit := wkhttp.StrictIPRateLimitMiddleware(ctx, 10.0/60, 5)
//	v.POST("/user/login", loginLimit, u.login)
//	v.POST("/user/usernamelogin", loginLimit, u.usernameLogin)
//
// fail-closed：拿不到 IP 时所有请求归入同一全局桶，与全局 RateLimitMiddleware 行为一致。
//
// ctx 取消时后台清理 goroutine 退出。
func StrictIPRateLimitMiddleware(ctx context.Context, rps float64, burst int) libwkhttp.HandlerFunc {
	kl := newKeyedLimiter(ctx, rps, burst)
	var unknownIPWarnOnce sync.Once

	return func(c *libwkhttp.Context) {
		ip := getClientIP(c.Request)
		if ip == "" {
			unknownIPWarnOnce.Do(func() {
				log.Warn("strict rate limit: client IP unavailable, falling back to shared bucket; check reverse proxy / XFF configuration")
			})
			ip = unknownIPKey
		}

		entry := kl.entryFor(ip)
		allowed := entry.limiter.Allow()
		setRateLimitHeaders(c.Writer.Header(), entry, burst, rps, allowed)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"msg":    "请求过于频繁，请稍后再试",
				"status": http.StatusTooManyRequests,
			})
			return
		}

		c.Next()
	}
}

// UIDRateLimitMiddleware 按登录用户 uid 限流。
//
// ⚠️ 挂载要求：必须挂在 AuthMiddleware 之后，且只用于认证路由组。
//
//	r.Group("/v1/foo", ctx.AuthMiddleware(r), wkhttp.UIDRateLimitMiddleware(ctx, 1, 2))
//
// Fail-open 语义：读不到 uid（未经 AuthMiddleware 或 token 无效）时直接放行，
// 不会按任何维度限流。这意味着本中间件**不具备**未认证场景的防护能力，
// 需配合全局 per-IP RateLimitMiddleware 作为底线。错误的挂载顺序会
// 导致限流静默失效，请务必用 AuthMiddleware 前置并在测试中验证。
//
// ctx 取消时后台清理 goroutine 退出。
func UIDRateLimitMiddleware(ctx context.Context, rps float64, burst int) libwkhttp.HandlerFunc {
	kl := newKeyedLimiter(ctx, rps, burst)
	return func(c *libwkhttp.Context) {
		uidAny, exists := c.Get("uid")
		if !exists {
			c.Next()
			return
		}
		uid, ok := uidAny.(string)
		if !ok || uid == "" {
			c.Next()
			return
		}

		entry := kl.entryFor(uid)
		allowed := entry.limiter.Allow()
		setRateLimitHeaders(c.Writer.Header(), entry, burst, rps, allowed)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"msg":    "请求过于频繁，请稍后再试",
				"status": http.StatusTooManyRequests,
			})
			return
		}

		c.Next()
	}
}
