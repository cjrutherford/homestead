package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
)

func komgaManifest() catalog.Manifest {
	return catalog.Manifest{Name: "komga", Title: "Komga", FirstRun: &catalog.FirstRun{
		Routine: catalog.FirstRunKomga, Service: "komga", Account: "homestead-recovery@localhost.invalid", Secret: "recovery_password",
	}}
}

func TestKomgaFreshClaimCreatesAndVerifiesRecoveryAdmin(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries, Pause = 2, time.Millisecond
	secrets := memSecrets{}
	requests := 0
	execIn = func(_ context.Context, container, input string, args ...string) (string, error) {
		requests++
		if container != "komga-komga" || len(args) < 2 || args[0] != "curl" {
			t.Fatalf("unexpected command: %s %v", container, args)
		}
		for _, arg := range args {
			if strings.Contains(arg, "homestead-recovery") || strings.Contains(arg, "app_komga") || (requests > 1 && strings.Contains(arg, secrets["app_komga_recovery_password"])) {
				t.Fatal("credential in argv")
			}
		}
		switch requests {
		case 1:
			if strings.Contains(input, "X-Komga-Password") {
				t.Fatal("claim status received password")
			}
			return "{\"isClaimed\":false}\n200", nil
		case 2:
			if !strings.Contains(input, "X-Komga-Password: "+secrets["app_komga_recovery_password"]) || !strings.Contains(input, "request = \"POST\"") {
				t.Fatal("claim did not receive sealed password on stdin")
			}
			return "{}\n200", nil
		case 3:
			if !strings.Contains(input, "user = \"homestead-recovery@localhost.invalid:"+secrets["app_komga_recovery_password"]) {
				t.Fatal("recovery login missing")
			}
			return "{\"email\":\"homestead-recovery@localhost.invalid\",\"roles\":[\"USER\",\"ADMIN\"]}\n200", nil
		default:
			t.Fatal("unexpected extra request")
			return "", nil
		}
	}
	result, err := Run(context.Background(), komgaManifest(), secrets)
	if err != nil || result != Created || requests != 3 || len(secrets["app_komga_recovery_password"]) < 30 {
		t.Fatalf("result=%q requests=%d err=%v", result, requests, err)
	}
}

func TestKomgaClaimedDatabaseIsUntouched(t *testing.T) {
	old := execIn
	defer func() { execIn = old }()
	requests := 0
	execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) {
		requests++
		return "{\"isClaimed\":true}\n200", nil
	}
	secrets := memSecrets{}
	_, err := Run(context.Background(), komgaManifest(), secrets)
	if err == nil || !strings.Contains(err.Error(), "already has accounts") || requests != 1 || len(secrets) != 0 {
		t.Fatalf("requests=%d secrets=%v err=%v", requests, secrets, err)
	}
}

func TestKomgaClaimFailureAndWrongRecoveryAccountAreRefused(t *testing.T) {
	for _, afterClaim := range []string{"refused", "wrong-account"} {
		t.Run(afterClaim, func(t *testing.T) {
			old := execIn
			defer func() { execIn = old }()
			requests := 0
			execIn = func(_ context.Context, _, _ string, _ ...string) (string, error) {
				requests++
				switch requests {
				case 1:
					return "{\"isClaimed\":false}\n200", nil
				case 2:
					if afterClaim == "refused" {
						return "{}\n400", nil
					}
					return "{}\n200", nil
				case 3:
					return "{\"email\":\"another@example.com\",\"roles\":[\"ADMIN\"]}\n200", nil
				default:
					return "", errors.New("extra request")
				}
			}
			_, err := Run(context.Background(), komgaManifest(), memSecrets{})
			if err == nil || (afterClaim == "refused" && requests != 2) || (afterClaim == "wrong-account" && requests != 3) {
				t.Fatalf("requests=%d err=%v", requests, err)
			}
		})
	}
}
