package server

import (
	"homestead/internal/cert"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/backup"
	"homestead/internal/claim"
	"homestead/internal/config"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/firewall"
	"homestead/internal/mdns"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/schema"
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
	// CraftLimits are the craft file proxy limits (S254); zero values
	// take the defaults (512 MiB upload, 30 minute timeout, 8 per session).
	CraftLimits craftfiles.Limits
}

// AppServices are the services MountApp built, for the caller's own
// wiring (boundary, claim token, discovery).
type AppServices struct {
	Claim *claim.Service
	Auth  *auth.Service
	// Scheduler is built and mounted for status; the daemon runs it (S260).
	Scheduler *scheduler.Scheduler
	// DashboardAddress owns the dashboard edge route; the daemon calls
	// RunEnsure so a fresh appliance is claimable on the LAN (S289).
	DashboardAddress *DashboardAddressService
	// Names serves app names: the mDNS labels and the optional `.lan`
	// resolver, which the daemon runs (S295).
	Names *LocalNames
	// LocalTLS issues local-only leaves; the daemon reconciles it once at
	// start so a route's new `.local` name is served after an upgrade
	// rather than at the next renewal pass (S295).
	LocalTLS *cert.LocalTLS
}

// MountApp registers every production TCP route on srv.
func MountApp(srv *Server, c AppConfig) (*AppServices, error) {
	st := c.Store
	backup.SweepScratch()
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	names := NewLocalNames(st)
	fw := firewall.Schema()
	configSvc := config.New(schema.Schema{Fields: append(fw.Fields, craftfiles.SchemaFields()...)}, c.ConfigPath)
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
	issuing, err := NewCertIssuing(st, secrets)
	if err != nil {
		return nil, err
	}
	local, err := NewLocalTLS(st, secrets, filepath.Join(filepath.Dir(c.ConfigPath), "internal-ca.yaml"), os.Getenv)
	if err != nil {
		return nil, err
	}
	srv.MountDoctor(
		func() []doctor.Check { return []doctor.Check{driftDoctorCheck(st, time.Now)()} },
		certDoctorChecks(st, issuing, time.Now),
		certOverrideDoctorCheck(os.Getenv),
		platformDoctorCheck(st),
		platformApplyDoctorCheck(st, time.Now),
		migrationDoctorChecks(st, c.CatalogDir),
		backupEncryptionDoctorCheck(st),
		localTLSDoctorChecks(local, time.Now),
		craftFilesDoctorCheck(st, docker.CraftMountPresent, docker.CraftSocketPath),
		edgeUpstreamDoctorCheck(st, docker.NewClient("").Containers),
		dashboardSocketDoctorCheck(st, docker.DashboardMountPresent, docker.DashboardSocketPath),
		mdnsDoctorCheck(mdns.CurrentState),
		resolverDoctorCheck(names),
	)
	srv.MountLocalTLS(st, local)
	srv.MountTLSRenewal(st, issuing, time.Now)
	srv.MountTLSIssuance(st, issuing)
	srv.MountOperations(operation.New(st))
	srv.MountCatalog(st, c.CatalogDir, secrets)
	srv.MountAppSecrets(st, c.CatalogDir, secrets)
	srv.MountMigrations(st, c.CatalogDir, secrets)
	srv.MountStorage(st, c.CatalogDir)
	srv.MountBackups(st, c.CatalogDir)
	srv.MountBackupPolicy(st, c.CatalogDir)
	srv.MountBackupRecipients(st, secrets, filepath.Join(filepath.Dir(c.ConfigPath), "backup-recipients.yaml"))
	sched := NewSchedulerWith(st, c.CatalogDir, secrets, issuing)
	sched.Register("localtls", localTLSSource(local, sched.Now))
	srv.MountScheduler(sched)
	srv.MountDriftScan(st, sched.Now)
	srv.MountDrift(st, c.CatalogDir)
	dockerClient := docker.NewClient("")
	srv.MountDocker(dockerClient)
	srv.MountDockge(dockerClient)
	srv.MountEdge(st, local)
	dashAddr := srv.MountDashboardAddress(st, local)
	srv.MountCraft(st, c.CatalogDir, filepath.Join(filepath.Dir(c.ConfigPath), "craft-trust.yaml"), docker.BuildClient{})
	srv.MountCraftFiles(CraftFilesConfig{
		Store: st, Secrets: secrets, Auth: authSvc, Local: local,
		YAMLPath: filepath.Join(filepath.Dir(c.ConfigPath), "craft-files.yaml"),
		Limits:   c.CraftLimits,
		Settings: func() map[string]any {
			st, err := configSvc.Get()
			if err != nil {
				return nil
			}
			return st.Settings
		},
	})
	srv.MountUpdates(st, c.CatalogDir, dockerClient, secrets)
	srv.MountPlatform(st, filepath.Join(filepath.Dir(c.ConfigPath), "platform-trust.yaml"))
	srv.MountFirewall(c.Port, configSvc, names.Enabled)
	srv.MountNames(names)
	srv.MountExposure(st, c.Port)
	ssoManager := srv.MountSSO(SSOConfig{
		Store: st, Secrets: secrets, Auth: authSvc, Claim: claimSvc, Local: local,
		CatalogDir: c.CatalogDir, YAMLPath: ssoYAMLPath(c.ConfigPath), Port: c.Port,
	})
	srv.MountTunnel(st, secrets, c.Port)
	srv.MountAppPublic(st, secrets, c.CatalogDir)
	srv.MountSetup(SetupConfig{
		Store: st, Secrets: secrets, Wizards: wizard.New(st), SSO: ssoManager, CatalogDir: c.CatalogDir,
		Port: c.Port,
	})
	if c.Dashboard != nil {
		srv.MountDashboard(c.Dashboard)
	}
	return &AppServices{Claim: claimSvc, Auth: authSvc, Scheduler: sched, DashboardAddress: dashAddr, Names: names, LocalTLS: local}, nil
}
