#!/usr/bin/env bash
# Prepare code and dependencies only. Does not log in, install a unit or restart services.
set -euo pipefail
target=${1:?Usage: prepare.sh /absolute/adapter/directory}
revision=ad23ce2db3b5212c0355762d981c3877322fb160
case "$target" in /*) ;; *) echo 'An absolute adapter path is required' >&2; exit 1;; esac
if [ ! -e "$target" ]; then
  git clone https://github.com/Nonary/ghcp_proxy.git "$target"
fi
test "$(git -C "$target" remote get-url origin)" = 'https://github.com/Nonary/ghcp_proxy.git'
test -z "$(git -C "$target" status --porcelain --untracked-files=no)"
git -C "$target" fetch origin "$revision"
git -C "$target" checkout --detach "$revision"
if [ ! -x "$target/.venv/bin/python" ]; then
  python3 -m venv "$target/.venv"
fi
"$target/.venv/bin/python" -m pip install -r "$target/requirements.txt" 'github-copilot-sdk==1.0.14'
echo "Prepared pinned adapter at $target; login and service activation are separate steps."
