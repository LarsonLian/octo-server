package space

import (
	"sort"
	"strings"
	"sync"
)

const SpaceChannelPrefix = "s"

// knownSpaceIDs 已知的 spaceId 列表（按长度降序排列，优先匹配最长前缀）
var (
	knownSpaceIDs []string
	knownMu       sync.RWMutex
)

// RegisterSpaceIDs 注册已知的 spaceId 列表（启动时从 DB 加载）
func RegisterSpaceIDs(ids []string) {
	knownMu.Lock()
	defer knownMu.Unlock()
	knownSpaceIDs = make([]string, len(ids))
	copy(knownSpaceIDs, ids)
	// 按长度降序排列，优先匹配最长前缀
	sort.Slice(knownSpaceIDs, func(i, j int) bool {
		return len(knownSpaceIDs[i]) > len(knownSpaceIDs[j])
	})
}

// BuildChannelID 构建 Space 内的 channel_id
// 个人空间返回原始 peerID
func BuildChannelID(spaceID, peerID string) string {
	if spaceID == "" {
		return peerID
	}
	return SpaceChannelPrefix + spaceID + "_" + peerID
}

// ParseChannelID 从 channel_id 解析 space_id 和 peer_id
// 优先用已知 spaceId 列表做最长前缀匹配，回退用 LastIndex
func ParseChannelID(channelID string) (spaceID, peerID string) {
	if !strings.HasPrefix(channelID, SpaceChannelPrefix) {
		return "", channelID
	}
	rest := channelID[len(SpaceChannelPrefix):]

	// 优先用已知 spaceId 列表匹配（按长度降序，最长优先）
	knownMu.RLock()
	ids := knownSpaceIDs
	knownMu.RUnlock()
	for _, sid := range ids {
		prefix := sid + "_"
		if strings.HasPrefix(rest, prefix) {
			return sid, rest[len(prefix):]
		}
	}

	// 回退：LastIndex（适用于 peerID 不含下划线的情况）
	if idx := strings.LastIndex(rest, "_"); idx > 0 {
		return rest[:idx], rest[idx+1:]
	}
	return "", channelID
}
