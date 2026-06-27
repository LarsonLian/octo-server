package avatarrender

import "testing"

// 渲染层性能基准。avatarGet 对无自定义上传的群每次 200 响应都实时渲染，故单次渲染
// 成本 + 并行吞吐是容量规划关键。运行：
//
//	go test ./pkg/avatarrender/ -bench 'Render' -benchmem -benchtime=2s

func BenchmarkRenderGroup2x2(b *testing.B) {
	style := GroupStyleForSeed("g_bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderGroup("架构讨论", style, DefaultSize); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderGroupSingleCJK(b *testing.B) {
	style := GroupStyleForSeed("g_bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderGroup("开发", style, DefaultSize); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderGroupLatin(b *testing.B) {
	style := GroupStyleForSeed("g_bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderGroup("abcd", style, DefaultSize); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderIcon(b *testing.B) {
	style := GroupStyleForSeed("g_bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderIcon(style); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenderGroupParallel 衡量多核聚合吞吐（渲染是 CPU 密集、无共享状态）。
func BenchmarkRenderGroupParallel(b *testing.B) {
	style := GroupStyleForSeed("g_bench")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := RenderGroup("架构讨论", style, DefaultSize); err != nil {
				b.Fatal(err)
			}
		}
	})
}
