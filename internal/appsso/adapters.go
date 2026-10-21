package appsso

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/catalog"
)

// ProviderKey is the name the app knows Authentik by (Nextcloud's
// provider identifier, Jellyfin's provider key).
const ProviderKey = "authentik"

// ButtonText labels the app's sign-in button where it has one.
const ButtonText = "Sign in with Authentik"

// Target is the installed app an adapter configures.
type Target struct {
	Manifest catalog.Manifest
	// Container is the service that talks to Authentik.
	Container string
}

// container returns <package>-<service>, the generated container name.
func container(m catalog.Manifest, service string) string { return m.Name + "-" + service }

// Settings are the values an adapter writes. ClientSecret never leaves
// the adapter except on a container's stdin.
type Settings struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

// Adapter configures one app's OpenID Connect sign-in (S298). Each is
// first-party code; the manifest only names it.
type Adapter interface {
	RedirectURIs(origins []string) []string
	// SignInURL is where a person starts signing in, given the app's
	// first origin.
	SignInURL(origin string) string
	OnSettings(issuer, clientID string) []string
	OffSettings() []string
	// TrustEnv makes the service trust the appliance root mounted at CAFile.
	TrustEnv() map[string]string
	Enable(ctx context.Context, t Target, s Settings) error
	Disable(ctx context.Context, t Target) error
}

// AdapterFor returns the adapter a manifest declares.
func AdapterFor(d catalog.OIDCAdapter) (Adapter, error) {
	switch d.Adapter {
	case catalog.AdapterImmich:
		return immich{}, nil
	case catalog.AdapterNextcloud:
		return nextcloud{}, nil
	case catalog.AdapterJellyfinSSO:
		if d.Plugin == nil {
			return nil, fmt.Errorf("appsso: %s needs a pinned plugin", d.Adapter)
		}
		return jellyfin{plugin: *d.Plugin}, nil
	}
	return nil, fmt.Errorf("appsso: unknown adapter %q", d.Adapter)
}

// ---- runtime seams, stubbed in tests ----

// execIn runs a command in a container. Secrets travel on stdin only,
// never argv or environment (ADR-021), and are redacted from errors.
var execIn = func(ctx context.Context, container, user, stdin string, args ...string) (string, error) {
	argv := []string{"exec", "-i"}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	argv = append(append(argv, container), args...)
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("exec in %s: %w: %s", container, err, strings.TrimSpace(redact(out.String(), stdin)))
	}
	return out.String(), nil
}

