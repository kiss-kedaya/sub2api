#!/usr/bin/env python3
"""Prime GOCACHE by compiling (never running) the tests a workflow runs.

Every `go test` line in the selected jobs of a workflow file becomes a
compile-only twin, so the real `go test -p 1 ...` step that follows starts with
compile, vet and dependency work already done at full parallelism.

  * -p, -run, -count, -timeout are dropped; `-exec true -count=1` is added, so
    the test binary is built, vetted and linked but never started
  * -race and -tags are kept, because they change the build action ids
  * lines with the same flag set are merged into one invocation
  * `make <target>` lines are expanded from backend/Makefile

The real test commands are not modified. Failures only print a warning: warming
is best-effort and this script always exits 0.
"""
import argparse
import os
import re
import shlex
import subprocess
import sys
import time

JOB_RE = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
RUN_RE = re.compile(r"^(\s+)((?:- )?)run:\s*(.*)$")
DROP_VALUE = {"-p", "-run", "-count", "-timeout", "-parallel", "-cpu", "-exec", "-o"}


def workflow_commands(path, jobs):
    cur, in_jobs, key_col = None, False, None
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    for raw in lines:
        if raw.startswith("jobs:"):
            in_jobs = True
            continue
        if not in_jobs:
            continue
        m = JOB_RE.match(raw)
        if m:
            cur, key_col = m.group(1), None
            continue
        if cur is None or (jobs and cur not in jobs):
            continue
        if key_col is not None:
            if not raw.strip():
                continue
            if len(raw) - len(raw.lstrip(" ")) > key_col:
                yield raw.strip()
                continue
            key_col = None
        m = RUN_RE.match(raw)
        if m:
            rest = m.group(3).strip()
            if rest in ("|", "|-", ">", ">-"):
                key_col = len(m.group(1)) + len(m.group(2))
            elif rest:
                yield rest


def make_recipe(target, makefile):
    out, on = [], False
    with open(makefile, encoding="utf-8") as fh:
        for line in fh.read().split("\n"):
            if re.match(rf"^{re.escape(target)}:", line):
                on = True
            elif on and line.startswith("\t"):
                out.append(line.strip().lstrip("@-"))
            elif on:
                break
    return out


def split_flags(tokens):
    race, tags, trim, pkgs, extra, i = False, None, False, [], [], 0
    while i < len(tokens):
        t = tokens[i]
        if t == "-race":
            race = True
        elif t == "-trimpath":
            trim = True
        elif t.startswith("-tags="):
            tags = t[len("-tags="):]
        elif t == "-tags" and i + 1 < len(tokens):
            tags, i = tokens[i + 1], i + 1
        elif t in DROP_VALUE:
            i += 1
        elif re.match(r"^-(p|run|count|timeout|parallel|cpu|exec|ldflags|o)=", t) or t == "-ldflags":
            i += 2 if t == "-ldflags" else 0
        elif t.startswith("-"):
            extra.append(t)
        else:
            pkgs.append(t)
        i += 1
    return race, tags, trim, tuple(extra), pkgs


def parse(cmdline, makefile, depth=0):
    try:
        toks = shlex.split(cmdline)
    except ValueError:
        return []
    env = {}
    while toks and re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", toks[0]):
        k, v = toks.pop(0).split("=", 1)
        env[k] = v
    if toks[:1] == ["make"] and len(toks) > 1 and depth == 0:
        return [c for line in make_recipe(toks[1], makefile) for c in parse(line, makefile, 1)]
    if toks[:2] not in (["go", "test"], ["go", "build"]) or "-c" in toks:
        return []
    kind = toks[1]
    race, tags, trim, extra, pkgs = split_flags(toks[2:])
    return [(kind, race, tags, trim, extra, tuple(sorted(env.items())), pkgs)]


def merge(parsed):
    merged = {}
    for kind, race, tags, trim, extra, env, pkgs in parsed:
        bucket = merged.setdefault((kind, race, tags, trim, extra, env), [])
        bucket.extend(p for p in pkgs if p not in bucket)
    cmds = []
    for (kind, race, tags, trim, extra, env), pkgs in sorted(merged.items(), key=lambda kv: (kv[0][0] != "test", str(kv[0]))):
        argv = ["go", kind]
        argv += ["-race"] if race else []
        argv += [f"-tags={tags}"] if tags else []
        argv += ["-trimpath"] if trim else []
        argv += list(extra)
        argv += ["-exec", "true", "-count=1"] if kind == "test" else ["-o", os.devnull]
        cmds.append((argv + pkgs, dict(env)))
    return cmds


def run(cmds, cwd, ag_dir, limit):
    base = os.environ.get("GOFLAGS", "")
    for n, (argv, env) in enumerate(cmds, 1):
        e = dict(os.environ, **env)
        if ag_dir:
            e["GOFLAGS"] = f"{base} -debug-actiongraph={ag_dir}/warm-{n:02d}.json".strip()
        print(f"::group::warm {n}/{len(cmds)}: {shlex.join(argv)}", flush=True)
        t0 = time.time()
        try:
            rc = subprocess.run(argv, cwd=cwd, env=e, timeout=limit).returncode
        except subprocess.TimeoutExpired:
            rc = 124
        print("::endgroup::", flush=True)
        print(f"warm {n}/{len(cmds)}: rc={rc} {time.time() - t0:.1f}s", flush=True)
        if rc:
            print(f"::warning::cache warm step {n} exited {rc}; priming is best-effort", flush=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--workflow", required=True)
    ap.add_argument("--jobs", default="", help="comma separated job ids (default: all)")
    ap.add_argument("--makefile", default="backend/Makefile")
    ap.add_argument("--cwd", default="backend")
    ap.add_argument("--extra", action="append", default=[], help="extra command run verbatim")
    ap.add_argument("--ag-dir", default="")
    ap.add_argument("--timeout", type=int, default=1800, help="seconds per command")
    ap.add_argument("--print", dest="show", action="store_true")
    a = ap.parse_args()
    jobs = {j for j in a.jobs.split(",") if j}
    parsed = [p for c in workflow_commands(a.workflow, jobs) for p in parse(c, a.makefile)]
    cmds = merge(parsed) + [(shlex.split(x), {}) for x in a.extra]
    if a.show:
        for argv, env in cmds:
            print(" ".join([f"{k}={v}" for k, v in env.items()] + [shlex.join(argv)]))
        return 0
    if a.ag_dir:
        os.makedirs(a.ag_dir, exist_ok=True)
    run(cmds, a.cwd, a.ag_dir, a.timeout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
