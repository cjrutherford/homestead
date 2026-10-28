package sso

import (
	"strings"
	"testing"
)

func hintObject(t *testing.T, objs []Object) Object {
	t.Helper()
	for _, o := range objs {
		if o.Kind == "Login page hint" {
			return o
		}
	}
	t.Fatalf("the plan has no login page hint: %+v", objs)
	return Object{}
}

func TestLoginHintIsSetOnTheDefaultAuthenticationFlow(t *testing.T) {
	h, ak := planHarness(t)
	plan := planApply(t, h)
	if plan.Spec.LoginHint != LoginHint {
		t.Fatalf("spec login hint = %q", plan.Spec.LoginHint)
	}
	if o := hintObject(t, plan.Objects); o.Action != ActionUpdate || !strings.Contains(strings.Join(o.Changes, " "), "ask the owner") {
		t.Fatalf("hint object = %+v", o)
	}
	if ak.authTitle != LoginHint || ak.titlePatchs != 1 {
		t.Fatalf("title = %q, patches = %d", ak.authTitle, ak.titlePatchs)
	}
	if !strings.Contains(LoginHint, "Ask the owner") {
		t.Fatal("hint text")
	}
}

func TestLoginHintIsIdempotent(t *testing.T) {
	h, ak := planHarness(t)
	ak.authTitle = LoginHint
	plan := planApply(t, h)
	if o := hintObject(t, plan.Objects); o.Action != ActionUnchanged {
		t.Fatalf("hint object = %+v", o)
	}
	if ak.titlePatchs != 0 {
		t.Fatalf("patched an up-to-date title %d times", ak.titlePatchs)
	}
}

func TestLoginHintSkippedWithoutTheFlow(t *testing.T) {
	h, ak := planHarness(t)
	ak.noAuthFlow = true
	plan := planApply(t, h)
	if o := hintObject(t, plan.Objects); o.Action != ActionUnchanged {
		t.Fatalf("hint object = %+v", o)
	}
	if ak.titlePatchs != 0 {
		t.Fatal("patched a missing flow")
	}
}

// The login page must not offer a recovery link: only the brand's flow is
// set, never a stage's, and the stages are never touched.
func TestLoginHintLeavesIdentificationStageAlone(t *testing.T) {
	h, ak := planHarness(t)
	planApply(t, h)
	for _, r := range ak.requests {
		if strings.Contains(r, "/stages/identification/") {
			t.Fatalf("touched the identification stage: %s", r)
		}
	}
}

func TestSpecsWithoutALoginHintNeverTouchTheFlow(t *testing.T) {
	h, ak := planHarness(t)
	spec := demoSpec(h, ak)
	spec.LoginHint = ""
	api := &API{Base: ak.srv.URL, Token: apiToken, Client: ak.client}
	if err := api.Reconcile(ctxb(), spec, "s3cret", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if ak.titlePatchs != 0 {
		t.Fatal("an app spec set the hint")
	}
}

func TestLoginHintChangesTheDigest(t *testing.T) {
	h, _ := planHarness(t)
	spec, err := h.m.BuildSpec(PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	old := spec
	old.LoginHint = ""
	if digestOf(old) == digestOf(spec) {
		t.Fatal("the login hint must be covered by the digest")
	}
}
