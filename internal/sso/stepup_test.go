package sso

import (
	"strings"
	"testing"
	"time"
)

// stepUp runs StartStepUp and returns the callback inputs.
func (h *harness) stepUp(session, identity int64, returnTo string) (code, state string) {
	h.t.Helper()
	start, err := h.m.StartStepUp(ctxb(), session, identity, "owner", "127.0.0.1:8080", returnTo)
	if err != nil {
		h.t.Fatalf("StartStepUp: %v", err)
	}
	if !strings.Contains(start.URL, "max_age="+FreshMaxAge) || strings.Contains(start.URL, "prompt=login") {
		h.t.Fatalf("step-up must ask for a fresh sign-in with max_age, not prompt=login: %s", start.URL)
	}
	return h.idp.authorize(start.URL)
}

func TestStepUpHappyPath(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.stepUp(42, 7, "/people")
	out, err := h.m.Callback(ctxb(), state, code, "", state)
	if err != nil {
		t.Fatalf("step-up: %v", err)
	}
	if out.Kind != KindStepUp || out.SessionID != 42 || out.IdentityID != 7 || out.ReturnTo != "/people" || out.Subject != "user-1" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestStepUpRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c map[string]any)
		cookie func(state string) string
		code   string
	}{
		{"stale auth_time", func(c map[string]any) { c["auth_time"] = time.Now().Add(-10 * time.Minute).Unix() }, nil, CodeStale},
		{"missing auth_time", func(c map[string]any) { delete(c, "auth_time") }, nil, CodeStale},
		{"future auth_time", func(c map[string]any) { c["auth_time"] = time.Now().Add(time.Hour).Unix() }, nil, CodeStale},
		{"another subject", func(c map[string]any) { c["sub"] = "someone-else" }, nil, CodeUnlinked},
		{"no state cookie", nil, func(string) string { return "" }, CodeState},
		{"other browser's cookie", nil, func(string) string { return "nope" }, CodeState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.configure()
			h.link()
			h.idp.mutate = tc.mutate
			code, state := h.stepUp(42, 7, "/people")
			cookie := state
			if tc.cookie != nil {
				cookie = tc.cookie(state)
			}
			out, err := h.m.Callback(ctxb(), state, code, "", cookie)
			wantCode(t, err, tc.code)
			if out.SessionID != 0 && out.IdentityID != 0 {
				t.Fatalf("a refused step-up carries a result: %+v", out)
			}
		})
	}
}

func TestStepUpIsBoundToTheOwnersSession(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	if _, err := h.m.StartStepUp(ctxb(), 42, 99, "x", "127.0.0.1:8080", "/people"); err != ErrNotOwner {
		t.Fatalf("another identity's step-up = %v, want ErrNotOwner", err)
	}
	if _, err := h.m.StartStepUp(ctxb(), 0, 7, "x", "127.0.0.1:8080", "/people"); err != ErrNotOwner {
		t.Fatalf("no session = %v, want ErrNotOwner", err)
	}
	// A state minted for a session that is not the owner's is refused at
	// the callback too.
	code, state := h.stepUp(42, 7, "/people")
	if _, err := h.m.DB.Exec(`UPDATE sso_state SET identity_id = 99`); err != nil {
		t.Fatal(err)
	}
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeSession)
}

func TestStepUpStateIsSingleUseExpiresAndIsReplaced(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.stepUp(42, 7, "/people")
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatal(err)
	}
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeState)

	code, state = h.stepUp(42, 7, "/people")
	h.now = h.now.Add(StepUpStateTTL + time.Second)
	_, err = h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeState)
	h.now = time.Now()

	_, old := h.stepUp(42, 7, "/a")
	_, fresh := h.stepUp(42, 7, "/b")
	if _, err := h.m.Callback(ctxb(), old, "x", "", old); err == nil {
		t.Fatal("a replaced step-up state still works")
	}
	_ = fresh
}

func TestValidReturnTo(t *testing.T) {
	for _, ok := range []string{"/", "/people", "/people?x=1", "/apps/a#b"} {
		if !ValidReturnTo(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "people", "//evil.test", "/\\evil.test", "https://evil.test/", "http:evil", "/a//b", "/a\nb", "javascript:alert(1)", "/" + string(make([]byte, 600))} {
		if ValidReturnTo(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
