package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// YUJ-399 · 默认 ClaimsFetcher 实现:Aegis 管理接口拉单用户 identity_verification claims。
//
// 架构:
//   1. admin token 走 OAuth2 client_credentials grant,来自 DM_AEGIS_ADMIN_* env
//      或回退到 DM_OIDC_PROVIDER_CLIENT_ID/SECRET(大多数 Aegis 部署同一对 client
//      同时开放 identity_verification read 权限)。
//   2. 调用 GET {AdminBaseURL}/admin/users/{sub}?include=identity_verification,
//      返回 JSON 反序列化成 OIDCVerificationClaims 的 5 个字段。
//   3. 任一基础设施失败(token 网络 / admin endpoint 5xx / URL 非法)都映射成
//      ErrFetcherUnavailable,调用方(api_realname_pull.go endpoint)据此降级返
//      {realname_verified:false},不 panic / 不 5xx。
//
// 放在 modules/user 而非 modules/oidc 的考量:
//   本 fetcher 归属 user 模块(写的是 user_verification);让 fetcher 住在同模块
//   避免反向 import 环(oidc 已 import user,user 不能 import oidc)。Admin HTTP
//   协议不复杂(一跳 token + 一跳 GET),内联实现 30 行,不值得为复用拆公共包。
//
// (历史记号:原本同一 fetcher 也服务 YUJ-399 Phase 2f TTL refresh worker,但
// worker 方案于 2026-05-10 归档,仅保留本 fetcher 给 Phase 2e endpoint 使用。
// 见 scope cleanup commit 36279c34。)

// aegisAdminFetcherConfig 从 env 读取的配置子集。
type aegisAdminFetcherConfig struct {
	// AdminBaseURL 是 Aegis 管理面的 URL 前缀(不含 /admin/users),如 https://accounts.imocto.cn/api。
	AdminBaseURL string
	// TokenURL 是 client_credentials grant 的 /oauth/token 端点 —— 与 OIDC Discovery
	// token_endpoint 一致(Aegis 部署同一 issuer 下)。为减少对 Discovery 的硬依赖,
	// 这里独立 env 配置,不从 Issuer 动态 discover。
	TokenURL string
	// ClientID / ClientSecret 用于 client_credentials。默认回退到 OIDC provider 的同名字段,
	// 允许独立 client(如果运维给 worker 单独开了一个只读 admin client,可以单独覆盖)。
	ClientID     string
	ClientSecret string
	// Scopes client_credentials 请求的 scope;Aegis 一般需要 "identity_verification.read" 或类似。
	// 默认 nil(发空 scope),由 Aegis 端按 client 默认 scope 兜底。
	Scopes []string
	// HTTPTimeout 单次 HTTP 请求超时;包括 token 拿取 + admin GET。
	HTTPTimeout time.Duration
}

func loadAegisAdminFetcherConfig() aegisAdminFetcherConfig {
	timeoutStr := os.Getenv("DM_AEGIS_ADMIN_HTTP_TIMEOUT")
	timeout := 10 * time.Second
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = d
		}
	}
	cid := os.Getenv("DM_AEGIS_ADMIN_CLIENT_ID")
	if cid == "" {
		cid = os.Getenv("DM_OIDC_PROVIDER_CLIENT_ID")
	}
	cs := os.Getenv("DM_AEGIS_ADMIN_CLIENT_SECRET")
	if cs == "" {
		cs = os.Getenv("DM_OIDC_PROVIDER_CLIENT_SECRET")
	}
	scopesEnv := strings.TrimSpace(os.Getenv("DM_AEGIS_ADMIN_SCOPES"))
	var scopes []string
	if scopesEnv != "" {
		for _, p := range strings.Split(scopesEnv, ",") {
			if t := strings.TrimSpace(p); t != "" {
				scopes = append(scopes, t)
			}
		}
	}
	return aegisAdminFetcherConfig{
		AdminBaseURL: strings.TrimRight(os.Getenv("DM_AEGIS_ADMIN_BASE_URL"), "/"),
		TokenURL:     os.Getenv("DM_AEGIS_ADMIN_TOKEN_URL"),
		ClientID:     cid,
		ClientSecret: cs,
		Scopes:       scopes,
		HTTPTimeout:  timeout,
	}
}

// aegisAdminFetcher 是生产路径的默认 ClaimsFetcher。
//
// 字段含义:
//   - httpClient 由 clientcredentials.Config.Client 生成,内部 auto-refresh token。
//   - adminBase 只保留 scheme+host+path prefix,末尾不含 '/'。
type aegisAdminFetcher struct {
	adminBase  string
	httpClient *http.Client
	ready      bool
}

