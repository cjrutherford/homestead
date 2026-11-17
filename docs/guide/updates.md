# Updates and releases

## App updates

Open **Updates** to see which apps have a newer version in the catalog. Applying an update is reviewed and approved like any change, and is audited.

```
homesteadctl updates list
homesteadctl updates apply PACKAGE --backup --follow
```

`--backup` takes a backup of the app first. Homestead moves an app to the version its catalog entry names. After an update, sign-in settings for apps that need them are checked again.

## Platform updates and signed releases

The platform (the `homestead-server` daemon and its tools) is updated from a release you download yourself. Every release is signed offline with a minisign key. Homestead trusts a release only if its signature checks out against a key in your trusted list, so a tampered or unsigned file is refused.

```
homesteadctl updates platform
homesteadctl updates verify --component NAME --artifact FILE --checksums FILE --signature FILE
homesteadctl updates apply-platform --component NAME --artifact FILE --checksums FILE --signature FILE --follow
```

Verification runs first and nothing is installed if it fails. Applying keeps the previous binary, so you can go back:

```
homesteadctl updates rollback-platform --follow
```

Older releases are refused unless you say so explicitly (`--allow-older`). The daemon reads the release files from a place it is allowed to see, such as `/srv`. It cannot read your home directory or `/tmp`.

## Trusted keys

The **Updates** page lists the trusted release keys. The one built into your appliance is the HAI release key.

```
homesteadctl updates trust list
```

### Rotating the release key

When the publisher moves to a new signing key, you move over in three steps, so you are never without a working key:

1. **Add** the new public key: `homesteadctl updates trust add --key-file NEW.pub --label "new key"`.
2. Install a release signed with the new key. Both keys now verify.
3. **Remove** the old key: `homesteadctl updates trust remove KEY_ID`. Only the new key verifies now.

Craft apps use a separate set of signing keys (`homesteadctl craft trust list`), so rotating one does not affect the other.

If you publish your own releases, `scripts/release/keygen.sh --out PATH` creates a minisign key pair offline. The private key must stay outside the source tree and off the appliance. The procedure is in `docs/RELEASING.md`.
