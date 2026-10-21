"""Canaries for scanner diagnostics; fixtures stay in captured child processes."""

import json
from pathlib import Path
import subprocess
import sys
import unittest


SCANNER = Path(__file__).with_name("check_sensitive.py")
CANARY_CONTEXT = "CANARY_SOURCE_CONTEXT_MUST_STAY_PRIVATE"

# Execute the real main/scan/output code, replacing only its Git/disk readers.
# No fixture, index, or bytecode files are written, and no real secrets are read.
CANARY_RUNNER = """
import json
import runpy
import sys

scanner_path, mode = sys.argv[1:]
fixtures = json.load(sys.stdin)
main = runpy.run_path(scanner_path)["main"]
runtime = main.__globals__
runtime["staged_files"] = runtime["tracked_files"] = lambda: list(fixtures)
runtime["staged_content"] = runtime["disk_content"] = fixtures.__getitem__
sys.argv = [scanner_path] + (["--all"] if mode == "all" else [])
raise SystemExit(main())
"""


class SensitiveOutputCanaryTests(unittest.TestCase):
    def run_scanner(self, fixtures, mode):
        return subprocess.run(
            [sys.executable, "-B", "-c", CANARY_RUNNER, str(SCANNER), mode],
            input=json.dumps(fixtures),
            capture_output=True,
            text=True,
            encoding="utf-8",
            cwd=SCANNER.parent.parent,
            timeout=10,
            check=False,
        )

    def assert_redacted(self, result, hidden, labels):
        # Never include captured output in assertion failures: the pre-fix
        # regression run must not reprint even these synthetic credentials.
        self.assertEqual(result.returncode, 1, "canary must block publication")
        output = result.stdout + result.stderr
        self.assertTrue(
            all(value not in output for value in hidden),
            "diagnostics exposed canary data",
        )
        self.assertTrue("[REDACTED]" in output, "missing redaction marker")
        self.assertTrue("BLOCKED" in output, "missing blocked diagnostic")
        self.assertTrue("docs/canary.md:2:" in output, "missing file/line")
        for label in labels:
            self.assertTrue(label in output, "missing rule label: " + label)

    def test_secret_patterns_are_redacted_in_both_modes(self):
        canaries = {
            "api key": "sk-" + "A1b2C3d4" * 4,
            "anthropic key": "sk-ant-" + "E5f6G7h8" * 4,
            "bearer token": "Bearer " + "I9j0K1l2" * 4,
            "private key": "-----BEGIN " + "RSA PRIVATE KEY-----",
            "jwt": "eyJ" + "M3n4O5p6" * 3 + "." + "Q7r8S9t0" * 3 + "." + "U1v2W3z4" * 3,
            "aws key": "AKIA" + "13579BDF" * 2,
        }
        for mode in ("staged", "all"):
            for label, value in canaries.items():
                with self.subTest(mode=mode, rule=label):
                    body = "safe heading\n" + CANARY_CONTEXT + " " + value + "\n"
                    hidden = (value, value[-12:], CANARY_CONTEXT)
                    self.assert_redacted(
                        self.run_scanner({"docs/canary.md": body}, mode),
                        hidden,
                        (label,),
                    )

    def test_multiple_matches_and_unmatched_context_are_hidden(self):
        key = "sk-" + "A1b2C3d4" * 4
        token = "Bearer " + "I9j0K1l2" * 4
        opaque = "CANARY_UNRECOGNIZED_CREDENTIAL"
        body = "safe heading\n" + " ".join((CANARY_CONTEXT, key, token, opaque))
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                self.assert_redacted(
                    self.run_scanner({"docs/canary.md": body}, mode),
                    (key, token, opaque, CANARY_CONTEXT),
                    ("api key", "bearer token"),
                )

    def test_infrastructure_match_does_not_echo_source_context(self):
        host = "canary-relay.invalid"
        body = "safe heading\nbase_url=https://" + host + " " + CANARY_CONTEXT
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                self.assert_redacted(
                    self.run_scanner({"docs/canary.md": body}, mode),
                    (host, CANARY_CONTEXT),
                    ("third-party relay",),
                )

    def test_clean_input_still_succeeds(self):
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                result = self.run_scanner({"docs/canary.md": "safe heading\n"}, mode)
                self.assertEqual(result.returncode, 0)
                self.assertTrue("check_sensitive: OK (1 files)" in result.stdout)
                self.assertEqual(result.stderr, "")

    def test_empty_input_still_succeeds(self):
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                result = self.run_scanner({}, mode)
                self.assertEqual(result.returncode, 0)
                self.assertTrue("nothing to scan" in result.stdout)
                self.assertEqual(result.stderr, "")

    def test_existing_placeholder_and_path_exclusions_are_preserved(self):
        fixtures = {
            "docs/canary.md": "sk-" + "placeholder" * 4,
            "backend/canary_test.go": "sk-" + "A1b2C3d4" * 4,
        }
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                result = self.run_scanner(fixtures, mode)
                self.assertEqual(result.returncode, 0)
                self.assertTrue("check_sensitive: OK (2 files)" in result.stdout)
                self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
