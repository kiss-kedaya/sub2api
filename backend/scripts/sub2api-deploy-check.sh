#!/usr/bin/env bash
#
# sub2api 部署一致性看门狗（主机层）
#
# 为什么在主机层而不是应用内：进程看不到自己的 systemd 单元名，而且它的沙箱
# （ProtectSystem=strict / ProtectHome=true）也读不到单元文件。而 2026-09-30
# 真实发生的故障恰恰是「单元名叫 sub2api-2.0.30-canary，ExecStart 却指向
# /opt/sub2api/sub2api-2.0.28」—— 40% 的流量在缺两版修复的二进制上跑了很久。
#
# 本脚本只读：不改单元、不重启服务、不动数据库。它做的事就是：
#   1) 找到主服务实际在跑的那个二进制（/proc/<pid>/exe，不是单元名！）
#   2) 和 EXPECTED_VERSION 比对
#   3) 把结论写成一个状态文件，交给应用侧的告警指标去发邮件
#
# 退出码：0 = 一致；1 = 不一致或无法判定（会进 journal，便于 systemd 层面可见）
#
# 安装位置：/usr/local/bin/sub2api-deploy-check.sh
# 由 systemd timer 周期调用，见 backend/scripts/systemd/。

set -uo pipefail

EXPECTED_VERSION_FILE="${EXPECTED_VERSION_FILE:-/opt/sub2api/EXPECTED_VERSION}"
STATUS_FILE="${STATUS_FILE:-/opt/sub2api/deploy-check.status}"
MAIN_PORT="${MAIN_PORT:-8228}"
APP_USER="${APP_USER:-sub2api}"
EXPECTED_PREFIX="${EXPECTED_PREFIX:-sub2api}"

log() { printf '%s %s\n' "$(date -Is)" "$*" >&2; }

write_status() {
  # $1 = version_mismatch (0/1), $2 = detail
  local mismatch="$1" detail="$2" tmp
  tmp="$(mktemp "${STATUS_FILE}.XXXXXX" 2>/dev/null)" || { log "cannot create temp status file"; return 1; }
  {
    printf 'version_mismatch=%s\n' "$mismatch"
    printf 'checked_at_unix=%s\n' "$(date +%s)"
    printf 'detail=%s\n' "$(printf '%s' "$detail" | tr '\n' ' ')"
  } > "$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$STATUS_FILE"
  # 让应用用户能读到（应用在 ProtectSystem=strict 下只被允许读 /opt/sub2api）
  chown "${APP_USER}:${APP_USER}" "$STATUS_FILE" 2>/dev/null || true
}

fail() {
  local detail="$1"
  log "MISMATCH: $detail"
  write_status 1 "$detail"
  exit 1
}

# --- 1. 期望版本 --------------------------------------------------------------
if [[ ! -r "$EXPECTED_VERSION_FILE" ]]; then
  fail "expected version file missing: $EXPECTED_VERSION_FILE"
fi
EXPECTED="$(tr -d '[:space:]' < "$EXPECTED_VERSION_FILE")"
if [[ -z "$EXPECTED" ]]; then
  fail "expected version file is empty: $EXPECTED_VERSION_FILE"
fi
EXPECTED_BIN="${EXPECTED_PREFIX}-${EXPECTED}"

# --- 2. 找到主服务实际在跑的进程 ----------------------------------------------
# 优先用监听端口定位：单元名可能撒谎，监听端口不会。
pid=""
if command -v ss >/dev/null 2>&1; then
  pid="$(ss -ltnpH "sport = :${MAIN_PORT}" 2>/dev/null \
    | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u | head -1)"
fi
if [[ -z "$pid" ]] && command -v lsof >/dev/null 2>&1; then
  pid="$(lsof -tiTCP:"${MAIN_PORT}" -sTCP:LISTEN 2>/dev/null | head -1)"
fi

if [[ -z "$pid" ]]; then
  fail "no process is listening on port ${MAIN_PORT} (expected ${EXPECTED_BIN})"
fi
if [[ ! -e "/proc/${pid}" ]]; then
  fail "pid ${pid} disappeared while checking (expected ${EXPECTED_BIN})"
fi

exe="$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
if [[ -z "$exe" ]]; then
  fail "cannot resolve /proc/${pid}/exe (expected ${EXPECTED_BIN})"
fi
actual_bin="$(basename "$exe")"

# --- 3. 二进制版本比对 --------------------------------------------------------
if [[ "$actual_bin" != "$EXPECTED_BIN" ]]; then
  fail "running binary is ${actual_bin} but expected ${EXPECTED_BIN} (pid ${pid}, exe ${exe})"
fi

# --- 4. 顺带核对单元名与 ExecStart -------------------------------------------
# 单元名与实际二进制不符，正是当初把 2.0.29/2.0.30 两次「部署」变成空转的原因。
unit="$(systemctl status "$pid" 2>/dev/null | head -1 | sed -n 's/^.*\(\S*\.service\).*$/\1/p')"
if [[ -z "$unit" ]]; then
  unit="$(grep -ho '[a-zA-Z0-9@_.-]*\.service' "/proc/${pid}/cgroup" 2>/dev/null | head -1)"
fi
if [[ -n "$unit" ]]; then
  exec_start="$(systemctl show -p ExecStart --value "$unit" 2>/dev/null | grep -o '/[^ ;]*' | head -1)"
  if [[ -n "$exec_start" && "$(basename "$exec_start")" != "$EXPECTED_BIN" ]]; then
    fail "unit ${unit} ExecStart is ${exec_start} but expected ${EXPECTED_BIN}"
  fi
fi

log "OK: pid ${pid} exe ${exe} matches expected ${EXPECTED_BIN}${unit:+ (unit ${unit})}"
write_status 0 "pid ${pid} exe ${exe}${unit:+ unit ${unit}}"
exit 0
