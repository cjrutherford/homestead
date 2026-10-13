package server

import (
	"homestead/internal/backup"
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
