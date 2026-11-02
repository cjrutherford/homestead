// Command everything-bridge serves the Everything application data bridge.
// It does not depend on the Homestead daemon. S367 spike: the production build
// has no identity verifier (real Authentik verification is not implemented),
// so it refuses to start. The development verifier exists only in binaries
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

func main() {
	var origins listFlag
	dbPath := flag.String("db", "", "SQLite data file (private regular file; created if absent)")
	app := flag.String("app", "tasks", "application name scoping stored data")
	listen := flag.String("listen", "127.0.0.1:8081", "listen address")
	flag.Var(&origins, "origin", "trusted browser origin (repeatable), e.g. https://home.example")
	dev := flag.Bool("dev-identity", false, "enable the INSECURE development identity (only in everythingdev builds)")
	flag.Parse()
	if *dbPath == "" {
		log.Fatal("everything-bridge: -db is required")
	}
	identity, extra, err := newIdentity(*dev, *app, *listen)
	if err != nil {
		log.Fatal(err)
	}
	store, err := everything.OpenStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	handler, err := everything.NewServer(store, identity, origins, extra)
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
