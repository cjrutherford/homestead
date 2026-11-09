# Troubleshooting

## Start with Doctor

Open **Doctor** in the dashboard, or run this on the appliance:

```
homesteadctl doctor --live
```

Doctor groups every check by area (System, Storage, Network, Certificates, Tunnel, Sign-in, Apps), puts problems first, and says what each one means and what to do. **Check again** also asks the tunnel, Authentik and your apps. Doctor only reads and explains. It changes nothing.

## Common warnings

| You see | What it means | What to do |
|---|---|---|
| The recovery key has not been acknowledged | You never exported it, or you rotated it | Export or rotate it on **Backups**, store it offline, then acknowledge. See [Storage and backups](storage-backups.md). |
| An app's container is stopped | It was stopped outside Homestead | Start the stack, or reinstall; Doctor names the app. |
| A public name has a plan problem | A tunnel entry or DNS record does not match what Homestead recorded | Review the plan on **Tunnel** and approve it again. |
| `homestead.local` does not resolve | The device has no mDNS, or something else holds UDP 5353 | Use `homestead.lan` with a hosts or DNS entry, or `ssh -L`. |
| Certificate warning on every visit | The device does not trust the appliance root | Download it from the **Trust** page and install it on that device. |
| Public name gives a TLS handshake error | Cloudflare has no certificate for a name more than one level deep | Use a one-level name or order Advanced Certificate Manager. |
| Public dashboard: Authentik refuses the redirect | Sign-in was not re-planned after the tunnel | Re-run the Sign-in plan and approve it. |
| An app asks for setup you did not expect | Its post-install setup stopped | Open the app's card, read why, then resume or skip. |
| A drift notice on an app | Someone edited its Compose files by hand | Open **Drift** and accept or restore. |

## Known limits

- **FreshRSS sign-in fails for the owner.** The owner's Authentik token carries every app group, which makes FreshRSS's session too large to store, so the owner gets an error on `/i/oidc/`. People with fewer groups sign in normally. Until a fix lands, open FreshRSS with a person account that has few groups; this is recorded in `docs/DEFERRED.md`.
- **Craft apps do not keep your work.** See [Apps and the catalog](apps.md).
- **The public Authentik login has no Forgot password link.** Ask the owner to reset it from People.

The full list of known gaps is `docs/DEFERRED.md` in the Homestead source.

## If you are locked out

- Authentik is down or unreachable: use your local password on your home network.
- You forgot the owner password: `sudo homesteadctl admin recover` on the appliance.
- The daemon: `systemctl status homestead-server` and `journalctl -u homestead-server`.
- The tunnel connector: `systemctl status homestead-tunnel`.
