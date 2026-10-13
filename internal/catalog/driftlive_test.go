package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/docker"
	"homestead/internal/store"
)

type fakeLive struct {
	inv docker.Inventory
	err error
}

func (f fakeLive) Inventory(context.Context) (docker.Inventory, error) { return f.inv, f.err }

const liveCompose = `name: demo
services:
  web:
    image: nginx:1.27
    restart: unless-stopped
    ports: ["8080:80", "127.0.0.1:9000:9000/tcp", "7000"]
    volumes:
      - type: bind
        source: /mnt/data/demo/web
        target: /data
      - type: tmpfs
        target: /tmp
        tmpfs: {size: 100}
      - ./conf:/etc/conf:ro
    secrets: [web_key]
    networks: [edge]
  db:
    image: postgres:16
    restart: unless-stopped
networks:
  edge:
    external: true
secrets:
  web_key:
    file: ./secrets/web_key
`

func liveDir(t *testing.T, compose string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func lbl(service string) map[string]string {
	return map[string]string{docker.LabelProject: "demo", docker.LabelService: service}
}

func goodWeb(dir string) docker.Container {
	return docker.Container{
		ID: "w1", Names: []string{"demo-web"}, Image: "nginx:1.27", State: "running", Status: "Up 1 hour", Labels: lbl("web"),
		Ports: []docker.Port{
			{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"},
			{IP: "::", Private: 80, Public: 8080, Type: "tcp"},
			{IP: "127.0.0.1", Private: 9000, Public: 9000, Type: "tcp"},
			{IP: "0.0.0.0", Private: 7000, Public: 49153, Type: "tcp"},
			{Private: 5432, Type: "tcp"}, // exposed only
		},
		Networks: []string{"edge"},
		Mounts: []docker.Mount{
			{Type: "bind", Source: "/mnt/data/demo/web", Destination: "/data", RW: true},
			{Type: "tmpfs", Destination: "/tmp", RW: true},
			{Type: "bind", Source: filepath.Join(dir, "conf"), Destination: "/etc/conf", RW: false},
			{Type: "bind", Source: filepath.Join(dir, "secrets", "web_key"), Destination: "/run/secrets/web_key", RW: false},
			{Type: "volume", Name: strings.Repeat("a", 64), Destination: "/var/cache/nginx", RW: true},
			{Type: "bind", Source: "/var/lib/docker/containers/x/hosts", Destination: "/etc/hosts", RW: true},
		},
	}
}

func goodDB() docker.Container {
	return docker.Container{ID: "d1", Names: []string{"demo-db"}, Image: "postgres:16", State: "running", Labels: lbl("db"),
		Networks: []string{"demo_default"}}
}

func runLive(t *testing.T, dir string, inv docker.Inventory) LiveReview {
	t.Helper()
	inv.Reachable = true
	review, err := LiveDrift(context.Background(), fakeLive{inv: inv}, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	return review
}

func expectOne(t *testing.T, review LiveReview, kind, contains, resolution string) {
	t.Helper()
	if len(review.Findings) != 1 {
		t.Fatalf("findings = %+v", review.Findings)
	}
	f := review.Findings[0]
	if f.Kind != kind || !strings.Contains(f.Detail, contains) || f.Resolution != resolution {
		t.Fatalf("finding = %+v, want %s containing %q (%s)", f, kind, contains, resolution)
	}
}

func TestLiveCleanWithNormalizations(t *testing.T) {
	dir := liveDir(t, liveCompose)
	web := goodWeb(dir)
	web.Image = "docker.io/library/nginx:1.27" // registry and library prefixes normalize
	review := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}})
	if !review.Checked || len(review.Findings) != 0 {
		t.Fatalf("review = %+v", review)
	}
}

