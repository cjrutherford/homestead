package release

import (
	"fmt"
	"os"
)

// EmbeddedCraftKeyLabel names the craft signing key compiled into the
// binary (S279, ADR-028).
const EmbeddedCraftKeyLabel = "HAI craft signing key"

// EmbeddedCraftPublicKey is the HAI craft signing public key shipped in
// the binary. It is a distinct key class from EmbeddedPublicKey: a
// craft-signed manifest never verifies as a platform release and vice
// versa. Key id B03DB3F5AA66DB3E, generated 2026-10-17; the private half
// is held offline by HAI (docs/architecture/CRAFT_SIGNING_RUNBOOK.md) and
// the same public key is published as catalog/craft/craft.pub.
const EmbeddedCraftPublicKey = "untrusted comment: minisign public key B03DB3F5AA66DB3E\nRWQ+22aq9bM9sFj9nQ6kvyg5+UZnmMyNJM9k1QE8Bv0Bd7MaYzfLS+NQ\n"

// VerifyMessage checks a minisign signature text over message against
// the trusted keys and returns the matching key id. It is the shared
// authenticity step for every signed object (checksums files, craft
// manifests); the caller supplies only the keys of its own class.
func VerifyMessage(sigText string, message []byte, trusted []PublicKey) (string, error) {
	sig, err := ParseSignature(sigText)
	if err != nil {
		return "", err
	}
	for i := range trusted {
		if trusted[i].raw == sig.keyID {
			if err := sig.Verify(trusted[i], message); err != nil {
				return "", err
			}
			return trusted[i].KeyID, nil
		}
	}
	return "", fmt.Errorf("%w (key id %s)", ErrUnknownKey, sig.KeyID)
}

// ReadSmallFile reads a regular file of at most MaxSmallFileBytes under
// the S264 path-safety rules (no symlink final component, no FIFO or
// device, checked on the open descriptor).
func ReadSmallFile(path string) ([]byte, error) { return readSmall(path) }

// OpenRegularFile opens path under the same path-safety rules, bounded
// by limit bytes.
func OpenRegularFile(path string, limit int64) (*os.File, error) { return openRegular(path, limit) }
