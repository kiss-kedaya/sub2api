"""Exercise real CLI audit validation without network access or repository writes."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


CHECKER = Path(__file__).with_name("check_pnpm_audit_exceptions.py")


def report(records=None, *, modern=False, high=0):
    return {
        "auditReportVersion": 2 if modern else 1,
        "vulnerabilities" if modern else "advisories": records or {},
        "metadata": {
            "vulnerabilities": {"info": 0, "low": 0, "moderate": 0, "high": high, "critical": 0},
            "dependencies": 1,
            "devDependencies": 0,
            "optionalDependencies": 0,
            "totalDependencies": 1,
        },
    }


class AuditReportValidationTests(unittest.TestCase):
    def check_report(self, payload, *, exception=False, raw=False):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            audit = root / "audit.json"
            audit.write_text(payload if raw else json.dumps(payload), encoding="utf-8")
            exceptions = root / "exceptions.yml"
            content = "version: 1\nexceptions:\n"
            if exception:
                content += (
                    "  - package: example-package\n"
                    "    advisory: GHSA-example-id\n"
                    "    severity: high\n"
                    "    mitigation: synthetic fixture\n"
                    "    expires_on: 2999-01-01\n"
                )
            exceptions.write_text(content, encoding="utf-8")
            return subprocess.run(
                [sys.executable, "-B", str(CHECKER), "--audit", str(audit), "--exceptions", str(exceptions)],
                capture_output=True, text=True, encoding="utf-8", check=False, timeout=10,
            )

    def assert_blocked(self, payload, *, raw=False):
        result = self.check_report(payload, raw=raw)
        self.assertEqual(result.returncode, 1)
        self.assertIn("Invalid audit report:", result.stderr)
        self.assertNotIn("Audit exceptions validated.", result.stdout)
        self.assertNotIn("Traceback", result.stderr)

    def test_valid_zero_vulnerability_reports_pass_in_both_formats(self):
        for modern in (False, True):
            with self.subTest(modern=modern):
                payload = report(modern=modern)
                if not modern:
                    payload.update(actions=[], muted=[])
                result = self.check_report(payload)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("Audit exceptions validated.", result.stdout)

    def test_registry_error_is_blocked_without_echoing_private_error_details(self):
        for payload in (
            {"error": {"code": "ERR_PNPM_AUDIT_ENDPOINT_NOT_EXISTS", "message": "PRIVATE_REGISTRY_DETAIL"}},
            {**report(), "error": {"message": "PRIVATE_REGISTRY_DETAIL"}},
            {**report(), "errors": ["PRIVATE_REGISTRY_DETAIL"]},
        ):
            with self.subTest(payload_keys=list(payload)):
                result = self.check_report(payload)
                self.assertEqual(result.returncode, 1)
                self.assertIn("Invalid audit report:", result.stderr)
                self.assertNotIn("PRIVATE_REGISTRY_DETAIL", result.stdout + result.stderr)

    def test_incomplete_or_invalid_report_shapes_are_blocked(self):
        cases = [None, [], {}, {"advisories": {}}, {"metadata": report()["metadata"]}]
        for field, value in (
            ("advisories", []),
            ("advisories", {"1": None}),
            ("advisories", {"1": {"module_name": "example-package", "severity": "high"}}),
            ("advisories", {"1": {"module_name": "example-package", "severity": "unknown", "id": 1}}),
            ("advisories", {"1": {"module_name": "example-package", "severity": "high", "cves": 123}}),
            ("metadata", {}),
        ):
            cases.append({**report(), field: value})
        for invalid_count in (-1, True, "0", None):
            payload = report()
            payload["metadata"]["vulnerabilities"]["critical"] = invalid_count
            cases.append(payload)
        for payload in cases:
            with self.subTest(payload=payload):
                self.assert_blocked(payload)

    def test_high_counts_cannot_hide_behind_empty_advisory_maps(self):
        for modern in (False, True):
            with self.subTest(modern=modern):
                self.assert_blocked(report(modern=modern, high=1))

    def test_unreadable_audit_json_does_not_become_an_empty_success(self):
        for raw in ("", "PRIVATE_INVALID_JSON", "{", '\ufeff{"advisories":'):
            with self.subTest(raw_length=len(raw)):
                self.assert_blocked(raw, raw=True)

    def test_valid_high_vulnerability_reports_still_require_matching_exception(self):
        legacy = report({"1": {
            "module_name": "example-package", "severity": "high", "github_advisory_id": "GHSA-example-id",
        }}, high=1)
        modern = report({"example-package": {
            "severity": "high", "via": [{"source": 123, "url": "GHSA-example-id", "title": "synthetic"}],
        }}, modern=True, high=1)
        for payload in (legacy, modern):
            with self.subTest(modern="vulnerabilities" in payload):
                blocked = self.check_report(payload)
                self.assertEqual(blocked.returncode, 1)
                self.assertIn("High/Critical vulnerabilities missing exceptions:", blocked.stderr)
                accepted = self.check_report(payload, exception=True)
                self.assertEqual(accepted.returncode, 0, accepted.stderr)

    def test_modern_records_need_usable_sources(self):
        for via in (None, [], {}, [None], [False], [{}], [""], [{"source": 1, "title": 123}]):
            with self.subTest(via=via):
                self.assert_blocked(report({"example-package": {"severity": "high", "via": via}}, modern=True, high=1))

    def test_null_error_field_and_utf8_bom_do_not_reject_a_valid_report(self):
        payload = {**report(), "error": None}
        result = self.check_report("\ufeff" + json.dumps(payload), raw=True)
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
