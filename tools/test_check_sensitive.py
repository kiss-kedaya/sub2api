"""Canaries for scanner diagnostics; fixtures stay in captured child processes."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import runpy


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

    def test_personal_mailbox_and_profile_paths_are_redacted(self):
        values = (
            ("987654321" + "@qq.com", "personal mailbox"),
            ("E:" + chr(92) + "Users" + chr(92) + "private-person" + chr(92) + "Downloads", "personal Windows profile path"),
        )
        for mode in ("staged", "all"):
            for value, label in values:
                with self.subTest(mode=mode, rule=label):
                    self.assert_redacted(
                        self.run_scanner({"docs/canary.md": "heading\n" + value}, mode),
                        (value,), (label,),
                    )

    def test_public_identity_and_documentation_profile_placeholders_are_allowed(self):
        for value in (
            "12345+maintainer@users.noreply.github.com", "user@example.com",
            "C:/Users/<username>/project", "C:/Users/Public/project", "C:/Users/example/project",
        ):
            result = self.run_scanner({"docs/canary.md": value}, "all")
            self.assertEqual(result.returncode, 0, "public or placeholder identity was incorrectly rejected")

    def test_commit_metadata_checks_private_identity_and_message(self):
        runtime = runpy.run_path(str(SCANNER))
        check = runtime["metadata_privacy_problems"]
        self.assertTrue(check("987654321" + "@qq.com", "safe commit"))
        self.assertTrue(check("maintainer@users.noreply.github.com", "reference C:/Users/private-person/project"))
        self.assertFalse(check("maintainer@users.noreply.github.com", "fix: enforce authenticator checks"))

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


class EmptyCommitMetadataTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="sensitive-metadata-")
        self.addCleanup(self.directory.cleanup)
        self.repo = Path(self.directory.name)
        self.environment = os.environ.copy()
        # Keep the tests independent of the user's index, Git identity overrides,
        # hooks and signing configuration. All values below are synthetic.
        for name in tuple(self.environment):
            if name.startswith(("GIT_", "EMAIL")):
                self.environment.pop(name)
        self.environment.update({
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_AUTHOR_NAME": "Canary Maintainer",
            "GIT_AUTHOR_EMAIL": "maintainer@users.noreply.github.com",
            "GIT_COMMITTER_NAME": "Canary Maintainer",
            "GIT_COMMITTER_EMAIL": "maintainer@users.noreply.github.com",
        })
        self.run_git("init", "--quiet")
        self.run_git("config", "core.hooksPath", str(self.repo / "no-hooks"))
        self.run_git("config", "commit.gpgsign", "false")
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "safe initial commit")

    def run_git(self, *arguments, environment=None):
        result = subprocess.run(
            ["git", *arguments], cwd=self.repo,
            env=environment or self.environment,
            capture_output=True, timeout=10, check=False,
        )
        self.assertEqual(result.returncode, 0, "isolated Git fixture failed")
        return result

    def run_cli(self, *arguments, environment=None):
        return subprocess.run(
            [sys.executable, "-B", str(SCANNER.resolve()), *arguments],
            cwd=self.repo, env=environment or self.environment,
            capture_output=True, text=True, encoding="utf-8",
            timeout=10, check=False,
        )

    def assert_private_blocked(self, result, label, hidden):
        self.assertEqual(result.returncode, 1, "empty commit must be blocked")
        output = result.stdout + result.stderr
        self.assertTrue("BLOCKED" in output, "missing blocked diagnostic")
        self.assertTrue(label in output, "missing metadata rule")
        self.assertTrue("[REDACTED]" in output, "missing redaction marker")
        self.assertTrue(all(value not in output for value in hidden),
                        "metadata diagnostic exposed synthetic private values")

    def test_identity_checks_empty_index_with_effective_git_identity(self):
        private_email = "987654321" + "@qq.com"
        environment = self.environment | {"GIT_AUTHOR_EMAIL": private_email}
        result = self.run_cli("--identity", environment=environment)
        self.assert_private_blocked(result, "personal mailbox", (private_email,))

    def test_metadata_checks_private_identity_on_empty_commit(self):
        private_email = "987654321" + "@qq.com"
        environment = self.environment | {"GIT_COMMITTER_EMAIL": private_email}
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "safe commit",
                     environment=environment)
        self.assert_private_blocked(self.run_cli("--metadata", "HEAD"),
                                    "personal mailbox", (private_email,))

    def test_metadata_checks_sensitive_message_on_empty_commit(self):
        private_path = "C:/Users/private-canary-person/project"
        self.run_git("commit", "--quiet", "--allow-empty", "-m",
                     "fix: source " + private_path + " " + CANARY_CONTEXT)
        self.assert_private_blocked(
            self.run_cli("--metadata", "HEAD"),
            "personal Windows profile path", (private_path, CANARY_CONTEXT),
        )

    def test_metadata_argument_errors_are_reported_with_empty_index(self):
        for arguments in (("--metadata",), ("--metadata", "--identity"),
                          ("--metadata", "missing-canary-reference")):
            with self.subTest(arguments=arguments):
                result = self.run_cli(*arguments)
                self.assertEqual(result.returncode, 2, "bad metadata argument must fail")
                self.assertTrue("check_sensitive:" in result.stdout)
                self.assertEqual(result.stderr, "")

    def test_clean_empty_index_and_public_metadata_succeed(self):
        for arguments in ((), ("--all",), ("--identity",),
                          ("--metadata", "HEAD"), ("--identity", "--metadata", "HEAD")):
            with self.subTest(arguments=arguments):
                result = self.run_cli(*arguments)
                self.assertEqual(result.returncode, 0, "clean empty commit must succeed")
                self.assertTrue("nothing to scan" in result.stdout)
                self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
