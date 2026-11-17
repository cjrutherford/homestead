package sso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Authentik's recovery flow (ADR-049 §3). A one-time sign-in link
// (core/users/{pk}/recovery/) needs a recovery flow on the default brand;
// a stock Authentik has none. Homestead adds its own when the brand has
// none, and leaves any recovery flow the owner set alone.
const (
	// RecoveryFlowSlug is the flow Homestead creates. Its stages carry
	// the same prefix so a re-apply finds them.
	RecoveryFlowSlug  = "homestead-recovery"
	recoveryFlowName  = "Homestead password recovery"
	recoveryFlowTitle = "Reset your password"

	// RecoveryLine is the plan's change line.
	RecoveryLine = "Authentik gets a password recovery flow, so one-time sign-in links work."

	// RecoveryPolicyLine is the plan's change line for the owner-links-only
	// policy.
	RecoveryPolicyLine = "The recovery flow refuses anyone who did not come from an owner-issued one-time link, so the sign-in page's \"Forgot password?\" link is not a dead end."

	// recoveryPolicyDenied is what a person sees when they open the flow
	// by hand.
	recoveryPolicyDenied = "Ask the owner of this Homestead to reset your password."

	// Authentik's stock password-change prompts, reused when present.
	stockPasswordPrompt = "default-password-change-field-password"
	stockRepeatPrompt   = "default-password-change-field-password-repeat"
)

// recoveryPolicyExpression allows only a restored plan, which is what a
// one-time link (core/users/{pk}/recovery/) starts, and the administrator's
// own API call that makes that link (Authentik plans the flow then, before
// the plan is restored, with the person as the pending user). Authentik 2026.8 shows
// a "Forgot password?" link from the brand's recovery flow, and the
// appliance has no email, so opened by hand the flow must say so.
const recoveryPolicyExpression = "if request.context.get(\"is_restored\"):\n" +
	"    return True\n" +
	"if request.context.get(\"pending_user\") is not None:\n" +
	"    return True\n" +
	"ak_message(\"" + recoveryPolicyDenied + "\")\n" +
	"return False\n"

// remoteBrand is the part of an Authentik brand Homestead reads.
type remoteBrand struct {
	UUID         string `json:"brand_uuid"`
	Domain       string `json:"domain"`
	Default      bool   `json:"default"`
	FlowRecovery string `json:"flow_recovery"`
}

