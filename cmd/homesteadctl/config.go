package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"homestead/internal/apiclient"
	"homestead/internal/desired"
	"homestead/internal/schema"
)

func runConfig(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl config <get|plan|apply> [-f FILE]")
		return exitUsage
	}
	switch args[0] {
	case "get":
		return runConfigGet(ctx, client, stdout, stderr)
	case "plan", "apply":
		return runConfigWrite(ctx, client, args[0] == "apply", args[1:], stdout, stderr)
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
