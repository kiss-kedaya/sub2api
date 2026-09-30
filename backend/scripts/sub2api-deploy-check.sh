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
# 主线单元名。用于区分「正常重启的空窗期」与「自称 active 却没人监听」。
# 留空则不做这个区分，空窗期会被判为不一致（会在每次发版时误报一次 P0）。
MAIN_UNIT_DEFAULT_SUFFIX="${MAIN_UNIT_SUFFIX:--canary}"

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

# 「运行中的二进制 != EXPECTED_VERSION」是**部署中间态**：先换二进制、后改期望值，
# 中间必然有一段不一致窗口。用绝对值阈值（>0）报警，等于每次正常发版都报一次，
# 而且只要期望值没跟上就**一直报**——和之前计费规则把「持续状态」当「事件」是同一个错误。
#
# 所以这条单独走宽限窗口：不一致必须**持续**超过 MISMATCH_GRACE_SECONDS 才算错。
# 「第一次见到不一致」的时刻落盘，所以对 systemd timer 的调用间隔不敏感。
# 结构性故障（期望值文件缺失、自称 active 却没人监听）不走这里，仍立即上报。
MISMATCH_GRACE_SECONDS="${MISMATCH_GRACE_SECONDS:-900}"
MISMATCH_SINCE_FILE="${MISMATCH_SINCE_FILE:-/opt/sub2api/deploy-check.mismatch-since}"

clear_mismatch_state() {
  rm -f "$MISMATCH_SINCE_FILE" 2>/dev/null || true
}

fail_version_mismatch() {
  local detail="$1" now since age
  now="$(date +%s)"
  since="$(cat "$MISMATCH_SINCE_FILE" 2>/dev/null || true)"
  if [[ ! "$since" =~ ^[0-9]+$ ]]; then
    since="$now"
    printf '%s\n' "$since" > "$MISMATCH_SINCE_FILE" 2>/dev/null || true
  fi
  age=$(( now - since ))
  if (( age < MISMATCH_GRACE_SECONDS )); then
    log "PENDING: $detail (${age}s < ${MISMATCH_GRACE_SECONDS}s grace, treating as deploy window)"
    write_status 0 "${detail}; pending ${age}s of ${MISMATCH_GRACE_SECONDS}s grace"
    exit 0
  fi
  fail "${detail}; persisted ${age}s (grace was ${MISMATCH_GRACE_SECONDS}s)"
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
# 主线单元名：默认 <prefix>-<version>-canary，可用 MAIN_UNIT 覆盖。
MAIN_UNIT="${MAIN_UNIT:-${EXPECTED_BIN}${MAIN_UNIT_DEFAULT_SUFFIX}}"

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
  # 没有进程在监听，**不能**直接判定为版本不一致：正常发版/重启的那几秒就是空窗期。
  # 本脚本只负责「版本一致性」，服务存活由现有的成功率/错误率规则负责，不要重复告警。
  # 只有当单元自称 active 却没人监听时，才说明确实出了问题。
  if [[ -n "$MAIN_UNIT" ]] && command -v systemctl >/dev/null 2>&1; then
    unit_state="$(systemctl is-active "$MAIN_UNIT" 2>/dev/null || true)"
    if [[ "$unit_state" != "active" ]]; then
      log "SKIP: no listener on ${MAIN_PORT} and ${MAIN_UNIT} is ${unit_state:-unknown} (restart/deploy window, not a version mismatch)"
      write_status 0 "no listener on ${MAIN_PORT}; unit ${MAIN_UNIT} is ${unit_state:-unknown}; version not compared"
      exit 0
    fi
  fi
  fail "no process is listening on port ${MAIN_PORT} but the unit reports active (expected ${EXPECTED_BIN})"
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
  fail_version_mismatch "running binary is ${actual_bin} but expected ${EXPECTED_BIN} (pid ${pid}, exe ${exe})"
fi
# 一致了：清掉宽限状态，下一次不一致重新计时。
clear_mismatch_state

# --- 4. 顺带核对单元名与 ExecStart -------------------------------------------
# 单元名与实际二进制不符，正是当初把 2.0.29/2.0.30 两次「部署」变成空转的原因。
#
# 取单元名的顺序（原先只用第一种，且正则贪婪匹配，实测在 2.0.35 上只抓到 ".service"，
# 于是下面「单元是 active 才算真故障」的保护静默失效）：
#   1) cgroup 路径末尾的 <unit>.service —— 最可靠，直接来自内核
#   2) 按 MainPID 反查 systemd —— cgroup 格式变了也能兜住
unit="$(sed -n 's#.*/\([^/]*\.service\)$#\1#p' "/proc/${pid}/cgroup" 2>/dev/null | head -1)"
if [[ -z "$unit" ]] && command -v systemctl >/dev/null 2>&1; then
  unit="$(systemctl list-units --type=service --state=running --no-legend --plain 'sub2api*' 2>/dev/null \
    | awk '{print $1}' \
    | while read -r candidate; do
        if [[ "$(systemctl show -p MainPID --value "$candidate" 2>/dev/null)" == "$pid" ]]; then
          printf '%s\n' "$candidate"
          break
        fi
      done)"
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
