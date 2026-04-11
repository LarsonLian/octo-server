package thread

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildThreadCreatedPayload_WithSourceMessageID(t *testing.T) {
	sourceMessageID := int64(2042876115227152384)
	sourcePayload := json.RawMessage(`{"type":1,"content":"hello"}`)

	payload := buildThreadCreatedPayload(
		"shortID123",
		"测试子区",
		"groupNo____shortID123",
		"uid_creator",
		"创建者",
		&sourceMessageID,
		sourcePayload,
	)

	assert.Equal(t, ContentTypeThreadCreated, payload["type"])
	assert.Equal(t, "shortID123", payload["short_id"])
	assert.Equal(t, sourceMessageID, payload["source_message_id"],
		"IM 推送的 payload 应包含 source_message_id")
	assert.Equal(t, int64(1), payload["message_count"])
	assert.NotNil(t, payload["last_message"])
}

func TestBuildThreadCreatedPayload_WithoutSourceMessageID(t *testing.T) {
	payload := buildThreadCreatedPayload(
		"shortID456",
		"无源消息子区",
		"groupNo____shortID456",
		"uid_creator",
		"创建者",
		nil,
		nil,
	)

	assert.Equal(t, ContentTypeThreadCreated, payload["type"])
	assert.Equal(t, "shortID456", payload["short_id"])
	_, hasSourceMsgID := payload["source_message_id"]
	assert.False(t, hasSourceMsgID,
		"source_message_id 为 nil 时不应出现在 payload 中")
	assert.Equal(t, int64(0), payload["message_count"])
}
