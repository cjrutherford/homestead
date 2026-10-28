package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// Craft files linked as part of an app's setup (S326, ADR-048 §4). A
// craft app with a file hook is linked to Nextcloud when it is
// installed, as the nextcloud-link step after its address. Nextcloud,
// installed later, links every craft app already installed as the last
// step of its own setup. The link is local appliance configuration: the
// install's review lists it ("Link <app> to Nextcloud for files") and
// its digest is recorded with the setup's steps, so it runs, stops on a
// changed plan or a failure, and resumes like every other step.

const (
	nextcloudPackage     = "nextcloud"
	nextcloudAdminSecret = "admin_password"
	setupFilesLink       = "nextcloud-link"
)

// filesLinkPlan is the nextcloud-link step's plan: the craft apps it
// links and where their files go. NewLink says the step first links this
// appliance's Nextcloud, with the administrator account Homestead
// created when it installed Nextcloud.
type filesLinkPlan struct {
	Apps    []string `json:"apps"`
	Lines   []string `json:"lines"`
	BaseURL string   `json:"base_url"`
	Account string   `json:"account"`
	Root    string   `json:"root"`
	NewLink bool     `json:"new_link"`
	Digest  string   `json:"digest"`
}

// filesLinkDigest binds the apps and the link they use. Whether the link
// still has to be made is left out: a link made in the meantime to the
// same Nextcloud, account and folder is the same plan.
func filesLinkDigest(p filesLinkPlan) string {
	data, _ := json.Marshal(struct {
		Apps    []string `json:"apps"`
		BaseURL string   `json:"base_url"`
		Account string   `json:"account"`
		Root    string   `json:"root"`
	}{p.Apps, p.BaseURL, p.Account, p.Root})
	sum := sha256.Sum256(append([]byte("nextcloud-link="), data...))
	return hex.EncodeToString(sum[:])
}

// applianceNextcloud is this appliance's own Nextcloud: its address
// under the appliance's names (ADR-044), or on its own name for an
// install that predates the move, and its administrator account. With
// requireInstalled, a Nextcloud that is not installed is none.
func applianceNextcloud(st *store.Store, catalogDir string, requireInstalled bool) (base, account string, ok bool) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return "", "", false
	}
	m, err := entries.Get(nextcloudPackage)
	if err != nil {
		return "", "", false
	}
	if requireInstalled {
		if installed, err := catalog.Installed(st, m.Name, stackDirFor(m.Name)); err != nil || !installed {
			return "", "", false
		}
	}
	account = "admin"
	for _, svc := range m.Services {
		if u := strings.TrimSpace(svc.Environment["NEXTCLOUD_ADMIN_USER"]); u != "" {
			account = u
			break
		}
	}
	path, host := m.AppPath(), ""
	if set, err := loadEdgeRoutes(st); err == nil {
		for _, r := range appsso.AppRoutes(m, set.Routes) {
			if r.Exposure == docker.ExposurePublic {
				continue
			}
			if r.IsPath() {
				path, host = r.Path, ""
			} else {
				path, host = "", string(r.Host)
			}
			break
		}
	}
	if path != "" {
		names := applianceNames(st)
		if len(names) == 0 {
			names = catalog.DefaultApplianceNames
		}
		return "https://" + names[0] + strings.TrimSuffix(path, "/"), account, true
	}
	if host == "" {
		return "", "", false
	}
	return "https://" + host, account, true
}

func (a *appSetup) filesManager() *craftfiles.Manager {
	if a.s.files == nil {
		return nil
	}
	return a.s.files.Manager
}

// filesTarget is where craft files go: the link as it stands, or else
// this appliance's Nextcloud. nextcloudPending counts a Nextcloud being
// installed as there.
func (a *appSetup) filesTarget(mgr *craftfiles.Manager, nextcloudPending bool) (base, account, root string, ok bool) {
	if link, linked, err := mgr.CurrentLink(); err == nil && linked {
		return link.BaseURL, link.Account, link.Root, true
	}
	base, account, ok = applianceNextcloud(a.st, a.catalogDir, !nextcloudPending)
	return base, account, craftfiles.DefaultRoot, ok
}

// filesLinkFor plans linking apps: the hooked craft apps among them that
// are not set to browser-local and are installed with an address.
// pending is an app being installed, whose address comes from its setup.
// Nil when there is nothing to link or nowhere to link it.
func (a *appSetup) filesLinkFor(apps []string, pending string, nextcloudPending bool) (*filesLinkPlan, error) {
	mgr := a.filesManager()
	if mgr == nil {
		return nil, nil
	}
	base, account, root, ok := a.filesTarget(mgr, nextcloudPending)
	if !ok {
		return nil, nil
	}
	st, err := mgr.Status()
	if err != nil {
		return nil, err
	}
	byName := map[string]craftfiles.AppStatus{}
	for _, s := range st.Apps {
		byName[s.Name] = s
	}
	plan := &filesLinkPlan{BaseURL: base, Account: account, Root: root, NewLink: !st.Linked, Apps: []string{}, Lines: []string{}}
	for _, app := range apps {
		s, hooked := byName[app]
		if !hooked || s.LocalOnly {
			continue
		}
		if app != pending {
			installed, err := catalog.Installed(a.st, app, stackDirFor(app))
			if err != nil || !installed || s.Host == "" {
				continue
			}
		}
		plan.Apps = append(plan.Apps, app)
		plan.Lines = append(plan.Lines, fmt.Sprintf("Link %s to Nextcloud for files", s.Title))
	}
	if len(plan.Apps) == 0 {
		return nil, nil
	}
	plan.Digest = filesLinkDigest(*plan)
	return plan, nil
}

