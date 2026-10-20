#!/usr/bin/env python3
"""Reject staged changes that carry operator-private data.

This repository is public. Docs, release notes and code must never reveal which
upstream providers, accounts, keys or hosts the operator actually runs.

Design: high-confidence only. A noisy check gets bypassed with --no-verify and
stops protecting anything. Credentials are scanned everywhere; infrastructure
identifiers (hosts, IPs, relay domains) are scanned in prose/config, not in
test fixtures which are full of fake values by construction.

Usage:
    python tools/check_sensitive.py          # staged changes (pre-commit)
    python tools/check_sensitive.py --all    # every tracked file
"""

import io
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
]

IPV4_RE = re.compile(r"(?<![\w./-])(?:\d{1,3}\.){3}\d{1,3}(?!\w)(?!/\d)")

# Public resolvers are documentation, not operator infrastructure.
ALLOWED_IPS = {"8.8.8.8", "8.8.4.4", "1.1.1.1", "9.9.9.9", "4.4.4.4",
               "223.5.5.5", "119.29.29.29", "114.114.114.114", "169.254.169.254"}

DOC_IP_PREFIXES = ("192.0.2.", "198.51.100.", "203.0.113.", "1.2.3.4")

# Files that legitimately contain infrastructure detail, or are fixtures.
SKIP_PATHS = (
    "tools/check_sensitive.py",
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


def _git(args):
    return subprocess.run(args, capture_output=True).stdout.decode("utf-8", "replace")


def staged_files():
    out = _git(["git", "diff", "--cached", "--name-only", "--diff-filter=ACMRT"])
    return [f for f in out.splitlines() if f.strip()]


def tracked_files():
    return [f for f in _git(["git", "ls-files"]).splitlines() if f.strip()]


def staged_content(path):
    return _git(["git", "show", ":" + path])


def disk_content(path):
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except OSError:
        return ""


def skipped(path):
    p = path.replace("\\", "/")
    return any(part in p for part in SKIP_PATHS)


def scan(path, text):
    problems = []
    prose = is_prose(path)
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
    scan_all = "--all" in sys.argv
    files = tracked_files() if scan_all else staged_files()
    if not files:
        print("check_sensitive: nothing to scan")
        return 0

    binary_ext = (".png", ".jpg", ".jpeg", ".gif", ".zip", ".gz", ".pdf",
                  ".ico", ".woff", ".woff2", ".ttf", ".db", ".sqlite", ".svg")
    problems = []
    for path in files:
        if path.endswith(binary_ext) or skipped(path):
            continue
        text = disk_content(path) if scan_all else staged_content(path)
        if not text:
            continue
        problems.extend(scan(path, text))

    if not problems:
        print("check_sensitive: OK ({} files)".format(len(files)))
        return 0

    print("{}check_sensitive: BLOCKED{}".format(RED, RESET))
    print("These lines carry operator-private data and must not be published:")
    for path, lineno, label, value, ctx in problems:
        print("  {}{}:{}:{} {} -> {}{}".format(
            YELLOW, path, lineno, RESET, label, value, RESET))
        print("      {}".format(ctx))
    print()
    print("Replace real hosts, account ids, keys and private IPs with neutral"
          " placeholders before committing.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
