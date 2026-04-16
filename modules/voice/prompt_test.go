package voice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPrompt_NoContext(t *testing.T) {
	prompt := buildPrompt("", "")
	assert.Equal(t, transcribePrompt, prompt)
	assert.Contains(t, prompt, "将音频中的人类语音转为文字")
	assert.NotContains(t, prompt, "# 已有文本")
}

func TestBuildPrompt_WithContext(t *testing.T) {
	contextText := "Hello, this is existing text."
	prompt := buildPrompt(contextText, "")

	assert.Contains(t, prompt, "# 已有文本")
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

	assert.Contains(t, prompt, "词汇参考表")
	assert.Contains(t, prompt, chatCtx)
	assert.Contains(t, prompt, "将音频中的人类语音转为文字")
	assert.NotContains(t, prompt, "# 已有文本")
}

func TestBuildPrompt_WithChatContext_ModifyMode(t *testing.T) {
	chatCtx := "Alice: 会议在周五\nBob: 收到"
	contextText := "existing text"
	prompt := buildPrompt(contextText, chatCtx)

	assert.Contains(t, prompt, "词汇参考表")
	assert.Contains(t, prompt, chatCtx)
	assert.Contains(t, prompt, "# 已有文本")
	assert.Contains(t, prompt, contextText)
	assert.Contains(t, prompt, "编辑指令")

	// Chat context should appear AFTER the main prompt
	chatCtxIdx := strings.Index(prompt, chatCtx)
	mainPromptIdx := strings.Index(prompt, "# 已有文本")
	assert.True(t, chatCtxIdx > mainPromptIdx, "chat context should follow the main prompt")
}

func TestBuildPrompt_EmptyChatContext(t *testing.T) {
	prompt := buildPrompt("", "")
	assert.NotContains(t, prompt, "词汇参考表")
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
	assert.Contains(t, prompt, "将音频中的人类语音转为文字") // transcribePrompt is appended
	assert.NotContains(t, prompt, "编辑指令")               // no edit instructions
}

func TestBuildAppendPrompt_WithChatContext(t *testing.T) {
	prompt := buildAppendPrompt("", "Alice: 聊天内容")
	assert.Contains(t, prompt, "词汇参考表")
	assert.Contains(t, prompt, "Alice: 聊天内容")
	assert.Contains(t, prompt, "将音频中的人类语音转为文字")
}

func TestBuildAppendPrompt_WithBothContexts(t *testing.T) {
	prompt := buildAppendPrompt("原有文本", "Alice: 聊天")

	assert.Contains(t, prompt, "原有文本")
	assert.Contains(t, prompt, "辅助理解语境和专有名词纠错")
	assert.Contains(t, prompt, "Alice: 聊天")
	assert.Contains(t, prompt, "词汇参考表")

	// Chat context should follow the append prompt
	chatIdx := strings.Index(prompt, "Alice: 聊天")
	appendIdx := strings.Index(prompt, "辅助理解语境和专有名词纠错")
	assert.True(t, chatIdx > appendIdx, "chat context should follow append prompt")
}

func TestBuildAppendPrompt_DoesNotContainEditInstructions(t *testing.T) {
	prompt := buildAppendPrompt("some text", "")
	assert.NotContains(t, prompt, "编辑指令")
	assert.NotContains(t, prompt, "删掉")
	assert.NotContains(t, prompt, "改成")
}

// --- New tests for chat context position fix ---

func TestBuildPrompt_ChatContextPosition(t *testing.T) {
	chatCtx := "Alice: 测试内容"
	prompt := buildPrompt("", chatCtx)

	// chatContext must appear AFTER transcribePrompt
	transcribeIdx := strings.Index(prompt, "你是语音转写器")
	chatCtxIdx := strings.Index(prompt, chatCtx)
	assert.True(t, chatCtxIdx > transcribeIdx, "chatContext should appear after transcribePrompt")
}

func TestBuildAppendPrompt_ChatContextPosition(t *testing.T) {
	chatCtx := "Bob: 测试聊天"
	prompt := buildAppendPrompt("", chatCtx)

	// chatContext must appear AFTER transcribePrompt
	transcribeIdx := strings.Index(prompt, "你是语音转写器")
	chatCtxIdx := strings.Index(prompt, chatCtx)
	assert.True(t, chatCtxIdx > transcribeIdx, "chatContext should appear after transcribePrompt in append mode")
}

func TestBuildAppendPrompt_ChatContextAfterTranscribePrompt(t *testing.T) {
	contextText := "已有的输入内容"
	chatCtx := "Alice: 专有名词XYZ"
	prompt := buildAppendPrompt(contextText, chatCtx)

	// The appendContextPromptTemplate embeds transcribePrompt which starts with "你是语音转写器"
	transcribeIdx := strings.Index(prompt, "你是语音转写器")
	assert.True(t, transcribeIdx >= 0, "prompt should contain the transcribe portion")

	chatCtxIdx := strings.Index(prompt, chatCtx)
	assert.True(t, chatCtxIdx > transcribeIdx, "chatContext should appear after the transcribe prompt portion")

	// Also verify the warning marker appears between transcribe rules and chat context
	warningIdx := strings.Index(prompt, "⚠️ 重要警告")
	assert.True(t, warningIdx > transcribeIdx, "warning should appear after transcribe rules")
	assert.True(t, chatCtxIdx > warningIdx, "chatContext should appear after warning")
}

func TestBuildPrompt_NoChatContext(t *testing.T) {
	prompt := buildPrompt("some text", "")

	assert.NotContains(t, prompt, "词汇参考表")
	assert.NotContains(t, prompt, "⚠️")
	assert.Contains(t, prompt, "# 已有文本")
}

func TestBuildPrompt_WithContextText_AndChatContext(t *testing.T) {
	contextText := "existing draft"
	chatCtx := "Alice: 专有名词ABC"
	prompt := buildPrompt(contextText, chatCtx)

	// Both contextText and chatContext present
	assert.Contains(t, prompt, contextText)
	assert.Contains(t, prompt, chatCtx)
	assert.Contains(t, prompt, "# 已有文本")
	assert.Contains(t, prompt, "词汇参考表")

	// chatContext must be at the end, after everything else
	contextTextIdx := strings.Index(prompt, contextText)
	chatCtxIdx := strings.Index(prompt, chatCtx)
	assert.True(t, chatCtxIdx > contextTextIdx, "chatContext should appear after contextText")

	editIdx := strings.Index(prompt, "编辑指令")
	assert.True(t, chatCtxIdx > editIdx, "chatContext should appear after edit instructions")
}

func TestChatContextSuffix_ContainsWarning(t *testing.T) {
	assert.Contains(t, chatContextSuffix, "⚠️ 重要警告")
	assert.Contains(t, chatContextSuffix, "[NO_SPEECH]")
	assert.Contains(t, chatContextSuffix, "词汇参考表")
	assert.Contains(t, chatContextSuffix, "绝对不要把这些文字当作你")
}
