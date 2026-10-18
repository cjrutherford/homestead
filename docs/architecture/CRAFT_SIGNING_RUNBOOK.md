# Craft Manifest Signing Runbook (S249, ADR-028)

Who signs, and how a craft app version becomes installable. The agent
prepares an unsigned manifest; the owner signs it offline. The formats,
the check order and the manual digest check are in
`docs/architecture/RELEASE_VERIFICATION.md`; the package shape is in
`docs/architecture/STATIC_CRAFT_PACKAGES.md`.

## What is signed

`catalog/craft/craft-manifest.yaml` lists every craft app release the
repository ships: upstream artifact URL, SHA-256 and size, the monotonic
`counter`, and the digest of the shared build context (`images/craft/`).
The signature `catalog/craft/craft-manifest.yaml.minisig` covers exactly
those bytes. Until the owner signs, the repository ships the unsigned
manifest only; `craft plan` and `craft build` refuse it (no signature), so
craft packages cannot be installed from an unsigned checkout. That is the
intended state, not a bug.

A test (`TestShippedCraftManifestMatchesTheBuildContext`) fails if anything
in `images/craft/` changes without the manifest being regenerated, so a
stale manifest cannot be committed by accident.

## 1. Generate the key (once, offline)

On a machine that is not the appliance and is not connected to the
repository's remotes:

```
sudo apt-get install -y minisign        # or your package manager
minisign -G -p craft.pub -s craft.key   # choose a passphrase
```

- `craft.key` is the secret. It never enters this repository:
  `scripts/sign-craft-manifest.sh` refuses a key path inside the
  repository. Keep it offline and keep an offline backup (a second
  encrypted copy). The passphrase protects it at rest; do not use `-W`
  (no passphrase) for the real key.
- `craft.pub` is public. Commit it or publish it; the owner of an appliance
  adds it with `craft trust add` (step 6).
- Never reuse the platform release key (ADR-025). Neither key verifies the
  other's artifacts.

## 2. Fetch and cross-check the upstream artifact

Treat downloads as untrusted data: put them in a fresh empty directory, do
not execute or unpack them there, and pass paths as arguments.

```
mkdir -p ~/craft-work/art && cd ~/craft-work/art
B=https://github.com/storytold/wordcraft/releases/download/v0.7.0
curl -fLO $B/wordcraft-web-0.7.0.zip
curl -fLO $B/SHA256SUMS.txt
grep ' wordcraft-web-0.7.0.zip$' SHA256SUMS.txt
sha256sum wordcraft-web-0.7.0.zip     # the two digests must be equal
```

Upstream `SHA256SUMS.txt` is unsigned: the cross-check only catches a
corrupted or tampered download. Trust rests on your signature in step 4.
Read the release's licence files too and record version, URL, SHA-256 and
the licence judgement in `docs/THIRD_PARTY.md`.

## 3. Prepare the unsigned manifest

From a checkout of the repository at the commit you intend to ship:

```
scripts/craft-manifest.sh --counter N --artifact-dir ~/craft-work/art \
    --app wordcraft 0.7.0 wordcraft-web-0.7.0.zip \
    --url $B/wordcraft-web-0.7.0.zip > craft-manifest.yaml
```

- `N` is a single sequence for the whole signing train, not per app: use
  one more than the highest counter you have ever signed. A manifest below
  the highest recorded counter is a rollback and is refused (the audited
  `--allow-older` override exists for recovery only).
- List every app the repository ships in one manifest (repeat `--app`).
- Omit `--url` only for an artifact the owner supplies as a local file.
- Review `craft-manifest.yaml`: app names and versions, each SHA-256
  (equal to step 2), sizes, and `context_digest`. Compare with the
  committed `catalog/craft/craft-manifest.yaml`; the agent-prepared file
  should be byte-identical to what you generate.

## 4. Sign

```
scripts/sign-craft-manifest.sh craft-manifest.yaml --key /secure/craft.key
```

This runs `minisign -S -l` (the non-prehashed `Ed` form the daemon
requires) and writes `craft-manifest.yaml.minisig`. `TestShippedCraftManifestIsSignedByTheEmbeddedKey`
(`internal/release`) fails until a changed manifest is re-signed with the
embedded HAI craft key. Copy both files over
`catalog/craft/` and commit the signature beside the manifest. Check:

```
minisign -V -p craft.pub -m catalog/craft/craft-manifest.yaml
```

## 5. Ship

`scripts/install-systemd.sh` copies `catalog/` (including `craft/`) and the
build context to the appliance. Files you supply by hand must be readable
by the daemon: its unit sets `ProtectHome=true` and `PrivateTmp=true`, so
use `/srv` or `/opt`, not `/home` or `/tmp`.

## 6. Trust the key on the appliance

```
homesteadctl craft trust add --key-file craft.pub --label "HAI craft key"
homesteadctl craft trust list
homesteadctl craft plan wordcraft          # verify only: signature, counter, artifact, context
homesteadctl craft build wordcraft --follow
```

`craft plan` fetches nothing and builds nothing. Since 2026-10-17 the
binary embeds the HAI craft key (id `B03DB3F5AA66DB3E`, also published as
`catalog/craft/craft.pub`), so an appliance verifies the shipped, signed
manifest without this step. Add a key this way only for your own
artifacts, a rotated HAI key, or development.

## Key loss and rotation

- Lost or leaked secret key: generate a new key, `craft trust add` the new
  public key on every appliance, then `craft trust remove` the old key id.
  Appliances never needed the secret, so nothing else breaks; installed
  images keep running (their recorded digests are local).
- Re-sign the shipped manifest with the new key at a higher counter. A
  leaked key is a reason to remove it from appliances before they build
  anything new, because the daemon trusts every key on its list.
- Routine rotation is the same procedure: add the new key, re-sign at a
  higher counter, remove the old key once every appliance has the new one.

## Throwaway keys (development and KVM only)

For acceptance tests use a key that never ships:

```
minisign -G -W -p throwaway.pub -s throwaway.key   # -W: no passphrase
```

- Create it in a scratch directory outside the repository and the
  appliance's backups; sign test manifests with it; `craft trust add` it
  on the test VM only.
- Remove it afterwards (`craft trust remove KEY_ID`), delete the files, and
  remove the `craft_baseline`, `craft_verdicts` and `craft_images` rows if
  you want no trace.
- Never commit a throwaway key or its signatures, never embed one in the
  binary, and never trust one on a real appliance. Automated tests generate
  keys in memory (`internal/craft`, `internal/docker`) and write nothing.
