//go:build !everythingdev

package main

import (
	"errors"
	"testing"

	"homestead/internal/everything"
)

func TestProductionBuildCannotEnableDevIdentity(t *testing.T) {
	for _, dev := range []bool{false, true} {
		t.Setenv("HOMESTEAD_EVERYTHING_INSECURE_DEV", "i-understand-this-is-not-authentication")
		id, _, err := newIdentity(dev, "tasks", "127.0.0.1:0")
		if id != nil || !errors.Is(err, everything.ErrNoVerifier) {
			t.Fatalf("dev=%v: got %v %v", dev, id, err)
		}
	}
}
