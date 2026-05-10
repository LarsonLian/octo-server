package user

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

// TestVerifyTokenAegisRedirect_Authenticated 验证 /v1/internal/verify-token 在已登录
// 状态下返回 200 + Aegis 账户页 URL(带 return_to 深链)+ expires_in=300。
// YUJ-394 / Aegis OIDC Phase 2d 的核心验收。
func TestVerifyTokenAegisRedirect_Authenticated(t *testing.T) {
	s, _ := testutil.NewTestServer()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/internal/verify-token", nil)
	req.Header.Set("token", testutil.Token)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()

	// URL 必须指向 Aegis 账户页,不能是老的 verify-service。
	assert.Contains(t, body, `"url":"https://accounts.example.com/profile/info?anchor=verification`)
	assert.Contains(t, body, `return_to=octo://verified`)
	assert.Contains(t, body, `"expires_in":300`)

	// 回归保护:确保我们没有误指回 verify-service。
	assert.False(t, strings.Contains(body, "verify-service"),
		"verify-token response must NOT point to verify-service: %s", body)
	assert.False(t, strings.Contains(body, "verify.xming"),
		"verify-token response must NOT point to verify.xming.ai: %s", body)
}

// TestVerifyTokenAegisRedirect_POSTCompat 老 App 走 POST 方法命中同一 handler,
// 返回与 GET 一致的 Aegis URL。
func TestVerifyTokenAegisRedirect_POSTCompat(t *testing.T) {
	s, _ := testutil.NewTestServer()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/internal/verify-token", nil)
	req.Header.Set("token", testutil.Token)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"url":"https://accounts.example.com/profile/info?anchor=verification`)
	assert.Contains(t, w.Body.String(), `"expires_in":300`)
}

// TestVerifyTokenAegisRedirect_Unauthenticated 未登录请求必须被 AuthMiddleware 拒掉:
// 不能让匿名用户拿到带 return_to 的跳转 URL(防钓鱼)。
func TestVerifyTokenAegisRedirect_Unauthenticated(t *testing.T) {
	s, _ := testutil.NewTestServer()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/internal/verify-token", nil)
	// 不设置 token header
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	// 未登录响应中绝不能泄露 Aegis 跳转 URL。
	assert.NotContains(t, w.Body.String(), "accounts.example.com")
	assert.NotContains(t, w.Body.String(), "octo://verified")
}

// TestVerifyTokenAegisRedirect_InvalidToken 带无效 token 应等价未登录,拒 401。
func TestVerifyTokenAegisRedirect_InvalidToken(t *testing.T) {
	s, _ := testutil.NewTestServer()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/internal/verify-token", nil)
	req.Header.Set("token", "token-does-not-exist-123")
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "accounts.example.com")
}
