"""S463 - Docker default-address-pools sized for the full catalog."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestDockerPools(unittest.TestCase):
    def test_installer_restarts_docker_only_on_change(self):
        s = read("scripts/install-systemd.sh")
        self.assertIn("docker-pools", s)
        self.assertIn('= "changed"', s)
        self.assertIn("systemctl restart docker.service", s)

    def test_pool_is_small_subnets_and_never_clobbers(self):
        s = read("internal/dockerpools/dockerpools.go")
        self.assertIn("const Size = 24", s)
        self.assertIn('"10.200.0.0/16"', s)
        self.assertIn("owner's pools win", s)

    def test_doctor_check_and_meta(self):
        self.assertIn('"docker-address-pools"', read("internal/doctor/doctor.go"))
        self.assertIn('"docker-address-pools"', read("internal/doctor/meta.go"))

    def test_slice_recorded(self):
        self.assertIn("S463", read("docs/SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S463-*.md")))


if __name__ == "__main__":
    unittest.main()
