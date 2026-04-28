package oidc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// 拉起跨模块迁移依赖,与 db_integration_test.go 同因。
	_ "github.com/Mininglamp-OSS/octo-server/modules/group"
	_ "github.com/Mininglamp-OSS/octo-server/modules/robot"
)

// 抢空锁应成功;同 key 第二次抢应失败(他人持锁)。
func TestRedisTickLock_AcquireExclusive_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	key := "oidc:sync:test:exclusive:" + uniqSuffix(t)
	bg := context.Background()

	got, err := l.Acquire(bg, key, "tok-A", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, got, "first Acquire on empty key should succeed")

	got2, err := l.Acquire(bg, key, "tok-B", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, got2, "second Acquire while held must fail")

	// 清理
	released, err := l.Release(bg, key, "tok-A")
	require.NoError(t, err)
	assert.True(t, released)
}

// 仅 token 匹配的 owner 才能 Release;其他 token 调 Release 不能误删别人的锁。
func TestRedisTickLock_ReleaseOnlyByOwner_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	key := "oidc:sync:test:owner:" + uniqSuffix(t)
	bg := context.Background()

	_, err := l.Acquire(bg, key, "owner-token", 5*time.Second)
	require.NoError(t, err)

	// 非 owner 不能释放
	released, err := l.Release(bg, key, "evil-token")
	require.NoError(t, err)
	assert.False(t, released, "non-owner Release must fail without error")

	// 锁仍由 owner 持有 → 第三方不能抢
	got, err := l.Acquire(bg, key, "third", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, got, "lock should still be held after evil Release attempt")

	// owner 释放成功
	ok, err := l.Release(bg, key, "owner-token")
	require.NoError(t, err)
	assert.True(t, ok)
}

// 不存在的 key 调 Release 返 (false, nil),不当作错误。
func TestRedisTickLock_ReleaseNonExistent_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	released, err := l.Release(context.Background(),
		"oidc:sync:test:noexist:"+uniqSuffix(t), "any")
	require.NoError(t, err)
	assert.False(t, released)
}

// TTL 到期后,新 token 可以抢到,等价于上一 owner 自然释放。
func TestRedisTickLock_AcquireAfterTTL_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	key := "oidc:sync:test:ttl:" + uniqSuffix(t)
	bg := context.Background()

	got, err := l.Acquire(bg, key, "old", 500*time.Millisecond)
	require.NoError(t, err)
	require.True(t, got)

	time.Sleep(700 * time.Millisecond) // 等 TTL 过期

	got2, err := l.Acquire(bg, key, "new", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, got2, "new acquire after TTL should succeed")

	// 此时旧 owner 调 Release 应返 false(token 不匹配新 owner)
	released, err := l.Release(bg, key, "old")
	require.NoError(t, err)
	assert.False(t, released, "stale owner must not delete new owner's lock (Lua CAS-DEL)")

	// 新 owner 仍持有
	got3, err := l.Acquire(bg, key, "third", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, got3, "new owner's lock survived stale Release attempt")

	_, _ = l.Release(bg, key, "new")
}

// N 个 goroutine 并发 Acquire 同 key,应只有 1 个赢。
func TestRedisTickLock_ConcurrentAcquire_OnlyOneWins_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	key := "oidc:sync:test:concurrent:" + uniqSuffix(t)
	bg := context.Background()

	const N = 20
	var wins int32
	var wg sync.WaitGroup
	winnerToken := make(chan string, 1)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, _ := NewRandomString(8)
			ok, err := l.Acquire(bg, key, tok, 10*time.Second)
			if err != nil {
				t.Errorf("Acquire err: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&wins, 1)
				select {
				case winnerToken <- tok:
				default:
				}
			}
		}(i)
	}
	wg.Wait()
	assert.EqualValues(t, 1, atomic.LoadInt32(&wins),
		"exactly 1 of %d concurrent Acquires should win", N)

	tok := <-winnerToken
	_, _ = l.Release(bg, key, tok)
}

// 入参校验:空 key/token/ttl=0 应直接报错,不打 Redis。
func TestRedisTickLock_InvalidArgs_Integration(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	l := newRedisTickLock(ctx)
	defer l.Close()

	bg := context.Background()
	cases := []struct {
		name string
		key  string
		tok  string
		ttl  time.Duration
	}{
		{"empty key", "", "t", time.Second},
		{"empty token", "k", "", time.Second},
		{"zero ttl", "k", "t", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := l.Acquire(bg, c.key, c.tok, c.ttl)
			assert.Error(t, err)
		})
	}
}

// uniqSuffix 防并行测试 key 冲突。
func uniqSuffix(t *testing.T) string {
	t.Helper()
	s, err := NewRandomString(6)
	if err != nil {
		t.Fatalf("uniq: %v", err)
	}
	return s
}
