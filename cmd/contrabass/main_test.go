package main

import (
	"testing"

	"github.com/fatihkarahan/contrabass-pi/internal/config"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  logSeverity
	}{
		{name: "debug", value: "debug", want: severityDebug},
		{name: "info", value: "info", want: severityInfo},
		{name: "warn", value: "warn", want: severityWarn},
		{name: "error", value: "error", want: severityError},
		{name: "trimmed and uppercased", value: "  INFO  ", want: severityInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLogLevel(tt.value)
			if err != nil {
				t.Fatalf("parseLogLevel(%q) returned error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseLogLevelRejectsInvalidValues(t *testing.T) {
	if _, err := parseLogLevel("trace"); err == nil {
		t.Fatal("parseLogLevel(\"trace\") expected error, got nil")
	}
}

func TestNewAgentRunnerFromConfig_OpenCode(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{Type: "opencode"},
		OpenCode: &config.OpenCodeConfig{
			BinaryPath: "opencode serve",
		},
	}
	runner, err := newAgentRunnerFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner == nil {
		t.Fatal("expected runner, got nil")
	}
}

func TestNewAgentRunnerFromConfig_OpenCodeMissingBlock(t *testing.T) {
	cfg := &config.Config{
		Agent:    config.AgentConfig{Type: "opencode"},
		OpenCode: nil,
	}
	_, err := newAgentRunnerFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error when opencode block is missing")
	}
}

func TestNewAgentRunnerFromConfig_UnknownType(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{Type: "unknown-agent"},
	}
	_, err := newAgentRunnerFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown agent type")
	}
}
