//go:build ignore

// Demo script to test Phase 2: OpenCodeRunner with real opencode server
// and profile/agent configuration
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func main() {
	fmt.Println("=== Contrabass Phase 2 Demo ===")
	fmt.Println("Testing OpenCodeRunner with profile/agent configuration\n")

	// Get profile and agent from environment or use defaults
	profile := os.Getenv("OPENCODE_TEST_PROFILE")
	if profile == "" {
		profile = "ws"
	}
	agentName := os.Getenv("OPENCODE_TEST_AGENT")
	if agentName == "" {
		agentName = "scribe"
	}

	// Create runner with profile and agent configuration
	runner := agent.NewOpenCodeRunner("opencode serve", 0, "", "", 30*time.Second, profile, agentName, "")

	workspace := os.TempDir()
	prompt := "just testing, just say yes"

	fmt.Printf("Starting session in workspace: %s\n", workspace)
	fmt.Printf("Profile: %s\n", profile)
	fmt.Printf("Agent: %s\n", agentName)
	fmt.Printf("Prompt: %s\n\n", prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start the agent
	proc, err := runner.Start(ctx, types.Issue{ID: "demo-1", Title: "Test"}, workspace, prompt)
	if err != nil {
		fmt.Printf("❌ Start failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Server started (PID: %d, Session: %s)\n\n", proc.PID, proc.SessionID)
	fmt.Println("--- Events ---")

	// Drain events
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\n⏱️  Context cancelled")
			return
		case event, ok := <-proc.Events:
			if !ok {
				fmt.Println("\n📭 Events channel closed")
				goto checkDone
			}
			fmt.Printf("  [%s] %s\n", event.Type, formatPayload(event.Payload))
		case doneErr, ok := <-proc.Done:
			if !ok {
				fmt.Println("\n📭 Done channel closed")
				return
			}
			if doneErr != nil {
				fmt.Printf("\n❌ Session failed: %v\n", doneErr)
				runner.Close()
				os.Exit(1)
			}
			fmt.Println("\n✅ Session completed successfully")
			runner.Close()
			return
		}
	}

checkDone:
	// Drain done
	<-proc.Done
	fmt.Println("\n✅ Session completed")
	runner.Close()
}

func formatPayload(payload interface{}) string {
	if payload == nil {
		return ""
	}
	if m, ok := payload.(map[string]interface{}); ok {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
		if content, ok := m["content"].(string); ok {
			return content
		}
	}
	return fmt.Sprintf("%v", payload)
}
