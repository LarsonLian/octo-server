package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
)

// fakeVerification 内存版 verificationUpserter,断言 OIDC callback 是否调用了它
// 以及带什么参数。err 非 nil 时下一次 Upsert 返回该错误(测试"写库失败不阻断登录")。
type fakeVerification struct {
	mu    sync.Mutex
	calls []fakeVerificationCall
	err   error
}

type fakeVerificationCall struct {
	UID    string
	Claims user.OIDCVerificationClaims
}

func (f *fakeVerification) UpsertVerificationFromOIDC(_ context.Context, uid string, claims user.OIDCVerificationClaims) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeVerificationCall{UID: uid, Claims: claims})
	return f.err
}

func (f *fakeVerification) last() (fakeVerificationCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return fakeVerificationCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

func (f *fakeVerification) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// runCallbackWithClaims 搭一整套 mock provider + test OIDC,预置给定的 ID Token
// extra claims,走一次完整 callback,返回装了 fakeVerification 的 OIDC 实例。
func runCallbackWithClaims(t *testing.T, idTokenClaims map[string]interface{}) (*fakeVerification, int) {
	t.Helper()
	mp := NewMockProvider(t)
	const sub = "u-verify"
	mp.PrepUser(sub, idTokenClaims)

	users := &fakeUserLookup{
		loginResp: &IssueSessionResp{
			UID:           "uid-verify",
			LoginRespJSON: `{"ok":true}`,
		},
	}
	store := newFakeIdentityStore()
	o := newTestOIDC(t, mp, users, store)
	verify := &fakeVerification{}
	o.verification = verify

	// 预置 state + code,直接走 callback
	state := "st-verify-1"
	nonce := "nonce-verify-1"
	code := "code-verify-1"
	sd := &StateData{
		Provider:       o.cfg.Provider.ID,
		CodeVerifier:   "cv-verify",
		Nonce:          nonce,
		IP:             "1.2.3.4",
		UserAgent:      "ua",
		ClientAuthcode: "ac-verify",
		DeviceFlag:     0,
	}
	if err := o.stateStore.Save(context.Background(), state, sd, time.Minute); err != nil {
		t.Fatalf("stateStore.Save: %v", err)
	}
	mp.PrepCode(code, sub, nonce)

	r := newTestRouter(o)
	reqURL := fmt.Sprintf("/v1/auth/oidc/aegis/callback?state=%s&code=%s",
		url.QueryEscape(state), url.QueryEscape(code))
	req := httptest.NewRequest("GET", reqURL, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return verify, w.Code
}

// TestOIDCCallback_Verification_FullClaimsWritten 覆盖:Aegis 返回完整
// identity_verification claims → OIDC callback 应 upsert user_verification 一次,
// 参数透传正确。
func TestOIDCCallback_Verification_FullClaimsWritten(t *testing.T) {
	claims := map[string]interface{}{
		"email":             "a@example.com",
		"email_verified":    true,
		"is_verified":       true,
		"verified_at":       int64(1_715_000_000),
		"verified_provider": "cas.example.com",
		"legal_name":        "张三",
		"legal_email":       "zhangsan@corp.example.com",
	}
	verify, status := runCallbackWithClaims(t, claims)
	if status != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", status)
	}
	if verify.count() != 1 {
		t.Fatalf("UpsertVerificationFromOIDC calls = %d, want 1", verify.count())
	}
	last, _ := verify.last()
	if last.UID != "uid-verify" {
		t.Errorf("UID = %q, want %q", last.UID, "uid-verify")
	}
	if last.Claims.LegalName != "张三" {
		t.Errorf("LegalName = %q", last.Claims.LegalName)
	}
	if last.Claims.LegalEmail != "zhangsan@corp.example.com" {
		t.Errorf("LegalEmail = %q", last.Claims.LegalEmail)
	}
	if last.Claims.VerifiedProvider != "cas.example.com" {
		t.Errorf("VerifiedProvider = %q", last.Claims.VerifiedProvider)
	}
	if last.Claims.VerifiedAt != 1_715_000_000 {
		t.Errorf("VerifiedAt = %d", last.Claims.VerifiedAt)
	}
}

// TestOIDCCallback_Verification_IsVerifiedFalse_NoWrite 覆盖:is_verified=false →
// callback 不调 UpsertVerificationFromOIDC(仍登录成功)。
func TestOIDCCallback_Verification_IsVerifiedFalse_NoWrite(t *testing.T) {
	claims := map[string]interface{}{
		"email":             "a@example.com",
		"email_verified":    true,
		"is_verified":       false,
		"verified_at":       int64(1_715_000_000),
		"verified_provider": "cas.example.com",
		"legal_name":        "张三",
	}
	verify, status := runCallbackWithClaims(t, claims)
	if status != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", status)
	}
	if verify.count() != 0 {
		t.Fatalf("UpsertVerificationFromOIDC should not be called, got %d", verify.count())
	}
}

// TestOIDCCallback_Verification_EmptyLegalName_NoWrite 覆盖:legal_name 缺失 →
// callback 不调 UpsertVerificationFromOIDC,即使 is_verified=true。
func TestOIDCCallback_Verification_EmptyLegalName_NoWrite(t *testing.T) {
	claims := map[string]interface{}{
		"email":             "a@example.com",
		"email_verified":    true,
		"is_verified":       true,
		"verified_at":       int64(1_715_000_000),
		"verified_provider": "cas.example.com",
		// legal_name intentionally omitted
	}
	verify, status := runCallbackWithClaims(t, claims)
	if status != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", status)
	}
	if verify.count() != 0 {
		t.Fatalf("UpsertVerificationFromOIDC should not be called, got %d", verify.count())
	}
}

