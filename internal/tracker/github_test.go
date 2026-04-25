package tracker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func withTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *GitHubTracker) {
	server := httptest.NewServer(handler)
	oldBase := githubAPIBase
	githubAPIBase = server.URL
	t.Cleanup(func() {
		githubAPIBase = oldBase
		server.Close()
	})
	tr := NewGitHub("test-owner", "test-repo", "test-token", "contrabass", "contrabass-bot")
	return server, tr
}

func TestGitHubTracker_FetchIssues_SkipsPRs(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []map[string]interface{}{
			{
				"number":       1,
				"title":        "Real Issue",
				"body":         "This is a real issue",
				"state":        "open",
				"labels":       []map[string]string{},
				"assignees":    []map[string]string{},
				"html_url":     "https://github.com/test-owner/test-repo/issues/1",
				"created_at":   "2024-01-01T00:00:00Z",
				"updated_at":   "2024-01-01T00:00:00Z",
				"pull_request": map[string]string{},
			},
			{
				"number":     2,
				"title":      "Another Issue",
				"body":       "This is another issue",
				"state":      "open",
				"labels":     []map[string]string{},
				"assignees":  []map[string]string{},
				"html_url":   "https://github.com/test-owner/test-repo/issues/2",
				"created_at": "2024-01-02T00:00:00Z",
				"updated_at": "2024-01-02T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})

	issues, err := tr.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (PR skipped), got %d", len(issues))
	}
	if issues[0].ID != "2" {
		t.Errorf("expected issue #2, got %s", issues[0].ID)
	}
}

func TestGitHubTracker_ClaimIssue(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	callCount := 0
	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var issue map[string]interface{}
		switch r.Method {
		case "GET":
			issue = map[string]interface{}{
				"number":     42,
				"title":      "Test Issue",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{},
				"assignees":  []map[string]string{},
				"html_url":   "https://github.com/test-owner/test-repo/issues/42",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			}
		case "PATCH":
			issue = map[string]interface{}{
				"number":     42,
				"title":      "Test Issue",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{},
				"assignees":  []map[string]string{{"login": "contrabass-bot"}},
				"html_url":   "https://github.com/test-owner/test-repo/issues/42",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	issue, err := tr.ClaimIssue("42")
	if err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}

	if issue.State != types.StateRunning {
		t.Errorf("expected state running, got %v", issue.State)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 API calls (GET + PATCH), got %d", callCount)
	}
}

func TestGitHubTracker_ClaimIssue_Race(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		issue := map[string]interface{}{
			"number":     42,
			"title":      "Test Issue",
			"body":       "Description",
			"state":      "open",
			"labels":     []map[string]string{},
			"assignees":  []map[string]string{{"login": "someone-else"}},
			"html_url":   "https://github.com/test-owner/test-repo/issues/42",
			"created_at": "2024-01-01T00:00:00Z",
			"updated_at": "2024-01-01T00:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	_, err := tr.ClaimIssue("42")
	if err == nil {
		t.Fatal("expected ClaimIssue to fail when already assigned")
	}
	if !strings.Contains(err.Error(), "already assigned") {
		t.Errorf("expected 'already assigned' error, got: %v", err)
	}
}

func TestGitHubTracker_GetIssue_404_NoRetry(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	callCount := 0
	mux.HandleFunc("/repos/test-owner/test-repo/issues/999", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})

	start := time.Now()
	_, err := tr.GetIssue("999")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected GetIssue to fail for non-existent issue")
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 API call (no retry for 404), got %d", callCount)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected immediate failure without retry sleep, took %v", elapsed)
	}
}

