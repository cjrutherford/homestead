package craftfiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// nextcloud performs the few non-proxy calls the link needs: credential
// verification, root folder creation and the app-password endpoints. It
// never returns upstream bodies in errors.
type nextcloud struct {
	client   *http.Client
	link     Link
	password string
}

func (n *nextcloud) do(ctx context.Context, method, target, user, pass string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("User-Agent", "homestead-craftfiles")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%w: could not reach %s: %v", ErrUpstream, n.link.BaseURL, err)
	}
	return resp, nil
}

func (n *nextcloud) davBase() string {
	return strings.TrimSuffix(n.link.BaseURL, "/") + "/remote.php/dav/files/" + url.PathEscape(n.link.Account)
}

func statusErr(what string, code int) error {
	switch code {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: Nextcloud rejected the credential", ErrUpstream)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s not found (is this the Nextcloud address and account?)", ErrUpstream, what)
	}
	return fmt.Errorf("%w: %s answered %d", ErrUpstream, what, code)
}

// verify proves the credential against the account's file space.
func (n *nextcloud) verify(ctx context.Context) error {
	resp, err := n.do(ctx, "PROPFIND", n.davBase()+"/", n.link.Account, n.password, map[string]string{"Depth": "0"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusMultiStatus {
		return statusErr("the account's file space", resp.StatusCode)
	}
	return nil
}

// ensureRoot creates the root folder path segment by segment; an
// existing folder is fine (405).
func (n *nextcloud) ensureRoot(ctx context.Context) error {
	cur := n.davBase()
	for _, seg := range strings.Split(n.link.Root, "/") {
		cur += "/" + url.PathEscape(seg)
		resp, err := n.do(ctx, "MKCOL", cur, n.link.Account, n.password, nil)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusCreated, http.StatusMethodNotAllowed:
		default:
			return statusErr("creating the root folder", resp.StatusCode)
		}
	}
	return nil
}

// createAppPassword asks Nextcloud for an app password with the account
// password (used once, never kept).
func (n *nextcloud) createAppPassword(ctx context.Context, accountPassword string) (string, error) {
	target := strings.TrimSuffix(n.link.BaseURL, "/") + "/ocs/v2.php/core/getapppassword?format=json"
	resp, err := n.do(ctx, http.MethodGet, target, n.link.Account, accountPassword, map[string]string{"OCS-APIRequest": "true", "Accept": "application/json"})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", statusErr("the app-password endpoint", resp.StatusCode)
	}
	var doc struct {
		OCS struct {
			Data struct {
				AppPassword string `json:"apppassword"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.OCS.Data.AppPassword == "" {
		return "", fmt.Errorf("%w: the app-password endpoint returned no app password (accounts with two-factor authentication need a manually created one)", ErrUpstream)
	}
	return doc.OCS.Data.AppPassword, nil
}

// revoke deletes the app password used to authenticate (best effort).
func (n *nextcloud) revoke(ctx context.Context) error {
	target := strings.TrimSuffix(n.link.BaseURL, "/") + "/ocs/v2.php/core/apppassword"
	resp, err := n.do(ctx, http.MethodDelete, target, n.link.Account, n.password, map[string]string{"OCS-APIRequest": "true"})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
