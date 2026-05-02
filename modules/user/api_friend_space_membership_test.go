package user

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// YUJ-231 / GH#1290 — friendApply / friendSure space_id membership 校验（纵深防御）。
//
// 背景：friendApply 与 friendSure 从 body/query/header 读 space_id，最终写入
// DM tip payload（:908-910 / :932-934）。无 CheckMembership 校验时，攻击者可
// 任意伪造 space_id，让攻击者-受害者 DM 出现在受害者非成员 Space 视图
// （客户端按 payload.space_id 路由）。修复：claim 后走 spacepkg.CheckMembership，
// 非成员降级为空串 + Warn。
//
// 测试风格沿用 modules/group/api_xspaceid_membership_test.go 的源码 grep：
//   - testutil 不初始化 wkevent 子系统，HTTP 驱动测试会 nil-deref panic。
//   - 源码 grep 锁住关键实现特征，防回归。
//   - 每个 case 都带「不做 X 就回归」的语义说明。

// Test 1: friendApply 必须对 claim 的 space_id 做 CheckMembership(fromUID)
// 校验，非成员 → 降级空串 + Warn，CheckMembership 出错 → Error + 降级（不阻断）。
func TestFriendApply_SpaceIDNotMember_FallsBackAndLogs(t *testing.T) {
	body := mustReadFriendFunc(t, "api_friend.go", "func (f *Friend) friendApply(")

	// A. 必须调用 spacepkg.CheckMembership(..., fromUID) —— fromUID 是当前
	//    登录用户（申请发起方），要验证的是他是否是 claim 的 space 成员。
	assert.Regexp(t,
		regexp.MustCompile(`spacepkg\.CheckMembership\(\s*f\.ctx\.DB\(\)\s*,\s*spaceID\s*,\s*fromUID\s*\)`),
		body,
		"friendApply 必须用 spacepkg.CheckMembership 校验 claim 的 space_id 是否收 fromUID")

	// B. 非成员分支必须 f.Warn + 清空 spaceID，Warn 文案固定 "friend apply: not a member"
	//    方便线上 grep。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^}]*f\.Warn\(\s*"friend apply: not a member of claimed space[^"]*"[^}]*spaceID\s*=\s*""`),
		body,
		"friendApply 非成员分支必须 Warn 并把 spaceID 清空（降级）")

	// C. DB 出错也必须降级（f.Error + 清空），不能 return 5xx 阻断好友申请主流程。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)membershipErr\s*!=\s*nil[^}]*f\.Error\(\s*"好友申请 space_id[^"]*"[^}]*spaceID\s*=\s*""`),
		body,
		"friendApply 的 CheckMembership 出错必须 Error + 降级，不抛 5xx")

	// D. 校验必须在读完 spaceID 之后、写 cache 之前（cache 里的 space_id
	//    会在 friendSure 被 fallback 读出来；如果校验摆在写 cache 之后，
	//    cache 依然存脏数据）。
	checkIdx := strings.Index(body, "spacepkg.CheckMembership(f.ctx.DB(), spaceID, fromUID)")
	cacheWriteIdx := strings.Index(body, `FriendApplyTokenCachePrefix+token+toUser.UID`)
	assert.NotEqual(t, -1, checkIdx, "CheckMembership 缺失 → YUJ-231 回归")
	assert.NotEqual(t, -1, cacheWriteIdx, "cache 写入 key 构造不存在 → 函数体被改")
	assert.Less(t, checkIdx, cacheWriteIdx,
		"CheckMembership 必须在写 FriendApplyTokenCache 之前完成，否则 cache 里的 space_id 依然是脏数据")
}

// Test 2: friendSure 必须对 claim 的 space_id 做 CheckMembership(loginUID) 校验。
func TestFriendSure_SpaceIDNotMember_FallsBackAndLogs(t *testing.T) {
	body := mustReadFriendFunc(t, "api_friend.go", "func (f *Friend) friendSure(")

	// A. 必须 spacepkg.CheckMembership(..., loginUID) —— loginUID 是当前确认者，
	//    写进 DM tip payload 的 space_id 必须是其成员 Space。
	assert.Regexp(t,
		regexp.MustCompile(`spacepkg\.CheckMembership\(\s*f\.ctx\.DB\(\)\s*,\s*spaceID\s*,\s*loginUID\s*\)`),
		body,
		"friendSure 必须用 spacepkg.CheckMembership 校验 claim 的 space_id 是否收 loginUID")

	// B. 非成员分支必须 Warn + 清空 spaceID，文案 "friend sure: not a member"。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^}]*f\.Warn\(\s*"friend sure: not a member of claimed space[^"]*"[^}]*spaceID\s*=\s*""`),
		body,
		"friendSure 非成员分支必须 Warn 并把 spaceID 清空")

	// C. DB 出错也必须降级，不抛 5xx。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)membershipErr\s*!=\s*nil[^}]*f\.Error\(\s*"好友确认 space_id[^"]*"[^}]*spaceID\s*=\s*""`),
		body,
		"friendSure 的 CheckMembership 出错必须 Error + 降级，不抛 5xx")

	// D. 校验必须发生在 cache fallback 之**前**。若先 fallback 再校验，攻击者
	//    让 request spaceID 为空就能绕过校验直接用 cache 里的（由 applyUID
	//    写入，在 friendApply 已校验 → 干净）——这点没问题；但关键是：
	//    来自 request 的 claim 必须先被校验，再触发 fallback。否则一旦未来
	//    有人把 cache 数据链也污染，fallback 就会被直接写进 payload。
	//    因此语义上：CheckMembership 在 `if spaceID == ""` cache fallback 之前。
	checkIdx := strings.Index(body, "spacepkg.CheckMembership(f.ctx.DB(), spaceID, loginUID)")
	fallbackIdx := strings.Index(body, `cachedSpaceID, ok := valueMap["space_id"].(string)`)
	assert.NotEqual(t, -1, checkIdx, "CheckMembership 缺失 → YUJ-231 回归")
	assert.NotEqual(t, -1, fallbackIdx, "cache fallback 分支消失 → 函数体被改")
	assert.Less(t, checkIdx, fallbackIdx,
		"CheckMembership 必须在 cache fallback 之前，保证 request-claim 路径的校验不被 fallback 跳过")
}

// Test 3: api_friend.go 必须仍 import spacepkg；若被清理会导致 CheckMembership
// 调用悄悄消失的 build 错，grep 级先失败给 reviewer 预警。
func TestFriend_StillImportsSpacepkg_YUJ231(t *testing.T) {
	src, err := os.ReadFile("api_friend.go")
	assert.NoError(t, err)
	assert.Contains(t, string(src), `spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"`,
		"api_friend.go 必须保留 spacepkg import；friend apply/sure 的核心路径依赖它")
}

// mustReadFriendFunc 读取 filename，截取以 prefix 开头的函数体到下一个
// `func (f *Friend) ` 开头之前，作为 grep 作用域。
func mustReadFriendFunc(t *testing.T, filename, prefix string) string {
	t.Helper()
	src, err := os.ReadFile(filename)
	assert.NoError(t, err)
	text := string(src)
	start := strings.Index(text, prefix)
	if start == -1 {
		t.Fatalf("函数 %q 不存在于 %s（是否被误删 / 被 rename）", prefix, filename)
	}
	rest := text[start:]
	nextFuncOffset := strings.Index(rest[1:], "func (f *Friend) ")
	if nextFuncOffset == -1 {
		return rest
	}
	return rest[:nextFuncOffset+1]
}
