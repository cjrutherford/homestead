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

	// Authentik's stock password-change prompts, reused when present.
	stockPasswordPrompt = "default-password-change-field-password"
	stockRepeatPrompt   = "default-password-change-field-password-repeat"
)

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
	if brand.FlowRecovery != "" {
		o.Action, o.Changes = ActionUnchanged, []string{"the default brand already has a recovery flow: it is left as it is"}
		return o, nil
	}
	pk, err := a.findFlow(ctx, slug)
	if err != nil {
		return o, err
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
	if brand.FlowRecovery != "" {
		step("the default brand already has a recovery flow")
		return nil
	}
	flow, err := a.findFlow(ctx, slug)
	if err != nil {
		return err
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
	if err := a.do(ctx, http.MethodPatch, "/api/v3/core/brands/"+url.PathEscape(brand.UUID)+"/", map[string]any{"flow_recovery": flow}, nil); err != nil {
		return err
	}
	step("set the recovery flow on the default brand")
	return nil
}
