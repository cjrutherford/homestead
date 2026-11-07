"""S448: Mealie and Calibre-Web Automated lose their well-known seeded logins at install."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_manifests_declare_first_run_and_revealable_secret():
    for name, routine in (("mealie", "mealie"), ("calibre-web-automated", "calibre-web-automated")):
        y = read(f"catalog/{name}.yaml")
        assert f"routine: {routine}" in y
        assert "name: admin_password" in y and "reveal: true" in y


def test_mealie_routine_replaces_password_and_identity():
    g = read("internal/firstrun/mealie.go")
    assert "/api/users/password" in g and "MealieVerified" in g and "isFirstLogin" in g
    assert "OIDC_REQUIRES_EMAIL_VERIFICATION" in read("catalog/mealie.yaml")


def test_cwa_routine_is_compare_and_set_and_pinned():
    g = read("internal/firstrun/cwa.go")
    assert "AND password = ?" in g and "CWAVerified" in g and "os.setuid" in g


def test_checklist_and_copy():
    t = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
    assert "SRV-MEALIE-3a" in t and "SRV-CALIBRE-WEB-AUTOMATED-3a" in t
    c = read("dashboard/src/app/catalog/curated-copy.ts")
    assert "MyPassword" in c and "admin123" in c
