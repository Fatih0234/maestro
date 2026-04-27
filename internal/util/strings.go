// Package util provides shared utility functions used across the project.
package util

import (
	"strings"

	"github.com/fatihkarahan/maestro/internal/types"
)

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

// ExpandPrompt substitutes issue placeholders in a prompt template.
// Supported placeholders: {{ issue.id }}, {{ issue.identifier }},
// {{ issue.title }}, {{ issue.description }}, {{ issue.labels }}.
// If template is empty, returns issue.Description as the prompt.
func ExpandPrompt(template string, issue types.Issue) string {
	if template == "" {
		return issue.Description
	}
	template = strings.ReplaceAll(template, "{{ issue.id }}", issue.ID)
	template = strings.ReplaceAll(template, "{{ issue.identifier }}", issue.Identifier)
	template = strings.ReplaceAll(template, "{{ issue.title }}", issue.Title)
	template = strings.ReplaceAll(template, "{{ issue.description }}", issue.Description)
	labels := ""
	if len(issue.Labels) > 0 {
		labels = strings.Join(issue.Labels, ", ")
	}
	template = strings.ReplaceAll(template, "{{ issue.labels }}", labels)
	return strings.TrimSpace(template)
}
