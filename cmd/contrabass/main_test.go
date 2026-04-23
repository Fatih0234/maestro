package main

import "testing"

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
