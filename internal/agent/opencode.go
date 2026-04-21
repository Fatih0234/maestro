// Package agent provides the OpenCode agent runner.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// SSE event parsed from server stream
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// SSE reader that parses Server-Sent Events format
type sseReader struct {
	scanner     *bufio.Scanner
	lastEventID string
}

// Managed opencode server process
type openCodeServer struct {
	process *exec.Cmd
	url     string
	port    int
	ready   chan struct{}
}

// OpenCodeRunner manages opencode serve processes and HTTP communication.
type OpenCodeRunner struct {
	binaryPath string
	port       int
	password   string
	username   string
	timeout    time.Duration

	mu           sync.Mutex
	servers      map[string]*openCodeServer
	httpClient   *http.Client
	streamClient *http.Client
}

// Error definitions
var (
	ErrServerAlreadyStopped = errors.New("opencode server already stopped")
	ErrServerStopFailed     = errors.New("opencode server stop failed")
	ErrMissingSessionID    = errors.New("missing session ID")
	ErrMissingServerURL    = errors.New("missing server URL")
)

// Verify we implement the interface
var _ types.AgentRunner = (*OpenCodeRunner)(nil)

// NewOpenCodeRunner creates a new OpenCode runner.
func NewOpenCodeRunner(binaryPath string, port int, password, username string, timeout time.Duration) *OpenCodeRunner {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &OpenCodeRunner{
		binaryPath:   binaryPath,
		port:         port,
		password:     password,
		username:     username,
		timeout:      timeout,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		streamClient: &http.Client{},
		servers:      make(map[string]*openCodeServer),
	}
}

// newSSEReader creates a new SSE reader for the given input.
func newSSEReader(r io.Reader) *sseReader {
	scanner := bufio.NewScanner(r)
	// 64KB buffer, 10MB max
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	return &sseReader{scanner: scanner}
}

// Next returns the next SSE event from the reader.
// Returns io.EOF when the stream ends.
func (s *sseReader) Next() (*sseEvent, error) {
	event := &sseEvent{Event: "message", ID: s.lastEventID}
	dataLines := make([]string, 0, 4)
	hasData := false

	for {
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return nil, err
			}
			if !hasData {
				return nil, io.EOF
			}
			event.Data = strings.Join(dataLines, "\n")
			if event.Event == "" {
				event.Event = "message"
			}
			if event.ID == "" {
				event.ID = s.lastEventID
			}
			return event, nil
		}

		line := s.scanner.Text()
		if line == "" {
			if !hasData {
				event = &sseEvent{Event: "message", ID: s.lastEventID}
				dataLines = dataLines[:0]
				continue
			}
			event.Data = strings.Join(dataLines, "\n")
			if event.Event == "" {
				event.Event = "message"
			}
			if event.ID == "" {
				event.ID = s.lastEventID
			}
			return event, nil
		}

		// SSE comments
		if strings.HasPrefix(line, ":") {
			continue
		}

		field := line
		value := ""
		if idx := strings.Index(line, ":"); idx >= 0 {
			field = line[:idx]
			value = line[idx+1:]
			// Strip leading space after colon
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch field {
		case "event":
			event.Event = value
		case "data":
			dataLines = append(dataLines, value)
			hasData = true
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				event.ID = value
				s.lastEventID = value
			}
		case "retry":
			// Retry is informational, ignore
			continue
		}
	}
}

