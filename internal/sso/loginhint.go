package sso

import (
	"context"
	"net/http"
	"net/url"
)

// The login page hint (ADR-049 amendment, S336). Authentik shows a
// "Forgot username or password?" link only when the identification
// stage's own recovery flow is set; the brand's flow_recovery (needed for
// one-time links) does not add one. The appliance has no email, so no
// self-service link is wanted. The flow title is the one text Authentik
// renders on the sign-in card, so Homestead sets it to the hint.
const (
	// AuthFlowSlug is Authentik's stock sign-in flow.
	AuthFlowSlug = "default-authentication-flow"
	// LoginHint is the title set on that flow.
	LoginHint = "Forgot your password? Ask the owner of this Homestead to reset it."
	// LoginHintLine is the plan's change line.
	LoginHintLine = "Authentik's sign-in page tells people to ask the owner to reset a forgotten password."
)

type remoteFlow struct {
	PK    string `json:"pk"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// authFlow returns the default authentication flow, or nil when the
// owner removed it.
func (a *API) authFlow(ctx context.Context) (*remoteFlow, error) {
	var out listResult[remoteFlow]
	if err := a.do(ctx, http.MethodGet, "/api/v3/flows/instances/?slug="+url.QueryEscape(AuthFlowSlug), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if out.Results[i].Slug == AuthFlowSlug {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

func (a *API) inspectLoginHint(ctx context.Context, hint string) (Object, error) {
	o := Object{Kind: "Login page hint", Name: AuthFlowSlug}
	f, err := a.authFlow(ctx)
	if err != nil {
		return o, err
	}
	switch {
	case f == nil:
		o.Action, o.Changes = ActionUnchanged, []string{"Authentik has no " + AuthFlowSlug + ": the hint is skipped"}
	case f.Title == hint:
		o.Action, o.Changes = ActionUnchanged, []string{"the sign-in page already shows the hint"}
	default:
		o.Action, o.Changes = ActionUpdate, []string{LoginHintLine}
	}
	return o, nil
}

// ensureLoginHint sets the flow title when it differs, so a re-apply
// posts nothing.
func (a *API) ensureLoginHint(ctx context.Context, hint string, step func(string)) error {
	f, err := a.authFlow(ctx)
	if err != nil {
		return err
	}
	if f == nil || f.Title == hint {
		return nil
	}
	if err := a.do(ctx, http.MethodPatch, "/api/v3/flows/instances/"+url.PathEscape(AuthFlowSlug)+"/", map[string]any{"title": hint}, nil); err != nil {
		return err
	}
	step("set the sign-in page hint")
	return nil
}
