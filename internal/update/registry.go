package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Registry discovery (S343): Docker Hub stays the anonymous-tag path
// S190 introduced, and GitHub Container Registry joins it through the
// token flow GHCR requires for every request. Every other registry
// refuses by name instead of being queried on the wrong endpoint —
// pretending is worse than a loud gap.

// GHCRBase is GitHub Container Registry's API root.
const GHCRBase = "https://ghcr.io"

// Registry names carried in discovery provenance.
const (
	RegistryHub  = "docker hub"
	RegistryGHCR = "ghcr.io"
	// RegistryUnknown marks an image reference Homestead cannot
	// classify; discovery never guesses.
	RegistryUnknown = "unknown"
	// RegistryLocal marks a locally built image with no registry.
	RegistryLocal = "local build"
)

// RegistryClient performs tag discovery across supported registries.
// The bases are injectable so tests never touch a live registry, and
// the zero value works with production defaults.
type RegistryClient struct {
	HTTP     *http.Client
	HubBase  string
	GHCRBase string
}

func (c RegistryClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: httpTimeout}
}

func (c RegistryClient) hubBase() string {
	if c.HubBase != "" {
		return c.HubBase
	}
	return DockerHubAPI
}

func (c RegistryClient) ghcrBase() string {
	if c.GHCRBase != "" {
		return c.GHCRBase
	}
	return GHCRBase
}

// ImageRef is an image reference split into the registry that publishes
// it, its repository path, and its tag. Registry is a display name
// (docker hub, ghcr.io), not a hostname.
type ImageRef struct {
	Registry   string
	Repository string
	Tag        string
}

// ParseImage classifies an image reference. A first path segment with a
// dot or a colon is a registry host; bare names and owner/name live on
// Docker Hub. Digests are dropped from the tag, because discovery
// compares tags.
func ParseImage(image string) (ImageRef, error) {
	ref := ImageRef{Registry: RegistryUnknown}
	if image == "" {
		return ref, fmt.Errorf("update: empty image reference")
	}
	name := image
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	if slash, colon := strings.LastIndex(name, "/"), strings.LastIndex(name, ":"); colon > slash {
		ref.Tag = name[colon+1:]
		name = name[:colon]
	}
	host := ""
	repo := name
	if slash := strings.Index(name, "/"); slash >= 0 {
		first := name[:slash]
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			host = first
			repo = name[slash+1:]
		}
	}
	switch host {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io":
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
		ref.Registry = RegistryHub
	case "ghcr.io":
		ref.Registry = RegistryGHCR
	default:
		return ref, fmt.Errorf("update: registry %q does not support update discovery", host)
	}
	ref.Repository = repo
	return ref, nil
}

// Discovery is one image's registry result. Tags are the raw listing;
// filtering to newer versions stays with the planner. Err is nil only
// when the registry answered.
type Discovery struct {
	Service    string
	Registry   string
	Repository string
	Tags       []string
	// Local marks a locally built image that has no registry to ask.
	Local bool
	Err   error
}

// Discover classifies an image and lists its tags. Failures come back in
// Err rather than aborting the caller: one sulking registry must not
// hide the rest of the plan.
func (c RegistryClient) Discover(service, image string) Discovery {
	ref, tags, err := c.Tags(image)
	return Discovery{
		Service: service, Registry: ref.Registry, Repository: ref.Repository,
		Tags: tags, Err: err,
	}
}

// Tags lists an image's tags from the registry that publishes it.
func (c RegistryClient) Tags(image string) (ImageRef, []string, error) {
	ref, err := ParseImage(image)
	if err != nil {
		return ref, nil, err
	}
	switch ref.Registry {
	case RegistryHub:
		tags, err := RegistryTags(c.client(), c.hubBase(), image)
		return ref, tags, err
	case RegistryGHCR:
		tags, err := c.ghcrTags(ref.Repository)
		return ref, tags, err
	}
	return ref, nil, fmt.Errorf("update: %q has no supported registry", image)
}

// ghcrTags reads a public repository's tag list. GHCR requires a bearer
// token even for anonymous pulls: one token request, then at most two
// listing pages, matching the Docker Hub bound.
func (c RegistryClient) ghcrTags(repo string) ([]string, error) {
	base := strings.TrimSuffix(c.ghcrBase(), "/")
	if repo == "" {
		return nil, fmt.Errorf("update: ghcr image has no repository")
	}
	token, err := c.ghcrToken(base, repo)
	if err != nil {
		return nil, err
	}
	var tags []string
	next := base + "/v2/" + repo + "/tags/list?n=1000"
	for pages := 0; pages < 2 && next != ""; pages++ {
		var page struct {
			Tags []string `json:"tags"`
		}
		follow, err := c.get(next, token, &page)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page.Tags...)
		next = follow
	}
	return tags, nil
}

// ghcrToken requests the anonymous pull token GHCR hands out for public
// repositories. Private images are out of scope: the catalog ships
// public images only, so a token failure is reported, never retried with
// credentials Homestead does not have.
func (c RegistryClient) ghcrToken(base, repo string) (string, error) {
	url := base + "/token?service=ghcr.io&scope=repository:" + repo + ":pull"
	var out struct {
		Token string `json:"token"`
	}
	if _, err := c.get(url, "", &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("update: ghcr returned no pull token for %q", repo)
	}
	return out.Token, nil
}

// get performs one registry GET, decoding JSON into out and resolving a
// relative `Link: rel="next"` continuation. Bodies are bounded, and
// errors carry the status only.
func (c RegistryClient) get(url, token string, out any) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("update: registry request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("update: registry: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("update: registry read: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("update: registry rate limited")
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("update: registry refused the request; the image may be private")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("update: registry status %s", resp.Status)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return "", fmt.Errorf("update: registry decode: %w", err)
		}
	}
	return nextLink(resp.Header.Get("Link"), url), nil
}

// nextLink extracts a relative `rel="next"` target from a Link header.
// Registry pagination puts the target in angle brackets; anything else
// ends the listing.
func nextLink(header, current string) string {
	if header == "" {
		return ""
	}
	start := strings.Index(header, "<")
	end := strings.Index(header, ">")
	if start < 0 || end <= start {
		return ""
	}
	if !strings.Contains(header[end:], `rel="next"`) {
		return ""
	}
	target := header[start+1 : end]
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if !strings.HasPrefix(target, "/") {
		return ""
	}
	// Resolve against the request origin so a test double keeps working.
	if scheme := strings.Index(current, "://"); scheme >= 0 {
		rest := current[scheme+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return current[:scheme+3] + rest[:slash] + target
		}
	}
	return ""
}
