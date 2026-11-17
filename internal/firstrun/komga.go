package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"homestead/internal/catalog"
)

// Komga 1.29.1 exposes an unauthenticated claim only while its user table is
// empty. A claimed database is never modified: email matching in Komga's
// OIDC implementation cannot prove ownership of an existing local account.
func komgaFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "komga" || f.Service != "komga" || f.Account != "homestead-recovery@localhost.invalid" {
		return "", errors.New("Komga recovery account configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	// Komga serves under its context path (S474); an install made before the
	// move still answers at the root, so the claim probe tries both.
	base := &komgaBase{bases: []string{strings.TrimSuffix(m.AppPath(), "/")}}
	if base.bases[0] != "" {
		base.bases = append(base.bases, "")
	}
	ctx = context.WithValue(ctx, komgaBaseKey{}, base)
	status, err := waitFor(ctx, m.Title, func() (Result, bool, error) {
		code, body, err := komgaHTTP(ctx, container, "GET", "/api/v1/claim", "", "", "")
		for code == 404 && err == nil && base.next() {
			code, body, err = komgaHTTP(ctx, container, "GET", "/api/v1/claim", "", "", "")
		}
		if err != nil {
			return "", false, errors.New("Komga is starting")
		}
		if code != 200 {
			return "", true, fmt.Errorf("Komga claim status returned HTTP %d", code)
		}
		var state struct {
			IsClaimed *bool `json:"isClaimed"`
		}
		if json.Unmarshal([]byte(body), &state) != nil || state.IsClaimed == nil {
			return "", true, errors.New("Komga returned an unexpected claim status")
		}
		if *state.IsClaimed {
			return Result("claimed"), true, nil
		}
		return Ready, true, nil
	})
	if err != nil {
		return "", err
	}
	if status != Ready && status != Result("claimed") {
		return "", errors.New("Komga returned an unexpected first-run state")
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	if status == Result("claimed") {
		// Only Homestead's own recovery account may be used to finish an
		// interrupted setup (a claim whose library step failed). Any
		// other database is refused untouched.
		if err := komgaVerifyRecovery(ctx, container, f.Account, password); err != nil {
			return "", errors.New("Komga already has accounts; Homestead refuses to claim or link an existing database")
		}
		if err := komgaLibraries(ctx, container, f.Account, password, f.Libraries); err != nil {
			return "", err
		}
		return Ready, nil
	}
	code, _, err := komgaHTTP(ctx, container, "POST", "/api/v1/claim", f.Account, password, "")
	if err != nil {
		return "", errors.New("Komga claim request failed; inspect the local recovery account before retrying")
	}
	if code != 200 && code != 201 {
		return "", fmt.Errorf("Komga refused the fresh claim (HTTP %d); inspect existing accounts before retrying", code)
	}
	if err := komgaVerifyRecovery(ctx, container, f.Account, password); err != nil {
		return "", err
	}
	if err := komgaLibraries(ctx, container, f.Account, password, f.Libraries); err != nil {
		return "", err
	}
	return Created, nil
}

// komgaVerifyRecovery proves the recovery account logs in and is an
// administrator.
func komgaVerifyRecovery(ctx context.Context, container, account, password string) error {
	code, body, err := komgaHTTP(ctx, container, "GET", "/api/v2/users/me", account, password, "")
	if err != nil || code != 200 {
		return errors.New("Komga created an account but recovery login could not be verified; inspect it before retrying")
	}
	var me struct {
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	}
	if json.Unmarshal([]byte(body), &me) != nil || !strings.EqualFold(me.Email, account) {
		return errors.New("Komga recovery login returned an unexpected account")
	}
	for _, role := range me.Roles {
		if role == "ADMIN" {
			return nil
		}
	}
	return errors.New("Komga recovery account does not have administrator access")
}

// komgaLibraries adds each declared library the server lacks (matched by
// name). Komga scans a new library at once, and its default is every
// user, so people who sign in later see it. Files the owner adds later are
// found by a scan at startup and every hour (S452: the file watcher does
// not see changes on the bind mount, and an ordinary owner cannot scan).
func komgaLibraries(ctx context.Context, container, account, password string, libs []catalog.Library) error {
	if len(libs) == 0 {
		return nil
	}
	code, body, err := komgaHTTP(ctx, container, "GET", "/api/v1/libraries", account, password, "")
	if err != nil || code != 200 {
		return errors.New("Komga could not list its libraries")
	}
	var have []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(body), &have) != nil {
		return errors.New("Komga returned an unexpected library list")
	}
	names := map[string]bool{}
	for _, h := range have {
		names[h.Name] = true
	}
	for _, lib := range libs {
		if names[lib.Name] {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"name": lib.Name, "root": lib.Path, "scanOnStartup": true, "scanInterval": "HOURLY"})
		code, _, err := komgaHTTP(ctx, container, "POST", "/api/v1/libraries", account, password, string(payload))
		if err != nil || code != 200 && code != 201 {
			return fmt.Errorf("Komga could not add the %s library", lib.Name)
		}
	}
	return nil
}

