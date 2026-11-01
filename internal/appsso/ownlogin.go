package appsso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"homestead/internal/catalog"
)

// Own-login-off (S422, ADR-055 §6 amendment): behind the gate and the host
// block an admin-only app's own login adds nothing, so a manifest may ship
// pinned in-app commands that switch it off and on. The account the app
// needs is created with a random password nobody knows; its bcrypt hash
// travels on stdin, never in argv, Compose, the audit log or the plan.

// ownLoginAccount is the JSON line the on command reads from stdin.
type ownLoginAccount struct {
	Username string `json:"username"`
	Hash     string `json:"hash"`
}

// ownLoginUsername is the app account's name: the person who applied sign-in,
// reduced to plain characters; "owner" when there is none.
func ownLoginUsername(actor string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(actor) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '@':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "owner"
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

// unknownPasswordHash is the bcrypt hash (cost 10, what the app checks) of
// 32 random bytes that are dropped here: nobody can sign in with it.
func unknownPasswordHash() (string, error) {
	pw := make([]byte, 32)
	if _, err := rand.Read(pw); err != nil {
		return "", fmt.Errorf("appsso: entropy: %w", err)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(pw)), 10)
	if err != nil {
		return "", fmt.Errorf("appsso: hash: %w", err)
	}
	return string(h), nil
}

// ownLoginExec runs one static argv in the app's container as the
// manifest's user, retried for the same wait the other in-app commands
// allow (the app may still be starting or creating its tables). It
// returns the command's output; stdin is the only way data reaches it.
func ownLoginExec(ctx context.Context, container, user, stdin string, argv []string) (string, error) {
	var err error
	for i := 0; i < readyTries; i++ {
		var out string
		if out, err = execIn(ctx, container, user, stdin, argv...); err == nil {
			return out, nil
		}
		if i < readyTries-1 {
			if perr := pause(ctx); perr != nil {
				return "", perr
			}
		}
	}
	return "", fmt.Errorf("appsso: the in-app command %q failed in %s (the app must finish creating its database; the manual fallback is in its slice notes): %w",
		describeArgv(argv), container, err)
}

func wantsRestart(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == catalog.RestartMarker {
			return true
		}
	}
	return false
}

// ownLoginOff switches the app's own login off: prepare (restart if it
// asks), then create the account and set the flag (restart if it asks).
func ownLoginOff(ctx context.Context, container, actor string, o catalog.ForwardOwnLoginOff) error {
	out, err := ownLoginExec(ctx, container, o.ExecUser, "", o.Prepare)
	if err != nil {
		return err
	}
	if wantsRestart(out) {
		if err := restart(ctx, container); err != nil {
			return fmt.Errorf("appsso: restarting %s failed: %w", container, err)
		}
	}
	hash, err := unknownPasswordHash()
	if err != nil {
		return err
	}
	doc, err := json.Marshal(ownLoginAccount{Username: ownLoginUsername(actor), Hash: hash})
	if err != nil {
		return err
	}
	out, err = ownLoginExec(ctx, container, o.ExecUser, string(doc)+"\n", o.OnApply)
	if err != nil {
		return err
	}
	if wantsRestart(out) {
		if err := restart(ctx, container); err != nil {
			return fmt.Errorf("appsso: the in-app command succeeded but restarting %s failed: %w", container, err)
		}
	}
	return nil
}

// ownLoginOn puts the app's own login back, then restarts it if asked.
func ownLoginOn(ctx context.Context, container string, o catalog.ForwardOwnLoginOff) error {
	out, err := ownLoginExec(ctx, container, o.ExecUser, "", o.OnOff)
	if err != nil {
		return err
	}
	if wantsRestart(out) {
		if err := restart(ctx, container); err != nil {
			return fmt.Errorf("appsso: the in-app command succeeded but restarting %s failed: %w", container, err)
		}
	}
	return nil
}
