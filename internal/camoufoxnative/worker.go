package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const aiStudioOrigin = "https://aistudio.google.com"

const generateContentPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateContent"

var publicHeaderNames = []string{
	"x-goog-api-key",
	"x-goog-authuser",
	"x-user-agent",
	"x-aistudio-g1-tier",
	"x-aistudio-visit-id",
	"x-goog-ext-519733851-bin",
	"user-agent",
}

// Worker maintains a long-running Camoufox instance and WAA service for a single account.
type Worker struct {
	mu         sync.Mutex
	process    *browserProcess
	connection *websocket.Conn
	client     *bidiClient
	contextID  string
	state      State
	closed     bool
}

// Start launches an isolated Camoufox instance and performs a bootstrap with the official WAA service.
func Start(ctx context.Context, options Options) (*Worker, error) {
	state, err := loadStorageState(options.StorageStatePath)
	if err != nil {
		return nil, err
	}
	if options.Model == "" {
		return nil, errors.New("WAA bootstrap requires a chat model from live catalog")
	}
	if options.BootstrapPrompt == "" {
		options.BootstrapPrompt = fmt.Sprintf("AIStudio2API bootstrap %d", time.Now().UnixNano())
	}
	options.reportStartup(StartupPreparingBrowser)
	ffVersion := camoufoxFirefoxMajor
	fingerprint, err := loadAccountCamoufoxConfig(options.StorageStatePath, ffVersion, options.Locale, options.Timezone)
	if err != nil {
		return nil, err
	}
	options.reportStartup(StartupLaunchingBrowser)
	process, endpoint, err := launchBrowser(ctx, options, fingerprint)
	if err != nil {
		return nil, err
	}
	worker := &Worker{process: process}
	failed := true
	defer func() {
		if failed {
			_ = worker.abort()
		}
	}()
	options.reportStartup(StartupConnectingBiDi)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to Camoufox BiDi: %w", err)
	}
	worker.connection = connection
	worker.client = newBiDiClient(connection)
	if err := worker.bootstrap(ctx, options, state); err != nil {
		return nil, err
	}
	failed = false
	return worker, nil
}

func (worker *Worker) abort() error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	worker.closed = true
	connection := worker.connection
	process := worker.process
	worker.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
	return process.Close()
}

// ProtocolHeaders returns the seven common headers constructed by the official site for GenerateContent.
func (worker *Worker) ProtocolHeaders(ctx context.Context) (http.Header, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if worker.closed {
		return nil, errors.New("Camoufox runtime is closed")
	}
	return worker.state.Headers.Clone(), nil
}

// Proof synchronizes the prompt state on the official page and generates a fresh WAA proof for the SHA-256 digest.
func (worker *Worker) Proof(ctx context.Context, digest string, prompt string) (string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return "", errors.New("Camoufox runtime is closed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		value, err := worker.client.evaluateString(ctx, worker.contextID, fillPromptExpression(prompt))
		if err != nil {
			return "", fmt.Errorf("synchronizing official page prompt: %w", err)
		}
		if value == prompt {
			break
		}
		if time.Now().After(deadline) {
			return "", errors.New("official page prompt state not synchronized")
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			return "", err
		}
	}
	proof, err := worker.client.evaluateString(ctx, worker.contextID, takeProofExpression(digest))
	if err != nil {
		return "", fmt.Errorf("generating fresh WAA proof: %w", err)
	}
	if !strings.HasPrefix(proof, "!") {
		return "", errors.New("invalid fresh WAA proof prefix")
	}
	return proof, nil
}

// State returns an immutable copy of the runtime state.
func (worker *Worker) State() State {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	state := worker.state
	state.Headers = state.Headers.Clone()
	return state
}

// Close terminates the BiDi session and cleans up the Camoufox profile.
func (worker *Worker) Close() error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	client := worker.client
	connection := worker.connection
	process := worker.process
	worker.mu.Unlock()
	if client != nil && connection != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = client.command(closeCtx, "session.end", map[string]any{})
		cancel()
		_ = connection.Close()
	}
	if err := process.Close(); err != nil {
		return err
	}
	worker.mu.Lock()
	worker.closed = true
	worker.mu.Unlock()
	return nil
}

