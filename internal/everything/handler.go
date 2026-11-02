package everything

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// Identity is implemented by the bridge's future standalone authentication
// service. Both methods must validate server-held session state; neither may
// trust a subject or app supplied in the request. No implementation is wired
// to production yet.
type Identity interface {
	Authenticate(*http.Request) (Scope, error)
	CheckCSRF(*http.Request) bool
}

type handler struct {
	store    *Store
	identity Identity
	origins  map[string]bool
}

// NewHandler refuses to start without an identity verifier and an explicit
// allowlist of trusted browser origins. This prototype is not an auth system.
func NewHandler(store *Store, identity Identity, origins []string) (http.Handler, error) {
	if store == nil || identity == nil || len(origins) == 0 {
		return nil, ErrInvalid
	}
	v := reflect.ValueOf(identity)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return nil, ErrInvalid
		}
	}
	allowed := make(map[string]bool, len(origins))
	for _, raw := range origins {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "* ") {
			return nil, ErrInvalid
		}
		allowed[raw] = true
	}
	return &handler{store: store, identity: identity, origins: allowed}, nil
}

type request struct {
	Operation string       `json:"operation"`
	Kind      string       `json:"kind"`
	Key       string       `json:"key"`
	Value     []byte       `json:"value"`
	Entries   []batchEntry `json:"entries"`
}

type batchEntry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.origins[r.Header.Get("Origin")] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.identity.Authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.identity.CheckCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12*1024*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body request
	if err = dec.Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if body.Operation == "whoami" {
		// The caller's own verified identity; the client never supplies it.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"app": scope.App, "subject": scope.Subject})
		return
	}
	var kind Kind
	switch body.Kind {
	case "kv":
		kind = KindKV
	case "file":
		kind = KindFile
	default:
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch body.Operation {
	case "put":
		err = h.store.Put(r.Context(), scope, kind, body.Key, body.Value)
		if err == nil {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
	case "putbatch":
		entries := make([]Entry, len(body.Entries))
		for i, e := range body.Entries {
			entries[i] = Entry{Key: e.Key, Value: e.Value}
		}
		err = h.store.PutBatch(r.Context(), scope, kind, entries)
		if err == nil {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
	case "get":
		var value []byte
		value, err = h.store.Get(r.Context(), scope, kind, body.Key)
		if err == nil {
			_ = json.NewEncoder(w).Encode(map[string][]byte{"value": value})
			return
		}
	case "delete":
		err = h.store.Delete(r.Context(), scope, kind, body.Key)
		if err == nil {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
	case "list":
		var keys []string
		keys, err = h.store.List(r.Context(), scope, kind, body.Key)
		if err == nil {
			if keys == nil {
				keys = []string{}
			}
			_ = json.NewEncoder(w).Encode(map[string][]string{"keys": keys})
			return
		}
	default:
		err = ErrInvalid
	}
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, ErrInvalid):
		http.Error(w, "invalid request", http.StatusBadRequest)
	default:
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
	}
}
