package server

import (
	"net/http"
	"path/filepath"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/backup"
	"homestead/internal/claim"
	"homestead/internal/config"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/firewall"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// AppConfig is everything MountApp needs to register the production TCP
// route set. cmd/homestead-server and the route-classification test both
// call MountApp, so wiring drift cannot hide from the test (ADR-020).
type AppConfig struct {
	Store         *store.Store
	ConfigPath    string
	CatalogDir    string
	SecretKeyPath string
	// Port is the daemon's serving port, for firewall self-protection.
	Port int
	// Dashboard serves the static UI catch-all; nil leaves it unmounted.
	Dashboard http.Handler
}

// AppServices are the services MountApp built, for the caller's own
// wiring (boundary, claim token, discovery).
type AppServices struct {
	Claim *claim.Service
	Auth  *auth.Service
	// Scheduler is built and mounted for status; the daemon runs it (S260).
	Scheduler *scheduler.Scheduler
}

// MountApp registers every production TCP route on srv.
func MountApp(srv *Server, c AppConfig) (*AppServices, error) {
	st := c.Store
	backup.SweepScratch()
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	configSvc := config.New(firewall.Schema(), c.ConfigPath)
	srv.MountConfig(configSvc)
	secrets, err := secret.New(st, c.SecretKeyPath)
	if err != nil {
		return nil, err
	}
	srv.MountConfigIO(c.ConfigPath, secrets)
	srv.MountSecrets(st, secrets)
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountAudit(audit.New(st))
	srv.MountRecovery(authSvc)
	srv.MountWizards(wizard.New(st))
	srv.MountDoctor(
		func() []doctor.Check { return []doctor.Check{driftDoctorCheck(st, time.Now)()} },
		certDoctorChecks(st, time.Now),
		platformDoctorCheck(st),
		migrationDoctorChecks(st, c.CatalogDir),
		backupEncryptionDoctorCheck,
	)
	srv.MountTLSRenewal(st, time.Now)
	srv.MountOperations(operation.New(st))
	srv.MountCatalog(st, c.CatalogDir, secrets)
	srv.MountAppSecrets(st, c.CatalogDir, secrets)
	srv.MountMigrations(st, c.CatalogDir, secrets)
	srv.MountStorage(st)
	srv.MountBackups(st)
	sched := NewScheduler(st, c.CatalogDir, secrets)
	srv.MountScheduler(sched)
	srv.MountDriftScan(st, sched.Now)
	srv.MountDrift(st, c.CatalogDir)
	dockerClient := docker.NewClient("")
	srv.MountDocker(dockerClient)
	srv.MountDockge(dockerClient)
	srv.MountEdge(st)
	srv.MountUpdates(st, c.CatalogDir, dockerClient, secrets)
	srv.MountPlatform(st, filepath.Join(filepath.Dir(c.ConfigPath), "platform-trust.yaml"))
	srv.MountFirewall(c.Port, configSvc)
	srv.MountExposure(st, c.Port)
	srv.MountTunnel(st, secrets)
	if c.Dashboard != nil {
		srv.MountDashboard(c.Dashboard)
	}
	return &AppServices{Claim: claimSvc, Auth: authSvc, Scheduler: sched}, nil
}
