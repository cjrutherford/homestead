"""S444: Gitea has a first-run administrator and closed registration."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_manifest_closes_registration_and_declares_first_run():
    y = read("catalog/gitea.yaml")
    assert 'GITEA__service__DISABLE_REGISTRATION: "true"' in y
    assert "routine: gitea" in y and "account: homestead-admin" in y


def test_routine_keeps_password_out_of_argv():
    g = read("internal/firstrun/gitea.go")
    assert "--random-password" in g and '"--password"' not in g


def test_checklist_asks_for_no_manual_account():
    t = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
    assert "Register the first local account" not in t
