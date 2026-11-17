package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// MealieSeededAccount is the administrator every Mealie database is born
// with (email changeme@example.com, password "MyPassword"). Its presence
// also makes Mealie print those credentials on the login page and send
// the administrator to a first-login wizard (S448).
const MealieSeededAccount = "changeme@example.com"

// MealieVerified is the Mealie version the script below was verified
// against; the pin test fails when the package moves off it.
const MealieVerified = "3.29.0"

// MealieAdminEmail and MealieAdminUsername replace the seeded identity,
// so the credentials banner and the wizard go away with the password.
const (
	MealieAdminEmail    = "homestead-admin@homestead.lan"
	MealieAdminUsername = "homestead-admin"
)

// mealieScript runs inside the Mealie container with its own python and
// talks to Mealie's HTTP API on localhost. The new password arrives on
// stdin as JSON, never argv or environment. It prints one JSON line:
// {"state": "waiting"|"created"|"ready"|"failed", "detail": ...}.
//
// Steps, each safe to repeat: sign in with the seeded password and change
// it; then sign in with the generated one and replace the seeded email
// and user name. A seeded account whose password the owner already
// changed refuses both sign-ins, and is left exactly as it is.
//
// VERIFIED AGAINST Mealie v3.29.0: GET /api/app/about, POST
// /api/auth/token (form), GET /api/users/self, PUT /api/users/password
// (currentPassword, newPassword), PUT /api/users/{id} (the user's own
// fields), and GET /api/app/about/startup-info isFirstLogin, which stays
// true while any user has the email changeme@example.com.
const mealieScript = `
import json, sys, urllib.error, urllib.parse, urllib.request
B = "http://127.0.0.1:9000"
def out(state, detail=""):
    print(json.dumps({"state": state, "detail": detail}))
    sys.exit(0)
try:
    cfg = json.load(sys.stdin)
    seeded, new, email, name = cfg["account"], cfg["password"], cfg["email"], cfg["username"]
    if not (seeded and new and email and name): raise ValueError
except (ValueError, KeyError):
    out("failed", "the account and password are missing")
def req(method, path, token=None, body=None, form=None):
    data = None
    headers = {}
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if token: headers["Authorization"] = "Bearer " + token
    r = urllib.request.Request(B + path, data, headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=15) as x: return x.status, x.read()
    except urllib.error.HTTPError as e: return e.code, b""
    except (urllib.error.URLError, OSError): return 0, b""
def signin(user, password):
    s, b = req("POST", "/api/auth/token", form={"username": user, "password": password})
    if s != 200: return None
    try: return json.loads(b)["access_token"]
    except (ValueError, KeyError): return None
s, _ = req("GET", "/api/app/about")
if s != 200: out("waiting", "Mealie is not answering yet")
created = False
tok = signin(seeded, "MyPassword")
if tok:
    s, _ = req("PUT", "/api/users/password", tok, {"currentPassword": "MyPassword", "newPassword": new})
    if s != 200: out("failed", "Mealie refused the new password")
    created = True
    tok = None
tok = signin(seeded, new)
if not tok:
    if created: out("failed", "the new password was not accepted")
    out("ready", "the account is not the seeded administrator any more")
s, b = req("GET", "/api/users/self", tok)
if s != 200: out("failed", "the administrator could not be read")
u = json.loads(b)
keep = ("id", "fullName", "admin", "group", "household", "advanced", "canInvite", "canManage", "canManageHousehold", "canOrganize")
body = {k: u[k] for k in keep if k in u}
body.update({"email": email, "username": name, "fullName": "Homestead Administrator"})
s, _ = req("PUT", "/api/users/" + u["id"], tok, body)
if s != 200: out("failed", "Mealie refused the administrator's new identity")
if signin(email, new) is None or signin(seeded, "MyPassword") is not None:
    out("failed", "the seeded login was not replaced")
out("created" if created else "ready")
`

func mealieStep(ctx context.Context, container, password string) (Result, bool, error) {
	input, _ := json.Marshal(map[string]string{"account": MealieSeededAccount, "password": password, "email": MealieAdminEmail, "username": MealieAdminUsername})
	raw, err := execIn(ctx, container, string(input), "python3", "-c", mealieScript)
	raw = strings.ReplaceAll(raw, password, "[redacted]")
	return stepReply("Mealie", raw, password, err)
}

// stepReply turns a routine script's one-line JSON answer into a step
// result: created and ready finish, waiting retries, failed stops.
func stepReply(app, raw, password string, err error) (Result, bool, error) {
	if err != nil {
		return "", false, errors.New(strings.ReplaceAll(err.Error(), password, "[redacted]"))
	}
	var reply struct{ State, Detail string }
	if json.Unmarshal([]byte(lastLine(raw)), &reply) != nil {
		return "", false, errors.New(app + " returned an unexpected first-run response")
	}
	switch reply.State {
	case "created":
		return Created, true, nil
	case "ready":
		return Ready, true, nil
	case "waiting":
		return "", false, errors.New(app + " is starting: " + reply.Detail)
	}
	return "", true, errors.New(app + " first run failed: " + reply.Detail)
}

// mealieFirstRun replaces the seeded administrator's well-known password
// and identity with a generated, revealable password and a Homestead
// identity, and leaves an account the owner already changed alone. Until
// it finishes, changeme@example.com still opens with "MyPassword".
func mealieFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "mealie" || f.Account != MealieSeededAccount {
		return "", errors.New("Mealie first-run configuration is unsupported")
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	container := m.Name + "-" + f.Service
	res, err := waitFor(ctx, m.Title, func() (Result, bool, error) { return mealieStep(ctx, container, password) })
	if err != nil {
		return "", errors.New(err.Error() + " (until this finishes Mealie's seeded administrator " + MealieSeededAccount + " still opens with the password \"MyPassword\": change it in Mealie under your user profile)")
	}
	return res, nil
}
