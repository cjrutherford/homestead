package appsso

import (
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// TestTasksSignInUsesTheBridgesOwnRoutes: the Tasks bridge is its own OIDC
// client (ADR-053, ADR-057). Path apps already carry their path in the
// origin, so the redirect URI is exactly the one the bridge computes from
// its base path: <origin>/apps/tasks/_homestead/callback.
func TestTasksSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("tasks")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/tasks/", Target: docker.Target{Host: "tasks", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	uris := a.RedirectURIs(origins)
	if len(uris) == 0 || uris[0] != "https://homestead.lan/apps/tasks/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/tasks/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
	env := a.(envOIDC).Env(Settings{Issuer: "https://auth.example/application/o/tasks/", ClientID: "cid", ClientSecret: "never-in-env"})
	if env["HOMESTEAD_EVERYTHING_OIDC_ISSUER"] != "https://auth.example/application/o/tasks/" || env["HOMESTEAD_EVERYTHING_OIDC_CLIENT_ID"] != "cid" {
		t.Fatalf("env = %v", env)
	}
	for k, v := range env {
		if strings.Contains(v, "never-in-env") {
			t.Errorf("the client secret reached the environment as %s", k)
		}
	}
}

// S369: the second Everything app signs in through the same generic adapter,
// at its own path.
func TestProjectsSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("projects")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/projects/", Target: docker.Target{Host: "projects", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/projects/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/projects/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S370: Records signs in through the same generic adapter,
// at its own path.
func TestRecordsSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("records")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/records/", Target: docker.Target{Host: "records", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/records/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/records/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S371: Notes signs in through the same generic adapter,
// at its own path.
func TestNotesSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("notes")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/notes/", Target: docker.Target{Host: "notes", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/notes/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/notes/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S372: Contacts signs in through the same generic adapter,
// at its own path.
func TestContactsSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("contacts")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/contacts/", Target: docker.Target{Host: "contacts", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/contacts/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/contacts/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S373: Inventory signs in through the same generic adapter, at its own path.
func TestInventorySignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("inventory")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/inventory/", Target: docker.Target{Host: "inventory", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/inventory/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/inventory/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S374: TimeTracking signs in through the same generic adapter, at its own path.
func TestTimeTrackingSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("time-tracking")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/time-tracking/", Target: docker.Target{Host: "time-tracking", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/time-tracking/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/time-tracking/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S375: Circuits signs in through the same generic adapter, at its own path.
func TestCircuitsSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("circuits")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/circuits/", Target: docker.Target{Host: "circuits", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/circuits/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/circuits/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}

// S376: Parametric signs in through the same generic adapter, at its own path.
func TestParametricSignInUsesTheBridgesOwnRoutes(t *testing.T) {
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("parametric")
	if err != nil {
		t.Fatal(err)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	r := docker.Route{Host: "homestead.lan", Path: "/apps/parametric/", Target: docker.Target{Host: "parametric", Port: 8081},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	origins := Origins([]docker.Route{r})
	if uris := a.RedirectURIs(origins); len(uris) == 0 || uris[0] != "https://homestead.lan/apps/parametric/_homestead/callback" {
		t.Fatalf("redirect URIs = %v", uris)
	}
	if got := a.SignInURL(origins[0]); got != "https://homestead.lan/apps/parametric/_homestead/login" {
		t.Fatalf("sign-in URL = %s", got)
	}
}
