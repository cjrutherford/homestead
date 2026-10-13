package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// MountAppSecrets serves package secret-backed variables (S266,
// ADR-021): which secrets a package declares and whether each is
// present, and registration of an existing secret for installs that
// predate S266. Responses and audit carry names, never values.
func (s *Server) MountAppSecrets(st *store.Store, catalogDir string, secrets *secret.Store) {
	auditSvc := audit.New(st)
	s.reconcileRotations(st, catalogDir, secrets)
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/secrets", handleAppSecretsList(st, catalogDir, secrets))
	s.mux.HandleFunc("POST /api/v1/catalog/{name}/secrets/{secret}", s.handleAppSecretRegister(catalogDir, secrets, auditSvc))
}

// appSecretStatus describes one declared secret without its value.
type appSecretStatus struct {
	Name      string   `json:"name"`
	StoreName string   `json:"store_name"`
	Present   bool     `json:"present"`
	Immutable bool     `json:"immutable"`
	Services  []string `json:"services"`
	Database  bool     `json:"database_password"`
	// Wired is true when the installed stack carries secret wiring;
	// Installed reports a stack exists. Installed and not wired means
	// the stack predates S266 and can adopt its secrets.
	Wired     bool `json:"wired"`
	Installed bool `json:"installed"`
}

func handleAppSecretsList(st *store.Store, catalogDir string, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
		installed, _ := catalog.Installed(st, manifest.Name, docker.DirFor(manifest.Name))
		wired := catalog.SecretsWired(docker.DirFor(manifest.Name), manifest)
		out := []appSecretStatus{}
		for _, decl := range manifest.Secrets {
			store := catalog.SecretStoreName(manifest.Name, decl.Name)
			_, err := secrets.Get(store)
			if err != nil && !errors.Is(err, secret.ErrNotFound) {
				http.Error(w, "secret store unavailable", http.StatusInternalServerError)
				return
			}
			entry := appSecretStatus{
				Name: decl.Name, StoreName: store, Present: err == nil, Immutable: decl.Immutable,
				Database: manifest.Database != nil && manifest.Database.PasswordSecret == decl.Name,
				Wired:    wired, Installed: installed,
			}
			for _, c := range decl.Consumers {
				entry.Services = append(entry.Services, c.Service)
			}
			out = append(out, entry)
		}
		writeJSON(w, out)
	}
}

// registerBody carries the existing value once; it is never stored
// outside the secret store nor echoed.
type registerBody struct {
	Value      string `json:"value"`
	SkipVerify bool   `json:"skip_verify"`
	// Force overwrites an existing value without verification (audited).
	Force bool `json:"force"`
}

func (s *Server) handleAppSecretRegister(catalogDir string, secrets *secret.Store, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		manifest, ok := loadPackage(catalogDir, name, w)
		if !ok {
			return
		}
		var body registerBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body); err != nil {
			// Generic on purpose: decode errors can echo input.
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := catalog.RefuseDuringMigration(name); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		actor := operatorActor(r)
		secretName := r.PathValue("secret")
		result, err := catalog.RegisterSecret(r.Context(), manifest, secretName, body.Value, secrets,
			catalog.RegisterOptions{SkipVerify: body.SkipVerify, Force: body.Force})
		outcome := audit.OutcomeOK
		detail := fmt.Sprintf("registered secret %s for %s (verify skipped: %v, forced: %v, changed: %v)", secretName, name, body.SkipVerify, body.Force, result.Changed)
		if err != nil {
			outcome = audit.OutcomeError
			detail = fmt.Sprintf("register secret %s for %s refused or failed", secretName, name)
		}
		if _, aerr := auditSvc.Record(audit.Entry{Actor: actor, Action: "app.secret.register", Outcome: outcome, Detail: detail}); aerr != nil {
			s.log.Error("app secret audit record failed", "package", name, "error", aerr)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		// A changed value on a wired stack must reach the containers:
		// files rewritten and consumers restarted. Stacks that predate
		// S266 stay untouched until adopted.
		if result.Changed {
			if aerr := catalog.ApplyRegistered(r.Context(), manifest, docker.DirFor(name), name, secretName, secrets); aerr != nil {
				http.Error(w, "registered, but applying it to the running stack failed: "+aerr.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, map[string]any{"store_name": result.StoreName})
	}
}

// reconcileRotations settles interrupted secret rotations at startup
// (S266): a pending journal row means the daemon stopped between
// changing the database and settling the store, so it asks the database
// which password it accepts and makes store and files agree. The
// database may still be starting, so unresolved rows retry for a while.
func (s *Server) reconcileRotations(st *store.Store, catalogDir string, secrets *secret.Store) {
	auditSvc := audit.New(st)
	ctx := s.bg
	if ctx == nil {
		return
	}
	go func() {
		for attempt := 0; attempt < 8; attempt++ {
			if s.reconcileOnce(ctx, auditSvc, catalogDir, secrets) == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
}

// reconcileOnce returns how many pending rotations remain unresolved.
func (s *Server) reconcileOnce(ctx context.Context, auditSvc *audit.Service, catalogDir string, secrets *secret.Store) int {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return 0
	}
	unresolved := 0
	for _, name := range entries.Names() {
		manifest, err := entries.Get(name)
		if err != nil || len(manifest.Secrets) == 0 || !catalog.SecretsWired(docker.DirFor(name), manifest) {
			continue
		}
		for _, decl := range manifest.Secrets {
			result, err := catalog.ReconcileRotation(ctx, manifest, docker.DirFor(name), name, decl.Name, secrets)
			switch {
			case err != nil:
				unresolved++
				s.log.Warn("pending secret rotation not settled", "package", name, "secret", decl.Name, "error", err)
			case result != "":
				_, _ = auditSvc.Record(audit.Entry{Actor: "system", Action: "app.secret.reconcile", Outcome: audit.OutcomeOK,
					Detail: fmt.Sprintf("%s %s: %s", name, decl.Name, result)})
			}
		}
	}
	return unresolved
}
