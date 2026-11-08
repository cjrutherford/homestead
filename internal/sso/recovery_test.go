package sso

import (
	"strings"
	"testing"
)

func recoveryObject(t *testing.T, objs []Object) Object {
	t.Helper()
	for _, o := range objs {
		if o.Kind == "Recovery flow" {
			return o
		}
	}
	t.Fatalf("the plan has no recovery flow: %+v", objs)
	return Object{}
}

func planApply(t *testing.T, h *harness) Plan {
	t.Helper()
	plan, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Approve(ApproveRequest{Digest: plan.Digest, APIToken: apiToken}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := runDef(t, h); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRecoveryFlowIsCreatedWhenTheBrandHasNone(t *testing.T) {
	h, ak := planHarness(t)
	plan := planApply(t, h)
	if plan.Spec.RecoveryFlow != RecoveryFlowSlug {
		t.Fatalf("spec recovery flow = %q", plan.Spec.RecoveryFlow)
	}
	if o := recoveryObject(t, plan.Objects); o.Action != ActionCreate || !strings.Contains(strings.Join(o.Changes, " "), "one-time sign-in links work") {
		t.Fatalf("recovery object = %+v", o)
	}
	flow := ak.flows[RecoveryFlowSlug]
	if flow == nil || flow["designation"] != "recovery" || flow["authentication"] != "none" || flow["title"] != "Reset your password" {
		t.Fatalf("flow = %v", flow)
	}
	pw := ak.named["/api/v3/stages/prompt/prompts/"+RecoveryFlowSlug+"-field-password"]
	rep := ak.named["/api/v3/stages/prompt/prompts/"+RecoveryFlowSlug+"-field-password-repeat"]
	if pw == nil || pw["field_key"] != "password" || pw["type"] != "password" || rep == nil || rep["field_key"] != "password_repeat" {
		t.Fatalf("prompts = %v / %v", pw, rep)
	}
	stage := ak.named["/api/v3/stages/prompt/stages/"+RecoveryFlowSlug+"-password"]
	if fields, _ := stage["fields"].([]any); len(fields) != 2 || fields[0] != pw["pk"] || fields[1] != rep["pk"] {
		t.Fatalf("prompt stage = %v", stage)
	}
	if w := ak.named["/api/v3/stages/user_write/"+RecoveryFlowSlug+"-write"]; w == nil || w["user_creation_mode"] != "never_create" {
		t.Fatalf("user write stage = %v", w)
	}
	want := []string{"pk-" + RecoveryFlowSlug + "-password", "pk-" + RecoveryFlowSlug + "-write", "pk-" + RecoveryFlowSlug + "-login"}
	if len(ak.flowBindings) != 3 {
		t.Fatalf("flow bindings = %v", ak.flowBindings)
	}
	for i, b := range ak.flowBindings {
		if b["target"] != "flow-"+RecoveryFlowSlug || b["stage"] != want[i] || b["order"] != float64((i+1)*10) {
			t.Fatalf("binding %d = %v", i, b)
		}
	}
	if ak.recovery != "flow-"+RecoveryFlowSlug || ak.brandPatches != 1 {
		t.Fatalf("brand recovery = %q patches=%d", ak.recovery, ak.brandPatches)
	}

	// Re-plan and re-apply: the brand has the flow now, nothing changes.
	posts := ak.recoveryPosts
	plan2 := planApply(t, h)
	if o := recoveryObject(t, plan2.Objects); o.Action != ActionUnchanged {
		t.Fatalf("re-plan recovery = %+v", o)
	}
	if ak.recoveryPosts != posts || ak.brandPatches != 1 {
		t.Fatalf("re-apply changed the recovery flow: posts %d->%d patches=%d", posts, ak.recoveryPosts, ak.brandPatches)
	}
}

func TestRecoveryFlowConvergesAfterAPartialApply(t *testing.T) {
	h, ak := planHarness(t)
	planApply(t, h)
	posts := ak.recoveryPosts
	// The objects exist but the brand lost (or never got) the flow.
	ak.recovery = ""
	plan := planApply(t, h)
	if o := recoveryObject(t, plan.Objects); o.Action != ActionUpdate {
		t.Fatalf("recovery object = %+v", o)
	}
	if ak.recoveryPosts != posts || len(ak.flowBindings) != 3 || ak.recovery != "flow-"+RecoveryFlowSlug || ak.brandPatches != 2 {
		t.Fatalf("posts %d->%d bindings=%d brand=%q", posts, ak.recoveryPosts, len(ak.flowBindings), ak.recovery)
	}
}

func TestRecoveryFlowReusesStockPasswordPrompts(t *testing.T) {
	h, ak := planHarness(t)
	for _, n := range []string{stockPasswordPrompt, stockRepeatPrompt} {
		ak.named["/api/v3/stages/prompt/prompts/"+n] = map[string]any{"pk": "stock-" + n, "name": n}
	}
	planApply(t, h)
	if _, ok := ak.named["/api/v3/stages/prompt/prompts/"+RecoveryFlowSlug+"-field-password"]; ok {
		t.Fatal("created a prompt field although the stock one exists")
	}
	stage := ak.named["/api/v3/stages/prompt/stages/"+RecoveryFlowSlug+"-password"]
	if fields, _ := stage["fields"].([]any); len(fields) != 2 || fields[0] != "stock-"+stockPasswordPrompt || fields[1] != "stock-"+stockRepeatPrompt {
		t.Fatalf("prompt stage = %v", stage)
	}
}

func TestRecoveryFlowLeavesTheBrandsOwnAlone(t *testing.T) {
	h, ak := planHarness(t)
	ak.recovery = "flow-owners-own"
	plan := planApply(t, h)
	if o := recoveryObject(t, plan.Objects); o.Action != ActionUnchanged || !strings.Contains(strings.Join(o.Changes, " "), "already has") {
		t.Fatalf("recovery object = %+v", o)
	}
	if ak.recoveryPosts != 0 || ak.brandPatches != 0 || ak.recovery != "flow-owners-own" || len(ak.flows) != 0 {
		t.Fatalf("touched the owner's recovery flow: posts=%d patches=%d brand=%q", ak.recoveryPosts, ak.brandPatches, ak.recovery)
	}
}

func TestRecoveryFlowNeedsADefaultBrand(t *testing.T) {
	h, ak := planHarness(t)
	ak.noBrand = true
	if _, err := h.m.Plan(ctxb(), PlanRequest{APIToken: apiToken}, "owner"); err == nil || !strings.Contains(err.Error(), "no default brand") {
		t.Fatalf("plan = %v", err)
	}
}

func TestSpecsWithoutARecoveryFlowNeverTouchTheBrand(t *testing.T) {
	h, ak := planHarness(t)
	spec := demoSpec(h, ak)
	spec.RecoveryFlow = ""
	api := &API{Base: ak.srv.URL, Token: apiToken, Client: ak.client}
	if err := api.Reconcile(ctxb(), spec, "s3cret", func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, r := range ak.requests {
		if strings.Contains(r, "/core/brands/") || strings.Contains(r, "/stages/") || strings.Contains(r, "/flows/bindings/") {
			t.Fatalf("an app spec touched the recovery flow: %s", r)
		}
	}
}

func TestRecoveryFlowChangesTheDigest(t *testing.T) {
	h, _ := planHarness(t)
	spec, err := h.m.BuildSpec(PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	old := spec
	old.RecoveryFlow = ""
	if digestOf(old) == digestOf(spec) {
		t.Fatal("the recovery flow must be covered by the digest")
	}
}

func recoveryPolicyBindings(ak *fakeAuthentik) []map[string]any {
	return ak.policyBinds
}

// S466: Authentik 2026.8 shows "Forgot password?" from the brand's
// recovery flow, so the flow refuses anyone without a one-time link.
func TestRecoveryFlowRefusesVisitorsWithoutAOneTimeLink(t *testing.T) {
	h, ak := planHarness(t)
	planApply(t, h)
	p := ak.policies[recoveryPolicyName(RecoveryFlowSlug)]
	if p == nil || p["expression"] != recoveryPolicyExpression {
		t.Fatalf("policy = %v", p)
	}
	for _, want := range []string{"is_restored", "pending_user", "ak_message", "Ask the owner", "return False"} {
		if !strings.Contains(recoveryPolicyExpression, want) {
			t.Fatalf("expression lacks %q", want)
		}
	}
	bs := recoveryPolicyBindings(ak)
	if len(bs) != 1 || bs[0]["policy"] != p["pk"] || bs[0]["target"] != "pbm-"+RecoveryFlowSlug || bs[0]["negate"] != false || bs[0]["failure_result"] != false {
		t.Fatalf("bindings = %v", bs)
	}
}

func TestRecoveryPolicyIsAddedToAnExistingFlowAndIsIdempotent(t *testing.T) {
	h, ak := planHarness(t)
	planApply(t, h)
	// An appliance set up before S466: flow and brand exist, no policy.
	delete(ak.policies, recoveryPolicyName(RecoveryFlowSlug))
	ak.policyBinds = nil
	plan := planApply(t, h)
	if o := recoveryObject(t, plan.Objects); o.Action != ActionUpdate || !strings.Contains(strings.Join(o.Changes, " "), "Forgot password") {
		t.Fatalf("recovery object = %+v", o)
	}
	if len(recoveryPolicyBindings(ak)) != 1 || ak.policies[recoveryPolicyName(RecoveryFlowSlug)] == nil {
		t.Fatalf("policy not restored: %v %v", ak.policies, ak.policyBinds)
	}
	plan = planApply(t, h)
	if o := recoveryObject(t, plan.Objects); o.Action != ActionUnchanged {
		t.Fatalf("re-plan = %+v", o)
	}
	if len(recoveryPolicyBindings(ak)) != 1 {
		t.Fatalf("bindings = %v", ak.policyBinds)
	}
	// A hand-edited expression is put back.
	ak.policies[recoveryPolicyName(RecoveryFlowSlug)]["expression"] = "return True"
	planApply(t, h)
	if ak.policies[recoveryPolicyName(RecoveryFlowSlug)]["expression"] != recoveryPolicyExpression || ak.policyPatches != 1 {
		t.Fatalf("expression not corrected, patches=%d", ak.policyPatches)
	}
}

func TestRecoveryPolicyLeavesTheOwnersBrandFlowAlone(t *testing.T) {
	h, ak := planHarness(t)
	ak.recovery = "flow-owners-own"
	planApply(t, h)
	if len(ak.policies) != 0 || len(ak.policyBinds) != 0 {
		t.Fatalf("touched the owner's flow: %v %v", ak.policies, ak.policyBinds)
	}
}
