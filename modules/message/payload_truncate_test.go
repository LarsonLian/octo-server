package message

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
)

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"ascii truncated", "hello world", 5, "hello"},
		{"empty string", "", 10, ""},
		{"zero limit", "abc", 0, ""},
		{
			// 中文每字符 3 字节，限制在字符中间应回退到合法边界
			name:     "utf8 boundary fallback",
			input:    "你好世界",
			maxBytes: 7, // 2 个汉字 = 6 字节，第 7 字节落在第 3 个汉字中间
			want:     "你好",
		},
		{"utf8 aligned", "你好", 6, "你好"},
		{"all multibyte truncated to zero", "你好", 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUTF8(tc.input, tc.maxBytes)
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

	t.Run("string content truncated and type preserved", func(t *testing.T) {
		big := strings.Repeat("a", 5000)
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": big,
		})
		got := TruncatedPayload(raw)
		s := got["content"].(string)
		assert.True(t, strings.HasSuffix(s, truncatedContentSuffix))
		assert.Equal(t, truncatedContentHeadBytes+len(truncatedContentSuffix), len(s))
		// type 保留（json.Number 反序列化）
		assert.NotNil(t, got["type"])
	})

	t.Run("object content coerced and truncated", func(t *testing.T) {
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

	t.Run("no content field keeps only type and visibles", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]interface{}{
			"type":       7,
			"visibles":   []interface{}{"u1", "u2"},
			"big_field":  strings.Repeat("x", 20000),
			"other_key":  "dropped",
		})
		got := TruncatedPayload(raw)
		assert.NotNil(t, got["type"])
		visibles, ok := got["visibles"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, visibles, 2)
		// 未知大字段必须丢弃
		_, hasBig := got["big_field"]
		assert.False(t, hasBig, "oversized unknown fields must be dropped")
		_, hasOther := got["other_key"]
		assert.False(t, hasOther)
	})

	t.Run("visibles preserved on normal truncation", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]interface{}{
			"type":     1,
			"content":  strings.Repeat("a", 5000),
			"visibles": []interface{}{"u1"},
		})
		got := TruncatedPayload(raw)
		visibles, ok := got["visibles"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, visibles, 1)
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

	t.Run("oversized custom fields dropped when content truncated", func(t *testing.T) {
		// content 很短，但自定义扩展字段 300KB。截断后终检应发现整体仍超限，
		// 回退到白名单（type + visibles + content），丢弃 extension。
		raw, _ := json.Marshal(map[string]interface{}{
			"type":      1,
			"content":   "hi",
			"visibles":  []interface{}{"u1"},
			"extension": strings.Repeat("x", 300*1024),
		})
		got := TruncatedPayload(raw)
		_, hasExt := got["extension"]
		assert.False(t, hasExt, "oversized extension field must be dropped")
		assert.NotNil(t, got["type"])
		_, hasVisibles := got["visibles"]
		assert.True(t, hasVisibles)
		assert.Contains(t, got["content"], truncatedContentSuffix)
	})

	t.Run("small custom fields kept when overall size is safe", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": strings.Repeat("a", 5000),
			"mention": []interface{}{"u1", "u2"},
		})
		got := TruncatedPayload(raw)
		mention, ok := got["mention"].([]interface{})
		assert.True(t, ok, "small extension fields should survive")
		assert.Len(t, mention, 2)
	})

	t.Run("nil input falls back to placeholder", func(t *testing.T) {
		got := TruncatedPayload(nil)
		assert.Equal(t, errType, got["type"])
		assert.Equal(t, truncatedContentSuffix, got["content"])
	})

	t.Run("string typed type field does not trigger text coercion", func(t *testing.T) {
		// CoerceTextPayloadContent 应忽略 string 类型的 "1"，避免误命中。
		m := map[string]interface{}{
			"type":    "1",
			"content": map[string]interface{}{"k": "v"},
		}
		CoerceTextPayloadContent(m)
		_, stillMap := m["content"].(map[string]interface{})
		assert.True(t, stillMap, "string-typed type field must not trigger coercion")
	})

	t.Run("chinese content truncation is valid utf8", func(t *testing.T) {
		big := strings.Repeat("你好", 1000) // 6000 bytes
		raw, _ := json.Marshal(map[string]interface{}{
			"type":    1,
			"content": big,
		})
		got := TruncatedPayload(raw)
		s := got["content"].(string)
		assert.True(t, utf8.ValidString(s), "must be valid UTF-8")
		assert.True(t, strings.HasSuffix(s, truncatedContentSuffix))
	})
}