// restart restarts a container so it reads changed settings.
var restart = func(ctx context.Context, container string) error {
	if out, err := exec.CommandContext(ctx, "docker", "restart", container).CombinedOutput(); err != nil {
		return fmt.Errorf("restart %s: %w: %s", container, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyInto copies a host directory into a container path.
var copyInto = func(ctx context.Context, src, container, dst string) error {
	if out, err := exec.CommandContext(ctx, "docker", "cp", src, container+":"+dst).CombinedOutput(); err != nil {
		return fmt.Errorf("copy into %s: %w: %s", container, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// maxPlugin bounds a plugin download.
const maxPlugin = 64 << 20

// download fetches a pinned release.
var download = func(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPlugin+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if len(data) > maxPlugin {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, maxPlugin)
	}
	return data, nil
}

func redact(text, secret string) string {
	if secret == "" {
		return text
	}
	for _, line := range strings.Split(secret, "\n") {
		if strings.TrimSpace(line) != "" {
			text = strings.ReplaceAll(text, line, "[redacted]")
		}
	}
	return text
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ---- Immich: its built-in OAuth settings ----

// immich writes the oauth block of Immich's stored system config (the
// system_metadata row the admin settings page edits) over the database
// container's local socket, then restarts the server to read it.
// Password login is left on: the local admin keeps working.
type immich struct{}

func (immich) RedirectURIs(origins []string) []string {
	var out []string
	for _, o := range origins {
		out = append(out, o+"/auth/login", o+"/user-settings")
	}
	return append(out, "app.immich:///oauth-callback")
}

func (immich) SignInURL(origin string) string { return origin + "/auth/login" }

func (immich) OnSettings(issuer, clientID string) []string {
	return []string{
		fmt.Sprintf("Immich OAuth: on, issuer %s, client id %s, scopes openid email profile, button %q.", issuer, clientID, ButtonText),
		"Immich creates an account at a person's first sign-in (auto register). Password login stays on.",
		"The mobile app signs in through app.immich:///oauth-callback.",
	}
}

func (immich) OffSettings() []string {
	return []string{"Immich OAuth: off, and its stored client secret is cleared. Password login is unchanged."}
}

func (immich) TrustEnv() map[string]string { return map[string]string{"NODE_EXTRA_CA_CERTS": CAFile} }

func (immich) psql(ctx context.Context, m catalog.Manifest, script string) error {
	db := m.Database
	if db == nil || db.Kind != catalog.DatabasePostgres {
		return fmt.Errorf("appsso: %s declares no postgres database", m.Name)
	}
	_, err := execIn(ctx, container(m, db.Service), "", script, "psql", "-q", "-v", "ON_ERROR_STOP=1",
		"--username", db.Username, "--dbname", db.Database)
	return err
}

func (a immich) Enable(ctx context.Context, t Target, s Settings) error {
	oauth, _ := json.Marshal(map[string]any{
		"enabled": true, "issuerUrl": s.Issuer, "clientId": s.ClientID, "clientSecret": s.ClientSecret,
		"scope": "openid email profile", "buttonText": ButtonText, "autoRegister": true, "autoLaunch": false,
		"tokenEndpointAuthMethod": "client_secret_post",
	})
	lit := sqlQuote(string(oauth)) + "::jsonb"
	script := "INSERT INTO system_metadata (key, value) VALUES ('system-config', jsonb_build_object('oauth', " + lit + "))\n" +
		"ON CONFLICT (key) DO UPDATE SET value = jsonb_set(COALESCE(system_metadata.value, '{}'::jsonb), '{oauth}',\n" +
		"  COALESCE(system_metadata.value->'oauth', '{}'::jsonb) || " + lit + ");\n"
	if err := a.psql(ctx, t.Manifest, script); err != nil {
		return fmt.Errorf("appsso: write Immich OAuth settings: %w", err)
	}
	return restart(ctx, t.Container)
}

func (a immich) Disable(ctx context.Context, t Target) error {
	script := "UPDATE system_metadata SET value = jsonb_set(jsonb_set(value, '{oauth,enabled}', 'false'), '{oauth,clientSecret}', '\"\"')\n" +
		"WHERE key = 'system-config' AND value ? 'oauth';\n"
	if err := a.psql(ctx, t.Manifest, script); err != nil {
		return fmt.Errorf("appsso: turn Immich OAuth off: %w", err)
	}
	return restart(ctx, t.Container)
}

// ---- Nextcloud: the user_oidc app through occ ----

// nextcloud installs and enables user_oidc, imports the appliance root
// into Nextcloud's own trust store, lets it reach the appliance edge
// (allow_local_remote_servers) and registers the provider. The client
// secret reaches occ through stdin. Login with a local password stays
// (allow_multiple_user_backends).
type nextcloud struct{}

func (nextcloud) RedirectURIs(origins []string) []string {
	var out []string
	for _, o := range origins {
		out = append(out, o+"/apps/user_oidc/code", o+"/index.php/apps/user_oidc/code")
	}
	return out
}

func (nextcloud) SignInURL(origin string) string { return origin + "/login" }

func (nextcloud) OnSettings(issuer, clientID string) []string {
	return []string{
		"Nextcloud app user_oidc: installed from the Nextcloud app store and enabled.",
		fmt.Sprintf("user_oidc provider %q: client id %s, discovery %s.well-known/openid-configuration, scopes openid email profile.", ProviderKey, clientID, issuer),
		"user_oidc keeps the password login form (allow_multiple_user_backends 1). Accounts are created at first sign-in.",
		"Nextcloud trusts the appliance root (security:certificates:import) and may reach addresses on the appliance (allow_local_remote_servers true).",
	}
}

func (nextcloud) OffSettings() []string {
	return []string{
		fmt.Sprintf("user_oidc provider %q: deleted; the user_oidc app: disabled. allow_local_remote_servers is left as it is.", ProviderKey),
	}
}

func (nextcloud) TrustEnv() map[string]string { return nil }

func (nextcloud) occ(ctx context.Context, t Target, stdin string, args ...string) (string, error) {
	return execIn(ctx, t.Container, "www-data", stdin, append([]string{"php", "occ", "--no-interaction"}, args...)...)
}

// readyTries and readyPause bound the wait for a recreated container.
var (
	readyTries = 30
	readyPause = 4 * time.Second
)

// ready waits until occ answers: the container was just recreated to
// pick up the sign-in wiring.
func (a nextcloud) ready(ctx context.Context, t Target) error {
	var err error
	for i := 0; i < readyTries; i++ {
		if _, err = a.occ(ctx, t, "", "status"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPause):
		}
	}
	return fmt.Errorf("appsso: Nextcloud did not answer after it restarted: %w", err)
}

func (a nextcloud) Enable(ctx context.Context, t Target, s Settings) error {
	if err := a.ready(ctx, t); err != nil {
		return err
	}
	if _, err := a.occ(ctx, t, "", "app:enable", "user_oidc"); err != nil {
		if _, err := a.occ(ctx, t, "", "app:install", "user_oidc"); err != nil {
			return fmt.Errorf("appsso: install user_oidc: %w", err)
		}
	}
	steps := [][]string{
		{"security:certificates:import", CAFile},
		{"config:app:set", "user_oidc", "allow_multiple_user_backends", "--value=1"},
		{"config:system:set", "allow_local_remote_servers", "--value=true", "--type=boolean"},
	}
	for _, args := range steps {
		if _, err := a.occ(ctx, t, "", args...); err != nil {
			return fmt.Errorf("appsso: occ %s: %w", args[0], err)
		}
	}
	script := `IFS= read -r secret; exec php occ --no-interaction user_oidc:provider "$1" --clientid="$2" --clientsecret="$secret" --discoveryuri="$3" --scope="openid email profile"`
	if _, err := execIn(ctx, t.Container, "www-data", s.ClientSecret+"\n", "sh", "-c", script, "sh",
		ProviderKey, s.ClientID, s.Issuer+".well-known/openid-configuration"); err != nil {
		return fmt.Errorf("appsso: register the user_oidc provider: %w", err)
	}
	return nil
}

func (a nextcloud) Disable(ctx context.Context, t Target) error {
	if out, err := a.occ(ctx, t, "", "user_oidc:provider:delete", ProviderKey, "--force"); err != nil &&
		!strings.Contains(strings.ToLower(out), "not found") && !strings.Contains(strings.ToLower(out), "does not exist") &&
		!strings.Contains(strings.ToLower(out), "not defined") {
		return fmt.Errorf("appsso: delete the user_oidc provider: %w", err)
	}
	if _, err := a.occ(ctx, t, "", "app:disable", "user_oidc"); err != nil {
		return fmt.Errorf("appsso: disable user_oidc: %w", err)
	}
	return nil
}

// ---- Jellyfin: the pinned jellyfin-plugin-sso ----

// Jellyfin plugin paths in the official image (data directory /config).
const (
	jellyfinPlugins    = "/config/plugins"
	jellyfinPluginConf = jellyfinPlugins + "/configurations/SSO-Auth.xml"
)

// jellyfin installs the pinned plugin release (refused unless its SHA-256
// matches the catalog) and writes its configuration with one OpenID
// provider. Jellyfin's own login form is untouched.
type jellyfin struct{ plugin catalog.PinnedPlugin }

func (jellyfin) RedirectURIs(origins []string) []string {
	var out []string
	for _, o := range origins {
		out = append(out, o+"/sso/OID/redirect/"+ProviderKey)
	}
	return out
}

func (jellyfin) SignInURL(origin string) string { return origin + "/sso/OID/start/" + ProviderKey }

func (a jellyfin) OnSettings(issuer, clientID string) []string {
	return []string{
		fmt.Sprintf("Jellyfin plugin %s %s: installed (checksum checked) into %s.", a.plugin.Name, a.plugin.Version, jellyfinPlugins),
		fmt.Sprintf("Plugin provider %q: on, endpoint %s, client id %s, scopes openid email profile, every library visible.", ProviderKey, issuer, clientID),
		"Jellyfin creates an account at a person's first sign-in. The Jellyfin login form and its admin are unchanged; people start at the sign-in link below.",
	}
}

func (a jellyfin) OffSettings() []string {
	return []string{fmt.Sprintf("Plugin provider %q: removed from the plugin's settings (with its secret). The plugin stays installed.", ProviderKey)}
}

// TrustEnv adds the appliance root's directory to .NET's trust list.
func (jellyfin) TrustEnv() map[string]string {
	return map[string]string{"SSL_CERT_DIR": "/etc/ssl/certs:" + CAMount}
}

func (a jellyfin) pluginDir() string {
	return strings.ReplaceAll(a.plugin.Name, " ", "-") + "_" + a.plugin.Version
}

// ErrChecksum refuses a plugin build other than the pinned one.
var ErrChecksum = fmt.Errorf("appsso: the plugin download does not match the catalog's pinned SHA-256; nothing was installed")

// unpack verifies the archive and extracts its plain files into dir.
func (a jellyfin) unpack(data []byte, dir string) error {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != a.plugin.SHA256 {
		return ErrChecksum
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("appsso: plugin archive: %w", err)
	}
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || name != path.Base(name) || strings.HasPrefix(name, ".") ||
			!(strings.HasSuffix(name, ".dll") || name == "meta.json") {
			return fmt.Errorf("appsso: plugin archive holds an unexpected entry %q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxPlugin))
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (a jellyfin) Enable(ctx context.Context, t Target, s Settings) error {
	data, err := download(ctx, a.plugin.URL)
	if err != nil {
		return fmt.Errorf("appsso: %w", err)
	}
	tmp, err := os.MkdirTemp("", "homestead-plugin-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, a.pluginDir())
	if err := os.Mkdir(staged, 0o755); err != nil {
		return err
	}
	if err := a.unpack(data, staged); err != nil {
		return err
	}
	prefix := strings.ReplaceAll(a.plugin.Name, " ", "-") + "_"
	// Other builds of the plugin go first, so only the pinned one loads.
	clean := `for d in "$1"/"$2"*; do [ -e "$d" ] || continue; [ "$d" = "$1/$3" ] || rm -rf "$d"; done; mkdir -p "$1/configurations"`
	if _, err := execIn(ctx, t.Container, "", "", "sh", "-c", clean, "sh", jellyfinPlugins, prefix, a.pluginDir()); err != nil {
		return fmt.Errorf("appsso: prepare the plugin directory: %w", err)
	}
	if err := copyInto(ctx, staged, t.Container, jellyfinPlugins+"/"); err != nil {
		return fmt.Errorf("appsso: install the plugin: %w", err)
	}
	if err := a.writeConfig(ctx, t, &s); err != nil {
		return err
	}
	return restart(ctx, t.Container)
}

func (a jellyfin) Disable(ctx context.Context, t Target) error {
	if err := a.writeConfig(ctx, t, nil); err != nil {
		return err
	}
	return restart(ctx, t.Container)
}

// writeConfig writes the plugin's settings file over stdin: one provider
// when s is set, none when it is nil.
func (a jellyfin) writeConfig(ctx context.Context, t Target, s *Settings) error {
	doc, err := jellyfinConfig(s)
	if err != nil {
		return err
	}
	write := `mkdir -p "$(dirname "$1")" && cat > "$1.tmp" && mv "$1.tmp" "$1"`
	secret := ""
	if s != nil {
		secret = s.ClientSecret
	}
	if out, err := execIn(ctx, t.Container, "", doc, "sh", "-c", write, "sh", jellyfinPluginConf); err != nil {
		return fmt.Errorf("appsso: write the plugin settings: %w: %s", err, redact(out, secret))
	}
	return nil
}

type xmlStrings struct {
	Items []string `xml:"string"`
}

type oidConfig struct {
	XMLName                xml.Name   `xml:"OidConfig"`
	OidEndpoint            string     `xml:"OidEndpoint"`
	OidClientID            string     `xml:"OidClientId"`
	OidSecret              string     `xml:"OidSecret"`
	Enabled                bool       `xml:"Enabled"`
	EnableAuthorization    bool       `xml:"EnableAuthorization"`
	EnableAllFolders       bool       `xml:"EnableAllFolders"`
	EnabledFolders         xmlStrings `xml:"EnabledFolders"`
	AdminRoles             xmlStrings `xml:"AdminRoles"`
	Roles                  xmlStrings `xml:"Roles"`
	EnableFolderRoles      bool       `xml:"EnableFolderRoles"`
	OidScopes              xmlStrings `xml:"OidScopes"`
	SchemeOverride         string     `xml:"SchemeOverride"`
	NewPath                bool       `xml:"NewPath"`
	DefaultUsernameClaim   string     `xml:"DefaultUsernameClaim"`
	DisablePushedAuthorize bool       `xml:"DisablePushedAuthorization"`
}

type oidItem struct {
	Key struct {
		String string `xml:"string"`
	} `xml:"key"`
	Value struct{ Config oidConfig } `xml:"value"`
}

type pluginConfiguration struct {
	XMLName     xml.Name `xml:"PluginConfiguration"`
	XSI         string   `xml:"xmlns:xsi,attr"`
	XSD         string   `xml:"xmlns:xsd,attr"`
	SamlConfigs struct{} `xml:"SamlConfigs"`
	OidConfigs  struct {
		Items []oidItem `xml:"item"`
	} `xml:"OidConfigs"`
}

func jellyfinConfig(s *Settings) (string, error) {
	doc := pluginConfiguration{XSI: "http://www.w3.org/2001/XMLSchema-instance", XSD: "http://www.w3.org/2001/XMLSchema"}
	if s != nil {
		var item oidItem
		item.Key.String = ProviderKey
		item.Value.Config = oidConfig{
			OidEndpoint: s.Issuer, OidClientID: s.ClientID, OidSecret: s.ClientSecret, Enabled: true,
			EnableAllFolders: true, OidScopes: xmlStrings{Items: []string{"openid", "email", "profile"}},
			SchemeOverride: "https", NewPath: true, DefaultUsernameClaim: "preferred_username",
			// Plain authorization requests, as for every other app.
			DisablePushedAuthorize: true,
		}
		doc.OidConfigs.Items = []oidItem{item}
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("appsso: render plugin settings: %w", err)
	}
	return xml.Header + string(out) + "\n", nil
}
