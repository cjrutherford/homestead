package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TunnelSecretName is the sealed-store name for the tunnel provider
// API token, following the domain credential convention: values meet
// calls without ever surfacing in payloads, logs, or exports.
const TunnelSecretName = "router_tunnel_api_token"

// CloudflareAPIBase is the production API origin. Tests override it
// through NewTunnelClient; callers never build URLs by hand.
const CloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// procNetRoute locates the kernel route table for DefaultRoute,
// overridden in tests with fixture files.
var procNetRoute = "/proc/net/route"

// Tunnel is one named tunnel: the provider-side object a connector
// attaches to. Token carries the connector run token, populated only
// by CreateTunnel — the provider reveals it at creation time and
// never again. Routes and connector files live beside it, never in it.
type Tunnel struct {
	ID    string
	Name  string
	Token string
}

// TunnelConnection is one live edge connection on a tunnel.
type TunnelConnection struct {
	Colo   string
	Opened string
}

// TunnelStatus is tunnel health: reported status plus live
// connections. Empty connections with an active status means the
// connector registered nothing — worth surfacing, never an error.
type TunnelStatus struct {
	ID          string
	Name        string
	Status      string
	Connections []TunnelConnection
}

// TunnelClient speaks the tunnel provider API. The token travels per
// call from the sealed store; the client never holds credentials.
type TunnelClient struct {
	base   string
	client *http.Client
}

// NewTunnelClient returns a client over baseURL, which must be an
// http(s) origin with a host.
func NewTunnelClient(baseURL string) (*TunnelClient, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("router: tunnel base URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("router: tunnel base URL must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("router: tunnel base URL must have a host")
	}
	return &TunnelClient{
		base:   strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// TunnelError carries a provider refusal with its code.
type TunnelError struct {
	Code    int
	Message string
}

func (e *TunnelError) Error() string {
	return fmt.Sprintf("router: tunnel API %d: %s", e.Code, e.Message)
}

// envelope decodes the provider result envelope: success false is a
// refusal with codes, never an empty result.
func envelope(body []byte, out any) error {
	var decoded struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("router: tunnel decode: %w", err)
	}
	if !decoded.Success {
		message := "unknown refusal"
		code := 0
		if len(decoded.Errors) > 0 {
			message = decoded.Errors[0].Message
			code = decoded.Errors[0].Code
		}
		return &TunnelError{Code: code, Message: message}
	}
	if out != nil {
		if err := json.Unmarshal(decoded.Result, out); err != nil {
			return fmt.Errorf("router: tunnel result decode: %w", err)
		}
	}
	return nil
}

// call issues one authenticated request and decodes the envelope.
func (c *TunnelClient) call(ctx context.Context, token, method, path string, body, out any) error {
	if token == "" {
		return fmt.Errorf("router: tunnel needs an API token")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("router: tunnel encode: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("router: tunnel request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("router: tunnel call: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("router: tunnel read: %w", err)
	}
	return envelope(raw, out)
}

// validateAccountID checks the account tag shape: 32 hex characters.
// Guessing accounts is how tokens leak across tenants, so anything
// else refuses before any call.
func validateAccountID(accountID string) error {
	if len(accountID) != 32 {
		return fmt.Errorf("router: account id must be 32 hex characters")
	}
	for _, r := range accountID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return fmt.Errorf("router: account id must be 32 hex characters")
		}
	}
	return nil
}

// ValidateTunnelName checks the tunnel name: one lowercase label run,
// so connector files and DNS stay predictable.
func ValidateTunnelName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("router: tunnel name must be 1-64 characters")
	}
	for _, r := range name {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("router: tunnel name must be lowercase letters, digits, or dashes")
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("router: tunnel name must not start or end with a dash")
	}
	return nil
}

// CreateTunnel creates a named tunnel under the account, returning
// its id for routes and connector files.
func (c *TunnelClient) CreateTunnel(ctx context.Context, token, accountID, name string) (Tunnel, error) {
	if err := validateAccountID(accountID); err != nil {
		return Tunnel{}, err
	}
	if err := ValidateTunnelName(name); err != nil {
		return Tunnel{}, err
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := c.call(ctx, token, http.MethodPost, "/accounts/"+accountID+"/tunnels",
		map[string]any{"name": name}, &created); err != nil {
		return Tunnel{}, err
	}
	if created.ID == "" {
		return Tunnel{}, fmt.Errorf("router: tunnel create returned no id")
	}
	if created.Token == "" {
		return Tunnel{}, fmt.Errorf("router: tunnel create returned no run token")
	}
	return Tunnel{ID: created.ID, Name: created.Name, Token: created.Token}, nil
}

// DeleteTunnel removes a tunnel by id. Routes pointing at it are
// deleted by the caller first: the provider may refuse while
// hostnames still reference it.
func (c *TunnelClient) DeleteTunnel(ctx context.Context, token, accountID, id string) error {
	if err := validateAccountID(accountID); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("router: tunnel id must not be empty")
	}
	return c.call(ctx, token, http.MethodDelete, "/accounts/"+accountID+"/tunnels/"+id, nil, nil)
}

