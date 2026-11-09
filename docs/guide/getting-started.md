# Getting started

This page takes you from a bare machine to a claimed appliance with working sign-in. The full step-by-step install, including disk preparation and Cloudflare screens, is `docs/INSTALL.md` in the Homestead source.

## What you need

- A machine with Debian or Ubuntu Server (x86_64 or arm64), at least 4 GB of RAM (8 GB if you run Immich or Nextcloud), a system disk and one or more data disks.
- A fixed address on your network. Use a DHCP reservation on your router.
- For public access only: a domain whose DNS is on Cloudflare. Without one, Homestead works on your home network only.

## 1. Mount your data disks

Homestead never formats, partitions or mounts disks. You mount them once, and Homestead uses what is mounted. A typical layout:

| Mount | Used for |
|---|---|
| `/mnt/app` | app data and databases |
| `/mnt/bulk` | large media libraries |
| `/mnt/backup` | nightly backups |

One disk can carry several of these if you have only one.

## 2. Install

On the machine, from the Homestead release you mean to run:

```
sudo ./scripts/bootstrap.sh
```

It installs Docker, the edge proxy and `cloudflared`, builds `homestead-server` and `homesteadctl`, and starts the `homestead-server` service. It is safe to run again. Use `--no-cloudflared` if the appliance will stay on your home network.

## 3. Claim the appliance

Claiming happens on your own network or on the machine itself. Nothing unclaimed is ever put on the internet.

1. On the appliance, print the one-time claim token. It is valid for 30 minutes.

   ```
   sudo homesteadctl claim token
   ```

2. On a computer on the same network, open `https://homestead.local`. Your browser warns about the certificate. That is expected, because the appliance has its own certificate authority.
3. Choose **Claim this appliance**, paste the token and create the owner account with a password from your password manager.

If `homestead.local` does not resolve (some Android devices and office networks do not do mDNS), use `https://homestead.lan` with a router DNS entry or a hosts entry, or forward the port over SSH and claim at `http://localhost:8080`.

On each device you use, download the root certificate from the **Trust** page, check its fingerprint against the one on the page and install it. Homestead never installs it for you. After that, local names open without warnings.

## 4. The setup wizard

After you claim, the dashboard walks you through six steps: Account, Storage, Sign-in, Domain and access, Apps and Backups. You can leave and come back; it opens on the first step still pending. Do **Sign-in before Domain**, because the tunnel publishes the dashboard only once Authentik sign-in is linked.

1. **Account.** On the **Backups** page, export the recovery key and store it offline, then acknowledge it. See [Storage and backups](storage-backups.md). Then confirm your password.
2. **Storage.** On the **Storage** page, register each mounted disk and assign a class to it. The step is done once the `application` class is assigned.
3. **Sign-in.** Install Authentik and link it, as described below.
4. **Domain and access.** Optional. See [Names, tunnel and public access](exposure.md).
5. **Apps.** Install your first apps. See [Apps and the catalog](apps.md).
6. **Backups.** Approve the proposed nightly policy.

## The first-run Authentik setup stays manual

Every other app is set up by Homestead with no first-run screen for you to fill in. Authentik is the one exception: it holds your accounts, so you create its administrator yourself.

1. Install Authentik from the **Sign-in** step. The first start takes a few minutes.
2. Open `https://homestead.local/apps/auth/` and create the `akadmin` account with a strong password.
3. In Authentik, go to **Admin interface**, then **Directory**, then **Tokens and App passwords** and create a token for `akadmin` with intent **API Token**. Copy the key.
4. On Homestead's **Sign-in** page, paste the token and choose **Plan**. Review what will be created in Authentik and **Approve**. The token is used for this change only and then forgotten, unless you choose to keep it. People management needs a kept token.
5. Choose **Link**, sign in to Authentik and come back. The login page now offers **Sign in with Authentik**. Your local password still works on your network as the way back in.

## The dashboard

Open `https://homestead.local` (or the name you chose). Signed in as the owner you see the apps page first, with the **Admin** area for settings: Catalog, People, Storage, Backups, Updates, Tunnel, Doctor, Audit and the rest. People you give a sign-in see only the apps page. The **Guide** link opens this guide.

If you lose the owner password, run this on the appliance:

```
sudo homesteadctl admin recover
```
