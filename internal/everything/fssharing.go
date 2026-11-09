package everything

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
)

// File sharing (ADR-058 section 3, "fs"): fs.share, fs.unshare, fs.getShares,
// fs.listShared, and file operations on the reserved `~shared/<id>/...`
// paths. The grants store does the authorisation (S426); the share-attempt
// rate limit, the session-age recheck and the uniform recipient error are the
// same ones kv sharing uses (S428).

// SharedRoot is the reserved first path segment of a path through a share.
// Ordinary file operations reject it (see validate).
const SharedRoot = "~shared"

// sharedFSPath splits a `~shared/<id>[/<rest>]` key.
func sharedFSPath(key string) (id, rest string, ok bool) {
	if key == SharedRoot {
		return "", "", true
	}
	if !strings.HasPrefix(key, SharedRoot+"/") {
		return "", "", false
	}
	id, rest, _ = strings.Cut(key[len(SharedRoot)+1:], "/")
	return id, rest, true
}

// resolveShared proves the caller holds the grant named by key and returns
// the owner's scope and key. For a single-file grant the path is
// `~shared/<id>` or `~shared/<id>/<file name>`; for a folder grant the rest
// is relative to the folder. Every failure that could reveal anything is
// ErrNotFound; ErrReadOnly is returned only to the proven recipient.
func (h *handler) resolveShared(ctx context.Context, scope Scope, key string, write bool) (Resolved, string, error) {
	id, rest, _ := sharedFSPath(key)
	if strings.HasSuffix(key, "/") {
		return Resolved{}, "", ErrNotFound
	}
	root, err := h.store.ResolveGrant(ctx, scope, id, GrantFS, "", false)
	if err != nil {
		return Resolved{}, "", err
	}
	rel := rest
	if !root.Prefix {
		if rest != "" && rest != path.Base(root.Key) {
			return Resolved{}, "", ErrNotFound
		}
		rel = ""
	}
	rv, err := h.store.ResolveGrant(ctx, scope, id, GrantFS, rel, write)
	return rv, rel, err
}

// handleSharedFS serves a file operation whose key is a `~shared` path. It
// returns nil when it wrote the response, else an error for the common mapper.
func (h *handler) handleSharedFS(w http.ResponseWriter, r *http.Request, scope Scope, body request) error {
	ctx := r.Context()
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "fs_sharing_disabled")
		return nil
	}
	if !h.fresh(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}
	write := false
	switch body.Operation {
	case "get", "stat", "readdir":
	case "put", "delete", "deletetree", "rename":
		write = true
	default:
		return ErrInvalid
	}
	rv, rel, err := h.resolveShared(ctx, scope, body.Key, write)
	if errors.Is(err, ErrReadOnly) {
		shareErr(w, 422, "share_read_only")
		return nil
	}
	if err != nil {
		return err
	}
	switch body.Operation {
	case "get":
		v, err := h.store.Get(ctx, rv.Owner, KindFile, rv.Key)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(map[string][]byte{"value": v})
	case "stat":
		info, err := h.store.Stat(ctx, rv.Owner, rv.Key)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(info)
	case "readdir":
		infos, err := h.store.Readdir(ctx, rv.Owner, rv.Key)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(map[string][]FileInfo{"entries": infos})
	case "put":
		if rv.Prefix && rel == "" {
			return ErrInvalid // the folder itself holds no value
		}
		if !rv.Prefix {
			// A file grant overwrites the shared file; it never creates one.
			if info, err := h.store.Stat(ctx, rv.Owner, rv.Key); err != nil || info.IsDir {
				return ErrNotFound
			}
		}
		// The owner's person and app quotas pay for the bytes.
		if err := h.store.Put(ctx, rv.Owner, KindFile, rv.Key, body.Value); err != nil {
			return err
		}
		return writeStamp(ctx, w, h.store, rv.Owner, rv.Key)
	case "delete", "deletetree":
		if !rv.Prefix || rel == "" {
			return ErrInvalid // a file grant never deletes; the folder itself stays
		}
		if body.Operation == "delete" {
			if err := h.store.Delete(ctx, rv.Owner, KindFile, rv.Key); err != nil {
				return err
			}
		} else if _, err := h.store.DeleteTree(ctx, rv.Owner, rv.Key); err != nil {
			return err
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
		return nil
	default: // rename
		if !rv.Prefix || rel == "" {
			return ErrInvalid // a file grant never renames
		}
		toID, toRest, ok := sharedFSPath(body.To)
		id, _, _ := sharedFSPath(body.Key)
		if !ok || toID != id || toRest == "" || strings.HasSuffix(body.To, "/") {
			return ErrInvalid
		}
		to, _, err := h.resolveShared(ctx, scope, body.To, true)
		if err != nil {
			return err
		}
		if err := h.store.Rename(ctx, rv.Owner, rv.Key, to.Key); err != nil {
			return err
		}
		return writeStamp(ctx, w, h.store, rv.Owner, to.Key)
	}
}

