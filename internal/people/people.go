// Package people is the household's people (S317, ADR-043 section 4): the
// Authentik users Homestead created or adopted, the apps each is granted,
// and the read-back that shows drift. Homestead's record wins: nothing in
// Authentik is changed to match Authentik, and nothing is reverted unless
// the owner chooses Put back.
//
// A person is never a Homestead administrator. This package touches only
// users it has a record of, and groups with the homestead- prefix. No
// password or recovery link is stored or logged; a generated one is
// returned once to the caller.
package people

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"homestead/internal/sso"
)

// Roles in an app.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Person states and origins.
const (
	StateActive   = "active"
	StateDisabled = "disabled"
	OriginCreated = "created"
	OriginAdopted = "adopted"
)

// Errors the API maps to status codes. Messages are shown to the owner.
var (
	ErrNoToken  = errors.New("people need the kept Authentik API token")
	ErrNotFound = errors.New("no such person")
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
	// ErrRefused wraps a reason the owner can act on (a policy, a missing
	// recovery flow, an app without groups). Its text is shown.
	ErrRefused = errors.New("not done")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Service holds what People needs. Every field but Now is required.
type Service struct {
	DB *sql.DB
	// API returns an Authentik client built with the kept token, or
	// ErrNoToken. The token lives only in that client.
	API func() (*sso.API, error)
	// Apps lists the packages that sign in with Authentik (the apps that
	// can be granted).
	Apps func() ([]string, error)
	// OwnerSub is the linked owner's Authentik subject; that user is the
	// owner, never a person, and is not listed.
	OwnerSub func() string
	Audit    func(actor, action, outcome, detail string)
	Now      func() time.Time
	// EndSessions ends a person's dashboard sessions (ADR-050 §1): when
	// they are disabled, removed or no longer match Authentik.
	EndSessions func(personID int64)
	// PublicAuthentik returns Authentik's public (tunnel) address, or "".
	// A one-time link then also comes back on that address, so a person
	// away from the local network can open it.
	PublicAuthentik func() string
}

func (s *Service) endSessions(id int64) {
	if s.EndSessions != nil {
		s.EndSessions(id)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) audit(actor, action, outcome, detail string) {
	if s.Audit != nil {
		s.Audit(actor, action, outcome, detail)
	}
}

// Grant is one app a person may sign in to.
type Grant struct {
	App  string `json:"app"`
	Role string `json:"role"`
}

// DriftItem is one difference between Homestead's record and Authentik.
type DriftItem struct {
	// Kind is field or grant.
	Kind  string `json:"kind"`
	Field string `json:"field,omitempty"`
	App   string `json:"app,omitempty"`
	// Homestead and Authentik are the two values, in words.
	Homestead string `json:"homestead"`
	Authentik string `json:"authentik"`
}

// Statuses of a person's read-back.
const (
	StatusOK        = "ok"
	StatusDrifted   = "drifted"
	StatusMissing   = "missing"
	StatusUnchecked = "unchecked"
)

// Person is Homestead's record of one person, with the read-back.
type Person struct {
	ID          int64       `json:"id"`
	DisplayName string      `json:"display_name"`
	Username    string      `json:"username"`
	Email       string      `json:"email"`
	State       string      `json:"state"`
	Origin      string      `json:"origin"`
	CreatedBy   string      `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	Grants      []Grant     `json:"grants"`
	Status      string      `json:"status"`
	Drift       []DriftItem `json:"drift"`
	pk          int
	// uid is the Authentik user's uid: the OIDC subject under the
	// dashboard provider's hashed_user_id mode. Kept so a sign-in is matched
	// to the record, never by username or email.
	uid string
}

// Unmanaged is an Authentik user Homestead did not create.
type Unmanaged struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Active   bool   `json:"active"`
}

// View is the People page: the people, the others in Authentik, the apps
// that can be granted, and whether Homestead can act.
type View struct {
	TokenKept bool        `json:"token_kept"`
	People    []Person    `json:"people"`
	Unmanaged []Unmanaged `json:"unmanaged"`
	Apps      []string    `json:"apps"`
	// ReadError says why the read-back could not run, when it could not.
	ReadError string `json:"read_error,omitempty"`
}

// Revealed is a credential shown once. Kind is password or link. A link's
// Value opens on the local network; PublicValue is the same link on
// Authentik's public address, empty while there is none.
type Revealed struct {
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	PublicValue string `json:"public_value,omitempty"`
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,149}$`)

func validName(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 150 }

func validEmail(s string) bool {
	if s == "" {
		return true
	}
	at := strings.IndexByte(s, '@')
	return len(s) <= 254 && at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n,;<>")
}

func validRole(r string) bool { return r == RoleUser || r == RoleAdmin }

// --- records ---

const personCols = `id, display_name, username, email, authentik_pk, state, origin, created_by, created_at, authentik_uid`

func scanPerson(sc interface{ Scan(...any) error }) (Person, error) {
	var p Person
	var at string
	if err := sc.Scan(&p.ID, &p.DisplayName, &p.Username, &p.Email, &p.pk, &p.State, &p.Origin, &p.CreatedBy, &at, &p.uid); err != nil {
		return p, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, at)
	p.Grants, p.Drift, p.Status = []Grant{}, []DriftItem{}, StatusUnchecked
	return p, nil
}

func (s *Service) person(id int64) (Person, error) {
	p, err := scanPerson(s.DB.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Grants, err = s.grants(id)
	return p, err
}

// AppGrants is what a person may open (S337, ADR-050 §4): whether the
// person is active and their grants from Homestead's own record. It never
// calls Authentik. ErrNotFound for an unknown id.
func (s *Service) AppGrants(id int64) (active bool, grants []Grant, err error) {
	p, err := s.person(id)
	if err != nil {
		return false, nil, err
	}
	return p.State == StateActive, p.Grants, nil
}

func (s *Service) grants(id int64) ([]Grant, error) {
	rows, err := s.DB.Query(`SELECT app, role FROM people_grants WHERE person_id = ? ORDER BY app`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.App, &g.Role); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) records() ([]Person, error) {
	rows, err := s.DB.Query(`SELECT ` + personCols + ` FROM people ORDER BY lower(display_name), id`)
	if err != nil {
		return nil, err
	}
	var out []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		if out[i].Grants, err = s.grants(out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) setGrant(id int64, app, role string) error {
	_, err := s.DB.Exec(`INSERT INTO people_grants (person_id, app, role) VALUES (?, ?, ?)
		ON CONFLICT(person_id, app) DO UPDATE SET role = excluded.role`, id, app, role)
	return err
}

func (s *Service) dropGrant(id int64, app string) error {
	_, err := s.DB.Exec(`DELETE FROM people_grants WHERE person_id = ? AND app = ?`, id, app)
	return err
}

func grantFor(gs []Grant, app string) (Grant, bool) {
	for _, g := range gs {
		if g.App == app {
			return g, true
		}
	}
	return Grant{}, false
}

// --- read-back ---

func (s *Service) apps() ([]string, error) {
	if s.Apps == nil {
		return []string{}, nil
	}
	apps, err := s.Apps()
	if apps == nil {
		apps = []string{}
	}
	sort.Strings(apps)
	return apps, err
}

// appOfGroup returns the app and role a homestead-<app>-users/admins group
// stands for, among apps.
func appOfGroup(group string, apps []string) (app, role string, ok bool) {
	for _, a := range apps {
		u, ad := sso.AppGroups(a)
		switch group {
		case u:
			return a, RoleUser, true
		case ad:
			return a, RoleAdmin, true
		}
	}
	return "", "", false
}

// inspect compares the record with the Authentik user. nil acct: missing.
func inspect(p Person, acct *sso.RemoteAccount, apps []string) (string, []DriftItem) {
	if acct == nil {
		return StatusMissing, []DriftItem{}
	}
	items := []DriftItem{}
	field := func(name, mine, theirs string) {
		if mine != theirs {
			items = append(items, DriftItem{Kind: "field", Field: name, Homestead: mine, Authentik: theirs})
		}
	}
	field("username", p.Username, acct.Username)
	field("name", p.DisplayName, acct.Name)
	field("email", p.Email, acct.Email)
	state := StateDisabled
	if acct.IsActive {
		state = StateActive
	}
	field("state", p.State, state)
	// Memberships: what Authentik has against the grants.
	have := map[string]string{}
	for _, g := range acct.HomesteadGroups() {
		if app, role, ok := appOfGroup(g, apps); ok {
			if role == RoleAdmin || have[app] == "" {
				have[app] = role
			}
		}
	}
	seen := map[string]bool{}
	for _, g := range p.Grants {
		seen[g.App] = true
		switch got := have[g.App]; {
		case got == g.Role:
		default:
			items = append(items, DriftItem{Kind: "grant", App: g.App, Homestead: roleWord(g.Role), Authentik: roleWord(got)})
		}
	}
	for app, role := range have {
		if !seen[app] {
			items = append(items, DriftItem{Kind: "grant", App: app, Homestead: roleWord(""), Authentik: roleWord(role)})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Field+items[i].App < items[j].Field+items[j].App
	})
	if len(items) > 0 {
		return StatusDrifted, items
	}
	return StatusOK, items
}

func roleWord(r string) string {
	switch r {
	case RoleUser:
		return "user"
	case RoleAdmin:
		return "admin"
	}
	return "no access"
}

// List returns the page. Without the kept token it still lists the record,
// unchecked, and says so.
func (s *Service) List(ctx context.Context) (View, error) {
	v := View{People: []Person{}, Unmanaged: []Unmanaged{}}
	var err error
	if v.Apps, err = s.apps(); err != nil {
		return v, err
	}
	if v.People, err = s.records(); err != nil {
		return v, err
	}
	if v.People == nil {
		v.People = []Person{}
	}
	api, err := s.API()
	if errors.Is(err, ErrNoToken) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.TokenKept = true
	accounts, err := api.ListAccounts(ctx)
	if err != nil {
		v.ReadError = err.Error()
		return v, nil
	}
	byPK := map[int]*sso.RemoteAccount{}
	for i := range accounts {
		byPK[accounts[i].PK] = &accounts[i]
	}
	known := map[int]bool{}
	for i := range v.People {
		p := &v.People[i]
		known[p.pk] = true
		p.Status, p.Drift = inspect(*p, byPK[p.pk], v.Apps)
		if a := byPK[p.pk]; a != nil && a.UID != "" && p.uid != a.UID && p.uid == "" {
			// Older records have no uid yet: remember it (never
			// overwrite one, which would move a sign-in to another user).
			_, _ = s.DB.Exec(`UPDATE people SET authentik_uid = ? WHERE id = ? AND authentik_uid = ''`, a.UID, p.ID)
		}
	}
	owner := ""
	if s.OwnerSub != nil {
		owner = s.OwnerSub()
	}
	for _, u := range accounts {
		if known[u.PK] || u.Managed() || (owner != "" && u.UID == owner) || u.Username == "akadmin" {
			continue
		}
		v.Unmanaged = append(v.Unmanaged, Unmanaged{Username: u.Username, Name: u.Name, Email: u.Email, Active: u.IsActive})
	}
	return v, nil
}

// --- credentials ---

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword is 20 characters from an alphabet without look-alikes,
// with at least one lower case, upper case and digit.
func generatePassword() (string, error) {
	for {
		b := make([]byte, 20)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
			if err != nil {
				return "", err
			}
			b[i] = passwordAlphabet[n.Int64()]
		}
		s := string(b)
		if strings.ContainsAny(s, "abcdefghijkmnopqrstuvwxyz") && strings.ContainsAny(s, "ABCDEFGHJKLMNPQRSTUVWXYZ") && strings.ContainsAny(s, "23456789") {
			return s, nil
		}
	}
}

// Credential methods.
const (
	MethodLink      = "link"
	MethodGenerated = "generated"
)

// credential sets up how a person first signs in, or signs in again after
// a reset. A given password (from the owner) is set and not returned.
func (s *Service) credential(ctx context.Context, api *sso.API, pk int, method, given string) (*Revealed, error) {
	switch {
	case given != "":
		if err := api.SetPassword(ctx, pk, given); err != nil {
			return nil, credError(err)
		}
		return nil, nil
	case method == MethodGenerated:
		pw, err := generatePassword()
		if err != nil {
			return nil, err
		}
		if err := api.SetPassword(ctx, pk, pw); err != nil {
			return nil, credError(err)
		}
		return &Revealed{Kind: "password", Value: pw}, nil
	case method == MethodLink || method == "":
		link, err := api.RecoveryLink(ctx, pk)
		if err != nil {
			return nil, credError(err)
		}
		rev := &Revealed{Kind: "link", Value: link}
		if s.PublicAuthentik != nil {
			rev.PublicValue = sso.PublicLink(link, s.PublicAuthentik())
		}
		return rev, nil
	}
	return nil, invalid("method must be %q or %q", MethodLink, MethodGenerated)
}

// credError explains the likely cause of a refused credential call without
// quoting Authentik.
func credError(err error) error {
	var ae *sso.APIError
	if errors.As(err, &ae) && ae.Status == 400 {
		return fmt.Errorf("%w: Authentik refused the password or link: its password policy, or a missing recovery flow in the default brand, may be the cause", ErrRefused)
	}
	return err
}

// --- actions ---

func (s *Service) client() (*sso.API, error) {
	api, err := s.API()
	if err != nil {
		return nil, err
	}
	return api, nil
}

// AddRequest adds a person. With a Password it is set and not returned;
// otherwise Method says whether a one-time link or a generated password is
// revealed.
type AddRequest struct {
	DisplayName string  `json:"display_name"`
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Method      string  `json:"method"`
	Password    string  `json:"password,omitempty"`
	Grants      []Grant `json:"grants,omitempty"`
}

// AddResult is what adding a person returns. Warning says what a later
// step could not do; SignInFailed says how they sign in was not set up
// (ADR-049 §4), so a client can offer a generated password instead.
type AddResult struct {
	Person       Person    `json:"person"`
	Revealed     *Revealed `json:"revealed"`
	Warning      string    `json:"warning"`
	SignInFailed bool      `json:"sign_in_failed,omitempty"`
}

// Add creates the Authentik user, records the person, and sets up how they
// sign in. If a later step fails the person exists and the warning says
// what to redo.
func (s *Service) Add(ctx context.Context, actor string, req AddRequest) (AddResult, error) {
	req.DisplayName, req.Username, req.Email = strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Username), strings.TrimSpace(req.Email)
	switch {
	case !validName(req.DisplayName):
		return AddResult{}, invalid("a name is required (up to 150 characters)")
	case !usernameRe.MatchString(req.Username):
		return AddResult{}, invalid("a username uses letters, digits and . _ @ + - only")
	case !validEmail(req.Email):
		return AddResult{}, invalid("that email address is not valid")
	case req.Method != "" && req.Method != MethodLink && req.Method != MethodGenerated:
		return AddResult{}, invalid("method must be %q or %q", MethodLink, MethodGenerated)
	}
	apps, err := s.apps()
	if err != nil {
		return AddResult{}, err
	}
	for _, g := range req.Grants {
		if err := checkGrant(g, apps); err != nil {
			return AddResult{}, err
		}
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE lower(username) = lower(?)`, req.Username).Scan(&n); err != nil {
		return AddResult{}, err
	}
	if n > 0 {
		return AddResult{}, fmt.Errorf("%w: %s is already a person here", ErrConflict, req.Username)
	}
	api, err := s.client()
	if err != nil {
		return AddResult{}, err
	}
	acct, err := api.CreateAccount(ctx, sso.AccountFields{Username: req.Username, Name: req.DisplayName, Email: req.Email})
	if err != nil {
		s.audit(actor, "people.add", "error", "could not create "+req.Username)
		var ae *sso.APIError
		if errors.As(err, &ae) && ae.Status == 400 {
			return AddResult{}, fmt.Errorf("%w: Authentik refused the user (the username or email may already exist there; adopt the existing user instead)", ErrConflict)
		}
		return AddResult{}, err
	}
	res, err := s.DB.Exec(`INSERT INTO people (display_name, username, email, authentik_pk, state, origin, created_by, created_at, authentik_uid)
		VALUES (?, ?, ?, ?, 'active', 'created', ?, ?, ?)`, req.DisplayName, req.Username, req.Email, acct.PK, actor, s.now().Format(time.RFC3339), acct.UID)
	if err != nil {
		// Do not leave an unrecorded user behind.
		_ = api.DeleteAccount(ctx, acct.PK)
		return AddResult{}, err
	}
	id, _ := res.LastInsertId()
	s.audit(actor, "people.add", "ok", fmt.Sprintf("added %s (person %d)", req.Username, id))
	rev, cerr := s.credential(ctx, api, acct.PK, req.Method, req.Password)
	var problems []string
	if err := s.syncMember(ctx, api, acct.PK, true); err != nil {
		problems = append(problems, fmt.Sprintf("they could not be added to the dashboard sign-in group (%v)", err))
	}
	if cerr != nil {
		problems = append(problems, fmt.Sprintf("how they sign in was not set up (%v)", cerr))
	}
	// The grants are made even when sign-in failed, so a password set
	// afterwards opens the chosen apps.
	for _, g := range req.Grants {
		if err := s.applyGrant(ctx, api, actor, id, acct.PK, g); err != nil {
			problems = append(problems, fmt.Sprintf("%s could not be granted (%v)", g.App, err))
			break
		}
	}
	p, err := s.person(id)
	if err != nil {
		return AddResult{}, err
	}
	out := AddResult{Person: p, Revealed: rev, SignInFailed: cerr != nil}
	if len(problems) > 0 {
		out.Warning = fmt.Sprintf("%s was added, but %s.", req.Username, strings.Join(problems, ", and "))
		if cerr != nil {
			out.Warning += " Reset their password to try again."
		}
	}
	return out, nil
}

func checkGrant(g Grant, apps []string) error {
	if !validRole(g.Role) {
		return invalid("a role is user or admin")
	}
	for _, a := range apps {
		if a == g.App {
			return nil
		}
	}
	return invalid("%q does not sign in with Authentik, so it cannot be granted", g.App)
}

// EditRequest changes the given fields; nil leaves a field alone.
type EditRequest struct {
	DisplayName *string `json:"display_name"`
	Username    *string `json:"username"`
	Email       *string `json:"email"`
}

// Edit changes a person's name, username and email in Authentik and in
// the record.
func (s *Service) Edit(ctx context.Context, actor string, id int64, req EditRequest) (Person, error) {
	p, err := s.person(id)
	if err != nil {
		return p, err
	}
	fields := map[string]any{}
	if req.DisplayName != nil {
		v := strings.TrimSpace(*req.DisplayName)
		if !validName(v) {
			return p, invalid("a name is required (up to 150 characters)")
		}
		fields["name"], p.DisplayName = v, v
	}
	if req.Username != nil {
		v := strings.TrimSpace(*req.Username)
		if !usernameRe.MatchString(v) {
			return p, invalid("a username uses letters, digits and . _ @ + - only")
		}
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE lower(username) = lower(?) AND id <> ?`, v, id).Scan(&n); err != nil {
			return p, err
		}
		if n > 0 {
			return p, fmt.Errorf("%w: %s is already a person here", ErrConflict, v)
		}
		fields["username"], p.Username = v, v
	}
	if req.Email != nil {
		v := strings.TrimSpace(*req.Email)
		if !validEmail(v) {
			return p, invalid("that email address is not valid")
		}
		fields["email"], p.Email = v, v
	}
	if len(fields) == 0 {
		return p, invalid("nothing to change")
	}
	api, err := s.client()
	if err != nil {
		return p, err
	}
	if err := api.PatchAccount(ctx, p.pk, fields); err != nil {
		s.audit(actor, "people.edit", "error", fmt.Sprintf("person %d not changed", id))
		return p, err
	}
	if _, err := s.DB.Exec(`UPDATE people SET display_name = ?, username = ?, email = ? WHERE id = ?`, p.DisplayName, p.Username, p.Email, id); err != nil {
		return p, err
	}
	s.audit(actor, "people.edit", "ok", fmt.Sprintf("edited %s (person %d): %s", p.Username, id, fieldNames(fields)))
	return p, nil
}

func fieldNames(m map[string]any) string {
	var n []string
	for k := range m {
		n = append(n, k)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// ResetPassword sets a new way to sign in: a link, a generated password
// (both returned once), or the owner's own password (not returned).
func (s *Service) ResetPassword(ctx context.Context, actor string, id int64, method, given string) (*Revealed, error) {
	p, err := s.person(id)
	if err != nil {
		return nil, err
	}
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	rev, err := s.credential(ctx, api, p.pk, method, given)
	if err != nil {
		s.audit(actor, "people.password-reset", "error", fmt.Sprintf("person %d: the reset failed", id))
		return nil, err
	}
	how := method
	if given != "" {
		how = "given"
	}
	if how == "" {
		how = MethodLink
	}
	s.audit(actor, "people.password-reset", "ok", fmt.Sprintf("reset %s (person %d) by %s", p.Username, id, how))
	return rev, nil
}

// ResetSecondFactor removes the person's registered authenticators so
// they enrol again.
func (s *Service) ResetSecondFactor(ctx context.Context, actor string, id int64) (removed, skipped int, err error) {
	p, err := s.person(id)
	if err != nil {
		return 0, 0, err
	}
	api, err := s.client()
	if err != nil {
		return 0, 0, err
	}
	removed, skipped, err = api.ResetSecondFactor(ctx, p.pk)
	if err != nil {
		s.audit(actor, "people.mfa-reset", "error", fmt.Sprintf("person %d", id))
		return removed, skipped, err
	}
	s.audit(actor, "people.mfa-reset", "ok", fmt.Sprintf("reset second factor of %s (person %d): %d removed", p.Username, id, removed))
	return removed, skipped, nil
}

// SetActive disables or enables a person. Their data stays in the apps.
func (s *Service) SetActive(ctx context.Context, actor string, id int64, active bool) error {
	p, err := s.person(id)
	if err != nil {
		return err
	}
	api, err := s.client()
	if err != nil {
		return err
	}
	action, state := "people.disable", StateDisabled
	if active {
		action, state = "people.enable", StateActive
	}
	if err := api.PatchAccount(ctx, p.pk, map[string]any{"is_active": active}); err != nil {
		s.audit(actor, action, "error", fmt.Sprintf("person %d", id))
		return err
	}
	if _, err := s.DB.Exec(`UPDATE people SET state = ? WHERE id = ?`, state, id); err != nil {
		return err
	}
	if !active {
		s.endSessions(id)
	}
	s.audit(actor, action, "ok", fmt.Sprintf("%s (person %d)", p.Username, id))
	if err := s.syncMember(ctx, api, p.pk, active); err != nil {
		s.audit(actor, action, "error", fmt.Sprintf("%s (person %d): dashboard sign-in group not updated: %v", p.Username, id, err))
		return fmt.Errorf("%s is %s, but their place in the dashboard sign-in group was not updated (%v): try again", p.Username, state, err)
	}
	return nil
}

// Remove removes a person. A user Homestead created is deleted from
// Authentik, when it still carries the Homestead tag; an adopted user is
// released: left in Authentik, out of the homestead- groups and untagged.
// App data stays in the apps either way.
func (s *Service) Remove(ctx context.Context, actor string, id int64) error {
	p, err := s.person(id)
	if err != nil {
		return err
	}
	api, err := s.client()
	if err != nil {
		return err
	}
	acct, err := api.GetAccount(ctx, p.pk)
	if err != nil {
		return err
	}
	apps, err := s.apps()
	if err != nil {
		return err
	}
	if acct != nil {
		if p.Origin == OriginCreated && acct.Managed() {
			err = api.DeleteAccount(ctx, p.pk)
		} else {
			err = s.release(ctx, api, p, *acct, apps)
		}
		if err != nil {
			s.audit(actor, "people.remove", "error", fmt.Sprintf("person %d", id))
			return err
		}
	}
	if err := s.forget(id); err != nil {
		return err
	}
	s.audit(actor, "people.remove", "ok", fmt.Sprintf("removed %s (person %d, %s)", p.Username, id, p.Origin))
	return nil
}

// release takes an adopted (or untagged) user out of Homestead's hands.
func (s *Service) release(ctx context.Context, api *sso.API, p Person, acct sso.RemoteAccount, apps []string) error {
	if err := s.syncMember(ctx, api, p.pk, false); err != nil {
		return err
	}
	for _, g := range acct.HomesteadGroups() {
		if _, _, ok := appOfGroup(g, apps); ok {
			if err := api.RemoveFromGroup(ctx, g, p.pk); err != nil {
				return err
			}
		}
	}
	if acct.Managed() {
		return api.UntagAccount(ctx, acct)
	}
	return nil
}

func (s *Service) forget(id int64) error {
	s.endSessions(id)
	if _, err := s.DB.Exec(`DELETE FROM people_grants WHERE person_id = ?`, id); err != nil {
		return err
	}
	_, err := s.DB.Exec(`DELETE FROM people WHERE id = ?`, id)
	return err
}

// Grant lets a person sign in to an app, as user or admin. An admin is
// also a user.
func (s *Service) Grant(ctx context.Context, actor string, id int64, g Grant) (Person, error) {
	p, err := s.person(id)
	if err != nil {
		return p, err
	}
	apps, err := s.apps()
	if err != nil {
		return p, err
	}
	if err := checkGrant(g, apps); err != nil {
		return p, err
	}
	api, err := s.client()
	if err != nil {
		return p, err
	}
	if err := s.applyGrant(ctx, api, actor, id, p.pk, g); err != nil {
		return p, err
	}
	return s.person(id)
}

func (s *Service) applyGrant(ctx context.Context, api *sso.API, actor string, id int64, pk int, g Grant) error {
	users, admins := sso.AppGroups(g.App)
	if err := api.AddToGroup(ctx, users, pk); err != nil {
		s.audit(actor, "people.grant", "error", fmt.Sprintf("person %d: %s", id, g.App))
		return groupError(g.App, err)
	}
	if g.Role == RoleAdmin {
		if err := api.AddToGroup(ctx, admins, pk); err != nil {
			s.audit(actor, "people.grant", "error", fmt.Sprintf("person %d: %s", id, g.App))
			return groupError(g.App, err)
		}
	} else if err := api.RemoveFromGroup(ctx, admins, pk); err != nil {
		return groupError(g.App, err)
	}
	if err := s.setGrant(id, g.App, g.Role); err != nil {
		return err
	}
	s.audit(actor, "people.grant", "ok", fmt.Sprintf("person %d: %s as %s", id, g.App, g.Role))
	return nil
}

func groupError(app string, err error) error {
	if strings.Contains(err.Error(), "does not exist") {
		return fmt.Errorf("%w: %s has no access groups in Authentik yet: plan its sign-in again first", ErrRefused, app)
	}
	return err
}

// Revoke takes an app away from a person.
func (s *Service) Revoke(ctx context.Context, actor string, id int64, app string) (Person, error) {
	p, err := s.person(id)
	if err != nil {
		return p, err
	}
	if _, ok := grantFor(p.Grants, app); !ok {
		return p, invalid("%s is not granted to %s", app, p.Username)
	}
	api, err := s.client()
	if err != nil {
		return p, err
	}
	users, admins := sso.AppGroups(app)
	for _, g := range []string{admins, users} {
		if err := api.RemoveFromGroup(ctx, g, p.pk); err != nil {
			s.audit(actor, "people.revoke", "error", fmt.Sprintf("person %d: %s", id, app))
			return p, groupError(app, err)
		}
	}
	if err := s.dropGrant(id, app); err != nil {
		return p, err
	}
	s.audit(actor, "people.revoke", "ok", fmt.Sprintf("person %d: %s", id, app))
	return s.person(id)
}

// Adopt brings an existing Authentik user under Homestead: tags them and
// records them and the app groups they are already in. Service and system
// accounts are refused.
func (s *Service) Adopt(ctx context.Context, actor, username string) (Person, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Person{}, invalid("a username is required")
	}
	api, err := s.client()
	if err != nil {
		return Person{}, err
	}
	accounts, err := api.ListAccounts(ctx)
	if err != nil {
		return Person{}, err
	}
	var acct *sso.RemoteAccount
	for i := range accounts {
		if accounts[i].Username == username {
			acct = &accounts[i]
		}
	}
	if acct == nil {
		return Person{}, fmt.Errorf("%w: Authentik has no person called %q (service and system accounts cannot be adopted)", ErrNotFound, username)
	}
	if s.OwnerSub != nil && s.OwnerSub() != "" && acct.UID == s.OwnerSub() {
		return Person{}, invalid("%s is the owner, who already has every app", username)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE authentik_pk = ?`, acct.PK).Scan(&n); err != nil {
		return Person{}, err
	}
	if n > 0 {
		return Person{}, fmt.Errorf("%w: %s is already a person here", ErrConflict, username)
	}
	apps, err := s.apps()
	if err != nil {
		return Person{}, err
	}
	if err := api.TagAccount(ctx, *acct); err != nil {
		s.audit(actor, "people.adopt", "error", "could not tag "+username)
		return Person{}, err
	}
	name := acct.Name
	if strings.TrimSpace(name) == "" {
		name = acct.Username
	}
	state := StateDisabled
	if acct.IsActive {
		state = StateActive
	}
	res, err := s.DB.Exec(`INSERT INTO people (display_name, username, email, authentik_pk, state, origin, created_by, created_at, authentik_uid)
		VALUES (?, ?, ?, ?, ?, 'adopted', ?, ?, ?)`, name, acct.Username, acct.Email, acct.PK, state, actor, s.now().Format(time.RFC3339), acct.UID)
	if err != nil {
		return Person{}, err
	}
	id, _ := res.LastInsertId()
	roles := map[string]string{}
	for _, g := range acct.HomesteadGroups() {
		if app, role, ok := appOfGroup(g, apps); ok && (role == RoleAdmin || roles[app] == "") {
			roles[app] = role
		}
	}
	for app, role := range roles {
		if err := s.setGrant(id, app, role); err != nil {
			return Person{}, err
		}
	}
	s.audit(actor, "people.adopt", "ok", fmt.Sprintf("adopted %s (person %d)", username, id))
	if err := s.syncMember(ctx, api, acct.PK, state == StateActive); err != nil {
		s.audit(actor, "people.adopt", "error", fmt.Sprintf("person %d: dashboard sign-in group not updated: %v", id, err))
	}
	return s.person(id)
}

// Resolution actions for drift.
const (
	ActionPutBack  = "put-back"
	ActionKeep     = "keep"
	ActionRecreate = "recreate"
	ActionForget   = "forget"
)

// Resolve settles a person's drift. Put back restores Homestead's record
// in Authentik (the recommended action); Keep accepts Authentik's values
// into the record. For a person deleted in Authentik, Recreate makes the
// user again (revealing a new credential once) and Forget drops the
// record. Nothing happens unless the owner chooses.
func (s *Service) Resolve(ctx context.Context, actor string, id int64, action, method string) (*Revealed, error) {
	p, err := s.person(id)
	if err != nil {
		return nil, err
	}
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	apps, err := s.apps()
	if err != nil {
		return nil, err
	}
	acct, err := api.GetAccount(ctx, p.pk)
	if err != nil {
		return nil, err
	}
	var rev *Revealed
	switch {
	case action == ActionForget:
		if acct != nil {
			return nil, invalid("%s still exists in Authentik; use remove", p.Username)
		}
		err = s.forget(id)
	case action == ActionRecreate:
		if acct != nil {
			return nil, invalid("%s still exists in Authentik", p.Username)
		}
		rev, err = s.recreate(ctx, api, p, method)
	case acct == nil:
		return nil, invalid("%s is missing in Authentik: recreate them or remove them from Homestead", p.Username)
	case action == ActionPutBack:
		err = s.putBack(ctx, api, p, *acct, apps)
	case action == ActionKeep:
		err = s.keep(p, *acct, apps)
	default:
		return nil, invalid("action must be put-back, keep, recreate or forget")
	}
	if err != nil {
		s.audit(actor, "people.resolve", "error", fmt.Sprintf("person %d: %s", id, action))
		return nil, err
	}
	s.audit(actor, "people.resolve", "ok", fmt.Sprintf("person %d (%s): %s", id, p.Username, action))
	return rev, nil
}

func (s *Service) recreate(ctx context.Context, api *sso.API, p Person, method string) (*Revealed, error) {
	acct, err := api.CreateAccount(ctx, sso.AccountFields{Username: p.Username, Name: p.DisplayName, Email: p.Email})
	if err != nil {
		return nil, err
	}
	// A new Authentik user has a new uid: the old one signs in nobody.
	if _, err := s.DB.Exec(`UPDATE people SET authentik_pk = ?, authentik_uid = ?, origin = 'created' WHERE id = ?`, acct.PK, acct.UID, p.ID); err != nil {
		return nil, err
	}
	s.endSessions(p.ID)
	if p.State == StateDisabled {
		if err := api.PatchAccount(ctx, acct.PK, map[string]any{"is_active": false}); err != nil {
			return nil, err
		}
	}
	if err := s.syncMember(ctx, api, acct.PK, p.State == StateActive); err != nil {
		return nil, err
	}
	for _, g := range p.Grants {
		users, admins := sso.AppGroups(g.App)
		if err := api.AddToGroup(ctx, users, acct.PK); err != nil {
			return nil, groupError(g.App, err)
		}
		if g.Role == RoleAdmin {
			if err := api.AddToGroup(ctx, admins, acct.PK); err != nil {
				return nil, groupError(g.App, err)
			}
		}
	}
	return s.credential(ctx, api, acct.PK, method, "")
}

func (s *Service) putBack(ctx context.Context, api *sso.API, p Person, acct sso.RemoteAccount, apps []string) error {
	fields := map[string]any{"username": p.Username, "name": p.DisplayName, "email": p.Email, "is_active": p.State == StateActive}
	if err := api.PatchAccount(ctx, p.pk, fields); err != nil {
		return err
	}
	if !acct.Managed() {
		if err := api.TagAccount(ctx, acct); err != nil {
			return err
		}
	}
	if p.State != StateActive {
		s.endSessions(p.ID)
	}
	if err := s.syncMember(ctx, api, p.pk, p.State == StateActive); err != nil {
		return err
	}
	have := map[string]string{}
	for _, g := range acct.HomesteadGroups() {
		if app, role, ok := appOfGroup(g, apps); ok && (role == RoleAdmin || have[app] == "") {
			have[app] = role
		}
	}
	want := map[string]string{}
	for _, g := range p.Grants {
		want[g.App] = g.Role
	}
	for _, app := range apps {
		users, admins := sso.AppGroups(app)
		got, role := have[app], want[app]
		if got == role {
			continue
		}
		switch role {
		case "":
			for _, g := range []string{admins, users} {
				if err := api.RemoveFromGroup(ctx, g, p.pk); err != nil {
					return groupError(app, err)
				}
			}
		case RoleUser:
			if err := api.AddToGroup(ctx, users, p.pk); err != nil {
				return groupError(app, err)
			}
			if err := api.RemoveFromGroup(ctx, admins, p.pk); err != nil {
				return groupError(app, err)
			}
		case RoleAdmin:
			for _, g := range []string{users, admins} {
				if err := api.AddToGroup(ctx, g, p.pk); err != nil {
					return groupError(app, err)
				}
			}
		}
	}
	return nil
}

func (s *Service) keep(p Person, acct sso.RemoteAccount, apps []string) error {
	state := StateDisabled
	if acct.IsActive {
		state = StateActive
	}
	if _, err := s.DB.Exec(`UPDATE people SET display_name = ?, username = ?, email = ?, state = ? WHERE id = ?`,
		acct.Name, acct.Username, acct.Email, state, p.ID); err != nil {
		return err
	}
	if state != StateActive {
		s.endSessions(p.ID)
	}
	if acct.UID != "" {
		if _, err := s.DB.Exec(`UPDATE people SET authentik_uid = ? WHERE id = ?`, acct.UID, p.ID); err != nil {
			return err
		}
	}
	have := map[string]string{}
	for _, g := range acct.HomesteadGroups() {
		if app, role, ok := appOfGroup(g, apps); ok && (role == RoleAdmin || have[app] == "") {
			have[app] = role
		}
	}
	for _, app := range apps {
		_, was := grantFor(p.Grants, app)
		switch role := have[app]; {
		case role != "":
			if err := s.setGrant(p.ID, app, role); err != nil {
				return err
			}
		case was:
			if err := s.dropGrant(p.ID, app); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- dashboard sign-in (ADR-050) ---

// syncMember puts a person's Authentik user in sso.PeopleGroup, or takes
// it out. The group exists only once the owner has approved the sign-in
// plan that creates it; until then there is nothing to keep in step, and
// applying that plan puts everyone in (SyncGroup).
func (s *Service) syncMember(ctx context.Context, api *sso.API, pk int, member bool) error {
	var err error
	if member {
		err = api.AddToGroup(ctx, sso.PeopleGroup, pk)
	} else {
		err = api.RemoveFromGroup(ctx, sso.PeopleGroup, pk)
	}
	if err != nil && strings.Contains(err.Error(), "does not exist") {
		return nil
	}
	return err
}

// SyncGroup makes sso.PeopleGroup hold exactly the active people.
func (s *Service) SyncGroup(ctx context.Context, api *sso.API) error {
	people, err := s.records()
	if err != nil {
		return err
	}
	for _, p := range people {
		if err := s.syncMember(ctx, api, p.pk, p.State == StateActive); err != nil {
			return fmt.Errorf("%s: %w", p.Username, err)
		}
	}
	return nil
}

// maxBackfill bounds the Authentik reads one sign-in may cause.
const maxBackfill = 50

// SignIn finds the active person whose Authentik user has the OIDC subject
// sub (ADR-050 §1). The match is on Homestead's own record of the person's
// Authentik user: the uid kept when the person was created, adopted or read
// back. A record without a uid yet (made before S338) is checked by fetching
// its Authentik user by pk and comparing that user's uid to sub; a match is
// remembered. A username or email claim is never consulted. A disabled
// person, or no match, is not found.
func (s *Service) SignIn(ctx context.Context, sub string) (id int64, username, displayName string, found bool, err error) {
	if sub == "" {
		return 0, "", "", false, nil
	}
	p, err := scanPerson(s.DB.QueryRow(`SELECT `+personCols+` FROM people WHERE authentik_uid = ? AND authentik_uid != ''`, sub))
	switch {
	case err == nil:
		if p.State != StateActive {
			return 0, "", "", false, nil
		}
		return p.ID, p.Username, p.DisplayName, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, "", "", false, err
	}
	// Records without a uid: ask Authentik about each, bounded.
	rows, err := s.DB.Query(`SELECT `+personCols+` FROM people WHERE authentik_uid = '' AND state = 'active' ORDER BY id LIMIT ?`, maxBackfill)
	if err != nil {
		return 0, "", "", false, err
	}
	var pending []Person
	for rows.Next() {
		q, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return 0, "", "", false, err
		}
		pending = append(pending, q)
	}
	rows.Close()
	if len(pending) == 0 {
		return 0, "", "", false, nil
	}
	api, aerr := s.API()
	if aerr != nil {
		// No kept token: nobody can be matched this way. Not a match.
		return 0, "", "", false, nil
	}
	for _, q := range pending {
		acct, err := api.GetAccount(ctx, q.pk)
		if err != nil {
			return 0, "", "", false, err
		}
		if acct == nil || acct.UID == "" {
			continue
		}
		if _, err := s.DB.Exec(`UPDATE people SET authentik_uid = ? WHERE id = ? AND authentik_uid = ''`, acct.UID, q.ID); err != nil {
			return 0, "", "", false, err
		}
		if acct.UID == sub && acct.IsActive {
			return q.ID, q.Username, q.DisplayName, true, nil
		}
	}
	return 0, "", "", false, nil
}
