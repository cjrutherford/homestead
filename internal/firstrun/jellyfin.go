package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"homestead/internal/catalog"
)

// JellyfinServerName is the name Jellyfin shows for the appliance.
const JellyfinServerName = "Homestead"

// jellyfinRun completes Jellyfin's startup wizard through the API the
// wizard page itself uses (S319). Until the wizard is complete these
// calls need no sign-in; once it is, Jellyfin refuses them, so an
// installation finished by its owner is never touched. Requests go
// through curl in the container; bodies, the password among them, travel
// on stdin.
type jellyfinRun struct {
	container string
	// bases are the URL prefixes to try, in order.
	bases     []string
	account   string
	password  string
	libraries []catalog.Library
}

const jellyfinOrigin = "http://127.0.0.1:8096"

var errStarting = errors.New("Jellyfin is still starting")

// brief is a short, single-line reply for an error, never a page.
func brief(reply string) string {
	reply = strings.TrimSpace(strings.TrimPrefix(reply, "\ufeff"))
	if reply == "" || strings.HasPrefix(reply, "<") {
		return ""
	}
	reply = strings.Join(strings.Fields(reply), " ")
	if len(reply) > 200 {
		reply = reply[:200] + "…"
	}
	return " " + reply
}

// call sends one request and returns the status and body. err means
// curl did not get an answer (the container is down or still starting).
func (j jellyfinRun) call(ctx context.Context, method, base, path string, body any) (int, string, error) {
	args := []string{"curl", "-s", "-m", "20", "-X", method, "-w", "\n%{http_code}"}
	stdin := ""
	if body != nil {
		raw, _ := json.Marshal(body)
		stdin = string(raw)
		args = append(args, "-H", "Content-Type: application/json", "--data-binary", "@-")
	}
	args = append(args, jellyfinOrigin+base+path)
	out, err := execIn(ctx, j.container, stdin, args...)
	out = strings.ReplaceAll(out, j.password, "[redacted]")
	if err != nil {
		return 0, "", fmt.Errorf("%s", strings.ReplaceAll(err.Error(), j.password, "[redacted]"))
	}
	out = strings.TrimRight(out, "\n")
	i := strings.LastIndex(out, "\n")
	code, convErr := strconv.Atoi(out[i+1:])
	if convErr != nil {
		return 0, "", fmt.Errorf("unexpected reply: %s", out)
	}
	if i < 0 {
		return code, "", nil
	}
	return code, out[:i], nil
}

func (j jellyfinRun) run(ctx context.Context) (Result, bool, error) {
	base, done, err := j.find(ctx)
	if err != nil {
		return "", false, err
	}
	if done {
		return Ready, true, nil
	}
	step := func(what, method, path string, body any) error {
		code, reply, err := j.call(ctx, method, base, path, body)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if code == 503 {
			// Jellyfin answers /System/Info/Public before it has
			// finished starting; everything else is its "starting"
			// page until then.
			return fmt.Errorf("%s: %w", what, errStarting)
		}
		if code < 200 || code > 299 {
			return fmt.Errorf("%s: HTTP %d%s", what, code, brief(reply))
		}
		return nil
	}
	fail := func(err error) (Result, bool, error) {
		if errors.Is(err, errStarting) {
			// Not answered yet: the whole sequence runs again, and every
			// step is safe to repeat while the wizard is open.
			return "", false, err
		}
		return "", true, fmt.Errorf("Jellyfin refused its startup setup: %w", err)
	}
	if err := step("name the server", "POST", "/Startup/Configuration", map[string]string{
		"ServerName": JellyfinServerName, "UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en",
	}); err != nil {
		return fail(err)
	}
	// The wizard reads the first user before renaming it; Jellyfin
	// creates that user on this read when there is none.
	if err := step("prepare the administrator", "GET", "/Startup/User", nil); err != nil {
		return fail(err)
	}
	if err := step("create the administrator", "POST", "/Startup/User", map[string]string{"Name": j.account, "Password": j.password}); err != nil {
		return fail(err)
	}
	have, err := j.libraryNames(ctx, base)
	if err != nil {
		return fail(err)
	}
	for _, lib := range j.libraries {
		if have[lib.Name] {
			continue
		}
		q := url.Values{"name": {lib.Name}, "collectionType": {lib.Kind}, "paths": {lib.Path}, "refreshLibrary": {"false"}}
		// Watch the folder, so files copied in appear without a scan.
		opts := map[string]any{"LibraryOptions": map[string]any{"EnableRealtimeMonitor": true}}
		if err := step("add the "+lib.Name+" library", "POST", "/Library/VirtualFolders?"+q.Encode(), opts); err != nil {
			return fail(err)
		}
	}
	// Remote access stays on: the edge and the tunnel reach Jellyfin
	// over the network. No port mapping on the router.
	if err := step("allow connections through the edge", "POST", "/Startup/RemoteAccess",
		map[string]bool{"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false}); err != nil {
		return fail(err)
	}
	if err := step("finish the wizard", "POST", "/Startup/Complete", struct{}{}); err != nil {
		return fail(err)
	}
	return Created, true, nil
}

// find returns the base Jellyfin answers on and whether its wizard is
// already complete.
func (j jellyfinRun) find(ctx context.Context) (string, bool, error) {
	var last error
	for _, base := range j.bases {
		code, body, err := j.call(ctx, "GET", base, "/System/Info/Public", nil)
		if err != nil {
			return "", false, err
		}
		if code != 200 {
			last = fmt.Errorf("Jellyfin is starting: HTTP %d at %q", code, base+"/System/Info/Public")
			continue
		}
		var info struct{ StartupWizardCompleted bool }
		if err := json.Unmarshal([]byte(body), &info); err != nil {
			last = fmt.Errorf("Jellyfin is starting: %v", err)
			continue
		}
		return base, info.StartupWizardCompleted, nil
	}
	return "", false, last
}

func (j jellyfinRun) libraryNames(ctx context.Context, base string) (map[string]bool, error) {
	code, body, err := j.call(ctx, "GET", base, "/Library/VirtualFolders", nil)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("list libraries: HTTP %d", code)
	}
	var folders []struct{ Name string }
	if err := json.Unmarshal([]byte(body), &folders); err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	have := map[string]bool{}
	for _, f := range folders {
		have[f.Name] = true
	}
	return have, nil
}
