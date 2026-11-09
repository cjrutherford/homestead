"""S445: FreshRSS is installed by Homestead, no web installer."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


class S445FreshrssFirstRunTest(unittest.TestCase):
    def test_manifest_declares_first_run_and_revealable_password(self):
        y = read("catalog/freshrss.yaml")
        assert "routine: freshrss" in y and "secret: admin_password" in y
        assert "reveal: true" in y and "Finish FreshRSS's web installer first" not in y


    def test_routine_uses_cli_and_keeps_password_off_argv(self):
        g = read("internal/firstrun/freshrss.go")
        assert "do-install.php" in g and "create-user.php" in g and '$(cat)' in g


    def test_checklist_has_no_installer_step(self):
        t = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        assert "finish FreshRSS's web installer" not in t


if __name__ == "__main__":
    unittest.main()
