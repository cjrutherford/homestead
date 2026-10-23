package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DockerHubAPI lists tags without authentication: the Docker Hub half of
// discovery. GHCR needs the token flow in registry.go (S343), and the
// two paths are chosen by image reference, never by guessing.
const DockerHubAPI = "https://hub.docker.com/v2/repositories"

// httpTimeout bounds registry calls; discovery degrades to manifest
// drift when registries sulk, never hangs operations on them.
const httpTimeout = 15 * time.Second

// versionPrefix splits numeric dotted prefixes ("35.0.1" in
// "35.0.1-apache") from variant suffixes, tolerating the `v` prefix GHCR
// projects use ("v1.119.0"): the prefix is decoration, not a version
// component. Variants never compare across suffixes: fpm builds are not
// updates for apache pins.
var versionPrefix = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(.*)$`)

// Available is one detected update: the service, its running image,
// the target image, and where the target came from (manifest pins or
// registry listings).
type Available struct {
	Service string
	Current string
	Target  string
	Source  string
}

// Sources naming update origins.
const (
	SourceManifest = "manifest"
	SourceRegistry = "registry"
)

// Drift compares pinned manifest images against running ones: any
// service whose running tag differs from its pin has an update
// available in exactly one direction — toward the pin. Empty running
// maps fail instead of reporting the whole catalog stale.
func Drift(manifest map[string]string, running map[string]string) ([]Available, error) {
	if len(running) == 0 {
		return nil, fmt.Errorf("update: no running images to compare")
	}
	var out []Available
	for service, pinned := range manifest {
		current, ok := running[service]
		if !ok {
			continue
		}
		if current != pinned {
			out = append(out, Available{Service: service, Current: current, Target: pinned, Source: SourceManifest})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out, nil
}

// splitTag separates image, numeric version parts, and variant
// suffix. Non-numeric tags (latest, stable, dated builds without
// dotted numbers) report no version and never participate.
func splitTag(tag string) ([]int, string, bool) {
	parts := versionPrefix.FindStringSubmatch(tag)
	if parts == nil {
		return nil, "", false
	}
	var numbers []int
	for _, field := range strings.Split(parts[1], ".") {
		number, err := strconv.Atoi(field)
		if err != nil {
			return nil, "", false
		}
		numbers = append(numbers, number)
	}
	return numbers, parts[2], true
}

// newerThan reports strictly newer versions within the same variant
// family: longer-or-greater numeric prefixes with identical suffixes.
// Different suffixes never compare — fpm is not an update for apache.
func newerThan(candidate, pinned string) bool {
	candidateNumbers, candidateSuffix, ok := splitTag(candidate)
	if !ok {
		return false
	}
	pinnedNumbers, pinnedSuffix, ok := splitTag(pinned)
	if !ok || candidateSuffix != pinnedSuffix {
		return false
	}
	longer := len(candidateNumbers) > len(pinnedNumbers)
	for i := 0; i < len(candidateNumbers) && i < len(pinnedNumbers); i++ {
		if candidateNumbers[i] != pinnedNumbers[i] {
			return candidateNumbers[i] > pinnedNumbers[i]
		}
	}
	return longer
}

// NewerTags filters listings to versions newer than the pin, newest
// first. Comparison is numeric per dotted component, so 9 sorts
// below 10 where string sorting would embarrass itself.
func NewerTags(pinned string, tags []string) []string {
	var out []string
	for _, tag := range tags {
		if newerThan(tag, pinned) {
			out = append(out, tag)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, _, _ := splitTag(out[i])
		right, _, _ := splitTag(out[j])
		for k := 0; k < len(left) && k < len(right); k++ {
			if left[k] != right[k] {
				return left[k] > right[k]
			}
		}
		return len(left) > len(right)
	})
	return out
}

// dockerHubRepo maps image names to registry paths: bare names live
// under library, qualified names pass through, and ghcr (or any
// registry host) refuses with a clear error instead of a wrong URL.
func dockerHubRepo(image string) (string, error) {
	name := image
	if index := strings.Index(name, ":"); index >= 0 {
		name = name[:index]
	}
	if strings.Contains(name, "/") {
		first := name[:strings.Index(name, "/")]
		if strings.Contains(first, ".") || strings.Contains(first, ":") {
			return "", fmt.Errorf("update: %q is not a Docker Hub image", image)
		}
		return name, nil
	}
	return "library/" + name, nil
}

// hubPage decodes one tag-listing page.
type hubPage struct {
	Results []struct {
		Name string `json:"name"`
	} `json:"results"`
	Next string `json:"next"`
}

// RegistryTags lists tags for a Docker Hub image, following one
// continuation page: two hundred candidates bound the work while
// covering every realistic track. Rate limits and outages error
// instead of returning partial lists that would read as "current".
func RegistryTags(client *http.Client, baseURL, image string) ([]string, error) {
	repo, err := dockerHubRepo(image)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	var tags []string
	next := strings.TrimSuffix(baseURL, "/") + "/" + repo + "/tags?page_size=100&ordering=last_updated"
	for pages := 0; pages < 2 && next != ""; pages++ {
		var page hubPage
		follow, err := fetchPage(client, next, &page)
		if err != nil {
			return nil, err
		}
		for _, result := range page.Results {
			tags = append(tags, result.Name)
		}
		next = follow
	}
	return tags, nil
}

// fetchPage GETs one listing page, decoding the envelope and
// returning the continuation URL.
func fetchPage(client *http.Client, url string, page *hubPage) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("update: registry: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("update: registry read: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("update: registry rate limited")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("update: registry status %s", resp.Status)
	}
	if err := json.Unmarshal(raw, page); err != nil {
		return "", fmt.Errorf("update: registry decode: %w", err)
	}
	return page.Next, nil
}

// RegistryUpdates filters a tag listing to versions newer than the
// service's pinned tag: the registry half of discovery. Non-Hub
// images refuse here (see dockerHubRepo) rather than querying wrong
// endpoints.
func RegistryUpdates(client *http.Client, baseURL, image, pinnedTag string) ([]string, error) {
	tags, err := RegistryTags(client, baseURL, image)
	if err != nil {
		return nil, err
	}
	return NewerTags(pinnedTag, tags), nil
}
