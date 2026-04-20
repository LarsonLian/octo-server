package util

import "testing"

func TestIsHexString_Valid(t *testing.T) {
	if !IsHexString("afd1a8d99bb94bf0a8d2c1e3f4a5b6c7") {
		t.Error("expected true for valid 32-char lowercase hex string")
	}
}

func TestIsHexString_ShortValid(t *testing.T) {
	if !IsHexString("0123456789abcdef") {
		t.Error("expected true for valid hex string")
	}
}

func TestIsHexString_Empty(t *testing.T) {
	if IsHexString("") {
		t.Error("expected false for empty string")
	}
}

func TestIsHexString_UpperCase(t *testing.T) {
	if !IsHexString("AFD1A8D99BB94BF0A8D2C1E3F4A5B6C7") {
		t.Error("expected true for uppercase hex string")
	}
}

func TestIsHexString_NonHex(t *testing.T) {
	if IsHexString("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz") {
		t.Error("expected false for non-hex characters")
	}
}

func TestIsHexString_WithDash(t *testing.T) {
	if IsHexString("afd1a8d9-9bb9-4bf0-a8d2-c1e3f4a5b6c7") {
		t.Error("expected false for string with dashes")
	}
}

func TestIsHexString_MixedCase(t *testing.T) {
	if !IsHexString("afd1A8d99bb94bf0a8d2c1e3f4a5b6c7") {
		t.Error("expected true for mixed case hex string")
	}
}

func TestIsHexString_SingleChar(t *testing.T) {
	if !IsHexString("a") {
		t.Error("expected true for single hex char")
	}
	if IsHexString("g") {
		t.Error("expected false for non-hex single char")
	}
}
