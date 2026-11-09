package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func jellyfinManifest() catalog.Manifest {
	return catalog.Manifest{Name: "jellyfin", Title: "Jellyfin", Serve: catalog.ServePath, Path: "/apps/jellyfin/",
		FirstRun: &catalog.FirstRun{Routine: catalog.FirstRunJellyfin, Service: "jellyfin", Account: "admin", Secret: "admin_password",
			Libraries: []catalog.Library{{Name: "Movies", Kind: "movies", Path: "/media/movies"}, {Name: "TV Shows", Kind: "tvshows", Path: "/media/tv"}}}}
}

type fakeJellyfin struct {
	t        *testing.T
	starting int
	// root answers at the root instead of /apps/jellyfin (pre-S315).
	root      bool
	completed bool
	folders   []string
	fail      string
	// busy answers 503 (Jellyfin's starting page) this many times to
	// anything but /System/Info/Public.
	busy   int
	calls  []string
	stdins map[string]string
}

func (f *fakeJellyfin) install(t *testing.T) {
	f.t, f.stdins = t, map[string]string{}
	old, oldTries, oldPause := execIn, Tries, Pause
	Tries, Pause = 5, time.Millisecond
	execIn = func(_ context.Context, container, stdin string, args ...string) (string, error) {
		if container != "jellyfin-jellyfin" || args[0] != "curl" {
			t.Fatalf("exec %s %v", container, args)
		}
		for _, a := range args {
			if strings.Contains(a, "s3cret-pw") {
				t.Fatalf("password in argv: %v", args)
			}
		}
		if f.starting > 0 {
			f.starting--
			return "", errors.New("exit status 7")
		}
		method, target := args[5], args[len(args)-1]
		target = strings.TrimPrefix(target, jellyfinOrigin)
		base := "/apps/jellyfin"
		if f.root {
			base = ""
		}
		if !strings.HasPrefix(target, base+"/") || (f.root && strings.HasPrefix(target, "/apps/")) {
			return "\n404", nil
		}
		path := strings.TrimPrefix(target, base)
		call := method + " " + strings.SplitN(path, "?", 2)[0]
		f.calls = append(f.calls, call)
		f.stdins[call] = stdin
		if f.busy > 0 && call != "GET /System/Info/Public" {
			f.busy--
			return "\ufeff<!DOCTYPE html><html>starting</html>\n503", nil
		}
		if call == f.fail {
			return "boom\n500", nil
		}
		switch call {
		case "GET /System/Info/Public":
			return `{"StartupWizardCompleted":` + map[bool]string{true: "true", false: "false"}[f.completed] + "}\n200", nil
		case "GET /Library/VirtualFolders":
			var out []map[string]string
			for _, n := range f.folders {
				out = append(out, map[string]string{"Name": n})
			}
			raw, _ := json.Marshal(out)
			return string(raw) + "\n200", nil
		case "GET /Startup/User":
			return `{"Name":"root"}` + "\n200", nil
		}
		return "\n204", nil
	}
	t.Cleanup(func() { execIn, Tries, Pause = old, oldTries, oldPause })
}

func TestJellyfinCompletesWizard(t *testing.T) {
	f := &fakeJellyfin{starting: 2, folders: []string{"Movies"}}
	f.install(t)
	secrets := memSecrets{"app_jellyfin_admin_password": "s3cret-pw"}
	res, err := Run(context.Background(), jellyfinManifest(), secrets)
	if err != nil || res != Created {
		t.Fatalf("res %q err %v", res, err)
	}
	want := []string{"GET /System/Info/Public", "POST /Startup/Configuration", "GET /Startup/User", "POST /Startup/User",
		"GET /Library/VirtualFolders", "POST /Library/VirtualFolders", "POST /Startup/RemoteAccess", "POST /Startup/Complete"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(f.stdins["POST /Startup/User"], `"Password":"s3cret-pw"`) || !strings.Contains(f.stdins["POST /Startup/User"], `"Name":"admin"`) {
		t.Fatalf("user body %q", f.stdins["POST /Startup/User"])
	}
	if !strings.Contains(f.stdins["POST /Startup/Configuration"], `"ServerName":"Homestead"`) {
		t.Fatalf("configuration body %q", f.stdins["POST /Startup/Configuration"])
	}
}

// Live on VM 2: /System/Info/Public answers while every other endpoint
// is still Jellyfin's 503 starting page. That is waited out, not failed.
func TestJellyfinStartingPageIsWaitedOut(t *testing.T) {
	f := &fakeJellyfin{busy: 2}
	f.install(t)
	res, err := Run(context.Background(), jellyfinManifest(), memSecrets{"app_jellyfin_admin_password": "s3cret-pw"})
	if err != nil || res != Created {
		t.Fatalf("res %q err %v", res, err)
	}
	if f.calls[len(f.calls)-1] != "POST /Startup/Complete" {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestJellyfinCompletedIsLeftAlone(t *testing.T) {
	f := &fakeJellyfin{completed: true, root: true}
	f.install(t)
	res, err := Run(context.Background(), jellyfinManifest(), memSecrets{})
	if err != nil || res != Ready {
		t.Fatalf("res %q err %v", res, err)
	}
	if len(f.calls) != 1 || f.calls[0] != "GET /System/Info/Public" {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestJellyfinRefusalIsNamed(t *testing.T) {
	f := &fakeJellyfin{fail: "POST /Startup/User"}
	f.install(t)
	_, err := Run(context.Background(), jellyfinManifest(), memSecrets{"app_jellyfin_admin_password": "s3cret-pw"})
	if err == nil || !strings.Contains(err.Error(), "create the administrator: HTTP 500 boom") {
		t.Fatalf("err %v", err)
	}
	if f.calls[len(f.calls)-1] != "POST /Startup/User" {
		t.Fatalf("went on after the refusal: %v", f.calls)
	}
}
