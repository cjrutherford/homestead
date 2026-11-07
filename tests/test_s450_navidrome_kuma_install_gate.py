"""S450: Navidrome and Uptime Kuma get their administrator and gate at install."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_manifests_declare_first_run_and_revealable_secret():
    for name in ("navidrome", "uptime-kuma"):
        y = read(f"catalog/{name}.yaml")
        assert f"routine: {name}" in y
        assert "name: admin_password" in y and "reveal: true" in y


def test_kuma_account_mode_and_trust_proxy():
    y = read("catalog/uptime-kuma.yaml")
    assert "account:" in y and "trustProxy" in y


def test_routines_use_stdin_only():
    assert "createAdmin" in read("internal/firstrun/navidrome.go")
    assert "bcrypt" in read("internal/firstrun/uptimekuma.go")


def test_slice_doc():
    assert "ADR-059" in read("docs/slices/S450-navidrome-kuma-install-gate.md")
