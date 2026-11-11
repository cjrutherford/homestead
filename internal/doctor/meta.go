package doctor

import "strings"

// Groups, in the order the dashboard and the CLI show them (S316).
const (
	GroupSystem       = "System"
	GroupStorage      = "Storage"
	GroupNetwork      = "Network"
	GroupCertificates = "Certificates"
	GroupTunnel       = "Tunnel"
	GroupSignIn       = "Sign-in"
	GroupApps         = "Apps"
)

// Groups lists every group in display order.
var Groups = []string{GroupSystem, GroupStorage, GroupNetwork, GroupCertificates, GroupTunnel, GroupSignIn, GroupApps}

// meta is what a check says about itself in the owner's words. Checks
// with a ":" suffix (one per certificate, per app) share their prefix's
// entry. Doctor only explains: every fix is a page or a command the owner
// uses, never something Doctor does.
type meta struct {
	group, title, why string
	fix               Fix
}

const restart = "sudo systemctl restart homestead-server"

var metas = map[string]meta{
	"discovery":              {GroupSystem, "Host details", "Homestead could not read this machine's details.", Fix{Text: "Restart the service, then check its log for the error above.", Command: restart}},
	"hardware":               {GroupSystem, "Hardware", "Homestead could not read the hardware.", Fix{Text: "Restart the service, then check its log for the error above.", Command: restart}},
	"network":                {GroupNetwork, "Network", "Homestead could not read the network settings.", Fix{Text: "Restart the service, then check its log for the error above.", Command: restart}},
	"storage":                {GroupStorage, "Storage", "Homestead could not read the disks.", Fix{Text: "Restart the service, then check its log for the error above.", Command: restart}},
	"os-distro":              {GroupSystem, "Operating system", "Homestead is built and tested for Debian and Ubuntu Server.", Fix{Text: "Move to Debian or Ubuntu Server to get support."}},
	"memory":                 {GroupSystem, "Memory", "Apps need memory; below 1 GiB the appliance cannot run reliably.", Fix{Text: "Add memory, or stop apps you do not use.", Link: "/admin/catalog"}},
	"cpu":                    {GroupSystem, "Processor", "Fewer than two cores makes apps feel slow.", Fix{Text: "Use a machine with at least two cores."}},
	"state-dirs":             {GroupSystem, "Homestead folders", "Homestead keeps its state in a few folders that must exist.", Fix{Text: "Re-run the installer to recreate them.", Command: "sudo make install"}},
	"docker-address-pools":   {GroupSystem, "Docker network space", "Every app gets its own Docker network; the built-in pool holds about 31, fewer than the catalog.", Fix{Text: "Re-run the installer to add a roomier address pool; it restarts Docker only when it changes the file.", Command: "sudo make install"}},
	"drift-scan":             {GroupSystem, "Configuration drift scan", "Generated files that changed outside Homestead can undo your settings.", Fix{Text: "Review what changed on the Drift page.", Link: "/admin/drift", Command: "homesteadctl drift schedule"}},
	"platform-apply":         {GroupSystem, "Platform update", "A platform update that failed or stalled leaves the appliance half updated.", Fix{Text: "Read the update's work order on the Updates page.", Link: "/admin/updates"}},
	"release-trust":          {GroupSystem, "Update signing keys", "Updates are only installed when signed by a key you trust.", Fix{Text: "Check the trusted release keys on the Updates page.", Link: "/admin/updates"}},
	"backup-encryption":      {GroupStorage, "Backup encryption", "Encrypted backups need a recovery identity you have confirmed storing safely.", Fix{Text: "Confirm the recovery identity on the Backups page.", Link: "/admin/backups", Command: "homesteadctl backup identity ack"}},
	"backup-rewrap":          {GroupStorage, "Backup re-wrap", "Existing backups should open with the current recovery key, and no retired key should be unreadable.", Fix{Text: "Re-wrap existing backups from the Backups page.", Link: "/admin/backups", Command: "homesteadctl backup identity rewrap"}},
	"root-disk":              {GroupStorage, "System disk space", "A full system disk stops apps and updates.", Fix{Text: "Free space: remove old images and backups you no longer need.", Command: "docker system df"}},
	"stale-mounts":           {GroupStorage, "Storage mounts", "A drive that stopped answering takes its apps' data with it.", Fix{Text: "Reconnect the drive, or remove the mount on the Storage page.", Link: "/admin/storage"}},
	"app-data":               {GroupStorage, "App data placement", "App data should live on mounted storage so backups and moves work.", Fix{Text: "Plan the data move on the Data page.", Link: "/admin/data", Command: "homesteadctl data status"}},
	"default-gateway":        {GroupNetwork, "Internet route", "Without a route to the internet there are no updates or remote access.", Fix{Text: "Connect the appliance to your router."}},
	"dns-resolvers":          {GroupNetwork, "Name lookup", "Updates and the catalog need a name server.", Fix{Text: "Set a name server on the appliance or in your router."}},
	"local-resolver":         {GroupNetwork, "The .lan resolver", "Your devices find .lan names through the appliance resolver.", Fix{Text: "Free port 53 or turn the resolver off on the Names page.", Link: "/admin/names"}},
	"mdns-name-conflict":     {GroupNetwork, "Local name conflict", "Another device on your network uses the appliance's .local name.", Fix{Text: "Rename one of the two devices.", Link: "/admin/names"}},
	"dashboard-mdns-name":    {GroupNetwork, "The .local name", "Your devices find the dashboard at the .local name.", Fix{Text: "Restart the service so it answers again.", Command: restart}},
	"edge-dashboard-socket":  {GroupNetwork, "Dashboard through the edge", "The edge reaches the dashboard through a shared socket folder.", Fix{Text: "Re-apply the edge so it mounts the folder.", Link: "/admin/edge"}},
	"edge-craft-socket":      {GroupNetwork, "Craft files through the edge", "The edge reaches craft file sharing through a shared socket folder.", Fix{Text: "Re-apply the edge so it mounts the folder.", Link: "/admin/edge"}},
	"edge-upstream-networks": {GroupNetwork, "Edge reaches its apps", "The edge proxy must share a network with each app it serves.", Fix{Text: "Re-apply the edge on the Edge page.", Link: "/admin/edge"}},
	"local-ca":               {GroupCertificates, "Local certificate authority", "Your devices trust the appliance's own CA for local HTTPS.", Fix{Text: "Review the CA on the Trust page.", Link: "/admin/trust"}},
	"local-ca-old-key":       {GroupCertificates, "Old CA key", "A retired CA key should be destroyed after a rotation.", Fix{Text: "Review the CA on the Trust page.", Link: "/admin/trust"}},
	"local-leaf":             {GroupCertificates, "Local certificate", "A local name needs a valid certificate from the local CA.", Fix{Text: "Re-issue it on the Trust page.", Link: "/admin/trust"}},
	"tls-renewal":            {GroupCertificates, "Certificate renewal", "Certificates must renew before they expire.", Fix{Text: "Review renewals on the TLS page.", Link: "/admin/tls", Command: "homesteadctl tls renewal"}},
	"acme-overrides":         {GroupCertificates, "Certificate validation overrides", "ACME overrides should only be set while testing.", Fix{Text: "Unset the override when you are done testing."}},
	"edge-certificate":       {GroupCertificates, "Public name certificate", "Cloudflare's edge needs a certificate that covers the public name, or browsers refuse it.", Fix{Text: "Add a certificate in Cloudflare, or publish a name one level below your domain.", Link: "/admin/tunnel"}},
	"tunnel-connector":       {GroupTunnel, "Tunnel connector", "The connector carries public traffic from Cloudflare to the appliance.", Fix{Text: "Check the connector on the Tunnel page.", Link: "/admin/tunnel", Command: "sudo systemctl status homestead-tunnel"}},
	"tunnel-exists":          {GroupTunnel, "Tunnel in Cloudflare", "The tunnel must still exist in your Cloudflare account.", Fix{Text: "Plan the tunnel again on the Tunnel page.", Link: "/admin/tunnel", Command: "homesteadctl tunnel plan"}},
	"tunnel-dns":             {GroupTunnel, "Tunnel DNS record", "Each public name needs a DNS record pointing at the tunnel.", Fix{Text: "Plan the tunnel again to repair the record.", Link: "/admin/tunnel", Command: "homesteadctl tunnel plan"}},
	"sign-in":                {GroupSignIn, "Dashboard sign-in", "Dashboard sign-in with Authentik must be set up and applied.", Fix{Text: "Finish the plan on the Sign-in page.", Link: "/admin/sign-in", Command: "homesteadctl sso status"}},
	"authentik":              {GroupSignIn, "Authentik answers", "Sign-in cannot work while Authentik does not answer.", Fix{Text: "Check Authentik's containers on the Docker page.", Link: "/admin/catalog", Command: "homesteadctl docker health"}},
	"app-access":             {GroupSignIn, "App access", "A sign-in made before groups lets every Authentik user into the app.", Fix{Text: "Plan the sign-in again and approve it to close the app to its group.", Command: "homesteadctl apps sign-in plan <app>"}},
	"app-containers":         {GroupApps, "App containers", "An app works only while all its containers run and are healthy.", Fix{Text: "Open the app's containers and start or restart them.", Link: "/admin/catalog", Command: "homesteadctl docker health"}},
	"app-reach":              {GroupApps, "App address", "Each address of an app should answer, locally and publicly.", Fix{Text: "Follow the reason, then check again.", Link: "/admin/catalog"}},
	"app-setup":              {GroupApps, "App first run", "Some apps need their own first-run setup before sign-in or use.", Fix{Text: "Open the app in a browser and finish its setup.", Link: "/admin/catalog"}},
}

