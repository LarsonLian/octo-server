package user

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/redis"
	"go.uber.org/zap"
)

const (
	defaultLoginFailThreshold = 5
	defaultLoginFailWindow    = 15 * time.Minute
	loginFailKeyPrefix        = "login:fail:"
)

// ErrLoginLocked 表示账号因连续登录失败被临时锁定。
var ErrLoginLocked = errors.New("登录失败次数过多，账号已被临时锁定，请稍后再试")

// LoginGuard 为登录接口提供连续失败计数与临时锁定能力，防止暴力破解 / 撞库。
//
// 计数维度：按请求传入的 account（username / phone / email）归一化后作为 key，
// 未登录阶段无 uid，使用 account 能覆盖"用户不存在"的探测场景。
//
// 存储：Redis INCR + EXPIRE，首次失败时设置 TTL，TTL 过期后自动解锁。
type LoginGuard struct {
	redis     *redis.Conn
	threshold int64
	window    time.Duration
}

// NewLoginGuard 创建 LoginGuard。threshold <=0 或 window <=0 时使用默认值。
func NewLoginGuard(r *redis.Conn, threshold int64, window time.Duration) *LoginGuard {
	if threshold <= 0 {
		threshold = defaultLoginFailThreshold
	}
	if window <= 0 {
		window = defaultLoginFailWindow
	}
	return &LoginGuard{redis: r, threshold: threshold, window: window}
}

func normalizeAccount(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

func (g *LoginGuard) key(account string) string {
	return loginFailKeyPrefix + account
}

// Check 若当前失败计数已达阈值，返回 ErrLoginLocked；否则返回 nil。
// 空 account 视为无效标识，直接放行（由上层业务字段校验兜底）。
//
// 故障语义：Redis 不可用时 fail-open（仅记一条 warn），避免单点故障造成全量登录瘫痪。
// 代价是 Redis 抖动期间短暂失去暴力破解防护，但全局 IP 限流仍在生效作为兜底。
func (g *LoginGuard) Check(account string) error {
	account = normalizeAccount(account)
	if account == "" {
		return nil
	}
	s, err := g.redis.GetString(g.key(account))
	if err != nil {
		log.Warn("LoginGuard Check 读取失败，fail-open", zap.String("account", account), zap.Error(err))
		return nil
	}
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	if n >= g.threshold {
		return ErrLoginLocked
	}
	return nil
}

// RecordFailure 失败次数 +1，每次都重设 TTL。
//
// 为什么每次都 Expire：INCR+EXPIRE 非原子，若首次 Incr 成功而 Expire 失败，key 将永不过期
// 导致账号永久锁定。每次 Expire 保证即使前几次 Expire 失败，后续失败也会修复 TTL。
// 语义副作用：计数窗口从"固定窗口"变为"滑动窗口"（攻击者持续尝试时窗口会续期），
// 这对防暴力破解反而更严格，符合安全预期。
func (g *LoginGuard) RecordFailure(account string) error {
	account = normalizeAccount(account)
	if account == "" {
		return nil
	}
	key := g.key(account)
	if _, err := g.redis.Incr(key); err != nil {
		return fmt.Errorf("incr login failure: %w", err)
	}
	if err := g.redis.Expire(key, g.window); err != nil {
		return fmt.Errorf("expire login failure: %w", err)
	}
	return nil
}

// loginGuardThresholdFromEnv 从环境变量读取阈值，缺省或非法时返回 0（NewLoginGuard 会用默认值）。
func loginGuardThresholdFromEnv() int64 {
	if v := os.Getenv("DM_LOGIN_FAIL_THRESHOLD"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// loginGuardWindowFromEnv 从 DM_LOGIN_FAIL_WINDOW_SEC 读取锁定窗口（秒），
// 缺省或非法时返回 0（NewLoginGuard 会用默认值 15 分钟）。
func loginGuardWindowFromEnv() time.Duration {
	if v := os.Getenv("DM_LOGIN_FAIL_WINDOW_SEC"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

// RecordFailureLogged 包装 RecordFailure，失败时只 warn 不扩散错误，方便 handler 调用。
func (g *LoginGuard) RecordFailureLogged(account string) {
	if err := g.RecordFailure(account); err != nil {
		log.Warn("LoginGuard RecordFailure 失败", zap.String("account", account), zap.Error(err))
	}
}

// ResetLogged 包装 Reset，失败时只 warn。
func (g *LoginGuard) ResetLogged(account string) {
	if err := g.Reset(account); err != nil {
		log.Warn("LoginGuard Reset 失败", zap.String("account", account), zap.Error(err))
	}
}

// Reset 登录成功后清除失败计数。
func (g *LoginGuard) Reset(account string) error {
	account = normalizeAccount(account)
	if account == "" {
		return nil
	}
	if err := g.redis.Del(g.key(account)); err != nil {
		return fmt.Errorf("del login failure: %w", err)
	}
	return nil
}
