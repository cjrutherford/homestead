package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"homestead/internal/apiclient"
	"homestead/internal/storage"
)

func runStorage(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homesteadctl storage <mounts|register|registrations|unregister|assign|assignments|unassign> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "mounts":
		return runStorageMounts(ctx, client, stdout, stderr)
	case "register":
		return runStorageRegister(ctx, client, args[1:], stdout, stderr)
	case "registrations":
		return runStorageRegistrations(ctx, client, stdout, stderr)
	case "unregister":
		return runStorageUnregister(ctx, client, args[1:], stdout, stderr)
	case "unassign":
		return runStorageUnassign(ctx, client, args[1:], stdout, stderr)
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
	// Data disks first: they are where classes belong (S293).
	sort.SliceStable(mounts, func(i, j int) bool {
		return (mounts[i].Kind == storage.KindData) && (mounts[j].Kind != storage.KindData)
	})
	for _, mount := range mounts {
		line := fmt.Sprintf("%s %s kind=%s accessible=%v free=%d", mount.Mountpoint, mount.FSType, mount.Kind, mount.Accessible, mount.FreeBytes)
		if len(mount.Suggested) > 0 {
			classes := make([]string, len(mount.Suggested))
			for i, class := range mount.Suggested {
				classes[i] = string(class)
			}
			line += " suggested=" + strings.Join(classes, ",")
		}
		fmt.Fprintln(stdout, line)
	}
	return exitOK
}

func storageActorFlags(set *flag.FlagSet) *string {
	return set.String("actor", "", "acting owner recorded with the change (default: the signed-in caller)")
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
	if *mountpoint == "" || *label == "" {
		fmt.Fprintln(stderr, "storage register requires --mountpoint and --label")
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
	acknowledge := set.Bool("acknowledge-impact", false, "proceed even if installed packages still keep data on the mount this class leaves (audited)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *class == "" || *mountpoint == "" {
		fmt.Fprintln(stderr, "storage assign requires --class and --mountpoint")
		return exitUsage
	}
	assignment, err := client.StorageAssign(ctx, *class, *mountpoint, *actor, *acknowledge)
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

func runStorageUnassign(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("storage unassign", flag.ContinueOnError)
	set.SetOutput(stderr)
	class := set.String("class", "", "storage class to unassign (refused with the app names while any installed app keeps data under it, even if another class shares the mount)")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *class == "" {
		fmt.Fprintln(stderr, "storage unassign requires --class")
		return exitUsage
	}
	if err := client.StorageUnassign(ctx, *class); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "unassigned %s; no data on the disk was touched\n", *class)
	return exitOK
}

func runStorageUnregister(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("storage unregister", flag.ContinueOnError)
	set.SetOutput(stderr)
	mountpoint := set.String("mountpoint", "", "registered mount path to drop")
	if err := set.Parse(args); err != nil {
		return exitUsage
	}
	if *mountpoint == "" {
		fmt.Fprintln(stderr, "storage unregister requires --mountpoint")
		return exitUsage
	}
	if err := client.StorageUnregister(ctx, *mountpoint); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "unregistered %s; no data on the disk was touched\n", *mountpoint)
	return exitOK
}
