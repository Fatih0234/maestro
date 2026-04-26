package main

import (
	"testing"
)

func TestRunDoctor_DoesNotPanic(t *testing.T) {
	// runDoctor should never panic, even when run outside a project.
	if err := runDoctor(nil); err != nil {
		t.Fatalf("runDoctor returned unexpected error: %v", err)
	}
}
