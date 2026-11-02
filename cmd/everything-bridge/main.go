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
}

func envDefault(name, fallback string) string {
	if v := os.Getenv("HOMESTEAD_EVERYTHING_" + name); v != "" {
		return v
	}
	return fallback
}

func main() {
	var origins listFlag
	dbPath := flag.String("db", "", "SQLite data file (private regular file; created if absent)")
	app := flag.String("app", "tasks", "application name scoping stored data")
	listen := flag.String("listen", "127.0.0.1:8081", "listen address")
	flag.Var(&origins, "origin", "trusted browser origin (repeatable), e.g. https://home.example")
	dev := flag.Bool("dev-identity", false, "enable the INSECURE development identity (only in everythingdev builds)")
	base := flag.String("base-path", envDefault("BASE_PATH", ""), "application path prefix, e.g. /apps/tasks")
	static := flag.String("static-dir", envDefault("STATIC_DIR", ""), "prepared application files (must contain index.html)")
	issuer := flag.String("oidc-issuer", envDefault("OIDC_ISSUER", ""), "OIDC issuer URL, exactly as in the ID token iss claim")
	clientID := flag.String("oidc-client-id", envDefault("OIDC_CLIENT_ID", ""), "OIDC client id")
	secretFile := flag.String("oidc-client-secret-file", envDefault("OIDC_CLIENT_SECRET_FILE", ""), "file holding the OIDC client secret")
	redirect := flag.String("redirect-url", envDefault("OIDC_REDIRECT_URL", ""), "OIDC redirect URL, <origin><base-path>/_homestead/callback")
	caFile := flag.String("ca-file", envDefault("OIDC_CA_FILE", ""), "optional PEM CA bundle for a private issuer")
	flag.Parse()
	if *dbPath == "" {
		log.Fatal("everything-bridge: -db is required")
	}
	store, err := everything.OpenStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	identity, extra, err := newIdentity(config{dev: *dev, app: *app, listen: *listen, base: *base, static: *static, origins: origins,
		issuer: *issuer, clientID: *clientID, secretFile: *secretFile, redirectURL: *redirect, caFile: *caFile}, store)
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
