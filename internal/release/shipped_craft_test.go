package release

import (
	"os"
	"strings"
	"testing"
)

// The shipped craft manifest must carry a signature by the embedded HAI
// craft key, so any manifest change without a re-sign fails here
// (docs/architecture/CRAFT_SIGNING_RUNBOOK.md, step 4).
func TestShippedCraftManifestIsSignedByTheEmbeddedKey(t *testing.T) {
	manifest, err := os.ReadFile("../../catalog/craft/craft-manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile("../../catalog/craft/craft-manifest.yaml.minisig")
	if err != nil {
		t.Fatalf("catalog/craft/craft-manifest.yaml.minisig: %v; the HAI signer must re-sign the manifest", err)
	}
	key, err := ParsePublicKey(EmbeddedCraftPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMessage(string(sig), manifest, []PublicKey{key}); err != nil {
		t.Fatalf("shipped craft manifest does not verify against the embedded craft key: %v; the HAI signer must re-sign it", err)
	}
}

// catalog/craft/craft.pub publishes the same key the binary embeds.
func TestPublishedCraftKeyMatchesTheEmbeddedKey(t *testing.T) {
	published, err := os.ReadFile("../../catalog/craft/craft.pub")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(published)) != strings.TrimSpace(EmbeddedCraftPublicKey) {
		t.Fatalf("catalog/craft/craft.pub differs from EmbeddedCraftPublicKey")
	}
}
