"""Canaries for scanner diagnostics; fixtures stay in captured child processes."""

import json
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest
import runpy
import shutil


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
runtime["fixture_baseline"] = lambda _scan_all: {}
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

    def test_placeholders_remain_allowed_but_test_paths_do_not_exempt_credentials(self):
        key = "sk-" + "A1b2C3d4" * 4
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                result = self.run_scanner({"docs/canary.md": "sk-" + "placeholder" * 4}, mode)
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stderr, "")
                result = self.run_scanner({"backend/canary_test.go": key}, mode)
                self.assertEqual(result.returncode, 1, "test files must scan credentials")
                self.assertTrue("api key" in result.stdout)
                self.assertTrue(key not in result.stdout + result.stderr)

    def test_fixture_infrastructure_exclusion_does_not_skip_secrets(self):
        key = "sk-" + "A1b2C3d4" * 4
        for mode in ("staged", "all"):
            with self.subTest(mode=mode):
                path = "backend/testdata/config.json"
                result = self.run_scanner({path: "base_url=https://fixture-relay.invalid"}, mode)
                self.assertEqual(result.returncode, 0)
                result = self.run_scanner({path: key}, mode)
                self.assertEqual(result.returncode, 1, "fixture secret must not be skipped")
                self.assertTrue(key not in result.stdout + result.stderr)

    def test_binary_extension_does_not_skip_credential_text(self):
        key = "sk-" + "A1b2C3d4" * 4
        result = self.run_scanner({"assets/canary.svg": "<svg>" + key + "</svg>"}, "all")
        self.assertEqual(result.returncode, 1)
        self.assertTrue(key not in result.stdout + result.stderr)


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

    def test_range_checks_sensitive_middle_message_after_safe_head(self):
        base = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        private_path = "C:/Users/private-middle-canary/project"
        self.run_git("commit", "--quiet", "--allow-empty", "-m",
                     "fix: source " + private_path + " " + CANARY_CONTEXT)
        leaked_sha = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe final commit")
        safe_sha = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        self.assertEqual(self.run_cli("--metadata", "HEAD").returncode, 0)
        result = self.run_cli("--metadata-range", base + "..HEAD")
        self.assert_private_blocked(result, "personal Windows profile path",
                                    (private_path, CANARY_CONTEXT))
        self.assertIn(leaked_sha, result.stdout, "missing offending commit SHA")
        self.assertNotIn(safe_sha, result.stdout, "safe head should not be flagged")

    def test_range_checks_middle_commit_identity(self):
        base = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        private_email = "987654321" + "@qq.com"
        environment = self.environment | {"GIT_AUTHOR_EMAIL": private_email}
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe prose",
                     environment=environment)
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe head")
        self.assertEqual(self.run_cli("--metadata", "HEAD").returncode, 0)
        self.assert_private_blocked(self.run_cli("--metadata-range", base + "..HEAD"),
                                    "personal mailbox", (private_email,))

    def test_range_excludes_existing_ancestor_metadata(self):
        private_path = "C:/Users/private-existing-canary/project"
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "old prose " + private_path)
        base = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe new commit")
        result = self.run_cli("--metadata-range", base + "..HEAD")
        self.assertEqual(result.returncode, 0, "range must not walk unrelated ancestor history")
        self.assertNotIn(private_path, result.stdout + result.stderr)

    def test_range_argument_and_git_read_failures_fail_closed_without_values(self):
        private_ref = "C:/Users/private-invalid-canary/project"
        for value in ("", "HEAD", "..HEAD", "HEAD..", "HEAD...HEAD", "HEAD..HEAD..HEAD",
                      private_ref + "..HEAD"):
            with self.subTest(case="invalid range"):
                result = self.run_cli("--metadata-range=" + value)
                self.assertEqual(result.returncode, 2, "invalid range must fail closed")
                self.assertNotIn(private_ref, result.stdout + result.stderr)
                self.assertEqual(result.stderr, "")
        result = self.run_cli("--metadata-range", "HEAD..HEAD")
        self.assertEqual(result.returncode, 0, "empty explicit range is valid")

    def test_message_file_checks_proposed_body_with_empty_index(self):
        private_path = "C:/Users/private-message-canary/project"
        message = self.repo / "proposed-message"
        message.write_text("fix: safe subject\n\n" + private_path + " " + CANARY_CONTEXT,
                           encoding="utf-8")
        self.assert_private_blocked(self.run_cli("--message-file", str(message)),
                                    "personal Windows profile path", (private_path, CANARY_CONTEXT))
        message.write_text("fix: safe subject\n\nsafe body", encoding="utf-8")
        self.assertEqual(self.run_cli("--message-file", str(message)).returncode, 0)

    def test_message_file_io_and_decoding_failures_fail_closed(self):
        invalid = self.repo / "invalid-message"
        invalid.write_bytes(b"\xff\xfe")
        missing = self.repo / "private-missing-message"
        for value in ("", str(missing), str(self.repo), str(invalid)):
            with self.subTest(case="unreadable message"):
                result = self.run_cli("--message-file=" + value)
                self.assertEqual(result.returncode, 2, "unreadable message must fail closed")
                self.assertNotIn(str(self.repo), result.stdout + result.stderr)
                self.assertEqual(result.stderr, "")

    def test_git_io_failure_is_not_treated_as_empty_scan(self):
        environment = self.environment | {"GIT_DIR": str(self.repo / "missing-git-private-path")}
        result = self.run_cli("--identity", environment=environment)
        self.assertEqual(result.returncode, 2, "Git read failure must fail closed")
        self.assertNotIn("missing-git-private-path", result.stdout + result.stderr)
        self.assertEqual(result.stderr, "")

    def test_real_commit_message_hook_rejects_sensitive_empty_commit(self):
        (self.repo / "tools").mkdir()
        shutil.copyfile(SCANNER, self.repo / "tools" / "check_sensitive.py")
        hooks = self.repo / "hooks"
        hooks.mkdir()
        hook = hooks / "commit-msg"
        shutil.copyfile(SCANNER.parent.parent / ".githooks" / "commit-msg", hook)
        hook.chmod(0o755)
        self.run_git("config", "core.hooksPath", str(hooks))
        before = self.run_git("rev-parse", "HEAD").stdout
        private_path = "C:/Users/private-hook-canary/project"
        environment = self.environment | {
            "PATH": str(Path(sys.executable).parent) + os.pathsep + self.environment.get("PATH", ""),
        }
        result = subprocess.run(
            ["git", "commit", "--quiet", "--allow-empty", "-m", "fix: safe subject",
             "-m", private_path + " " + CANARY_CONTEXT],
            cwd=self.repo, env=environment, capture_output=True, text=True,
            encoding="utf-8", timeout=10, check=False,
        )
        self.assert_private_blocked(result, "personal Windows profile path",
                                    (private_path, CANARY_CONTEXT))
        self.assertEqual(before, self.run_git("rev-parse", "HEAD").stdout,
                         "rejected hook must not create a commit")
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe allowed commit",
                     environment=environment)

    def test_workflow_uses_event_range_without_scanning_old_history(self):
        # Execute the actual embedded workflow runner against real commits,
        # rather than mirroring its event-selection conditions in this test.
        workflow = (SCANNER.parent.parent / ".github" / "workflows" / "sensitive-check.yml").read_text()
        runner = textwrap.dedent(workflow.split("python - <<'PY'\n", 1)[1].split("\n          PY", 1)[0])
        (self.repo / "tools").mkdir()
        shutil.copyfile(SCANNER, self.repo / "tools" / "check_sensitive.py")
        base = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        private_path = "C:/Users/private-workflow-canary/project"
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "old source " + private_path)
        self.run_git("commit", "--quiet", "--allow-empty", "-m", "fix: safe final commit")
        head = self.run_git("rev-parse", "HEAD").stdout.decode().strip()
        cases = (
            ("push", {"before": base, "after": head}, 1),
            ("pull_request", {"pull_request": {"base": {"sha": base}, "head": {"sha": head}}}, 1),
            ("push", {"before": "0" * 40, "after": head}, 0),
            ("workflow_dispatch", {}, 0),
        )
        for event_name, event, expected in cases:
            with self.subTest(event_name=event_name, expected=expected):
                event_path = self.repo / "workflow-event.json"
                event_path.write_text(json.dumps(event), encoding="utf-8")
                environment = self.environment | {
                    "GITHUB_EVENT_PATH": str(event_path), "GITHUB_EVENT_NAME": event_name,
                }
                result = subprocess.run(
                    [sys.executable, "-B", "-c", runner], cwd=self.repo, env=environment,
                    capture_output=True, text=True, encoding="utf-8", timeout=10, check=False,
                )
                self.assertEqual(result.returncode, expected, "wrong metadata scan scope")
                self.assertNotIn(private_path, result.stdout + result.stderr)

    def install_audited_fixture(self, path="backend/internal/service/audit_log_test.go"):
        baseline = json.loads((SCANNER.parent.parent / ".github" / "privacy-fixture-allowlist.json").read_text())
        entry = next(item for item in baseline["fixtures"] if item["path"] == path)
        source = SCANNER.parent.parent / path
        self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(), entry["sha256"],
                         "audited fixture changed and needs baseline review")
        fixture = self.repo / path
        fixture.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, fixture)
        baseline_path = self.repo / ".github" / "privacy-fixture-allowlist.json"
        baseline_path.parent.mkdir(exist_ok=True)
        baseline_path.write_text(json.dumps({"version": 1, "fixtures": [entry]}), encoding="utf-8")
        self.run_git("add", "--", path, ".github/privacy-fixture-allowlist.json")
        return fixture, baseline_path

    def test_audited_fixture_exact_hash_passes_disk_and_staged_scans(self):
        self.install_audited_fixture()
        for arguments in ((), ("--all",)):
            with self.subTest(arguments=arguments):
                result = self.run_cli(*arguments)
                self.assertEqual(result.returncode, 0, "reviewed exact fixture should pass")
                self.assertEqual(result.stderr, "")

    def test_modified_fixture_invalidates_baseline_for_even_safe_byte_change(self):
        fixture, _baseline_path = self.install_audited_fixture()
        fixture.write_bytes(fixture.read_bytes() + b"\n// harmless change still requires review\n")
        result = self.run_cli("--all")
        self.assertEqual(result.returncode, 1, "any change must invalidate fixture allowance")
        self.assertEqual(self.run_cli().returncode, 0, "staged original bytes should retain allowance")
        self.run_git("add", "--", str(fixture.relative_to(self.repo)))
        self.assertEqual(self.run_cli().returncode, 1, "changed index bytes need fresh audit")

    def test_injected_token_in_existing_fixture_is_blocked_in_both_snapshots(self):
        fixture, baseline_path = self.install_audited_fixture()
        token = "Bearer " + "A1b2C3d4" * 5
        fixture.write_bytes(fixture.read_bytes() + ("\n// " + token + "\n").encode())
        result = self.run_cli("--all")
        self.assert_private_blocked(result, "bearer token", (token, token[-12:]))
        self.assertEqual(self.run_cli().returncode, 0, "unstaged file cannot change index scan")
        self.run_git("add", "--", str(fixture.relative_to(self.repo)))
        self.assert_private_blocked(self.run_cli(), "bearer token", (token, token[-12:]))
        # An unstaged forged allowlist must not grant the staged file an exception.
        baseline = json.loads(baseline_path.read_text())
        baseline["fixtures"][0]["sha256"] = hashlib.sha256(fixture.read_bytes()).hexdigest()
        baseline_path.write_text(json.dumps(baseline), encoding="utf-8")
        self.assert_private_blocked(self.run_cli(), "bearer token", (token, token[-12:]))

    def test_new_test_file_with_same_audited_bytes_is_not_allowed(self):
        fixture, _baseline_path = self.install_audited_fixture()
        new_file = fixture.with_name("new_unreviewed_test.go")
        shutil.copyfile(fixture, new_file)
        self.run_git("add", "--", str(new_file.relative_to(self.repo)))
        for arguments in ((), ("--all",)):
            with self.subTest(arguments=arguments):
                result = self.run_cli(*arguments)
                self.assertEqual(result.returncode, 1, "path and hash must both match audit")
                self.assertTrue("api key" in result.stdout)

    def test_new_test_file_with_random_shaped_key_is_blocked(self):
        fixture, _baseline_path = self.install_audited_fixture()
        new_file = fixture.with_name("new_credential_test.go")
        key = "sk-" + "Q7r8S9t0U1v2W3z4" * 3
        new_file.write_text('package service\nvar credential = "' + key + '"\n', encoding="utf-8")
        self.run_git("add", "--", str(new_file.relative_to(self.repo)))
        for arguments in ((), ("--all",)):
            with self.subTest(arguments=arguments):
                self.assert_private_blocked(self.run_cli(*arguments), "api key", (key, key[-12:]))

    def test_new_test_file_cannot_use_an_audited_private_key_marker(self):
        fixture, _baseline_path = self.install_audited_fixture(
            "backend/internal/payment/provider/wxpay_test.go")
        new_file = fixture.with_name("new_private_key_test.go")
        marker = "-----BEGIN " + "PRIVATE KEY-----"
        new_file.write_text("package provider\n// " + marker, encoding="utf-8")
        self.run_git("add", "--", str(new_file.relative_to(self.repo)))
        for arguments in ((), ("--all",)):
            with self.subTest(arguments=arguments):
                self.assert_private_blocked(self.run_cli(*arguments), "private key", (marker,))

    def test_fixture_baseline_io_and_schema_errors_fail_closed(self):
        _fixture, baseline_path = self.install_audited_fixture()
        for content in ("not json", "[]", '{"version":2,"fixtures":[]}',
                        '{"version":1,"fixtures":[{"path":"backend/canary_test.go"}]}'):
            with self.subTest(case="invalid fixture audit"):
                baseline_path.write_text(content, encoding="utf-8")
                result = self.run_cli("--all")
                self.assertEqual(result.returncode, 2, "broken audit must fail closed")
                self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
