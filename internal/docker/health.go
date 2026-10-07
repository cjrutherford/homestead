package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultSocketPath is the local Engine socket. DOCKER_HOST with a
// unix:// URL overrides it; TCP remotes are out of scope (local daemon,
// local engine).
const DefaultSocketPath = "/var/run/docker.sock"

// ClientTimeout bounds Engine API calls.
const ClientTimeout = 15 * time.Second

// Container is one container summary.
type Container struct {
	ID     string            `json:"id"`
	Names  []string          `json:"names"`
	Image  string            `json:"image"`
	State  string            `json:"state"`
	Status string            `json:"status"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Image is one image summary.
type Image struct {
	ID       string   `json:"id"`
	RepoTags []string `json:"repo_tags"`
	Size     int64    `json:"size"`
	Created  int64    `json:"created"`
}

// Network is one network summary.
type Network struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

// Health is engine health plus inventory. Unreachable daemons report
// Reachable false with no error — absence is data; malformed responses
// are errors.
type Health struct {
	Reachable     bool        `json:"reachable"`
	Version       string      `json:"version"`
	APIVersion    string      `json:"api_version"`
	StorageDriver string      `json:"storage_driver"`
	Containers    []Container `json:"containers"`
	Images        []Image     `json:"images"`
	Networks      []Network   `json:"networks"`
}

// Client speaks the Engine API over the local Unix socket with the
// standard library only — no SDK dependency (S070 allowlist stands).
type Client struct {
	socketPath string
	http       *http.Client
}

// SocketPath resolves the Engine socket: DOCKER_HOST unix:// URL wins.
func SocketPath() string {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return strings.TrimPrefix(host, "unix://")
	}
	return DefaultSocketPath
}

// NewClient returns a Client for socketPath ("" resolves SocketPath()).
func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = SocketPath()
	}
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Timeout: ClientTimeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					dialer := &net.Dialer{}
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// ping proves the socket answers; any failure means unreachable.
func (c *Client) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/_ping", nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("docker: ping status %d", res.StatusCode)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("docker: %s status %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("docker: decode %s: %w", path, err)
	}
	return nil
}

// Health reports engine health plus inventory. An unreachable daemon
// (missing socket, refused dial) yields Reachable false and no error;
// anything stranger after contact is an error.
func (c *Client) Health(ctx context.Context) (Health, error) {
	health := Health{Containers: []Container{}, Images: []Image{}, Networks: []Network{}}
	if err := c.ping(ctx); err != nil {
		return health, nil
	}
	health.Reachable = true
	var version struct {
		Version    string `json:"Version"`
		ApiVersion string `json:"ApiVersion"`
	}
	if err := c.get(ctx, "/version", &version); err != nil {
		return Health{}, err
	}
	health.Version = version.Version
	health.APIVersion = version.ApiVersion
	var info struct {
		Driver string `json:"Driver"`
	}
	if err := c.get(ctx, "/info", &info); err != nil {
		return Health{}, err
	}
	health.StorageDriver = info.Driver
	var containers []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
	}
	if err := c.get(ctx, "/containers/json?all=1", &containers); err != nil {
		return Health{}, err
	}
	health.Containers = []Container{}
	for _, container := range containers {
		names := make([]string, 0, len(container.Names))
		for _, name := range container.Names {
			names = append(names, strings.TrimPrefix(name, "/"))
		}
		health.Containers = append(health.Containers, Container{
			ID: container.ID, Names: names,
			Image: container.Image, State: container.State, Status: container.Status,
			Labels: container.Labels,
		})
	}
	var images []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Size     int64    `json:"Size"`
		Created  int64    `json:"Created"`
	}
	if err := c.get(ctx, "/images/json", &images); err != nil {
		return Health{}, err
	}
	health.Images = []Image{}
	for _, image := range images {
		tags := image.RepoTags
		if tags == nil {
			tags = []string{}
		}
		health.Images = append(health.Images, Image{
			ID: image.ID, RepoTags: tags, Size: image.Size, Created: image.Created,
		})
	}
	var networks []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Driver string `json:"Driver"`
		Scope  string `json:"Scope"`
	}
	if err := c.get(ctx, "/networks", &networks); err != nil {
		return Health{}, err
	}
	health.Networks = []Network{}
	for _, network := range networks {
		health.Networks = append(health.Networks, Network{
			ID: network.ID, Name: network.Name, Driver: network.Driver, Scope: network.Scope,
		})
	}
	return health, nil
}
