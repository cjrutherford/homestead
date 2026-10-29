package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/people"
	"homestead/internal/sso"
)

// People (S317, ADR-043 section 4): the household's people, kept in
// Homestead and held in Authentik. Every route is session class and takes
// "confirm": true in its body. Admin grants, password resets, second-factor
// resets, removal and drift resolution are step-up. People are never
// Homestead administrators: nothing here touches the ADR-033 owner link.
// The Authentik token is the kept one only (ADR-033 opt-in); a credential
// the API reveals is returned once and never stored or logged.

// peopleTokenWords says plainly what the kept token can do.
const peopleTokenWords = "The Authentik API token you keep can do anything an Authentik administrator can; Authentik tokens are not narrowly scoped. Homestead keeps it sealed and uses it only for the actions you take on this page and for the read-back. You can stop keeping it by turning off sign-in with Authentik."

type peopleHandlers struct {
	s   *Server
	m   *sso.Manager
	cfg SSOConfig
	svc *people.Service
}

func (s *Server) mountPeople(c SSOConfig, m *sso.Manager, client func() *http.Client, record func(actor, action, outcome, detail string)) {
	h := &peopleHandlers{s: s, m: m, cfg: c}
	h.svc = &people.Service{
		DB: c.Store.DB(), Audit: record, Now: c.Now,
		API: func() (*sso.API, error) {
			spec, ok, err := m.Config()
			if err != nil {
				return nil, err
			}
			if !ok || c.Secrets == nil {
				return nil, fmt.Errorf("%w: sign-in with Authentik is not set up", people.ErrNoToken)
			}
			token, err := c.Secrets.Get(sso.APITokenName)
			if err != nil || token == "" {
				return nil, people.ErrNoToken
			}
			return &sso.API{Base: spec.AuthentikURL, Token: token, Client: client}, nil
		},
		Apps: func() ([]string, error) { return (appsso.Records{DB: c.Store.DB()}).Packages() },
		PublicAuthentik: func() string {
			if spec, ok, err := m.Config(); err == nil && ok {
				return spec.PublicAuthentikURL
			}
			return ""
		},
		OwnerSub: func() string {
			if st, err := m.Status(); err == nil && st.Offered {
				return st.Subject
			}
			return ""
		},
	}
	if c.Auth != nil {
		h.svc.EndSessions = func(id int64) {
			if err := c.Auth.RevokePersonSessions(id); err != nil {
				s.log.Warn("could not end a person's sessions", "person", id, "error", err)
			}
		}
	}
	m.Person = func(ctx context.Context, sub string) (sso.PersonMatch, bool, error) {
		id, username, name, ok, err := h.svc.SignIn(ctx, sub)
		return sso.PersonMatch{ID: id, Username: username, DisplayName: name}, ok, err
	}
	m.SyncPeople = h.svc.SyncGroup
	s.people = h.svc
	s.mux.HandleFunc("GET /api/v1/people", h.list)
	s.mux.HandleFunc("POST /api/v1/people", h.add)
	s.mux.HandleFunc("POST /api/v1/people/adopt", h.adopt)
	s.mux.HandleFunc("PATCH /api/v1/people/{id}", h.edit)
	s.mux.HandleFunc("POST /api/v1/people/{id}/password-reset", h.passwordReset)
	s.mux.HandleFunc("POST /api/v1/people/{id}/mfa-reset", h.mfaReset)
	s.mux.HandleFunc("POST /api/v1/people/{id}/disable", h.disable)
	s.mux.HandleFunc("POST /api/v1/people/{id}/enable", h.enable)
	s.mux.HandleFunc("POST /api/v1/people/{id}/remove", h.remove)
	s.mux.HandleFunc("PUT /api/v1/people/{id}/apps/{app}", h.grantUser)
	s.mux.HandleFunc("PUT /api/v1/people/{id}/apps/{app}/admin", h.grantAdmin)
	s.mux.HandleFunc("POST /api/v1/people/{id}/apps/{app}/revoke", h.revoke)
	s.mux.HandleFunc("POST /api/v1/people/{id}/resolve", h.resolve)
}

// peopleView is the list response.
type peopleView struct {
	people.View
	// Owner is the signed-in owner, who always has every app.
	Owner string `json:"owner"`
	// Reason is why Homestead cannot act: no-sign-in or no-token. Empty when it can.
	Reason string `json:"reason,omitempty"`
	// TokenWords says plainly what keeping the token allows.
	TokenWords string `json:"token_words"`
}