func (worker *Worker) bootstrap(ctx context.Context, options Options, storage storageState) error {
	client := worker.client
	if _, err := client.command(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		return err
	}
	tree, err := client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return err
	}
	contexts, _ := tree["contexts"].([]any)
	if len(contexts) == 0 {
		return errors.New("Camoufox BiDi did not return an initial tab")
	}
	root, _ := contexts[0].(map[string]any)
	contextID, _ := root["context"].(string)
	if contextID == "" {
		return errors.New("Camoufox BiDi initial tab is invalid")
	}
	worker.contextID = contextID
	if err := client.installLocalStorage(ctx, contextID, storage.Origins); err != nil {
		return err
	}
	if err := client.installCookies(ctx, storage.Cookies); err != nil {
		return err
	}
	options.reportStartup(StartupLoadingAIStudio)
	target := aiStudioOrigin + "/prompts/new_chat?model=" + url.QueryEscape(options.Model)
	if options.TemporaryChat {
		target += "&temporary=true"
	}
	if _, err := client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": contextID,
		"url":     target,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return fmt.Errorf("navigating to AI Studio: %w", err)
	}
	if err := client.waitFor(ctx, contextID, workerPageReadyExpression, 120*time.Second); err != nil {
		pageURL, _ := client.evaluateString(ctx, contextID, "location.href")
		return fmt.Errorf("AI Studio prompt textarea not ready url=%s: %w", pageURL, err)
	}
	pageURL, err := client.evaluateString(ctx, contextID, "location.href")
	if err != nil {
		return err
	}
	if strings.Contains(pageURL, "accounts.google.com") {
		return fmt.Errorf("isolated login state expired url=%s", pageURL)
	}
	if err := dismissKnownOverlays(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupLocatingWAA)
	snapshotKey, err := client.waitSnapshotFunction(ctx, contextID, 30*time.Second)
	if err != nil {
		return err
	}
	if err := installBootstrapRequestCapture(ctx, client, contextID); err != nil {
		return err
	}
	options.reportStartup(StartupBootstrappingWAA)
	filled, err := client.evaluateString(ctx, contextID, fillPromptExpression(options.BootstrapPrompt))
	if err != nil || filled != options.BootstrapPrompt {
		return fmt.Errorf("failed to fill bootstrap prompt value=%q err=%v", filled, err)
	}
	if _, err := client.command(ctx, "session.subscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent"},
		"contexts": []string{contextID},
	}); err != nil {
		return err
	}
	intercept, err := client.command(ctx, "network.addIntercept", map[string]any{
		"phases":   []string{"beforeRequestSent"},
		"contexts": []string{contextID},
		"urlPatterns": []map[string]any{{
			"type":     "pattern",
			"protocol": "https",
			"hostname": "alkalimakersuite-pa.clients6.google.com",
			"pathname": generateContentPath,
		}},
	})
	if err != nil {
		return fmt.Errorf("installing GenerateContent intercept: %w", err)
	}
	interceptID, _ := intercept["intercept"].(string)
	if interceptID == "" {
		return errors.New("invalid GenerateContent intercept ID")
	}
	if _, err := client.evaluate(ctx, contextID, submitPromptExpression); err != nil {
		return fmt.Errorf("submitting prompt on official page: %w", err)
	}
	if err := client.waitFor(ctx, contextID, "Boolean(window.__aistudioWaaService)", 60*time.Second); err != nil {
		return fmt.Errorf("official WAA service not exposed: %w", err)
	}
	requestID, err := client.waitBlockedGenerateRequest(ctx, contextID, 60*time.Second)
	if err != nil {
		return err
	}
	if _, err := client.command(ctx, "network.failRequest", map[string]any{"request": requestID}); err != nil {
		return fmt.Errorf("aborting bootstrap GenerateContent: %w", err)
	}
	if _, err := client.command(ctx, "network.removeIntercept", map[string]any{"intercept": interceptID}); err != nil {
		return fmt.Errorf("removing GenerateContent intercept: %w", err)
	}
	actualModel, err := capturedBootstrapModel(ctx, client, contextID)
	if err != nil {
		return err
	}
	if actualModel != strings.TrimPrefix(options.Model, "models/") {
		return fmt.Errorf("official page initialization model mismatch expected=%s actual=%s", options.Model, actualModel)
	}
	restored, err := client.evaluateBool(ctx, contextID, `(() => {
  if (typeof window.__aistudioRestoreBootstrapCapture !== 'function') return false;
  window.__aistudioRestoreBootstrapCapture();
  delete window.__aistudioRestoreBootstrapCapture;
  return true;
})()`)
	if err != nil {
		return fmt.Errorf("removing bootstrap request capture: %w", err)
	}
	if !restored {
		return errors.New("bootstrap request capture was not installed")
	}
	headers := make(http.Header, len(publicHeaderNames))
	for _, name := range publicHeaderNames {
		value := client.generateHeaders[name]
		if value != "" {
			headers.Set(name, value)
		}
	}
	for _, name := range []string{"user-agent", "x-goog-api-key", "x-goog-authuser", "x-user-agent"} {
		if headers.Get(name) == "" {
			return fmt.Errorf("official GenerateContent missing required header %s", name)
		}
	}
	if _, err := client.command(ctx, "session.unsubscribe", map[string]any{
		"events":   []string{"network.beforeRequestSent"},
		"contexts": []string{contextID},
	}); err != nil {
		return fmt.Errorf("stopping GenerateContent network event subscription: %w", err)
	}
	userAgent, _ := client.evaluateString(ctx, contextID, "navigator.userAgent")
	platform, _ := client.evaluateString(ctx, contextID, "navigator.platform")
	timezone, _ := client.evaluateString(ctx, contextID, "Intl.DateTimeFormat().resolvedOptions().timeZone")
	worker.state = State{
		PID:         worker.process.command.Process.Pid,
		PageURL:     pageURL,
		UserAgent:   userAgent,
		Platform:    platform,
		Timezone:    timezone,
		SnapshotKey: snapshotKey,
		Headers:     headers,
	}
	return nil
}