// filesLinkPreview is the link an install of m brings, for its review:
// a hooked craft app links itself; Nextcloud, when nothing is linked
// yet, links every craft app already installed. Nil otherwise.
func (a *appSetup) filesLinkPreview(m catalog.Manifest) *filesLinkPlan {
	if _, hooked := craftfiles.HookedApp(m.Name); hooked {
		plan, _ := a.filesLinkFor([]string{m.Name}, m.Name, false)
		return plan
	}
	if m.Name != nextcloudPackage {
		return nil
	}
	mgr := a.filesManager()
	if mgr == nil {
		return nil
	}
	if _, linked, err := mgr.CurrentLink(); err != nil || linked {
		return nil
	}
	var names []string
	for _, app := range craftfiles.Apps() {
		names = append(names, app.Name)
	}
	plan, _ := a.filesLinkFor(names, "", true)
	return plan
}

// filesLinkNow is the recorded step's plan as it stands now.
func (a *appSetup) filesLinkNow(rec setupRecord) (*filesLinkPlan, error) {
	return a.filesLinkFor(rec.FilesApps, "", false)
}

// approveFilesLink checks the install's approval against the link the
// install brings now, and returns the step to record.
func (a *appSetup) approveFilesLink(m catalog.Manifest, approved string) (setupStep, []string, error) {
	plan := a.filesLinkPreview(m)
	if plan == nil || plan.Digest != strings.TrimSpace(approved) {
		return setupStep{}, nil, fmt.Errorf("the Nextcloud link for files: %w", errPlanChanged)
	}
	return setupStep{Name: setupFilesLink, State: stepWaiting, Digest: plan.Digest}, plan.Apps, nil
}

func (a *appSetup) recordLink(actor, outcome, detail string) {
	if _, err := audit.New(a.st).Record(audit.Entry{Actor: actor, Action: "craftfiles.link", Outcome: outcome, Detail: detail}); err != nil {
		a.s.log.Warn("audit record failed", "action", "craftfiles.link", "error", err)
	}
}

// runFilesLink is the nextcloud-link step: re-planned and compared with
// its approval, it links this appliance's Nextcloud when nothing is
// linked yet, then checks each app can use it. Each app's link is
// audited as craftfiles.link.
func (a *appSetup) runFilesLink(ctx context.Context, rec setupRecord, approved string) error {
	plan, err := a.filesLinkNow(rec)
	if err != nil {
		return err
	}
	if plan == nil || plan.Digest != approved {
		return errPlanChanged
	}
	mgr := a.filesManager()
	apps := strings.Join(plan.Apps, ",")
	if plan.NewLink {
		if err := a.linkNextcloud(ctx, mgr, plan.Root, rec.Actor); err != nil {
			a.recordLink(rec.Actor, audit.OutcomeError, fmt.Sprintf("apps=%s account=%s root=%s: %v", apps, plan.Account, plan.Root, err))
			return fmt.Errorf("linking Nextcloud did not finish: %w", err)
		}
		link, linked, err := mgr.CurrentLink()
		if err != nil {
			return err
		}
		if !linked || link.BaseURL != plan.BaseURL || link.Account != plan.Account || link.Root != plan.Root {
			return errPlanChanged
		}
	}
	st, err := mgr.Status()
	if err != nil {
		return err
	}
	for _, app := range plan.Apps {
		for _, s := range st.Apps {
			if s.Name != app || s.Ready {
				continue
			}
			why := "it has no address on the edge yet"
			if !st.CredentialStored {
				why = "the Nextcloud app password is missing; rotate it on the Craft files page"
			}
			a.recordLink(rec.Actor, audit.OutcomeError, fmt.Sprintf("app=%s account=%s root=%s: %s", app, plan.Account, plan.Root, why))
			return fmt.Errorf("%s cannot keep its files in Nextcloud: %s", s.Title, why)
		}
	}
	for _, app := range plan.Apps {
		a.recordLink(rec.Actor, audit.OutcomeOK, fmt.Sprintf("app=%s account=%s root=%s", app, plan.Account, plan.Root))
	}
	return nil
}

// linkNextcloud links this appliance's Nextcloud. Right after its
// install Nextcloud may still be setting itself up, so an unreachable or
// unready Nextcloud is retried until linkWait passes.
func (a *appSetup) linkNextcloud(ctx context.Context, mgr *craftfiles.Manager, root, actor string) error {
	link := a.linkFn
	if link == nil {
		link = mgr.LinkAppliance
	}
	deadline := a.now().Add(a.linkWait)
	for {
		err := link(ctx, root, actor)
		if err == nil || errors.Is(err, craftfiles.ErrAlreadyLinked) {
			return nil
		}
		if !errors.Is(err, craftfiles.ErrUpstream) || !a.now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(a.linkRetry):
		}
	}
}
