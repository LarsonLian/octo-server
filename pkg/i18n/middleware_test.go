package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEarlyMiddlewareNegotiatesLanguageIntoRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(EarlyMiddleware(MiddlewareOptions{DefaultLanguage: SourceLanguage}))
	r.GET("/x", func(c *gin.Context) {
		decision, ok := LanguageFromContext(c.Request.Context())
		if !ok {
			t.Fatal("language decision missing")
		}
		if decision.Language != "zh-CN" {
			t.Fatalf("language = %q, want zh-CN", decision.Language)
		}
		if decision.Source != LanguageSourceAccept {
			t.Fatalf("source = %q, want %q", decision.Source, LanguageSourceAccept)
		}
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Language"); got != "zh-CN" {
		t.Fatalf("Content-Language = %q, want zh-CN", got)
	}
}

func TestEarlyMiddlewareUsesDefaultWhenNoSignal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(EarlyMiddleware(MiddlewareOptions{DefaultLanguage: "zh-CN"}))
	r.GET("/x", func(c *gin.Context) {
		decision, ok := LanguageFromContext(c.Request.Context())
		if !ok {
			t.Fatal("language decision missing")
		}
		if decision.Language != "zh-CN" {
			t.Fatalf("language = %q, want zh-CN", decision.Language)
		}
		if decision.Source != LanguageSourceDefault {
			t.Fatalf("source = %q, want %q", decision.Source, LanguageSourceDefault)
		}
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(rec, req)
}
