package util

import (
	"net/url"
	"strings"
)

// ExtractFilenameFromPath extracts the original filename from a storage path.
// It handles two path formats:
//   - Legacy: "chat/timestamp/uuid_filename" → strips 32-char hex UUID prefix + underscore
//   - New:    "chat/timestamp/uuid/filename" → returns the last path segment
//
// Percent-encoded filenames are decoded. If the extracted filename is empty
// (e.g. legacy path ending in "uuid_"), the full last segment is returned as fallback.
//
// Callers should pass raw (not pre-decoded) paths. If the path is already decoded,
// the PathUnescape is a no-op for most filenames, but filenames containing literal
// percent-encoded sequences may be double-decoded.
func ExtractFilenameFromPath(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	lastPart := parts[len(parts)-1]

	// Only attempt legacy UUID prefix stripping for 3-segment paths (legacy format: chat/ts/uuid_filename).
	// New format (chat/ts/uuid/filename) has 4+ segments, so lastPart IS the filename.
	if len(parts) <= 3 {
		if idx := strings.Index(lastPart, "_"); idx == 32 && IsHexString(lastPart[:32]) {
			after := lastPart[idx+1:]
			if after != "" {
				unescaped, err := url.PathUnescape(after)
				if err == nil {
					return unescaped
				}
				return after
			}
			// Empty filename after UUID prefix — fall back to full last segment
		}
	}

	unescaped, err := url.PathUnescape(lastPart)
	if err == nil {
		return unescaped
	}
	return lastPart
}