var defaultMeta = meta{GroupSystem, "", "Homestead reports this check.", Fix{Text: "Read the detail, then run the live checks.", Command: "homesteadctl doctor --live"}}

// Annotate fills group, title, why and fix on checks that lack them. A
// check that carries its own keeps it.
func Annotate(checks []Check) {
	for i := range checks {
		c := &checks[i]
		key := c.Name
		suffix := ""
		if at := strings.IndexByte(key, ':'); at >= 0 {
			key, suffix = key[:at], key[at+1:]
		}
		m, ok := metas[key]
		if !ok {
			m = defaultMeta
			m.title = c.Name
		}
		if c.Group == "" {
			c.Group = m.group
		}
		if c.Title == "" {
			c.Title = m.title
			if suffix != "" {
				c.Title += ": " + suffix
			}
		}
		if c.Why == "" {
			c.Why = m.why
		}
		if c.Fix == nil {
			f := m.fix
			f.Command = strings.ReplaceAll(f.Command, "<app>", suffix)
			if suffix == "" {
				f.Command = strings.ReplaceAll(f.Command, " <app>", "")
			}
			c.Fix = &f
		}
	}
}

// ByGroup splits checks by group in display order; unknown groups follow.
func ByGroup(checks []Check) []struct {
	Group  string
	Checks []Check
} {
	var out []struct {
		Group  string
		Checks []Check
	}
	index := map[string]int{}
	add := func(g string) int {
		if i, ok := index[g]; ok {
			return i
		}
		out = append(out, struct {
			Group  string
			Checks []Check
		}{Group: g})
		index[g] = len(out) - 1
		return index[g]
	}
	for _, g := range Groups {
		add(g)
	}
	for _, c := range checks {
		i := add(c.Group)
		out[i].Checks = append(out[i].Checks, c)
	}
	kept := out[:0]
	for _, g := range out {
		if len(g.Checks) > 0 {
			kept = append(kept, g)
		}
	}
	return kept
}
