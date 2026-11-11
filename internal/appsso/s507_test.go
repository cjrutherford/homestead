package appsso

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const ncUsers = `{
 "admin": {"user_id":"admin","email":"owner@x.test","backend":"Database","groups":["admin"]},
 "h1": {"user_id":"h1","email":"Ada@x.test","backend":"user_oidc","groups":["admin","abc"]},
 "h2": {"user_id":"h2","email":"bob@x.test","backend":"user_oidc","groups":["abc"]}
}`

func TestRemoveNextcloudAdminUsesOccWithTheMatchedUID(t *testing.T) {
	f := stubDocker(t)
	f.fail = func(c call) (string, error) {
		if strings.Contains(strings.Join(c.args, " "), "user:list") {
			return ncUsers, nil
		}
		return "", nil
	}
	if err := RemoveNextcloudAdmin(context.Background(), "nextcloud-nextcloud", "ada", "ada@x.test"); err != nil {
		t.Fatal(err)
	}
	var removes []string
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.args, " "), "group:removeuser") {
			removes = append(removes, c.container+" "+c.user+" "+strings.Join(c.args, " "))
		}
	}
	if len(removes) != 1 || !strings.HasSuffix(removes[0], "group:removeuser admin h1") {
		t.Fatalf("%q", removes)
	}
}

func TestRemoveNextcloudAdminNeverTouchesLocalAdminOrNonAdmins(t *testing.T) {
	f := stubDocker(t)
	f.fail = func(c call) (string, error) {
		if strings.Contains(strings.Join(c.args, " "), "user:list") {
			return ncUsers, nil
		}
		return "", nil
	}
	// The owner's local account shares the email; a non-admin matches too.
	for _, who := range [][2]string{{"admin", "owner@x.test"}, {"bob", "bob@x.test"}} {
		if err := RemoveNextcloudAdmin(context.Background(), "nextcloud-nextcloud", who[0], who[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.args, " "), "group:removeuser") {
			t.Fatalf("unexpected removal: %v", c.args)
		}
	}
}

func TestRemoveNextcloudAdminReportsOccFailure(t *testing.T) {
	f := stubDocker(t)
	f.fail = func(call) (string, error) { return "", errors.New("down") }
	if err := RemoveNextcloudAdmin(context.Background(), "c", "ada", "ada@x.test"); err == nil {
		t.Fatal("want an error")
	}
}

func TestNextcloudListenerGrantsOnlyTheExactProviderGroup(t *testing.T) {
	var src string
	for _, f := range nextcloudAdminFiles {
		if strings.HasSuffix(f.Path, "LoginListener.php") {
			src = f.Body
		}
	}
	for _, want := range []string{"adminGroupIds", "'_0_admin'", "findProviderByIdentifier('authentik')", "$this->groups->get($gid)"} {
		if !strings.Contains(src, want) {
			t.Errorf("listener lacks %q", want)
		}
	}
	for _, bad := range []string{"getDisplayName", "groups->search"} {
		if strings.Contains(src, bad) {
			t.Errorf("listener still matches loosely: %q", bad)
		}
	}
}
