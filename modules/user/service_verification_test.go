package user

import "testing"

// TestStripOIDCVerifiedProvider 覆盖 verified_provider → source 一级名的剥离逻辑,
// 与 UpsertVerificationFromOIDC 里 allowlist 白名单拼合形成"脏值拒写"防线。
func TestStripOIDCVerifiedProvider(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"standard aegis cas domain", "cas.example.com", "cas"},
		{"already stripped", "cas", "cas"},
		{"uppercase normalised", "CAS.Example.COM", "cas"},
		{"wecom full", "wecom.qy.weixin.qq.com", "wecom"},
		{"feishu simple", "feishu", "feishu"},
		{"surrounding whitespace", "  cas.example  ", "cas"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"leading dot stays empty", ".cas.example", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripOIDCVerifiedProvider(tc.input)
			if got != tc.want {
				t.Fatalf("stripOIDCVerifiedProvider(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestUpsertVerificationFromOIDC_ValidationRejections 覆盖所有"不写库直接报错"
// 的快速失败分支,不触 DB。用 *Service 字面构造(verificationDB 保持 nil),
// 若任何 case 意外走到 DB 写入则因 nil-deref 直接 panic,相当于强制 assert。
func TestUpsertVerificationFromOIDC_ValidationRejections(t *testing.T) {
	svc := &Service{} // 不设 verificationDB / extLogin —— 任一被调到都会 panic

	cases := []struct {
		name    string
		uid     string
		claims  OIDCVerificationClaims
		wantErr bool
	}{
		{
			name: "empty uid",
			uid:  "",
			claims: OIDCVerificationClaims{
				LegalName: "张三", VerifiedAt: 1_715_000_000, VerifiedProvider: "cas.example",
			},
			wantErr: true,
		},
		{
			name: "empty legal_name",
			uid:  "u1",
			claims: OIDCVerificationClaims{
				LegalName: "", VerifiedAt: 1_715_000_000, VerifiedProvider: "cas.example",
			},
			wantErr: true,
		},
		{
			name: "zero verified_at",
			uid:  "u1",
			claims: OIDCVerificationClaims{
				LegalName: "张三", VerifiedAt: 0, VerifiedProvider: "cas.example",
			},
			wantErr: true,
		},
		{
			name: "negative verified_at",
			uid:  "u1",
			claims: OIDCVerificationClaims{
				LegalName: "张三", VerifiedAt: -1, VerifiedProvider: "cas.example",
			},
			wantErr: true,
		},
		{
			name: "empty verified_provider",
			uid:  "u1",
			claims: OIDCVerificationClaims{
				LegalName: "张三", VerifiedAt: 1_715_000_000, VerifiedProvider: "",
			},
			wantErr: true,
		},
		{
			name: "provider not in allowlist",
			uid:  "u1",
			claims: OIDCVerificationClaims{
				LegalName: "张三", VerifiedAt: 1_715_000_000, VerifiedProvider: "evil.example.com",
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpsertVerificationFromOIDC(nil, tc.uid, tc.claims)
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
