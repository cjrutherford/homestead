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
	status, err := waitFor(ctx, m.Title, func() (Result, bool, error) {
		code, body, err := komgaHTTP(ctx, container, "GET", "/api/v1/claim", "", "")
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
			return "", true, errors.New("Komga already has accounts; Homestead refuses to claim or link an existing database")
		}
		return Ready, true, nil
	})
	if err != nil {
		return "", err
	}
	if status != Ready {
		return "", errors.New("Komga returned an unexpected first-run state")
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	code, _, err := komgaHTTP(ctx, container, "POST", "/api/v1/claim", f.Account, password)
	if err != nil {
		return "", errors.New("Komga claim request failed; inspect the local recovery account before retrying")
	}
	if code != 200 && code != 201 {
		return "", fmt.Errorf("Komga refused the fresh claim (HTTP %d); inspect existing accounts before retrying", code)
	}
	code, body, err := komgaHTTP(ctx, container, "GET", "/api/v2/users/me", f.Account, password)
	if err != nil || code != 200 {
		return "", errors.New("Komga created an account but recovery login could not be verified; inspect it before retrying")
	}
	var me struct {
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	}
	if json.Unmarshal([]byte(body), &me) != nil || !strings.EqualFold(me.Email, f.Account) {
		return "", errors.New("Komga recovery login returned an unexpected account")
	}
	for _, role := range me.Roles {
		if role == "ADMIN" {
			return Created, nil
		}
	}
	return "", errors.New("Komga recovery account does not have administrator access")
}

// curl is in the pinned Komga image. Its config (including the password)
// travels over docker exec stdin, never through argv or environment. Only
// fixed endpoints and the manifest's fixed recovery address are accepted.
func komgaHTTP(ctx context.Context, container, method, path, email, password string) (int, string, error) {
	if method != "GET" && method != "POST" {
		return 0, "", errors.New("unsupported Komga method")
	}
	if path != "/api/v1/claim" && path != "/api/v2/users/me" {
		return 0, "", errors.New("unsupported Komga endpoint")
	}
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(password, "\r\n\"\\") {
			return 0, "", errors.New("invalid Komga recovery credentials")
		}
	}
	config := "url = \"http://127.0.0.1:25600" + path + "\"\n"
	if method == "POST" {
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