func TestGitHubTracker_GetIssue_404(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})

	_, err := tr.GetIssue("999")
	if err == nil {
		t.Fatal("expected GetIssue to fail for non-existent issue")
	}
	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestGitHubTracker_RateLimit(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1234567890")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "API rate limit exceeded"})
	})

	_, err := tr.FetchIssues()
	if err == nil {
		t.Fatal("expected FetchIssues to fail on rate limit")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestGitHubTracker_ReleaseIssue(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		var issue map[string]interface{}
		if r.Method == "PATCH" {
			issue = map[string]interface{}{
				"number":     42,
				"title":      "Test Issue",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{},
				"assignees":  []map[string]string{},
				"html_url":   "https://github.com/test-owner/test-repo/issues/42",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	issue, err := tr.ReleaseIssue("42")
	if err != nil {
		t.Fatalf("ReleaseIssue failed: %v", err)
	}
	if issue.State != types.StateUnclaimed {
		t.Errorf("expected state unclaimed, got %v", issue.State)
	}
}

func TestGitHubTracker_UpdateIssueState_InReview(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{{"name": "contrabass:review"}})
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "body": "contrabass: handoff for review"})
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		issue := map[string]interface{}{
			"number":     42,
			"title":      "Test Issue",
			"body":       "Description",
			"state":      "open",
			"labels":     []map[string]string{{"name": "contrabass:review"}},
			"assignees":  []map[string]string{},
			"html_url":   "https://github.com/test-owner/test-repo/issues/42",
			"created_at": "2024-01-01T00:00:00Z",
			"updated_at": "2024-01-01T00:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	issue, err := tr.UpdateIssueState("42", types.StateInReview)
	if err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}
	if issue.State != types.StateInReview {
		t.Errorf("expected state in_review, got %v", issue.State)
	}
}

func TestGitHubTracker_UpdateIssueState_Released(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		issue := map[string]interface{}{
			"number":     42,
			"title":      "Test Issue",
			"body":       "Description",
			"state":      "closed",
			"labels":     []map[string]string{},
			"assignees":  []map[string]string{},
			"html_url":   "https://github.com/test-owner/test-repo/issues/42",
			"created_at": "2024-01-01T00:00:00Z",
			"updated_at": "2024-01-01T00:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	issue, err := tr.UpdateIssueState("42", types.StateReleased)
	if err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}
	if issue.State != types.StateReleased {
		t.Errorf("expected state released, got %v", issue.State)
	}
}

func TestGitHubTracker_UpdateIssueState_Running(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		issue := map[string]interface{}{
			"number":     42,
			"title":      "Test Issue",
			"body":       "Description",
			"state":      "open",
			"labels":     []map[string]string{},
			"assignees":  []map[string]string{{"login": "contrabass-bot"}},
			"html_url":   "https://github.com/test-owner/test-repo/issues/42",
			"created_at": "2024-01-01T00:00:00Z",
			"updated_at": "2024-01-01T00:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	issue, err := tr.UpdateIssueState("42", types.StateRunning)
	if err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}
	if issue.State != types.StateRunning {
		t.Errorf("expected state running, got %v", issue.State)
	}
}

func TestGitHubTracker_SetRetryQueue(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{{"name": "contrabass:retry"}})
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		issue := map[string]interface{}{
			"number":     42,
			"title":      "Test Issue",
			"body":       "Description",
			"state":      "open",
			"labels":     []map[string]string{{"name": "contrabass:retry"}},
			"assignees":  []map[string]string{},
			"html_url":   "https://github.com/test-owner/test-repo/issues/42",
			"created_at": "2024-01-01T00:00:00Z",
			"updated_at": "2024-01-01T00:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	})

	retryAt := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	issue, err := tr.SetRetryQueue("42", retryAt)
	if err != nil {
		t.Fatalf("SetRetryQueue failed: %v", err)
	}
	if issue.State != types.StateRetryQueued {
		t.Errorf("expected state retry_queued, got %v", issue.State)
	}
	if issue.RetryAfter == nil {
		t.Fatal("expected RetryAfter to be set")
	}
	if !issue.RetryAfter.Equal(retryAt) {
		t.Errorf("RetryAfter = %v, want %v", *issue.RetryAfter, retryAt)
	}
}

