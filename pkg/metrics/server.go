package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewScrapeServer 构造一个仅暴露 /metrics 的 http.Server。
//
// 设计取舍:
//   - 独立端口(默认 :9090),不挂业务端口 — 业务限流/CORS/Recovery 不影响 scrape,
//     业务挂掉时 scrape 还能拉到最后状态;同时不会经 CLB 暴露到公网。
//   - 默认 promhttp.Handler() 走全局 DefaultGatherer — 与 modules/oidc/metrics.go
//     的 promauto 注册到 default registry 一致,一次抓取拿全所有指标。
//   - ReadHeaderTimeout 防 slow-header DoS;Listen 路径上 G112 静态检查也要求设。
//
// 调用方负责 ListenAndServe / Shutdown 生命周期。
func NewScrapeServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout 防 slow-body 客户端长占 goroutine,即便端口在内网仍做防御性设置。
		// 10s 足够覆盖最大 metric family 的写出(典型 IM 服务 <100KB)。
		WriteTimeout: 10 * time.Second,
	}
}