func TestLiveUnreachableIsNotClean(t *testing.T) {
	dir := liveDir(t, liveCompose)
	review, err := LiveDrift(context.Background(), fakeLive{}, "demo", dir)
	if err != nil || review.Checked {
		t.Fatalf("review = %+v err = %v", review, err)
	}
	if _, err := LiveDrift(context.Background(), fakeLive{err: errors.New("boom")}, "demo", dir); err == nil {
		t.Fatal("inventory error must surface")
	}
	if _, err := LiveDrift(context.Background(), fakeLive{}, "demo", t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing compose err = %v", err)
	}
}

func TestLiveImageFindings(t *testing.T) {
	dir := liveDir(t, liveCompose)
	run := func(web docker.Container, images ...docker.Image) LiveReview {
		return runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}, Images: images})
	}
	web := goodWeb(dir)
	web.Image = "nginx:1.26"
	expectOne(t, run(web), LiveImage, "running nginx:1.26, compose declares nginx:1.27", ResolveRestore)
	// Untagged reads as latest: nginx != nginx:1.27.
	web.Image = "nginx"
	expectOne(t, run(web), LiveImage, "compose declares nginx:1.27", ResolveRestore)
	// Image ID that still carries the tag resolves clean.
	id := "sha256:" + strings.Repeat("b", 64)
	web.Image, web.ImageID = id, id
	if r := run(web, docker.Image{ID: id, RepoTags: []string{"nginx:1.27"}}); len(r.Findings) != 0 {
		t.Fatalf("resolved ID = %+v", r.Findings)
	}
	// Tag moved to a newer image: container's ID no longer carries it.
	expectOne(t, run(web, docker.Image{ID: id, RepoTags: []string{}}, docker.Image{ID: "sha256:other", RepoTags: []string{"nginx:1.27"}}),
		LiveImage, "no longer carries nginx:1.27", ResolveRestore)
	// An ID the image list cannot resolve is not drift.
	if r := run(web); len(r.Findings) != 0 {
		t.Fatalf("unresolvable ID = %+v", r.Findings)
	}
}

func TestLiveDigestPins(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	dir := liveDir(t, strings.Replace(liveCompose, "image: nginx:1.27", "image: nginx:1.27@"+digest, 1))
	web := goodWeb(dir)
	// Created from the pin: Engine shows the digest reference.
	web.Image = "nginx@" + digest
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}}); len(r.Findings) != 0 {
		t.Fatalf("pinned = %+v", r.Findings)
	}
	web.Image = "nginx@sha256:" + strings.Repeat("d", 64)
	r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}})
	expectOne(t, r, LiveImage, "compose pins", ResolveRestore)
	// Tag shown, but the image carries the pinned digest: clean.
	web.Image, web.ImageID = "nginx:1.27", "sha256:img"
	good := docker.Image{ID: "sha256:img", RepoDigests: []string{"nginx@" + digest}}
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}, Images: []docker.Image{good}}); len(r.Findings) != 0 {
		t.Fatalf("digest via image = %+v", r.Findings)
	}
	bad := docker.Image{ID: "sha256:img", RepoDigests: []string{"nginx@sha256:" + strings.Repeat("e", 64)}}
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}, Images: []docker.Image{bad}}),
		LiveImage, "compose pins", ResolveRestore)
}

