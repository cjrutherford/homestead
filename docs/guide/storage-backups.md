# Storage and backups

## Mounted storage

Homestead never formats, partitions or mounts disks. You mount them yourself, then tell Homestead about them on the **Storage** page:

1. **Register** each mounted disk with a short label.
2. **Assign** a class to a registered mount. The classes are `application`, `database`, `bulk` and `backup`. Apps keep their data on the disk assigned to the class they ask for. One disk can carry several classes.

```
homesteadctl storage mounts
homesteadctl storage register --mountpoint /mnt/app --label app
homesteadctl storage assign --class application --mountpoint /mnt/app
homesteadctl storage assignments
```

Moving a class to another disk does not move existing data. Use **Data** (`homesteadctl data plan PACKAGE`) to review and approve a move of an app's data. Unassigning or unregistering only drops Homestead's record; your files stay where they are.

## Nightly backups

Homestead proposes a nightly policy during setup: 02:00 in the appliance's time zone, keeping the newest 7 runs of each app. It lists which apps it covers and which it leaves out, and why. Apps that keep no data, such as the Craft apps, are not backed up. A night missed because the appliance was off runs once when it starts again.

Backups are written to the disk assigned the `backup` class. You can change the time and retention on the **Backups** page or with the CLI, and give one app its own schedule.

```
homesteadctl backup policy status
homesteadctl backup destination
homesteadctl backup schedules
homesteadctl scheduler jobs
```

Every backup is encrypted with the `age` format. Run files contain a plain `backup.json` that says what is inside and holds no secrets.

## The recovery key

Backups are encrypted to the appliance's own recovery key, and to any owner keys you add. If the appliance disk is lost, the backups can be opened only with a key you kept elsewhere. **Export the recovery key once, store it offline** (a USB stick in a drawer), and acknowledge that you did. Doctor warns until you have.

```
homesteadctl backup identity status
homesteadctl backup identity export --out recovery.key
homesteadctl backup identity ack
```

Treat the file like a password. Exporting needs a fresh sign-in and is audited. You can also add your own key as an extra recipient with `homesteadctl backup recipients add --key age1...`. It applies to new runs only.

### Rotating the recovery key

If the key may have leaked, or just on a schedule, rotate it from **Backups** (Rotate recovery identity) or:

```
homesteadctl backup identity rotate --out new-recovery.key --ack
```

Rotation makes a new key at once, then re-wraps the header of every existing backup in the background so the new key opens it; the command follows progress, and the Backups page shows it in the work orders. Nothing is deleted. If a re-wrap fails or is interrupted, Doctor says so; run `homesteadctl backup identity rewrap` (or **Re-wrap existing backups**) to finish it. Rotation is refused until you have exported and acknowledged the current key. Store the new key offline and acknowledge it.

**Keep the old key** until every run made before the rotation has been pruned by retention. The appliance keeps the old key as a fallback and never removes it by itself, and older runs on another disk may not have been re-wrapped. Rotation does not take access away from a leaked old key to backups that already exist; it protects new ones only. Rotating again re-wraps runs that were missed.

## Restoring

Restoring needs only standard tools, so it works even if Homestead is gone. You need the backup run directory, a key that opened it (your exported recovery key or an owner key), `age`, `tar` and Docker with Compose. The step-by-step runbook is `docs/architecture/BACKUP_RESTORE_RUNBOOK.md` in the Homestead source. In outline:

1. Read the run's `backup.json` and note its warnings.
2. Decrypt each archive with `age` and your key into a private directory.
3. Extract it as root into the app's data path, then start the app's Compose stack.

You can also copy the whole appliance configuration for disaster recovery with `homesteadctl config export -f FILE`. It contains secret names, never values.
