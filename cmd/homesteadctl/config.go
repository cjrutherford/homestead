package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/apiclient"
	"homestead/internal/desired"
	"homestead/internal/schema"
)

func runConfig(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl config <get|plan|apply|export|import> [-f FILE]")
		return exitUsage
	}
	switch args[0] {
	case "get":
		return runConfigGet(ctx, client, stdout, stderr)
	case "plan", "apply":
		return runConfigWrite(ctx, client, args[0] == "apply", args[1:], stdout, stderr)
	case "export":
		return runConfigExport(ctx, client, args[1:], stdout, stderr)
	case "import":
		return runConfigImport(ctx, client, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config subcommand %q\n", args[0])
		return exitUsage
	}
}

func runConfigGet(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	state, err := client.GetConfig(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	writeJSON(stdout, withSettings(state))
	return exitOK
}

func runConfigWrite(ctx context.Context, client *apiclient.Client, apply bool, args []string, stdout, stderr io.Writer) int {
	name := "plan"
	if apply {
		name = "apply"
	}
	set := flag.NewFlagSet("config "+name, flag.ContinueOnError)
	set.SetOutput(stderr)
	file := set.String("f", "", "settings file (JSON or YAML by extension)")
	set.StringVar(file, "file", "", "settings file (JSON or YAML by extension)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *file == "" {
		fmt.Fprintf(stderr, "config %s requires -f FILE\n", name)
		return exitUsage
	}
	settings, err := loadSettingsFile(*file)
	if err != nil {
		return fail(stderr, err)
	}
	if !apply {
		plan, err := client.PlanConfig(ctx, settings)
		if err != nil {
			return fail(stderr, err)
		}
		if !plan.Valid {
			printFieldErrors(stderr, plan.Errors)
			return exitError
		}
		writeJSON(stdout, plan)
		return exitOK
	}
	state, fieldErrs, err := client.ApplyConfig(ctx, settings)
	if err != nil {
		return fail(stderr, err)
	}
	if len(fieldErrs) > 0 {
		printFieldErrors(stderr, fieldErrs)
		return exitError
	}
	writeJSON(stdout, withSettings(state))
	return exitOK
}
func printFieldErrors(stderr io.Writer, errs []schema.FieldError) {
	for _, e := range errs {
		if e.Value == nil {
			fmt.Fprintf(stderr, "%s: %s\n", e.Field, e.Reason)
		} else {
			fmt.Fprintf(stderr, "%s: %s (got %v)\n", e.Field, e.Reason, e.Value)
		}
	}
}

// withSettings normalizes nil settings to {} so CLI output never shows null.
func withSettings(state desired.State) desired.State {
	if state.Settings == nil {
		state.Settings = map[string]any{}
	}
	return state
}

func runConfigExport(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("config export", flag.ContinueOnError)
	set.SetOutput(stderr)
	file := set.String("f", "", "output file (default stdout)")
	set.StringVar(file, "file", "", "output file (default stdout)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	export, err := client.ExportConfig(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if *file == "" {
		writeJSON(stdout, export)
		return exitOK
	}
	if err := writeExportFile(*file, export); err != nil {
		return fail(stderr, err)
	}
	return exitOK
}

func runConfigImport(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("config import", flag.ContinueOnError)
	set.SetOutput(stderr)
	file := set.String("f", "", "export document file (JSON or YAML by extension)")
	set.StringVar(file, "file", "", "export document file (JSON or YAML by extension)")
	secretsFile := set.String("secrets-file", "", "JSON object file mapping secret names to values")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *file == "" {
		fmt.Fprintln(stderr, "config import requires -f FILE")
		return exitUsage
	}
	document, err := readExportFile(*file)
	if err != nil {
		return fail(stderr, err)
	}
	secrets := map[string]string{}
	if *secretsFile != "" {
		loaded, err := loadSettingsFile(*secretsFile)
		if err != nil {
			return fail(stderr, err)
		}
		for name, value := range loaded {
			text, ok := value.(string)
			if !ok || text == "" {
				return fail(stderr, fmt.Errorf("secret %q must map to a non-empty string", name))
			}
			secrets[name] = text
		}
	}
	summary, err := client.ImportConfig(ctx, document, secrets)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "settings: %d secrets: %d\n", summary.Settings, summary.Secrets)
	return exitOK
}

// writeExportFile persists an export document as YAML: the portable,
// inspectable form disaster recovery carries between machines.
func writeExportFile(path string, export desired.Export) error {
	var body bytes.Buffer
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(export); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	if err := os.WriteFile(path, body.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readExportFile reads an export document as JSON or YAML by
// extension, mirroring loadSettingsFile conventions.
func readExportFile(path string) (desired.Export, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return desired.Export{}, fmt.Errorf("read %s: %w", path, err)
	}
	var export desired.Export
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &export); err != nil {
			return desired.Export{}, fmt.Errorf("parse %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &export); err != nil {
			return desired.Export{}, fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		return desired.Export{}, fmt.Errorf("unsupported extension %q (want .json, .yaml, .yml)", filepath.Ext(path))
	}
	return export, nil
}
