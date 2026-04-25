package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

var githubAPIBase = "https://api.github.com"

// GitHubTracker implements types.IssueTracker using the GitHub REST API v3.
type GitHubTracker struct {
	owner       string
	repo        string
	token       string
	labelPrefix string
	assigneeBot string
	client      *http.Client
}

// NewGitHub creates a new GitHubTracker.
func NewGitHub(owner, repo, token, labelPrefix, assigneeBot string) *GitHubTracker {
	if strings.TrimSpace(labelPrefix) == "" {
		labelPrefix = "contrabass"
	}
	if strings.TrimSpace(assigneeBot) == "" {
		assigneeBot = "contrabass"
	}
	return &GitHubTracker{
		owner:       strings.TrimSpace(owner),
		repo:        strings.TrimSpace(repo),
		token:       strings.TrimSpace(token),
		labelPrefix: labelPrefix,
		assigneeBot: assigneeBot,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

// --- GitHub API types ---

type githubIssue struct {
	Number      int           `json:"number"`
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	State       string        `json:"state"`
	Labels      []githubLabel `json:"labels"`
	Assignees   []githubUser  `json:"assignees"`
	HTMLURL     string        `json:"html_url"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	PullRequest *struct{}     `json:"pull_request,omitempty"`
}

type githubLabel struct {
	Name string `json:"name"`
}

type githubUser struct {
	Login string `json:"login"`
}

type githubComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// --- IssueTracker implementation ---

// FetchIssues returns all open issues that are ready for processing.
// It skips pull requests, issues in review, closed issues, and retry-queued
// issues whose retry time has not yet passed.
func (t *GitHubTracker) FetchIssues() ([]types.Issue, error) {
	var all []githubIssue
	path := "/issues?state=open&per_page=100"
	if err := t.get(path, &all); err != nil {
		return nil, fmt.Errorf("fetching issues: %w", err)
	}

	now := time.Now()
	var result []types.Issue

	for _, gi := range all {
		// Skip pull requests
		if gi.PullRequest != nil {
			continue
		}

		state := t.toIssueState(&gi)

		// Skip terminal/handoff states
		if state == types.StateInReview || state == types.StateReleased {
			continue
		}

		// Skip retry-queued issues that aren't due yet
		if state == types.StateRetryQueued {
			due, err := t.retryDueTime(gi.Number)
			if err != nil {
				continue // be conservative: skip if we can't tell
			}
			if due != nil && now.Before(*due) {
				continue
			}
		}

		// Skip issues claimed by someone else
		if len(gi.Assignees) > 0 {
			claimedByBot := false
			for _, a := range gi.Assignees {
				if strings.EqualFold(a.Login, t.assigneeBot) {
					claimedByBot = true
					break
				}
			}
			if !claimedByBot {
				continue
			}
		}

		result = append(result, t.toTypesIssue(gi, state))
	}

	return result, nil
}

// ClaimIssue assigns the configured bot user to the issue.
// It fails if the issue is already assigned to someone else (race).
func (t *GitHubTracker) ClaimIssue(id string) (types.Issue, error) {
	num, err := parseIssueNumber(id)
	if err != nil {
		return types.Issue{}, err
	}

	// Fetch current state to detect races
	var gi githubIssue
	if err := t.get(fmt.Sprintf("/issues/%d", num), &gi); err != nil {
		return types.Issue{}, fmt.Errorf("getting issue %s: %w", id, err)
	}

	if len(gi.Assignees) > 0 {
		claimedByBot := false
		for _, a := range gi.Assignees {
			if strings.EqualFold(a.Login, t.assigneeBot) {
				claimedByBot = true
				break
			}
		}
		if !claimedByBot {
			return types.Issue{}, fmt.Errorf("issue %s is already assigned to another user", id)
		}
	}

	var updated githubIssue
	body := map[string]interface{}{"assignees": []string{t.assigneeBot}}
	if err := t.patch(fmt.Sprintf("/issues/%d", num), body, &updated); err != nil {
		return types.Issue{}, fmt.Errorf("claiming issue %s: %w", id, err)
	}

	return t.toTypesIssue(updated, types.StateRunning), nil
}

// ReleaseIssue unassigns the issue and re-opens it.
func (t *GitHubTracker) ReleaseIssue(id string) (types.Issue, error) {
	num, err := parseIssueNumber(id)
	if err != nil {
		return types.Issue{}, err
	}

	var updated githubIssue
	body := map[string]interface{}{
		"assignees": []string{},
		"state":     "open",
	}
	if err := t.patch(fmt.Sprintf("/issues/%d", num), body, &updated); err != nil {
		return types.Issue{}, fmt.Errorf("releasing issue %s: %w", id, err)
	}

	return t.toTypesIssue(updated, types.StateUnclaimed), nil
}

// GetIssue fetches a single issue by ID.
func (t *GitHubTracker) GetIssue(id string) (types.Issue, error) {
	num, err := parseIssueNumber(id)
	if err != nil {
		return types.Issue{}, err
	}

	var gi githubIssue
	if err := t.get(fmt.Sprintf("/issues/%d", num), &gi); err != nil {
		return types.Issue{}, fmt.Errorf("getting issue %s: %w", id, err)
	}

	state := t.toIssueState(&gi)
	return t.toTypesIssue(gi, state), nil
}

// UpdateIssueState updates an issue's state on GitHub.
func (t *GitHubTracker) UpdateIssueState(id string, state types.IssueState) (types.Issue, error) {
	num, err := parseIssueNumber(id)
	if err != nil {
		return types.Issue{}, err
	}

	switch state {
	case types.StateUnclaimed:
		return t.updateToUnclaimed(num)
	case types.StateClaimed, types.StateRunning:
		return t.updateToRunning(num)
	case types.StateInReview:
		return t.updateToInReview(num)
	case types.StateReleased:
		return t.updateToReleased(num)
	case types.StateRetryQueued:
		return t.updateToRetryQueued(num)
	default:
		return types.Issue{}, fmt.Errorf("unsupported issue state: %v", state)
	}
}

// SetRetryQueue marks an issue as waiting for retry by adding a retry label
// and posting a comment with the retry timestamp.
func (t *GitHubTracker) SetRetryQueue(id string, retryAt time.Time) (types.Issue, error) {
	num, err := parseIssueNumber(id)
	if err != nil {
		return types.Issue{}, err
	}

	// Add retry label
	if err := t.addLabel(num, t.retryLabel()); err != nil {
		return types.Issue{}, fmt.Errorf("adding retry label to issue %s: %w", id, err)
	}

	// Post retry comment
	comment := fmt.Sprintf("%s:retry:%s", t.labelPrefix, retryAt.UTC().Format(time.RFC3339))
	if err := t.postComment(num, comment); err != nil {
		return types.Issue{}, fmt.Errorf("posting retry comment to issue %s: %w", id, err)
	}

	var gi githubIssue
	if err := t.get(fmt.Sprintf("/issues/%d", num), &gi); err != nil {
		return types.Issue{}, fmt.Errorf("getting issue %s after retry queue: %w", id, err)
	}

	issue := t.toTypesIssue(gi, types.StateRetryQueued)
	issue.RetryAfter = &retryAt
	return issue, nil
}

// --- State-specific update helpers ---

func (t *GitHubTracker) updateToUnclaimed(num int) (types.Issue, error) {
	labels, err := t.filterLabels(num, nil)
	if err != nil {
		return types.Issue{}, fmt.Errorf("filtering labels for issue #%d: %w", num, err)
	}
	var updated githubIssue
	body := map[string]interface{}{
		"assignees": []string{},
		"state":     "open",
		"labels":    labels,
	}
	if err := t.patch(fmt.Sprintf("/issues/%d", num), body, &updated); err != nil {
		return types.Issue{}, err
	}
	return t.toTypesIssue(updated, types.StateUnclaimed), nil
}

func (t *GitHubTracker) updateToRunning(num int) (types.Issue, error) {
	var updated githubIssue
	body := map[string]interface{}{
		"assignees": []string{t.assigneeBot},
		"state":     "open",
	}
	if err := t.patch(fmt.Sprintf("/issues/%d", num), body, &updated); err != nil {
		return types.Issue{}, err
	}
	return t.toTypesIssue(updated, types.StateRunning), nil
}

func (t *GitHubTracker) updateToInReview(num int) (types.Issue, error) {
	// Set labels: remove retry, add review (idempotent — overwrites managed labels)
	labels, err := t.filterLabels(num, []string{t.reviewLabel()})
	if err != nil {
		return types.Issue{}, fmt.Errorf("filtering labels for issue #%d: %w", num, err)
	}
	var updated githubIssue
	if err := t.patch(fmt.Sprintf("/issues/%d", num), map[string]interface{}{"labels": labels}, &updated); err != nil {
		return types.Issue{}, err
	}

	// Post handoff comment only if one doesn't already exist
	handoffText := fmt.Sprintf("%s: handoff for review", t.labelPrefix)
	exists, err := t.hasComment(num, handoffText)
	if err != nil {
		return types.Issue{}, fmt.Errorf("checking existing handoff comment: %w", err)
	}
	if !exists {
		if err := t.postComment(num, handoffText); err != nil {
			return types.Issue{}, err
		}
	}

	if err := t.get(fmt.Sprintf("/issues/%d", num), &updated); err != nil {
		return types.Issue{}, err
	}
	return t.toTypesIssue(updated, types.StateInReview), nil
}

func (t *GitHubTracker) updateToReleased(num int) (types.Issue, error) {
	var updated githubIssue
	body := map[string]interface{}{
		"state": "closed",
	}
	if err := t.patch(fmt.Sprintf("/issues/%d", num), body, &updated); err != nil {
		return types.Issue{}, err
	}
	return t.toTypesIssue(updated, types.StateReleased), nil
}

func (t *GitHubTracker) updateToRetryQueued(num int) (types.Issue, error) {
	if err := t.addLabel(num, t.retryLabel()); err != nil {
		return types.Issue{}, err
	}
	var updated githubIssue
	if err := t.get(fmt.Sprintf("/issues/%d", num), &updated); err != nil {
		return types.Issue{}, err
	}
	return t.toTypesIssue(updated, types.StateRetryQueued), nil
}

// --- Label helpers ---

func (t *GitHubTracker) retryLabel() string {
	if t.labelPrefix == "" {
		return "retry"
	}
	return t.labelPrefix + ":retry"
}

func (t *GitHubTracker) reviewLabel() string {
	if t.labelPrefix == "" {
		return "review"
	}
	return t.labelPrefix + ":review"
}

// filterLabels returns the current labels for an issue minus any contrabass-managed labels.
func (t *GitHubTracker) filterLabels(num int, extra []string) ([]string, error) {
	var gi githubIssue
	if err := t.get(fmt.Sprintf("/issues/%d", num), &gi); err != nil {
		return nil, err
	}

	managed := map[string]bool{
		t.retryLabel():  true,
		t.reviewLabel(): true,
	}

	var filtered []string
	for _, l := range gi.Labels {
		if !managed[l.Name] {
			filtered = append(filtered, l.Name)
		}
	}
	return append(filtered, extra...), nil
}

// --- Retry time parsing ---

// retryDueTime fetches comments for an issue and finds the most recent
// contrabass retry timestamp. Returns nil if no retry comment is found.
func (t *GitHubTracker) retryDueTime(num int) (*time.Time, error) {
	var comments []githubComment
	if err := t.get(fmt.Sprintf("/issues/%d/comments", num), &comments); err != nil {
		return nil, err
	}

	prefix := t.labelPrefix + ":retry:"
	var latest *time.Time

	for _, c := range comments {
		if !strings.HasPrefix(c.Body, prefix) {
			continue
		}
		tsStr := strings.TrimPrefix(c.Body, prefix)
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			continue
		}
		if latest == nil || ts.After(*latest) {
			latest = &ts
		}
	}

	return latest, nil
}

// --- Conversion helpers ---

func (t *GitHubTracker) toIssueState(gi *githubIssue) types.IssueState {
	if gi.State == "closed" {
		return types.StateReleased
	}

	hasRetry := false
	hasReview := false
	for _, l := range gi.Labels {
		if l.Name == t.retryLabel() {
			hasRetry = true
		}
		if l.Name == t.reviewLabel() {
			hasReview = true
		}
	}

	if hasReview {
		return types.StateInReview
	}
	if hasRetry {
		return types.StateRetryQueued
	}

	if len(gi.Assignees) > 0 {
		for _, a := range gi.Assignees {
			if strings.EqualFold(a.Login, t.assigneeBot) {
				return types.StateRunning
			}
		}
		// Assigned to someone else
		return types.StateUnclaimed
	}

	return types.StateUnclaimed
}

func (t *GitHubTracker) toTypesIssue(gi githubIssue, state types.IssueState) types.Issue {
	labels := make([]string, len(gi.Labels))
	for i, l := range gi.Labels {
		labels[i] = l.Name
	}

	return types.Issue{
		ID:          strconv.Itoa(gi.Number),
		Identifier:  strconv.Itoa(gi.Number),
		Title:       gi.Title,
		Description: gi.Body,
		State:       state,
		Labels:      labels,
		URL:         gi.HTMLURL,
		CreatedAt:   gi.CreatedAt,
		UpdatedAt:   gi.UpdatedAt,
	}
}

func parseIssueNumber(id string) (int, error) {
	num, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("invalid issue id %q (expected numeric GitHub issue number): %w", id, err)
	}
	return num, nil
}

// --- HTTP helpers ---

// githubAPIError carries the HTTP status code and body for failed GitHub API calls.
type githubAPIError struct {
	StatusCode int
	Body       string
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("github API error %d: %s", e.StatusCode, e.Body)
}

// isRetryable reports whether an error from do() warrants a retry.
// Client errors (4xx) are not retried except 429 (Too Many Requests).
// Server errors (5xx) and network errors are retried.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(*githubAPIError)
	if !ok {
		return true // network or other non-HTTP error
	}
	if apiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return apiErr.StatusCode >= 500
}

func (t *GitHubTracker) get(path string, out interface{}) error {
	url := githubAPIBase + "/repos/" + t.owner + "/" + t.repo + path

	var lastErr error
	for i := 0; i < 2; i++ {
		if i > 0 {
			time.Sleep(1 * time.Second)
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}

		resp, err := t.do(req)
		if err != nil {
			lastErr = err
			if !isRetryable(err) {
				break
			}
			continue
		}

		if out != nil {
			decodeErr := json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			if decodeErr != nil {
				return fmt.Errorf("decoding github response: %w", decodeErr)
			}
		} else {
			resp.Body.Close()
		}
		return nil
	}

	return fmt.Errorf("github GET %s failed after retry: %w", path, lastErr)
}

func (t *GitHubTracker) patch(path string, body interface{}, out interface{}) error {
	url := githubAPIBase + "/repos/" + t.owner + "/" + t.repo + path
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (t *GitHubTracker) postComment(num int, text string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", githubAPIBase, t.owner, t.repo, num)
	body := map[string]string{"body": text}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

// hasComment returns true if the issue already has a comment with the given text.
func (t *GitHubTracker) hasComment(num int, text string) (bool, error) {
	var comments []githubComment
	if err := t.get(fmt.Sprintf("/issues/%d/comments", num), &comments); err != nil {
		return false, err
	}
	for _, c := range comments {
		if strings.TrimSpace(c.Body) == text {
			return true, nil
		}
	}
	return false, nil
}

func (t *GitHubTracker) addLabel(num int, label string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/labels", githubAPIBase, t.owner, t.repo, num)
	body := map[string][]string{"labels": {label}}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (t *GitHubTracker) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		if remaining == "0" {
			resetStr := resp.Header.Get("X-RateLimit-Reset")
			resp.Body.Close()
			return nil, fmt.Errorf("github rate limit exceeded (reset at %s)", resetStr)
		}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &githubAPIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	return resp, nil
}