type komgaBaseKey struct{}

// komgaBase is the URL prefix Komga answers on, found by the claim probe.
type komgaBase struct {
	bases []string
	i     int
}

func (b *komgaBase) current() string { return b.bases[b.i] }

// next moves to the next candidate prefix, reporting whether there was one.
func (b *komgaBase) next() bool {
	if b.i+1 >= len(b.bases) {
		return false
	}
	b.i++
	return true
}

// curl is in the pinned Komga image. Its config (including the password)
// travels over docker exec stdin, never through argv or environment. Only
// fixed endpoints and the manifest's fixed recovery address are accepted.
func komgaHTTP(ctx context.Context, container, method, path, email, password, jsonBody string) (int, string, error) {
	if method != "GET" && method != "POST" {
		return 0, "", errors.New("unsupported Komga method")
	}
	if path != "/api/v1/claim" && path != "/api/v2/users/me" && path != "/api/v1/libraries" {
		return 0, "", errors.New("unsupported Komga endpoint")
	}
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(password, "\r\n\"\\") {
			return 0, "", errors.New("invalid Komga recovery credentials")
		}
	}
	prefix := ""
	if b, ok := ctx.Value(komgaBaseKey{}).(*komgaBase); ok {
		prefix = b.current()
	}
	config := "url = \"http://127.0.0.1:25600" + prefix + path + "\"\n"
	if method == "POST" && path == "/api/v1/libraries" {
		if email == "" || strings.ContainsAny(jsonBody, "\r\n") {
			return 0, "", errors.New("invalid Komga library request")
		}
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(jsonBody)
		config += "request = \"POST\"\nheader = \"Content-Type: application/json\"\ndata = \"" + escaped + "\"\nuser = \"" + email + ":" + password + "\"\n"
	} else if method == "POST" {
		config += "request = \"POST\"\nheader = \"X-Komga-Email: " + email + "\"\nheader = \"X-Komga-Password: " + password + "\"\n"
	} else if email != "" {
		config += "user = \"" + email + ":" + password + "\"\n"
	}
	raw, err := execIn(ctx, container, config, "curl", "--silent", "--show-error", "--max-time", "10", "--connect-timeout", "3", "--config", "-", "--write-out", "\\n%{http_code}")
	if err != nil {
		return 0, "", errors.New("Komga HTTP request failed")
	}
	idx := strings.LastIndexByte(raw, '\n')
	if idx < 0 {
		return 0, "", errors.New("Komga HTTP response had no status")
	}
	var code int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw[idx+1:]), "%d", &code); err != nil || code < 100 || code > 599 {
		return 0, "", errors.New("Komga HTTP response had an invalid status")
	}
	return code, raw[:idx], nil
}
