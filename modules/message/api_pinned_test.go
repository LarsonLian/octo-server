package message

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParsePayloadType tests safe type assertion for payload type field
func TestParsePayloadType(t *testing.T) {
	tests := []struct {
		name         string
		jsonPayload  string
		useNumber    bool // whether to use json.Number mode
		expectedType int
	}{
		{
			name:         "type as json.Number (normal case)",
			jsonPayload:  `{"type": 1, "content": "hello"}`,
			useNumber:    true,
			expectedType: 1,
		},
		{
			name:         "type as float64 (standard json.Unmarshal)",
			jsonPayload:  `{"type": 1, "content": "hello"}`,
			useNumber:    false,
			expectedType: 1,
		},
		{
			name:         "type is nil",
			jsonPayload:  `{"content": "hello"}`,
			useNumber:    true,
			expectedType: 0,
		},
		{
			name:         "type is string (should not panic)",
			jsonPayload:  `{"type": "invalid", "content": "hello"}`,
			useNumber:    true,
			expectedType: 0,
		},
		{
			name:         "type is boolean (should not panic)",
			jsonPayload:  `{"type": true, "content": "hello"}`,
			useNumber:    true,
			expectedType: 0,
		},
		{
			name:         "type is array (should not panic)",
			jsonPayload:  `{"type": [1,2,3], "content": "hello"}`,
			useNumber:    true,
			expectedType: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payloadMap map[string]interface{}
			if tt.useNumber {
				decoder := json.NewDecoder(strings.NewReader(tt.jsonPayload))
				decoder.UseNumber()
				err := decoder.Decode(&payloadMap)
				assert.NoError(t, err)
			} else {
				err := json.Unmarshal([]byte(tt.jsonPayload), &payloadMap)
				assert.NoError(t, err)
			}

			// This should not panic with any input type
			contentType := parsePayloadType(payloadMap)
			assert.Equal(t, tt.expectedType, contentType)
		})
	}
}

// TestParsePayloadContent tests safe type assertion for payload content field
func TestParsePayloadContent(t *testing.T) {
	tests := []struct {
		name            string
		jsonPayload     string
		expectedContent string
		expectFallback  bool
	}{
		{
			name:            "content is string",
			jsonPayload:     `{"type": 1, "content": "hello world"}`,
			expectedContent: "`hello world`",
			expectFallback:  false,
		},
		{
			name:            "content is missing",
			jsonPayload:     `{"type": 1}`,
			expectedContent: "",
			expectFallback:  true,
		},
		{
			name:            "content is number (should not panic)",
			jsonPayload:     `{"type": 1, "content": 123}`,
			expectedContent: "",
			expectFallback:  true,
		},
		{
			name:            "content is object (should not panic)",
			jsonPayload:     `{"type": 1, "content": {"key": "value"}}`,
			expectedContent: "",
			expectFallback:  true,
		},
		{
			name:            "content is null (should not panic)",
			jsonPayload:     `{"type": 1, "content": null}`,
			expectedContent: "",
			expectFallback:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payloadMap map[string]interface{}
			decoder := json.NewDecoder(strings.NewReader(tt.jsonPayload))
			decoder.UseNumber()
			err := decoder.Decode(&payloadMap)
			assert.NoError(t, err)

			// This should not panic with any input type
			content, ok := parsePayloadContent(payloadMap)
			if tt.expectFallback {
				assert.False(t, ok)
			} else {
				assert.True(t, ok)
				assert.Equal(t, tt.expectedContent, content)
			}
		})
	}
}

// parsePayloadType safely extracts type from payload map
func parsePayloadType(payloadMap map[string]interface{}) int {
	if payloadMap["type"] == nil {
		return 0
	}
	switch v := payloadMap["type"].(type) {
	case json.Number:
		contentTypeI, _ := v.Int64()
		return int(contentTypeI)
	case float64:
		return int(v)
	}
	return 0
}

// parsePayloadContent safely extracts content from payload map
func parsePayloadContent(payloadMap map[string]interface{}) (string, bool) {
	if contentStr, ok := payloadMap["content"].(string); ok {
		return fmt.Sprintf("`%s`", contentStr), true
	}
	return "", false
}
