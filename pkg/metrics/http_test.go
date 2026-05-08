package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-server/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// histogramSampleCount 通过 Gather 严格读取指定 label 组合的样本数。
//
// 不用 HistogramVec.WithLabelValues / GetMetricWithLabelValues, 因为它们在
// label 组合不存在时会"静默创建一个 0 样本子项"返回, 写错断言时容易得到
// 假的 0 而非显式失败 — Gather 路径只反映真实存在的 series。
func histogramSampleCount(
	t *testing.T,
	reg prometheus.Gatherer,
	name string,
	wantLabels map[string]string,
) uint64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsEqual(m.GetLabel(), wantLabels) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func labelsEqual(have []*dto.LabelPair, want map[string]string) bool {
	if len(have) != len(want) {
		return false
	}
	for _, lp := range have {
		if v, ok := want[lp.GetName()]; !ok || v != lp.GetValue() {
			return false
		}
	}
	return true
}

func newRouterWithMetrics(t *testing.T) (*gin.Engine, *metrics.HTTPMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := metrics.NewHTTPMetrics(reg)
	r := gin.New()
	// 真实 main.go 顺序: wkhttp.New() 先装 gin.Recovery (最外层),
	// 再 UseGin(metricsMiddleware). 测试模拟这个顺序。
	r.Use(gin.Recovery())
	r.Use(m.GinMiddleware())
	return r, m, reg
}

func TestGinMiddleware_RecordsRequest(t *testing.T) {
	cases := []struct {
		name          string
		registerPath  string
		registerMeth  string
		requestPath   string
		requestMeth   string
		handlerStatus int
		handlerPanics bool
		wantPath      string
		wantMethod    string
		wantStatus    string
	}{
		{
			name:          "matched route uses FullPath template not raw uri",
			registerPath:  "/v1/users/:uid/im",
			registerMeth:  http.MethodGet,
			requestPath:   "/v1/users/abc-123/im",
			requestMeth:   http.MethodGet,
			handlerStatus: http.StatusOK,
			wantPath:      "/v1/users/:uid/im",
			wantMethod:    http.MethodGet,
			wantStatus:    "200",
		},
		{
			name:         "unmatched route collapses to 'unmatched'",
			registerPath: "/v1/known",
			registerMeth: http.MethodGet,
			requestPath:  "/v1/never-registered/xyz",
			requestMeth:  http.MethodGet,
			wantPath:     "unmatched",
			wantMethod:   http.MethodGet,
			wantStatus:   "404",
		},
		{
			name:          "5xx status recorded as label",
			registerPath:  "/v1/error",
			registerMeth:  http.MethodPost,
			requestPath:   "/v1/error",
			requestMeth:   http.MethodPost,
			handlerStatus: http.StatusInternalServerError,
			wantPath:      "/v1/error",
			wantMethod:    http.MethodPost,
			wantStatus:    "500",
		},
		{
			name:          "panic recorded as 500",
			registerPath:  "/v1/panic",
			registerMeth:  http.MethodGet,
			requestPath:   "/v1/panic",
			requestMeth:   http.MethodGet,
			handlerPanics: true,
			wantPath:      "/v1/panic",
			wantMethod:    http.MethodGet,
			wantStatus:    "500",
		},
		{
			name:          "non-standard method normalized to 'other'",
			registerPath:  "/v1/foo",
			registerMeth:  "WEIRD",
			requestPath:   "/v1/foo",
			requestMeth:   "WEIRD",
			handlerStatus: http.StatusOK,
			wantPath:      "/v1/foo",
			wantMethod:    "other",
			wantStatus:    "200",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _, reg := newRouterWithMetrics(t)
			r.Handle(tc.registerMeth, tc.registerPath, func(c *gin.Context) {
				if tc.handlerPanics {
					panic("boom")
				}
				if tc.handlerStatus != 0 {
					c.Status(tc.handlerStatus)
				}
			})

			req := httptest.NewRequest(tc.requestMeth, tc.requestPath, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			got := histogramSampleCount(t, reg, "dmwork_http_request_duration_seconds",
				map[string]string{
					"method": tc.wantMethod,
					"path":   tc.wantPath,
					"status": tc.wantStatus,
				})
			if got != 1 {
				t.Errorf("expected 1 sample for {method=%s, path=%s, status=%s}, got %d",
					tc.wantMethod, tc.wantPath, tc.wantStatus, got)
			}
		})
	}
}

func TestGinMiddleware_SkipsMetricsEndpoint(t *testing.T) {
	r, _, reg := newRouterWithMetrics(t)
	r.GET("/metrics", func(c *gin.Context) { c.String(http.StatusOK, "scrape body") })

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// /metrics 必须不产生任何样本 — 整个 family 在 Gather 中应不存在。
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() == "dmwork_http_request_duration_seconds" {
			t.Errorf("expected no histogram samples after /metrics scrape, got %d series",
				len(mf.GetMetric()))
		}
	}
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics handler should still execute, got status %d", rec.Code)
	}
}

func TestGinMiddleware_InFlightGaugeNoLeak(t *testing.T) {
	r, m, _ := newRouterWithMetrics(t)
	r.GET("/v1/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/v1/panic", func(c *gin.Context) { panic("boom") })

	hit := func(path string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}
	for i := 0; i < 5; i++ {
		hit("/v1/ok")
		hit("/v1/panic")
	}

	if v := testutil.ToFloat64(m.InFlight); v != 0 {
		t.Errorf("in-flight gauge leaked: expected 0 after all requests done, got %v", v)
	}
}

func TestGinMiddleware_InFlightGaugeIncrementsDuringRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewHTTPMetrics(reg)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(m.GinMiddleware())

	entered := make(chan struct{})
	release := make(chan struct{})
	r.GET("/slow", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusOK)
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}()

	<-entered
	if v := testutil.ToFloat64(m.InFlight); v != 1 {
		t.Errorf("expected in-flight=1 mid-request, got %v", v)
	}
	close(release)
	wg.Wait()

	if v := testutil.ToFloat64(m.InFlight); v != 0 {
		t.Errorf("expected in-flight=0 after request, got %v", v)
	}
}

func TestNewHTTPMetrics_RegistersOnProvidedRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewHTTPMetrics(reg)

	// HistogramVec 在未观测前不会被 Gather() 报告(prometheus 库行为),
	// 先打一个样本让 family 出现, 再断言两个指标都注册成功。
	m.Duration.WithLabelValues("GET", "/probe", "200").Observe(0.001)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"dmwork_http_request_duration_seconds": false,
		"dmwork_http_requests_in_flight":       false,
	}
	for _, mf := range families {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected metric %q to be registered", name)
		}
	}
}

func TestNewHTTPMetrics_PanicsOnDuplicateRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = metrics.NewHTTPMetrics(reg)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	_ = metrics.NewHTTPMetrics(reg)
}
