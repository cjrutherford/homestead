package docker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func stubEngine(t *testing.T) (string, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/_ping":
			_, _ = w.Write([]byte("OK"))
		case "/version":
			_, _ = w.Write([]byte(`{"Version":"29.1.2","ApiVersion":"1.52"}`))
		case "/info":
			_, _ = w.Write([]byte(`{"Driver":"overlayfs"}`))
		case "/containers/json":
			_, _ = w.Write([]byte(`[{"Id":"abc123def456","Names":["/web"],"Image":"nginx:latest","State":"running","Status":"Up 2 hours","ImageID":"sha256:111","Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":443,"Type":"tcp"}],"NetworkSettings":{"Networks":{"web_default":{},"edge":{}}},"Mounts":[{"Type":"volume","Name":"vol1","Source":"","Destination":"/data","RW":true},{"Type":"volume","Name":"vol2","Source":"/var/lib/docker/volumes/vol2/_data","Destination":"/b","RW":true}]}]`))
		case "/volumes/vol1":
			_, _ = w.Write([]byte(`{"Name":"vol1","Mountpoint":"/var/lib/docker/volumes/vol1/_data"}`))
		case "/images/json":
			_, _ = w.Write([]byte(`[{"Id":"sha256:111","RepoTags":["nginx:latest"],"RepoDigests":["nginx@sha256:abc"],"Size":188000000,"Created":1700000000}]`))
		case "/networks":
			_, _ = w.Write([]byte(`[{"Id":"net1","Name":"bridge","Driver":"bridge","Scope":"local"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return socketPath, &calls
}

func TestHealthRoundTrip(t *testing.T) {
	socketPath, _ := stubEngine(t)
	health, err := NewClient(socketPath).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !health.Reachable || health.Version != "29.1.2" || health.APIVersion != "1.52" {
		t.Fatalf("health = %+v", health)
	}
	if health.StorageDriver != "overlayfs" {
		t.Fatalf("driver = %q", health.StorageDriver)
	}
	if len(health.Containers) != 1 || health.Containers[0].Names[0] != "web" {
		t.Fatalf("containers = %+v", health.Containers)
	}
	if len(health.Images) != 1 || health.Images[0].RepoTags[0] != "nginx:latest" {
		t.Fatalf("images = %+v", health.Images)
	}
	if len(health.Networks) != 1 || health.Networks[0].Name != "bridge" {
		t.Fatalf("networks = %+v", health.Networks)
	}
}

func TestUnreachableSocket(t *testing.T) {
	health, err := NewClient(filepath.Join(t.TempDir(), "absent.sock")).Health(context.Background())
	if err != nil {
		t.Fatalf("unreachable = %v, want nil error", err)
	}
	if health.Reachable {
		t.Fatal("reported reachable without socket")
	}
}

func TestBrokenResponsesError(t *testing.T) {
	// Ping succeeds but version is garbage: anomaly, not unreachability.
	broken := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		_, _ = w.Write([]byte("nope"))
	}))
	brokenPath := filepath.Join(t.TempDir(), "broken.sock")
	brokenListener, err := net.Listen("unix", brokenPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	broken.Listener = brokenListener
	broken.Start()
	defer broken.Close()
	if _, err := NewClient(brokenPath).Health(context.Background()); err == nil {
		t.Fatal("garbage version accepted, want error")
	}
}

func TestLiveHealth(t *testing.T) {
	health, err := NewClient("").Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	t.Logf("reachable=%v version=%q api=%q containers=%d images=%d networks=%d driver=%q",
		health.Reachable, health.Version, health.APIVersion,
		len(health.Containers), len(health.Images), len(health.Networks), health.StorageDriver)
}

func TestContainersResolveEmptyVolumeSource(t *testing.T) {
	socketPath, _ := stubEngine(t)
	containers, err := NewClient(socketPath).Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(containers) != 1 || len(containers[0].Mounts) != 2 {
		t.Fatalf("containers = %+v", containers)
	}
	if got := containers[0].Mounts[0].Source; got != "/var/lib/docker/volumes/vol1/_data" {
		t.Fatalf("empty list Source not resolved from the volume record: %q", got)
	}
	if got := containers[0].Mounts[1].Source; got != "/var/lib/docker/volumes/vol2/_data" {
		t.Fatalf("listed Source changed: %q", got)
	}
}

func TestInventoryReadsPortsNetworksAndDigests(t *testing.T) {
	socketPath, _ := stubEngine(t)
	inv, err := NewClient(socketPath).Inventory(context.Background())
	if err != nil || !inv.Reachable || len(inv.Containers) != 1 || inv.Images != nil || inv.LoadImages == nil || !inv.Local {
		t.Fatalf("inventory = %+v err = %v", inv, err)
	}
	images, err := inv.LoadImages()
	if err != nil || len(images) != 1 {
		t.Fatalf("images = %+v err = %v", images, err)
	}
	c := inv.Containers[0]
	if c.ImageID != "sha256:111" || len(c.Ports) != 2 || c.Ports[0].Public != 8080 || c.Ports[1].Public != 0 {
		t.Fatalf("container = %+v", c)
	}
	if len(c.Networks) != 2 || c.Networks[0] != "edge" || c.Networks[1] != "web_default" {
		t.Fatalf("networks = %v", c.Networks)
	}
	if got := images[0].RepoDigests; len(got) != 1 || got[0] != "nginx@sha256:abc" {
		t.Fatalf("digests = %v", got)
	}
	// Unreachable daemons are data, not errors.
	gone, err := NewClient(filepath.Join(t.TempDir(), "none.sock")).Inventory(context.Background())
	if err != nil || gone.Reachable {
		t.Fatalf("unreachable = %+v err = %v", gone, err)
	}
}