func (h *peopleHandlers) list(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, err := h.svc.List(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := peopleView{View: v, Owner: op.Session.Username, TokenWords: peopleTokenWords}
	if !v.TokenKept {
		out.Reason = "no-token"
		if _, ok, _ := h.m.Config(); !ok {
			out.Reason = "no-sign-in"
		}
	}
	writeJSON(w, out)
}

// fail maps a People error to a status. Authentik's own words are never
// passed through; only the reasons this package writes.
func (h *peopleHandlers) fail(w http.ResponseWriter, err error) {
	var ae *sso.APIError
	switch {
	case errors.Is(err, people.ErrNoToken):
		w.Header().Set(ErrorCodeHeader, "people-no-token")
		http.Error(w, "Homestead cannot manage people without a kept Authentik API token. "+peopleTokenWords+" Keep one from Settings, Sign-in (homesteadctl sso approve --keep-token).", http.StatusUnprocessableEntity)
	case errors.Is(err, people.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, people.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, people.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, people.ErrRefused):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.As(err, &ae), errors.Is(err, sso.ErrUnreachable):
		h.s.log.Warn("people: Authentik call failed", "error", err)
		http.Error(w, "Authentik could not do that: "+readBackReason(err), http.StatusBadGateway)
	default:
		h.s.log.Warn("people request failed", "error", err)
		http.Error(w, "the people request failed", http.StatusInternalServerError)
	}
}

// start decodes a mutating request: an operator, a body, and confirm.
func (h *peopleHandlers) start(w http.ResponseWriter, r *http.Request, body interface{ confirmed() bool }) (actor string, ok bool) {
	op, ok := requireOperator(w, r)
	if !ok {
		return "", false
	}
	if body != nil {
		if err := decodeBody(r, body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return "", false
		}
		if !body.confirmed() {
			http.Error(w, "this change requires \"confirm\": true", http.StatusBadRequest)
			return "", false
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	return op.Session.Username, true
}

type confirmBody struct {
	Confirm bool `json:"confirm"`
}

func (b *confirmBody) confirmed() bool { return b.Confirm }

func personID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "no such person", http.StatusNotFound)
		return 0, false
	}
	return id, true
}

func (h *peopleHandlers) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 60*time.Second)
}

type addBody struct {
	confirmBody
	people.AddRequest
}

func (b *addBody) confirmed() bool { return b.Confirm }

func (h *peopleHandlers) add(w http.ResponseWriter, r *http.Request) {
	var body addBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.svc.Add(ctx, actor, body.AddRequest)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, res)
}

type adoptBody struct {
	confirmBody
	Username string `json:"username"`
}

func (b *adoptBody) confirmed() bool { return b.Confirm }

func (h *peopleHandlers) adopt(w http.ResponseWriter, r *http.Request) {
	var body adoptBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	p, err := h.svc.Adopt(ctx, actor, body.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"person": p})
}

type editBody struct {
	confirmBody
	people.EditRequest
}

func (b *editBody) confirmed() bool { return b.Confirm }

func (h *peopleHandlers) edit(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body editBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	p, err := h.svc.Edit(ctx, actor, id, body.EditRequest)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"person": p})
}

type resetBody struct {
	confirmBody
	Method   string `json:"method"`
	Password string `json:"password,omitempty"`
}

func (b *resetBody) confirmed() bool { return b.Confirm }

func (h *peopleHandlers) passwordReset(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body resetBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	rev, err := h.svc.ResetPassword(ctx, actor, id, body.Method, body.Password)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"revealed": rev})
}

func (h *peopleHandlers) mfaReset(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body confirmBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	removed, skipped, err := h.svc.ResetSecondFactor(ctx, actor, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"removed": removed, "skipped": skipped})
}

func (h *peopleHandlers) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body confirmBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if err := h.svc.SetActive(ctx, actor, id, active); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *peopleHandlers) disable(w http.ResponseWriter, r *http.Request) { h.setActive(w, r, false) }
func (h *peopleHandlers) enable(w http.ResponseWriter, r *http.Request)  { h.setActive(w, r, true) }

func (h *peopleHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body confirmBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if err := h.svc.Remove(ctx, actor, id); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "note": "App data stays in the apps."})
}

func (h *peopleHandlers) grant(w http.ResponseWriter, r *http.Request, role string) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body confirmBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	p, err := h.svc.Grant(ctx, actor, id, people.Grant{App: r.PathValue("app"), Role: role})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"person": p})
}

func (h *peopleHandlers) grantUser(w http.ResponseWriter, r *http.Request) {
	h.grant(w, r, people.RoleUser)
}

func (h *peopleHandlers) grantAdmin(w http.ResponseWriter, r *http.Request) {
	h.grant(w, r, people.RoleAdmin)
}

func (h *peopleHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body confirmBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	p, err := h.svc.Revoke(ctx, actor, id, r.PathValue("app"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"person": p})
}

type resolveBody struct {
	confirmBody
	Action string `json:"action"`
	Method string `json:"method"`
}

func (b *resolveBody) confirmed() bool { return b.Confirm }

func (h *peopleHandlers) resolve(w http.ResponseWriter, r *http.Request) {
	id, ok := personID(w, r)
	if !ok {
		return
	}
	var body resolveBody
	actor, ok := h.start(w, r, &body)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	rev, err := h.svc.Resolve(ctx, actor, id, body.Action, body.Method)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"revealed": rev})
}
