package server

import (
	"fmt"
	"strings"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/doctor"
	"homestead/internal/store"
)

// backupEncryptionDoctorCheck warns, never fails, while backups can only
// be read with the appliance identity: losing the appliance disk loses
// the secret store and with it the only key (ADR-022). It clears when an
// owner-added recipient exists or the owner acknowledged storing an
// exported recovery identity offline (S271).
func backupEncryptionDoctorCheck(st *store.Store) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "backup-encryption"
		rec := backup.NewRecipientStore(st)
		owners, err := rec.Owner()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "backup recipients are unreadable: " + err.Error()}}
		}
		if len(owners) > 0 {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK,
				Detail: "backups are encrypted to the appliance identity and an owner-held recipient"}}
		}
		acked, hasAck, err := rec.LastAcknowledged()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the recovery identity export record: " + err.Error()}}
		}
		if hasAck {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK,
				Detail: "the recovery identity was exported and acknowledged as stored offline by " + acked.Actor + " on " +
					acked.AcknowledgedAt.Format("2006-01-02")}}
		}
		last, exported, err := rec.LastExport()
		if err != nil {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: "cannot read the recovery identity export record: " + err.Error()}}
		}
		if exported {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
				Detail: "the recovery identity was exported by " + last.Actor + " on " + last.ExportedAt.Format("2006-01-02") +
					" but not acknowledged as stored offline (`homesteadctl backup identity ack`)"}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "backups are encrypted only to the appliance identity, which lives in this appliance's secret store: " +
				"if the appliance is lost they cannot be read. Add an owner recipient (`homesteadctl backup recipients add`) " +
				"or export the recovery identity (`homesteadctl backup identity export`), store it offline, and acknowledge it"}}
	}
}

// backupRewrapDoctorCheck warns when backups may not all open with the
// current recovery key (S505): a re-wrap after a rotation that has not
// completed cleanly, or retired keys stored unreadably. Peek-style reads
// only; it never creates an identity.
func backupRewrapDoctorCheck(st *store.Store, secrets catalog.SecretStore) func() []doctor.Check {
	return func() []doctor.Check {
		const name = "backup-rewrap"
		warn := func(detail string) []doctor.Check {
			return []doctor.Check{{Name: name, Status: doctor.StatusWarn, Detail: detail}}
		}
		keys, err := backupKeyStore(secrets)
		if err != nil {
			return warn("the secret store is unavailable: " + err.Error())
		}
		var problems []string
		if _, bad, rerr := backup.RetiredIdentityReport(keys); rerr != nil {
			problems = append(problems, "the retired recovery keys cannot be read: "+rerr.Error())
		} else if bad > 0 {
			problems = append(problems, fmt.Sprintf("%d retired recovery key(s) are unreadable; older backups may not open with them (readable keys are still used)", bad))
		}
		sum, found, err := backup.NewRecipientStore(st).RewrapLast()
		if err != nil {
			problems = append(problems, "the last re-wrap record is unreadable: "+err.Error())
		} else if found {
			if cur, ok, perr := backup.PeekApplianceIdentity(keys); perr == nil && ok && sum.Fingerprint == backup.Fingerprint(cur.Recipient().String()) {
				switch {
				case sum.FailedCount > 0:
					problems = append(problems, fmt.Sprintf("the last re-wrap left %d file(s) unconverted (%s)", sum.FailedCount, strings.Join(sum.Failed, ", ")))
				case !sum.Complete:
					problems = append(problems, "existing backups have not been re-wrapped to the current recovery key since it was created")
				}
			}
		}
		if len(problems) > 0 {
			return warn(strings.Join(problems, "; ") + ". Run `homesteadctl backup identity rewrap` or use the Backups page")
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusOK, Detail: "existing backups were re-wrapped to the current recovery key and no retired key is unreadable"}}
	}
}
