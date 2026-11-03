package everything

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KV sharing request handling (ADR-058 sections 2, 3 and the SDK surface):
// kv.share, kv.unshare, kv.listShared, kv.listSharedByMe, the {handle}
// option on kv operations, and perms.request. The grants store does the
// authorisation; this file maps requests to it, applies the share-attempt
// rate limit and the recipient-side session-age recheck, and fixes the error
// codes the Tasks app (and the SDK) match on.

// MaxSharedSessionAge is the oldest session that may use a recipient-side
// shared operation. An older one answers 401 and the page signs in again,
// which Authentik refuses for a person whose access was removed.
const MaxSharedSessionAge = 24 * time.Hour

// ShareAttemptsPerHour limits kv.share calls per person per app, failures
// included, so the uniform "no such user" answer cannot be used to scan names.
const ShareAttemptsPerHour = 60

// Page size for listShared and listSharedByMe.
const (
	defaultSharePage = 100
	maxSharePage     = 200
)

// AppID is the constant application id the SDK reports (`puter.appID`): the
// same string for every person of the app.
func AppID(app string) string { return "app-" + app }

// SessionAger is implemented by identities that know how old the caller's
// session is. A recipient-side shared operation from an identity without it
// is refused.
type SessionAger interface {
	SessionAge(*http.Request) (time.Duration, bool)
}

type shareLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// allow records an attempt and reports whether it is within the hourly limit.
func (l *shareLimiter) allow(scope Scope, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-time.Hour)
	key := scope.App + "\x00" + scope.Subject
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= ShareAttemptsPerHour {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 10000 { // bound memory: drop idle people
		for k, v := range l.hits {
			if len(v) == 0 || !v[len(v)-1].After(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func shareErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": code})
}

// shareOp reports whether op is one of the sharing operations.
func shareOp(op string) bool {
	switch op {
	case "kv.share", "kv.unshare", "kv.listShared", "kv.listSharedByMe", "perms.request":
		return true
	}
	return false
}

func (h *handler) fresh(r *http.Request) bool {
	a, ok := h.identity.(SessionAger)
	if !ok {
		return false
	}
	age, ok := a.SessionAge(r)
	return ok && age < MaxSharedSessionAge
}

type shareItem struct {
	Handle    string `json:"handle"`
	Holder    string `json:"holder"`
	Owner     string `json:"owner"`
	Prefix    string `json:"prefix"`
	Access    string `json:"access"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
	RevokedAt *int64 `json:"revokedAt"`
	AppUID    string `json:"appUid"`
}

func toItem(g Grant) shareItem {
	it := shareItem{Handle: g.ID, Holder: g.RecipName, Owner: g.OwnerName, Prefix: g.Key, Access: g.Access, Label: g.Label,
		CreatedAt: g.CreatedAt, AppUID: AppID(g.App)}
	if g.RevokedAt != 0 {
		v := g.RevokedAt
		it.RevokedAt = &v
	}
	return it
}

// accessOf maps the SDK's mode to a stored access. `manage` (re-sharing) is
// not supported by the bridge.
func accessOf(a string) (string, string) {
	switch a {
	case AccessRead, AccessWrite:
		return a, ""
	case "manage":
		return "", "bridge_unsupported"
	}
	return "", "invalid_argument"
}

func validSharePrefix(p string) bool {
	return validText(p, 1024) && !strings.HasSuffix(p, "*")
}

// handleShareOp serves the sharing operations. It always writes a response.
func (h *handler) handleShareOp(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	ctx := r.Context()
	if body.Operation == "perms.request" {
		h.permsRequest(w, r, scope, body)
		return
	}
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "kv_handles_disabled")
		return
	}
	switch body.Operation {
	case "kv.share":
		if !h.limiter.allow(scope, h.now()) {
			shareErr(w, http.StatusTooManyRequests, "share_rate_limited")
			return
		}
		access, bad := accessOf(body.Access)
		if bad != "" {
			shareErr(w, http.StatusBadRequest, bad)
			return
		}
		name := strings.TrimSpace(body.Recipient)
		name = strings.TrimPrefix(name, "@")
		if name == "" || strings.Contains(name, "@") {
			shareErr(w, http.StatusBadRequest, "invalid_recipient") // emails are refused
			return
		}
		if body.Label != "" && !validText(body.Label, 200) {
			shareErr(w, http.StatusBadRequest, "kv_share_label_invalid")
			return
		}
		if !validSharePrefix(body.Prefix) {
			shareErr(w, http.StatusBadRequest, "invalid_argument")
			return
		}
		g, _, err := h.store.CreateGrant(ctx, scope, GrantRequest{Username: name, Kind: GrantKV, Key: body.Prefix, IsPrefix: true,
			Access: access, Label: body.Label}, h.now())
		switch {
		case err == nil:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(toItem(g))
		case errors.Is(err, ErrNoRecipient):
			shareErr(w, http.StatusNotFound, "subject_does_not_exist")
		case errors.Is(err, ErrSelfShare):
			shareErr(w, http.StatusBadRequest, "cannot_share_with_self")
		case errors.Is(err, ErrGrantLimit):
			shareErr(w, http.StatusTooManyRequests, "share_limit")
		case errors.Is(err, ErrInvalid):
			shareErr(w, http.StatusBadRequest, "invalid_argument")
		default:
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
		}
	case "kv.unshare":
		err := h.store.RevokeGrant(ctx, scope, body.Handle, h.now())
		switch {
		case err == nil:
			_, _ = w.Write([]byte(`{"ok":true}`))
		case errors.Is(err, ErrNotFound):
			// Unknown, someone else's and already revoked answer alike; Tasks
			// reads this code as "nothing left to stop".
			shareErr(w, http.StatusNotFound, "subject_does_not_exist")
		default:
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
		}
	case "kv.listShared", "kv.listSharedByMe":
		var gs []Grant
		var err error
		if body.Operation == "kv.listShared" {
			if !h.fresh(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			gs, err = h.store.GrantsForRecipient(ctx, scope, GrantKV)
		} else {
			gs, err = h.store.GrantsByOwner(ctx, scope, GrantKV)
		}
		if err != nil {
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
			return
		}
		items, next, ok := pageGrants(gs, body.Limit, body.Cursor)
		if !ok {
			shareErr(w, http.StatusBadRequest, "invalid_argument")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": next})
	}
}

// pageGrants applies limit and an opaque cursor ("<created>:<id>", the last
// item returned) to grants already sorted by (created, id).
func pageGrants(gs []Grant, limit int, cursor string) ([]shareItem, string, bool) {
	if limit < 0 {
		return nil, "", false
	}
	if limit == 0 {
		limit = defaultSharePage
	}
	if limit > maxSharePage {
		limit = maxSharePage
	}
	var after struct {
		at int64
		id string
	}
	if cursor != "" {
		i := strings.IndexByte(cursor, ':')
		if i < 1 || len(cursor) > 128 {
			return nil, "", false
		}
		n, err := strconv.ParseInt(cursor[:i], 10, 64)
		if err != nil {
			return nil, "", false
		}
		after.at, after.id = n, cursor[i+1:]
	}
	items := []shareItem{}
	next := ""
	for _, g := range gs {
		if cursor != "" && (g.CreatedAt < after.at || (g.CreatedAt == after.at && g.ID <= after.id)) {
			continue
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = strconv.FormatInt(last.CreatedAt, 10) + ":" + last.Handle
			break
		}
		items = append(items, toItem(g))
	}
	return items, next, true
}

// permsRequest implements perms.request: it maps to the sharing permission,
// shows no prompt and keeps no state (the share click is the consent).
func (h *handler) permsRequest(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	ok := false
	if h.store.sharing {
		switch body.Resource {
		case "kvShare":
			_, bad := accessOf(body.Access)
			ok = bad == "" && validSharePrefix(body.Prefix)
		case "permission":
			// manage:kv-share-<read|write>:<uuid>:<appID>:<prefix>
			parts := strings.SplitN(body.Permission, ":", 5)
			if len(parts) == 5 && parts[0] == "manage" && (parts[1] == "kv-share-read" || parts[1] == "kv-share-write") &&
				parts[3] == AppID(scope.App) && validSharePrefix(parts[4]) {
				if p, err := h.store.Person(r.Context(), scope); err == nil && p.UUID != "" && p.UUID == parts[2] {
					ok = true
				}
			}
		}
	}
	if !ok {
		shareErr(w, http.StatusBadRequest, "invalid_argument")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"granted":true}`))
}

// handleKVOp serves kv get/set/del/list/listvalues/putbatch with a {handle}:
// the operation is rewritten to the owner's prefix after the grant is proven
// to belong to the caller. It always writes a response unless it returns a
// storage error for the common mapper.
func (h *handler) handleKVOp(w http.ResponseWriter, r *http.Request, scope Scope, body request) error {
	ctx := r.Context()
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "kv_handles_disabled")
		return nil
	}
	if !h.fresh(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}
	// resolve maps every grant failure to the one handle error.
	var failed bool
	resolve := func(rel string, write bool) Resolved {
		rv, err := h.store.ResolveGrant(ctx, scope, body.Handle, GrantKV, rel, write)
		switch {
		case err == nil:
		case errors.Is(err, ErrNotFound):
			shareErr(w, http.StatusNotFound, "kv_handle_not_found")
			failed = true
		case errors.Is(err, ErrReadOnly):
			shareErr(w, 422, "kv_handle_read_only")
			failed = true
		default:
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
			failed = true
		}
		return rv
	}
	switch body.Operation {
	case "get":
		rv := resolve(body.Key, false)
		if failed {
			return nil
		}
		v, err := h.store.Get(ctx, rv.Owner, KindKV, rv.Key)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(map[string][]byte{"value": v})
	case "put", "delete":
		if body.Key == "" {
			return ErrInvalid // the bare prefix holds no value
		}
		rv := resolve(body.Key, true)
		if failed {
			return nil
		}
		var err error
		if body.Operation == "put" {
			err = h.store.Put(ctx, rv.Owner, KindKV, rv.Key, body.Value)
		} else {
			err = h.store.Delete(ctx, rv.Owner, KindKV, rv.Key)
		}
		if err != nil {
			return err
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
		return nil
	case "putbatch":
		var owner Scope
		entries := make([]Entry, len(body.Entries))
		for i, e := range body.Entries {
			if e.Key == "" {
				return ErrInvalid
			}
			rv := resolve(e.Key, true)
			if failed {
				return nil
			}
			owner = rv.Owner
			entries[i] = Entry{Key: rv.Key, Value: e.Value}
		}
		if len(entries) == 0 {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return nil
		}
		if err := h.store.PutBatch(ctx, owner, KindKV, entries); err != nil {
			return err
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
		return nil
	case "list", "listvalues":
		rv := resolve(body.Key, false)
		if failed {
			return nil
		}
		gp := strings.TrimSuffix(rv.Key, body.Key) // the grant's own prefix
		if body.Operation == "list" {
			keys, err := h.store.List(ctx, rv.Owner, KindKV, rv.Key)
			if err != nil {
				return err
			}
			out := make([]string, 0, len(keys))
			for _, k := range keys {
				out = append(out, strings.TrimPrefix(k, gp))
			}
			return json.NewEncoder(w).Encode(map[string][]string{"keys": out})
		}
		after := ""
		if body.After != "" {
			after = gp + body.After
		}
		entries, next, err := h.store.ListValues(ctx, rv.Owner, KindKV, rv.Key, after)
		if err != nil {
			return err
		}
		out := make([]batchEntry, len(entries))
		for i, e := range entries {
			out[i] = batchEntry{Key: strings.TrimPrefix(e.Key, gp), Value: e.Value}
		}
		if next != "" {
			next = strings.TrimPrefix(next, gp)
		}
		return json.NewEncoder(w).Encode(map[string]any{"entries": out, "next": next})
	}
	return ErrInvalid
}
