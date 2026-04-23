package space

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-server/pkg/db"
	"go.uber.org/zap"
)

// 邀请码生成参数。
//
// 16 hex = 64 bit 熵（原 8 hex 仅 32 bit，见 issue #1000）。DB 列 VARCHAR(40) 兼容旧码。
const (
	inviteCodeHexLen     = 16
	inviteCodeMaxRetries = 3

	envInviteDefaultMaxUses = "DM_SPACE_INVITE_DEFAULT_MAX_USES"
	envInviteDefaultTTL     = "DM_SPACE_INVITE_DEFAULT_TTL"

	inviteDefaultTTL = 72 * time.Hour
)

// generateInviteCodeFn 包级函数变量，测试通过直接赋值注入碰撞场景。
// 非线程安全：测试用例**禁止**使用 t.Parallel()，修改需配合 defer 恢复原值。
var generateInviteCodeFn = defaultGenerateInviteCode

func defaultGenerateInviteCode() (string, error) {
	buf := make([]byte, inviteCodeHexLen/2)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成邀请码随机字节失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// generateInviteCode 通过 generateInviteCodeFn 生成 16 hex 邀请码。
func generateInviteCode() (string, error) {
	return generateInviteCodeFn()
}

// inviteDefaults 邀请码默认 max_uses / expires_at。
// 未设置或非法环境变量时：max_uses=0（不限），expires_at=now+72h。
func inviteDefaults(now time.Time) (int, *time.Time) {
	maxUses := 0
	if v := strings.TrimSpace(os.Getenv(envInviteDefaultMaxUses)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxUses = n
		}
	}
	ttl := inviteDefaultTTL
	if v := strings.TrimSpace(os.Getenv(envInviteDefaultTTL)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ttl = d
		}
	}
	var expiresAt *time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		expiresAt = &t
	}
	return maxUses, expiresAt
}

// applyAutoInviteDefaults 为"无用户输入"的自动路径（createSpaceCore、用户侧 createInvite）
// 填充默认 max_uses / expires_at。
//
// 仅当字段为零值时才填充——MaxUses==0 对这些调用方等价于"未设置"。
// **管理端路径禁止使用本函数**：管理端需要区分"用户显式传 max_uses=0（不限）"与"未传"，
// 应由 handler 自行按 *int 的 nil 与否决定是否应用默认值。
func applyAutoInviteDefaults(model *InvitationModel, now time.Time) {
	defMaxUses, defExpiresAt := inviteDefaults(now)
	if model.MaxUses == 0 {
		model.MaxUses = defMaxUses
	}
	if model.ExpiresAt == nil && defExpiresAt != nil {
		t := db.Time(*defExpiresAt)
		model.ExpiresAt = &t
	}
}

// insertInvitationWithRetry 碰撞重试写入。成功返回最终 code；持续 Duplicate 则在耗尽重试后返回错误。
// 调用方需预先填好 SpaceId/Creator/MaxUses/ExpiresAt/Status 等字段（含默认值），InviteCode 由本函数覆盖。
// 不再内部应用默认值——默认策略由调用方按语义决定。
func (s *Space) insertInvitationWithRetry(model *InvitationModel) (string, error) {
	var lastErr error
	for attempt := 0; attempt < inviteCodeMaxRetries; attempt++ {
		code, err := generateInviteCode()
		if err != nil {
			return "", err
		}
		model.InviteCode = code
		if err := s.db.insertInvitation(model); err == nil {
			return code, nil
		} else {
			lastErr = err
			if strings.Contains(err.Error(), "Duplicate") {
				s.Warn("invite_code 碰撞，重试",
					zap.String("code", code),
					zap.Int("attempt", attempt+1),
				)
				continue
			}
			return "", err
		}
	}
	return "", fmt.Errorf("邀请码生成碰撞重试耗尽: %w", lastErr)
}
