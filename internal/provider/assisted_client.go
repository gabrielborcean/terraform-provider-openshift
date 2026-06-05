package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AssistedClient is a thin HTTP client for the Assisted Installer (assisted-service) REST API.
type AssistedClient struct {
	BaseURL      string
	Token        string        // static bearer token
	TokenManager *TokenManager // dynamic token (offline_token flow); takes precedence
	HTTPClient   *http.Client
	PullSecret   string
}

// NewAssistedClient creates a new AssistedClient with a static bearer token.
func NewAssistedClient(baseURL, token, pullSecret string) *AssistedClient {
	return &AssistedClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		PullSecret: pullSecret,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewAssistedClientWithManager creates a new AssistedClient that refreshes
// tokens automatically via the provided TokenManager.
func NewAssistedClientWithManager(baseURL string, tm *TokenManager, pullSecret string) *AssistedClient {
	return &AssistedClient{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		TokenManager: tm,
		PullSecret:   pullSecret,
		HTTPClient:   &http.Client{Timeout: 60 * time.Second},
	}
}

// resolveToken returns the current bearer token, refreshing if needed.
func (c *AssistedClient) resolveToken(ctx context.Context) (string, error) {
	if c.TokenManager != nil {
		return c.TokenManager.Token(ctx)
	}
	return c.Token, nil
}

// ---- types ------------------------------------------------------------------

type CreateClusterParams struct {
	Name                  string
	OpenshiftVersion      string
	BaseDNSDomain         string
	APIVIP                string
	IngressVIP            string
	NetworkType           string
	ClusterNetworkCIDR    string
	ServiceNetworkCIDR    string
	MachineNetworkCIDR    string
	SSHPublicKey          string
	PullSecret            string
	CPUReplicas           int64
	WorkerReplicas        int64
	AdditionalTrustBundle string
	HTTPProxy             string
	HTTPSProxy            string
	NoProxy               string
	ImageContentSources   []ImageContentSource
}

type UpdateClusterParams struct {
	APIVIP       *string
	IngressVIP   *string
	SSHPublicKey *string
}

type Cluster struct {
	ID               string
	Name             string
	Status           string
	StatusInfo       string
	APIURL           string
	ConsoleURL       string
	OpenshiftVersion string
	CreatedAt        time.Time
}

type CreateInfraEnvParams struct {
	Name                  string
	ClusterID             string
	SSHPublicKey          string
	PullSecret            string
	ImageType             string
	OpenshiftVersion      string
	Proxy                 *AssistedProxyConfig
	AdditionalTrustBundle string
}

type InfraEnv struct {
	ID          string
	Name        string
	DownloadURL string
	ExpiresAt   time.Time
}

type Host struct {
	ID                string
	InfraEnvID        string
	Status            string
	Role              string
	Hostname          string
	RequestedHostname string
}

type UpdateHostParams struct {
	HostName string
	Role     string
}

type Credentials struct {
	Username   string
	Password   string
	ConsoleURL string
}

type ImageContentSource struct {
	Source  string
	Mirrors []string
}

type AssistedProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// ---- API error --------------------------------------------------------------

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// assisted-service also uses "reason" and HTTP status in some responses
	Reason     string `json:"reason"`
	StatusCode int    `json:"-"`
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Reason
	}
	if e.Code != "" {
		return fmt.Sprintf("assisted-service error %s (HTTP %d): %s", e.Code, e.StatusCode, msg)
	}
	return fmt.Sprintf("assisted-service HTTP %d: %s", e.StatusCode, msg)
}

// ---- internal helpers -------------------------------------------------------

func (c *AssistedClient) url(path string) string {
	return c.BaseURL + "/api/assisted-install/v2" + path
}

func (c *AssistedClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshalling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.url(path), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok, err := c.resolveToken(ctx); err != nil {
		return nil, fmt.Errorf("resolving auth token: %w", err)
	} else if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request %s %s: %w", method, path, err)
	}
	return resp, nil
}

