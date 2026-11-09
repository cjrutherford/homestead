"""S446: Memos is set up by Homestead, no first-account screen."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


class S446MemosFirstRunTest(unittest.TestCase):
    def test_manifest_declares_first_run_and_revealable_password(self):
        y = read("catalog/memos.yaml")
        assert "routine: memos" in y and "secret: admin_password" in y and "reveal: true" in y
        assert "disallowPasswordAuth" in y and "disallowUserRegistration" in y


    def test_routine_keeps_password_on_stdin(self):
        g = read("internal/firstrun/memos.go")
        assert "--post-file" in g and "/dev/stdin" in g and "needsSetup" in g


    def test_checklist_has_no_first_account_step(self):
        t = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        assert "Create the first Memos account" not in t


if __name__ == "__main__":
    unittest.main()
