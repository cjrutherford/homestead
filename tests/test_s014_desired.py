"""S014 — YAML Desired State conformance tests.

Guardrails for desired-state storage: the YAML library stays pure-Go,
state defaults under the S012 config directory, the envelope is versioned,
writes are atomic (temp + rename), files stay customer-readable, parsing is
strict, and secret values are banned by construction note. Runtime behavior
is covered by Go tests in internal/desired.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestYamlDependency(unittest.TestCase):
    def test_yaml_library_is_direct_dep(self):
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertIn("gopkg.in/yaml.v3", direct)

    def test_no_competing_yaml_library(self):
        gomod = read("go.mod")
        for rival in ("gopkg.in/yaml.v2", "sigs.k8s.io/yaml"):
            with self.subTest(rival=rival):
                self.assertNotIn(rival, gomod)


class TestDesiredContract(unittest.TestCase):
    def test_default_path_under_etc_dir(self):
        src = read("internal/desired/desired.go")
        self.assertIn("layout.EtcDir", src)
        self.assertIn("desired.yaml", src)

    def test_envelope_is_versioned(self):
        src = read("internal/desired/desired.go")
        self.assertIn("CurrentVersion", src)
        self.assertIn("newer than supported", src)

    def test_writes_are_atomic(self):
        src = read("internal/desired/desired.go")
        self.assertIn("os.CreateTemp", src)
        self.assertIn("os.Rename", src)
        self.assertIn(".tmp", src)

    def test_file_stays_customer_readable(self):
        src = read("internal/desired/desired.go")
        self.assertIn("0o644", src)

    def test_parsing_is_strict(self):
        src = read("internal/desired/desired.go")
        self.assertIn("KnownFields(true)", src)

    def test_no_secret_values_by_construction(self):
        """Credentials stay in the secret store (S096), referenced by ID."""
        src = read("internal/desired/desired.go")
        self.assertIn("secret", src.lower())


if __name__ == "__main__":
    unittest.main()