func checkResponse(resp *http.Response, expectedCodes ...int) error {
	for _, code := range expectedCodes {
		if resp.StatusCode == code {
			return nil
		}
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err == nil && (apiErr.Code != "" || apiErr.Message != "") {
		apiErr.StatusCode = resp.StatusCode
		return &apiErr
	}
	return &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

func decodeJSON(resp *http.Response, v interface{}) error {
	defer resp.Body.Close() //nolint:errcheck
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// ---- wire types (JSON) ------------------------------------------------------

type wireCluster struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	StatusInfo       string `json:"status_info"`
	APIVIPDNSName    string `json:"api_vip_dns_name"`
	ConsoleURL       string `json:"console_url"`
	OpenshiftVersion string `json:"openshift_version"`
	CreatedAt        string `json:"created_at"`
	// populated by GET /clusters/{id}/credentials
}

func wireToCluster(w wireCluster) Cluster {
	var createdAt time.Time
	if w.CreatedAt != "" {
		createdAt, _ = time.Parse(time.RFC3339, w.CreatedAt)
	}
	apiURL := ""
	if w.APIVIPDNSName != "" {
		apiURL = "https://" + w.APIVIPDNSName + ":6443"
	}
	return Cluster{
		ID:               w.ID,
		Name:             w.Name,
		Status:           w.Status,
		StatusInfo:       w.StatusInfo,
		APIURL:           apiURL,
		ConsoleURL:       w.ConsoleURL,
		OpenshiftVersion: w.OpenshiftVersion,
		CreatedAt:        createdAt,
	}
}

type wireCreateCluster struct {
	Name             string `json:"name"`
	OpenshiftVersion string `json:"openshift_version"`
	BaseDNSDomain    string `json:"base_dns_domain"`
	// VIPs
	APIVips     []wireVIP `json:"api_vips,omitempty"`
	IngressVips []wireVIP `json:"ingress_vips,omitempty"`
	// Networking
	NetworkType        string              `json:"network_type,omitempty"`
	ClusterNetworks    []wireClusterNet    `json:"cluster_networks,omitempty"`
	ServiceNetworks    []wireServiceNet    `json:"service_networks,omitempty"`
	MachineNetworks    []wireMachineNet    `json:"machine_networks,omitempty"`
	SSHPublicKey       string              `json:"ssh_public_key,omitempty"`
	PullSecret         string              `json:"pull_secret"`
	ControlPlaneCount  int64               `json:"high_availability_mode,omitempty"`
	AdditionalTrustBundlePolicy string     `json:"additional_trust_bundle_policy,omitempty"`
	AdditionalTrustBundle string           `json:"additional_trust_bundle,omitempty"`
	HTTPProxy          string              `json:"http_proxy,omitempty"`
	HTTPSProxy         string              `json:"https_proxy,omitempty"`
	NoProxy            string              `json:"no_proxy,omitempty"`
	ImageContentSources []wireICS          `json:"image_content_sources,omitempty"`
}

type wireVIP struct {
	IP string `json:"ip"`
}
type wireClusterNet struct {
	CIDR       string `json:"cidr"`
	HostPrefix int    `json:"host_prefix"`
}
type wireServiceNet struct {
	CIDR string `json:"cidr"`
}
type wireMachineNet struct {
	CIDR string `json:"cidr"`
}
type wireICS struct {
	Source  string   `json:"source"`
	Mirrors []string `json:"mirrors"`
}

type wireInfraEnv struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	ExpiresAt   string `json:"expires_at"`
}

func wireToInfraEnv(w wireInfraEnv) InfraEnv {
	var exp time.Time
	if w.ExpiresAt != "" {
		exp, _ = time.Parse(time.RFC3339, w.ExpiresAt)
	}
	return InfraEnv{
		ID:          w.ID,
		Name:        w.Name,
		DownloadURL: w.DownloadURL,
		ExpiresAt:   exp,
	}
}

type wireCreateInfraEnv struct {
	Name             string     `json:"name"`
	ClusterID        string     `json:"cluster_id"`
	SSHAuthorizedKey string     `json:"ssh_authorized_key,omitempty"`
	PullSecret       string     `json:"pull_secret"`
	ImageType        string     `json:"image_type,omitempty"`
	OpenshiftVersion string     `json:"openshift_version,omitempty"`
	Proxy            *wireProxy `json:"proxy,omitempty"`
	AdditionalTrustBundle string `json:"additional_trust_bundle,omitempty"`
}

type wireProxy struct {
	HTTPProxy  string `json:"http_proxy,omitempty"`
	HTTPSProxy string `json:"https_proxy,omitempty"`
	NoProxy    string `json:"no_proxy,omitempty"`
}

type wireHost struct {
	ID                string `json:"id"`
	InfraEnvID        string `json:"infra_env_id"`
	Status            string `json:"status"`
	Role              string `json:"role"`
	RequestedHostname string `json:"requested_hostname"`
}

type wireCredentials struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	ConsoleURL string `json:"console_url"`
}

// ---- Cluster operations -----------------------------------------------------