func writeStamp(ctx context.Context, w http.ResponseWriter, s *Store, owner Scope, key string) error {
	if info, err := s.Stat(ctx, owner, key); err == nil {
		return json.NewEncoder(w).Encode(map[string]any{"ok": true, "size": info.Size, "modified": info.Modified})
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
	return nil
}

// recipientName normalises a typed recipient; ok is false for an empty name
// or an email address (the bridge holds none).
func recipientName(v string) (string, bool) {
	name := strings.TrimPrefix(strings.TrimSpace(v), "@")
	return name, name != "" && !strings.Contains(name, "@")
}

// handleFSShareOp serves fs.share, fs.unshare, fs.getShares and fs.listShared.
func (h *handler) handleFSShareOp(w http.ResponseWriter, r *http.Request, scope Scope, body request) {
	ctx := r.Context()
	if !h.store.sharing {
		shareErr(w, http.StatusForbidden, "fs_sharing_disabled")
		return
	}
	storage := func() { http.Error(w, "storage unavailable", http.StatusInternalServerError) }
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch body.Operation {
	case "fs.share", "fs.unshare":
		// Both take a typed name, so both count against the attempt limit:
		// neither may be used to scan for names.
		if !h.limiter.allow(scope, h.now()) {
			shareErr(w, http.StatusTooManyRequests, "share_rate_limited")
			return
		}
		name, ok := recipientName(body.Recipient)
		if !ok {
			shareErr(w, http.StatusBadRequest, "invalid_recipient")
			return
		}
		if validate(scope, KindFile, body.Key) != nil {
			shareErr(w, http.StatusBadRequest, "invalid_argument")
			return
		}
		if body.Operation == "fs.unshare" {
			n, err := h.store.RevokeShare(ctx, scope, name, GrantFS, body.Key, h.now())
			switch {
			case err == nil:
				if n > 0 {
					if rs, rerr := resolveName(ctx, h.store.db, scope.App, name); rerr == nil {
						h.notify(scope.App, rs, "fs.share.revoked", nil)
					}
				}
				reply(map[string]int{"revoked": n})
			case errors.Is(err, ErrNoRecipient):
				reply(map[string]int{"revoked": 0}) // same answer as "no such share"
			default:
				storage()
			}
			return
		}
		access, bad := accessOf(body.Access)
		if bad != "" {
			shareErr(w, http.StatusBadRequest, bad)
			return
		}
		info, err := h.store.Stat(ctx, scope, body.Key)
		if errors.Is(err, ErrNotFound) {
			shareErr(w, http.StatusNotFound, "path_not_found")
			return
		}
		if err != nil {
			storage()
			return
		}
		g, isNew, err := h.store.CreateGrant(ctx, scope, GrantRequest{Username: name, Kind: GrantFS, Key: body.Key,
			IsPrefix: info.IsDir, Access: access}, h.now())
		if err != nil {
			grantErr(w, err, "user_does_not_exist")
			return
		}
		h.notify(scope.App, g.Recipient, "fs.share.received", map[string]any{"id": g.ID})
		reply(map[string]any{"isNew": isNew, "holder": g.RecipName, "mode": g.Access, "path": body.Key})
	case "fs.getShares":
		if validate(scope, KindFile, body.Key) != nil {
			shareErr(w, http.StatusBadRequest, "invalid_argument")
			return
		}
		gs, err := h.store.GrantsByOwner(ctx, scope, GrantFS)
		if err != nil {
			storage()
			return
		}
		items := []map[string]any{}
		for _, g := range gs {
			if g.RevokedAt != 0 {
				continue
			}
			switch {
			case g.Key == body.Key:
				items = append(items, map[string]any{"holder": g.RecipName, "mode": g.Access, "inheritedFrom": nil})
			case g.IsPrefix && strings.HasPrefix(body.Key, g.Key+"/"):
				items = append(items, map[string]any{"holder": g.RecipName, "mode": g.Access, "inheritedFrom": g.Key})
			}
		}
		reply(map[string]any{"items": items})
	case "fs.listShared":
		if !h.fresh(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		gs, err := h.store.GrantsForRecipient(ctx, scope, GrantFS)
		if err != nil {
			storage()
			return
		}
		page, next, ok := pageGrantRows(gs, body.Limit, body.Cursor)
		if !ok {
			shareErr(w, http.StatusBadRequest, "invalid_argument")
			return
		}
		items := []map[string]any{}
		for _, g := range page {
			owner := Scope{App: g.App, Subject: g.Owner}
			info, err := h.store.Stat(ctx, owner, g.Key)
			if err != nil {
				continue // the owner removed the file: nothing to open
			}
			p := SharedRoot + "/" + g.ID
			if !g.IsPrefix {
				p += "/" + info.Name
			}
			items = append(items, map[string]any{"path": p, "name": info.Name, "owner": g.OwnerName, "mode": g.Access,
				"modified": info.Modified, "size": info.Size, "is_dir": info.IsDir})
		}
		reply(map[string]any{"items": items, "cursor": next})
	}
}
