# Bootstrap to Public TLS — Trust and Exposure Ladder

A fresh appliance cannot safely begin as a publicly reachable HTTPS service. Homestead intentionally moves through distinct trust stages.

## Stage 0 — Fresh host

Starting state:
- Debian or Ubuntu Server installed.
- Network connectivity exists.
- No Docker assumption.
- No public DNS assumption.
- No Homestead administrator exists.

Bootstrap installs the native binaries and systemd unit, creates root-owned state directories, initializes SQLite/YAML, and starts `homestead-server`.

The daemon begins in `UNCLAIMED` state.

## Stage 1 — Local bootstrap endpoint

Homestead binds a management endpoint only to an explicitly safe local surface.

Preferred discovery/access options, in order:
1. LAN IP shown on the console.
2. mDNS/local discovery name if enabled.
3. `homesteadctl` over local Unix socket.

At this point there is no assumption of globally valid TLS.

The local bootstrap endpoint exposes only:
- health;
- appliance identity;
- claim flow.

It does not expose the normal administration API.

## Stage 2 — Appliance claim

The console displays a short-lived cryptographic claim token.

The owner opens the local setup endpoint and supplies the token.

Successful claim:
1. validates token and origin constraints;
2. creates the first administrator;
3. establishes durable authentication;
4. invalidates the claim token;
5. transitions appliance to `CLAIMED`.

No default administrator password exists.

Local root recovery remains possible with:

```bash
sudo homesteadctl admin recover
```

## Stage 3 — Locally secured management

Before Internet exposure, Homestead establishes the local security baseline:
- identify LAN interface/subnet;
- configure Homestead-owned nftables rules or record external-firewall mode;
- restrict management to LAN/local socket;
- configure a local friendly name;
- optionally establish an internal/local CA certificate for browser-trusted LAN HTTPS.

This is the first fully usable Homestead state.

The machine can remain here permanently.

## Stage 4 — Customer domain onboarding

The owner chooses a real domain or subdomain.

Homestead then configures one of:
- supported DNS provider API;
- manual DNS instructions;
- existing DNS infrastructure.

Initial provider targets:
- Cloudflare;
- Amazon Route 53;
- DigitalOcean;
- manual provider.

Credentials are stored in the secret store and referenced from YAML by secret ID.

Homestead proves that it can read/write or otherwise validate the intended DNS zone before proceeding.

## Stage 5 — Split DNS established

Homestead creates DNS views so the same friendly service name can resolve appropriately by context.

Example:

`media.example.com`

LAN view:
- resolves directly to the appliance LAN address.

Overlay view:
- resolves to the overlay address or overlay-routable endpoint.

Public view:
- absent until explicit public exposure is enabled; or
- points to the configured public edge/reverse proxy when enabled.

This stage is the key intermediary between local-only setup and public availability.

The owner now has real names and can use valid DNS-based certificate issuance without yet exposing applications publicly.

## Stage 6 — Private remote access

Optionally configure a private overlay before public exposure.

Providers:
- Headscale;
- Tailscale.

This gives the owner secure remote administration and private application access without opening the appliance directly to the Internet.

Recommended management policy at this stage:
- local: allowed;
- overlay: allowed;
- public: denied.

For many customers, this is the recommended final state.

## Stage 7 — Public TLS provisioning

When a route is selected for public exposure, Homestead determines the certificate strategy.

Preferred decision path:

1. If direct HTTP/TLS ACME challenge is safely reachable, allow normal Caddy certificate automation.
2. Otherwise, if a supported public DNS provider is connected, use DNS-01 orchestration.
3. Otherwise, guide the owner through a manual DNS challenge/certificate path.

DNS-01 is particularly useful because Homestead can obtain a publicly trusted certificate before exposing the service itself.

This produces a TLS-capable route, but the firewall/router may still block public traffic.

## Stage 8 — Exposure plan

Before making a service public, Homestead renders an explicit plan:

- DNS records to create/change;
- certificate to request/use;
- local firewall rules to add;
- reverse-proxy route to create;
- router/NAT changes required;
- application being exposed;
- management-risk warning where relevant.

Nothing public is opened merely because an app was installed.

The owner approves the plan.

## Stage 9 — Host firewall opening

Homestead updates only its own nftables table/rules.

It validates the ruleset before atomically applying it.

Lockout protection verifies that the current management connection remains available.

Management interfaces stay non-public by default.

The user may override this recommendation after an explicit warning and review screen.

## Stage 10 — Gateway/NAT/public edge

Homestead now addresses the network perimeter.

Modes:
- manual forwarding instructions;
- assisted gateway detection and verification;
- future opt-in automated PCP/NAT-PMP/UPnP/provider integrations;
- external public reverse proxy / gateway;
- overlay/tunnel based ingress.

The demo profile uses an external/public edge path through the existing SWAG + Tailscale arrangement.

This is deliberately an adapter/profile, not a hard-coded architecture requirement.

## Stage 11 — Public validation

Homestead validates from the best available external vantage point:
- public DNS resolves correctly;
- TLS certificate is valid for the hostname;
- TCP/443 reaches the intended edge;
- the reverse proxy reaches the application;
- application health endpoint succeeds;
- no unintended management route has become public.

Only after this validation does the dashboard show the route as `PUBLIC / HEALTHY`.

## Stage 12 — Steady state

A service can now have independent exposure policy:

```yaml
routes:
  media:
    hostname: media.example.com
    exposure:
      - local
      - overlay
      - public

  management:
    hostname: homestead.example.com
    exposure:
      - local
      - overlay
```

## State progression

```text
UNCLAIMED
   ↓
CLAIMED_LOCAL
   ↓
LOCAL_SECURED
   ↓
DOMAIN_CONNECTED
   ↓
SPLIT_DNS_READY
   ↓
OVERLAY_READY          (optional)
   ↓
TLS_READY
   ↓
PUBLIC_PLAN_APPROVED
   ↓
PUBLIC_EDGE_READY
   ↓
PUBLIC_VALIDATED
```

Each transition must be independently resumable and reversible where practical.

## Product principle

Public accessibility is not a bootstrap prerequisite. It is the last step of a controlled exposure process.