// newAegisAdminFetcher 从 env 构造 fetcher。env 缺失时返回 ready=false 的壳对象,
// 上层 Start 会因 "fetcher nil 依赖缺" 不启动 worker —— 但配置齐全路径才真正起作用。
func newAegisAdminFetcher(cfg aegisAdminFetcherConfig) *aegisAdminFetcher {
	if cfg.AdminBaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return &aegisAdminFetcher{ready: false}
	}
	cc := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	// 把带 Timeout 的 http.Client 经 oauth2.HTTPClient context key 传给 clientcredentials。
	// 这样 token endpoint 调用本身就受 HTTPTimeout 保护;同时 cc.Client 返回的 http.Client
	// 的 Transport 会包一层自动 refresh。最后再设一次 hc.Timeout 让真正的 admin GET 也受限。
	base := &http.Client{Timeout: cfg.HTTPTimeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	hc := cc.Client(ctx)
	hc.Timeout = cfg.HTTPTimeout
	return &aegisAdminFetcher{
		adminBase:  cfg.AdminBaseURL,
		httpClient: hc,
		ready:      true,
	}
}

// aegisAdminUserResponse 与 Aegis /admin/users/{sub} 的返回 JSON 约定对齐。
// 字段子集与 UserInfoClaims / IDTokenClaims 保持一致,直接复用 IsVerifiedClaim /
// VerifiedAtClaim 的 bool/number/string 兜底解码器。
//
// 注意:IsVerifiedClaim / VerifiedAtClaim 定义在 modules/oidc 中,user 包不能
// import oidc(循环)。这里用本地 minimal mirror(verifiedFlexBool / verifiedFlexTime)
// 复制相同解码语义,保证 Aegis 侧 wire-type 漂移时 worker 和 OIDC callback 同步防御。
type aegisAdminUserResponse struct {
	Sub              string           `json:"sub"`
	IsVerified       verifiedFlexBool `json:"is_verified"`
	VerifiedAt       verifiedFlexTime `json:"verified_at"`
	VerifiedProvider string           `json:"verified_provider"`
	LegalName        string           `json:"legal_name"`
	LegalEmail       string           `json:"legal_email"`
}

