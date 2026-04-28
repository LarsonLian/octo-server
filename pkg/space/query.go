package space

import (
	"github.com/gocraft/dbr/v2"
)

// SystemBots 是所有 Space 都可见的系统级 Bot UID。
var SystemBots = map[string]bool{
	"botfather":     true,
	"u_10000":       true,
	"fileHelper":    true,
	"notification":  true,
}

// GetBotUIDs 从给定 UID 列表中查询哪些是 Bot（robot=1），排除系统 Bot。
// 返回 Bot UID 集合。DB 查询失败时返回 error。
func GetBotUIDs(session *dbr.Session, uids []string) (map[string]bool, error) {
	result := make(map[string]bool)
	if len(uids) == 0 {
		return result, nil
	}
	var nonSystemUIDs []string
	for _, uid := range uids {
		if !SystemBots[uid] {
			nonSystemUIDs = append(nonSystemUIDs, uid)
		}
	}
	if len(nonSystemUIDs) == 0 {
		return result, nil
	}
	var botUIDs []string
	_, err := session.Select("uid").From("`user`").
		Where("uid IN ? AND robot=1", nonSystemUIDs).
		Load(&botUIDs)
	if err != nil {
		return nil, err
	}
	for _, uid := range botUIDs {
		result[uid] = true
	}
	return result, nil
}

// CheckBotsInSpace 查询给定 Bot UID 中哪些是指定 Space 的成员。
// 返回在 Space 中的 Bot UID 集合。DB 查询失败时返回 error。
func CheckBotsInSpace(session *dbr.Session, spaceID string, botUIDs map[string]bool) (map[string]bool, error) {
	result := make(map[string]bool)
	if spaceID == "" || len(botUIDs) == 0 {
		return result, nil
	}
	uids := make([]string, 0, len(botUIDs))
	for uid := range botUIDs {
		uids = append(uids, uid)
	}
	var memberUIDs []string
	_, err := session.Select("uid").From("space_member").
		Where("space_id=? AND uid IN ? AND status=1", spaceID, uids).
		Load(&memberUIDs)
	if err != nil {
		return nil, err
	}
	for _, uid := range memberUIDs {
		result[uid] = true
	}
	return result, nil
}

// GetGroupSpaceMap 批量查询群的 space_id，返回 groupNo -> spaceID 映射。
// 需要传入一个能执行 GetGroups 的回调。
func GetGroupSpaceMap(groupNos []string, getGroups func([]string) ([]GroupSpaceInfo, error)) (map[string]string, error) {
	result := make(map[string]string, len(groupNos))
	if len(groupNos) == 0 {
		return result, nil
	}
	groups, err := getGroups(groupNos)
	if err != nil {
		return result, err
	}
	for _, g := range groups {
		result[g.GroupNo] = g.SpaceID
	}
	return result, nil
}

// GroupSpaceInfo 用于 GetGroupSpaceMap 回调的最小接口。
type GroupSpaceInfo struct {
	GroupNo string
	SpaceID string
}
