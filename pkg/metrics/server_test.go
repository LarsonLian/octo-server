package metrics_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/pkg/metrics"
)

func TestNewScrapeServer_ServesMetricsOn200(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := metrics.NewScrapeServer(ln.Addr().String())

	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	url := "http://" + ln.Addr().String() + "/metrics"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "# HELP") {
		t.Errorf("expected exposition format with '# HELP' line, got: %s", body)
	}
}

func TestNewScrapeServer_404OnOtherPaths(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := metrics.NewScrapeServer(ln.Addr().String())

	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 on /, got %d", resp.StatusCode)
	}
}

func TestNewScrapeServer_HasTimeoutsConfigured(t *testing.T) {
	srv := metrics.NewScrapeServer(":0")
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout should be set to defend against slow-header DoS")
	}
	if srv.WriteTimeout == 0 {
		t.Error("WriteTimeout should be set to defend against slow-body clients holding goroutines")
	}
}
