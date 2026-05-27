package i18n

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
)

func TestErrorRenderer_RendersLocalizedDualEnvelope(t *testing.T) {
	r := wkhttp.New()
	r.SetErrorRenderer(NewErrorRenderer(NewLocalizer(SourceLanguage)))
	r.GET("/limited", func(c *wkhttp.Context) {
		c.Request = c.Request.WithContext(WithLanguage(c.Request.Context(), LanguageDecision{
			Language: "zh-CN",
			Source:   LanguageSourceAccept,
		}))
		c.RenderError(wkhttp.ErrorSpec{
			Code:            "err.shared.rate.limited",
			DefaultMessage:  "raw fallback",
			TransportStatus: http.StatusTooManyRequests,
			SemanticStatus:  http.StatusTooManyRequests,
			Details: map[string]any{
				"retry_after": 3,
				"raw_err":     "do not leak",
			},
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/limited", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("HTTP status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Content-Language"); got != "zh-CN" {
		t.Fatalf("Content-Language = %q, want zh-CN", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body["msg"]; got != "请求过于频繁，请稍后再试。" {
		t.Fatalf("msg = %q", got)
	}
	if got := body["status"]; got != float64(http.StatusTooManyRequests) {
		t.Fatalf("status = %v, want %d", got, http.StatusTooManyRequests)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %#v", body["error"])
	}
	if got := errObj["code"]; got != "err.shared.rate.limited" {
		t.Fatalf("error.code = %q", got)
	}
	if got := errObj["message"]; got != body["msg"] {
		t.Fatalf("error.message = %q, want msg %q", got, body["msg"])
	}
	if got := errObj["http_status"]; got != float64(http.StatusTooManyRequests) {
		t.Fatalf("error.http_status = %v", got)
	}
	details, ok := errObj["details"].(map[string]any)
	if !ok {
		t.Fatalf("error.details missing: %#v", errObj["details"])
	}
	if got := details["retry_after"]; got != float64(3) {
		t.Fatalf("details.retry_after = %v", got)
	}
	if _, ok := details["raw_err"]; ok {
		t.Fatal("unsafe detail raw_err leaked")
	}
}

func TestErrorRenderer_InternalDoesNotExposeSpecData(t *testing.T) {
	r := wkhttp.New()
	r.SetErrorRenderer(NewErrorRenderer(NewLocalizer(SourceLanguage)))
	r.GET("/internal", func(c *wkhttp.Context) {
		c.Request = c.Request.WithContext(WithLanguage(c.Request.Context(), LanguageDecision{
			Language: "zh-CN",
			Source:   LanguageSourceAccept,
		}))
		c.RenderError(wkhttp.ErrorSpec{
			Code:            "err.server.thread.store_failed",
			DefaultMessage:  "database host 10.0.0.1 failed",
			TransportStatus: http.StatusBadRequest,
			SemanticStatus:  http.StatusInternalServerError,
			Params: map[string]any{
				"table": "thread",
			},
			Details: map[string]any{
				"raw_err": "secret",
			},
			Internal: true,
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal", nil)
	r.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body["msg"]; got != "服务器内部错误。" {
		t.Fatalf("msg = %q", got)
	}
	errObj := body["error"].(map[string]any)
	if got := errObj["message"]; got != "服务器内部错误。" {
		t.Fatalf("error.message = %q", got)
	}
	details := errObj["details"].(map[string]any)
	if len(details) != 0 {
		t.Fatalf("internal details leaked: %#v", details)
	}
}