// GetTunnel reports tunnel health with live connections.
func (c *TunnelClient) GetTunnel(ctx context.Context, token, accountID, id string) (TunnelStatus, error) {
	if err := validateAccountID(accountID); err != nil {
		return TunnelStatus{}, err
	}
	if id == "" {
		return TunnelStatus{}, fmt.Errorf("router: tunnel id must not be empty")
	}
	var status struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		Connections []struct {
			ColoName string `json:"colo_name"`
			OpenedAt string `json:"opened_at"`
		} `json:"connections"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/tunnels/"+id, nil, &status); err != nil {
		return TunnelStatus{}, err
	}
	out := TunnelStatus{ID: status.ID, Name: status.Name, Status: status.Status}
	for _, connection := range status.Connections {
		out.Connections = append(out.Connections, TunnelConnection{
			Colo: connection.ColoName, Opened: connection.OpenedAt,
		})
	}
	return out, nil
}

// ListAccounts returns the account ids the token can access. Status
// uses it to resolve tenancy without storing account ids beside
// routes: exactly one account means health checks know their owner,
// while zero or many read as unknown rather than guessed.
func (c *TunnelClient) ListAccounts(ctx context.Context, token string) ([]string, error) {
	if token == "" {
		return nil, fmt.Errorf("router: tunnel needs an API token")
	}
	var accounts []struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/accounts", nil, &accounts); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, account := range accounts {
		if account.ID != "" {
			ids = append(ids, account.ID)
		}
	}
	return ids, nil
}

// TunnelRoute is one served hostname: the public name, the tunnel
// carrying it, and the local origin service answering behind the
// connector. One hostname, one route — duplicates across tunnels
// would split traffic silently.
type TunnelRoute struct {
	Hostname string
	TunnelID string
	Service  string
}

// Validate checks the route shape: a real hostname, a named tunnel,
// and an http(s) origin URL. Hostnames stay lowercase FQDNs without
// trailing dots so DNS and connector config agree.
func (r TunnelRoute) Validate() error {
	if len(r.Hostname) == 0 || len(r.Hostname) > 253 {
		return fmt.Errorf("router: route hostname must be 1-253 characters")
	}
	if strings.ToLower(r.Hostname) != r.Hostname {
		return fmt.Errorf("router: route hostname must be lowercase")
	}
	if strings.HasSuffix(r.Hostname, ".") || strings.HasPrefix(r.Hostname, ".") ||
		strings.Contains(r.Hostname, "..") {
		return fmt.Errorf("router: route hostname must be a bare FQDN")
	}
	for _, label := range strings.Split(r.Hostname, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("router: invalid hostname label %q", label)
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return fmt.Errorf("router: invalid hostname label %q", label)
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("router: invalid hostname label %q", label)
		}
	}
	if r.TunnelID == "" {
		return fmt.Errorf("router: route needs a tunnel id")
	}
	service, err := url.Parse(r.Service)
	if err != nil {
		return fmt.Errorf("router: route service: %w", err)
	}
	if service.Scheme != "http" && service.Scheme != "https" {
		return fmt.Errorf("router: route service must be http or https")
	}
	if service.Host == "" {
		return fmt.Errorf("router: route service needs a host")
	}
	return nil
}

// PreferredProvider orders remote-access paths per ADR-017: automated
// router protocols first in detection strength, the tunnel when the
// router stays silent, and manual instructions when nothing answers.
// Names match Provider implementations except tunnel and manual,
// which need no mapping automation.
func PreferredProvider(protocols []MappingProtocol) string {
	for _, want := range []MappingProtocol{ProtocolUPnP, ProtocolNATPMP, ProtocolPCP} {
		for _, have := range protocols {
			if have == want {
				return string(want)
			}
		}
	}
	if len(protocols) == 0 {
		return "tunnel"
	}
	return "manual"
}

// DefaultRoute reports the default gateway's interface and address
// from the kernel route table and the dialing interface: the facts
// DiscoverGateway needs, with no guessing when either is absent.
func DefaultRoute() (interfaceName, gateway string, err error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", "", fmt.Errorf("router: dial out: %w", err)
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", "", fmt.Errorf("router: no local address")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", "", fmt.Errorf("router: list interfaces: %w", err)
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil && ip.Equal(local.IP) {
				gateway, err := defaultGateway(iface.Name)
				if err != nil {
					return "", "", err
				}
				return iface.Name, gateway, nil
			}
		}
	}
	return "", "", fmt.Errorf("router: no interface owns %s", local.IP)
}

// defaultGateway reads the default next hop for an interface: the
// 00000000 destination with no mask, rendered from little-endian hex.
func defaultGateway(interfaceName string) (string, error) {
	file, err := os.Open(procNetRoute)
	if err != nil {
		return "", fmt.Errorf("router: read route table: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] != interfaceName || fields[1] != "00000000" {
			continue
		}
		raw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		return fmt.Sprintf("%d.%d.%d.%d",
			byte(raw), byte(raw>>8), byte(raw>>16), byte(raw>>24)), nil
	}
	return "", fmt.Errorf("router: no default route on %s", interfaceName)
}

// Ingress is one connector ingress rule: a public hostname and the local
// service the connector delivers it to.
type Ingress struct {
	Hostname string
	Service  string
}

// RenderConnectorConfig renders the connector configuration for one
// hostname route.
func RenderConnectorConfig(tunnelID, hostname, service string) string {
	return RenderConnectorIngress(tunnelID, []Ingress{{Hostname: hostname, Service: service}})
}

// RenderConnectorIngress renders the connector configuration: the tunnel
// id plus one ingress rule per hostname, closing with a 404 catch-all so
// unlisted names never leak a default backend.
func RenderConnectorIngress(tunnelID string, ingress []Ingress) string {
	var rendered strings.Builder
	rendered.WriteString("tunnel: " + tunnelID + "\n")
	rendered.WriteString("ingress:\n")
	for _, in := range ingress {
		rendered.WriteString("  - hostname: " + in.Hostname + "\n")
		rendered.WriteString("    service: " + in.Service + "\n")
	}
	rendered.WriteString("  - service: http_status:404\n")
	return rendered.String()
}

// TunnelDetails is what adoption checks about a tunnel (S290, ADR-041):
// whether Cloudflare still has it, and whether its ingress comes from the
// connector's local file or from the Cloudflare dashboard.
type TunnelDetails struct {
	TunnelStatus
	RemoteConfig bool
	Deleted      bool
}

// GetTunnelDetails reads one tunnel through the cfd_tunnel API, which
// reports where its configuration lives.
func (c *TunnelClient) GetTunnelDetails(ctx context.Context, token, accountID, id string) (TunnelDetails, error) {
	if err := validateAccountID(accountID); err != nil {
		return TunnelDetails{}, err
	}
	if id == "" {
		return TunnelDetails{}, fmt.Errorf("router: tunnel id must not be empty")
	}
	var details struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		RemoteConfig bool   `json:"remote_config"`
		ConfigSrc    string `json:"config_src"`
		DeletedAt    string `json:"deleted_at"`
		Connections  []struct {
			ColoName string `json:"colo_name"`
			OpenedAt string `json:"opened_at"`
		} `json:"connections"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+url.PathEscape(id), nil, &details); err != nil {
		return TunnelDetails{}, err
	}
	out := TunnelDetails{
		TunnelStatus: TunnelStatus{ID: details.ID, Name: details.Name, Status: details.Status},
		RemoteConfig: details.RemoteConfig || details.ConfigSrc == "cloudflare",
		Deleted:      details.DeletedAt != "",
	}
	for _, connection := range details.Connections {
		out.Connections = append(out.Connections, TunnelConnection{Colo: connection.ColoName, Opened: connection.OpenedAt})
	}
	return out, nil
}

