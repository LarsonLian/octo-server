package user

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// YUJ-399 · Aegis admin fetcher 单测:覆盖 Aegis mock 返 2xx / 4xx / 5xx /
// 路由不可达的全部路径,验证 ErrFetcherUnavailable 只在"基础设施级"挂掉时映射。
//
// 构造方式:起 httptest 起 token 端点 + admin 端点两个 stub,通过显式 config
// 绕过 env 读取,避免测试污染进程级环境变量。

func tokenEndpoint(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Logf("token endpoint parse form: %v", err)
		}
		// 简化返回:不校验 client_id/secret,直接签一个静态 token。
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stub-admin-token","token_type":"Bearer","expires_in":3600}`))
	}
}

func TestAegisAdminFetcher_HappyPath(t *testing.T) {
	var adminCalls int32
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		atomic.AddInt32(&adminCalls, 1)
		assert.Equal(t, "Bearer stub-admin-token", r.Header.Get("Authorization"))
		assert.True(t, strings.HasPrefix(r.URL.Path, "/admin/users/"))
		// Round 1 审查:请求必须带 include=identity_verification,否则部分 Aegis
		// 部署会返回空 claims(无 5 字段) → worker 永远 upsert 不进,沉默失败。
		assert.Equal(t, "identity_verification", r.URL.Query().Get("include"),
			"必须带 include=identity_verification query,与注释契约对齐")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":               "cas-sub-xx",
			"is_verified":       true,
			"verified_at":       1_778_331_902,
			"verified_provider": "cas.example.com",
			"legal_name":        "张三",
			"legal_email":       "zhang@example.com",
		})
	}))
	defer admin.Close()

	cfg := aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	}
	f := newAegisAdminFetcher(cfg)
	require.True(t, f.ready)

	claims, err := f.FetchClaims(context.Background(), "u-uid", "cas-sub-xx")
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, "张三", claims.LegalName)
	assert.Equal(t, int64(1_778_331_902), claims.VerifiedAt)
	assert.Equal(t, "cas.example.com", claims.VerifiedProvider)
	assert.Equal(t, "zhang@example.com", claims.LegalEmail)
	assert.Equal(t, int32(1), atomic.LoadInt32(&adminCalls))
}

func TestAegisAdminFetcher_5xx_MapsToUnavailable(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		http.Error(w, "backend meltdown", http.StatusInternalServerError)
	}))
	defer admin.Close()

	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	require.True(t, f.ready)
	_, err := f.FetchClaims(context.Background(), "u", "s")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFetcherUnavailable),
		"5xx 应映射成 ErrFetcherUnavailable,实际: %v", err)
}

func TestAegisAdminFetcher_404_ReturnsNilNil(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer admin.Close()

	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	claims, err := f.FetchClaims(context.Background(), "u", "s")
	require.NoError(t, err, "404 应视为'无 claims 可用',非错误")
	assert.Nil(t, claims)
}

func TestAegisAdminFetcher_401_MapsToUnavailable(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer admin.Close()

	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	_, err := f.FetchClaims(context.Background(), "u", "s")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFetcherUnavailable))
}

func TestAegisAdminFetcher_NotReady_ReturnsUnavailable(t *testing.T) {
	f := newAegisAdminFetcher(aegisAdminFetcherConfig{}) // 全空 → ready=false
	require.False(t, f.ready)
	_, err := f.FetchClaims(context.Background(), "u", "s")
	require.ErrorIs(t, err, ErrFetcherUnavailable)
}

func TestAegisAdminFetcher_SubEmpty_FallbackToUID(t *testing.T) {
	var captured string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		captured = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":               "from-sub",
			"is_verified":       true,
			"verified_at":       1,
			"verified_provider": "cas",
			"legal_name":        "x",
		})
	}))
	defer admin.Close()
	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	_, err := f.FetchClaims(context.Background(), "u-fallback-uid", "")
	require.NoError(t, err)
	assert.Equal(t, "/admin/users/u-fallback-uid", captured)
}

// verifiedFlexBool / verifiedFlexTime 的 wire-format 兜底断言 —— 复刻 oidc 侧
// 相同语义测试,保证 Aegis 改 wire-type 时两边同步。
func TestVerifiedFlexBool_WireFormats(t *testing.T) {
	type holder struct {
		V verifiedFlexBool `json:"v"`
	}
	cases := map[string]bool{
		`{"v":true}`:    true,
		`{"v":false}`:   false,
		`{"v":"true"}`:  true,
		`{"v":"1"}`:     true,
		`{"v":"yes"}`:   true,
		`{"v":"false"}`: false,
		`{"v":1}`:       true,
		`{"v":0}`:       false,
		`{"v":null}`:    false,
	}
	for in, want := range cases {
		var h holder
		require.NoError(t, json.Unmarshal([]byte(in), &h), in)
		assert.Equal(t, want, h.V.Bool(), in)
	}
}

func TestVerifiedFlexTime_WireFormats(t *testing.T) {
	type holder struct {
		V verifiedFlexTime `json:"v"`
	}
	cases := map[string]int64{
		`{"v":1778331902}`:    1778331902,
		`{"v":"1778331902"}`:  1778331902,
		`{"v":1.778331902e9}`: 1778331902,
		`{"v":null}`:          0,
		`{"v":""}`:            0,
		`{"v":"abc"}`:         0,
	}
	for in, want := range cases {
		var h holder
		require.NoError(t, json.Unmarshal([]byte(in), &h), in)
		assert.Equal(t, want, int64(h.V), in)
	}
}

// Round 2 Critical 2 — fetcher 必须把 is_verified=false 当成"无实名记录",返 (nil, nil)。
// 防止 Aegis 残留历史字段被 worker 误写回 OCTO 造成"已实名"徽章错误显示。
func TestAegisAdminFetcher_IsVerifiedFalse_ReturnsNilNil(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		// Aegis 残留 legal_name + verified_at,但 is_verified=false(取消实名场景)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":               "cas-sub-canceled",
			"is_verified":       false,
			"verified_at":       1_700_000_000,
			"verified_provider": "cas.example.com",
			"legal_name":        "历史姓名",
			"legal_email":       "hist@example.com",
		})
	}))
	defer admin.Close()

	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	claims, err := f.FetchClaims(context.Background(), "u", "cas-sub-canceled")
	require.NoError(t, err, "is_verified=false 不算 error")
	assert.Nil(t, claims,
		"is_verified=false 必须返 nil 让 worker 走 claims_incomplete 分支,不能把历史 legal_name 误写回 OCTO")
}

// Round 2 Critical 2 补强:is_verified=true 且字段完整 → 正常返 claims
func TestAegisAdminFetcher_IsVerifiedTrue_ReturnsClaims(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			tokenEndpoint(t)(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":               "cas-sub-ok",
			"is_verified":       true,
			"verified_at":       1_778_331_902,
			"verified_provider": "cas",
			"legal_name":        "实名张",
		})
	}))
	defer admin.Close()

	f := newAegisAdminFetcher(aegisAdminFetcherConfig{
		AdminBaseURL: admin.URL,
		TokenURL:     admin.URL + "/oauth/token",
		ClientID:     "cid",
		ClientSecret: "csec",
	})
	claims, err := f.FetchClaims(context.Background(), "u", "cas-sub-ok")
	require.NoError(t, err)
	require.NotNil(t, claims, "is_verified=true + fields 完整 → 应返 claims")
	assert.Equal(t, "实名张", claims.LegalName)
}
