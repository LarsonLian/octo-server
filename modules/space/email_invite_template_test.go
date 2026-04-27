package space

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmailInviteAcceptURL(t *testing.T) {
	t.Run("无尾斜杠", func(t *testing.T) {
		got := emailInviteAcceptURL("https://h5.example.com", "abc")
		assert.Equal(t, "https://h5.example.com/v1/space/email-invite?token=abc", got)
	})
	t.Run("有尾斜杠", func(t *testing.T) {
		got := emailInviteAcceptURL("https://h5.example.com/", "abc")
		assert.Equal(t, "https://h5.example.com/v1/space/email-invite?token=abc", got)
	})
	t.Run("token 走 URL 转义（防止 ?/& 截断查询串）", func(t *testing.T) {
		got := emailInviteAcceptURL("https://h5.example.com", "a/b?c=d")
		assert.Contains(t, got, "token=a%2Fb%3Fc%3Dd")
	})
	t.Run("空 base 返回空串", func(t *testing.T) {
		assert.Equal(t, "", emailInviteAcceptURL("", "abc"))
		assert.Equal(t, "", emailInviteAcceptURL("   ", "abc"))
	})
}

func TestBuildOwnerInviteEmail_ContainsKeyFields(t *testing.T) {
	inv := &spaceEmailInviteModel{
		Email:              "to@example.com",
		PlannedName:        "我的团队",
		PlannedDescription: "做大事",
		InviteType:         EmailInviteTypeOwner,
	}
	link := "https://h5.example.com/space-email-invite.html?token=tok123"
	subject, body, err := buildOwnerInviteEmail(inv, "Alice", link)
	assert.NoError(t, err)

	assert.Contains(t, subject, "我的团队")
	assert.Contains(t, body, "我的团队")
	assert.Contains(t, body, "Alice")
	assert.Contains(t, body, link)
	assert.Contains(t, body, "做大事")
}

func TestBuildOwnerInviteEmail_EscapesHTML(t *testing.T) {
	inv := &spaceEmailInviteModel{
		PlannedName:        "<script>alert(1)</script>",
		PlannedDescription: "<img onerror=x>",
		InviteType:         EmailInviteTypeOwner,
	}
	_, body, err := buildOwnerInviteEmail(inv, "B<o>b", "https://h5.example.com/x?token=t")
	assert.NoError(t, err)

	// 危险标签必须被转义，绝不出现在原文中
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.NotContains(t, body, "<img onerror=x>")
	assert.Contains(t, body, "&lt;script&gt;")
	assert.NotContains(t, body, "B<o>b")
}

func TestBuildOwnerInviteEmail_AnonymousInviter(t *testing.T) {
	inv := &spaceEmailInviteModel{PlannedName: "X", InviteType: EmailInviteTypeOwner}
	_, body, err := buildOwnerInviteEmail(inv, "", "https://h5.example.com/?token=t")
	assert.NoError(t, err)
	// 匿名时给出兜底文案，不要出现裸 "by " 或空白
	assert.NotContains(t, body, "by  ")
}

func TestBuildMemberInviteEmail_RoleLabel(t *testing.T) {
	link := "https://h5.example.com/?token=t"

	t.Run("普通成员", func(t *testing.T) {
		inv := &spaceEmailInviteModel{Role: EmailInviteRoleMember, InviteType: EmailInviteTypeMember}
		subj, body, err := buildMemberInviteEmail(inv, "Alice", "Acme", link)
		assert.NoError(t, err)
		assert.Contains(t, subj, "Acme")
		assert.Contains(t, body, "Acme")
		assert.Contains(t, body, link)
		assert.Contains(t, body, "Alice")
		assert.True(t, strings.Contains(body, "成员") && !strings.Contains(body, "管理员"))
	})

	t.Run("管理员", func(t *testing.T) {
		inv := &spaceEmailInviteModel{Role: EmailInviteRoleAdmin, InviteType: EmailInviteTypeMember}
		_, body, err := buildMemberInviteEmail(inv, "Alice", "Acme", link)
		assert.NoError(t, err)
		assert.Contains(t, body, "管理员")
	})
}

func TestBuildMemberInviteEmail_EscapesHTML(t *testing.T) {
	inv := &spaceEmailInviteModel{Role: EmailInviteRoleMember, InviteType: EmailInviteTypeMember}
	_, body, err := buildMemberInviteEmail(inv, "<img>", "<svg/onload=x>", "https://h5.example.com/?token=t")
	assert.NoError(t, err)
	assert.NotContains(t, body, "<svg/onload=x>")
	assert.Contains(t, body, "&lt;svg")
}
