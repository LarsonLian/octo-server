package group

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
)

// avatarGet 端到端基准。无自定义上传的群每次 200 都查 DB + 服务端渲染；命中
// If-None-Match 时走 304（仅 DB 查询 + ETag，不渲染）。两者之差 = 渲染成本，也量化
// ETag/304 短路的收益（生产中靠它 + CDN 把渲染压到冷路径）。运行：
//
//	go test ./modules/group/ -bench 'AvatarGet' -benchmem -benchtime=2s -run '^$'

func benchAvatarSetup(b *testing.B, groupNo, name string) http.Handler {
	b.Helper()
	s, ctx := testutil.NewTestServer()
	bindTestDBPool(ctx)
	if err := testutil.CleanAllTables(ctx); err != nil {
		b.Fatal(err)
	}
	g := New(ctx)
	if err := g.db.Insert(&Model{GroupNo: groupNo, Name: name, Creator: "c1", Status: 1}); err != nil {
		b.Fatal(err)
	}
	return s.GetRoute()
}

// BenchmarkAvatarGetRender 测 200 路径（DB 查询 + 实时渲染，每次都渲染）。
func BenchmarkAvatarGetRender(b *testing.B) {
	route := benchAvatarSetup(b, "bench_render", "压测群组")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/groups/bench_render/avatar", nil)
		route.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("unexpected code %d", w.Code)
		}
	}
}

// BenchmarkAvatarGet304 测 304 路径（DB 查询 + ETag 比对，不渲染）—— 客户端/CDN
// 带 If-None-Match 的命中场景，是生产主路径。
func BenchmarkAvatarGet304(b *testing.B) {
	route := benchAvatarSetup(b, "bench_304", "压测群组")
	// 先取一次 ETag。
	w0 := httptest.NewRecorder()
	req0, _ := http.NewRequest("GET", "/v1/groups/bench_304/avatar", nil)
	route.ServeHTTP(w0, req0)
	etag := w0.Header().Get("ETag")
	if etag == "" {
		b.Fatal("no ETag")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/groups/bench_304/avatar", nil)
		req.Header.Set("If-None-Match", etag)
		route.ServeHTTP(w, req)
		if w.Code != http.StatusNotModified {
			b.Fatalf("unexpected code %d", w.Code)
		}
	}
}