func installBootstrapRequestCapture(ctx context.Context, client *bidiClient, contextID string) error {
	encodedPath, err := json.Marshal(generateContentPath)
	if err != nil {
		return err
	}
	expression := fmt.Sprintf(`(() => {
  const targetPath = %s;
  const matches = (input) => {
    const raw = typeof input === 'string' ? input : input?.url;
    if (!raw) return false;
    try { return new URL(raw, location.href).pathname === targetPath; } catch { return false; }
  };
  const record = (body) => {
    if (typeof body === 'string') {
      window.__aistudioBootstrapRequestBody = body;
      return;
    }
    if (body instanceof Blob) {
      body.text().then(record);
      return;
    }
    if (body instanceof ArrayBuffer) {
      record(new TextDecoder().decode(body));
      return;
    }
    if (ArrayBuffer.isView(body)) record(new TextDecoder().decode(body));
  };
  const originalFetch = window.fetch;
  const fetchWrapper = function(input, init) {
    if (matches(input)) {
      const body = init?.body;
      if (body !== undefined) record(body);
      else if (input instanceof Request) input.clone().text().then(record);
    }
    return originalFetch.apply(this, arguments);
  };
  const originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send;
  const requestURLs = new WeakMap();
  const openWrapper = function(method, url) {
    requestURLs.set(this, String(url));
    return originalOpen.apply(this, arguments);
  };
  const sendWrapper = function(body) {
    if (matches(requestURLs.get(this))) record(body);
    return originalSend.apply(this, arguments);
  };
  window.fetch = fetchWrapper;
  XMLHttpRequest.prototype.open = openWrapper;
  XMLHttpRequest.prototype.send = sendWrapper;
  window.__aistudioRestoreBootstrapCapture = () => {
    if (window.fetch === fetchWrapper) window.fetch = originalFetch;
    if (XMLHttpRequest.prototype.open === openWrapper) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === sendWrapper) XMLHttpRequest.prototype.send = originalSend;
  };
  return true;
})()`, encodedPath)
	installed, err := client.evaluateBool(ctx, contextID, expression)
	if err != nil {
		return fmt.Errorf("installing bootstrap request capture: %w", err)
	}
	if !installed {
		return errors.New("failed to install bootstrap request capture")
	}
	return nil
}

func capturedBootstrapModel(ctx context.Context, client *bidiClient, contextID string) (string, error) {
	if err := client.waitFor(ctx, contextID, "typeof window.__aistudioBootstrapRequestBody === 'string'", 5*time.Second); err != nil {
		return "", fmt.Errorf("official bootstrap request body not captured: %w", err)
	}
	body, err := client.evaluateString(ctx, contextID, `(() => {
  window.__aistudioRestoreBootstrapCapture?.();
  return window.__aistudioBootstrapRequestBody;
})()`)
	if err != nil {
		return "", err
	}
	var wire []any
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		return "", fmt.Errorf("parsing official bootstrap request body: %w", err)
	}
	if len(wire) == 0 {
		return "", errors.New("official bootstrap request missing model")
	}
	model, _ := wire[0].(string)
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return "", errors.New("official bootstrap request model is invalid")
	}
	return model, nil
}

func loadStorageState(path string) (storageState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storageState{}, fmt.Errorf("reading storage state: %w", err)
	}
	var state storageState
	if err := json.Unmarshal(data, &state); err != nil {
		return storageState{}, fmt.Errorf("parsing storage state: %w", err)
	}
	if len(state.Cookies) == 0 {
		return storageState{}, errors.New("storage state contains no cookies")
	}
	return state, nil
}
