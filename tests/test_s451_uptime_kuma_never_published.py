"""S451: Uptime Kuma is never published, enforced by a manifest field."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


class S451UptimeKumaNeverPublishedTest(unittest.TestCase):
    def test_manifest_field_is_publish_never(self):
        # S471 folded never_publish into publish: never (ADR-061).
        assert not [p.stem for p in (ROOT / "catalog").glob("*.yaml")
                    if "\nnever_publish:" in "\n" + read(f"catalog/{p.name}")]
        assert "\npublish: never" in "\n" + read("catalog/uptime-kuma.yaml")


    def test_prose_claims_have_the_field(self):
        for p in sorted((ROOT / "catalog").glob("*.yaml")):
            text = read(f"catalog/{p.name}").lower()
            claims = "never published" in text or "can never be published" in text or "refused by design" in text
            assert claims == (p.stem == "uptime-kuma"), p.name


    def test_server_enforces_on_plan_and_install(self):
        ap = read("internal/server/apppublic.go")
        assert "PublicRefusal()" in ap and "NeverPublish" in ap  # the status field
        assert "PublicRefusal()" in read("internal/server/catalog.go")
        assert "never-publish" in read("internal/server/doctorlive.go")


    def test_ui_offers_nothing(self):
        assert "data-never-publish" in read("dashboard/src/app/catalog/install-wizard.html")
        assert "never_publish" in read("dashboard/src/app/catalog/app-public.ts")


    def test_slice_doc_and_checklist(self):
        assert "never_publish" in read("docs/slices/S451-uptime-kuma-never-published.md")
        assert "S451" in read("docs/SLICE_INDEX.md")
        assert "never published (S451)" in read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")


if __name__ == "__main__":
    unittest.main()
