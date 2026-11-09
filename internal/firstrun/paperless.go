package firstrun

import (
	"context"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// PaperlessPeopleGroup is the Paperless group every person who signs in
// through Authentik joins (PAPERLESS_SOCIAL_ACCOUNT_DEFAULT_GROUPS names
// it in the manifest). Paperless gives a new single-sign-on account no
// permissions at all, which leaves its page empty with "Error loading
// settings"; the group is what makes a new account usable.
const PaperlessPeopleGroup = "Homestead people"

// paperlessScript runs under Django's shell in the pinned Paperless-ngx
// 3.3.4 image (through s6's with-contenv, so the application sees its
// supervised environment). It needs no password: the shell talks to the
// database directly, so a changed administrator password never blocks an
// update. The group is created once and never rewritten afterwards, so
// the owner's later edits stand. At creation it also joins existing
// ordinary accounts that belong to no group, so people who signed in
// before the group existed become usable.
//
// VERIFIED AGAINST Paperless-ngx 3.3.4: permission codenames (documents
// app), Group/User models and `manage.py shell` reading stdin.
const paperlessScript = `
from django.contrib.auth.models import Group, Permission, User
name = "` + PaperlessPeopleGroup + `"
models = ["document", "correspondent", "documenttype", "tag", "storagepath", "savedview", "note", "sharelink", "sharelinkbundle"]
codes = [a + "_" + m for m in models for a in ("view", "add", "change", "delete")]
codes += ["view_uisettings", "add_uisettings", "change_uisettings", "view_customfield", "view_paperlesstask"]
if Group.objects.filter(name=name).exists():
    print("HOMESTEAD-PAPERLESS ready")
else:
    perms = list(Permission.objects.filter(content_type__app_label="documents", codename__in=codes))
    if len(perms) != len(codes):
        print("HOMESTEAD-PAPERLESS missing-permissions")
    else:
        group = Group.objects.create(name=name)
        group.permissions.set(perms)
        for user in User.objects.filter(is_superuser=False, is_active=True, groups=None).exclude(username="AnonymousUser"):
            user.groups.add(group)
        print("HOMESTEAD-PAPERLESS created")
`

func paperlessFirstRun(ctx context.Context, m catalog.Manifest) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "paperless-ngx" || f.Service != "webserver" || f.Account != "admin" {
		return "", errors.New("Paperless-ngx first-run configuration is unsupported")
	}
	container := m.Name + "-" + f.Service
	return waitFor(ctx, m.Title, func() (Result, bool, error) {
		// Migrations run before the web server starts, so an answering
		// server means the tables exist.
		if _, err := execIn(ctx, container, "", "curl", "--silent", "--fail", "--max-time", "5", "--output", "/dev/null", "http://127.0.0.1:8000/accounts/login/"); err != nil {
			return "", false, errors.New("Paperless-ngx is starting")
		}
		out, err := execIn(ctx, container, paperlessScript, "sh", "-c", "cd /usr/src/paperless/src && with-contenv s6-setuidgid paperless python3 manage.py shell")
		switch {
		case err == nil && strings.Contains(out, "HOMESTEAD-PAPERLESS created"):
			return Created, true, nil
		case err == nil && strings.Contains(out, "HOMESTEAD-PAPERLESS ready"):
			return Ready, true, nil
		}
		return "", true, errors.New("Paperless-ngx first run could not create the group new sign-ins join; inspect Paperless's groups before retrying")
	})
}
