// Package config provides parsing for WORKFLOW.md files with YAML front matter.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v2"
)

// Config holds all configuration for the Contrabass orchestrator.
type Config struct {
	// Concurrency settings
	MaxConcurrency    int `yaml:"max_concurrency"`    // Max concurrent agents (default: 3)
	PollIntervalMs    int `yaml:"poll_interval_ms"`   // Poll interval in milliseconds (default: 30000)
	MaxRetryBackoffMs int `yaml:"max_retry_backoff_ms"` // Max backoff in ms (default: 240000)

	// Tracker settings
	Tracker TrackerConfig `yaml:"tracker"`

	// Agent settings
	Agent AgentConfig `yaml:"agent"`

	// Agent-specific settings (varies by agent type)
	OpenCode *OpenCodeConfig `yaml:"opencode,omitempty"`
	Codex    *CodexConfig    `yaml:"codex,omitempty"`

	// Workspace settings
	Workspace WorkspaceConfig `yaml:"workspace"`

	// Timeouts
	AgentTimeoutMs int `yaml:"agent_timeout_ms"` // Agent timeout in ms (default: 900000 = 15 min)
	StallTimeoutMs int `yaml:"stall_timeout_ms"`  // Stall detection in ms (default: 60000 = 1 min)

	// Raw content (the non-YAML part after ---)
	Content string
}

// TrackerConfig holds tracker-specific settings.
type TrackerConfig struct {
	Type        string `yaml:"type"`        // Tracker type (internal, github, linear)
	BoardDir    string `yaml:"board_dir"`    // Local board directory
	IssuePrefix string `yaml:"issue_prefix"` // Issue ID prefix (e.g., CB)
}

// AgentConfig holds agent-specific settings.
type AgentConfig struct {
	Type string `yaml:"type"` // Agent type (opencode, codex, omx, omc)
}

// OpenCodeConfig holds OpenCode-specific settings.
type OpenCodeConfig struct {
	BinaryPath string `yaml:"binary_path"` // Path to opencode binary (default: opencode serve)
	Port       int    `yaml:"port"`        // Server port (0 = auto)
	Password   string `yaml:"password"`    // Server password (optional)
	Model      string `yaml:"model"`       // Model in format "provider/model" (e.g., "minimax-coding-plan/MiniMax-M2.7")
	// Note: opencode serve inherits the model from the user's default profile
	// (~/.config/opencode/profiles/auto/opencode.jsonc). The Model field here
	// documents the intended model but requires the user to have it configured.
}

// CodexConfig holds Codex-specific settings.
type CodexConfig struct {
	BinaryPath     string `yaml:"binary_path"`     // Path to codex binary
	ApprovalPolicy string `yaml:"approval_policy"` // auto-edit, ask, never-edit
	Sandbox        string `yaml:"sandbox"`         // none, docker, vm
}

// WorkspaceConfig holds workspace-specific settings.
type WorkspaceConfig struct {
	BaseDir      string `yaml:"base_dir"`      // Base directory for workspaces (default: .)
	BranchPrefix string `yaml:"branch_prefix"` // Branch name prefix (default: opencode/)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MaxConcurrency:    3,
		PollIntervalMs:    30000,
		MaxRetryBackoffMs: 240000,
		Tracker: TrackerConfig{
			Type:        "internal",
			BoardDir:    ".contrabass/board",
			IssuePrefix: "CB",
		},
		Agent: AgentConfig{
			Type: "opencode",
		},
		OpenCode: &OpenCodeConfig{
			BinaryPath: "opencode serve",
			Port:       0,
			Password:   "",
			Model:      "minimax-coding-plan/MiniMax-M2.7",
		},
		Workspace: WorkspaceConfig{
			BaseDir:      ".",
			BranchPrefix: "opencode/",
		},
		AgentTimeoutMs: 900000,
		StallTimeoutMs:  60000,
	}
}

// Parse reads a WORKFLOW.md file and parses its configuration.
func Parse(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	return ParseBytes(data)
}

// ParseBytes parses WORKFLOW.md content from bytes.
func ParseBytes(data []byte) (*Config, error) {
	content := string(data)

	// Find YAML front matter (between --- markers)
	frontMatter, body, ok := extractFrontMatter(content)
	if !ok {
		// No front matter, use defaults
		cfg := DefaultConfig()
		cfg.Content = strings.TrimSpace(content)
		return cfg, nil
	}

	// Start with defaults
	cfg := DefaultConfig()

	// Parse YAML front matter
	if err := yaml.Unmarshal([]byte(frontMatter), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML front matter: %w", err)
	}

	// Store the body content
	cfg.Content = strings.TrimSpace(body)

	return cfg, nil
}

// extractFrontMatter extracts YAML front matter from markdown content.
// Returns the front matter, the remaining content, and whether front matter was found.
func extractFrontMatter(content string) (string, string, bool) {
	// Pattern matches --- at start of content
	// First --- marks start, second --- marks end of front matter
	pattern := `(?s)^\s*---\n(.*?)\n---`

	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(content)

	if len(matches) < 2 {
		return "", content, false
	}

	frontMatter := matches[1]
	// Body is everything after the full match (matches[0])
	body := content[len(matches[0]):]

	return frontMatter, body, true
}

// Load loads a configuration file, expanding paths and handling defaults.
func Load(path string) (*Config, error) {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}

	cfg, err := Parse(absPath)
	if err != nil {
		return nil, err
	}

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.MaxConcurrency < 1 {
		return fmt.Errorf("max_concurrency must be at least 1, got %d", c.MaxConcurrency)
	}

	if c.PollIntervalMs < 100 {
		return fmt.Errorf("poll_interval_ms must be at least 100, got %d", c.PollIntervalMs)
	}

	if c.Tracker.Type == "" {
		return fmt.Errorf("tracker.type is required")
	}

	if c.Agent.Type == "" {
		return fmt.Errorf("agent.type is required")
	}

	// Validate agent-specific config exists for the selected type
	switch c.Agent.Type {
	case "opencode":
		if c.OpenCode == nil {
			return fmt.Errorf("opencode config is required when agent.type is opencode")
		}
		if c.OpenCode.BinaryPath == "" {
			return fmt.Errorf("opencode.binary_path is required")
		}
	case "codex":
		if c.Codex == nil {
			return fmt.Errorf("codex config is required when agent.type is codex")
		}
		if c.Codex.BinaryPath == "" {
			return fmt.Errorf("codex.binary_path is required")
		}
	}

	return nil
}

// ResolvePaths resolves relative paths in the config to absolute paths.
func (c *Config) ResolvePaths(baseDir string) *Config {
	// Resolve workspace base directory
	if !filepath.IsAbs(c.Workspace.BaseDir) {
		c.Workspace.BaseDir = filepath.Join(baseDir, c.Workspace.BaseDir)
	}

	// Resolve tracker board directory
	if !filepath.IsAbs(c.Tracker.BoardDir) {
		c.Tracker.BoardDir = filepath.Join(baseDir, c.Tracker.BoardDir)
	}

	// Note: binary_path values are left as-is (user's PATH or relative to cwd)

	return c
}
