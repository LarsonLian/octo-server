package message

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/stretchr/testify/assert"
)

// 构造模拟 WuKongIM payload（UseNumber 模式），贴近真实反序列化语义。
func decodePayload(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

// TestSyncMessages_ExternalMarker 外部群里外部人员的消息，sync 响应应携带外部来源标识。
func TestSyncMessages_ExternalMarker(t *testing.T) {
	markers := map[string]group.MemberExternalMarker{
		"ext-uid": {IsExternal: 1, SourceSpaceName: "ExampleCorp"},
	}
	messages := []*MsgSyncResp{
		{FromUID: "ext-uid"},
	}

	applyExternalMarkers(messages, markers)

	assert.Equal(t, 1, messages[0].FromIsExternal,
		"外部成员消息的 from_is_external 应为 1")
	assert.Equal(t, "ExampleCorp", messages[0].FromSourceSpaceName,
		"外部成员消息应透传其 source_space_name")
}

// TestSyncMessages_InternalMarker 同一外部群中内部人员的消息不应带外部 marker。
func TestSyncMessages_InternalMarker(t *testing.T) {
	markers := map[string]group.MemberExternalMarker{
		"ext-uid":      {IsExternal: 1, SourceSpaceName: "ExampleCorp"},
		"internal-uid": {IsExternal: 0},
	}
	messages := []*MsgSyncResp{
		{FromUID: "internal-uid"},
	}

	applyExternalMarkers(messages, markers)

	assert.Equal(t, 0, messages[0].FromIsExternal,
		"内部成员 from_is_external 必须为 0")
	assert.Equal(t, "", messages[0].FromSourceSpaceName,
		"内部成员不应带 source_space_name，避免前端误渲染")
}

// TestSyncMessages_UnknownSender 出现在消息里但已不在群（已退群）的 FromUID，
// 应该保持默认 0 / ""，不能 panic 或泄漏其他群的来源。
func TestSyncMessages_UnknownSender(t *testing.T) {
	markers := map[string]group.MemberExternalMarker{
		"ext-uid": {IsExternal: 1, SourceSpaceName: "ExampleCorp"},
	}
	messages := []*MsgSyncResp{
		{FromUID: "ghost-uid"},
	}

	applyExternalMarkers(messages, markers)

	assert.Equal(t, 0, messages[0].FromIsExternal)
	assert.Equal(t, "", messages[0].FromSourceSpaceName)
}

// TestMergeforward_UsersExtended mergeforward 消息的 content.users 每个元素
// 应补上 is_external / source_space_name，保持其他字段原样。
func TestMergeforward_UsersExtended(t *testing.T) {
	rawPayload := `{
		"type": 11,
		"title": "聊天记录",
		"users": [
			{"uid": "ext-uid", "name": "外部张三"},
			{"uid": "internal-uid", "name": "内部李四"},
			{"uid": "ghost-uid", "name": "已退群王五"}
		],
		"messages": [{"content": "hello"}]
	}`
	payload := decodePayload(t, rawPayload)

	markers := map[string]group.MemberExternalMarker{
		"ext-uid":      {IsExternal: 1, SourceSpaceName: "ExampleCorp"},
		"internal-uid": {IsExternal: 0},
	}
	messages := []*MsgSyncResp{
		{
			FromUID: "ext-uid",
			Payload: payload,
		},
	}

	applyExternalMarkers(messages, markers)

	// 顶层 marker 同步生效
	assert.Equal(t, 1, messages[0].FromIsExternal)
	assert.Equal(t, "ExampleCorp", messages[0].FromSourceSpaceName)

	// users 列表被扩展
	users, ok := payload["users"].([]interface{})
	assert.True(t, ok, "users 仍应是数组")
	assert.Len(t, users, 3, "users 元素数量不应变")

	u0 := users[0].(map[string]interface{})
	assert.Equal(t, 1, u0["is_external"], "外部成员 is_external=1")
	assert.Equal(t, "ExampleCorp", u0["source_space_name"])
	assert.Equal(t, "外部张三", u0["name"], "已有字段不能丢")

	u1 := users[1].(map[string]interface{})
	assert.Equal(t, 0, u1["is_external"], "内部成员 is_external=0")
	assert.Equal(t, "", u1["source_space_name"], "内部成员 source_space_name 为空")

	u2 := users[2].(map[string]interface{})
	assert.Equal(t, 0, u2["is_external"], "已退群用户默认非外部")
	assert.Equal(t, "", u2["source_space_name"])
}

// TestMergeforward_NonMergeforwardUntouched 非 mergeforward 消息的 payload 不应被动到。
func TestMergeforward_NonMergeforwardUntouched(t *testing.T) {
	payload := decodePayload(t, `{"type": 1, "content": "hello", "users": [{"uid":"ext-uid"}]}`)
	markers := map[string]group.MemberExternalMarker{
		"ext-uid": {IsExternal: 1, SourceSpaceName: "ExampleCorp"},
	}
	messages := []*MsgSyncResp{{FromUID: "ext-uid", Payload: payload}}

	applyExternalMarkers(messages, markers)

	users, _ := payload["users"].([]interface{})
	u0 := users[0].(map[string]interface{})
	_, hasIsExt := u0["is_external"]
	assert.False(t, hasIsExt,
		"非 mergeforward 消息不应污染 payload.users[*].is_external")

	// 但顶层 from_is_external 仍应生效（这条消息的发送者是外部成员）。
	assert.Equal(t, 1, messages[0].FromIsExternal)
	// type 断言：payloadMsgType 应正确识别文本消息 (type=1)，
	// 间接保证 mergeforward 分支只对 type=11 生效。
	assert.Equal(t, common.Text.Int(), payloadMsgType(payload))
}

// TestApplyExternalMarkers_NoopCases enrichment 在空输入下必须静默返回。
func TestApplyExternalMarkers_NoopCases(t *testing.T) {
	// 空消息数组
	applyExternalMarkers(nil, map[string]group.MemberExternalMarker{})

	// 空 marker 映射（例如群暂无任何外部成员）—— 消息不应被修改
	messages := []*MsgSyncResp{{FromUID: "ext-uid"}}
	applyExternalMarkers(messages, nil)
	assert.Equal(t, 0, messages[0].FromIsExternal)
	assert.Equal(t, "", messages[0].FromSourceSpaceName)
}
