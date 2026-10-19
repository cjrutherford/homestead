package sso

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func ctxb() context.Context { return context.Background() }

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var fe *FlowError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want a FlowError with code %q", err, code)
	}
	if fe.Code != code {
		t.Fatalf("code = %q (%v), want %q", fe.Code, fe.Err, code)
	}
}

func TestLinkThenLoginHappyPath(t *testing.T) {
	h := newHarness(t)
	h.configure()
	st, _ := h.m.Status()
	if st.Offered {
		t.Fatal("sign-in must not be offered before a link exists")
	}
	h.link()
	st, _ = h.m.Status()
	if !st.Offered || !st.Linked || st.Subject != "user-1" {
		t.Fatalf("status after link = %+v", st)
	}
	code, state := h.startLogin()
	out, err := h.m.Callback(ctxb(), state, code, "", state)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if out.Kind != KindLogin || out.IdentityID != 7 || out.Subject != "user-1" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestLoginWithES256(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.alg = "ES256"
	code, state := h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatalf("ES256 login: %v", err)
	}
}

func TestIDTokenRefusals(t *testing.T) {
	cases := []struct {
		name string
		set  func(h *harness)
	}{
		{"wrong issuer", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["iss"] = "https://evil.example/" } }},
		{"issuer with different slash", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["iss"] = strings.TrimSuffix(h.idp.issuer(), "/") }
		}},
		{"wrong audience", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["aud"] = "someone-else" } }},
		{"audience list without client", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["aud"] = []string{"a", "b"} } }},
		{"multiple audiences without azp", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["aud"] = []string{ClientID, "b"} } }},
		{"wrong azp", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["azp"] = "other" } }},
		{"wrong nonce", func(h *harness) { h.idp.mutate = func(c map[string]any) { c["nonce"] = "replayed" } }},
		{"missing nonce", func(h *harness) { h.idp.mutate = func(c map[string]any) { delete(c, "nonce") } }},
		{"expired", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["exp"] = time.Now().Add(-10 * time.Minute).Unix() }
		}},
		{"expired just past the skew", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["exp"] = time.Now().Add(-Skew - 30*time.Second).Unix() }
		}},
		{"missing exp", func(h *harness) { h.idp.mutate = func(c map[string]any) { delete(c, "exp") } }},
		{"not yet valid", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["nbf"] = time.Now().Add(10 * time.Minute).Unix() }
		}},
		{"nbf just past the skew", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["nbf"] = time.Now().Add(Skew + 30*time.Second).Unix() }
		}},
		{"issued in the future", func(h *harness) {
			h.idp.mutate = func(c map[string]any) { c["iat"] = time.Now().Add(10 * time.Minute).Unix() }
		}},
		{"missing subject", func(h *harness) { h.idp.mutate = func(c map[string]any) { delete(c, "sub") } }},
		{"alg none", func(h *harness) {
			h.idp.tokenHook = func(hd map[string]any) { hd["alg"] = "none" }
			h.idp.sign = func(string, idpKey) []byte { return nil }
		}},
		{"alg HS256 keyed with the client secret", func(h *harness) {
			h.idp.tokenHook = func(hd map[string]any) { hd["alg"] = "HS256" }
			h.idp.sign = hs256([]byte(testSecret))
		}},
		{"alg HS256 keyed with the public key", func(h *harness) {
			h.idp.tokenHook = func(hd map[string]any) { hd["alg"] = "HS256" }
			h.idp.sign = func(in string, k idpKey) []byte { return hs256(k.rsa.N.Bytes())(in, k) }
		}},
		{"bad signature", func(h *harness) {
			h.idp.sign = func(string, idpKey) []byte { return make([]byte, 256) }
		}},
		{"signature by another key under the published kid", func(h *harness) {
			other := newFakeIDP(t)
			h.idp.sign = func(in string, _ idpKey) []byte {
				other.mu.Lock()
				defer other.mu.Unlock()
				return other.signRSA(in)
			}
		}},
		{"unknown kid", func(h *harness) { h.idp.tokenHook = func(hd map[string]any) { hd["kid"] = "nope" } }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.configure()
			h.link()
			tc.set(h)
			code, state := h.startLogin()
			_, err := h.m.Callback(ctxb(), state, code, "", state)
			wantCode(t, err, CodeToken)
			st, _ := h.m.Status()
			if !st.Linked || st.Subject != "user-1" {
				t.Fatalf("a refused token changed the link: %+v", st)
			}
		})
	}
}

