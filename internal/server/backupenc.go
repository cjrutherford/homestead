package server

import (
	"homestead/internal/doctor"
)

// ownerBackupRecipients counts owner-added recipients and recorded
// recovery-identity exports. S271 adds both; until then the appliance
// identity is the only recipient (ADR-022).
func ownerBackupRecipients() int { return 0 }

// backupEncryptionDoctorCheck warns, never fails, while backups are
// encrypted only to the appliance identity: losing the appliance disk
// loses the secret store and with it the only key (ADR-022).
func backupEncryptionDoctorCheck() []doctor.Check {
	const name = "backup-encryption"
	if ownerBackupRecipients() > 0 {
		return []doctor.Check{{Name: name, Status: doctor.StatusOK,
			Detail: "backups are encrypted to the appliance identity and an owner-held recipient"}}
	}
	return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
		Detail: "backups are encrypted only to the appliance identity, which lives in this appliance's secret store: " +
			"if the appliance is lost they cannot be read. Owner recipient and recovery-identity export arrive in S271"}}
}
