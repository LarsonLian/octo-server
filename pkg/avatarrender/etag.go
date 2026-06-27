package avatarrender

import (
	"fmt"
	"hash/crc32"
	"strings"
)

// ETag 为服务端渲染的默认头像计算一个**弱 ETag**（W/ 前缀）。parts 应覆盖所有
// 决定图像内容的因子（渲染版本、seed→颜色、展示文字）——任一变化都会改变 ETag，
// 使共享缓存/浏览器在改名或换色后能 revalidate 拿到新图，而不是按 max-age 继续
// 返回旧图。这是弱验证符：指纹来自渲染输入而非响应字节，且渲染版本前缀已能在渲染
// 逻辑变更时使其失效；crc32 仅作轻量指纹（非安全用途）。
//
// 与 user 模块历史上的 avatarETag 行为一致；此处提升为共享实现供 group 等复用。
func ETag(parts ...string) string {
	h := crc32.NewIEEE()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf(`W/"%08x"`, h.Sum32())
}

// IfNoneMatch 报告 If-None-Match 头是否匹配 etag（RFC 7232 §3.2 弱比较：忽略
// W/ 前缀）。支持逗号分隔的多个验证符与通配 "*"。
func IfNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	target := etagOpaqueTag(etag)
	for _, tok := range strings.Split(header, ",") {
		if etagOpaqueTag(tok) == target {
			return true
		}
	}
	return false
}

// etagOpaqueTag 去掉 W/ 弱前缀和外层引号，返回不透明标签，用于弱比较。
func etagOpaqueTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	return strings.Trim(s, `"`)
}