// ensureServer ensures a server is running for the given workspace.
// Returns the server URL and PID.
func (r *OpenCodeRunner) ensureServer(ctx context.Context, workDir string) (string, int, error) {
	key := serverKey(workDir)

	for {
		r.mu.Lock()
		if server, ok := r.servers[key]; ok && server != nil {
			if server.ready != nil {
				ready := server.ready
				r.mu.Unlock()
				select {
				case <-ctx.Done():
					return "", 0, ctx.Err()
				case <-ready:
				}
				continue
			}

			if server.url != "" {
				pid := serverPID(server)
				r.mu.Unlock()
				return server.url, pid, nil
			}

			delete(r.servers, key)
		}

		argv := strings.Fields(strings.TrimSpace(r.binaryPath))
		if len(argv) == 0 {
			r.mu.Unlock()
			return "", 0, errors.New("opencode binary path is empty")
		}

		port := r.portForNewServerLocked()
		placeholder := &openCodeServer{
			port:  port,
			ready: make(chan struct{}),
		}
		r.servers[key] = placeholder
		r.mu.Unlock()

		server, err := r.startServer(ctx, argv, workDir, port)

		r.mu.Lock()
		current, stillCurrent := r.servers[key]
		if stillCurrent && current == placeholder {
			if err != nil {
				delete(r.servers, key)
			} else {
				placeholder.process = server.process
				placeholder.url = server.url
			}
		}
		ready := placeholder.ready
		placeholder.ready = nil
		r.mu.Unlock()

		if ready != nil {
			close(ready)
		}

		if !stillCurrent || current != placeholder {
			if err == nil {
				_ = r.stopServer(server)
				return "", 0, errors.New("opencode server startup interrupted")
			}
			return "", 0, err
		}

		if err != nil {
			return "", 0, err
		}

		return server.url, serverPID(server), nil
	}
}

// startServer spawns the opencode serve process and waits for it to be ready.
func (r *OpenCodeRunner) startServer(
	ctx context.Context,
	argv []string,
	workDir string,
	port int,
) (*openCodeServer, error) {
	if port > 0 {
		argv = append(argv, "--port", strconv.Itoa(port))
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create opencode stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opencode server: %w", err)
	}

	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			if !scanner.Scan() {
				break
			}

			line := scanner.Text()
			if strings.Contains(line, "listening on http://") || strings.Contains(line, "listening on https://") {
				if url := extractListeningURL(line); url != "" {
					urlCh <- url
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		errCh <- errors.New("opencode server exited before emitting listening URL")
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, ctx.Err()
	case <-timer.C:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("timed out waiting for opencode server startup after %s", r.timeout)
	case err := <-errCh:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("failed to detect opencode server URL: %w", err)
	case url := <-urlCh:
		return &openCodeServer{
			process: cmd,
			url:     strings.TrimRight(url, "/"),
			port:    port,
		}, nil
	}
}

// isSignalError checks if an error is from a signal-terminated process.
func isSignalError(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	// Go returns ExitCode() == -1 for signal-terminated processes
	return exitErr.ExitCode() == -1
}

// stopServer gracefully stops the opencode server process.
func (r *OpenCodeRunner) stopServer(server *openCodeServer) error {
	if server == nil {
		return nil
	}

	cmd := server.process
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt opencode server: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		if err != nil && !errors.Is(err, os.ErrProcessDone) && !isSignalError(err) {
			return err
		}
		return nil
	case <-time.After(r.timeout):
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill opencode server: %w", err)
		}
		select {
		case <-waitCh:
		case <-time.After(r.timeout):
		}
		return nil
	}
}

