package message

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"ascii truncated", "hello world", 5, "hello"},
		{"empty string", "", 10, ""},
		{"zero limit", "abc", 0, ""},
		{"chinese exactly at limit", "你好世界", 4, "你好世界"},
		{"chinese truncated", "你好世界", 2, "你好"},
		{"mixed ascii and chinese", "ab你好cd", 4, "ab你好"},
		{"emoji counted as runes", "a🎉b🎉", 3, "a🎉b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunes(tc.input, tc.maxRunes)
			assert.Equal(t, tc.want, got)
			assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
		})
	}
}

func TestContentToString(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"string", "hello", "hello"},
		{"empty string", "", ""},
		{"nil", nil, ""},
		{"map", map[string]interface{}{"k": "v"}, `{"k":"v"}`},
		{"slice", []interface{}{"a", "b"}, `["a","b"]`},
		{"float number", float64(1.5), "1.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, contentToString(tc.input))
		})
	}
}

func TestCoerceTextPayloadContent(t *testing.T) {
	textType := common.Text.Int()

	t.Run("empty map no-op", func(t *testing.T) {
		var m map[string]interface{}
		CoerceTextPayloadContent(m)
		assert.Nil(t, m)
	})

	t.Run("non-text type untouched", func(t *testing.T) {
		m := map[string]interface{}{
			"type":    float64(2),
			"content": map[string]interface{}{"url": "x"},
		}
		CoerceTextPayloadContent(m)
		_, isMap := m["content"].(map[string]interface{})
		assert.True(t, isMap, "non-text content should stay as-is")
	})

	t.Run("text + string untouched", func(t *testing.T) {
		m := map[string]interface{}{
			"type":    float64(textType),
			"content": "hello",
		}
		CoerceTextPayloadContent(m)
		assert.Equal(t, "hello", m["content"])
	})

	t.Run("text + object coerced to string", func(t *testing.T) {
		m := map[string]interface{}{
			"type":    float64(textType),
			"content": map[string]interface{}{"PSChildName": "msg2.txt"},
		}
		CoerceTextPayloadContent(m)
		s, ok := m["content"].(string)
		assert.True(t, ok, "content should become string")
		assert.Contains(t, s, "PSChildName")
	})

	t.Run("text + missing content no-op", func(t *testing.T) {
		m := map[string]interface{}{"type": float64(textType)}
		CoerceTextPayloadContent(m)
		_, exists := m["content"]
		assert.False(t, exists)
	})

	t.Run("text with json.Number type works", func(t *testing.T) {
		// util.ReadJsonByByte 使用 UseNumber()，type 会是 json.Number
		raw := []byte(`{"type":1,"content":{"a":1}}`)
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		var m map[string]interface{}
		assert.NoError(t, decoder.Decode(&m))
		CoerceTextPayloadContent(m)
		_, ok := m["content"].(string)
		assert.True(t, ok)
	})

	t.Run("text with int typed type works", func(t *testing.T) {
		// 直接构造的 map literal type 字段会是 int，覆盖 isTextType 的 int 分支。
		m := map[string]interface{}{
			"type":    1,
			"content": map[string]interface{}{"k": "v"},
		}
		CoerceTextPayloadContent(m)
		_, ok := m["content"].(string)
		assert.True(t, ok, "int-typed type must be recognized as Text")
	})
}

func TestTruncatedPayload(t *testing.T) {
	errType := common.ContentError.Int()

	t.Run("invalid json fallback", func(t *testing.T) {
		got := TruncatedPayload([]byte("not json"))
		assert.Equal(t, errType, got["type"])
		assert.Equal(t, truncatedContentSuffix, got["content"])
	})

	t.Run("empty json fallback", func(t *testing.T) {
		got := TruncatedPayload([]byte("{}"))
		assert.Equal(t, errType, got["type"])
		assert.Equal(t, truncatedContentSuffix, got["content"])
	})

	t.Run("over hard limit short-circuits without parse", func(t *testing.T) {
		// 2MB 垃圾 bytes，不是合法 JSON 也不应报错
		raw := make([]byte, hardParsePayloadLimit+1)
		for i := range raw {
			raw[i] = 'x'
		}
		got := TruncatedPayload(raw)
		assert.Equal(t, errType, got["type"])
		assert.Equal(t, truncatedContentSuffix, got["content"])
	})

	t.Run("nil input falls back to placeholder", func(t *testing.T) {
		got := TruncatedPayload(nil)
		assert.Equal(t, errType, got["type"])
		assert.Equal(t, truncatedContentSuffix, got["content"])
	})
}

