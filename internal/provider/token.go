package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	rhSSOTokenURL = "https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token"
	rhSSOClientID = "cloud-services"
)

// TokenManager exchanges an offline token for a bearer token and refreshes it
// automatically before it expires. Safe for concurrent use.
type TokenManager struct {
	offlineToken string
	ssoURL       string

	mu          sync.Mutex
	bearerToken string
	expiresAt   time.Time
}

func NewTokenManager(offlineToken string) *TokenManager {
	return &TokenManager{
		offlineToken: offlineToken,
		ssoURL:       rhSSOTokenURL,
	}
}

// Token returns a valid bearer token, refreshing if necessary.
func (m *TokenManager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Refresh if expired or expiring within 60 seconds
	if time.Now().Add(60 * time.Second).Before(m.expiresAt) {
		return m.bearerToken, nil
	}

	return m.refresh(ctx)
}

func (m *TokenManager) refresh(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {rhSSOClientID},
		"refresh_token": {m.offlineToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.ssoURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchanging offline token: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	if body.Error != "" {
		return "", fmt.Errorf("token exchange failed: %s — %s", body.Error, body.ErrorDesc)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned empty access_token (status %d)", resp.StatusCode)
	}

	m.bearerToken = body.AccessToken
	m.expiresAt = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return m.bearerToken, nil
}
