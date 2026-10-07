"""S098 — Split DNS Reconciliation conformance tests.

Guardrails for multi-view DNS: baseline answers stand except where an
audience's view covers the same name/type key, view-less audiences fall
back to baseline, deletes hold for preview and audit unless explicitly
allowed while creates proceed, previews pair effective tables with
gated changes before anything mutates provider state, reconciliation
lists, previews, and applies the gated set while skipping empty
applies, and address records bridge to local-DNS rewrites with
non-answerable types staying out. Behavior is covered by Go tests in
internal/dns.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestReconcileContract(unittest.TestCase):
    def test_reconcile_shapes(self):
        src = read("internal/dns/reconcile.go")
        for symbol in ("func EffectiveRecords(", "func SplitChanges(",
                       "type Preview", "func PreviewZone(",
                       "type Summary", "func Reconcile(",
                       "func EffectiveRewrites("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_gate_promises(self):
        src = read("internal/dns/reconcile.go")
        for symbol in ("allowDeletes", "Held", "S091", "S227"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