// TestTruncatedPayload_TextType 验证 type=1 走 rune 截断路径（issue #1310）。
func TestTruncatedPayload_TextType(t *testing.T) {
	t.Run("ascii content under rune limit passes through", func(t *testing.T) {
		content := strings.Repeat("a", 3000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": content,
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, content, got["content"], "Text content under rune limit must not be truncated")
	})

	t.Run("chinese content under rune limit passes through", func(t *testing.T) {
		// 3000 中文字符 ≈ 9KB 字节，旧字节阈值会截断，新 rune 阈值不会。
		content := strings.Repeat("你", 3000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": content,
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, content, got["content"], "3000 Chinese runes must pass through under rune-based gate")
	})

	t.Run("ascii content over rune limit truncated to 4000 runes", func(t *testing.T) {
		content := strings.Repeat("a", 5000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": content,
		})
		got := TruncatedPayload(raw)
		s, ok := got["content"].(string)
		assert.True(t, ok)
		assert.True(t, strings.HasSuffix(s, truncatedContentSuffix))
		head := strings.TrimSuffix(s, truncatedContentSuffix)
		assert.Equal(t, TextContentMaxRunes, utf8.RuneCountInString(head))
	})

	t.Run("chinese content over rune limit truncated to 4000 runes", func(t *testing.T) {
		content := strings.Repeat("你", 5000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": content,
		})
		got := TruncatedPayload(raw)
		s, ok := got["content"].(string)
		assert.True(t, ok)
		assert.True(t, strings.HasSuffix(s, truncatedContentSuffix))
		assert.True(t, utf8.ValidString(s), "must be valid UTF-8")
		head := strings.TrimSuffix(s, truncatedContentSuffix)
		assert.Equal(t, TextContentMaxRunes, utf8.RuneCountInString(head))
	})

	t.Run("text with object content coerced then rune truncated", func(t *testing.T) {
		// Bot 误把 object 塞进 content，先 Coerce 成 string 再按 rune 截。
		big := map[string]interface{}{"data": strings.Repeat("x", 5000)}
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": big,
		})
		got := TruncatedPayload(raw)
		s, ok := got["content"].(string)
		assert.True(t, ok)
		assert.True(t, strings.HasSuffix(s, truncatedContentSuffix))
	})

	t.Run("text preserves visibles and small extension fields", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]interface{}{
			"type":     1,
			"content":  strings.Repeat("a", 5000),
			"visibles": []interface{}{"u1"},
			"mention":  []interface{}{"u1", "u2"},
		})
		got := TruncatedPayload(raw)
		visibles, ok := got["visibles"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, visibles, 1)
		mention, ok := got["mention"].([]interface{})
		assert.True(t, ok, "small extension fields must survive Text truncation")
		assert.Len(t, mention, 2)
	})

	t.Run("text with large extension fields kept since rune-driven", func(t *testing.T) {
		// content 短，rune 数 < 4000，不触发截断。Text 路径不再受 byte 闸门约束，
		// 即使 extension 把整体撑到 50KB 也不应被丢弃。
		raw, _ := json.Marshal(map[string]interface{}{
			"type":      1,
			"content":   "hi",
			"extension": strings.Repeat("x", 50*1024),
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, "hi", got["content"], "short content must not be truncated")
		ext, ok := got["extension"].(string)
		assert.True(t, ok, "Text-type extension must survive when rune count is safe")
		assert.Len(t, ext, 50*1024)
	})

	t.Run("string typed type field falls through to non-Text pass-through", func(t *testing.T) {
		// "1" 字符串不被识别为 Text type，走非 Text 路径——原样下发，不截断。
		content := strings.Repeat("a", 5000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    "1",
			"content": content,
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, content, got["content"], "non-Text falls through unchanged")
	})
}

