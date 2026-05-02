package botfather

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// YUJ-231 / GH#1291 — robotApply space_id membership 校验（纵深防御）。
//
// 背景：botfather.robotApply 从 body/query/header 读 space_id，最终写入
// bot notify payload（notifyOwnerNewApply / notifyApplicantResult / createFriendRelation
// 欢迎消息）。无 CheckMembership 校验时，攻击者可伪造任意 space_id，让 bot
// 通知 DM 出现在受害者非成员 Space 视图。与 P1-2 friendApply 同构修复。
//
// 测试风格沿用 modules/group/api_xspaceid_membership_test.go 的源码 grep。

// Test 1: robotApply 必须对 claim 的 space_id 做 CheckMembership(loginUID)。
func TestRobotApply_SpaceIDNotMember_FallsBackAndLogs(t *testing.T) {
	body := mustReadBotFatherFunc(t, "api_apply.go", "func (bf *BotFather) robotApply(")

	// A. 必须调用 spacepkg.CheckMembership(..., loginUID) —— loginUID 是申请者。
	assert.Regexp(t,
		regexp.MustCompile(`spacepkg\.CheckMembership\(\s*bf\.ctx\.DB\(\)\s*,\s*applySpaceID\s*,\s*loginUID\s*\)`),
		body,
		"robotApply 必须用 spacepkg.CheckMembership 校验 claim 的 space_id 是否收 loginUID")

	// B. 非成员分支必须 bf.Warn + 清空 applySpaceID，文案 "robot apply: not a member"。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^}]*bf\.Warn\(\s*"robot apply: not a member of claimed space[^"]*"[^}]*applySpaceID\s*=\s*""`),
		body,
		"robotApply 非成员分支必须 Warn 并把 applySpaceID 清空")

	// C. DB 出错也必须降级（bf.Error + 清空），不 return 5xx。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)membershipErr\s*!=\s*nil[^}]*bf\.Error\(\s*"robot 申请 space_id[^"]*"[^}]*applySpaceID\s*=\s*""`),
		body,
		"robotApply 的 CheckMembership 出错必须 Error + 降级，不抛 5xx")

	// D. 校验必须在 applyDB.insert 之**前**，否则脏 space_id 已写入 DB，
	//    下游 robotApplySure / robotApplyRefuse 会直接从 apply.SpaceID 读出。
	checkIdx := strings.Index(body, "spacepkg.CheckMembership(bf.ctx.DB(), applySpaceID, loginUID)")
	insertIdx := strings.Index(body, "applyDB.insert(apply)")
	assert.NotEqual(t, -1, checkIdx, "CheckMembership 缺失 → YUJ-231 回归")
	assert.NotEqual(t, -1, insertIdx, "applyDB.insert 调用消失 → 函数体被改")
	assert.Less(t, checkIdx, insertIdx,
		"CheckMembership 必须在 applyDB.insert 之前，否则 DB 里的 apply.SpaceID 依然是脏数据")

	// E. 规整后的 applySpaceID 必须被 notifyOwnerNewApply 使用（而不是又
	//    从 req 读一次脏数据）。
	notifyCallIdx := strings.Index(body, "bf.notifyOwnerNewApply(loginUID, req.RobotUID, robot.CreatorUID, req.Remark, applySpaceID)")
	assert.NotEqual(t, -1, notifyCallIdx,
		"notifyOwnerNewApply 必须用校验后的 applySpaceID 变量（不是 req.SpaceID / header），否则 payload 依然是攻击者 claim 值")
	assert.Less(t, checkIdx, notifyCallIdx,
		"CheckMembership 必须在 notifyOwnerNewApply 之前")
}

// Test 2: api_apply.go 必须仍 import spacepkg。
func TestBotFatherAPIApply_StillImportsSpacepkg_YUJ231(t *testing.T) {
	src, err := os.ReadFile("api_apply.go")
	assert.NoError(t, err)
	assert.Contains(t, string(src), `spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"`,
		"api_apply.go 必须保留 spacepkg import；robotApply 的核心路径依赖它")
}

// mustReadBotFatherFunc 读取 filename，截取以 prefix 开头的函数体到下一个
// `func (bf *BotFather) ` 开头之前，作为 grep 作用域。
func mustReadBotFatherFunc(t *testing.T, filename, prefix string) string {
	t.Helper()
	src, err := os.ReadFile(filename)
	assert.NoError(t, err)
	text := string(src)
	start := strings.Index(text, prefix)
	if start == -1 {
		t.Fatalf("函数 %q 不存在于 %s（是否被误删 / 被 rename）", prefix, filename)
	}
	rest := text[start:]
	nextFuncOffset := strings.Index(rest[1:], "func (bf *BotFather) ")
	if nextFuncOffset == -1 {
		return rest
	}
	return rest[:nextFuncOffset+1]
}
