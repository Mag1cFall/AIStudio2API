package aistudio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const publicDiscoveryUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:152.0) Gecko/20100101 Firefox/152.0"

var makerSuiteAPIKeyPattern = regexp.MustCompile(`"WIu0Nc":"([^"]+)"`)

// ProtocolHeaderProvider provides dynamic common headers discovered by official runtimes per account
type ProtocolHeaderProvider interface {
	ProtocolHeaders(context.Context, string) (http.Header, error)
}

// ProtocolHeaderProviderFunc adapts a function to ProtocolHeaderProvider
type ProtocolHeaderProviderFunc func(context.Context, string) (http.Header, error)

// ProtocolHeaders invokes the dynamic common headers function
func (f ProtocolHeaderProviderFunc) ProtocolHeaders(ctx context.Context, accountID string) (http.Header, error) {
	return f(ctx, accountID)
}

// HTTPTransportOptions defines account and network dependencies for standard MakerSuite RPC
type HTTPTransportOptions struct {
	Pool        *AccountPool
	Signer      *Signer
	Headers     ProtocolHeaderProvider
	GlobalProxy string
}

// MakerSuiteHTTPTransport sends standard RPC via account fixed egress
type MakerSuiteHTTPTransport struct {
	pool        *AccountPool
	signer      *Signer
	headers     ProtocolHeaderProvider
	globalProxy string
	now         func() time.Time
	clientsMu   sync.Mutex
	clients     map[string]*http.Client
}

type accountLeaseContextKey struct{}
type accountSelectionObserverContextKey struct{}

// ContextWithAccountLease passes an existing account lease to protocol transport
func ContextWithAccountLease(ctx context.Context, lease *AccountLease) context.Context {
	return context.WithValue(ctx, accountLeaseContextKey{}, lease)
}

// AccountLeaseFromContext returns the unique account lease for the current request
func AccountLeaseFromContext(ctx context.Context) (*AccountLease, bool) {
	lease, ok := ctx.Value(accountLeaseContextKey{}).(*AccountLease)
	return lease, ok && lease != nil && lease.Account() != nil
}

// ContextWithAccountSelectionObserver observes the account finally selected for the request
func ContextWithAccountSelectionObserver(ctx context.Context, observer func(*Account)) context.Context {
	return context.WithValue(ctx, accountSelectionObserverContextKey{}, observer)
}

func observeAccountSelection(ctx context.Context, account *Account) {
	observer, ok := ctx.Value(accountSelectionObserverContextKey{}).(func(*Account))
	if ok && observer != nil {
		observer(account)
	}
}

// NewMakerSuiteHTTPTransport creates standard MakerSuite RPC transport
func NewMakerSuiteHTTPTransport(options HTTPTransportOptions) (*MakerSuiteHTTPTransport, error) {
	if options.Pool == nil {
		return nil, fmt.Errorf("AI Studio account pool cannot be nil")
	}
	if options.Headers == nil {
		return nil, fmt.Errorf("AI Studio protocol header provider cannot be nil")
	}
	signer := options.Signer
	if signer == nil {
		signer = NewSigner()
	}
	transport := &MakerSuiteHTTPTransport{
		pool:        options.Pool,
		signer:      signer,
		headers:     options.Headers,
		globalProxy: strings.TrimSpace(options.GlobalProxy),
		now:         time.Now,
		clients:     make(map[string]*http.Client),
	}
	if _, err := transport.clientForProxy(transport.globalProxy); err != nil {
		return nil, err
	}
	return transport, nil
}