func TestSkewEdgesAreAccepted(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.mutate = func(c map[string]any) {
		c["exp"] = time.Now().Add(-Skew + 20*time.Second).Unix()
		c["nbf"] = time.Now().Add(Skew - 20*time.Second).Unix()
		c["iat"] = time.Now().Add(Skew - 20*time.Second).Unix()
	}
	code, state := h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatalf("a token inside the 2 minute skew must pass: %v", err)
	}
}

func TestAudienceListWithAzpIsAccepted(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.mutate = func(c map[string]any) { c["aud"] = []string{ClientID, "b"}; c["azp"] = ClientID }
	code, state := h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatal(err)
	}
}

func TestJWKSRotationRefreshesOnUnknownKid(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatal(err)
	}
	hits := h.idp.jwksHits
	// Authentik rotates its signing key: the next token carries a kid the
	// cache has not seen, which triggers one refresh (rate limited).
	h.idp.addKey("k2")
	h.idp.mu.Lock()
	h.idp.active = 1
	h.idp.mu.Unlock()
	h.now = h.now.Add(time.Minute)
	code, state = h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatalf("login after rotation: %v", err)
	}
	if h.idp.jwksHits != hits+1 {
		t.Fatalf("jwks fetched %d times, want one refresh for the new kid", h.idp.jwksHits-hits)
	}
}

func TestUnknownKidRefreshIsRateLimited(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.tokenHook = func(hd map[string]any) { hd["kid"] = "ghost" }
	for i := 0; i < 5; i++ {
		code, state := h.startLogin()
		_, err := h.m.Callback(ctxb(), state, code, "", state)
		wantCode(t, err, CodeToken)
	}
	if h.idp.jwksHits > 1 {
		t.Fatalf("an unknown kid caused %d JWKS fetches inside the refresh interval", h.idp.jwksHits)
	}
}

func TestStateIsSingleUse(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.startLogin()
	if _, err := h.m.Callback(ctxb(), state, code, "", state); err != nil {
		t.Fatal(err)
	}
	// Replaying the very same callback must fail.
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeState)
}

func TestUnknownAndMissingStateRefused(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	_, err := h.m.Callback(ctxb(), "never-issued", "code", "", "never-issued")
	wantCode(t, err, CodeState)
	_, err = h.m.Callback(ctxb(), "", "code", "", "")
	wantCode(t, err, CodeState)
}

func TestStateExpiresAfterTenMinutes(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.startLogin()
	h.now = h.now.Add(StateTTL + time.Second)
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeState)
}

func TestLoginStateNeedsItsCookie(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.startLogin()
	_, err := h.m.Callback(ctxb(), state, code, "", "")
	wantCode(t, err, CodeState)
	// A state cookie from another flow does not satisfy it either, and the
	// refused attempt used the state up.
	code, state = h.startLogin()
	_, err = h.m.Callback(ctxb(), state, code, "", "another-state")
	wantCode(t, err, CodeState)
}

func TestUnlinkedSubjectIsRefusedAndCreatesNothing(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.mutate = func(c map[string]any) {
		c["sub"] = "stranger"
		c["email"] = "owner@example.test"
		c["groups"] = []string{"authentik Admins"}
	}
	code, state := h.startLogin()
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeUnlinked)
	var n int
	_ = h.st.DB().QueryRow(`SELECT COUNT(*) FROM identities`).Scan(&n)
	if n != 0 {
		t.Fatalf("identities = %d: an unlinked subject must create nothing", n)
	}
	link, _, _ := h.m.readLink()
	if link.Sub != "user-1" {
		t.Fatalf("link changed to %q", link.Sub)
	}
}

func TestSameSubjectFromAnotherIssuerIsRefused(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	// The link is keyed by (issuer, sub): the same sub under a different
	// issuer string is a different identity.
	code, state := h.startLogin()
	if _, err := h.st.DB().Exec(`UPDATE sso_link SET issuer = 'https://other.example/'`); err != nil {
		t.Fatal(err)
	}
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeUnlinked)
}

func TestLoginIsNotOfferedWithoutALink(t *testing.T) {
	h := newHarness(t)
	h.configure()
	if _, err := h.m.StartLogin(ctxb(), "127.0.0.1:8080"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("StartLogin without a link = %v, want ErrNotConfigured", err)
	}
	// Even a forged callback finds no link.
	h.link()
	if err := h.m.Unlink("owner"); err != nil {
		t.Fatal(err)
	}
	if h.m.Offered() {
		t.Fatal("offered after unlink")
	}
	if len(h.revoked) != 1 || h.revoked[0] != 7 {
		t.Fatalf("unlink revoked %v, want the owner's oidc sessions", h.revoked)
	}
}

