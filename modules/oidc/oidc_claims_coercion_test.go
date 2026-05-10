package oidc

import (
	"encoding/json"
	"testing"
)

// IsVerifiedClaim 多形态 Unmarshal:bool / number / string / null / missing。
// 目的:Aegis 把 wire type 切到 string("true"),或网关把 true 变 1 时,
// callback 不挂;非法值保守落 false(等价 "未实名",不会脏数据污染 user_verification)。
func TestIsVerifiedClaim_Unmarshal(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		want    bool
		wantErr bool
	}{
		{"bool_true", `true`, true, false},
		{"bool_false", `false`, false, false},
		{"int_1", `1`, true, false},
		{"int_0", `0`, false, false},
		{"int_42", `42`, true, false},
		{"float_1", `1.0`, true, false},
		{"string_true_lower", `"true"`, true, false},
		{"string_True_mixed", `"True"`, true, false},
		{"string_TRUE_upper", `"TRUE"`, true, false},
		{"string_1", `"1"`, true, false},
		{"string_yes", `"yes"`, true, false},
		{"string_with_spaces", `"  true  "`, true, false},
		{"string_false", `"false"`, false, false},
		{"string_0", `"0"`, false, false},
		{"string_empty", `""`, false, false},
		{"string_no_match", `"foo"`, false, false},
		{"null", `null`, false, false},
		{"object_unsupported", `{"x":1}`, false, true},
		{"array_unsupported", `[1]`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c IsVerifiedClaim
			err := json.Unmarshal([]byte(tc.json), &c)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want err, got nil (value=%v)", bool(c))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if bool(c) != tc.want {
				t.Fatalf("IsVerifiedClaim = %v, want %v (json=%s)", bool(c), tc.want, tc.json)
			}
			if c.Bool() != tc.want {
				t.Fatalf("Bool() = %v, want %v", c.Bool(), tc.want)
			}
		})
	}
}

// 缺字段 → zero value false(struct-level)。
func TestIsVerifiedClaim_MissingField(t *testing.T) {
	var s struct {
		IsVerified IsVerifiedClaim `json:"is_verified"`
	}
	// 没有 is_verified 字段
	if err := json.Unmarshal([]byte(`{"other":"x"}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bool(s.IsVerified) != false {
		t.Fatalf("missing field should default false, got %v", bool(s.IsVerified))
	}
}

// 验证 IsVerifiedClaim 与 bool 可以用于条件判断、逻辑运算(回归验证)。
func TestIsVerifiedClaim_ConditionalUsage(t *testing.T) {
	var c IsVerifiedClaim = true
	if !c { // 直接用于条件
		t.Fatal("expected c to be truthy")
	}
	if !(c && true) { // && 与 bool 互操作
		t.Fatal("expected c && true")
	}
}

// VerifiedAtClaim 多形态 Unmarshal:int64 / float / string / null / missing。
// 目的:IdP 把 number 落成 string("1778331902") 或 JS 网关把 Unix 秒序列化成
// float(1.778e9)时不挂。非法字符串落 0,下游 VerifiedAt<=0 分支保护。
func TestVerifiedAtClaim_Unmarshal(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		want    int64
		wantErr bool
	}{
		{"int_unix_sec", `1715000000`, 1715000000, false},
		{"int_zero", `0`, 0, false},
		{"int_negative", `-1`, -1, false},
		{"float_unix_sec", `1715000000.0`, 1715000000, false},
		{"float_large_sci", `1.778331902e9`, 1778331902, false},
		{"string_int", `"1715000000"`, 1715000000, false},
		{"string_zero", `"0"`, 0, false},
		{"string_float", `"1715000000.5"`, 1715000000, false},
		{"string_empty", `""`, 0, false},
		{"string_invalid", `"notanumber"`, 0, false},
		{"null", `null`, 0, false},
		{"object_unsupported", `{"x":1}`, 0, true},
		{"array_unsupported", `[1]`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c VerifiedAtClaim
			err := json.Unmarshal([]byte(tc.json), &c)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want err, got nil (value=%d)", int64(c))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if int64(c) != tc.want {
				t.Fatalf("VerifiedAtClaim = %d, want %d (json=%s)", int64(c), tc.want, tc.json)
			}
			if c.Int64() != tc.want {
				t.Fatalf("Int64() = %d, want %d", c.Int64(), tc.want)
			}
		})
	}
}

// 缺字段 → zero value 0。
func TestVerifiedAtClaim_MissingField(t *testing.T) {
	var s struct {
		VerifiedAt VerifiedAtClaim `json:"verified_at"`
	}
	if err := json.Unmarshal([]byte(`{"other":"x"}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if int64(s.VerifiedAt) != 0 {
		t.Fatalf("missing field should default 0, got %d", int64(s.VerifiedAt))
	}
}

// End-to-end:完整 IDTokenClaims 按 Aegis 文档(string 形态)+ 实测(bool+int 形态)
// 混用一次,两种 wire 都要能解出同一逻辑值。
func TestIDTokenClaims_VerificationFieldsCoercion(t *testing.T) {
	// 实测形态:bool + number
	wireNative := `{
		"sub": "alice",
		"is_verified": true,
		"verified_at": 1715000000,
		"verified_provider": "cas.example.com",
		"legal_name": "张三"
	}`
	// 文档形态:都是 string
	wireStringly := `{
		"sub": "alice",
		"is_verified": "true",
		"verified_at": "1715000000",
		"verified_provider": "cas.example.com",
		"legal_name": "张三"
	}`

	for _, raw := range []string{wireNative, wireStringly} {
		var c IDTokenClaims
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("unmarshal(%s): %v", raw, err)
		}
		if !c.IsVerified.Bool() {
			t.Fatalf("IsVerified = false, want true (wire=%s)", raw)
		}
		if c.VerifiedAt.Int64() != 1715000000 {
			t.Fatalf("VerifiedAt = %d, want 1715000000 (wire=%s)", c.VerifiedAt.Int64(), raw)
		}
		if c.LegalName != "张三" {
			t.Fatalf("LegalName = %q (wire=%s)", c.LegalName, raw)
		}
	}
}