// DiscoverPublicHeaders reads public headers needed for standard RPC from the AI Studio home page
func DiscoverPublicHeaders(ctx context.Context, client *http.Client) (http.Header, error) {
	if client == nil {
		return nil, fmt.Errorf("HTTP client cannot be nil")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aiStudioOrigin+"/", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("User-Agent", publicDiscoveryUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read AI Studio home page: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AI Studio home page returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read AI Studio home page body: %w", err)
	}
	match := makerSuiteAPIKeyPattern.FindSubmatch(body)
	if len(match) != 2 || len(match[1]) == 0 {
		return nil, fmt.Errorf("AI Studio home page missing WIu0Nc")
	}
	visitID, err := newVisitID()
	if err != nil {
		return nil, err
	}
	return http.Header{
		"User-Agent":          []string{publicDiscoveryUserAgent},
		"X-Aistudio-Visit-Id": []string{visitID},
		"X-Goog-Api-Key":      []string{string(match[1])},
		"X-Goog-Authuser":     []string{"0"},
		"X-User-Agent":        []string{"grpc-web-javascript/0.1"},
	}, nil
}

func newVisitID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate AI Studio visit ID: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	uuid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16],
	)
	return "v1_" + base64.StdEncoding.EncodeToString([]byte(uuid)), nil
}

// NewProxyHTTPClient creates standard HTTP, HTTPS, or SOCKS5 fixed egress client
func NewProxyHTTPClient(proxyURL string) (*http.Client, error) {
	roundTripper, err := newBrowserRoundTripper(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: roundTripper}, nil
}

