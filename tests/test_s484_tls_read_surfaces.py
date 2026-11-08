"""S484 - TLS approvals and local leaf certificates on the Certificates page."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TlsReadSurfaces(unittest.TestCase):
    def test_docs(self):
        self.assertIn("localCaLeaves", read("docs/slices/S484-tls-read-surfaces.md"))
        self.assertIn("S484 lists the recorded TLS", read("docs/SLICE_INDEX.md"))

    def test_page_calls_wrappers(self):
        ts = read("dashboard/src/app/tls/tls.ts")
        self.assertIn("this.api.tlsIssuanceApprovals()", ts)
        self.assertIn("this.api.localCaLeaves()", ts)
        html = read("dashboard/src/app/tls/tls.html")
        self.assertIn("No issuance approvals recorded.", html)
        self.assertIn("No local-only HTTPS routes use the internal CA.", html)

    def test_parity_rows(self):
        text = read("docs/PARITY.md")
        self.assertIn("`tlsIssuanceApprovals`", text)
        self.assertIn("(S484)", text)

    def test_checklist(self):
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("**END-PARITY-4 ", text)
        self.assertIn("| END-PARITY | END-PARITY-1 to END-PARITY-", text)
        self.assertIn("END-PARITY-4", read("docs/reviews/acceptance-form.html"))


if __name__ == "__main__":
    unittest.main()