func TestLivePortFindings(t *testing.T) {
	dir := liveDir(t, liveCompose)
	mutate := func(f func(*docker.Container)) LiveReview {
		web := goodWeb(dir)
		f(&web)
		return runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}})
	}
	expectOne(t, mutate(func(c *docker.Container) { c.Ports = c.Ports[2:] }), LivePort, "host port 8080 -> 80/tcp is not published", ResolveRecreate)
	expectOne(t, mutate(func(c *docker.Container) { c.Ports[2].IP = "0.0.0.0" }), LivePort, "bound on all interfaces, compose binds 127.0.0.1", ResolveRecreate)
	expectOne(t, mutate(func(c *docker.Container) {
		c.Ports = append(c.Ports, docker.Port{IP: "0.0.0.0", Private: 22, Public: 2222, Type: "tcp"})
	}), LivePort, "unexpected published port 2222 -> 22/tcp", ResolveRecreate)
	r := mutate(func(c *docker.Container) { c.Ports[0].Type, c.Ports[1].Type = "udp", "udp" })
	if len(r.Findings) != 2 || !strings.Contains(r.Findings[0].Detail, "8080 -> 80/tcp is not published") ||
		!strings.Contains(r.Findings[1].Detail, "8080 -> 80/udp") {
		t.Fatalf("protocol mismatch = %+v", r.Findings)
	}
	// Ephemeral mapping must still be published somewhere.
	r = mutate(func(c *docker.Container) { c.Ports = append(c.Ports[:3:3], c.Ports[4]) })
	expectOne(t, r, LivePort, "container port 7000/tcp is not published", ResolveRecreate)
	// A stopped container lists no ports: not drift on its own.
	r = mutate(func(c *docker.Container) { c.State, c.Status, c.Ports = "exited", "Exited (1) 1 minute ago", nil })
	expectOne(t, r, LiveStopped, "exited", ResolveRestore)
}

func TestLiveNetworkFindings(t *testing.T) {
	dir := liveDir(t, liveCompose)
	web := goodWeb(dir)
	web.Networks = []string{"bridge"}
	r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}})
	if len(r.Findings) != 2 || r.Findings[0].Kind != LiveNetwork || r.Findings[1].Kind != LiveNetwork {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if !strings.Contains(r.Findings[0].Detail+r.Findings[1].Detail, "not attached to network edge") ||
		!strings.Contains(r.Findings[0].Detail+r.Findings[1].Detail, "unexpected network bridge") {
		t.Fatalf("findings = %+v", r.Findings)
	}
	// Project default network for services declaring none (db) is clean; a
	// wrong one is not.
	db := goodDB()
	db.Networks = []string{"other_default"}
	r = runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), db}})
	if len(r.Findings) != 2 {
		t.Fatalf("db findings = %+v", r.Findings)
	}
}

func TestLiveMountFindings(t *testing.T) {
	dir := liveDir(t, liveCompose)
	run := func(f func(*docker.Container)) LiveReview {
		web := goodWeb(dir)
		f(&web)
		return runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}})
	}
	// Anonymous volume where a bind is declared.
	expectOne(t, run(func(c *docker.Container) {
		c.Mounts[0] = docker.Mount{Type: "volume", Name: strings.Repeat("f", 64), Destination: "/data", RW: true}
	}), LiveMount, "mount /data is an anonymous volume, compose declares a bind mount", ResolveRecreate)
	// Named volume where a bind is declared.
	expectOne(t, run(func(c *docker.Container) {
		c.Mounts[0] = docker.Mount{Type: "volume", Name: "demo_data", Destination: "/data", RW: true}
	}), LiveMount, "named volume demo_data", ResolveRecreate)
	// Wrong bind source.
	expectOne(t, run(func(c *docker.Container) { c.Mounts[0].Source = "/srv/elsewhere" }),
		LiveMount, "binds /srv/elsewhere, compose declares /mnt/data/demo/web", ResolveRecreate)
	// Missing mount.
	expectOne(t, run(func(c *docker.Container) { c.Mounts = c.Mounts[1:] }),
		LiveMount, "mount /data is missing", ResolveRecreate)
	// Missing tmpfs.
	expectOne(t, run(func(c *docker.Container) { c.Mounts = append(c.Mounts[:1], c.Mounts[2:]...) }),
		LiveMount, "mount /tmp is missing", ResolveRecreate)
	// Read-only flag.
	expectOne(t, run(func(c *docker.Container) { c.Mounts[2].RW = true }),
		LiveMount, "writable, compose declares it read-only", ResolveRecreate)
	expectOne(t, run(func(c *docker.Container) { c.Mounts[0].RW = false }),
		LiveMount, "read-only, compose declares it writable", ResolveRecreate)
	// Secret mounted writable.
	expectOne(t, run(func(c *docker.Container) { c.Mounts[3].RW = true }),
		LiveMount, "secret /run/secrets/web_key is mounted writable", ResolveRecreate)
	// Unexpected extra bind and named volume (anonymous image volumes stay ignored).
	expectOne(t, run(func(c *docker.Container) {
		c.Mounts = append(c.Mounts, docker.Mount{Type: "bind", Source: "/", Destination: "/host", RW: true})
	}), LiveMount, "unexpected mount /host", ResolveRecreate)
	expectOne(t, run(func(c *docker.Container) {
		c.Mounts = append(c.Mounts, docker.Mount{Type: "volume", Name: "scratch", Destination: "/scratch", RW: true})
	}), LiveMount, "unexpected mount /scratch (named volume scratch)", ResolveRecreate)
}