// Close stops all managed server processes.
func (r *OpenCodeRunner) Close() error {
	r.mu.Lock()
	servers := make([]*openCodeServer, 0, len(r.servers))
	for _, server := range r.servers {
		servers = append(servers, server)
	}
	r.servers = make(map[string]*openCodeServer)
	r.mu.Unlock()

	var closeErr error
	for _, server := range servers {
		if err := r.stopServer(server); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	return closeErr
}

// Start launches an agent for the given issue.
func (r *OpenCodeRunner) Start(ctx context.Context, _ types.Issue, workspace string, prompt string) (*types.AgentProcess, error) {
	workDir := workspace
	if workDir == "" {
		workDir = r.defaultWorkDir()
	}

	serverURL, serverPID, err := r.ensureServer(ctx, workDir)
	if err != nil {
		return nil, err
	}

	sessionID, err := r.createSession(ctx, serverURL)
	if err != nil {
		return nil, err
	}

	events := make(chan types.AgentEvent, 128)
	done := make(chan error, 1)
	streamCtx, cancelStream := context.WithCancel(ctx)
	var doneOnce sync.Once

	finish := func(doneErr error) {
		doneOnce.Do(func() {
			cancelStream()
			done <- doneErr
			close(done)
			close(events)
		})
	}

	go r.streamSessionEvents(streamCtx, serverURL, sessionID, events, finish)

	if err := r.submitPrompt(ctx, serverURL, sessionID, prompt); err != nil {
		cancelStream()
		return nil, err
	}

	return &types.AgentProcess{
		PID:       serverPID,
		SessionID: sessionID,
		Events:    events,
		Done:      done,
		ServerURL: serverURL,
	}, nil
}

// Stop terminates a running agent process.
func (r *OpenCodeRunner) Stop(proc *types.AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	// Use serverURL from process if available, otherwise look it up
	serverURL := proc.ServerURL
	if serverURL == "" {
		serverURL = r.firstServerURL()
	}
	if serverURL == "" {
		return fmt.Errorf("%w: missing server URL", ErrServerAlreadyStopped)
	}

	if strings.TrimSpace(proc.SessionID) == "" {
		return fmt.Errorf("%w: missing session ID", ErrServerStopFailed)
	}

	if err := r.abortSession(context.Background(), serverURL, proc.SessionID); err != nil {
		if errors.Is(err, ErrServerAlreadyStopped) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrServerStopFailed, err)
	}

	return nil
}

// defaultWorkDir returns the default working directory.
func (r *OpenCodeRunner) defaultWorkDir() string {
	return ""
}

// firstServerURL returns the URL of the first available server.
func (r *OpenCodeRunner) firstServerURL() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, server := range r.servers {
		if server != nil && server.url != "" {
			return server.url
		}
	}

	return ""
}

// serverPID returns the PID of a server process.
func serverPID(server *openCodeServer) int {
	if server == nil || server.process == nil || server.process.Process == nil {
		return 0
	}
	return server.process.Process.Pid
}

// createSession creates a new session with the opencode server.
func (r *OpenCodeRunner) createSession(ctx context.Context, serverURL string) (string, error) {
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session", nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("create opencode session failed: status %d body=%q", resp.StatusCode, string(body))
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}

	sessionID, _ := payload["id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("create session response missing id")
	}

	return sessionID, nil
}

// submitPrompt submits a prompt to the session asynchronously.
func (r *OpenCodeRunner) submitPrompt(ctx context.Context, serverURL, sessionID, prompt string) error {
	body := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"type": "text", "text": prompt},
		},
	}
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session/"+sessionID+"/prompt_async", body, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("submit opencode prompt failed: status %d body=%q", resp.StatusCode, string(respBody))
	}

	return nil
}

// abortSession aborts the current task in a session.
func (r *OpenCodeRunner) abortSession(ctx context.Context, serverURL, sessionID string) error {
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session/"+sessionID+"/abort", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusGone {
		return fmt.Errorf("%w: status %d", ErrServerAlreadyStopped, resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("abort opencode session failed: status %d", resp.StatusCode)
	}

	return nil
}

