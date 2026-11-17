"""External tools the tests need, and the switch that makes their absence fail.

HOMESTEAD_REQUIRE_TOOLS is a comma list (or "all"), as in the Go helper
internal/testtools; HOMESTEAD_REQUIRE_AGE=1 counts as naming age (S506).
"""
import os
import shutil
import unittest


def required(tool):
    if tool == "age" and os.environ.get("HOMESTEAD_REQUIRE_AGE"):
        return True
    names = [n.strip() for n in os.environ.get("HOMESTEAD_REQUIRE_TOOLS", "").split(",")]
    return "all" in names or tool in names


def have(tool):
    return shutil.which(tool) is not None


def require_tool(tool, available=None):
    """Decorator: skip when the tool is missing, unless it is required, in
    which case the test fails. `available` overrides the PATH lookup (node
    with node:sqlite)."""
    ok = have(tool) if available is None else available
    if ok:
        return lambda f: f
    if required(tool):
        def deco(f):
            if isinstance(f, type):
                class Missing(unittest.TestCase):
                    def test_tool_present(self):
                        self.fail(f"{tool} is required (HOMESTEAD_REQUIRE_TOOLS) but not usable")
                Missing.__name__ = f.__name__
                return Missing
            def fail(self, *a, **k):
                self.fail(f"{tool} is required (HOMESTEAD_REQUIRE_TOOLS) but not usable")
            fail.__name__ = f.__name__
            return fail
        return deco
    return unittest.skip(f"{tool} not available")