func (c *AssistedClient) CreateCluster(ctx context.Context, params CreateClusterParams) (*Cluster, error) {
	payload := wireCreateCluster{
		Name:             params.Name,
		OpenshiftVersion: params.OpenshiftVersion,
		BaseDNSDomain:    params.BaseDNSDomain,
		SSHPublicKey:     params.SSHPublicKey,
		PullSecret:       params.PullSecret,
		NetworkType:      params.NetworkType,
		HTTPProxy:        params.HTTPProxy,
		HTTPSProxy:       params.HTTPSProxy,
		NoProxy:          params.NoProxy,
		AdditionalTrustBundle: params.AdditionalTrustBundle,
	}
	if params.APIVIP != "" {
		payload.APIVips = []wireVIP{{IP: params.APIVIP}}
	}
	if params.IngressVIP != "" {
		payload.IngressVips = []wireVIP{{IP: params.IngressVIP}}
	}
	if params.ClusterNetworkCIDR != "" {
		payload.ClusterNetworks = []wireClusterNet{{CIDR: params.ClusterNetworkCIDR, HostPrefix: 23}}
	}
	if params.ServiceNetworkCIDR != "" {
		payload.ServiceNetworks = []wireServiceNet{{CIDR: params.ServiceNetworkCIDR}}
	}
	if params.MachineNetworkCIDR != "" {
		payload.MachineNetworks = []wireMachineNet{{CIDR: params.MachineNetworkCIDR}}
	}
	if len(params.ImageContentSources) > 0 {
		for _, ics := range params.ImageContentSources {
			payload.ImageContentSources = append(payload.ImageContentSources, wireICS{Source: ics.Source, Mirrors: ics.Mirrors})
		}
	}
	if params.AdditionalTrustBundle != "" {
		payload.AdditionalTrustBundlePolicy = "add-to-all-nodes"
	}

	resp, err := c.do(ctx, http.MethodPost, "/clusters", payload)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusCreated, http.StatusOK); err != nil {
		return nil, fmt.Errorf("CreateCluster: %w", err)
	}
	var w wireCluster
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	cl := wireToCluster(w)
	return &cl, nil
}

func (c *AssistedClient) GetCluster(ctx context.Context, clusterID string) (*Cluster, error) {
	resp, err := c.do(ctx, http.MethodGet, "/clusters/"+clusterID, nil)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusOK); err != nil {
		return nil, fmt.Errorf("GetCluster: %w", err)
	}
	var w wireCluster
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	cl := wireToCluster(w)
	return &cl, nil
}

func (c *AssistedClient) UpdateCluster(ctx context.Context, clusterID string, params UpdateClusterParams) (*Cluster, error) {
	payload := map[string]interface{}{}
	if params.APIVIP != nil {
		payload["api_vips"] = []wireVIP{{IP: *params.APIVIP}}
	}
	if params.IngressVIP != nil {
		payload["ingress_vips"] = []wireVIP{{IP: *params.IngressVIP}}
	}
	if params.SSHPublicKey != nil {
		payload["ssh_public_key"] = *params.SSHPublicKey
	}

	resp, err := c.do(ctx, http.MethodPatch, "/clusters/"+clusterID, payload)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusCreated, http.StatusOK); err != nil {
		return nil, fmt.Errorf("UpdateCluster: %w", err)
	}
	var w wireCluster
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	cl := wireToCluster(w)
	return &cl, nil
}

func (c *AssistedClient) DeleteCluster(ctx context.Context, clusterID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/clusters/"+clusterID, nil)
	if err != nil {
		return err
	}
	if err := checkResponse(resp, http.StatusNoContent, http.StatusOK); err != nil {
		return fmt.Errorf("DeleteCluster: %w", err)
	}
	resp.Body.Close() //nolint:errcheck
	return nil
}

func (c *AssistedClient) InstallCluster(ctx context.Context, clusterID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/clusters/"+clusterID+"/actions/install", nil)
	if err != nil {
		return err
	}
	if err := checkResponse(resp, http.StatusAccepted, http.StatusOK); err != nil {
		return fmt.Errorf("InstallCluster: %w", err)
	}
	resp.Body.Close() //nolint:errcheck
	return nil
}

func (c *AssistedClient) GetCredentials(ctx context.Context, clusterID string) (*Credentials, error) {
	resp, err := c.do(ctx, http.MethodGet, "/clusters/"+clusterID+"/credentials", nil)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusOK); err != nil {
		return nil, fmt.Errorf("GetCredentials: %w", err)
	}
	var w wireCredentials
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	return &Credentials{Username: w.Username, Password: w.Password, ConsoleURL: w.ConsoleURL}, nil
}