// streamSessionEvents streams events from the opencode server.
func (r *OpenCodeRunner) streamSessionEvents(
	ctx context.Context,
	serverURL string,
	sessionID string,
	events chan<- types.AgentEvent,
	finish func(error),
) {
	defer finish(nil)

	headers := map[string]string{"Accept": "text/event-stream"}
	resp, err := r.doStreamRequest(ctx, http.MethodGet, serverURL+"/event", headers)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		finish(fmt.Errorf("connect opencode event stream: %w", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		finish(fmt.Errorf("opencode event stream failed: status %d body=%q", resp.StatusCode, string(body)))
		return
	}

	reader := newSSEReader(resp.Body)
	for {
		event, readErr := reader.Next()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || ctx.Err() != nil {
				return
			}
			finish(fmt.Errorf("read opencode event stream: %w", readErr))
			return
		}

		if event == nil || strings.TrimSpace(event.Data) == "" {
			continue
		}

		payload := map[string]interface{}{}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			protocolErr := types.AgentEvent{
				Type: EventTypeProtocolError,
				Payload: map[string]interface{}{
					"error": fmt.Sprintf("malformed opencode event payload: %s", err.Error()),
					"raw":   event.Data,
				},
			}
			select {
			case <-ctx.Done():
				return
			case events <- protocolErr:
			default:
			}
			continue
		}

		jsonEventType, _ := payload["type"].(string)
		eventType := event.Event
		if jsonEventType != "" {
			eventType = jsonEventType
		}
		if eventType == "" {
			eventType = "message"
		}

		// Ignore heartbeats and connected events
		if eventType == EventTypeHeartbeat || eventType == EventTypeConnected {
			continue
		}

		// Filter events by session ID
		eventSessionID := extractSessionIDFromPayload(payload)
		if eventSessionID != sessionID {
			continue
		}

		agentEvent := types.AgentEvent{
			Type:    eventType,
			Payload: payload,
		}

		select {
		case <-ctx.Done():
			return
		case events <- agentEvent:
		default:
		}

		// Check for session error
		if eventType == EventTypeSessionError {
			finish(errors.New("opencode session error"))
			return
		}

		// Check for idle status (session completed)
		if eventType == EventTypeSessionStatus && isStatusIdle(payload) {
			return
		}
	}
}

// extractSessionIDFromPayload extracts session ID from a payload map.
func extractSessionIDFromPayload(payload map[string]interface{}) string {
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return ""
	}
	sessionID, _ := properties["sessionID"].(string)
	return sessionID
}

// isStatusIdle checks if a payload indicates idle status.
func isStatusIdle(payload map[string]interface{}) bool {
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return false
	}

	status, ok := properties["status"].(map[string]interface{})
	if !ok {
		return false
	}

	statusType, _ := status["type"].(string)
	return statusType == "idle"
}

// doRequest performs an HTTP request.
func (r *OpenCodeRunner) doRequest(
	ctx context.Context,
	method string,
	url string,
	body interface{},
	headers map[string]string,
) (*http.Response, error) {
	return r.doRequestWithClient(ctx, r.httpClient, method, url, body, headers)
}

// doStreamRequest performs an HTTP request for streaming responses.
func (r *OpenCodeRunner) doStreamRequest(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
) (*http.Response, error) {
	return r.doRequestWithClient(ctx, r.streamClient, method, url, nil, headers)
}

// doRequestWithClient performs an HTTP request with the given client.
func (r *OpenCodeRunner) doRequestWithClient(
	ctx context.Context,
	client *http.Client,
	method string,
	url string,
	body interface{},
	headers map[string]string,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if r.password != "" {
		req.SetBasicAuth(r.username, r.password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform %s %s: %w", method, url, err)
	}

	return resp, nil
}

// extractListeningURL extracts the URL from an opencode startup line.
func extractListeningURL(line string) string {
	idx := strings.Index(line, "http://")
	if idx < 0 {
		idx = strings.Index(line, "https://")
	}
	if idx < 0 {
		return ""
	}

	urlPart := strings.TrimSpace(line[idx:])
	pieces := strings.Fields(urlPart)
	if len(pieces) == 0 {
		return ""
	}

	return strings.Trim(pieces[0], "\"'`")
}

// serverKey returns the key for a server in the map.
func serverKey(workDir string) string {
	return workDir
}

// portForNewServerLocked returns a port for a new server.
// Caller must hold r.mu.
func (r *OpenCodeRunner) portForNewServerLocked() int {
	if r.port <= 0 {
		return 0
	}

	port := r.port
	for {
		inUse := false
		for _, server := range r.servers {
			if server != nil && server.port == port {
				inUse = true
				break
			}
		}
		if !inUse {
			return port
		}
		port++
	}
}
