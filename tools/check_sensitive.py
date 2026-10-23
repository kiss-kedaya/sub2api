#!/usr/bin/env python3
"""Reject staged changes that carry operator-private data.

This repository is public. Docs, release notes and code must never reveal which
upstream providers, accounts, keys or hosts the operator actually runs.

Design: high-confidence only. Credentials are scanned everywhere. Audited
synthetic fixtures require exact path and content hashes; infrastructure
identifiers (hosts, IPs, relay domains) are checked in prose/config.

Usage:
    python tools/check_sensitive.py          # staged changes (pre-commit)
    python tools/check_sensitive.py --all    # every tracked file
    python tools/check_sensitive.py --message-file PATH  # commit-msg hook
    python tools/check_sensitive.py --metadata-range BASE..HEAD  # new commits
"""

import io
import argparse
import hashlib
import json
from pathlib import PurePosixPath
import re
import subprocess
import sys

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding="utf-8", errors="replace")

RED = "\033[31m"
YELLOW = "\033[33m"
RESET = "\033[0m"

# 1. Real credentials. Never acceptable anywhere.
SECRET_PATTERNS = [
    ("api key", re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b")),
    ("anthropic key", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{20,}\b")),
    ("bearer token", re.compile(r"\bBearer\s+[A-Za-z0-9._-]{24,}\b")),
    ("private key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b")),
    ("aws key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
]

# 2. Operator identifiers. Scanned in prose and config only (see is_prose).
INFRA_PATTERNS = [
    ("account id reference", re.compile(r"(?:account|acct|\u8d26\u53f7)\s*#?\s*\d{4,6}\b", re.I)),
    ("ssh host alias", re.compile(r"\bkedaya-(?:old|new|friend|small|pay)\b", re.I)),
    ("ssh alias path", re.compile(r"\.ssh/kedaya-ops")),
    ("personal mailbox", re.compile(r"\b[1-9][0-9]{4,}@(qq|163|126)\.com\b", re.I)),
    ("personal Windows profile path", re.compile(
        r"\b[A-Z]:[\\/](?:Users|用户)[\\/](?!Public\b|Default\b|<|\$|example\b|your[-_]?user\b)[A-Za-z0-9_.@-]+", re.I)),
]

IPV4_RE = re.compile(r"(?<![\w./-])(?:\d{1,3}\.){3}\d{1,3}(?!\w)(?!/\d)")

# Public resolvers are documentation, not operator infrastructure.
ALLOWED_IPS = {"8.8.8.8", "8.8.4.4", "1.1.1.1", "9.9.9.9", "4.4.4.4",
               "223.5.5.5", "119.29.29.29", "114.114.114.114", "169.254.169.254"}

DOC_IP_PREFIXES = ("192.0.2.", "198.51.100.", "203.0.113.", "1.2.3.4")

# Infrastructure details in fixtures are not operator deployment evidence.
# These exclusions NEVER skip credentials; exact audited content hashes below
# are the only fixture exceptions to credential findings.
SKIP_PATHS = (
    ".golangci.yml",
    "_test.go",
    "/testdata/",
    "__tests__/",
    ".spec.",
    ".test.",
    "openspec/changes/",
    "frontend/dev/",
    ".patch",
)

FIXTURE_ALLOWLIST_PATH = ".github/privacy-fixture-allowlist.json"

# Documentation may quote public vendor endpoints; these are the vendors the
# product genuinely supports.
ALLOWED_DOMAINS = {
    "api.openai.com", "api.anthropic.com", "api.deepseek.com",
    "api.moonshot.cn", "open.bigmodel.cn", "openrouter.ai",
    "generativelanguage.googleapis.com", "api.x.ai",
    "cloudcode-pa.googleapis.com", "daily-cloudcode-pa.googleapis.com",
    "accounts.x.ai", "claude.ai", "cli-chat-proxy.grok.com",
    "api.typesafe.ai",
    "github.com", "raw.githubusercontent.com", "go.dev", "golang.org",
    "proxy.golang.org", "sum.golang.org", "pkg.go.dev",
    "registry.npmjs.org", "example.com", "example.org", "example.net",
    "localhost", "chanshui.dev",
    "ghcr.io", "docker.com", "entgo.io", "pnpm.io", "shields.io",
    "vuejs.org", "postgresql.org", "redis.io", "gorm.io",
    "google.cn", "sum.golang.google.cn",
}

# Third-party relay hosts: any domain that appears next to a credential-ish
# context is suspicious even if it is not in a known-vendor list.
RELAY_CONTEXT = re.compile(
    r"(?:base_url|baseURL|api_base|upstream|provider|relay|\u4e2d\u8f6c)\s*[:=]?\s*"
    r"\"?https?://([a-z0-9][a-z0-9.-]+\.[a-z]{2,})", re.I)

DOC_SUFFIXES = (".md", ".mdx", ".txt", ".rst", ".yml", ".yaml", ".json", ".env", ".example")
PROSE_DIRS = ("docs/", "docs\\", ".learnings/", ".learnings\\", "deploy/", "deploy\\")


def is_prose(path):
    p = path.replace("\\", "/")
    return p.startswith(PROSE_DIRS) or p.endswith(DOC_SUFFIXES)


def valid_ipv4(value):
    parts = value.split(".")
    if len(parts) != 4:
        return False
    for part in parts:
        if not part.isdigit() or len(part) > 3:
            return False
        if len(part) > 1 and part[0] == "0":
            return False
        if int(part) > 255:
            return False
    return True


def is_private_ip(value):
    parts = [int(p) for p in value.split(".")]
    a, b = parts[0], parts[1]
    if a in (0, 10, 127):
        return True
    if a == 172 and 16 <= b <= 31:
        return True
    if a == 192 and b == 168:
        return True
    if a == 169 and b == 254:
        return True
    if a == 100 and 64 <= b <= 127:
        return True
    return False


def is_documentation_ip(value):
    return (value.startswith(DOC_IP_PREFIXES)
            or value in ALLOWED_IPS
            or is_private_ip(value))


def is_placeholder_secret(value):
    body = value.lower()
    for marker in ("xxxx", "placeholder", "your-key", "demo", "not-valid",
                   "example", "dummy", "fake"):
        if marker in body:
            return True
    tail = re.sub(r"^[a-z-]+", "", body)
    return bool(tail) and set(tail) <= {"x", "0", "-", "_"}


def _git_bytes(args):
    # Never turn a failed Git read into a successful empty scan, or expose its
    # stderr (which may contain a private path, ref, or commit message).
    try:
        result = subprocess.run(args, capture_output=True, check=True)
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ValueError("unable to read Git data") from exc
    return result.stdout


def _git(args):
    return _git_bytes(args).decode("utf-8", "replace")


def staged_files():
    out = _git(["git", "diff", "--cached", "--name-only", "--diff-filter=ACMRT"])
    return [f for f in out.splitlines() if f.strip()]


def tracked_files():
    return [f for f in _git(["git", "ls-files"]).splitlines() if f.strip()]


def staged_content(path):
    return _git_bytes(["git", "show", ":" + path])


def disk_content(path):
    try:
        with open(path, "rb") as fh:
            return fh.read()
    except OSError as exc:
        raise ValueError("unable to read tracked content") from exc


def skipped(path):
    p = path.replace("\\", "/")
    return any(part in p for part in SKIP_PATHS)


def fixture_baseline(scan_all):
    # Read the same snapshot as the scanned files. Unstaged allowlist edits
    # cannot authorize index content, or invalidate a staged content hash.
    if scan_all:
        try:
            with open(FIXTURE_ALLOWLIST_PATH, "rb") as fh:
                raw = fh.read()
        except FileNotFoundError:
            return {}
        except OSError as exc:
            raise ValueError("unable to read fixture baseline") from exc
    else:
        tracked = _git(["git", "ls-files", "--", FIXTURE_ALLOWLIST_PATH]).splitlines()
        if FIXTURE_ALLOWLIST_PATH not in tracked:
            return {}
        raw = staged_content(FIXTURE_ALLOWLIST_PATH)
    try:
        baseline = json.loads(raw)
        if baseline["version"] != 1 or not isinstance(baseline["fixtures"], list):
            raise ValueError("invalid fixture baseline")
        allowed = {}
        for entry in baseline["fixtures"]:
            path, digest, reason = entry["path"], entry["sha256"], entry["reason"]
            if (not isinstance(path, str) or not skipped(path)
                    or PurePosixPath(path).is_absolute() or ".." in PurePosixPath(path).parts
                    or "\\" in path or path != str(PurePosixPath(path))
                    or path in allowed or not isinstance(digest, str)
                    or not re.fullmatch(r"[0-9a-f]{64}", digest)
                    or not isinstance(reason, str) or not reason.strip()):
                raise ValueError("invalid fixture baseline entry")
            allowed[path] = digest
        return allowed
    except (KeyError, TypeError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError("invalid fixture baseline") from exc


def metadata_privacy_problems(identity, message, source="commit"):
    """Check our publishing identity and commit prose without printing values."""
    problems = []
    for label, pattern in INFRA_PATTERNS:
        if label == "personal mailbox" and pattern.search(identity):
            problems.append(("<{}-identity>".format(source), 1, label, "", ""))
    problems.extend(scan("<{}-message>.md".format(source), message))
    return problems


def publishing_metadata():
    # New commits use the effective Git identity, including environment overrides.
    identity = _git(["git", "var", "GIT_AUTHOR_IDENT"]) + _git(["git", "var", "GIT_COMMITTER_IDENT"])
    return metadata_privacy_problems(identity, "")


def resolve_commit(ref):
    sha = _git(["git", "rev-parse", "--verify", "--end-of-options", ref + "^{commit}"]).strip()
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", sha):
        raise ValueError("unable to resolve commit")
    return sha


def commit_metadata(ref):
    sha = resolve_commit(ref)
    raw = _git(["git", "show", "-s", "--format=%ae%x00%ce%x00%B", sha, "--"])
    fields = raw.split("\x00", 2)
    if len(fields) != 3:
        raise ValueError("unable to read commit metadata")
    return metadata_privacy_problems("\n".join(fields[:2]), fields[2], sha)


def range_metadata(revision_range):
    # Require both endpoints. A bare revision would walk upstream history and
    # turn unrelated historical metadata into false positives.
    if revision_range.count("..") != 1 or "..." in revision_range:
        raise ValueError("metadata range requires BASE..HEAD")
    base, head = revision_range.split("..")
    if not base or not head:
        raise ValueError("metadata range requires BASE..HEAD")
    base_sha, head_sha = resolve_commit(base), resolve_commit(head)
    commits = _git(["git", "rev-list", "--reverse", base_sha + ".." + head_sha, "--"]).splitlines()
    problems = []
    for sha in commits:
        problems.extend(commit_metadata(sha))
    return problems


def message_file_metadata(path):
    # commit-msg runs even for --allow-empty commits. Missing, unreadable or
    # malformed message files must reject the commit instead of skipping it.
    try:
        with open(path, encoding="utf-8") as fh:
            message = fh.read()
    except (OSError, UnicodeError) as exc:
        raise ValueError("unable to read proposed commit message") from exc
    return metadata_privacy_problems("", message)


class SafeArgumentParser(argparse.ArgumentParser):
    def error(self, message):
        # argparse normally echoes arguments; never print a private input.
        raise ValueError("invalid scanner arguments")


def parse_args():
    parser = SafeArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true")
    parser.add_argument("--identity", action="store_true")
    parser.add_argument("--metadata", metavar="COMMIT")
    parser.add_argument("--metadata-range", metavar="BASE..HEAD")
    parser.add_argument("--message-file", metavar="PATH")
    return parser.parse_args()


def scan(path, text):
    problems = []
    prose = is_prose(path) and not skipped(path)
    for lineno, line in enumerate(text.splitlines(), 1):
        for label, pattern in SECRET_PATTERNS:
            for m in pattern.finditer(line):
                if is_placeholder_secret(m.group(0)):
                    continue
                problems.append((path, lineno, label, m.group(0), line.strip()[:120]))
        if not prose:
            continue
        for label, pattern in INFRA_PATTERNS:
            for m in pattern.finditer(line):
                problems.append((path, lineno, label, m.group(0), line.strip()[:120]))
        for m in IPV4_RE.finditer(line):
            value = m.group(0)
            if not valid_ipv4(value) or is_documentation_ip(value):
                continue
            problems.append((path, lineno, "public ip", value, line.strip()[:120]))
        for m in RELAY_CONTEXT.finditer(line):
            host = m.group(1).lower()
            if host in ALLOWED_DOMAINS or host.endswith(".example.com"):
                continue
            if any(host.endswith("." + d) for d in ALLOWED_DOMAINS):
                continue
            problems.append((path, lineno, "third-party relay", host, line.strip()[:120]))
    return problems


def main():
    try:
        args = parse_args()
        return inspect(args)
    except ValueError:
        print("check_sensitive: unable to inspect requested files or metadata")
        return 2


def inspect(args):
    scan_all = args.all
    files = tracked_files() if scan_all else staged_files()
    problems = []
    if args.identity:
        problems.extend(publishing_metadata())
    if args.metadata is not None:
        problems.extend(commit_metadata(args.metadata))
    if args.metadata_range is not None:
        problems.extend(range_metadata(args.metadata_range))
    if args.message_file is not None:
        problems.extend(message_file_metadata(args.message_file))
    if not files and not problems:
        print("check_sensitive: nothing to scan")
        return 0
    baseline = fixture_baseline(scan_all)
    secret_labels = {label for label, _pattern in SECRET_PATTERNS}
    for path in files:
        # Scanner source necessarily contains its own regex examples. The
        # exception is exact; another test/source filename cannot inherit it.
        if path.replace("\\", "/") == "tools/check_sensitive.py":
            continue
        content = disk_content(path) if scan_all else staged_content(path)
        if not content:
            continue
        raw = content.encode("utf-8") if isinstance(content, str) else content
        findings = scan(path, raw.decode("utf-8", "replace"))
        if baseline.get(path) == hashlib.sha256(raw).hexdigest():
            findings = [finding for finding in findings if finding[2] not in secret_labels]
        problems.extend(findings)

    if not problems:
        print("check_sensitive: OK ({} files)".format(len(files)))
        return 0

    print("{}check_sensitive: BLOCKED{}".format(RED, RESET))
    print("These lines carry operator-private data and must not be published:")
    # CI logs can be public. Never echo matches or their source-line context.
    for path, lineno, label, _value, _ctx in problems:
        print("  {}{}:{}:{} {} -> [REDACTED]{}".format(
            YELLOW, path, lineno, RESET, label, RESET))
    print()
    print("Replace real hosts, account ids, keys and private IPs with neutral"
          " placeholders before committing.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
