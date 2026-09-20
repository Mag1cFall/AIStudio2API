package chromeauth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const aiStudioChatURL = "https://aistudio.google.com/prompts/new_chat"

// Verification holds acceptance results for the AI Studio login page and model catalog.
type Verification struct {
	ModelCount int
}

// Verify checks whether an account can access the WAA page and retrieve the real-time model catalog.
func Verify(ctx context.Context, state *aistudio.StorageState, proxy string) (Verification, error) {
	if state == nil {
		return Verification{}, fmt.Errorf("storage state is nil")
	}
	if _, err := aistudio.NewSigner().Sign(*state); err != nil {
		return Verification{}, err
	}
	client, err := aistudio.NewProxyHTTPClient(proxy)
	if err != nil {
		return Verification{}, err
	}
	defer client.CloseIdleConnections()
	headers, err := aistudio.DiscoverPublicHeaders(ctx, client)
	if err != nil {
		return Verification{}, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if err := verifyChatPage(ctx, client, state, headers.Get("User-Agent")); err != nil {
		return Verification{}, err
	}
	models, err := verifyModels(ctx, client, state, headers)
	if err != nil {
		return Verification{}, err
	}
	return Verification{ModelCount: models}, nil
}

func verifyChatPage(ctx context.Context, client *http.Client, state *aistudio.StorageState, userAgent string) error {
	cookie, err := state.CookieHeader(aiStudioChatURL, time.Now())
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aiStudioChatURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Cookie", cookie)
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("access AI Studio login page: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("AI Studio login page returned HTTP %d", response.StatusCode)
	}
	return mergeResponseCookies(state, response, aiStudioChatURL)
}

func verifyModels(ctx context.Context, client *http.Client, state *aistudio.StorageState, headers http.Header) (int, error) {
	url := aistudio.MakerSuiteRPCBase + "ListModels"
	cookie, err := state.CookieHeader(url, time.Now())
	if err != nil {
		return 0, err
	}
	authorization, err := aistudio.NewSigner().Authorization(*state)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("[]")))
	if err != nil {
		return 0, err
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", aistudio.JSONProtobufContentType)
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Origin", "https://aistudio.google.com")
	request.Header.Set("Referer", "https://aistudio.google.com/")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-site")
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("read AI Studio model catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return 0, fmt.Errorf("AI Studio ListModels returned HTTP %d", response.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), aistudio.JSONProtobufContentType) {
		return 0, fmt.Errorf("AI Studio ListModels returned unrecognized Content-Type %q", response.Header.Get("Content-Type"))
	}
	models, err := aistudio.ParseModels(response.Body)
	if err != nil {
		return 0, err
	}
	if len(models) == 0 {
		return 0, fmt.Errorf("AI Studio ListModels returned empty catalog")
	}
	if err := mergeResponseCookies(state, response, url); err != nil {
		return 0, err
	}
	return len(models), nil
}

func mergeResponseCookies(state *aistudio.StorageState, response *http.Response, sourceURL string) error {
	setCookies := response.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		return nil
	}
	return state.MergeSetCookieHeaders(setCookies, sourceURL, time.Now())
}
