package voice

import (
	"fmt"
	"strings"
)

const noSpeechSentinel = "[NO_SPEECH]"

const transcribePrompt = `# 角色
你是语音转写器。

# 任务
将音频中的人类语音转为文字。

# 规则（按优先级排序）

## P0 - 无语音判定
音频无清晰人类语音（静音、噪声、呼吸声、电流声、敲击声、极短音频）→ 只输出：
[NO_SPEECH]

## P1 - 禁止猜测
禁止根据上下文、常识、语义推断生成内容。只转写实际听到的语音。

## P2 - 语言润色
- 去除句中无意义的语气填充词（呃、嗯、啊、那个、就是说），保持语句流畅
- 但保留句首/句尾表达情绪或语气的词（如"呃 好吧"、"啊 好的"中的语气词保留）
- 将口语化表达轻度书面化（不改变原意，只调整措辞使其更通顺）
- 自动添加标点，修正明显口误和重复

## P3 - 输出格式
只输出两种结果之一：
- [NO_SPEECH]
- 纯转写文本（无解释、无前缀、无后缀）`

const modifyPromptTemplate = `# 角色
你是语音转写器和文本编辑器。

# 已有文本
---
%s
---

# 规则（按优先级排序）

## P0 - 无语音判定
音频无清晰人类语音（静音、噪声、呼吸声、电流声、极短音频）→ 原样输出已有文本，不做任何修改。

## P1 - 禁止猜测
禁止根据上下文、常识猜测或生成内容。

## P2 - 编辑指令识别
听到编辑指令时，对已有文本执行操作：
- 替换类：改成、替换、修改为、换成、不是X是Y
- 删除类：删掉、删除、去掉、移除
- 插入类：加上、添加、插入、后面加、前面加
- 调整类：提前、推迟、放到前面、移到后面

## P3 - 追加新内容
如果语音不包含编辑指令，将转写内容追加到已有文本末尾。

## P4 - 语言润色
- 去除句中无意义的语气填充词（呃、嗯、啊、那个、就是说），保留句首/句尾表达语气的
- 口语化表达轻度书面化（不改变原意）
- 自动添加标点，修正口误和重复

## P5 - 输出格式
输出操作后的完整文本，不加任何解释。`

const chatContextSuffix = `⚠️ 重要警告：下方的词汇表仅用于纠正你听到的语音中的专有名词拼写。如果音频是静音或噪音，这些文字与你的输出无关，你必须输出 [NO_SPEECH]。绝对不要把这些文字当作你"听到"的内容。

[词汇参考表 - 仅纠错用]
%s`

// appendContextPromptTemplate — append mode: contextText as context hint + transcribePrompt
const appendContextPromptTemplate = `# 已有文本（仅用于辅助理解语境和专有名词纠错，不得作为转写内容来源）
---
%s
---

` + transcribePrompt

// buildPrompt returns the appropriate prompt based on whether context text and chat context are provided.
// Used by edit mode (Gemini).
func buildPrompt(contextText string, chatContext string) string {
	var prompt string
	if contextText == "" {
		prompt = transcribePrompt
	} else {
		prompt = fmt.Sprintf(modifyPromptTemplate, contextText)
	}
	if chatContext != "" {
		prompt = prompt + "\n\n" + fmt.Sprintf(chatContextSuffix, chatContext)
	}
	return prompt
}

// buildAppendPrompt builds the prompt for append mode.
// contextText serves as context hint, chatContext for vocabulary correction.
func buildAppendPrompt(contextText string, chatContext string) string {
	var prompt string
	if contextText == "" {
		prompt = transcribePrompt
	} else {
		prompt = fmt.Sprintf(appendContextPromptTemplate, contextText)
	}
	if chatContext != "" {
		prompt = prompt + "\n\n" + fmt.Sprintf(chatContextSuffix, chatContext)
	}
	return prompt
}

// IsNoSpeech checks if the model output indicates no speech was detected.
func IsNoSpeech(text string) bool {
	if text == "" {
		return true
	}
	trimmed := strings.TrimSpace(text)
	return strings.Contains(trimmed, noSpeechSentinel)
}