// TestOIDCCallback_Verification_UpsertFailure_DoesNotBlockLogin 覆盖:
// 写 user_verification 失败只 warn,不阻断登录(ThirdAuthcode 仍应为成功 payload)。
func TestOIDCCallback_Verification_UpsertFailure_DoesNotBlockLogin(t *testing.T) {
	mp := NewMockProvider(t)
	const sub = "u-verify-fail"
	mp.PrepUser(sub, map[string]interface{}{
		"email":             "a@example.com",
		"email_verified":    true,
		"is_verified":       true,
		"verified_at":       int64(1_715_000_000),
		"verified_provider": "cas.example.com",
		"legal_name":        "张三",
	})
	users := &fakeUserLookup{
		loginResp: &IssueSessionResp{
			UID:           "uid-verify-fail",
			LoginRespJSON: `{"ok":true}`,
		},
	}
	store := newFakeIdentityStore()
	o := newTestOIDC(t, mp, users, store)
	verify := &fakeVerification{err: errors.New("fake db down")}
	o.verification = verify
	fa := o.authcode.(*fakeAuthcode)

	state := "st-vf"
	nonce := "nonce-vf"
	code := "code-vf"
	sd := &StateData{
		Provider:       o.cfg.Provider.ID,
		CodeVerifier:   "cv-vf",
		Nonce:          nonce,
		IP:             "1.2.3.4",
		ClientAuthcode: "ac-vf",
	}
	if err := o.stateStore.Save(context.Background(), state, sd, time.Minute); err != nil {
		t.Fatalf("stateStore.Save: %v", err)
	}
	mp.PrepCode(code, sub, nonce)

	r := newTestRouter(o)
	reqURL := fmt.Sprintf("/v1/auth/oidc/aegis/callback?state=%s&code=%s",
		url.QueryEscape(state), url.QueryEscape(code))
	req := httptest.NewRequest("GET", reqURL, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", w.Code)
	}
	if verify.count() != 1 {
		t.Fatalf("UpsertVerificationFromOIDC calls = %d, want 1", verify.count())
	}
	// 登录不受阻断:ThirdAuthcode 应写入成功 payload 而非 "0"
	if got := fa.get("ac-vf"); got == "" || got == "0" {
		t.Errorf("ThirdAuthcode = %q, want success payload (verification failure should not block login)", got)
	}
}

// TestOIDCCallback_Verification_FromUserInfoOnly 覆盖"identity_verification 仅
// 在 /userinfo 暴露"的部署(codex review 指出的关键场景)。
//
// 关键:ID Token 里 email/phone/name 都完整 —— 否则会因为 issue #1307 路径
// 去 fetch userinfo,测不到本 PR 新增的 scope-driven 触发逻辑。只有
// identity_verification 5 字段缺失,必须靠
// hasIdentityVerificationScope && LegalName=="" 才能把 userinfo 拉回来。
func TestOIDCCallback_Verification_FromUserInfoOnly(t *testing.T) {
	mp := NewMockProvider(t)
	const sub = "u-ui-only"
	mp.PrepUser(sub, map[string]interface{}{
		"email":                 "a@example.com",
		"email_verified":        true,
		"phone_number":          "+8613800000000",
		"phone_number_verified": true,
		"name":                  "Alice",
	})
	mp.PrepUserInfoOnly(sub, map[string]interface{}{
		"is_verified":       true,
		"verified_at":       int64(1_715_000_001),
		"verified_provider": "wecom.qy.example",
		"legal_name":        "李四",
		"legal_email":       "lisi@corp.example.com",
	})

	users := &fakeUserLookup{
		loginResp: &IssueSessionResp{
			UID:           "uid-ui-only",
			LoginRespJSON: `{"ok":true}`,
		},
	}
	store := newFakeIdentityStore()
	o := newTestOIDC(t, mp, users, store)
	verify := &fakeVerification{}
	o.verification = verify

	state := "st-ui"
	nonce := "nonce-ui"
	code := "code-ui"
	sd := &StateData{
		Provider:       o.cfg.Provider.ID,
		CodeVerifier:   "cv-ui",
		Nonce:          nonce,
		IP:             "1.2.3.4",
		ClientAuthcode: "ac-ui",
	}
	if err := o.stateStore.Save(context.Background(), state, sd, time.Minute); err != nil {
		t.Fatalf("stateStore.Save: %v", err)
	}
	mp.PrepCode(code, sub, nonce)

	r := newTestRouter(o)
	reqURL := fmt.Sprintf("/v1/auth/oidc/aegis/callback?state=%s&code=%s",
		url.QueryEscape(state), url.QueryEscape(code))
	req := httptest.NewRequest("GET", reqURL, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", w.Code)
	}
	if verify.count() != 1 {
		t.Fatalf("UpsertVerificationFromOIDC calls = %d, want 1 (userinfo merge path)", verify.count())
	}
	last, _ := verify.last()
	if last.Claims.LegalName != "李四" {
		t.Errorf("LegalName = %q, want %q", last.Claims.LegalName, "李四")
	}
	if last.Claims.VerifiedProvider != "wecom.qy.example" {
		t.Errorf("VerifiedProvider = %q", last.Claims.VerifiedProvider)
	}
	if last.Claims.VerifiedAt != 1_715_000_001 {
		t.Errorf("VerifiedAt = %d", last.Claims.VerifiedAt)
	}
}
