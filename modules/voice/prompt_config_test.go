package voice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadPrompts_FileNotFound(t *testing.T) {
	t.Cleanup(resetToDefaults)
	LoadPrompts("/nonexistent/path.yaml", nil)
	// Should use defaults
	assert.Equal(t, transcribePrompt, activePrompts.Transcribe)
	assert.Equal(t, modifyPromptTemplate, activePrompts.Modify)
}

func TestLoadPrompts_EmptyPath(t *testing.T) {
	t.Cleanup(resetToDefaults)
	LoadPrompts("", nil)
	assert.Equal(t, transcribePrompt, activePrompts.Transcribe)
}

func TestLoadPrompts_PartialOverride(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	os.WriteFile(path, []byte(`transcribe: "custom transcribe prompt"`), 0644)

	LoadPrompts(path, nil)
	assert.Equal(t, "custom transcribe prompt", activePrompts.Transcribe)
	// Other fields should remain as defaults
	assert.Equal(t, modifyPromptTemplate, activePrompts.Modify)
	assert.Equal(t, chatContextSuffix, activePrompts.ChatContextSuffix)
}

func TestLoadPrompts_FullOverride(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	content := `
transcribe: "custom transcribe"
modify: "custom modify %s"
append_context: "custom append %s"
chat_context_suffix: "custom suffix %s"
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	assert.Equal(t, "custom transcribe", activePrompts.Transcribe)
	assert.Equal(t, "custom modify %s", activePrompts.Modify)
	assert.Equal(t, "custom append %s", activePrompts.AppendContext)
	assert.Equal(t, "custom suffix %s", activePrompts.ChatContextSuffix)
}

func TestLoadPrompts_InvalidYAML(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(`{{{invalid`), 0644)

	LoadPrompts(path, nil)
	// Should fall back to defaults
	assert.Equal(t, transcribePrompt, activePrompts.Transcribe)
}

func TestLoadPrompts_EmptyFields(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	content := `
transcribe: ""
modify: "custom modify %s"
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	// Empty transcribe should keep default
	assert.Equal(t, transcribePrompt, activePrompts.Transcribe)
	// Non-empty modify should override
	assert.Equal(t, "custom modify %s", activePrompts.Modify)
}

func TestLoadPrompts_MultilineBlockScalar(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	content := `transcribe: |
  Line one.
  Line two.
  Line three.
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	assert.Equal(t, "Line one.\nLine two.\nLine three.", activePrompts.Transcribe)
}

func TestLoadPrompts_WhitespaceOnlyField(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	content := `
transcribe: "   "
modify: "custom modify %s"
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	// Whitespace-only transcribe should keep default
	assert.Equal(t, transcribePrompt, activePrompts.Transcribe)
	assert.Equal(t, "custom modify %s", activePrompts.Modify)
}

func TestLoadPrompts_InvalidPlaceholderCount(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")

	// modify with zero %s, append_context with two %s, chat_context_suffix with zero %s
	content := `
modify: "no placeholder here"
append_context: "two %s placeholders %s"
chat_context_suffix: "missing placeholder"
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	// All three should fall back to defaults due to wrong %s count
	assert.Equal(t, modifyPromptTemplate, activePrompts.Modify)
	assert.Equal(t, appendContextHint, activePrompts.AppendContext)
	assert.Equal(t, chatContextSuffix, activePrompts.ChatContextSuffix)
}

func TestLoadPrompts_ValidPlaceholder(t *testing.T) {
	t.Cleanup(resetToDefaults)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	content := `
modify: "edit this: %s done"
append_context: "context: %s end"
chat_context_suffix: "vocab %s list"
`
	os.WriteFile(path, []byte(content), 0644)

	LoadPrompts(path, nil)
	assert.Equal(t, "edit this: %s done", activePrompts.Modify)
	assert.Equal(t, "context: %s end", activePrompts.AppendContext)
	assert.Equal(t, "vocab %s list", activePrompts.ChatContextSuffix)
}