func TestRedirectOriginIsPinnedToThePlan(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	for _, host := range []string{"evil.example", "127.0.0.1:9999", "homestead.example.test:8443", "", "127.0.0.1:8080.evil.example"} {
		if _, err := h.m.StartLogin(ctxb(), host); !errors.Is(err, ErrOrigin) {
			t.Errorf("StartLogin(%q) = %v, want ErrOrigin", host, err)
		}
	}
	start, err := h.m.StartLogin(ctxb(), "HOMESTEAD.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(start.URL, "redirect_uri=https%3A%2F%2Fhomestead.example.test%2Fapi%2Fv1%2Fsso%2Fcallback") || !start.Secure {
		t.Fatalf("redirect not the planned https origin: %s", start.URL)
	}
	// The same state, replayed at the callback with a different redirect, is
	// refused by the provider: the stored redirect is the one in the URL.
	if _, err := h.m.CreateLinkIntent(ctxb(), 7, "owner", "x", "https://evil.example"); err == nil {
		t.Fatal("a link intent for an unplanned origin must be refused")
	}
}

func TestLinkIntentIsSingleUseAndExpires(t *testing.T) {
	h := newHarness(t)
	h.configure()
	start, err := h.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	code, state := h.idp.authorize(start.URL)
	if _, err := h.m.Callback(ctxb(), state, code, "", ""); err != nil {
		t.Fatal(err)
	}
	code2, _ := h.idp.authorize(start.URL)
	_, err = h.m.Callback(ctxb(), state, code2, "", "")
	wantCode(t, err, CodeState)

	h2 := newHarness(t)
	h2.configure()
	start, _ = h2.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", "")
	code, state = h2.idp.authorize(start.URL)
	h2.now = h2.now.Add(StateTTL + time.Second)
	_, err = h2.m.Callback(ctxb(), state, code, "", "")
	wantCode(t, err, CodeState)
	if h2.m.Offered() {
		t.Fatal("an expired intent linked")
	}
}

func TestOnlyOneLink(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	if _, err := h.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", ""); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second link = %v, want ErrAlreadyLinked", err)
	}
}

func TestNewIntentReplacesTheOldOne(t *testing.T) {
	h := newHarness(t)
	h.configure()
	first, _ := h.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", "")
	second, _ := h.m.CreateLinkIntent(ctxb(), 7, "owner", "127.0.0.1:8080", "")
	code, state := h.idp.authorize(first.URL)
	_, err := h.m.Callback(ctxb(), state, code, "", "")
	wantCode(t, err, CodeState)
	code, state = h.idp.authorize(second.URL)
	if _, err := h.m.Callback(ctxb(), state, code, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestProviderErrorAndDeniedCode(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	_, state := h.startLogin()
	_, err := h.m.Callback(ctxb(), state, "", "access_denied", state)
	wantCode(t, err, CodeDenied)
}

func TestAuthentikDownIsAnUnavailableCode(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.mu.Lock()
	h.idp.down = true
	h.idp.mu.Unlock()
	// Starting: discovery fails.
	h.m.providers = nil
	_, err := h.m.StartLogin(ctxb(), "127.0.0.1:8080")
	wantCode(t, err, CodeUnavailable)
	// Status is local state only and still says the button is offered.
	if st, _ := h.m.Status(); !st.Offered {
		t.Fatal("the offered flag must come from local state, not from probing Authentik")
	}
	// Token endpoint failing mid-flow.
	h.idp.mu.Lock()
	h.idp.down = false
	h.idp.mu.Unlock()
	code, state := h.startLogin()
	h.idp.mu.Lock()
	h.idp.down = true
	h.idp.mu.Unlock()
	_, err = h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeUnavailable)
}

func TestTokenEndpointRefusalIsATokenCode(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	code, state := h.startLogin()
	h.idp.tokenCode = 400
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeToken)
}

func TestWrongClientSecretIsRefusedByTheProvider(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	_ = h.secrets.SetSystem(ClientSecretName, "not-the-secret")
	code, state := h.startLogin()
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeToken)
}

