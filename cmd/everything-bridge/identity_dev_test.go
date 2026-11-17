//go:build everythingdev

package main

import "testing"

func TestDevIdentityNeedsFlagAckAndLoopback(t *testing.T) {
	t.Setenv("HOMESTEAD_EVERYTHING_INSECURE_DEV", "")
	if _, _, err := newIdentity(config{dev: true, app: "tasks", listen: "127.0.0.1:1"}, nil); err == nil {
		t.Fatal("enabled without acknowledgement")
	}
	t.Setenv("HOMESTEAD_EVERYTHING_INSECURE_DEV", "i-understand-this-is-not-authentication")
	if _, _, err := newIdentity(config{dev: false, app: "tasks", listen: "127.0.0.1:1"}, nil); err == nil {
		t.Fatal("enabled without flag")
	}
	if _, _, err := newIdentity(config{dev: true, app: "tasks", listen: "0.0.0.0:1"}, nil); err == nil {
		t.Fatal("enabled on non-loopback")
	}
	if _, _, err := newIdentity(config{dev: true, app: "tasks", listen: "127.0.0.1:1"}, nil); err != nil {
		t.Fatal(err)
	}
}
