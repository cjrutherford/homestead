package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func catalogStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog":
			_, _ = w.Write([]byte(`[{"name":"jellyfin","version":"12.2","title":"Jellyfin","blurb":"Media.","category":"media","icon":"mdi:plex"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/jellyfin":
			_, _ = w.Write([]byte(`{"Name":"jellyfin","Version":"12.2","Title":"Jellyfin","Requirements":{"MemoryMB":2048,"Ports":[8096]},"BackupPaths":["config"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/plan":
			_, _ = w.Write([]byte(`{"Package":"jellyfin","Requirements":{"Passed":true},"Routes":[{"Host":"jellyfin.lan","Exposure":"local"}],"DNS":[],"TLS":[{"Host":"jellyfin.lan","Issuer":"internal"}],"Firewall":[{"Service":{"Name":"web-http"}}],"Directories":[{"Mountpoint":"/mnt/data"}],"Actions":["provision","up"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/install":
			_, _ = w.Write([]byte(`{"id":7}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/configure":
			_, _ = w.Write([]byte(`{"id":8}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestCatalogCalls(t *testing.T) {
	client := New(catalogStub(t))
	ctx := context.Background()
	packages, err := client.CatalogPackages(ctx)
	if err != nil {
		t.Fatalf("CatalogPackages: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "jellyfin" {
		t.Fatalf("packages = %+v", packages)
	}
	manifest, err := client.CatalogPackage(ctx, "jellyfin")
	if err != nil {
		t.Fatalf("CatalogPackage: %v", err)
	}
	if manifest.Version != "12.2" || manifest.Requirements.MemoryMB != 2048 {
		t.Fatalf("manifest = %+v", manifest)
	}
	plan, err := client.PlanInstall(ctx, "jellyfin", map[string]any{"domain": "lan"})
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !plan.Requirements.Passed || len(plan.Routes) != 1 || plan.Actions[1] != "up" {
		t.Fatalf("plan = %+v", plan)
	}
	id, err := client.InstallPackage(ctx, "jellyfin", map[string]any{"domain": "lan"})
	if err != nil || id != 7 {
		t.Fatalf("id = %d %v", id, err)
	}
	id, err = client.ConfigurePackage(ctx, "jellyfin", []map[string]any{{"Service": "jellyfin"}})
	if err != nil || id != 8 {
		t.Fatalf("id = %d %v", id, err)
	}
	if _, err := client.CatalogPackage(ctx, "ghost"); err == nil {
		t.Fatal("fetched ghost package")
	}
}
