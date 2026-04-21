package util

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestExtractFilenameFromPath_LegacyUUIDPrefix(t *testing.T) {
	path := "chat/1713360000/afd1a8d99bb94bf0a8d2c1e3f4a5b6c7_report.xlsx"
	got := ExtractFilenameFromPath(path)
	if got != "report.xlsx" {
		t.Errorf("expected %q, got %q", "report.xlsx", got)
	}
}

func TestExtractFilenameFromPath_LegacyEncodedFilename(t *testing.T) {
	path := "chat/1713360000/afd1a8d99bb94bf0a8d2c1e3f4a5b6c7_" + url.PathEscape("报告.xlsx")
	got := ExtractFilenameFromPath(path)
	if got != "报告.xlsx" {
		t.Errorf("expected %q, got %q", "报告.xlsx", got)
	}
}

func TestExtractFilenameFromPath_LegacyUppercaseHex(t *testing.T) {
	path := "chat/1713360000/AFD1A8D99BB94BF0A8D2C1E3F4A5B6C7_report.xlsx"
	got := ExtractFilenameFromPath(path)
	if got != "report.xlsx" {
		t.Errorf("expected %q, got %q", "report.xlsx", got)
	}
}

func TestExtractFilenameFromPath_LegacyMixedCaseHex(t *testing.T) {
	path := "chat/1713360000/Afd1a8D99Bb94bf0a8d2C1e3f4A5b6c7_report.xlsx"
	got := ExtractFilenameFromPath(path)
	if got != "report.xlsx" {
		t.Errorf("expected %q, got %q", "report.xlsx", got)
	}
}

func TestExtractFilenameFromPath_EmptyFilenameAfterUUID(t *testing.T) {
	// Legacy path "uuid_" with nothing after underscore should fall back to full last segment
	path := "chat/1713360000/afd1a8d99bb94bf0a8d2c1e3f4a5b6c7_"
	got := ExtractFilenameFromPath(path)
	expected := "afd1a8d99bb94bf0a8d2c1e3f4a5b6c7_"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestExtractFilenameFromPath_NoFalsePositive(t *testing.T) {
	path := "chat/1713360000/my_very_long_filename_with_underscores.xlsx"
	got := ExtractFilenameFromPath(path)
	if got != "my_very_long_filename_with_underscores.xlsx" {
		t.Errorf("expected %q, got %q", "my_very_long_filename_with_underscores.xlsx", got)
	}
}

func TestExtractFilenameFromPath_NewPathFormat(t *testing.T) {
	uuid := GenerUUID()
	path := fmt.Sprintf("chat/%d/%s/file.xlsx", time.Now().Unix(), uuid)
	got := ExtractFilenameFromPath(path)
	if got != "file.xlsx" {
		t.Errorf("expected %q, got %q", "file.xlsx", got)
	}
}

func TestExtractFilenameFromPath_NewPathEncoded(t *testing.T) {
	uuid := GenerUUID()
	filename := "my report (final).xlsx"
	path := fmt.Sprintf("chat/%d/%s/%s", time.Now().Unix(), uuid, url.PathEscape(filename))
	got := ExtractFilenameFromPath(path)
	if got != filename {
		t.Errorf("expected %q, got %q", filename, got)
	}
}

func TestExtractFilenameFromPath_UnicodeFilename(t *testing.T) {
	uuid := GenerUUID()
	filename := "日本語ファイル名.pdf"
	path := fmt.Sprintf("chat/%d/%s/%s", time.Now().Unix(), uuid, url.PathEscape(filename))
	got := ExtractFilenameFromPath(path)
	if got != filename {
		t.Errorf("expected %q, got %q", filename, got)
	}
}

func TestExtractFilenameFromPath_NewPathHexStartingFilename(t *testing.T) {
	// New-format path (4 segments) where filename starts with 32 hex chars + underscore
	// should NOT be treated as legacy UUID prefix
	path := "chat/1234/realuuid/aabbccdd11223344aabbccdd11223344_notes.txt"
	got := ExtractFilenameFromPath(path)
	expected := "aabbccdd11223344aabbccdd11223344_notes.txt"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestExtractFilenameFromPath_SimpleFilename(t *testing.T) {
	got := ExtractFilenameFromPath("report.xlsx")
	if got != "report.xlsx" {
		t.Errorf("expected %q, got %q", "report.xlsx", got)
	}
}

func TestExtractFilenameFromPath_EmptyPath(t *testing.T) {
	got := ExtractFilenameFromPath("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
