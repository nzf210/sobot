package utils

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SanitizePath resolves raw path relative to base and ensures the resolved path
// stays within base. Returns the resolved clean path or an error if path traversal is detected.
func SanitizePath(raw string, base string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("cannot resolve base directory: %w", err)
	}

	resolved := filepath.Join(absBase, raw)
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
	}

	cleanBase := filepath.Clean(absBase)
	cleanResolved := filepath.Clean(absResolved)

	// Ensure cleanResolved is inside cleanBase.
	// Since cleanBase is clean, we can check if cleanResolved has cleanBase as prefix.
	// We append a path separator to ensure we match whole directory components.
	sep := string(filepath.Separator)
	prefix := cleanBase
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}

	if cleanResolved != cleanBase && !strings.HasPrefix(cleanResolved, prefix) {
		return "", fmt.Errorf("path traversal blocked: %q escapes %q", raw, base)
	}

	return cleanResolved, nil
}
