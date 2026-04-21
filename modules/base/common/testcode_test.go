package common

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

func TestIsTestCodeEnabled(t *testing.T) {
	tests := []struct {
		name    string
		mode    config.Mode
		smsCode string
		want    bool
	}{
		{"release + empty", config.ReleaseMode, "", false},
		{"release + set (should be disabled)", config.ReleaseMode, "123456", false},
		{"release + whitespace only", config.ReleaseMode, "   ", false},
		{"debug + empty", config.DebugMode, "", false},
		{"debug + set", config.DebugMode, "123456", true},
		{"bench + set", config.BenchMode, "000000", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tt.mode, SMSCode: tt.smsCode}
			assert.Equal(t, tt.want, IsTestCodeEnabled(cfg))
		})
	}
}

func TestMatchTestCode(t *testing.T) {
	tests := []struct {
		name    string
		mode    config.Mode
		smsCode string
		input   string
		want    bool
	}{
		{"release never matches even if equal", config.ReleaseMode, "123456", "123456", false},
		{"debug match", config.DebugMode, "123456", "123456", true},
		{"debug mismatch", config.DebugMode, "123456", "999999", false},
		{"debug empty config never matches", config.DebugMode, "", "", false},
		{"debug empty input never matches", config.DebugMode, "123456", "", false},
		{"debug whitespace-trimmed match", config.DebugMode, "123456", "  123456  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tt.mode, SMSCode: tt.smsCode}
			assert.Equal(t, tt.want, MatchTestCode(cfg, tt.input))
		})
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"foo@bar.com", "f**@bar.com"},
		{"a@b.com", "*@b.com"},
		{"alice@example.org", "a****@example.org"},
		{"", "***"},
		{"noatsign", "***"},
		{"@nolocal.com", "***"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, maskEmail(tt.in))
		})
	}
}

func TestTestCodeHelpersNilConfig(t *testing.T) {
	assert.False(t, IsTestCodeEnabled(nil))
	assert.False(t, MatchTestCode(nil, "123456"))
	assert.NoError(t, ValidateTestCodeConfig(nil))
}

func TestValidateTestCodeConfig(t *testing.T) {
	tests := []struct {
		name    string
		mode    config.Mode
		smsCode string
		wantErr bool
	}{
		{"release + empty ok", config.ReleaseMode, "", false},
		{"release + whitespace ok", config.ReleaseMode, "   ", false},
		{"release + set rejected", config.ReleaseMode, "123456", true},
		{"debug + set ok", config.DebugMode, "123456", false},
		{"bench + set ok", config.BenchMode, "123456", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tt.mode, SMSCode: tt.smsCode}
			err := ValidateTestCodeConfig(cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