type remotePK struct {
	PK   string `json:"pk"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (a *API) defaultBrand(ctx context.Context) (*remoteBrand, error) {
	var out listResult[remoteBrand]
	if err := a.do(ctx, http.MethodGet, "/api/v3/core/brands/?page_size=100", nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Default {
			return &out.Results[i], nil
		}
	}
	return nil, errors.New("Authentik has no default brand, so Homestead cannot set its recovery flow")
}

// findFlow returns the flow's pk by slug, or "".
func (a *API) findFlow(ctx context.Context, slug string) (string, error) {
	pk, err := a.flowPK(ctx, slug)
	if err == nil {
		return pk, nil
	}
	var ae *APIError
	if asAPIError(err, &ae) {
		return "", err
	}
	if errors.Is(err, ErrUnreachable) {
		return "", err
	}
	return "", nil
}

// findByName returns the pk of the object named name in the list at path.
// The name filter is also checked here, in case Authentik ignores it.
func (a *API) findByName(ctx context.Context, path, name string) (string, error) {
	var out listResult[remotePK]
	if err := a.do(ctx, http.MethodGet, path+"?page_size=200&name="+url.QueryEscape(name), nil, &out); err != nil {
		return "", err
	}
	for _, r := range out.Results {
		if r.Name == name {
			return r.PK, nil
		}
	}
	return "", nil
}

func (a *API) ensureByName(ctx context.Context, path, name string, body map[string]any, step func(string), what string) (string, error) {
	pk, err := a.findByName(ctx, path, name)
	if err != nil || pk != "" {
		return pk, err
	}
	body["name"] = name
	var created remotePK
	if err := a.do(ctx, http.MethodPost, path, body, &created); err != nil {
		return "", err
	}
	if created.PK == "" {
		return "", fmt.Errorf("Authentik did not return the new %s", what)
	}
	step("created the " + what)
	return created.PK, nil
}

// passwordPrompt reuses Authentik's stock prompt, or makes Homestead's own.
func (a *API) passwordPrompt(ctx context.Context, stock, own, key, label string, order int, step func(string)) (string, error) {
	pk, err := a.findByName(ctx, "/api/v3/stages/prompt/prompts/", stock)
	if err != nil || pk != "" {
		return pk, err
	}
	return a.ensureByName(ctx, "/api/v3/stages/prompt/prompts/", own, map[string]any{
		"field_key": key, "label": label, "type": "password", "required": true,
		"placeholder": label, "placeholder_expression": false, "order": order,
	}, step, "prompt field "+key)
}

// inspectRecovery says what applying would do to the recovery flow.
func (a *API) inspectRecovery(ctx context.Context, slug string) (Object, error) {
	o := Object{Kind: "Recovery flow", Name: slug}
	brand, err := a.defaultBrand(ctx)
	if err != nil {
		return o, err
	}
	pk, err := a.findFlow(ctx, slug)
	if err != nil {
		return o, err
	}
	if brand.FlowRecovery != "" {
		o.Action, o.Changes = ActionUnchanged, []string{"the default brand already has a recovery flow: it is left as it is"}
		if pk != "" && pk == brand.FlowRecovery {
			ok, err := a.recoveryPolicyCurrent(ctx, pk, slug)
			if err != nil {
				return o, err
			}
			if !ok {
				o.Action, o.Changes = ActionUpdate, []string{RecoveryPolicyLine}
			}
		}
		return o, nil
	}
	o.Action, o.Changes = ActionCreate, []string{RecoveryLine}
	if pk != "" {
		o.Action, o.Changes = ActionUpdate, []string{RecoveryLine, "the flow exists: it is set on the default brand"}
	}
	return o, nil
}

// ensureRecovery creates the recovery flow and sets it on the default
// brand, unless the brand already has one. Every object is found by name
// or slug first, so a re-apply after a partial one converges.
func (a *API) ensureRecovery(ctx context.Context, slug string, step func(string)) error {
	brand, err := a.defaultBrand(ctx)
	if err != nil {
		return err
	}
	flow, err := a.findFlow(ctx, slug)
	if err != nil {
		return err
	}
	if brand.FlowRecovery != "" {
		step("the default brand already has a recovery flow")
		if flow != "" && flow == brand.FlowRecovery {
			return a.ensureRecoveryPolicy(ctx, flow, slug, step)
		}
		return nil
	}
	if flow == "" {
		var created remotePK
		// authentication "none": Authentik plans the flow for the user
		// while the administrator's API call makes the link.
		if err := a.do(ctx, http.MethodPost, "/api/v3/flows/instances/", map[string]any{
			"name": recoveryFlowName, "slug": slug, "title": recoveryFlowTitle,
			"designation": "recovery", "authentication": "none",
		}, &created); err != nil {
			return err
		}
		if flow = created.PK; flow == "" {
			return errors.New("Authentik did not return the new recovery flow")
		}
		step("created the recovery flow")
	}
	pw, err := a.passwordPrompt(ctx, stockPasswordPrompt, slug+"-field-password", "password", "Password", 0, step)
	if err != nil {
		return err
	}
	repeat, err := a.passwordPrompt(ctx, stockRepeatPrompt, slug+"-field-password-repeat", "password_repeat", "Password (repeat)", 1, step)
	if err != nil {
		return err
	}
	prompt, err := a.ensureByName(ctx, "/api/v3/stages/prompt/stages/", slug+"-password",
		map[string]any{"fields": []string{pw, repeat}}, step, "password prompt stage")
	if err != nil {
		return err
	}
	write, err := a.ensureByName(ctx, "/api/v3/stages/user_write/", slug+"-write",
		map[string]any{"user_creation_mode": "never_create"}, step, "user write stage")
	if err != nil {
		return err
	}
	login, err := a.ensureByName(ctx, "/api/v3/stages/user_login/", slug+"-login", map[string]any{}, step, "user login stage")
	if err != nil {
		return err
	}
	var bound listResult[struct {
		Stage string `json:"stage"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/flows/bindings/?page_size=200&target="+url.QueryEscape(flow), nil, &bound); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, b := range bound.Results {
		have[b.Stage] = true
	}
	for i, stage := range []string{prompt, write, login} {
		if have[stage] {
			continue
		}
		if err := a.do(ctx, http.MethodPost, "/api/v3/flows/bindings/", map[string]any{
			"target": flow, "stage": stage, "order": (i + 1) * 10,
		}, nil); err != nil {
			return err
		}
		step(fmt.Sprintf("bound recovery stage %d", i+1))
	}
	if err := a.ensureRecoveryPolicy(ctx, flow, slug, step); err != nil {
		return err
	}
	if err := a.do(ctx, http.MethodPatch, "/api/v3/core/brands/"+url.PathEscape(brand.UUID)+"/", map[string]any{"flow_recovery": flow}, nil); err != nil {
		return err
	}
	step("set the recovery flow on the default brand")
	return nil
}

