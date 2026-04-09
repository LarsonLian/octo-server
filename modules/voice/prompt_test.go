package voice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPrompt_NoContext(t *testing.T) {
	prompt := buildPrompt("", "")
	assert.Equal(t, transcribePrompt, prompt)
	assert.Contains(t, prompt, "准确还原说话内容")
	assert.NotContains(t, prompt, "已有以下文本")
}

func TestBuildPrompt_WithContext(t *testing.T) {
	contextText := "Hello, this is existing text."
	prompt := buildPrompt(contextText, "")

	assert.Contains(t, prompt, "已有以下文本")
	assert.Contains(t, prompt, contextText)
	assert.Contains(t, prompt, "编辑指令")
	assert.NotEqual(t, transcribePrompt, prompt)
}

func TestBuildPrompt_ContextTextEmbedded(t *testing.T) {
	contextText := "Line 1\nLine 2\nLine 3"
	prompt := buildPrompt(contextText, "")

	// Context text should appear between the --- delimiters
	parts := strings.Split(prompt, "---")
	assert.True(t, len(parts) >= 3, "prompt should contain --- delimiters")
	assert.Contains(t, parts[1], contextText)
}

func TestBuildPrompt_WithChatContext_TranscribeMode(t *testing.T) {
	chatCtx := "Alice: 你好\nBob: 你好啊"
	prompt := buildPrompt("", chatCtx)

	assert.Contains(t, prompt, "辅助识别专有名词拼写")
	assert.Contains(t, prompt, chatCtx)
	assert.Contains(t, prompt, "准确还原说话内容")
	assert.NotContains(t, prompt, "已有以下文本")
}

func TestBuildPrompt_WithChatContext_ModifyMode(t *testing.T) {
	chatCtx := "Alice: 会议在周五\nBob: 收到"
	contextText := "existing text"
	prompt := buildPrompt(contextText, chatCtx)

	assert.Contains(t, prompt, "辅助识别专有名词拼写")
	assert.Contains(t, prompt, chatCtx)
	assert.Contains(t, prompt, "已有以下文本")
	assert.Contains(t, prompt, contextText)
	assert.Contains(t, prompt, "编辑指令")

	// Chat context should appear before the main prompt
	chatCtxIdx := strings.Index(prompt, chatCtx)
	mainPromptIdx := strings.Index(prompt, "已有以下文本")
	assert.True(t, chatCtxIdx < mainPromptIdx, "chat context should precede the main prompt")
}

func TestBuildPrompt_EmptyChatContext(t *testing.T) {
	prompt := buildPrompt("", "")
	assert.NotContains(t, prompt, "辅助识别专有名词拼写")
	assert.Equal(t, transcribePrompt, prompt)
}

// --- buildAppendPrompt tests ---

func TestBuildAppendPrompt_NoContext(t *testing.T) {
	prompt := buildAppendPrompt("", "")
	assert.Equal(t, transcribePrompt, prompt)
}

func TestBuildAppendPrompt_WithContextText(t *testing.T) {
	prompt := buildAppendPrompt("已有的文本内容", "")
	assert.Contains(t, prompt, "已有的文本内容")
	assert.Contains(t, prompt, "辅助理解语境和专有名词纠错")
	assert.Contains(t, prompt, "准确还原说话内容") // transcribePrompt is appended
	assert.NotContains(t, prompt, "编辑指令")       // no edit instructions
}

func TestBuildAppendPrompt_WithChatContext(t *testing.T) {
	prompt := buildAppendPrompt("", "Alice: 聊天内容")
	assert.Contains(t, prompt, "辅助识别专有名词拼写")
	assert.Contains(t, prompt, "Alice: 聊天内容")
	assert.Contains(t, prompt, "准确还原说话内容")
}

func TestBuildAppendPrompt_WithBothContexts(t *testing.T) {
	prompt := buildAppendPrompt("原有文本", "Alice: 聊天")

	assert.Contains(t, prompt, "原有文本")
	assert.Contains(t, prompt, "辅助理解语境和专有名词纠错")
	assert.Contains(t, prompt, "Alice: 聊天")
	assert.Contains(t, prompt, "辅助识别专有名词拼写")

	// Chat context should precede the append prompt
	chatIdx := strings.Index(prompt, "Alice: 聊天")
	appendIdx := strings.Index(prompt, "辅助理解语境和专有名词纠错")
	assert.True(t, chatIdx < appendIdx, "chat context should precede append prompt")
}

func TestBuildAppendPrompt_DoesNotContainEditInstructions(t *testing.T) {
	prompt := buildAppendPrompt("some text", "")
	assert.NotContains(t, prompt, "编辑指令")
	assert.NotContains(t, prompt, "删掉")
	assert.NotContains(t, prompt, "改成")
}
