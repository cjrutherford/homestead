"""S360 — community-image trust policy conformance (ADR-056).

Behavior is covered by Go tests in internal/catalog (TestCuratedImageTrust,
TestServicePublisherValidation); these guard the documents and manifests.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ADR = REPO / "docs" / "adr" / "ADR-056-community-container-image-trust-policy.md"
LINUXSERVER_OK = {"prowlarr", "sonarr", "radarr", "sabnzbd", "bookstack"}


def services(text):
    """Yield (image, publisher, justification) per service block."""
    for block in re.split(r"(?m)^- name:", text)[1:]:
        image = re.search(r"(?m)^  image: (\S+)", block)
        if not image:
            continue
        publisher = re.search(r"(?m)^  publisher: (\S+)", block)
        justification = re.search(r"(?m)^  justification: (.+)", block)
        yield (image.group(1), publisher and publisher.group(1),
               justification and justification.group(1))


class TestImageTrust(unittest.TestCase):
    def test_adr_accepted_and_indexed(self):
        text = ADR.read_text(encoding="utf-8")
        self.assertEqual(text.split("## Status", 1)[1].split("\n")[1].strip(), "Accepted")
        for word in ("linuxserver", "community", "upstream", "multi-arch", "arm64", "digest"):
            self.assertIn(word, text)
        index = (REPO / "docs" / "adr" / "README.md").read_text(encoding="utf-8")
        self.assertIn("ADR-056", index)

    def test_plan_references_adr_056(self):
        row = [l for l in (REPO / "docs" / "SLICE_PLAN.md").read_text(encoding="utf-8").splitlines()
               if l.startswith("| S360 ")][0]
        self.assertIn("ADR-056", row)
        self.assertNotIn("ADR-035", row)

    def test_every_manifest_image_is_classified(self):
        seen = 0
        for path in sorted((REPO / "catalog").glob("*.yaml")):
            name = path.stem
            for image, publisher, justification in services(path.read_text(encoding="utf-8")):
                seen += 1
                where = f"{name}: {image}"
                self.assertNotRegex(image, r":latest$", where)
                if image.startswith(("homestead/craft-", "homestead/everything-")):
                    self.assertIsNone(publisher, where)
                    continue
                self.assertIn(publisher, ("upstream", "linuxserver", "community"), where)
                if publisher == "community":
                    self.assertTrue(justification and len(justification) >= 40, where)
                else:
                    self.assertIsNone(justification, where)
                if "linuxserver/" in image:
                    self.assertEqual(publisher, "linuxserver", where)
                    self.assertIn(name, LINUXSERVER_OK, where)
        self.assertGreater(seen, 30)

    def test_cwa_is_community(self):
        text = (REPO / "catalog" / "calibre-web-automated.yaml").read_text(encoding="utf-8")
        (image, publisher, justification), = list(services(text))
        self.assertIn("crocodilestick", image)
        self.assertEqual(publisher, "community")
        self.assertIn("ADR-056", justification)


if __name__ == "__main__":
    unittest.main()
