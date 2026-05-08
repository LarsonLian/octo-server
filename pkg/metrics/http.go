// Package metrics 收集 dmwork 进程级 Prometheus 指标。
//
// HTTP 中间件提供 per-route 的延迟直方图和并发请求计数,
// path label 用 gin.Context.FullPath() 取路由模板而非真实 URI,
// 防止 uid/orderID 这类高基数值打爆 Prometheus 内存。
//
// 与 modules/oidc/metrics.go 不同: 那里用全局默认 Registry (promauto),
// 本文件让调用方注入 Registerer, 便于测试隔离和未来按需迁移到独立 registry。
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricNamespace = "dmwork"
	metricSubsystem = "http"

	// metricsEndpointPath 抓取端点本身在中间件中跳过埋点,
	// 防止 Prometheus 抓取自身产生指标 -> 循环放大。
	metricsEndpointPath = "/metrics"

	// pathLabelUnmatched 未匹配到任何路由模板时的 path 兜底值,
	// 防止把真实 URI(可能含 uid/token)写进 label 导致基数爆炸。
	pathLabelUnmatched = "unmatched"

	// methodLabelOther 非标准 HTTP method 的兜底值,
	// 防止 fuzz 攻击造的奇怪 method 打爆 method label 维度。
	methodLabelOther = "other"
)

// allowedMethods 是 method label 的白名单。任何不在此集合的方法
// 都归一化为 methodLabelOther,见 normalizeMethod。
var allowedMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// HTTPMetrics 持有所有 HTTP 入口指标。每进程一个实例。
type HTTPMetrics struct {
	// Duration 按 method/path/status 切分的请求延迟直方图。
	// Buckets 覆盖 5ms ~ 10s, 匹配 IM 业务的真实 P99 区间。
	Duration *prometheus.HistogramVec

	// InFlight 当前并发处理中的请求数, 用于发现 hang 或慢请求堆积。
	InFlight prometheus.Gauge
}

// NewHTTPMetrics 在传入的 Registerer 上注册所有 HTTP 指标。
//
// 调用契约: 同一个 Registerer 只能调用一次。重复注册触发 MustRegister 的 panic
// (这是 prometheus 库的契约,不是本函数的 bug)。
//   - 生产代码: 在 main.go 启动时一次性传入 prometheus.DefaultRegisterer。
//   - 测试代码: 每个用例传入 prometheus.NewRegistry() 以隔离全局状态。
func NewHTTPMetrics(reg prometheus.Registerer) *HTTPMetrics {
	m := &HTTPMetrics{
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency in seconds, labeled by method, route template, and status.",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "path", "status"}),
		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "requests_in_flight",
			Help:      "Number of HTTP requests currently being processed.",
		}),
	}
	reg.MustRegister(m.Duration, m.InFlight)
	return m
}

// GinMiddleware 返回一个 gin 中间件,
// 在 c.Next() 前后记录延迟、状态码和并发计数。
//
// panic 处理: 上层 gin.Recovery() 是最外层中间件, 它的 defer recover
// 在本中间件 defer 之后才会执行 — 直接读 c.Writer.Status() 会拿到 200。
// 因此本中间件自己 recover 一次记录 status=500, 再 re-panic 让外层 Recovery
// 完成最终响应写出。
func (m *HTTPMetrics) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 抓取端点本身不埋点。用 URL.Path 而非 FullPath, 因为 /metrics
		// 在中间件链入口路由还未匹配时也要跳过(虽然此场景目前不存在,
		// 但更安全的不变量是 URL.Path == "/metrics" 即跳过)。
		if c.Request.URL.Path == metricsEndpointPath {
			c.Next()
			return
		}

		m.InFlight.Inc()
		start := time.Now()

		defer func() {
			r := recover()
			status := c.Writer.Status()
			if r != nil {
				status = http.StatusInternalServerError
			}
			m.Duration.WithLabelValues(
				normalizeMethod(c.Request.Method),
				pathLabel(c),
				strconv.Itoa(status),
			).Observe(time.Since(start).Seconds())
			m.InFlight.Dec()
			if r != nil {
				panic(r) // 让外层 gin.Recovery 完成 500 响应写出
			}
		}()

		c.Next()
	}
}

// pathLabel 返回 path label 的值: 命中路由用模板, 否则用兜底常量。
func pathLabel(c *gin.Context) string {
	if p := c.FullPath(); p != "" {
		return p
	}
	return pathLabelUnmatched
}

// normalizeMethod 把请求方法收敛到白名单, 防止 method label 维度爆炸。
func normalizeMethod(method string) string {
	if _, ok := allowedMethods[method]; ok {
		return method
	}
	return methodLabelOther
}
