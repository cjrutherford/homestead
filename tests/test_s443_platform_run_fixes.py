"""S443: static checks that the platform-run fixes are in place."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


class S443PlatformRunFixesTest(unittest.TestCase):
    def test_identity_reports_setup_pending(self):
        assert "setup_pending" in read("internal/server/claim.go")
        assert "setup_pending" in read("dashboard/src/app/claim/claim.ts")


    def test_home_guard_sends_unclaimed_to_claim(self):
        guard = read("dashboard/src/app/home/home.guard.ts")
        assert "'/claim'" in guard and "setup_pending" in guard


    def test_mounts_deduped(self):
        assert "dedupeMountpoints" in read("internal/discover/storage.go")


    def test_checklist_names_real_commands(self):
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        assert "homesteadctl catalog" not in text
        assert "claim/status" not in text
        assert "auth.<public-domain>" not in text


if __name__ == "__main__":
    unittest.main()
