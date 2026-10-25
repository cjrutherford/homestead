# Installing Homestead: from bare metal to public apps

This guide takes one machine from an empty disk to a Homestead appliance
that serves its dashboard, Authentik sign-in and apps on your own domain
through a Cloudflare tunnel. Follow the parts in order. Each one ends with a
check, so you know it worked before you move on.

| Part | You end with | Where |
| --- | --- | --- |
| [A. Prepare the machine](#part-a--prepare-the-machine) | Debian, disks mounted, Docker, cloudflared | the machine's console or SSH |
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

### A3. Install Docker Engine and Compose

Homestead runs every app as an ordinary Docker Compose project. Install
Docker from Docker's own repository:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
. /etc/os-release
sudo curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

Debian's own packages also work. On Debian 13, `sudo apt-get install -y
docker.io docker-compose docker-buildx` is what the test VM runs.

Check: `sudo docker run --rm hello-world` prints "Hello from Docker!" and
`docker compose version` prints a v2 version.

### A4. Install cloudflared (for public access)

The tunnel runs Cloudflare's connector, `cloudflared`, as the
`homestead-tunnel` service. Homestead does not install it. Skip this step if
the appliance will stay on your home network only. You can come back to it
later.

```bash
sudo mkdir -p --mode=0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' \
  | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt-get update && sudo apt-get install -y cloudflared
```

If Cloudflare has rotated the key, take the current lines from
<https://pkg.cloudflare.com/>.

Check: `cloudflared --version` prints a version. Do **not** run
`cloudflared service install` or `cloudflared tunnel login`, because
Homestead writes the connector's configuration itself.

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
installs `ca-certificates`, `curl`, `nftables` and `age`, plus the Go
toolchain that `go.mod` names (official build, checksum-verified), and runs
`scripts/install-systemd.sh`. That script:

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

Check:

```bash
systemctl is-active homestead-server       # active
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
2. Open **https://authentik.local/** (the address itself; Authentik starts
   its first-run setup only there). Create the `akadmin` account with a
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

   Under Account Resources, include your account; under Zone Resources,
   include your domain's zone. This one token covers
   DNS and the tunnel. Homestead never asks for an account ID.

In the wizard's Domain step:

3. Choose **Cloudflare** as the provider and **Cloudflare Tunnel** as the
   access. Paste the token; it is sealed on the appliance and the form is
   cleared.
4. Enter your domain (for example `example.com`) and choose **Save domain**.
5. Open the **Tunnel** page. Name a **new** tunnel (for example
   `homestead-<customer>`). The form proposes `homestead.<domain>` for the
   dashboard and `auth.<domain>` for Authentik; you can rename either.
   Choose **Review plan**. The plan lists the tunnel to create, each DNS
   record, and any existing record it would replace. Approve it, then follow
   the operation to the end. If the plan is refused, the reason shows under
   Review plan.
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
   wizard asks for its storage, shows its local addresses
   (`jellyfin.lan` and `jellyfin.local`), and, where the app supports it,
   offers **Sign in with Authentik**. Review and approve, then follow the
   install.
2. To reach the app from the internet: in the Catalog, open the app's
   **Manage** panel. Under the public address, keep the proposed name
   (`jellyfin.<domain>`) or type another. Choose **Review public address**,
   then **Approve**. The page warns that the app's own sign-in is what
   protects it. With Authentik sign-in on, that is Authentik.

   CLI: `homesteadctl apps public plan jellyfin`, then
   `homesteadctl apps public apply jellyfin --digest <digest> --follow`.

### D6. Backups

The step proposes a nightly policy: 02:00 in the appliance's time zone,
keeping 7. It lists which apps it covers and which it leaves out, with the
reasons. Adjust the time or retention if you like, then **Approve**. Backups
go to the disk with the `backup` class.

The summary then shows every step's outcome.

---

## Part E — Final checks

From a phone on **mobile data** (not your Wi-Fi):

| Open | Expect |
| --- | --- |
| `https://homestead.<domain>` | the Homestead login with only **Sign in with Authentik**; after signing in, the dashboard |
| `https://auth.<domain>` | the Authentik login |
| `https://jellyfin.<domain>` | Jellyfin, signing in through Authentik |

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

| Symptom | Cause and fix |
| --- | --- |
| `https://homestead.local` answers "not found" instead of the dashboard | The dashboard was not embedded. Do B2, then `sudo make install`. |
| `homestead.local` does not resolve | The device lacks mDNS, or another service holds UDP 5353 on the appliance (`homesteadctl doctor` names it). Use `homestead.lan` with a DNS or hosts entry, or `ssh -L` (Part C). |
| Certificate warning on every visit | The device does not trust the root yet (C3). After a factory reset the root is new, so trust it again. |
| Install wizard says Docker is unreachable | Docker is not installed or not running: `sudo systemctl enable --now docker` (A3). |
| Tunnel step says "install cloudflared, then re-run setup" | Do A4, then review the tunnel plan again. |
| Tunnel plan refused: no zone contains the domain | The token cannot see the domain's zone, or the domain is not active on Cloudflare yet (D4 steps 1 and 2). |
| Adopt refused: no connector config | Expected on a new or reset appliance; name a new tunnel. |
| `https://homestead.<domain>` gives a Cloudflare error | The connector is down: `systemctl status homestead-tunnel`, `journalctl -u homestead-tunnel`. |
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
