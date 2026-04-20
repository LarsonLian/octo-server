package user

import (
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/redis"
	"github.com/stretchr/testify/assert"
)

// newGuardRedis 直接连 testenv Redis，绕开 NewTestServer 的 MySQL 迁移依赖，
// 因为 LoginGuard 只依赖 Redis。
func newGuardRedis(t *testing.T) *redis.Conn {
	t.Helper()
	return redis.New("127.0.0.1:6379", "")
}

// 统一清理测试账号对应的限流 key，避免测试间残留。
func clearGuardKey(t *testing.T, g *LoginGuard, accounts ...string) {
	t.Helper()
	for _, a := range accounts {
		_ = g.Reset(a)
	}
}

func TestLoginGuard_Check_NoRecord(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 5, 15*time.Minute)
	clearGuardKey(t, g, "nouser")

	err := g.Check("nouser")
	assert.NoError(t, err)
}

func TestLoginGuard_RecordFailure_IncrementsUnderThreshold(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 5, 15*time.Minute)
	account := "alice"
	clearGuardKey(t, g, account)

	for i := 0; i < 4; i++ {
		assert.NoError(t, g.RecordFailure(account))
	}

	// 未达阈值时 Check 仍放行
	assert.NoError(t, g.Check(account))
}

func TestLoginGuard_Check_LocksAtThreshold(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 3, 15*time.Minute)
	account := "bob"
	clearGuardKey(t, g, account)

	for i := 0; i < 3; i++ {
		assert.NoError(t, g.RecordFailure(account))
	}

	err := g.Check(account)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrLoginLocked), "expected ErrLoginLocked, got %v", err)
}

func TestLoginGuard_Reset_ClearsCounter(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 3, 15*time.Minute)
	account := "carol"
	clearGuardKey(t, g, account)

	for i := 0; i < 3; i++ {
		assert.NoError(t, g.RecordFailure(account))
	}
	assert.Error(t, g.Check(account))

	assert.NoError(t, g.Reset(account))
	assert.NoError(t, g.Check(account))
}

func TestLoginGuard_NormalizesAccount(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 3, 15*time.Minute)
	clearGuardKey(t, g, "dave@example.com")

	assert.NoError(t, g.RecordFailure("  Dave@Example.COM  "))
	assert.NoError(t, g.RecordFailure("dave@example.com"))
	assert.NoError(t, g.RecordFailure("DAVE@EXAMPLE.COM"))

	err := g.Check("dave@example.com")
	assert.True(t, errors.Is(err, ErrLoginLocked))
}

func TestLoginGuard_EmptyAccountIsNoop(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 3, 15*time.Minute)

	assert.NoError(t, g.RecordFailure(""))
	assert.NoError(t, g.RecordFailure("   "))
	assert.NoError(t, g.Check(""))
	assert.NoError(t, g.Reset(""))
}

func TestLoginGuard_DefaultThresholdAndWindow(t *testing.T) {
	r := newGuardRedis(t)
	g := NewLoginGuard(r, 0, 0)
	assert.Equal(t, int64(defaultLoginFailThreshold), g.threshold)
	assert.Equal(t, defaultLoginFailWindow, g.window)
}

func TestLoginGuard_EnvHelpers(t *testing.T) {
	t.Setenv("DM_LOGIN_FAIL_THRESHOLD", "7")
	t.Setenv("DM_LOGIN_FAIL_WINDOW_SEC", "60")
	assert.Equal(t, int64(7), loginGuardThresholdFromEnv())
	assert.Equal(t, 60*time.Second, loginGuardWindowFromEnv())

	t.Setenv("DM_LOGIN_FAIL_THRESHOLD", "")
	t.Setenv("DM_LOGIN_FAIL_WINDOW_SEC", "")
	assert.Equal(t, int64(0), loginGuardThresholdFromEnv())
	assert.Equal(t, time.Duration(0), loginGuardWindowFromEnv())

	// 非法或非正数值应回落为默认
	t.Setenv("DM_LOGIN_FAIL_THRESHOLD", "abc")
	t.Setenv("DM_LOGIN_FAIL_WINDOW_SEC", "-1")
	assert.Equal(t, int64(0), loginGuardThresholdFromEnv())
	assert.Equal(t, time.Duration(0), loginGuardWindowFromEnv())
}

func TestLoginGuard_TTLSetOnFirstFailure(t *testing.T) {
	r := newGuardRedis(t)
	window := 2 * time.Second
	g := NewLoginGuard(r, 3, window)
	account := "eve"
	clearGuardKey(t, g, account)

	assert.NoError(t, g.RecordFailure(account))
	assert.NoError(t, g.RecordFailure(account))
	assert.NoError(t, g.RecordFailure(account))
	assert.Error(t, g.Check(account))

	// TTL 过期后应自动解锁
	time.Sleep(window + 500*time.Millisecond)
	assert.NoError(t, g.Check(account))
}
