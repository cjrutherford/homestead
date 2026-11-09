# Names, tunnel and public access

Homestead is local first. Everything works on your home network. Public access is something you add on purpose, one reviewed change at a time.

## Local names

- The dashboard answers at `homestead.local` (mDNS, works on most devices) and at `homestead.lan`.
- Each app has `<app>.lan` and `<app>.local`. Apps that are served under the dashboard's name also answer at `/apps/<app>/`, for example `/apps/auth/` for Authentik.
- `.lan` names need DNS. Homestead can run a small resolver for them. Turn it on from the **Names** page or with `homesteadctl names resolver plan --on` and then `apply`. Otherwise add the names to your router or to each device's hosts file.
- You can rename the dashboard's local name (`homesteadctl dashboard address`).

See what each app answers to with `homesteadctl names` or `homesteadctl apps names`.

## The Cloudflare tunnel

With a domain on Cloudflare, Homestead can publish the dashboard and chosen apps through a Cloudflare Tunnel. You open no router ports.

1. In Cloudflare, create one API token with Zone Read, DNS Edit and Account Cloudflare Tunnel Edit. Reading SSL and Certificates is recommended. Homestead never asks for an account ID.
2. In the setup wizard's Domain step, paste the token (it is sealed on the appliance) and enter your domain, for example `example.com`.
3. On the **Tunnel** page, name a new tunnel. The page proposes `homestead.example.com` for the dashboard. Choose **Review plan**.
4. The plan lists the tunnel, every DNS record it creates and any existing record it would replace. Approve it and follow the operation.

```
homesteadctl tunnel plan --name homestead-home
homesteadctl tunnel setup --name homestead-home --digest DIGEST --follow
homesteadctl tunnel status
```

Setting up the tunnel also sets up public dashboard sign-in: re-run the Sign-in plan afterwards so Authentik learns the public address. On the public name only **Sign in with Authentik** is offered. Management stays local apart from this one signed-in path.

Cloudflare's free certificate covers your domain and names one level below it. A deeper name needs Advanced Certificate Manager; the plan warns you, and each public address shows whether a certificate covers it.

## Publishing an app

Publishing is per app and previewed first. On the app's card choose its public address, review the plan and approve it, or use `homesteadctl apps public status|plan|apply PACKAGE`.

### At `/apps/<app>/` on the dashboard's name

Apps that can run under a path are published at `https://homestead.example.com/apps/<app>/`. No new DNS record is needed. These include Nextcloud, Jellyfin, Navidrome, Authentik, paperless-ngx and the Craft apps, among others.

All of these share one web address with the dashboard. A browser treats them as one site, so a flaw in one of them could reach the dashboard's sign-in in that browser. Homestead accepts this so one name and one certificate cover everything, and the Tunnel page says so.

### Apps that keep their own name

These apps cannot run at a path, so a published one gets its own name such as `app.example.com`:

| App | Why |
|---|---|
| Immich | Needs its own origin. |
| Gitea, Actual Budget, Mealie, Calibre-Web Automated | Single-origin sign-in: they cannot sign in from a second address, so the local name redirects to the public one. |
| BookStack, Memos, Vikunja, Audiobookshelf, FreshRSS | Cannot be served under a path. |
| Vaultwarden | Your choice of design: a password manager should not share the dashboard's origin. |

Uptime Kuma is never published.

### Local requests redirect once an app is published

When an app at a path is published, its local address (`https://homestead.lan/apps/<app>/`) answers a redirect (308) to the public address, keeping the path and query, so the app has one address. The Craft apps are the exception: they stay open on the local network. Apps that are not published keep being served locally. The plan tells you before you approve it. Apps and devices on your network that do not follow redirects, for example a reader app using Komga's OPDS feed, must be given the public address, or you can unpublish the app to bring the local address back.

### The shared gate

The Craft apps have no sign-in of their own, and Navidrome relies on Authentik instead of an OpenID Connect login. When published at a path they sit behind one shared Authentik gate. Authentik says who the person is. The appliance's edge then checks that person's groups against the app's groups and answers 403 to anyone signed in who was not granted the app. The LAN stays open, as above.

## Planned, approved, audited

Every public change follows the same pattern: Homestead plans it, you review the exact preview, you approve it, and it runs as an operation. If a plan changes between preview and approval, approval is refused and you review again. Each approval and each result is recorded on the **Audit** page (`homesteadctl audit list`). Open the **Exposure** page to see what is published and to approve or apply exposure changes.

To remove public access for one app, plan its public name with `--off`:

```
homesteadctl apps public plan jellyfin --off
```

To undo the tunnel, use `homesteadctl tunnel teardown TUNNEL-ID`. It deletes only the DNS records Homestead created.
