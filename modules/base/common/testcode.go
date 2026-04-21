package common

import (
	"errors"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
)

// IsTestCodeEnabled 仅在非 release 模式且配置了 SMSCode 时返回 true。
// release 模式永远返回 false，避免通过配置热更新把测试验证码打开成万能验证码。
func IsTestCodeEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Mode == config.ReleaseMode {
		return false
	}
	return strings.TrimSpace(cfg.SMSCode) != ""
}

// MatchTestCode 在 IsTestCodeEnabled 为 true 时，比较用户输入（去空白）与配置的 SMSCode。
func MatchTestCode(cfg *config.Config, code string) bool {
	if !IsTestCodeEnabled(cfg) {
		return false
	}
	return strings.TrimSpace(cfg.SMSCode) == strings.TrimSpace(code)
}

// maskEmail 将 foo@bar.com 脱敏为 f**@bar.com，避免 warn 日志泄露完整邮箱。
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:]
	if len(local) <= 1 {
		return "*" + domain
	}
	return local[:1] + strings.Repeat("*", len(local)-1) + domain
}

// ValidateTestCodeConfig 在启动时校验：release 模式下不允许配置 SMSCode。
func ValidateTestCodeConfig(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.Mode == config.ReleaseMode && strings.TrimSpace(cfg.SMSCode) != "" {
		return errors.New("release 模式禁止配置 smsCode，避免万能验证码后门")
	}
	return nil
}
