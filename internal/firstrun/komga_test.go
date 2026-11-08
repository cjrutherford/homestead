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
		if requests == 2 {
			return "\n401", nil
		}
		return "{\"isClaimed\":true}\n200", nil
	}
	secrets := memSecrets{}
	_, err := Run(context.Background(), komgaManifest(), secrets)
	// The claim status, then one recovery login that must fail: nothing else is sent.
	if err == nil || !strings.Contains(err.Error(), "already has accounts") || requests != 2 {
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

func komgaLibraryManifest() catalog.Manifest {
	m := komgaManifest()
	m.FirstRun.Libraries = []catalog.Library{{Name: "Comics", Kind: "comics", Path: "/library"}}
	return m
}

func TestKomgaFreshClaimAddsTheComicsLibrary(t *testing.T) {
	old := execIn
	defer func() { execIn = old }()
	var posted string
	requests := 0
	execIn = func(_ context.Context, _, input string, args ...string) (string, error) {
		requests++
		switch requests {
		case 1:
			return "{\"isClaimed\":false}\n200", nil
		case 2:
			return "{}\n200", nil
		case 3:
			return "{\"email\":\"homestead-recovery@localhost.invalid\",\"roles\":[\"ADMIN\"]}\n200", nil
		case 4:
			return "[]\n200", nil
		case 5:
			posted = input
			return "{}\n200", nil
		}
		t.Fatal("extra request")
		return "", nil
	}
	res, err := Run(context.Background(), komgaLibraryManifest(), memSecrets{})
	if err != nil || res != Created || !strings.Contains(posted, `data = "{\"name\":\"Comics\",\"root\":\"/library\",\"scanInterval\":\"HOURLY\",\"scanOnStartup\":true}"`) {
		t.Fatalf("res=%q err=%v posted=%q", res, err, posted)
	}
}

func TestKomgaInterruptedSetupResumesWithRecoveryAdminAndIsIdempotent(t *testing.T) {
	old := execIn
	defer func() { execIn = old }()
	for _, have := range []string{"[]", `[{"name":"Comics"}]`} {
		posts := 0
		requests := 0
		execIn = func(_ context.Context, _, input string, _ ...string) (string, error) {
			requests++
			switch requests {
			case 1:
				return "{\"isClaimed\":true}\n200", nil
			case 2:
				return "{\"email\":\"homestead-recovery@localhost.invalid\",\"roles\":[\"ADMIN\"]}\n200", nil
			case 3:
				return have + "\n200", nil
			}
			posts++
			return "{}\n200", nil
		}
		res, err := Run(context.Background(), komgaLibraryManifest(), memSecrets{})
		want := 0
		if have == "[]" {
			want = 1
		}
		if err != nil || res != Ready || posts != want {
			t.Fatalf("have=%s res=%q posts=%d err=%v", have, res, posts, err)
		}
	}
}

// TestKomgaFirstRunUsesTheContextPath (S474): the package serves under
// /apps/komga, so the claim probe goes there first and an install made
// before the move (answering at the root) is found by the fallback.
func TestKomgaFirstRunUsesTheContextPath(t *testing.T) {
	old, tries, pause := execIn, Tries, Pause
	defer func() { execIn, Tries, Pause = old, tries, pause }()
	Tries, Pause = 2, time.Millisecond
	m := komgaManifest()
	m.Serve, m.Path = catalog.ServePath, "/apps/komga/"
	var urls []string
	execIn = func(_ context.Context, _, input string, _ ...string) (string, error) {
		urls = append(urls, strings.SplitN(input, "\n", 2)[0])
		if strings.Contains(urls[len(urls)-1], "127.0.0.1:25600/api/") {
			return "{\"isClaimed\":true}\n200", nil // root-served install
		}
		if strings.Contains(input, "user =") {
			return "[{\"name\":\"Comics\"}]\n200", nil
		}
		return "\n404", nil
	}
	_, _ = komgaFirstRun(context.Background(), m, memSecrets{})
	if len(urls) < 3 || !strings.Contains(urls[0], "25600/apps/komga/api/v1/claim") || !strings.Contains(urls[1], "25600/api/v1/claim") {
		t.Fatalf("urls = %v", urls)
	}
}
