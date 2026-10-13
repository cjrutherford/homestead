package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/apiclient"
	"homestead/internal/docker"
	"homestead/internal/operation"
)

func runEdge(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl edge <deploy|routes> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "deploy":
		return runEdgeDeploy(ctx, client, args[1:], stdout, stderr)
	case "routes":
		return runEdgeRoutes(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown edge subcommand %q\n", args[0])
		return exitUsage
	}
}

func followOperation(ctx context.Context, client *apiclient.Client, id int64, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "operation: %d\n", id)
	var last operation.Operation
	if err := client.StreamOperation(ctx, id, func(op operation.Operation) {
		last = op
		fmt.Fprintf(stdout, "%s: %s\n", op.Status, stepSummary(op))
	}); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return fail(stderr, err)
	}
	switch last.Status {
	case operation.StatusSucceeded:
		return exitOK
	default:
		return exitError
	}
}

func runEdgeDeploy(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	follow := false
	for _, arg := range args {
		switch arg {
		case "--follow":
			follow = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "unknown flag %q\n", arg)
				return exitUsage
			}
			fmt.Fprintln(stderr, "usage: homesteadctl edge deploy [--follow]")
			return exitUsage
		}
	}
	id, err := client.EdgeDeploy(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

func runEdgeRoutes(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "get" {
		routes, err := client.EdgeRoutes(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"routes": routes}); err != nil {
			return fail(stderr, err)
		}
		return exitOK
	}
	var file string
	follow := false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "set":
			positional = append(positional, args[i])
		case args[i] == "-f" && i+1 < len(args):
			file = args[i+1]
			i++
		case args[i] == "--follow":
			follow = true
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", args[i])
			return exitUsage
		default:
			fmt.Fprintln(stderr, "usage: homesteadctl edge routes <get | set -f FILE [--follow]>")
			return exitUsage
		}
	}
	if len(positional) != 1 || file == "" {
		fmt.Fprintln(stderr, "usage: homesteadctl edge routes <get | set -f FILE [--follow]>")
		return exitUsage
	}
	routes, err := loadRoutesFile(file)
	if err != nil {
		return fail(stderr, err)
	}
	id, err := client.EdgeReconcile(ctx, routes)
	if err != nil {
		return fail(stderr, err)
	}
	if !follow {
		fmt.Fprintf(stdout, "operation: %d\n", id)
		return exitOK
	}
	return followOperation(ctx, client, id, stdout, stderr)
}

// loadRoutesFile reads a route table document (JSON or YAML by
// extension) with capitalized route keys matching the docker model,
// e.g. {"routes": [{"Host": "app.lan", "Target": {"Host": "app",
// "Port": 8080}, "Exposure": "local", "TLS": "internal",
// "Health": {"Path": "/", "IntervalSeconds": 30}}]}.
func loadRoutesFile(path string) ([]docker.Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Routes []docker.Route `json:"routes" yaml:"routes"`
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported extension %q (want .json, .yaml, .yml)", filepath.Ext(path))
	}
	if doc.Routes == nil {
		return nil, fmt.Errorf("parse %s: missing routes table", path)
	}
	return doc.Routes, nil
}
