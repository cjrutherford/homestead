# Apps and the catalog

Homestead installs apps from a curated first-party catalog. Each app runs as a plain Docker Compose stack, so it keeps working if you stop using Homestead. Nothing outside the catalog can be installed from the dashboard.

## Installing an app

1. Open **Catalog** and choose an app. You can also start from a bundle, a curated group of apps with their combined memory needs checked against this machine (`homesteadctl apps bundles`).
2. The install wizard checks its requirements (Docker, the edge, storage), asks where the app keeps its data, and shows the app's addresses.
3. On the review step you choose whether the app gets **sign-in with Authentik**, whether it is also published on the internet, and which people can open it and with which role. Nothing is applied until you approve.
4. Homestead installs the app, then runs the steps you approved: it adds the app's address, publishes it if you asked, applies sign-in and grants the people you chose. Closing the browser does not interrupt this. If a step fails or its plan has changed since you approved it, the setup stops and tells you why, and you resume or skip it from the app's card.

From the command line the same flow is:

```
homesteadctl apps list
homesteadctl apps show jellyfin
homesteadctl apps plan jellyfin -f plan.yaml
homesteadctl apps install jellyfin -f plan.yaml --follow
homesteadctl apps setup status jellyfin
```

There is no first run for you to do by hand. Homestead creates each app's first account and seals any generated password; where an app lets you read it back, `homesteadctl apps secrets reveal PACKAGE SECRET` shows it once and the dashboard has the same control on the app's card.

## Where an app lives

Every app gets local names on your network: `<app>.lan` and `<app>.local`. Some apps are also served under the dashboard's own name at `/apps/<app>/`, for example `https://homestead.local/apps/jellyfin/`. See [Names, tunnel and public access](exposure.md).

## Signing in to apps

With Authentik linked, an app offers **Sign in with Authentik** (the button text differs from app to app). One account signs in to every app you have given that person.

- Most apps are signed in through OpenID Connect and keep a normal local login only for emergencies.
- Navidrome and Uptime Kuma cannot sign in through OpenID Connect, so Homestead puts Authentik in front of them (forward authentication). Uptime Kuma keeps its own login behind that.
- You can turn sign-in on or off per app on the app's card, or with `homesteadctl apps sign-in status|plan|apply PACKAGE`. Turning it off and on again restores each person's grants.

Writing an app's sign-in settings sometimes means writing into the app's own database. Homestead only does this where the owner has accepted it, and each app update is re-checked.

## The apps page

The apps page is the first thing anyone sees after signing in. It lists every installed app with a web address and opens it in one click. The owner sees everything and has a **Manage** link on each card. A person sees only the apps they were granted. `homesteadctl apps mine` prints the same list for you.

## App groups and roles

Access is **closed by default**: a new app is open to nobody except you until you grant it. Homestead keeps one pair of groups per app in Authentik, `homestead-<app>-users` and `homestead-<app>-admins`, plus an owner group. Granting a person an app adds them to the users group; making them an app admin adds the admins group as well. An app admin is an administrator of that app only, never of Homestead. Grants are made on the **People** page; see [People and sign-in](people.md).

## Craft apps

The Craft apps (DesignCraft, FigCraft, SoundCraft, GridCraft, VectorCraft, PhotoCraft, EffectCraft and the rest of that family) are small browser tools. Homestead builds them on the appliance from signed source, and you can check or rebuild one from the **Craft apps** page or with `homesteadctl craft status`, `craft plan APP` and `craft build APP`.

Things you should know:

- **They have no accounts and no server-side storage.** The LAN is open: anyone on your home network can open a craft app at its local address without signing in. Published on the internet, a craft app sits behind the shared Authentik gate and only people you granted can open it.
- **FigCraft and SoundCraft cannot save your work in the browser.** This is a limit of the upstream apps, not of Homestead. Closing the tab loses unsaved work. SoundCraft can export audio (Bounce Mix downloads a WAV file).
- **DesignCraft saves by download.** Use its Save or Export to download a file or PNG; it does not keep work between visits.
- Where you have linked Nextcloud for craft file storage, files can go there instead (`homesteadctl craft files status`).

## Changing and removing apps

Use **Configure** on the app's card, or `homesteadctl apps configure`. Updates are covered in [Updates and releases](updates.md).

```
homesteadctl apps uninstall jellyfin --follow
```

Uninstalling removes the app, its addresses and its sign-in. Its data stays on disk unless you pass `--remove-data --data-dir MOUNT:PATH`.
