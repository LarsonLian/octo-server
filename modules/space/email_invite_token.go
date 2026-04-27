package space

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
)

// emailInviteTokenBytes token 原文字节长度（256 bit 熵）。
const emailInviteTokenBytes = 32

// generateEmailInviteToken 生成邮件邀请 token；返回明文（base64url 无填充）与其 SHA-256 十六进制哈希。
// 明文用于拼邮件链接，哈希用于入库。明文一旦返回就不再保存。
func generateEmailInviteToken() (raw, hash string, err error) {
	buf := make([]byte, emailInviteTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("read random for email invite token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashEmailInviteToken(raw), nil
}

// hashEmailInviteToken 对明文 token 做 SHA-256 并返回十六进制字符串。
func hashEmailInviteToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// maskInviteEmail 将完整邮箱掩码为 "前 1-2 位 local***@域首字母***.TLD" 形式，
// 例如：
//   - jerry@example.com → je***@e***.com
//   - a@x.io            → a***@x***.io
//   - ab@y.cn           → ab***@y***.cn
//
// 公开预览端点返回该掩码，避免完整邮箱因链接被转发而泄漏。
// 输入无效（无 @ 或域无 . ）时返回 "***"，调用方应已经做过 validateInviteEmail。
func maskInviteEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "***"
	}
	local, domain := email[:at], email[at+1:]
	dot := strings.LastIndex(domain, ".")
	if dot <= 0 || dot == len(domain)-1 {
		return "***"
	}
	keep := 2
	if len(local) < keep {
		keep = len(local)
	}
	return local[:keep] + "***@" + domain[:1] + "***" + domain[dot:]
}

// validateInviteEmail 校验邀请邮箱：使用标准库 net/mail.ParseAddress 拒绝 "@"、"a@"、"@b" 这类
// 表面通过 strings.Contains 但 RFC 上无效的格式（PR #1172 review）。
// 入参应为已 trim 的字符串。
func validateInviteEmail(email string) error {
	if email == "" {
		return fmt.Errorf("邮箱不能为空")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return fmt.Errorf("邮箱格式错误")
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return fmt.Errorf("邮箱格式错误")
	}
	if !strings.Contains(email[at+1:], ".") {
		return fmt.Errorf("邮箱格式错误")
	}
	return nil
}
