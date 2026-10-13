package server

import (
	"homestead/internal/backup"
	"homestead/internal/doctor"
	"homestead/internal/store"
)

// backupEncryptionDoctorCheck warns, never fails, while backups can only
// be read with the appliance identity: losing the appliance disk loses
// the secret store and with it the only key (ADR-022). It clears when an
// owner-added recipient exists or the owner recorded exporting the
// recovery identity (S271).
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
		if e, ok, err := rec.LastExport(); err == nil && ok {
			return []doctor.Check{{Name: name, Status: doctor.StatusOK,
				Detail: "the recovery identity was exported by " + e.Actor + " on " + e.ExportedAt.Format("2006-01-02") +
					"; keep it stored offline"}}
		}
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "backups are encrypted only to the appliance identity, which lives in this appliance's secret store: " +
				"if the appliance is lost they cannot be read. Add an owner recipient (`homesteadctl backup recipients add`) " +
				"or export the recovery identity (`homesteadctl backup identity export`) and store it offline"}}
	}
}
