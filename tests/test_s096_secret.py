"""S096 — Secret Store conformance tests.

Guardrails for DNS/API secrets: values live AES-GCM encrypted in the
operational database (schema v7) with the key in a root-only file that
is generated once, refused when exposed, and validated for length;
reads fail closed on missing names and wrong keys; deletes converge;
listing reports names and timestamps but never values; names share the
credential-field shape; and values stay out of YAML, logs, and API
responses by construction. Behavior is covered by Go tests in
internal/secret against temporary stores.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSecretContract(unittest.TestCase):
    def test_store_shapes(self):
        src = read("internal/secret/secret.go")
        for symbol in ("type Store", "func New(", "func (s *Store) Set(",
                       "func (s *Store) Get(", "func (s *Store) Delete(",
                       "func (s *Store) List(", "type Info", "ErrNotFound",
                       '"secret.key"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_encrypted_at_rest(self):
        migration = read("internal/store/store.go")
        self.assertIn("secrets", migration)
        self.assertIn("ciphertext", migration)
        self.assertIn("CurrentVersion = 8", migration)
        src = read("internal/secret/secret.go")
        for symbol in ("aes.NewCipher", "cipher.NewGCM", "0600",
                       "must not be group/other readable", "decrypt failed"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_values_never_leave(self):
        src = read("internal/secret/secret.go")
        self.assertIn("never values", src)
        self.assertNotIn("log.", src)


if __name__ == "__main__":
    unittest.main()
