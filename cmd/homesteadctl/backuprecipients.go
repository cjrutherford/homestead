package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"homestead/internal/apiclient"
)

const recipientsUsage = "usage: homesteadctl backup recipients <list|add --key age1... [--label TEXT]|remove --fingerprint FP>"

// runBackupRecipients manages owner-held age recipients (S271). New
// backup runs encrypt to the appliance identity and every recipient;
// existing runs are not re-encrypted.
func runBackupRecipients(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, recipientsUsage)
		return exitUsage
	}
	set := flag.NewFlagSet("backup recipients "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	key := set.String("key", "", "age X25519 public key (age1...)")
	label := set.String("label", "", "free-text label")
	fingerprint := set.String("fingerprint", "", "recipient fingerprint")
	if err := set.Parse(args[1:]); err != nil {
		return exitUsage
	}
	switch args[0] {
	case "list":
		view, err := client.BackupRecipients(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		for _, r := range view.Recipients {
			fmt.Fprintf(stdout, "%s  %s  %s  %s\n", r.Fingerprint, r.Kind, r.PublicKey, r.Label)
		}
		if !view.OwnerHeld {
			fmt.Fprintln(stdout, "warning: no owner-held recipient and no recorded identity export; add a recipient or run `homesteadctl backup identity export`")
		}
		fmt.Fprintln(stdout, view.Note)
		return exitOK
	case "add":
		if *key == "" {
			fmt.Fprintln(stderr, recipientsUsage)
			return exitUsage
		}
		r, err := client.BackupRecipientAdd(ctx, *key, *label)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "added recipient %s; new backup runs include it, existing runs are not re-encrypted\n", r.Fingerprint)
		return exitOK
	case "remove":
		if *fingerprint == "" {
			fmt.Fprintln(stderr, recipientsUsage)
			return exitUsage
		}
		r, err := client.BackupRecipientRemove(ctx, *fingerprint)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "removed recipient %s; it can still read runs made while it was configured\n", r.Fingerprint)
		return exitOK
	default:
		fmt.Fprintln(stderr, recipientsUsage)
		return exitUsage
	}
}

const identityUsage = "usage: homesteadctl backup identity <status|ack|export (--out FILE | --stdout) [--ack]|rotate (--out FILE | --stdout) [--ack]>"

// runBackupIdentity reports or exports the recovery identity. Export
// never prints unless --stdout is given; --out writes a new 0600 file
// and refuses to overwrite.
func runBackupIdentity(ctx context.Context, client *apiclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, identityUsage)
		return exitUsage
	}
	set := flag.NewFlagSet("backup identity "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	out := set.String("out", "", "write the identity to a new 0600 file")
	toStdout := set.Bool("stdout", false, "print the identity to standard output")
	ack := set.Bool("ack", false, "after a successful export, confirm the key is stored offline")
	if err := set.Parse(args[1:]); err != nil {
		return exitUsage
	}
	switch args[0] {
	case "status":
		st, err := client.BackupIdentity(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if st.Created {
			fmt.Fprintf(stdout, "appliance recipient: %s (%s)\n", st.Recipient, st.Fingerprint)
		} else {
			fmt.Fprintln(stdout, "appliance identity not created yet (the first backup or an export creates it)")
		}
		if st.Exported {
			fmt.Fprintf(stdout, "recovery identity exported by %s at %s\n", st.ExportedBy, st.ExportedAt)
			if st.Acknowledged {
				fmt.Fprintf(stdout, "stored offline: acknowledged at %s\n", st.AcknowledgedAt)
			} else {
				fmt.Fprintln(stdout, "not acknowledged as stored offline; run `homesteadctl backup identity ack`")
			}
		} else {
			fmt.Fprintln(stdout, "recovery identity never exported")
		}
		return exitOK
	case "ack":
		if _, err := client.BackupIdentityAck(ctx); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, "recorded: the recovery identity is stored offline")
		return exitOK
	case "export", "rotate":
		rotate := args[0] == "rotate"
		if (*out == "") == !*toStdout {
			fmt.Fprintln(stderr, args[0]+" needs exactly one of --out FILE or --stdout\n"+identityUsage)
			return exitUsage
		}
		var file *os.File
		if *out != "" {
			// Open first so a bad path fails before anything is exported.
			f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fail(stderr, err)
			}
			file = f
		}
		var res apiclient.BackupIdentityExport
		var rotated apiclient.BackupIdentityRotate
		var err error
		if rotate {
			rotated, err = client.BackupIdentityRotate(ctx)
			res = rotated.BackupIdentityExport
		} else {
			res, err = client.BackupIdentityExport(ctx)
		}
		if err != nil {
			if file != nil {
				_ = file.Close()
				_ = os.Remove(*out)
			}
			return fail(stderr, err)
		}
		if file != nil {
			_, werr := fmt.Fprintln(file, res.Identity)
			if werr == nil {
				// Durable on disk before the owner is told it is safe.
				werr = file.Sync()
			}
			if cerr := file.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return fail(stderr, werr)
			}
			fmt.Fprintf(stdout, "recovery identity for %s written to %s (0600); store it offline, off this appliance\n", res.Fingerprint, *out)
		} else {
			fmt.Fprintln(stdout, res.Identity)
			fmt.Fprintln(stderr, "recovery identity printed above; store it offline, off this appliance. This export is audited.")
		}
		if rotate {
			fmt.Fprintf(stdout, "rotated from %s to %s: %d run files re-wrapped to the new key, %d already current, %d failed\n",
				rotated.PreviousFingerprint, res.Fingerprint, rotated.Rewrapped, rotated.AlreadyCurrent, len(rotated.Failed))
			fmt.Fprintln(stdout, rotated.RunsNote)
			for _, f := range rotated.Failed {
				fmt.Fprintf(stdout, "not re-wrapped (the appliance keeps the old key for it; rotate again to retry): %s\n", f)
			}
			fmt.Fprintln(stdout, "runs made before now stay readable with the new key and also with the old one; keep the old key until those runs are pruned")
		}
		if *ack {
			if _, err := client.BackupIdentityAck(ctx); err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintln(stdout, "recorded: the recovery identity is stored offline")
		} else {
			fmt.Fprintln(stderr, "the doctor warning stays until you confirm storage: run `homesteadctl backup identity ack`")
		}
		return exitOK
	default:
		fmt.Fprintln(stderr, identityUsage)
		return exitUsage
	}
}
