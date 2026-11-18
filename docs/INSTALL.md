# Installing Homestead: from bare metal to public apps

This guide takes one machine from an empty disk to a Homestead appliance
that serves its dashboard, Authentik sign-in and apps on your own domain
through a Cloudflare tunnel. Follow the parts in order. Each one ends with a
check, so you know it worked before you move on.

| Part | You end with | Where |
| --- | --- | --- |
| [A. Prepare the machine](#part-a--prepare-the-machine) | Debian, disks mounted | the machine's console or SSH |
| [B. Install Homestead](#part-b--install-homestead) | the daemon running, unclaimed | the machine |
| [C. Claim](#part-c--claim-on-your-network) | an owner account | a browser on the same network |
| [D. Setup wizard](#part-d--the-setup-wizard) | storage, sign-in, tunnel, apps, backups | the dashboard |
| [E. Final checks](#part-e--final-checks) | proof that it all works | a phone off your Wi-Fi |
| [F. Preparing an appliance for a customer](#part-f--preparing-an-appliance-for-a-customer) | a reset unit, ready to ship | the machine |

Setup was tested end to end on Debian 13 (trixie) with Docker 26 and
cloudflared 2026.10 (VM 2, 2026-10-25). Vendor screens (Cloudflare,
Authentik) change; where a menu path is given, it was correct on that date.

## What you need

- **A machine:** x86_64 or arm64, at least 4 GB RAM (8 GB if you run
  Immich or Nextcloud), a system disk, and one or more data disks.
  Homestead manages one machine only.
- **A network** where the machine has a fixed address. Use a DHCP
  reservation on the router.
- **For public access:** a domain whose DNS is on Cloudflare (free plan is
  fine) and a Cloudflare account. Without this, Homestead works on your home
  network only.
- **A build machine** (optional): any computer with Node.js 24, to build the
  dashboard if you'd rather not put Node on the appliance.

---

## Part A — Prepare the machine

### A1. Install Debian

Install Debian 13 (or Ubuntu Server 24.04 or later) from the official image:

- Choose **SSH server** and **standard system utilities**. Choose no desktop.
- Create an admin user with `sudo`.
- Use the whole system disk for the OS. Leave the data disks alone for
  now.

Check: `cat /etc/os-release` names Debian or Ubuntu, and
`systemctl --version` works.

### A2. Mount the data disks

Homestead never formats, partitions or mounts disks (ADR-013). You prepare
them once, and Homestead uses what is mounted. A typical layout:

| Mount | Used for (storage class) |
| --- | --- |
| `/mnt/app` | app data and databases (`application`, `database`) |
| `/mnt/bulk` | large media libraries (`bulk`) |
| `/mnt/backup` | nightly backups (`backup`) |

One disk can carry several classes if you have only one data disk. For
each disk (this **erases** it, so check the device name with `lsblk`
first):

```bash
sudo mkfs.ext4 -L app /dev/sdb          # repeat with -L bulk, -L backup
sudo mkdir -p /mnt/app /mnt/bulk /mnt/backup
echo 'LABEL=app    /mnt/app    ext4 defaults,nofail 0 2' | sudo tee -a /etc/fstab
echo 'LABEL=bulk   /mnt/bulk   ext4 defaults,nofail 0 2' | sudo tee -a /etc/fstab
echo 'LABEL=backup /mnt/backup ext4 defaults,nofail 0 2' | sudo tee -a /etc/fstab
sudo systemctl daemon-reload && sudo mount -a
```

Check: `findmnt /mnt/app` (and the others) shows each disk mounted.

### A3. Docker and cloudflared: nothing to do

`bootstrap.sh` (B3) installs both from their vendors' apt repositories:

- **Docker Engine** with the Compose plugin, from `download.docker.com`;
- **`cloudflared`**, the Cloudflare tunnel connector, from
  `pkg.cloudflare.com`.

Each repository key is checked against a pinned fingerprint before apt
trusts it (ADR-042). A Docker you installed yourself is kept if it has
Compose v2; Debian's `docker.io` with `docker-compose` counts. An existing
`cloudflared` is kept too.

Never run `cloudflared service install` or `cloudflared tunnel login`.
Homestead writes the connector's configuration itself, and runs it as
`homestead-tunnel` only after you approve a tunnel plan.

---

## Part B — Install Homestead

### B1. Put the source on the machine

Use the release you mean to ship. Either clone it:

```bash
sudo apt-get install -y git
git clone https://github.com/cjrutherford/homestead.git ~/homestead   # private: needs a deploy key or token
cd ~/homestead && git checkout <release-tag-or-commit>
```

or copy a checkout from the build machine:

```bash
# on the build machine, inside the checkout
git ls-files -z | tar --null -czf - -T - | ssh admin@appliance 'mkdir -p homestead && tar -xzf - -C homestead'
```

### B2. Build the dashboard

The daemon serves the dashboard only if it was built and embedded first.
Without this step, the install gives you a working API and CLI but **no web
dashboard**. On a machine with Node.js 24 (the build machine, or the
appliance itself):

```bash
corepack enable                        # provides the pinned pnpm
make dashboard-install dashboard-build dashboard-embed
```

This leaves the built files in `internal/dashboard/dist`. If you built on
another machine, copy that folder into the appliance's checkout:

```bash
tar -cf - internal/dashboard/dist | ssh admin@appliance 'tar -xf - -C homestead'
```

Check: `ls internal/dashboard/dist/index.html` exists in the appliance's
checkout.

### B3. Run the installer

```bash
cd ~/homestead
sudo ./scripts/bootstrap.sh
```

`bootstrap.sh` checks that this is Debian or Ubuntu with systemd. It then
installs:

- `ca-certificates`, `curl`, `nftables`, `age` and `gpg`;
- the Go toolchain that `go.mod` names (official build, checksum-verified);
- Docker Engine and Compose, enabled and started, plus `cloudflared` (A3).

Then it runs `scripts/install-systemd.sh`. Options:

- `--no-cloudflared` for an appliance that will stay on the home network
  (re-run without it later to add it);
- `--no-docker` if you manage Docker yourself.

`install-systemd.sh`:

- creates the folders `/etc/homestead`, `/var/lib/homestead`,
  `/var/log/homestead`, `/run/homestead` and `/opt/homestead/stacks`;
- builds `homestead-server` (with the dashboard) and `homesteadctl` into
  `/usr/local/bin`;
- installs the catalog and the `homestead-server`, `homestead-apply` and
  `homestead-tunnel` services;
- starts `homestead-server`.

On first start, the daemon brings up the edge proxy (the `homestead-caddy`
container) and answers on `homestead.local`. It is safe to re-run the
installer. To update to a newer release later, check it out, rebuild the
dashboard, and run `sudo make install`.

### B4. Install a prebuilt Debian package

This is an alternative to the source build and installer in B1–B3. Prepare
Docker/Compose and, if needed, cloudflared using A3 first; the package does not
install those host services. On Debian 13, install the `.deb` for the machine's
architecture:

```bash
sudo apt install ./homestead_<version>-<revision>_amd64.deb
```

Use the `_arm64.deb` asset on a 64-bit ARM machine. The package contains the
daemon, CLI, embedded dashboard, systemd units, curated catalog, and required
catalog build assets. It starts Homestead unclaimed. It does not install
Docker, Compose, or cloudflared; use the source bootstrap beforehand or
provide compatible host services yourself. Docker with Compose v2 is needed
for catalog apps. cloudflared is needed only for public access.

The amd64 binaries have run on the Debian 13 review appliance, but this `.deb`
package lifecycle has not been installed there. The arm64 package is
cross-built and its ELF binaries and metadata are inspected, but it has not
been exercised on arm64 hardware. Ubuntu compatibility is not verified for
these package artifacts. See `docs/RELEASING.md` for package build and
checksum instructions.

`apt remove homestead` stops and disables Homestead services and removes
package-owned files. It leaves configuration, SQLite state, Compose stacks,
containers, volumes, mounted data, and system groups in place. `apt purge`
also leaves that appliance data in place; package purge is not factory reset.

Check:

```bash
systemctl is-active homestead-server docker   # active, active
cloudflared --version                         # a version
curl -s localhost:8080/healthz             # {"status":"ok",...}
sudo docker ps --format '{{.Names}}'       # homestead-caddy
```

The installer ends by printing the two addresses to claim from.

---

## Part C — Claim (on your network)

Claim always happens on the local network or on the machine itself. Nothing
unclaimed is put on the internet.

1. On the appliance, print the one-time claim token:

   ```bash
   sudo homesteadctl claim token
   ```

   The token is valid for 30 minutes; after that, run the command again
   for a new one.
2. On a computer or phone on the same network, open
   **https://homestead.local**. The browser warns that the certificate is
   not trusted. This is expected: the appliance has its own certificate
   authority. Continue past the warning for now (C3 fixes it for good).
3. The Status page reads **Unclaimed**. Choose **Claim this appliance**,
   paste the token, and create the owner account. Use a password from your
   password manager. You are signed in, and the setup wizard opens.

If `homestead.local` does not resolve:

- **Some Android devices and corporate networks** do not do mDNS. Use
  `https://homestead.lan` after adding a router DNS entry or a hosts entry
  for it (see `docs/architecture/LOCAL_DASHBOARD.md`).
- **Headless or remote machine:** forward the port over SSH and claim at
  `http://localhost:8080`:

  ```bash
  ssh -L 8080:127.0.0.1:8080 admin@appliance
  ```

### C3. Trust the appliance's root certificate (each device, once)

Open the **Trust** page. Download the root certificate and check that its
SHA-256 fingerprint matches the one on the page (or `homesteadctl tls root
show`). Then install it using the steps for your system in
`docs/architecture/INTERNAL_CA_TRUST.md`. Homestead never installs it for
you. After this, `homestead.local`, `authentik.local` and every app's local
name open without warnings.

---

## Part D — The setup wizard

After claim, the dashboard sends you through six steps, one at a time:
Account, Storage, Sign-in, Domain and access, Apps, Backups. Next is enabled
when a step is done or skipped. Each step lists what it does and what you
must do in other tools ("Outside Homestead"). You can leave and come back,
because the wizard opens on the first step still pending. Every change that
touches the system is shown as a plan for you to review and approve first.

The order below is the one that ends with everything public and signed in.
**Do Sign-in before Domain**: the tunnel publishes the dashboard only once
Authentik sign-in is linked.

### D1. Account

1. On the **Backups** page, under the recovery identity, **export** the file
   and store it offline, for example on a USB stick in a drawer. Restoring
   backups after losing the appliance needs it. Acknowledge the export.
2. Back in the wizard, choose **Confirm my password**.

### D2. Storage

1. On the **Storage** page, register each mounted disk from A2 with a short
   label (`app`, `bulk`, `backup`).
2. Assign classes:
   - `application` and `database` to `/mnt/app`;
   - `bulk` to `/mnt/bulk`;
   - `backup` to `/mnt/backup`.

The step is done once `application` is assigned. CLI equivalent:

```bash
homesteadctl storage register --mountpoint /mnt/app --label app
homesteadctl storage assign --class application --mountpoint /mnt/app
```

### D3. Sign-in (Authentik)

Authentik becomes the one sign-in for the dashboard and for apps.

1. From the step, **install Authentik**. The install wizard checks its
   requirements (Docker, the edge, storage), shows the plan, and installs
   it. Follow the operation until it finishes; the first start takes a few
   minutes.
2. Open **https://homestead.local/apps/auth/** (Authentik answers under the
   appliance's name at `/apps/auth/`, ADR-044; it starts its first-run setup
   only there). Create the `akadmin` account with a
   strong password from your password manager.
3. In Authentik, go to **Admin interface → Directory → Tokens and App
   passwords → Create**. Make a token for user `akadmin`, with intent
   **API Token**. Copy the key.
4. On Homestead's **Sign-in** page, paste the token, choose **Plan**, review
   what will be created in Authentik, and **Approve**. The token is used for
   this change only and then forgotten.
5. Choose **Link** (on the Sign-in page or Account page), sign in to
   Authentik, and come back. Sign out. The login page now offers **Sign in
   with Authentik**, and the local password still works as the way back in.

### D4. Domain and access (Cloudflare tunnel)

Outside Homestead, in Cloudflare:

1. Add your domain to Cloudflare (**Add a domain**) and change the
   nameservers at your registrar to the two Cloudflare gives you. Wait until
   Cloudflare shows the domain as **Active**.
2. Create an API token: **My Profile → API Tokens → Create Token → Create
   Custom Token**, with these permissions:
   - Zone → Zone → Read
   - Zone → DNS → Edit
   - Account → Cloudflare Tunnel → Edit
   - Zone → SSL and Certificates → Read (recommended)

   Under Account Resources, include your account; under Zone Resources,
   include your domain's zone. This one token covers
   DNS and the tunnel. Homestead never asks for an account ID. The SSL and
   Certificates read lets Homestead check that Cloudflare has a certificate
   for each public name; without it, plans still go through, but warn that
   only the free certificate's rule was checked.

In the wizard's Domain step:

3. Choose **Cloudflare** as the provider and **Cloudflare Tunnel** as the
   access. Paste the token; it is sealed on the appliance and the form is
   cleared.
4. Enter your domain (for example `example.com`) and choose **Save domain**.
5. Open the **Tunnel** page. Name a **new** tunnel (for example
   `homestead-<customer>`). The form proposes `homestead.<domain>` for the
   dashboard; you can rename it. Authentik needs no name of its own: it
   answers at `https://homestead.<domain>/apps/auth/`, and the plan lists it
   under the dashboard's name with no DNS record (ADR-044). An appliance
   installed before S315 still serves Authentik at a name of its own, so the
   form also asks for one there, proposing `auth.<domain>`.
   Choose **Review plan**. The plan lists the tunnel to create, each DNS
   record, and any existing record it would replace. Approve it, then follow
   the operation to the end. If the plan is refused, the reason shows under
   Review plan.

   **Which names Cloudflare can serve.** Cloudflare's free certificate covers
   your domain and names **one level** below it (`homestead.example.com`,
   `auth.example.com`). A deeper name, such as `auth.homestead.example.com`
   or `jellyfin.homestead.example.com`, needs **Advanced Certificate
   Manager** on the zone (Cloudflare: SSL/TLS → Edge Certificates → order a
   certificate for `*.homestead.example.com`, or turn on Total TLS). Deeper
   names are allowed: the plan warns about each name no certificate covers,
   and you may still approve. The Tunnel page and each app's public address
   show a lamp: **certificate covers it** or **no certificate yet**.

   **One web address, shared.** Apps under the dashboard's public name
   (Authentik, Nextcloud, Jellyfin) share one web origin with the dashboard.
   A browser treats them as one site, so a flaw in one of them could reach
   the dashboard's sign-in in that browser. Homestead accepts this so one
   name and one certificate cover everything; the Tunnel page says so once.
   Immich keeps a public name of its own.
6. The Sign-in step now warns that sign-in must be **re-planned** for the
   public name. Go back to the **Sign-in** page and run Plan and Approve
   again with a new Authentik token, as in D3 step 4. This adds the public
   dashboard address to Authentik.
7. Back on the Domain step, choose **I have finished here**.

On the public dashboard name, only **Sign in with Authentik** is offered;
the local password works only on your network. Management stays local
apart from this one, signed-in path (ADR-037).

Already have a tunnel made outside Homestead? Use **adopt** instead of a new
name (`homesteadctl tunnel setup --adopt <tunnel-id>`). Adoption needs that
tunnel's connector configuration on this machine; a new or reset appliance
does not have it, so name a new tunnel.

### D5. Apps

1. Open the **Catalog** and choose an app, for example Jellyfin. The install
   wizard asks for its storage, then shows both of its addresses. Nextcloud,
   Jellyfin and Authentik answer under the appliance's name, by path
   (ADR-044); Immich and the craft apps keep a name of their own:
   - **On your network:** `https://homestead.lan/apps/jellyfin/` and
     `https://homestead.local/apps/jellyfin/` (for Immich, `immich.lan` and
     `immich.local`). When the appliance resolver is off, the step says
     `homestead.lan` needs the resolver (on the **Names** page) or a DNS
     entry on your router; the `.local` address works without setup.
   - **On the internet:** with a tunnel set up (D4), **Also publish at**
     shows `https://homestead.<domain>/apps/jellyfin/`, under the
     dashboard's public name with no DNS record; it needs the dashboard
     published first. For Immich it proposes `immich.<domain>`; keep it or
     type another. It is on when
     the same install sets up **Sign in with Authentik**, and off otherwise,
     because then only the app's own sign-in protects it. The step shows the
     certificate lamp for the name (the D4 step 5 rule). Without a tunnel the
     step says so and links to the **Tunnel** page.
2. On the review, the install plan and, when publishing, the public address
   plan are two permits, each with its digest. **Approve both and install**
   approves both. After the install, the Done step adds the local address,
   publishes the public name by its approved digest, then turns on sign-in,
   showing each phase. If the public plan changed in between, nothing is
   published: the step shows the new plan and asks again.

   The Done step then checks each address from the appliance and shows a
   lamp, **answers** or **does not answer yet**, with the reason and the fix
   (for example, turn on the resolver, or the certificate message). The
   check is advisory; it never fails the install. Run it again with
   **Check again**, or `homesteadctl apps reach jellyfin`.

   CLI: `homesteadctl apps install jellyfin -f jellyfin.json --public`
   prints the install digest and the public address plan with its digest,
   and changes nothing. Then
   `homesteadctl apps install jellyfin -f jellyfin.json --public --approve-public <digest>`
   installs, adds the address, publishes and prints the reach check (for
   Immich, pass the name after `--public`).

   To change or remove the public name later, use the app's **Manage**
   panel in the Catalog (**Review public address**, then **Approve**), or
   `homesteadctl apps public plan jellyfin`, then
   `homesteadctl apps public apply jellyfin --digest <digest> --follow`.

   **Apps installed before S315** keep their own name (`jellyfin.lan`).
   Their **Manage** panel offers **Move to https://homestead.lan/apps/jellyfin/**:
   **Review the move** shows the plan (the new addresses, the app settings
   it changes, a restart, and that the old address stops answering), and
   nothing moves until you approve it. Take a public name off the app
   first. Afterwards, re-plan the app's sign-in; for Authentik, tear the
   tunnel down first, then set the dashboard's sign-in up again at its new
   address. CLI: `homesteadctl apps move status|plan|apply jellyfin`.
3. **Nextcloud sets itself up.** Install creates its admin account (`admin`)
   with a generated password; there is no setup page to finish. The password
   is in the app's **Manage** panel under Secrets: choose **Reveal** and
   confirm (it asks you to sign in again, and the reveal is audited). CLI:
   `homesteadctl apps secrets reveal nextcloud admin_password`. Nextcloud
   takes a few minutes to finish installing; turning on sign-in waits for
   it. If it is still not ready, the page says so, links the app, and offers
   **Apply sign-in again**. Nextcloud trusts `homestead.lan`,
   `homestead.local` and the dashboard's public name, serves under
   `/apps/nextcloud/`, and builds https links behind the tunnel; publishing
   it or taking it off later updates its trusted domains.
   **Immich sets itself up too** (S318): at the end of install, Homestead
   creates its administrator, `admin@homestead.lan`, with a generated
   password (Manage → Secrets → **Reveal**, or
   `homesteadctl apps secrets reveal immich admin_password`). Immich opens
   on its login page, where people choose **Sign in with Authentik**; the
   administrator account is for emergencies. If Immich was slow to start,
   turning on sign-in (or **Apply sign-in again**) finishes the setup.
   **Jellyfin sets itself up too** (S319): install finishes its startup
   wizard. The server is named **Homestead**, the administrator is `admin`
   (password under Manage → Secrets → **Reveal**, or
   `homesteadctl apps secrets reveal jellyfin admin_password`), and three
   libraries are ready: Movies, TV Shows and Music. Their folders are on
   bulk storage at `<bulk mount>/jellyfin/media/movies`, `.../tv` and
   `.../music`; copy files there and they appear in Jellyfin.

### D6. Backups

The step proposes a nightly policy: 02:00 in the appliance's time zone,
keeping 7. It lists which apps it covers and which it leaves out, with the
reasons. Adjust the time or retention if you like, then **Approve**. Backups
go to the disk with the `backup` class.

The summary then shows every step's outcome.

---

## People

Open **People** (System group) to decide who can open which apps. It needs
Sign-in with Authentik with a kept API token (Sign-in page, "Keep the token");
without one the page says so, says what the token can do, and links to the
opt-in. Homestead creates, changes and removes only people it created or you
adopted, and only the `homestead-` groups; nothing else in Authentik is
touched, and service accounts are never listed.

- **Add a person**: name, username, optional email, the apps they can open,
  and how they first sign in (a one-time link, a generated password or a
  password you choose). A generated password or link is shown once and is not
  kept. One-time links need Authentik's recovery flow: the Sign-in plan adds
  Homestead's own (`homestead-recovery`) when the default brand has none. If
  sign-in could not be set up, the person is kept, the dialog says why and
  offers **Generate a password instead** (CLI: `homesteadctl people
  reset-password PERSON --generate`).
- **Apps**: grant or revoke each app, or make someone an app admin. People are
  never Homestead administrators; the dashboard stays yours alone.
- **Reset password**, **Reset second factor**, **Disable** and **Remove** each
  ask first. Removing deletes their Authentik account; app data stays in the
  apps.
- **Differs in Authentik**: if someone changed a person in Authentik, People
  shows the difference and changes nothing until you choose. Put back
  restores what Homestead recorded (recommended); Keep accepts Authentik's
  values. If the account was deleted, Recreate it or Remove from Homestead.
- **In Authentik, not managed here**: Adopt brings an existing account under
  Homestead.

The CLI does the same: `homesteadctl people list|add|edit|reset-password|
reset-mfa|disable|enable|remove|adopt|resolve` and
`people grant|revoke PERSON APP [--admin]`. Passwords are read from a 0600
file (`--password-file`) or standard input (`--password-stdin`), never from
the command line. The install wizard's review can grant people the app as part
of the install.

## Part E — Final checks

From a phone on **mobile data** (not your Wi-Fi):

| Open | Expect |
| --- | --- |
| `https://homestead.<domain>` | the Homestead login with only **Sign in with Authentik**; after signing in, the dashboard |
| `https://homestead.<domain>/apps/auth/` | the Authentik login |
| `https://homestead.<domain>/apps/jellyfin/` | Jellyfin, signing in through Authentik |
| `https://homestead.<domain>/apps/nextcloud/` while it is not published | 404 |

On the appliance:

```bash
systemctl is-active homestead-server homestead-tunnel   # active, active
homesteadctl login                                      # once, as the owner
homesteadctl setup status                               # every step done or skipped
homesteadctl doctor                                     # no failures
```

On your network, `https://homestead.local` and `https://jellyfin.local`
open without certificate warnings on devices that trust the root (C3).

Optional hardening: the **Firewall** page (`homesteadctl firewall plan`,
then `apply`) adds Homestead's nftables rules. The tunnel needs no inbound
port, so you never forward ports on the router for it.

---

## Part F — Preparing an appliance for a customer

To ship a unit, whether new, returned or used for testing, reset it to the
unclaimed state from the release checkout:

```bash
cd ~/homestead && git checkout <release> # then rebuild the dashboard (B2)
sudo ./scripts/factory-reset.sh --yes \
  --wipe-mount /mnt/app --wipe-mount /mnt/bulk --wipe-mount /mnt/backup \
  --reinstall
```

- **What it removes:** Homestead's services and binaries, its app stacks
  and their data, the edge proxy, and its state and configuration.
  Docker, cloudflared and docker images stay.
- **Storage:** it empties only the mounts named with `--wipe-mount` (it
  checks each is a real mount point). It never formats. Leave the flags off
  to keep data on the disks.
- **`--reinstall`:** brings the daemon back up, unclaimed. Check with
  `curl -s localhost:8080/api/v1/claim/status` ("appliance is not claimed")
  or the Status page.
- **The previous owner's tunnel and DNS records** live in their Cloudflare
  account. The reset does not remove them. Remove them there, or before the
  reset with `homesteadctl tunnel teardown <tunnel-id>`, which puts back any
  records the tunnel replaced.

Hand the customer the machine and this guide from Part C. The claim token
lasts 30 minutes, is replaced after that, and is not kept across a reboot,
so it cannot be printed in advance. Whoever claims reads it on the appliance
at that moment with `sudo homesteadctl claim token`. That means the customer
needs a console or SSH login to the machine's OS admin account: give them
those credentials (and ask them to change the password), or claim together
with them. Whoever redeems the token first becomes the owner.

---

## When something goes wrong

Start with Doctor: open **Doctor** in the System menu of the dashboard (or run
`homesteadctl doctor --live` on the appliance). It groups every check
(System, Storage, Network, Certificates, Tunnel, Sign-in, Apps), puts the
problems first, and says for each what it means and what to do. **Check
again** also asks the tunnel, Authentik and your apps. Doctor only reads and
explains; it changes nothing. The table below covers what Doctor cannot see.

| Symptom | Cause and fix |
| --- | --- |
| `https://homestead.local` answers "not found" instead of the dashboard | The dashboard was not embedded. Do B2, then `sudo make install`. |
| `homestead.local` does not resolve | The device lacks mDNS, or another service holds UDP 5353 on the appliance (`homesteadctl doctor` names it). Use `homestead.lan` with a DNS or hosts entry, or `ssh -L` (Part C). |
| Certificate warning on every visit | The device does not trust the root yet (C3). After a factory reset the root is new, so trust it again. |
| Install wizard says Docker is unreachable | Docker is not running: `sudo systemctl enable --now docker`. If it is missing, re-run `sudo ./scripts/bootstrap.sh`. |
| Tunnel step says "install cloudflared, then re-run setup" | The bootstrap ran with `--no-cloudflared`. Re-run `sudo ./scripts/bootstrap.sh` without it, then review the tunnel plan again. |
| Bootstrap: "docker is installed without Compose v2" | Install your Docker's compose package (Debian: `docker-compose`), or remove `docker.io` and re-run for Docker's own packages. |
| Bootstrap: "refusing to trust it" (fingerprint) | A vendor rotated its repository key. Check the vendor's published fingerprint, then re-run once with `HOMESTEAD_DOCKER_KEY_FPR` or `HOMESTEAD_CLOUDFLARE_KEY_FPR` set to it, and report it so the pin is updated. |
| Tunnel plan refused: no zone contains the domain | The token cannot see the domain's zone, or the domain is not active on Cloudflare yet (D4 steps 1 and 2). |
| Adopt refused: no connector config | Expected on a new or reset appliance; name a new tunnel. |
| `https://homestead.<domain>` gives a Cloudflare error | The connector is down: `systemctl status homestead-tunnel`, `journalctl -u homestead-tunnel`. |
| A public name fails with a TLS handshake error (`ERR_SSL_VERSION_OR_CIPHER_MISMATCH`, "SSL handshake failed", `curl: (35)`) | Cloudflare has no certificate for it: the name is more than one level below the domain. Its lamp says **no certificate yet**. Order an Advanced Certificate Manager certificate for the parent (`*.homestead.example.com`) or turn on Total TLS, or re-plan with a one-level name (`auth.example.com`). |
| Plans warn the token cannot read certificates | Add Zone → SSL and Certificates → Read to the token (D4 step 2) and save it again. Until then only the one-level rule is checked. |
| Nextcloud sign-in: "is not set up yet" | Nextcloud is still installing itself, or (for an install made before S312) its setup page was never finished. Open the app, finish or wait, then **Apply sign-in again**. |
| Public dashboard: Authentik refuses the redirect | Sign-in was not re-planned after the tunnel (D4 step 6). |
| Locked out of Authentik | Sign in with the local password on your network. `homesteadctl sso disable` turns Authentik sign-in off (`docs/architecture/DASHBOARD_SSO.md`). |
| Lost the owner password | On the appliance: `sudo homesteadctl admin recover`. |

## Further reading

- `docs/architecture/LOCAL_DASHBOARD.md`: local names, the `.lan` resolver,
  using the CLI from another machine
- `docs/architecture/SETUP_WALKTHROUGH.md`: how each wizard step is decided
- `docs/architecture/DASHBOARD_SSO.md`: Authentik sign-in and recovery
- `docs/architecture/INTERNAL_CA_TRUST.md`: trusting the root on each OS
- `docs/architecture/BACKUP_RESTORE_RUNBOOK.md`: restoring from backups
- `docs/adr/ADR-037-public-domain-tunnel-plan-and-public-dashboard.md`:
  what goes public and why
