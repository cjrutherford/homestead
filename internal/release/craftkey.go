package release

import (
	"fmt"
	"os"
)

// EmbeddedCraftKeyLabel names the craft signing key compiled into the
// binary (S279, ADR-028).
const EmbeddedCraftKeyLabel = "HAI craft signing key (PLACEHOLDER: production craft key pending, see docs/DEFERRED.md)"

// EmbeddedCraftPublicKey is the HAI craft signing public key shipped in
// the binary. It is a distinct key class from EmbeddedPublicKey: a
// craft-signed manifest never verifies as a platform release and vice
// versa. PLACEHOLDER: generated with its private half discarded, so
// nothing can be signed for it; owners add their own or HAI's published
// craft key by configuration meanwhile (docs/DEFERRED.md).
const EmbeddedCraftPublicKey = "untrusted comment: HAI Homestead craft signing public key (PLACEHOLDER)\nRWS23md1HH5K5ivLdhResP6aOF+BwsWaAOGknsugWCjyFQhac4/urPqd\n"

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
