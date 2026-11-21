#!/usr/bin/env bash
# ============================================================================
# 卡密哈希化 · 阶段 B · 步骤 2 驱动脚本：分批回填直到 pending = 0
# ============================================================================
#
# 为什么用 bash 循环而不是一条大 SQL：
#   * 每批 500 行 = 一条独立语句 = 一个短事务，不会长时间持锁；
#   * 单条 ≤30s，符合生产执行约束；
#   * 中断后重跑天然幂等（只处理 code_hash IS NULL 的行）。
#
# 用法（在能访问生产库的机器上）：
#   bash backend/migrations/manual/redeem_code_hash_step2_backfill_loop.sh \
#     'ssh kedaya-old "docker exec -i kedaya-sub2-postgres-1 psql -U sub2api -d sub2api"'
#
# 第一个参数是「执行 psql 的命令前缀」，脚本会通过它执行 -f 与 -c。
# 默认值按本仓库约定的库机走。

set -euo pipefail

PSQL_CMD="${1:-ssh kedaya-old \"docker exec -i kedaya-sub2-postgres-1 psql -U sub2api -d sub2api\"}"
SQL_FILE="$(cd "$(dirname "$0")" && pwd)/redeem_code_hash_step2_backfill.sql"
BATCH_SLEEP_SECONDS="${BATCH_SLEEP_SECONDS:-0.2}"
MAX_BATCHES="${MAX_BATCHES:-500}"

pending=$(
  eval "$PSQL_CMD -tAc" "\"SELECT COUNT(*) FROM redeem_codes WHERE code_hash IS NULL AND code IS NOT NULL\""
)

echo "[backfill] pending rows before: ${pending}"
batch=0
while [ "${pending}" -gt 0 ]; do
  batch=$((batch + 1))
  if [ "${batch}" -gt "${MAX_BATCHES}" ]; then
    echo "[backfill] aborting: exceeded MAX_BATCHES=${MAX_BATCHES}, pending=${pending}" >&2
    exit 1
  fi
  eval "$PSQL_CMD -f" "${SQL_FILE}" >/dev/null
  pending=$(
    eval "$PSQL_CMD -tAc" "\"SELECT COUNT(*) FROM redeem_codes WHERE code_hash IS NULL AND code IS NOT NULL\""
  )
  echo "[backfill] batch ${batch} done, pending=${pending}"
  sleep "${BATCH_SLEEP_SECONDS}"
done

echo "[backfill] complete after ${batch} batch(es)"