type remoteExpression struct {
	PK         string `json:"pk"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

func recoveryPolicyName(slug string) string { return slug + "-owner-links-only" }

// findRecoveryPolicy returns Homestead's expression policy, or nil.
func (a *API) findRecoveryPolicy(ctx context.Context, slug string) (*remoteExpression, error) {
	name := recoveryPolicyName(slug)
	var out listResult[remoteExpression]
	if err := a.do(ctx, http.MethodGet, "/api/v3/policies/expression/?page_size=200&name="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Name == name {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// flowBindingTarget is the id policy bindings use for a flow. Authentik
// keys bindings by the flow's policy-binding-model id, not its pk; the pk
// is the fallback for a server that does not report it.
func (a *API) flowBindingTarget(ctx context.Context, flow, slug string) (string, error) {
	var out listResult[struct {
		PK  string `json:"pk"`
		PBM string `json:"policybindingmodel_ptr_id"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/flows/instances/?slug="+url.QueryEscape(slug), nil, &out); err != nil {
		return "", err
	}
	for _, f := range out.Results {
		if f.PK == flow && f.PBM != "" {
			return f.PBM, nil
		}
	}
	return flow, nil
}

func (a *API) recoveryPolicyBound(ctx context.Context, flow, slug, policy string) (bool, error) {
	flow, err := a.flowBindingTarget(ctx, flow, slug)
	if err != nil {
		return false, err
	}
	var out listResult[struct {
		Policy string `json:"policy"`
	}]
	if err := a.do(ctx, http.MethodGet, "/api/v3/policies/bindings/?page_size=200&target="+url.QueryEscape(flow), nil, &out); err != nil {
		return false, err
	}
	for _, b := range out.Results {
		if b.Policy == policy {
			return true, nil
		}
	}
	return false, nil
}

// recoveryPolicyCurrent reports whether the flow already has the policy,
// with the current expression, bound to it.
func (a *API) recoveryPolicyCurrent(ctx context.Context, flow, slug string) (bool, error) {
	p, err := a.findRecoveryPolicy(ctx, slug)
	if err != nil || p == nil || p.Expression != recoveryPolicyExpression {
		return false, err
	}
	return a.recoveryPolicyBound(ctx, flow, slug, p.PK)
}

// ensureRecoveryPolicy makes the flow refuse anyone not holding an
// owner-issued one-time link. Found by name first, so a re-apply
// converges and an old expression is corrected.
func (a *API) ensureRecoveryPolicy(ctx context.Context, flow, slug string, step func(string)) error {
	p, err := a.findRecoveryPolicy(ctx, slug)
	if err != nil {
		return err
	}
	switch {
	case p == nil:
		var created remotePK
		if err := a.do(ctx, http.MethodPost, "/api/v3/policies/expression/", map[string]any{
			"name": recoveryPolicyName(slug), "execution_logging": false, "expression": recoveryPolicyExpression,
		}, &created); err != nil {
			return err
		}
		if created.PK == "" {
			return errors.New("Authentik did not return the new recovery policy")
		}
		p = &remoteExpression{PK: created.PK}
		step("created the owner-links-only policy")
	case p.Expression != recoveryPolicyExpression:
		if err := a.do(ctx, http.MethodPatch, "/api/v3/policies/expression/"+url.PathEscape(p.PK)+"/", map[string]any{"expression": recoveryPolicyExpression}, nil); err != nil {
			return err
		}
		step("updated the owner-links-only policy")
	}
	bound, err := a.recoveryPolicyBound(ctx, flow, slug, p.PK)
	if err != nil || bound {
		return err
	}
	target, err := a.flowBindingTarget(ctx, flow, slug)
	if err != nil {
		return err
	}
	if err := a.do(ctx, http.MethodPost, "/api/v3/policies/bindings/", map[string]any{
		"target": target, "policy": p.PK, "order": 0, "enabled": true, "negate": false, "timeout": 30, "failure_result": false,
	}, nil); err != nil {
		return err
	}
	step("bound the owner-links-only policy to the recovery flow")
	return nil
}