// TestTruncatedPayload_NonTextType 验证非 Text 全部原样下发，不做任何 content 截断。
// 包括媒体类（Image/Voice/Video/File/Location/Card）、富文本/合并转发、CMD、
// 系统通知（>=1000 群成员/客服/通话结果等）。
func TestTruncatedPayload_NonTextType(t *testing.T) {
	cases := []struct {
		name string
		typ  int
	}{
		{"image", common.Image.Int()},
		{"voice", common.Voice.Int()},
		{"video", common.Video.Int()},
		{"file", common.File.Int()},
		{"location", common.Location.Int()},
		{"card", common.Card.Int()},
		{"multiple_forward", common.MultipleForward.Int()},
		{"rich_text", common.RichText.Int()},
		{"cmd", common.CMD.Int()},
		{"group_create", common.GroupCreate.Int()},
		{"group_member_add", common.GroupMemberAdd.Int()},
		{"revoke_message", common.RevokeMessage.Int()},
		{"hotline_assign", common.HotlineAssignTo.Int()},
		{"tip", common.Tip.Int()},
		{"video_call_result", common.VideoCallResult.Int()},
	}
	for _, tc := range cases {
		t.Run(tc.name+"_object_content_preserved", func(t *testing.T) {
			content := map[string]interface{}{
				"url":  "https://example.com/x",
				"size": 12345,
				"name": strings.Repeat("a", 5000),
			}
			raw, _ := json.Marshal(map[string]interface{}{
				"type":     tc.typ,
				"content":  content,
				"visibles": []interface{}{"u1"},
				"extra":    []interface{}{"e1", "e2"},
			})
			got := TruncatedPayload(raw)
			gotContent, ok := got["content"].(map[string]interface{})
			assert.True(t, ok, "non-Text object content must remain object")
			assert.Equal(t, "https://example.com/x", gotContent["url"])
			assert.Len(t, gotContent["name"], 5000, "long string field inside content must not be sliced")
			extra, ok := got["extra"].([]interface{})
			assert.True(t, ok, "extra fields must survive")
			assert.Len(t, extra, 2)
		})
	}
}

// TestTruncatedPayload_NonTextLargePayload 验证非 Text 即使整体超 LargePayloadThreshold
// 也不截断 content，只在超 1MB 硬上限时占位。
func TestTruncatedPayload_NonTextLargePayload(t *testing.T) {
	t.Run("system message 50KB content preserved", func(t *testing.T) {
		// 群成员变更通知里带几千个 uid 的 extra，原样下发不截。
		uids := make([]interface{}, 5000)
		for i := range uids {
			uids[i] = "uid-padding-string-xxxxxxxxxx"
		}
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    common.GroupMemberAdd.Int(),
			"content": "群成员添加通知",
			"extra":   uids,
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, "群成员添加通知", got["content"])
		extra, ok := got["extra"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, extra, 5000, "system message extra field must survive")
	})

	t.Run("hard limit still applies to all types", func(t *testing.T) {
		// 即使是系统消息，超过 1MB 硬上限仍占位（防御异常 payload）。
		raw := make([]byte, hardParsePayloadLimit+1)
		for i := range raw {
			raw[i] = 'x'
		}
		got := TruncatedPayload(raw)
		assert.Equal(t, common.ContentError.Int(), got["type"])
	})

	t.Run("non-text without content field passes through", func(t *testing.T) {
		// 非 Text 不再走 truncatedFallback 白名单，所有字段原样保留。
		raw, _ := json.Marshal(map[string]interface{}{
			"type":     common.GroupCreate.Int(),
			"creator":  "u1",
			"visibles": []interface{}{"u1", "u2"},
		})
		got := TruncatedPayload(raw)
		assert.Equal(t, "u1", got["creator"], "non-Text fields must survive even without content")
		visibles, ok := got["visibles"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, visibles, 2)
	})
}
