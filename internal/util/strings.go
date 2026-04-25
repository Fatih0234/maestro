// Package util provides shared utility functions used across the project.
package util

import "strings"

// SanitizeBranchName sanitizes an issue ID for use in a git branch name.
// Only allows [a-zA-Z0-9_/-], replacing all other characters with hyphens.
func SanitizeBranchName(id string) string {
	var result strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '/' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}
	return result.String()
}

// SanitizeFileName sanitizes a string for use in a file or directory name.
// Only allows [a-zA-Z0-9_.-], replacing all other characters with hyphens.
func SanitizeFileName(name string) string {
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}
	return result.String()
}
