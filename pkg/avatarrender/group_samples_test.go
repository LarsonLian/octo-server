package avatarrender

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateGroupSamples 仅在设置 AVATAR_GROUP_SAMPLE_DIR 时运行，写出群默认头像
// 样张供肉眼比对设计稿（重点验证 4 字布局/字号不出圆），含文字模式与图标兜底。例：
//
//	AVATAR_GROUP_SAMPLE_DIR=.context/group-samples go test ./pkg/avatarrender/ -run TestGenerateGroupSamples -v
func TestGenerateGroupSamples(t *testing.T) {
	dir := os.Getenv("AVATAR_GROUP_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set AVATAR_GROUP_SAMPLE_DIR to generate group sample PNGs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// (group_no, 群名) — group_no 决定派生色，群名前 4 字决定文字。
	samples := []struct{ groupNo, name string }{
		{"g01", "后端架构讨论"},  // 5 字 → 前 4「后端架构」
		{"g02", "架构讨论"},    // 4 字
		{"g03", "三个字"},     // 3 字
		{"g04", "开发"},      // 2 字
		{"g05", "发"},       // 1 字
		{"g06", "abcd"},    // 4 拉丁
		{"g07", "efgh"},    // 4 拉丁
		{"g08", "产品需求评审组"}, // 前 4「产品需求」
	}
	for _, s := range samples {
		text := GroupText(s.name)
		data, err := RenderGroup(text, GroupStyleForSeed(s.groupNo), 200)
		if err != nil {
			t.Fatalf("render %s: %v", s.name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, s.groupNo+"_"+text+".png"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 图标兜底样张（群名为空），覆盖几个色板色。
	for _, gno := range []string{"icon_a", "icon_b", "icon_c"} {
		data, err := RenderIcon(GroupStyleForSeed(gno))
		if err != nil {
			t.Fatalf("render icon %s: %v", gno, err)
		}
		if err := os.WriteFile(filepath.Join(dir, gno+".png"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote group samples to %s", dir)
}
