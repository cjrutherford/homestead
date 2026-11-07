"""S443: static checks that the platform-run fixes are in place."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_identity_reports_setup_pending():
    assert "setup_pending" in read("internal/server/claim.go")
    assert "setup_pending" in read("dashboard/src/app/claim/claim.ts")


def test_home_guard_sends_unclaimed_to_claim():
    guard = read("dashboard/src/app/home/home.guard.ts")
    assert "'/claim'" in guard and "setup_pending" in guard


def test_mounts_deduped():
    assert "dedupeMountpoints" in read("internal/discover/storage.go")


def test_checklist_names_real_commands():
    text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
    assert "homesteadctl catalog" not in text
    assert "claim/status" not in text
    assert "auth.<public-domain>" not in text
