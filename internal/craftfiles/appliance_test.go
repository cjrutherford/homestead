package craftfiles

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// S326: this appliance's own Nextcloud links with the administrator
// password Homestead generated at its install, with no password typed,
// either from the setup (LinkAppliance) or from a plan the owner
// approves on the Craft files page.
func (e *mgrEnv) withAppliance(password string) {
	e.m.Appliance = func() (Appliance, bool) {
		return Appliance{BaseURL: e.baseURL, Account: "alice", Password: func() (string, error) { return password, nil }}, true
	}
}

func TestLinkApplianceUsesTheGeneratedAdminPassword(t *testing.T) {
	e := newMgr(t)
	if err := e.m.LinkAppliance(context.Background(), "", "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("no Nextcloud on the appliance: %v", err)
	}
	e.withAppliance("account-login-password")
	if err := e.m.LinkAppliance(context.Background(), "", "owner"); err != nil {
		t.Fatal(err)
	}
	link, linked, _ := e.m.CurrentLink()
	if !linked || link.BaseURL != e.baseURL || link.Account != "alice" || link.Root != DefaultRoot || e.secrets.m[SecretName] != testPassword {
		t.Fatalf("link = %+v %v, secrets = %v", link, linked, e.secrets.m)
	}
	audits := strings.Join(e.audits, "\n")
	if !strings.Contains(audits, "owner|craftfiles.link|ok|account=alice root=Homestead nextcloud=appliance") ||
		strings.Contains(audits, "account-login-password") || strings.Contains(audits, testPassword) {
		t.Fatalf("audits = %s", audits)
	}
	if err := e.m.LinkAppliance(context.Background(), "", "owner"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second link: %v", err)
	}
}

func TestLinkApplianceWrongAdminPasswordStoresNothing(t *testing.T) {
	e := newMgr(t)
	e.withAppliance("nope")
	if err := e.m.LinkAppliance(context.Background(), "", "owner"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("wrong admin password: %v", err)
	}
	if _, linked, _ := e.m.CurrentLink(); linked || len(e.secrets.m) != 0 {
		t.Fatal("a failed link left state behind")
	}
}

func TestPlanApplianceNeedsNoPassword(t *testing.T) {
	e := newMgr(t)
	if _, err := e.m.Plan(PlanRequest{Action: ActionLink, Appliance: true}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("plan without a Nextcloud: %v", err)
	}
	e.withAppliance("account-login-password")
	st, _ := e.m.Status()
	if st.ApplianceNextcloud == nil || st.ApplianceNextcloud.BaseURL != e.baseURL {
		t.Fatalf("status = %+v", st.ApplianceNextcloud)
	}
	p, st, err := link(t, e, PlanRequest{Action: ActionLink, Appliance: true}, ApproveRequest{})
	if err != nil || !p.Appliance || p.BaseURL != e.baseURL || p.Root != DefaultRoot || !st.Linked || !st.DaemonCreated {
		t.Fatalf("plan = %+v, status = %+v, err = %v", p, st, err)
	}
}