func TestLiveSymlinkedBindSourceIsNotDrift(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir := liveDir(t, strings.Replace(liveCompose, "/mnt/data/demo/web", link, 1))
	web := goodWeb(dir)
	web.Mounts[0].Source = real
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web, goodDB()}}); len(r.Findings) != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestLiveNamedVolumeResolvesProjectScope(t *testing.T) {
	dir := liveDir(t, "name: demo\nservices:\n  db:\n    image: postgres:16\n    volumes: ['pgdata:/var/lib/postgresql/data']\nvolumes:\n  pgdata: {}\n")
	db := goodDB()
	db.Mounts = []docker.Mount{{Type: "volume", Name: "demo_pgdata", Destination: "/var/lib/postgresql/data", RW: true}}
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{db}}); len(r.Findings) != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	db.Mounts[0].Name = "wrong"
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{db}}), LiveMount, "uses volume wrong, compose declares demo_pgdata", ResolveRecreate)
}

func TestLiveContainerStates(t *testing.T) {
	dir := liveDir(t, liveCompose)
	// Missing container.
	r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir)}})
	expectOne(t, r, LiveMissing, "no container exists", ResolveRestore)
	if r.Findings[0].Service != "db" {
		t.Fatalf("service = %s", r.Findings[0].Service)
	}
	// Restarting.
	db := goodDB()
	db.State, db.Status = "restarting", "Restarting (1) 3 seconds ago"
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), db}}), LiveRestarting, "Restarting", ResolveManual)
	// Stopped.
	db.State, db.Status = "exited", "Exited (137) 2 minutes ago"
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), db}}), LiveStopped, "exited", ResolveRestore)
	// Extra container in the project, and a one-off and foreign project that are ignored.
	ghost := docker.Container{ID: "g", Names: []string{"demo-ghost"}, Image: "alpine", State: "running", Labels: lbl("ghost")}
	oneoff := docker.Container{ID: "o", Names: []string{"demo-run"}, Image: "alpine", State: "exited", Labels: map[string]string{
		docker.LabelProject: "demo", docker.LabelService: "web", docker.LabelOneOff: "True"}}
	other := docker.Container{ID: "p", Image: "alpine", State: "running", Labels: map[string]string{docker.LabelProject: "other", docker.LabelService: "web"}}
	r = runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), goodDB(), ghost, oneoff, other}})
	expectOne(t, r, LiveExtra, "no compose service declares it", ResolveManual)
	// Too many containers for one service.
	dup := goodDB()
	dup.ID, dup.Names = "d2", []string{"demo-db-2"}
	r = runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), goodDB(), dup}})
	expectOne(t, r, LiveExtra, "2 containers run for this service, compose expects 1", ResolveManual)
}

func TestLiveRunOnceAndProfiles(t *testing.T) {
	dir := liveDir(t, "name: demo\nservices:\n  job:\n    image: alpine\n    restart: 'no'\n  dev:\n    image: alpine\n    profiles: [debug]\n")
	job := docker.Container{ID: "j", Names: []string{"demo-job"}, Image: "alpine", State: "exited", Status: "Exited (0) 5 minutes ago", Labels: lbl("job"), Networks: []string{"demo_default"}}
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{job}}); len(r.Findings) != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	job.Status = "Exited (2) 5 minutes ago"
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{job}}), LiveStopped, "exited", ResolveRestore)
}