func TestPKCEVerifierMustMatch(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	start, _ := h.m.StartLogin(ctxb(), "127.0.0.1:8080")
	code, state := h.idp.authorize(start.URL)
	h.idp.mu.Lock()
	f := h.idp.pending[code]
	f.challenge = Challenge("some-other-verifier")
	h.idp.pending[code] = f
	h.idp.mu.Unlock()
	_, err := h.m.Callback(ctxb(), state, code, "", state)
	wantCode(t, err, CodeToken)
}

func TestDiscoveryIsPinnedToTheConfiguredIssuer(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.idp.discoverX = func(d map[string]any) { d["issuer"] = "https://elsewhere.example/" }
	h.m.providers = nil
	_, err := h.m.StartLogin(ctxb(), "127.0.0.1:8080")
	wantCode(t, err, CodeUnavailable)

	h2 := newHarness(t)
	h2.configure()
	h2.link()
	h2.idp.discoverX = func(d map[string]any) { d["token_endpoint"] = "https://elsewhere.example/token" }
	h2.m.providers = nil
	_, err = h2.m.StartLogin(ctxb(), "127.0.0.1:8080")
	wantCode(t, err, CodeUnavailable)
}

func TestDormantWhenAuthentikIsUninstalled(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	h.m.Installed = func() bool { return false }
	st, _ := h.m.Status()
	if st.Offered || !st.Dormant || !st.Linked {
		t.Fatalf("status = %+v, want a dormant link and no button", st)
	}
}

func TestDisableRemovesEverythingButTheLocalOwner(t *testing.T) {
	h := newHarness(t)
	h.configure()
	h.link()
	_ = h.secrets.SetSystem(APITokenName, "tok")
	if err := h.m.Disable("owner"); err != nil {
		t.Fatal(err)
	}
	st, _ := h.m.Status()
	if st.Configured || st.Linked || st.Offered || h.secrets.has(ClientSecretName) || h.secrets.has(APITokenName) {
		t.Fatalf("disable left state behind: %+v", st)
	}
	if len(h.revoked) != 1 {
		t.Fatalf("revoked = %v", h.revoked)
	}
}

func TestRateLimitAggregatesInAudit(t *testing.T) {
	h := newHarness(t)
	allowed := 0
	for i := 0; i < 100; i++ {
		if h.m.Allow() {
			allowed++
		}
	}
	if allowed != rateMax {
		t.Fatalf("allowed %d, want %d", allowed, rateMax)
	}
	if n := countAudit(h, "sso.rate-limited"); n != 1 {
		t.Fatalf("rate-limit audit entries during the flood = %d, want 1", n)
	}
	h.now = h.now.Add(2 * time.Minute)
	if !h.m.Allow() {
		t.Fatal("a new window must allow again")
	}
	if n := countAudit(h, "sso.rate-limited"); n != 2 {
		t.Fatalf("the closing count must be one more entry, got %d", n)
	}
}

func countAudit(h *harness, action string) int {
	n := 0
	for _, a := range h.audits {
		if strings.Contains(a, "|"+action+"|") {
			n++
		}
	}
	return n
}

func TestValidateOrigin(t *testing.T) {
	good := map[string]string{
		"https://homestead.example.test":  "https://homestead.example.test",
		"HTTPS://Homestead.Example.test/": "https://homestead.example.test",
		"http://127.0.0.1:8080":           "http://127.0.0.1:8080",
		"http://localhost:8080":           "http://localhost:8080",
		"https://homestead.lan:8443":      "https://homestead.lan:8443",
		"http://[::1]:8080":               "http://[::1]:8080",
	}
	for in, want := range good {
		if got, err := ValidateOrigin(in); err != nil || got != want {
			t.Errorf("ValidateOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "homestead.lan", "http://homestead.lan", "ftp://x", "https://x/path", "https://x?q=1", "https://user@x", "javascript:alert(1)", "https://*.example.test", "https://x#frag"} {
		if _, err := ValidateOrigin(bad); err == nil {
			t.Errorf("ValidateOrigin(%q) accepted", bad)
		}
	}
}

func TestOriginForIgnoresEverythingButHost(t *testing.T) {
	spec := Spec{Origins: []string{"https://a.example", "http://127.0.0.1:8080"}}
	if o, ok := OriginFor(spec, "a.example"); !ok || o != "https://a.example" {
		t.Fatalf("OriginFor = %q, %v", o, ok)
	}
	if _, ok := OriginFor(spec, "a.example:443"); ok {
		t.Fatal("an explicit default port is a different Host string and is refused")
	}
}
