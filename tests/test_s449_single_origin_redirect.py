"""S449: single-origin apps redirect their LAN names to the public name (ADR-060)."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_only_the_four_single_origin_apps_are_marked():
    marked = sorted(p.stem for p in (ROOT / "catalog").glob("*.yaml")
                    if "\nsingle_origin_sign_in: true" in "\n" + p.read_text(encoding="utf-8"))
    assert marked == ["actual-budget", "calibre-web-automated", "gitea", "mealie"]


def test_adr_amends_047_and_is_indexed():
    assert "## Amended by ADR-060" in read("docs/adr/ADR-047-published-apps-sign-in-at-authentiks-public-address.md")
    assert "ADR-060" in read("docs/adr/README.md")
    assert "orchestrator" in read("docs/adr/ADR-060-single-origin-apps-redirect-lan-to-public.md")


def test_stale_warnings_are_gone():
    assert "Invalid redirect URL" not in read("catalog/actual-budget.yaml")
    assert "DEFERRED S447" not in read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
