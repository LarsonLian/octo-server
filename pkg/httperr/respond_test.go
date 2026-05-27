package httperr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

func TestResponseErrorLBuildsCompatibleSpec(t *testing.T) {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.SourceLanguage)))
	r.GET("/x", func(c *wkhttp.Context) {
		c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), i18n.LanguageDecision{
			Language: "zh-CN",
			Source:   i18n.LanguageSourceAccept,
		}))
		ResponseErrorL(c, errcode.ErrThreadGroupNoInvalid, nil, i18n.Details{
			"field":   "group_no",
			"raw_err": "secret",
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body["status"]; got != float64(http.StatusBadRequest) {
		t.Fatalf("status = %v, want 400", got)
	}
	errObj := body["error"].(map[string]any)
	if got := errObj["code"]; got != errcode.ErrThreadGroupNoInvalid.ID {
		t.Fatalf("error.code = %q", got)
	}
	if got := errObj["http_status"]; got != float64(http.StatusBadRequest) {
		t.Fatalf("error.http_status = %v, want 400", got)
	}
	details := errObj["details"].(map[string]any)
	if got := details["field"]; got != "group_no" {
		t.Fatalf("details.field = %v", got)
	}
	if _, ok := details["raw_err"]; ok {
		t.Fatal("unsafe detail leaked")
	}
}

func TestResponseErrorLUnknownCodeFallsBackToInternal(t *testing.T) {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.SourceLanguage)))
	r.GET("/x", func(c *wkhttp.Context) {
		ResponseErrorL(c, codes.Code{
			ID:             "err.server.thread.unregistered",
			HTTPStatus:     http.StatusForbidden,
			DefaultMessage: "unregistered",
		}, nil, nil)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errObj := body["error"].(map[string]any)
	if got := errObj["code"]; got != "err.shared.internal" {
		t.Fatalf("error.code = %q", got)
	}
	if got := errObj["http_status"]; got != float64(http.StatusInternalServerError) {
		t.Fatalf("error.http_status = %v, want 500", got)
	}
}