// FetchClaims 实现 ClaimsFetcher。
//
// 行为合约:
//   - sub 空 + uid 空 → 返回 error(编程错误)。sub 空时用 uid 兜底(Aegis 管理面通常支持 external_id)。
//   - Aegis 404 → (nil, nil):该用户在 Aegis 侧不存在 / 未实名,不算错误
//   - Aegis 5xx / 网络 → ErrFetcherUnavailable:调用方(endpoint)降级返 verified=false
//   - Aegis 2xx but is_verified=false → **归一返 (nil, nil)**(与 404 同义),
//     与 OIDC callback (modules/oidc/api.go::hasCompleteVerificationClaims) 语义对齐。
//     之前此处注释说"返回 claims(LegalName='')",是 R4 前的旧行为;Round 2 Critical 2
//     修复后改成 (nil,nil),YUJ-403 PR #1367 R5 Jerry-Xin Non-blocking 要求同步注释。
//   - JSON 解析失败 → 返回 err(非 ErrFetcherUnavailable),调用方降级返 verified=false
func (f *aegisAdminFetcher) FetchClaims(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error) {
	if !f.ready {
		return nil, ErrFetcherUnavailable
	}
	if sub == "" && uid == "" {
		return nil, errors.New("aegis admin fetch: both uid and sub empty")
	}
	// 优先用 sub 查;sub 空时退化为 uid 查(Aegis 管理面兼容参数)。
	// path 段必须 escape,防 sub 含 '/' 等特殊字符让路由失配。
	key := sub
	if key == "" {
		key = uid
	}
	// include=identity_verification 是 Aegis admin API 约定参数 —— 注释段里我们
	// 一直说要带,Round 1 审查发现实现漏了。显式带上,与注释契约对齐:
	//   * Aegis 严格要求时才返实名 5 字段(非严格部署会直接忽略 unknown query,无副作用)
	//   * 漏带的历史风险:Aegis 稳定返空 claims → worker 永远 upsert 不进,沉默失败
	// 如果未来 Aegis 把参数改名或去掉,只需改这里一行 + fetcher test assertion。
	u := fmt.Sprintf("%s/admin/users/%s?include=identity_verification",
		f.adminBase, url.PathEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("aegis admin fetch: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.httpClient.Do(req)
	if err != nil {
		// 网络 / token 拉取失败都归到 fetcher_unavailable,worker 据此跳过本 tick。
		return nil, fmt.Errorf("%w: %v", ErrFetcherUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// user 在 Aegis 侧不存在,不是错误 —— 告知 worker "无 claims 可写"
		return nil, nil
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrFetcherUnavailable, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// admin token 权限问题,归入 unavailable(整个 fetcher 暂时不可用)
		return nil, fmt.Errorf("%w: http %d (admin token unauthorized)", ErrFetcherUnavailable, resp.StatusCode)
	case resp.StatusCode >= 400:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("aegis admin fetch: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var body aegisAdminUserResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&body); err != nil {
		return nil, fmt.Errorf("aegis admin fetch: decode: %w", err)
	}
	// Round 2 Critical 2:is_verified 必须是写入门槛,与 OIDC callback
	// (modules/oidc/api.go::hasCompleteVerificationClaims)语义对齐。
	//
	// 场景:用户在 Aegis 取消实名但 legal_name / verified_at 字段历史残留,
	// is_verified=false 返下来。之前的实现只检查 LegalName + VerifiedAt,
	// 会把历史残留值误当"已实名"再写回 user_verification,污染 OCTO 徽章显示。
	//
	// 修法(Plan A):fetcher 层直接把 is_verified=false 转成 (nil, nil) ——
	// 与 404 / "该用户在 Aegis 侧无实名"同义。调用方(endpoint)的
	// evalPullFromAegisClaims 会在 claims=nil 时返 verified=false,
	// subject provenance gate 会决定是否清本地 cache(见 api_realname_pull.go)。
	if !body.IsVerified.Bool() {
		return nil, nil
	}
	claims := &OIDCVerificationClaims{
		Subject:          body.Sub,
		VerifiedProvider: body.VerifiedProvider,
		VerifiedAt:       int64(body.VerifiedAt),
		LegalName:        body.LegalName,
		LegalEmail:       body.LegalEmail,
	}
	return claims, nil
}

// -----------------------------------------------------------------------------
// verifiedFlexBool / verifiedFlexTime:oidc.IsVerifiedClaim / VerifiedAtClaim 的
// 本地复刻,为避免 user → oidc 反向 import 而建的镜像类型。
//
// 语义保持一比一对齐,任何一侧调整 wire 兜底(新增 string 别名 / 数字格式),
// 两边都要同步。YUJ-382 相关代码已在 oidc 侧定义时注明了理由。
// -----------------------------------------------------------------------------

type verifiedFlexBool bool

func (c verifiedFlexBool) Bool() bool { return bool(c) }

func (c *verifiedFlexBool) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*c = false
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*c = verifiedFlexBool(b)
		return nil
	}
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*c = verifiedFlexBool(n != 0)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes":
			*c = true
		default:
			*c = false
		}
		return nil
	}
	return fmt.Errorf("aegis admin fetch: unsupported is_verified JSON: %s", string(data))
}

type verifiedFlexTime int64

func (c *verifiedFlexTime) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*c = 0
		return nil
	}
	var i int64
	if err := json.Unmarshal(data, &i); err == nil {
		*c = verifiedFlexTime(i)
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		*c = verifiedFlexTime(int64(f))
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			*c = 0
			return nil
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			*c = verifiedFlexTime(n)
			return nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			*c = verifiedFlexTime(int64(f))
			return nil
		}
		*c = 0
		return nil
	}
	return fmt.Errorf("aegis admin fetch: unsupported verified_at JSON: %s", string(data))
}

// ---------------------------------------------------------------------------
// ClaimsFetcher 接口与 sentinel error。
//
// 原先定义在 realname_refresh_worker.go 中,YUJ-399 Phase 2f worker 方案于
// 2026-05-10 归档后随之删除(见 scope cleanup commit 36279c34)。Phase 2e
// endpoint (api_realname_pull.go) 仍需要 Aegis admin 协议复用,此处保留最小
// 接口 + sentinel 供 endpoint 使用。
// ---------------------------------------------------------------------------

// ClaimsFetcher 抽象 Aegis 管理面 claims 拉取,让 endpoint 在测试中可注入 mock。
type ClaimsFetcher interface {
	FetchClaims(ctx context.Context, uid, sub string) (*OIDCVerificationClaims, error)
}

// ErrFetcherUnavailable 表示 Aegis 整体不可达(网络 / admin token 获取失败 / 5xx 风暴)。
// endpoint 收到此错误会降级返 {realname_verified:false} 而非 500。
var ErrFetcherUnavailable = errors.New("user: realname claims fetcher unavailable")

// claimsUpserter Phase 2e endpoint 与 OIDC callback 共用的写入接口。
//
// (历史记号:名字带 "claims" 前缀是因为原 Phase 2f worker 也复用同一接口;
// worker 删除后名字保留不变,避免 downstream 引用大范围 rename。)
type claimsUpserter interface {
	UpsertVerificationFromOIDC(ctx context.Context, uid string, claims OIDCVerificationClaims) error
}
