package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"homestead/internal/apiclient"
)

func runStorage(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl storage <mounts|register|registrations|assign|assignments> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "mounts":
		return runStorageMounts(ctx, client, stdout, stderr)
	case "register":
		return runStorageRegister(ctx, client, args[1:], stdout, stderr)
	case "registrations":
		return runStorageRegistrations(ctx, client, stdout, stderr)
	case "assign":
		return runStorageAssign(ctx, client, args[1:], stdout, stderr)
	case "assignments":
		return runStorageAssignments(ctx, client, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown storage subcommand %q\n", args[0])
		return exitUsage
	}
}

func runStorageMounts(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	mounts, err := client.StorageMounts(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, mount := range mounts {
		fmt.Fprintf(stdout, "%s %s accessible=%v free=%d\n", mount.Mountpoint, mount.FSType, mount.Accessible, mount.FreeBytes)
	}
	return exitOK
}

func storageActorFlags(set *flag.FlagSet) *string {
	return set.String("actor", "", "acting owner recorded with the change")
}

func runStorageRegister(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("storage register", flag.ContinueOnError)
	set.SetOutput(stderr)
	mountpoint := set.String("mountpoint", "", "discovered mount path")
	label := set.String("label", "", "short name [a-z][a-z0-9-]*")
	actor := storageActorFlags(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *mountpoint == "" || *label == "" || *actor == "" {
		fmt.Fprintln(stderr, "storage register requires --mountpoint, --label and --actor")
		return exitUsage
	}
	registration, err := client.StorageRegister(ctx, *mountpoint, *label, *actor)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s %s\n", registration.Mountpoint, registration.Label)
	return exitOK
}

func runStorageRegistrations(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	registrations, err := client.StorageRegistrations(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, registration := range registrations {
		fmt.Fprintf(stdout, "%s %s by %s\n", registration.Mountpoint, registration.Label, registration.Actor)
	}
	return exitOK
}

func runStorageAssign(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("storage assign", flag.ContinueOnError)
	set.SetOutput(stderr)
	class := set.String("class", "", "storage class (application, bulk, backup, ...)")
	mountpoint := set.String("mountpoint", "", "registered mount path")
	actor := storageActorFlags(set)
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *class == "" || *mountpoint == "" || *actor == "" {
		fmt.Fprintln(stderr, "storage assign requires --class, --mountpoint and --actor")
		return exitUsage
	}
	assignment, err := client.StorageAssign(ctx, *class, *mountpoint, *actor)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%s %s\n", assignment.Class, assignment.Mountpoint)
	return exitOK
}

func runStorageAssignments(ctx context.Context, client *apiclient.Client, stdout, stderr io.Writer) int {
	assignments, err := client.StorageAssignments(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	for _, assignment := range assignments {
		fmt.Fprintf(stdout, "%s %s by %s\n", assignment.Class, assignment.Mountpoint, assignment.Actor)
	}
	return exitOK
}
