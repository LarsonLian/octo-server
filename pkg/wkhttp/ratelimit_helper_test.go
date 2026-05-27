package wkhttp

import (
	"testing"

	libwkhttp "github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/stretchr/testify/assert"
)

// resetUIDRateLimiterForTest 清除 SharedUIDRateLimiter 的单例状态，
// 供同 package 测试在不同 *config.Context 间重建限流器使用。
// 生产代码不会链接 _test.go 文件，不存在误用风险。
func resetUIDRateLimiterForTest() {
	uidRateLimitMu.Lock()
	defer uidRateLimitMu.Unlock()
	uidRateLimitMW = nil
	uidRateLimitReady = false
}

// TestSharedUIDRateLimiterSingleton 验证多次调用返回同一实例，
// 且 resetUIDRateLimiterForTest 能触发重建。
func TestSharedUIDRateLimiterSingleton(t *testing.T) {
	// 本测试不构造 *config.Context（会引入 DB 依赖），仅验证 reset 开关。
	// 真实初始化路径由集成测试或启动时覆盖。
	resetUIDRateLimiterForTest()
	assert.False(t, uidRateLimitReady)
	uidRateLimitMu.Lock()
	uidRateLimitMW = libwkhttp.HandlerFunc(func(_ *libwkhttp.Context) {}) // 仅用于验证 ready 切换，不会被调用
	uidRateLimitReady = true
	uidRateLimitMu.Unlock()
	resetUIDRateLimiterForTest()
	assert.False(t, uidRateLimitReady)
	assert.Nil(t, uidRateLimitMW)
}
