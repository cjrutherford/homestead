// Command everything-bridge serves the Everything application data bridge.
// It does not depend on the Homestead daemon. The production build signs
// people in with OIDC (Authentik) and refuses to start with incomplete
// configuration. The INSECURE development verifier exists only in binaries
// built with -tags everythingdev.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"homestead/internal/everything"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// config is everything newIdentity may need. Non-secret OIDC settings may
// come from flags or HOMESTEAD_EVERYTHING_* variables; the client secret only
// from a file.
type config struct {
	dev                          bool
	app, listen, base, static    string
	origins                      []string
	issuer, clientID, secretFile string
	redirectURL, caFile          string
	groups                       []string // optional defence-in-depth group names (ADR-058)
	// await starts the bridge unconfigured: it serves a plain "sign-in is
	// not set up" answer and no data until Homestead applies sign-in
	// (ADR-057). It only takes effect when no issuer or client id is set;
	// a half-configured bridge still refuses to start.
	await bool
}

func envDefault(name, fallback string) string {
	if v := os.Getenv("HOMESTEAD_EVERYTHING_" + name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string) int64 {
	n, _ := strconv.ParseInt(os.Getenv("HOMESTEAD_EVERYTHING_"+name), 10, 64)
	return n
}

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(runTool(os.Args[1], os.Args[2:], os.Stdout, os.Stderr))
	}
	var origins listFlag
	dbPath := flag.String("db", "", "SQLite data file (private regular file; created if absent)")
	app := flag.String("app", envDefault("APP", "tasks"), "application name scoping stored data")
	listen := flag.String("listen", envDefault("LISTEN", "127.0.0.1:8081"), "listen address")
	flag.Var(&origins, "origin", "trusted browser origin (repeatable), e.g. https://home.example")
	dev := flag.Bool("dev-identity", false, "enable the INSECURE development identity (only in everythingdev builds)")
	base := flag.String("base-path", envDefault("BASE_PATH", ""), "application path prefix, e.g. /apps/tasks")
	static := flag.String("static-dir", envDefault("STATIC_DIR", ""), "prepared application files (must contain index.html)")
	issuer := flag.String("oidc-issuer", envDefault("OIDC_ISSUER", ""), "OIDC issuer URL, exactly as in the ID token iss claim")
	clientID := flag.String("oidc-client-id", envDefault("OIDC_CLIENT_ID", ""), "OIDC client id")
	secretFile := flag.String("oidc-client-secret-file", envDefault("OIDC_CLIENT_SECRET_FILE", ""), "file holding the OIDC client secret")
	redirect := flag.String("redirect-url", envDefault("OIDC_REDIRECT_URL", ""), "OIDC redirect URL, <origin><base-path>/_homestead/callback")
	caFile := flag.String("ca-file", envDefault("OIDC_CA_FILE", ""), "optional PEM CA bundle for a private issuer")
	groups := flag.String("oidc-groups", envDefault("OIDC_GROUPS", ""), "optional comma-separated group names; a token with a groups claim must list one")
	personQuota := flag.Int64("quota-person-bytes", envInt("QUOTA_PERSON_BYTES"), "per-person byte quota (0 = default 256 MiB)")
	appQuota := flag.Int64("quota-app-bytes", envInt("QUOTA_APP_BYTES"), "whole-application byte quota (0 = default 4 GiB)")
	sharing := flag.Bool("sharing", os.Getenv("SHARING") == "1", "enable KV sharing between people of this app (ADR-058; off = kv_handles_disabled)")
	netOn := flag.Bool("net", os.Getenv("HOMESTEAD_EVERYTHING_NET") == "1", "enable the net.fetch operation: public internet only, limited and logged (ADR-058; off = net_disabled)")
	linkDays := flag.Int("read-link-max-days", int(envInt("READ_LINK_MAX_DAYS")), "longest read link lifetime in days (0 = 365, the cap)")
	proxies := flag.String("trusted-proxies", envDefault("TRUSTED_PROXIES", ""), "comma-separated CIDRs whose X-Forwarded-For is believed (empty = loopback and private ranges, the Homestead edge; none = trust no peer)")
	publicRead := flag.String("public-read-origin", envDefault("PUBLIC_READ_ORIGIN", ""), "public https origin that may mint read links (set when the owner approved read-link exposure; empty = none)")
	await := flag.Bool("await-signin", os.Getenv("HOMESTEAD_EVERYTHING_AWAIT_SIGNIN") == "1", "serve nothing until sign-in is configured (set by the Homestead package)")
	flag.Parse()
	if len(origins) == 0 && os.Getenv("HOMESTEAD_EVERYTHING_ORIGIN") != "" {
		origins = listFlag{os.Getenv("HOMESTEAD_EVERYTHING_ORIGIN")}
	}
	if *await && len(origins) == 0 {
		// Only used to build the (denying) data route while unconfigured.
		origins = listFlag{"https://sign-in-pending.invalid"}
	}
	if *dbPath == "" {
		log.Fatal("everything-bridge: -db is required")
	}
	store, err := everything.OpenStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	store.SetQuota(*personQuota, *appQuota)
	store.SetSharing(*sharing)
	store.SetNet(*netOn, nil)
	store.SetReadLinkMax(time.Duration(*linkDays) * 24 * time.Hour)
	if err = store.SetPublicReadOrigin(*publicRead); err != nil {
		log.Fatal(err)
	}
	if err = store.SetTrustedProxies(splitGroups(*proxies)); err != nil {
		log.Fatal(err)
	}
	identity, extra, err := newIdentity(config{dev: *dev, app: *app, listen: *listen, base: *base, static: *static, origins: origins,
		issuer: *issuer, clientID: *clientID, secretFile: *secretFile, redirectURL: *redirect, caFile: *caFile, await: *await, groups: splitGroups(*groups)}, store)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := everything.NewServerAt(*base, store, identity, origins, extra)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "everything-bridge listening on %s\n", ln.Addr())
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// splitGroups parses the comma-separated group list, dropping blanks.
func splitGroups(v string) []string {
	var out []string
	for _, g := range strings.Split(v, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}