// Do sends standard MakerSuite RPC and keeps the lease held by the response body
func (t *MakerSuiteHTTPTransport) Do(ctx context.Context, rpc RPCRequest) (*RPCResponse, error) {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: rpc.AccountID})
	if err != nil {
		return nil, err
	}
	releaseOnError := func(requestErr error) error {
		if !owned {
			return requestErr
		}
		return errors.Join(requestErr, lease.Release())
	}
	account := lease.Account()
	_, headers, err := prepareProtocolHeaders(ctx, lease, rpc, t.signer, t.headers, t.now(), true)
	if err != nil {
		return nil, releaseOnError(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc.URL, bytes.NewReader(rpc.Body))
	if err != nil {
		return nil, releaseOnError(fmt.Errorf("create MakerSuite %s request: %w", rpc.Method, err))
	}
	request.Header = headers
	client, err := t.clientForProxy(account.EffectiveProxy(t.globalProxy))
	if err != nil {
		return nil, releaseOnError(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, releaseOnError(fmt.Errorf("execute MakerSuite %s request: %w", rpc.Method, err))
	}
	setCookies := append([]string(nil), response.Header.Values("Set-Cookie")...)
	if len(setCookies) > 0 {
		if err := lease.MergeSetCookieHeaders(setCookies, rpc.URL, t.now()); err != nil {
			_ = response.Body.Close()
			return nil, releaseOnError(fmt.Errorf("merge MakerSuite %s response cookies: %w", rpc.Method, err))
		}
	}
	body := &leaseResponseBody{
		body: response.Body,
		finish: func() error {
			var finishErr error
			if owned {
				finishErr = errors.Join(finishErr, lease.Release())
			}
			return finishErr
		},
	}
	return &RPCResponse{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: body}, nil
}

// CloseIdleConnections closes all idle connections for fixed egresses
func (t *MakerSuiteHTTPTransport) CloseIdleConnections() {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	for _, client := range t.clients {
		client.CloseIdleConnections()
	}
}

func (t *MakerSuiteHTTPTransport) clientForProxy(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	if client := t.clients[proxyURL]; client != nil {
		return client, nil
	}
	client, err := NewProxyHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	t.clients[proxyURL] = client
	return client, nil
}

func prepareProtocolHeaders(
	ctx context.Context,
	lease *AccountLease,
	rpc RPCRequest,
	signer *Signer,
	provider ProtocolHeaderProvider,
	now time.Time,
	includeCookie bool,
) (StorageState, http.Header, error) {
	state, err := lease.ReloadStorageState()
	if err != nil {
		return StorageState{}, nil, fmt.Errorf("read account storage state: %w", err)
	}
	account := lease.Account()
	publicHeaders, err := provider.ProtocolHeaders(ctx, account.ID)
	if err != nil {
		return StorageState{}, nil, fmt.Errorf("read account dynamic headers: %w", err)
	}
	if publicHeaders == nil {
		return StorageState{}, nil, fmt.Errorf("account dynamic headers are empty")
	}
	headers := publicHeaders.Clone()
	for name, values := range rpc.Header {
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	for _, name := range []string{"User-Agent", "X-Goog-Api-Key", "X-Goog-Authuser", "X-User-Agent"} {
		if strings.TrimSpace(headers.Get(name)) == "" {
			return StorageState{}, nil, fmt.Errorf("account dynamic headers missing %s", name)
		}
	}
	authorization, err := signer.Authorization(state)
	if err != nil {
		return StorageState{}, nil, err
	}
	headers.Set("Authorization", authorization)
	headers.Set("Accept", "*/*")
	headers.Set("Origin", aiStudioOrigin)
	headers.Set("Referer", aiStudioOrigin+"/")
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-site")
	if language := account.AcceptLanguage(); language != "" {
		headers.Set("Accept-Language", language)
	}
	if includeCookie {
		cookie, err := state.CookieHeader(rpc.URL, now)
		if err != nil {
			return StorageState{}, nil, err
		}
		headers.Set("Cookie", cookie)
	}
	return state, headers, nil
}

func resolveAccountLease(ctx context.Context, pool *AccountPool, selection AccountSelection) (*AccountLease, bool, error) {
	if lease, ok := AccountLeaseFromContext(ctx); ok {
		if err := validateLeaseSelection(lease, selection); err != nil {
			return nil, false, err
		}
		observeAccountSelection(ctx, lease.Account())
		return lease, false, nil
	}
	lease, err := pool.AcquireFor(ctx, selection)
	if err != nil {
		return nil, false, err
	}
	observeAccountSelection(ctx, lease.Account())
	return lease, true, nil
}

func validateLeaseSelection(lease *AccountLease, selection AccountSelection) error {
	if lease == nil || lease.pool == nil || lease.account == nil {
		return fmt.Errorf("context account lease is not initialized")
	}
	lease.pool.mu.Lock()
	defer lease.pool.mu.Unlock()
	account := lease.Account()
	if lease.pool.byID[account.ID] != account {
		return fmt.Errorf("context leased account not found: %s", account.ID)
	}
	if accountID := strings.TrimSpace(selection.AccountID); accountID != "" && account.ID != accountID {
		return fmt.Errorf("context leased account %s does not match requested account %s", account.ID, accountID)
	}
	if selection.AllowedAccountIDs != nil {
		allowed := false
		for _, accountID := range selection.AllowedAccountIDs {
			if strings.TrimSpace(accountID) == account.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrNoEligibleAccount
		}
	}
	if resourceID := strings.TrimSpace(selection.ResourceID); resourceID != "" {
		owner, exists := lease.pool.resources[resourceID]
		if !exists {
			return ErrResourceNotFound
		}
		if owner != account.ID {
			return fmt.Errorf("resource %s is bound to account %s", resourceID, owner)
		}
	}
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	if modelID != "" && !account.SupportsModel(modelID) {
		return fmt.Errorf("context leased account %s does not support model %s", account.ID, modelID)
	}
	if selection.Method != "" && !account.SupportsMethod(modelID, selection.Method) {
		return fmt.Errorf("context leased account %s does not support method %s", account.ID, selection.Method)
	}
	capability := strings.TrimSpace(selection.Capability)
	if capability != "" {
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && model.Capabilities[capability] {
				return nil
			}
		}
		return fmt.Errorf("context leased account %s does not support capability %s", account.ID, capability)
	}
	return nil
}

type leaseResponseBody struct {
	body      io.ReadCloser
	finish    func() error
	once      sync.Once
	finishErr error
}

func (b *leaseResponseBody) Read(destination []byte) (int, error) {
	count, err := b.body.Read(destination)
	if err == io.EOF {
		b.finalize()
		if b.finishErr != nil {
			return count, errors.Join(err, b.finishErr)
		}
	}
	return count, err
}

func (b *leaseResponseBody) Close() error {
	closeErr := b.body.Close()
	b.finalize()
	return errors.Join(closeErr, b.finishErr)
}

func (b *leaseResponseBody) finalize() {
	b.once.Do(func() {
		b.finishErr = b.finish()
	})
}
