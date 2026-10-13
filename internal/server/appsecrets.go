package server

import (
	"errors"
	"fmt"
	"net/http"

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
	s.mux.HandleFunc("GET /api/v1/catalog/{name}/secrets", handleAppSecretsList(catalogDir, secrets))
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
}

func handleAppSecretsList(catalogDir string, secrets *secret.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifest, ok := loadPackage(catalogDir, r.PathValue("name"), w)
		if !ok {
			return
		}
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
}

func (s *Server) handleAppSecretRegister(catalogDir string, secrets *secret.Store, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		manifest, ok := loadPackage(catalogDir, name, w)
		if !ok {
			return
		}
		var body registerBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := catalog.RefuseDuringMigration(name); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		actor := "local"
		if op, ok := operatorFromContext(r.Context()); ok {
			actor = op.Session.Username
		}
		secretName := r.PathValue("secret")
		store, err := catalog.RegisterSecret(r.Context(), manifest, secretName, body.Value, secrets, body.SkipVerify)
		outcome := audit.OutcomeOK
		detail := fmt.Sprintf("registered secret %s for %s (verified: %v)", secretName, name, !body.SkipVerify)
		if err != nil {
			outcome = audit.OutcomeError
			detail = fmt.Sprintf("register secret %s for %s failed", secretName, name)
		}
		if _, aerr := auditSvc.Record(audit.Entry{Actor: actor, Action: "app.secret.register", Outcome: outcome, Detail: detail}); aerr != nil {
			s.log.Error("app secret audit record failed", "package", name, "error", aerr)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		// Wired stacks pick the value up as a file right away; stacks
		// that predate S266 keep their stack untouched.
		dir := docker.DirFor(name)
		if werr := catalog.WriteSecretFilesIfWired(dir, manifest, secrets); werr != nil {
			http.Error(w, werr.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"store_name": store})
	}
}
