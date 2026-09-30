#!/usr/bin/env python3
"""把 govulncheck 的「模块级漏洞」显式暴露到 CI 日志里。

背景：`govulncheck ./...` 只在**代码真的调用到**漏洞代码时返回非零。
「你依赖的模块里有漏洞、但你没调用到」这类发现（Module Results 段）既不失败、
平时也不打印，于是**永远不会有人修**——2026-09-30 实测有 11 条这样躺在仓库里，
其中 x/image 一个人占了 5 条（WEBP/TIFF 解码）。

本脚本读 govulncheck 的 verbose 输出：
  * 逐条发出 ::warning:: 注解（有修复版的标出目标版本），让它在 Actions 摘要里可见
  * 打印一张汇总表
  * 默认退出 0（不可达的模块级漏洞不该阻断构建）
  * 加 --fail-on-fixable 时：只要存在**有修复版**的模块级漏洞就以非零退出，
    用于防止它们无限期堆积
"""

from __future__ import annotations

import argparse
import re
import sys

VULN_RE = re.compile(r"^Vulnerability #\d+:\s+(\S+)\s*$")
MODULE_RE = re.compile(r"^\s+Module:\s+(\S+)\s*$")
FOUND_RE = re.compile(r"^\s+Found in:\s+(\S+)\s*$")
FIXED_RE = re.compile(r"^\s+Fixed in:\s+(\S+)\s*$")


def normalize_fixed(value: str | None) -> str:
    """govulncheck 用 `N/A` 表示没有修复版——不能当成版本号。"""
    if not value:
        return ""
    text = value.strip()
    if text.lower() in {"n/a", "na", "none", "-", "(none)", "unknown"}:
        return ""
    return text


def parse_module_results(text: str) -> list[dict[str, str]]:
    """只取 `=== Module Results ===` 之后的内容。"""
    marker = "=== Module Results ==="
    start = text.find(marker)
    if start < 0:
        return []
    body = text[start + len(marker):]

    findings: list[dict[str, str]] = []
    current: dict[str, str] | None = None

    for raw in body.splitlines():
        line = raw.rstrip()
        if line.startswith("===") or "Your code is affected" in line:
            break
        m = VULN_RE.match(line)
        if m:
            if current:
                findings.append(current)
            current = {"id": m.group(1), "title": ""}
            continue
        if current is None:
            continue
        if not current["title"] and line.startswith("    ") and not line.startswith("  More info"):
            current["title"] = line.strip()
            continue
        for regex, key in ((MODULE_RE, "module"), (FOUND_RE, "found"), (FIXED_RE, "fixed")):
            m = regex.match(line)
            if m:
                current[key] = m.group(1)
                break
    if current:
        findings.append(current)
    for f in findings:
        f["fixed"] = normalize_fixed(f.get("fixed"))
    return findings


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--log", required=True, help="govulncheck -show verbose 的输出文件")
    parser.add_argument(
        "--fail-on-fixable",
        action="store_true",
        help="存在有修复版的模块级漏洞时以非零退出",
    )
    args = parser.parse_args()

    with open(args.log, "r", encoding="utf-8", errors="replace") as handle:
        text = handle.read()

    findings = parse_module_results(text)
    if not findings:
        print("No module-level vulnerabilities reported.")
        return 0

    fixable = [f for f in findings if f.get("fixed")]

    print(f"Module-level vulnerabilities: {len(findings)} ({len(fixable)} have a fix)")
    print("")
    print(f"{'ID':<16} {'MODULE':<34} {'FOUND':<40} {'FIXED':<40}")
    for f in findings:
        print(
            f"{f.get('id',''):<16} {f.get('module',''):<34} "
            f"{f.get('found',''):<40} {f.get('fixed','(none)') or '(none)':<40}"
        )

    # GitHub Actions 注解：让每条都出现在 job summary / PR 里，而不是埋在日志中。
    for f in findings:
        state = "has a fix available" if f.get("fixed") else "no fix available"
        print(
            f"::warning title=govulncheck module finding::{f.get('id','')} "
            f"{f.get('title','')} [{f.get('module','')} {f.get('found','')}] — {state}"
            + (f"; upgrade to {f['fixed']}" if f.get("fixed") else "")
        )

    if args.fail_on_fixable and fixable:
        print("")
        print(
            f"ERROR: {len(fixable)} module-level vulnerabilities have a fix available. "
            "These are not reachable from our code, so the scan stays green — which is "
            "exactly why they accumulate. Upgrade them.",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