func TestGitHubTracker_FetchIssues_RetryNotDue(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []map[string]interface{}{
			{
				"number":     1,
				"title":      "Retry Issue",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{{"name": "contrabass:retry"}},
				"assignees":  []map[string]string{{"login": "contrabass-bot"}},
				"html_url":   "https://github.com/test-owner/test-repo/issues/1",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
		comments := []map[string]interface{}{
			{
				"body":       "contrabass:retry:" + future,
				"created_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	issues, err := tr.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues (retry not due), got %d", len(issues))
	}
}

func TestGitHubTracker_FetchIssues_RetryDue(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []map[string]interface{}{
			{
				"number":     1,
				"title":      "Retry Issue",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{{"name": "contrabass:retry"}},
				"assignees":  []map[string]string{{"login": "contrabass-bot"}},
				"html_url":   "https://github.com/test-owner/test-repo/issues/1",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})

	mux.HandleFunc("/repos/test-owner/test-repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
		comments := []map[string]interface{}{
			{
				"body":       "contrabass:retry:" + past,
				"created_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	issues, err := tr.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("expected 1 issue (retry is due), got %d", len(issues))
	}
}

func TestGitHubTracker_FetchIssues_SkipsAssignedToOther(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []map[string]interface{}{
			{
				"number":     1,
				"title":      "Assigned to other",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{},
				"assignees":  []map[string]string{{"login": "someone-else"}},
				"html_url":   "https://github.com/test-owner/test-repo/issues/1",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})

	issues, err := tr.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues (assigned to other), got %d", len(issues))
	}
}

func TestGitHubTracker_FetchIssues_SkipsInReview(t *testing.T) {
	mux := http.NewServeMux()
	_, tr := withTestServer(t, mux)

	mux.HandleFunc("/repos/test-owner/test-repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []map[string]interface{}{
			{
				"number":     1,
				"title":      "In Review",
				"body":       "Description",
				"state":      "open",
				"labels":     []map[string]string{{"name": "contrabass:review"}},
				"assignees":  []map[string]string{},
				"html_url":   "https://github.com/test-owner/test-repo/issues/1",
				"created_at": "2024-01-01T00:00:00Z",
				"updated_at": "2024-01-01T00:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})

	issues, err := tr.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues (in_review), got %d", len(issues))
	}
}

func TestGitHubTracker_NewGitHub_Defaults(t *testing.T) {
	tr := NewGitHub("owner", "repo", "token", "", "")
	if tr.labelPrefix != "contrabass" {
		t.Errorf("expected default labelPrefix 'contrabass', got %q", tr.labelPrefix)
	}
	if tr.assigneeBot != "contrabass" {
		t.Errorf("expected default assigneeBot 'contrabass', got %q", tr.assigneeBot)
	}
}

func TestGitHubTracker_ParseIssueNumber(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"42", 42, false},
		{"0", 0, false},
		{"abc", 0, true},
		{"", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseIssueNumber(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseIssueNumber(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestGitHubTracker_ToIssueState(t *testing.T) {
	tr := NewGitHub("o", "r", "t", "cb", "bot")

	tests := []struct {
		name string
		gi   *githubIssue
		want types.IssueState
	}{
		{
			name: "closed",
			gi:   &githubIssue{State: "closed"},
			want: types.StateReleased,
		},
		{
			name: "unassigned open",
			gi:   &githubIssue{State: "open", Assignees: nil, Labels: nil},
			want: types.StateUnclaimed,
		},
		{
			name: "assigned to bot",
			gi:   &githubIssue{State: "open", Assignees: []githubUser{{Login: "bot"}}, Labels: nil},
			want: types.StateRunning,
		},
		{
			name: "assigned to other",
			gi:   &githubIssue{State: "open", Assignees: []githubUser{{Login: "other"}}, Labels: nil},
			want: types.StateUnclaimed,
		},
		{
			name: "retry label",
			gi:   &githubIssue{State: "open", Labels: []githubLabel{{Name: "cb:retry"}}},
			want: types.StateRetryQueued,
		},
		{
			name: "review label",
			gi:   &githubIssue{State: "open", Labels: []githubLabel{{Name: "cb:review"}}},
			want: types.StateInReview,
		},
		{
			name: "review trumps retry",
			gi:   &githubIssue{State: "open", Labels: []githubLabel{{Name: "cb:review"}, {Name: "cb:retry"}}},
			want: types.StateInReview,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tr.toIssueState(tc.gi)
			if got != tc.want {
				t.Errorf("toIssueState() = %v, want %v", got, tc.want)
			}
		})
	}
}