// ---- Infra-env operations ---------------------------------------------------

func (c *AssistedClient) CreateInfraEnv(ctx context.Context, params CreateInfraEnvParams) (*InfraEnv, error) {
	pullSecret := params.PullSecret
	if pullSecret == "" {
		pullSecret = c.PullSecret
	}
	payload := wireCreateInfraEnv{
		Name:             params.Name,
		ClusterID:        params.ClusterID,
		SSHAuthorizedKey: params.SSHPublicKey,
		PullSecret:       pullSecret,
		ImageType:        params.ImageType,
		OpenshiftVersion: params.OpenshiftVersion,
		AdditionalTrustBundle: params.AdditionalTrustBundle,
	}
	if params.Proxy != nil {
		payload.Proxy = &wireProxy{
			HTTPProxy:  params.Proxy.HTTPProxy,
			HTTPSProxy: params.Proxy.HTTPSProxy,
			NoProxy:    params.Proxy.NoProxy,
		}
	}


	resp, err := c.do(ctx, http.MethodPost, "/infra-envs", payload)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusCreated, http.StatusOK); err != nil {
		return nil, fmt.Errorf("CreateInfraEnv: %w", err)
	}
	var w wireInfraEnv
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	ie := wireToInfraEnv(w)
	return &ie, nil
}

func (c *AssistedClient) GetInfraEnv(ctx context.Context, infraEnvID string) (*InfraEnv, error) {
	resp, err := c.do(ctx, http.MethodGet, "/infra-envs/"+infraEnvID, nil)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusOK); err != nil {
		return nil, fmt.Errorf("GetInfraEnv: %w", err)
	}
	var w wireInfraEnv
	if err := decodeJSON(resp, &w); err != nil {
		return nil, err
	}
	ie := wireToInfraEnv(w)
	return &ie, nil
}

func (c *AssistedClient) DeleteInfraEnv(ctx context.Context, infraEnvID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/infra-envs/"+infraEnvID, nil)
	if err != nil {
		return err
	}
	if err := checkResponse(resp, http.StatusNoContent, http.StatusOK); err != nil {
		return fmt.Errorf("DeleteInfraEnv: %w", err)
	}
	resp.Body.Close() //nolint:errcheck
	return nil
}

// ---- Host operations --------------------------------------------------------

func (c *AssistedClient) ListHosts(ctx context.Context, infraEnvID string) ([]Host, error) {
	resp, err := c.do(ctx, http.MethodGet, "/infra-envs/"+infraEnvID+"/hosts", nil)
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp, http.StatusOK); err != nil {
		return nil, fmt.Errorf("ListHosts: %w", err)
	}
	var wire []wireHost
	if err := decodeJSON(resp, &wire); err != nil {
		return nil, err
	}
	hosts := make([]Host, len(wire))
	for i, w := range wire {
		hosts[i] = Host{
			ID:                w.ID,
			InfraEnvID:        w.InfraEnvID,
			Status:            w.Status,
			Role:              w.Role,
			RequestedHostname: w.RequestedHostname,
		}
	}
	return hosts, nil
}

func (c *AssistedClient) UpdateHost(ctx context.Context, infraEnvID, hostID string, params UpdateHostParams) error {
	payload := map[string]interface{}{}
	if params.HostName != "" {
		payload["requested_hostname"] = params.HostName
	}
	if params.Role != "" {
		payload["role"] = params.Role
	}

	resp, err := c.do(ctx, http.MethodPatch, "/infra-envs/"+infraEnvID+"/hosts/"+hostID, payload)
	if err != nil {
		return err
	}
	if err := checkResponse(resp, http.StatusCreated, http.StatusOK); err != nil {
		return fmt.Errorf("UpdateHost: %w", err)
	}
	resp.Body.Close() //nolint:errcheck
	return nil
}

// WaitForHostsRegistered polls ListHosts until at least count hosts have registered,
// or until timeout is exceeded.
func (c *AssistedClient) WaitForHostsRegistered(ctx context.Context, infraEnvID string, count int, timeout time.Duration) ([]Host, error) {
	deadline := time.Now().Add(timeout)
	for {
		hosts, err := c.ListHosts(ctx, infraEnvID)
		if err != nil {
			return nil, err
		}
		if len(hosts) >= count {
			return hosts, nil
		}
		if time.Now().After(deadline) {
			return hosts, fmt.Errorf("timed out waiting for %d hosts; only %d registered after %s", count, len(hosts), timeout)
		}
		select {
		case <-ctx.Done():
			return hosts, ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}