func TestLiveOverrideAppliesImage(t *testing.T) {
	dir := liveDir(t, liveCompose)
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName), []byte("services:\n  db:\n    image: postgres:17\n    environment: {A: b}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{goodWeb(dir), goodDB()}}),
		LiveImage, "compose declares postgres:17", ResolveRestore)
}

func TestLiveLongPortSyntaxAndRanges(t *testing.T) {
	dir := liveDir(t, "name: demo\nservices:\n  web:\n    image: nginx\n    ports:\n      - target: 80\n        published: '8080'\n        host_ip: 0.0.0.0\n      - '[::1]:9090:90'\n      - '6000-6001:6000-6001/udp'\n")
	web := docker.Container{ID: "w", Names: []string{"w"}, Image: "nginx", State: "running", Labels: lbl("web"), Networks: []string{"demo_default"},
		Ports: []docker.Port{
			{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"},
			{IP: "::1", Private: 90, Public: 9090, Type: "tcp"},
			{IP: "0.0.0.0", Private: 6000, Public: 6000, Type: "udp"},
			{IP: "0.0.0.0", Private: 6001, Public: 6001, Type: "udp"},
		}}
	if r := runLive(t, dir, docker.Inventory{Containers: []docker.Container{web}}); len(r.Findings) != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	web.Ports = web.Ports[:3]
	expectOne(t, runLive(t, dir, docker.Inventory{Containers: []docker.Container{web}}), LivePort, "6001 -> 6001/udp is not published", ResolveRecreate)
}

func liveStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestScanWithLiveAndAcceptKeepsLiveFindings(t *testing.T) {
	st := liveStore(t)
	dir := liveDir(t, liveCompose)
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "web_key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordFingerprints(st, "demo", dir); err != nil {
		t.Fatal(err)
	}
	dirFor := func(string) string { return dir }
	now := time.Now()
	live := fakeLive{inv: docker.Inventory{Reachable: true, Containers: []docker.Container{goodWeb(dir)}}}
	// File edit plus a missing db container.
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(liveCompose+"# edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ScanWithLive(context.Background(), st, dirFor, now, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drifted) != 1 || len(res.Drifted[0].Files) != 1 || len(res.Drifted[0].Live) != 1 || res.Drifted[0].Live[0].Kind != LiveMissing {
		t.Fatalf("scan = %+v", res)
	}
	if err := SaveScan(st, res); err != nil {
		t.Fatal(err)
	}
	// Accept resolves file drift only; the live finding stays.
	if _, err := Accept(st, "demo", dir); err != nil {
		t.Fatal(err)
	}
	latest, _, err := LatestScan(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Drifted) != 1 || len(latest.Drifted[0].Files) != 0 || len(latest.Drifted[0].Live) != 1 {
		t.Fatalf("after accept = %+v", latest.Drifted)
	}
	// A scan without a live source stays file-only.
	plain, err := Scan(st, dirFor, now)
	if err != nil || !plain.Clean() || plain.LiveSkipped != "" {
		t.Fatalf("plain scan = %+v err = %v", plain, err)
	}
	// Unreachable engine skips the comparison without failing the scan.
	skipped, err := ScanWithLive(context.Background(), st, dirFor, now, fakeLive{})
	if err != nil || !skipped.Clean() || skipped.LiveSkipped == "" {
		t.Fatalf("skipped = %+v err = %v", skipped, err)
	}
	failed, err := ScanWithLive(context.Background(), st, dirFor, now, fakeLive{err: errors.New("boom")})
	if err != nil || !failed.Clean() || !strings.Contains(failed.LiveSkipped, "boom") {
		t.Fatalf("failed = %+v err = %v", failed, err)
	}
}