// TunnelRunToken fetches a tunnel's connector run token, which adoption
// writes to the connector's token file. It is never logged or returned.
func (c *TunnelClient) TunnelRunToken(ctx context.Context, token, accountID, id string) (string, error) {
	if err := validateAccountID(accountID); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("router: tunnel id must not be empty")
	}
	var runToken string
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+url.PathEscape(id)+"/token", nil, &runToken); err != nil {
		return "", err
	}
	if runToken == "" {
		return "", fmt.Errorf("router: Cloudflare returned no run token")
	}
	return runToken, nil
}

// ConnectorConfig is a connector config file as read back: the tunnel it
// names and its ingress, the catch-all included as an entry without a
// hostname.
type ConnectorConfig struct {
	Tunnel  string
	Ingress []Ingress
}

// ParseConnectorConfig reads a connector config file, the one Homestead
// writes or a hand-made one being adopted.
func ParseConnectorConfig(data []byte) (ConnectorConfig, error) {
	var raw struct {
		Tunnel  string `yaml:"tunnel"`
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
		} `yaml:"ingress"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return ConnectorConfig{}, fmt.Errorf("router: connector config: %w", err)
	}
	out := ConnectorConfig{Tunnel: strings.TrimSpace(raw.Tunnel)}
	for _, in := range raw.Ingress {
		out.Ingress = append(out.Ingress, Ingress{Hostname: strings.ToLower(strings.TrimSpace(in.Hostname)), Service: strings.TrimSpace(in.Service)})
	}
	return out, nil
}
