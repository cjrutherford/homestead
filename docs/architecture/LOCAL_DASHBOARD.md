# Reaching the dashboard on your network (S289, ADR-035)

The dashboard and API normally listen on `127.0.0.1:8080` only. From another
device on your network, open it through the appliance's own edge instead. This
needs no Cloudflare tunnel, so you can claim the appliance and finish setup
(including Authentik sign-in) entirely on the LAN.

## The two addresses

The edge answers the dashboard on two names, both on the same certificate
from the appliance's internal CA (ADR-030):

1. `https://homestead.local` is answered by the appliance itself over mDNS, using only the appliance addresses on the asking device's subnet
   (all addresses if none match). It
   needs no router or hosts change on most desktops and phones. Use this one
   first. If a device cannot resolve it (some Android versions, some corporate
   networks), use the second.
2. `https://homestead.lan` (the default owner-set name) needs a DNS entry
   or a hosts entry pointing at the appliance's IP address. Change it with
   `homesteadctl dashboard address set NAME` or on the **Trust** page. The name
   must end in `.lan`, `.local`, `.home.arpa`, `.internal` or `.localhost`,
   because the root certificate can only vouch for those suffixes. It is local
   or overlay only: a public exposure is refused, and the Cloudflare tunnel stays
   the public path.

The daemon records the default route and deploys the edge when it starts, before
the appliance is claimed. The installer prints both addresses.

## Point a device at the `.lan` name

- Router DNS: add an A record for the name to the appliance's address (most
  routers call it local DNS or a static host entry).
- Or a hosts entry on the device: `192.168.1.20  homestead.lan` in
  `/etc/hosts` (Linux, macOS) or
  `C:\Windows\System32\drivers\etc\hosts` (Windows).

## App names (S295, ADR-039)

Each app installed under a `.lan` name, such as `immich.lan`, also answers at
`immich.local`. The appliance announces those names over mDNS next to
`homestead.local`, and the app's certificate covers both. Nothing needs
setting up. `homesteadctl names` and the **Names** page list every app's
names; the catalog's Manage panel shows one app's.

The `.lan` names still need DNS. Rather than one router entry per app, turn on
the appliance's resolver: `homesteadctl names resolver plan --on`, then
`apply` with the digest, or "Review turning it on" on the Names page. It
answers only this appliance's `.lan` names on its LAN addresses (port 53, UDP
and TCP). Other `.lan` names get "no such name", every other lookup is refused,
and nothing is forwarded. Apply the firewall plan afterwards to add its port 53
rules. Then, on the router, add a conditional forward (some routers say domain
forward or DNS override) for the domain `lan` to the appliance's address. To
try it first, point one device's DNS at the appliance. If another DNS server
on the appliance already holds port 53, the plan says so and the doctor warns.

## Trust the root certificate

The first visit shows an untrusted-certificate warning until the device trusts
the appliance root. Download it from `https://homestead.local/api/v1/tls/local-ca/root.pem`
(or the Trust page), compare its SHA-256 fingerprint with the one on the signed-in
Trust page or `homesteadctl tls root show`, then follow
`docs/architecture/INTERNAL_CA_TRUST.md` for your system. Homestead never installs
the root for you.

## Claim and sign in over the LAN

Open `https://homestead.local`, redeem the claim token shown by
`sudo homesteadctl claim token` on the appliance, create the owner, and sign in.
Requests through the edge get the same claim-window, session and CSRF rules as
on loopback. The edge sets the client address; the daemon only believes it on
the dashboard socket, so rate limits and audit entries record the real device.
The recovery and craft routes are never served this way.

## Claim first, then the tunnel

Claim happens on the local network or on the appliance itself; the tunnel
comes after. Tunnel setup needs a signed-in owner, so an unclaimed appliance
cannot create one, and nothing unclaimed is put on the internet by Homestead
(S301). Order of work: claim, sign in, then the setup wizard's Domain and
Tunnel steps.

If no browser can reach `homestead.local` or `homestead.lan` (a remote or
headless box), forward the daemon's loopback port over SSH and claim there:

```
ssh -L 8080:127.0.0.1:8080 you@appliance
# then open http://localhost:8080 on your machine and redeem
# the token from `sudo homesteadctl claim token` on the appliance
```

The forward reaches the daemon's own loopback listener, so it needs no edge,
certificate or DNS, and it exposes nothing.

Last resort, when not even SSH reaches the box: lay out a tunnel by hand.
Create a tunnel in Cloudflare, put its run token in
`/etc/homestead/tunnel/token.env` as `TUNNEL_TOKEN=...` (mode 0600, root),
write `/etc/homestead/tunnel/config.yml` with the tunnel ID and one ingress
rule sending your public name to `http://127.0.0.1:8080`, add the proxied
CNAME to `<tunnel-id>.cfargotunnel.com`, and
`systemctl enable --now homestead-tunnel`. The unclaimed Claim page is then
public, guarded only by the claim token, so claim at once. Afterwards bring
the tunnel under Homestead with `homesteadctl tunnel setup --adopt <tunnel-id>`
(S290, ADR-041 §2): adoption rewrites the connector config through the edge
and replaces the hand-made rule.

## The CLI from another machine

```
homesteadctl --server https://homestead.local --ca-file homestead-local-ca.pem login
HOMESTEAD_CA_FILE=homestead-local-ca.pem homesteadctl --server https://homestead.local status
```

The certificate is always verified against the system roots plus the file you
give; there is no option to skip verification. The token travels as a bearer
header, so no cookie or Origin is involved.

## When it does not work

`homesteadctl doctor` reports:

- `edge-dashboard-socket`: no dashboard route, the socket is missing, or the
  running edge container does not mount `/run/homestead-dashboard`. An edge
  deployed before S289 needs a redeploy: `homesteadctl edge deploy --follow`
  (setting the dashboard name does this too).
- `dashboard-mdns-name`: the mDNS responder could not bind UDP 5353, usually
  because Avahi or systemd-resolved holds it. Stop that, or use the `.lan`
  name. The `.lan` name keeps working.
- `local-leaf:homestead.lan`: the certificate status, as for any local route.

SSO plans made before this slice do not list the new origins; re-run the
Sign-in plan so the Authentik redirect covers them.
