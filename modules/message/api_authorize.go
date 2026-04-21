package message

import (
	"errors"
	"strconv"

	"github.com/Mininglamp-OSS/octo-lib/common"
)

// errMutualDeleteDenied 双向删除未通过授权检查时返回。
var errMutualDeleteDenied = errors.New("用户无权删除此消息")

// errRevokeMessageIDMismatch 撤回请求中 message_id 与 clientMsgNo 反查到的消息不一致时返回。
var errRevokeMessageIDMismatch = errors.New("消息ID与clientMsgNo不匹配")

// authorizeMutualDelete 判定登录用户是否有权对指定消息做双向删除。
// fail-closed：未枚举的频道类型一律拒绝（issue #1063）。
//   - Person: 仅允许删自己发的消息
//   - Group: 需为当前群成员；允许删自己发的或以管理员身份删他人消息
//   - CommunityTopic: 需为父群成员，且仅删自己消息或以父群管理员身份删他人
func authorizeMutualDelete(
	channelType uint8,
	fromUID, loginUID string,
	isGroupMember, isGroupManager,
	isParentGroupMember, isParentGroupManager bool,
) error {
	switch channelType {
	case common.ChannelTypePerson.Uint8():
		if fromUID != loginUID {
			return errMutualDeleteDenied
		}
		return nil
	case common.ChannelTypeGroup.Uint8():
		if !isGroupMember {
			return errMutualDeleteDenied
		}
		if fromUID != loginUID && !isGroupManager {
			return errMutualDeleteDenied
		}
		return nil
	case common.ChannelTypeCommunityTopic.Uint8():
		if !isParentGroupMember {
			return errMutualDeleteDenied
		}
		if fromUID != loginUID && !isParentGroupManager {
			return errMutualDeleteDenied
		}
		return nil
	default:
		return errMutualDeleteDenied
	}
}

// verifyRevokeMessageID 校验撤回请求中用户传入的 message_id 与通过 clientMsgNo
// 反查到的真实 messageID 一致。空字符串视为老客户端未传，跳过比对，但调用方
// 必须以 resolved 值作为后续数据库操作的依据（issue #1048）。
func verifyRevokeMessageID(reqMessageID string, resolvedMessageID int64) error {
	if reqMessageID == "" {
		return nil
	}
	reqID, err := strconv.ParseInt(reqMessageID, 10, 64)
	if err != nil {
		return errRevokeMessageIDMismatch
	}
	if reqID != resolvedMessageID {
		return errRevokeMessageIDMismatch
	}
	return nil
}
