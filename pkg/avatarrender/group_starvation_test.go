package avatarrender

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 群头像（RenderGroup / RenderIcon）与个人头像走同一套按需渲染 + 同一个进程级共享
// 缓存（GetOrRender）。本文件把 issue#480 的 starvation 复现/修复验证延伸到**群路径**，
// 证明群 avatarGet 接入共享缓存后，成员/会话列表扇出不再把 CPU 打满、饿死同机其它请求。
// 复用 starvation_repro_test.go 的 victimWork / medianVictim（同包）。

// TestGroupRenderCost 量化群渲染单次成本，坐实「群渲染重」（RenderIcon 比文字更重）。
func TestGroupRenderCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skip render cost in -short")
	}
	style := GroupStyleForSeed("warm")
	if _, err := RenderGroup("研发", style, DefaultSize); err != nil { // 预热字体
		t.Fatalf("render group: %v", err)
	}
	const n = 20
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := RenderGroup("架构讨论", style, DefaultSize); err != nil {
			t.Fatalf("render group: %v", err)
		}
	}
	t.Logf("单次 RenderGroup ≈ %v (n=%d, 串行)", time.Since(start)/n, n)

	start = time.Now()
	for i := 0; i < n; i++ {
		if _, err := RenderIcon(style); err != nil {
			t.Fatalf("render icon: %v", err)
		}
	}
	t.Logf("单次 RenderIcon  ≈ %v (n=%d, 串行)", time.Since(start)/n, n)
}

// startGroupRenderFanout 启动 workers 个 goroutine 持续渲染群头像，模拟成员/会话列表
// 扇出下大量并发的群 avatar 200 响应（无 If-None-Match，每次落到真渲染）。cache 非 nil
// 时经 GetOrRender（共享缓存 + singleflight + 渲染信号量），nil 时直接渲染。
func startGroupRenderFanout(workers int, cache *Cache) (stop func(), served *int64) {
	var done int32
	var count int64
	var wg sync.WaitGroup
	groups := []string{"g_arch", "g_prod", "g_ops", "g_qa", "g_design"}
	texts := []string{"架构讨论", "产品需求", "运维", "测试组", "设计"}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		gno := groups[w%len(groups)]
		text := texts[w%len(texts)]
		style := GroupStyleForSeed(gno)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&done) == 0 {
				if cache != nil {
					key := CacheKey("group-name-v2", gno, "seed", text)
					_, _ = cache.GetOrRender(key, func() ([]byte, error) {
						return RenderGroup(text, style, DefaultSize)
					})
				} else {
					_, _ = RenderGroup(text, style, DefaultSize)
				}
				atomic.AddInt64(&count, 1)
			}
		}()
	}
	return func() { atomic.StoreInt32(&done, 1); wg.Wait() }, &count
}

// TestGroupRenderCacheCollapsesRenders 是**确定性**核心证明（不依赖计时）：大量并发、
// 仅少数不同 key 的群渲染，经 GetOrRender 后真实渲染次数收敛到 ~key 数（singleflight
// 合并冷启动 + LRU 命中复用），而非每请求一渲。这正是群 avatarGet 接入共享缓存要的效果。
func TestGroupRenderCacheCollapsesRenders(t *testing.T) {
	cache, err := NewCache(Config{MaxConcurrentRenders: 2})
	if err != nil {
		t.Fatal(err)
	}
	const (
		keys        = 4
		concurrency = 64
		rounds      = 6 // 每 worker 多轮，放大「命中复用」效应
	)
	var renders int64
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		gno := fmt.Sprintf("g%d", i%keys)
		text := fmt.Sprintf("群%d", i%keys)
		style := GroupStyleForSeed(gno)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				key := CacheKey("group-name-v2", gno, "seed", text)
				_, _ = cache.GetOrRender(key, func() ([]byte, error) {
					atomic.AddInt64(&renders, 1)
					return RenderGroup(text, style, DefaultSize)
				})
			}
		}()
	}
	wg.Wait()

	got := atomic.LoadInt64(&renders)
	t.Logf("%d 并发 × %d 轮 / %d 个 key → 真实渲染 %d 次, cache.Len=%d",
		concurrency, rounds, keys, got, cache.Len())
	// 无缓存时是 concurrency*rounds=384 次渲染；经缓存应收敛到接近 key 数。给宽松上限
	// （key 数的 3 倍）容忍冷启动竞态，但远低于 384，证明收敛。
	if got > keys*3 {
		t.Fatalf("期望并发收敛到 ~%d 次真实渲染，实测 %d（远超说明未命中缓存）", keys, got)
	}
}

// TestGroupRenderCacheEliminatesStarvation 对标 #481：GOMAXPROCS=2 下，群渲染扇出经
// 共享 Cache（LRU + singleflight + 渲染信号量）后，CPU 不再被反复渲染占满，受害者
// （模拟邀请请求）基本回到基线。验证「群路径接入共享缓存」确实拿到 #480 的保护。
func TestGroupRenderCacheEliminatesStarvation(t *testing.T) {
	if testing.Short() {
		t.Skip("skip starvation validation in -short")
	}
	// 本测断言 wall-clock 放大倍数（under/base ratio），它**演示**饿死消除、但不能**钉死**
	// 并发安全；在共享/受压 CI runner 上该比值会抖动误报（评审实测 1.9x/3.5x，见 #478
	// review）。故默认跳过，仅在显式 OCTO_TIMING_TESTS=1 时运行（手动复现/演示）。渲染
	// 经缓存收敛这一**确定性**证明由 TestGroupRenderCacheCollapsesRenders 承担，恒在 CI 跑。
	// 注：#481 的同型 TestCacheEliminatesStarvation 共享同一脆弱断言，属其范畴，本 PR 不动。
	if os.Getenv("OCTO_TIMING_TESTS") != "1" {
		t.Skip("set OCTO_TIMING_TESTS=1 to run the wall-clock starvation demonstration (timing-fragile on shared runners; the deterministic proof is TestGroupRenderCacheCollapsesRenders)")
	}
	prev := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(prev)

	const (
		ioRounds = 20
		ioRTT    = 1 * time.Millisecond
		samples  = 7
		workers  = 16
	)
	_ = victimWork(ioRounds, ioRTT) // warmup

	base := medianVictim(samples, ioRounds, ioRTT)
	ideal := time.Duration(ioRounds) * ioRTT
	if base > 3*ideal {
		t.Skipf("runner 过载：基线 %v 远高于理想 %v，跳过 timing 对比", base, ideal)
	}

	cache, err := NewCache(Config{MaxConcurrentRenders: 1})
	if err != nil {
		t.Fatal(err)
	}
	stop, served := startGroupRenderFanout(workers, cache)
	time.Sleep(50 * time.Millisecond)
	under := medianVictim(samples, ioRounds, ioRTT)
	stop()

	ratio := float64(under) / float64(base)
	t.Logf("群渲染经共享缓存的扇出：基线=%v 缓存扇出下=%v 放大=%.1fx（期望≈1x）服务=%d keys=%d",
		base, under, ratio, atomic.LoadInt64(served), cache.Len())
	if ratio > 1.8 {
		t.Fatalf("期望缓存消除群渲染饿死（放大≈1x），实测 %.1fx（base=%v under=%v）", ratio, base, under)
	}
}
