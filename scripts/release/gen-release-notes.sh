#!/usr/bin/env bash
#
# gen-release-notes.sh — 一条命令生成 docs/release-notes/v<版本>.md
#
# 数据来源：区间内合并到 main 的 PR（gh pr list），按标题前缀分组；
# 迁移：backend/migrations/ 区间内新增的 *.sql；
# 默认只生成文件，不改 git 历史、不发 Release；--publish 才回填 GitHub Release。
#
# 用法：
#   scripts/release/gen-release-notes.sh 2.3.153
#   scripts/release/gen-release-notes.sh 2.3.153 --from v2.3.151 --to v2.3.152
#   scripts/release/gen-release-notes.sh 2.3.153 --publish              # 回填已存在的 release
#   scripts/release/gen-release-notes.sh 2.3.153 --publish --dry-run    # 只预览发布内容
#
# 兼容：macOS（bash 3.2）/ Linux / Windows Git Bash。依赖 git、gh（已登录）+ bash 3.2+。
# gh 不可用或未登录时自动回退到 git 合并提交解析，不中断生成。

set -euo pipefail

# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# 生产仓库；所有 gh 调用必须带 --repo 指向它，避免在 fork/多远程下写错目标。
DEFAULT_REPO="kiss-kedaya/sub2api"
NOTES_DIR="docs/release-notes"
MIGRATIONS_DIR="backend/migrations"
# 单次 gh pr list 的上限；命中上限时提示用 --from/--to 缩小区间。
GH_PR_LIMIT="300"
# 验证清单最多摘多少条
MAX_VERIFY_ITEMS="8"
# 每「验证」章节最多摘几条
MAX_VERIFY_PER_PR="2"

REPO="$DEFAULT_REPO"
VERSION=""
FROM_TAG=""
TO_REF="HEAD"
PUBLISH=0
DRY_RUN=0
SKIP_VERIFY=0
FORCE=0
OUTPUT=""

# ---------------------------------------------------------------------------
# 输出helpers
# ---------------------------------------------------------------------------

log() { printf '%s\n' "$*" >&2; }
warn() { printf '警告：%s\n' "$*" >&2; }
die() {
	printf '错误：%s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
用法：
  gen-release-notes.sh <版本号> [选项]

参数：
  <版本号>            必填，形如 2.3.153 或 v2.3.153

选项：
  --from <tag>        起始 ref；默认取「上一个 tag」（git describe --tags --abbrev=0）
  --to <ref>          结束 ref；默认 HEAD
  --output <path>     输出路径；默认 docs/release-notes/v<版本号>.md
  --publish           用生成内容回填 GitHub Release 说明（默认只生成文件）
  --dry-run           配合 --publish 时只打印将要写入的 body，不真正写
  --no-verify         跳过读取 PR 的「## 验证」章节
  --repo <owner/repo> gh 目标仓库；默认 kiss-kedaya/sub2api
  --force             覆盖有未提交改动的手写 notes 文件
  -h, --help          显示帮助

示例：
  # 为下一个版本生成，区间 = 上一个 tag..HEAD
  scripts/release/gen-release-notes.sh 2.3.153

  # 指定区间（例如复盘已发布版本）
  scripts/release/gen-release-notes.sh 2.3.153 --from v2.3.151 --to v2.3.152

  # 生成后回填 GitHub Release（release 不存在时只提示 draft 创建命令）
  scripts/release/gen-release-notes.sh 2.3.153 --publish
EOF
}

# ---------------------------------------------------------------------------
# 参数解析
# ---------------------------------------------------------------------------

while [ $# -gt 0 ]; do
	case "$1" in
	--from)
		[ $# -ge 2 ] || die "--from 需要一个 tag/ref"
		FROM_TAG="$2"
		shift 2
		;;
	--from=*)
		FROM_TAG="${1#*=}"
		shift
		;;
	--to)
		[ $# -ge 2 ] || die "--to 需要一个 ref"
		TO_REF="$2"
		shift 2
		;;
	--to=*)
		TO_REF="${1#*=}"
		shift
		;;
	--output)
		[ $# -ge 2 ] || die "--output 需要一个路径"
		OUTPUT="$2"
		shift 2
		;;
	--output=*)
		OUTPUT="${1#*=}"
		shift
		;;
	--repo)
		[ $# -ge 2 ] || die "--repo 需要一个 owner/repo"
		REPO="$2"
		shift 2
		;;
	--repo=*)
		REPO="${1#*=}"
		shift
		;;
	--publish)
		PUBLISH=1
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--no-verify)
		SKIP_VERIFY=1
		shift
		;;
	--force)
		FORCE=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		die "未知参数：$1（-h 查看用法）"
		;;
	*)
		if [ -z "$VERSION" ]; then
			VERSION="$1"
			shift
		else
			die "多余参数：$1（-h 查看用法）"
		fi
		;;
	esac
done

[ -n "$VERSION" ] || {
	usage >&2
	exit 1
}

VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	die "版本号格式不对：$VERSION（期望 2.3.153 这种三段式）"
fi
TAG="v$VERSION"

# ---------------------------------------------------------------------------
# 仓库与 ref 解析
# ---------------------------------------------------------------------------

cd "$ROOT_DIR"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "$ROOT_DIR 不是 git 仓库"

TO_SHA="$(git rev-parse --verify --quiet "$TO_REF^{commit}" 2>/dev/null)" ||
	die "解析不到结束 ref：$TO_REF"
TO_LABEL="$TO_REF"

if [ -z "$FROM_TAG" ]; then
	FROM_TAG="$(git describe --tags --abbrev=0 --match 'v*' "$TO_SHA" 2>/dev/null || true)"
	[ -n "$FROM_TAG" ] || die "取不到上一个 tag，请用 --from <tag> 指定"
fi

FROM_SHA="$(git rev-parse --verify --quiet "$FROM_TAG^{commit}" 2>/dev/null)" ||
	die "解析不到起始 tag/ref：$FROM_TAG"
[ "$FROM_SHA" != "$TO_SHA" ] || die "区间为空：$FROM_TAG 与 $TO_REF 是同一个提交"

FROM_DATE="$(git log -1 --format=%cs "$FROM_SHA")"
TO_DATE="$(git log -1 --format=%cs "$TO_SHA")"
COMMIT_COUNT="$(git rev-list --count "$FROM_SHA..$TO_SHA")"

if git rev-parse --verify --quiet "refs/tags/$TAG" >/dev/null 2>&1; then
	warn "tag $TAG 已存在；本次只覆盖生成文件，不改 tag、不改 git 历史"
fi

# 输出路径与覆盖保护：放在调用 gh 之前，路径/权限有问题就立刻失败，不白跑一轮 API。
if [ -n "$OUTPUT" ]; then
	OUT_REL="$OUTPUT"
else
	OUT_REL="$NOTES_DIR/$TAG.md"
fi
mkdir -p "$(dirname "$OUT_REL")"
if [ -f "$OUT_REL" ] && [ "$FORCE" -eq 0 ] &&
	git ls-files --error-unmatch "$OUT_REL" >/dev/null 2>&1 &&
	! git diff --quiet -- "$OUT_REL" 2>/dev/null; then
	die "$OUT_REL 有未提交改动（可能是手写 notes）；确认覆盖请加 --force"
fi

# ---------------------------------------------------------------------------
# 工具函数：标题分类 / 渲染
# ---------------------------------------------------------------------------

# classify_title <原始标题>
#   -> CLASS（feat|fix|perf|sec|ops|other）、CLASS_DESC（去掉前缀后的中文描述）
CLASS=""
CLASS_DESC=""
classify_title() {
	local raw="$1"
	raw="${raw//：/:}" # 兼容中文冒号
	CLASS="other"
	CLASS_DESC="$raw"
	if [[ "$raw" =~ ^([A-Za-z]+)(\([^\)]*\))?!?:[[:space:]]*(.*)$ ]]; then
		local type_name
		type_name="$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')"
		local desc="${BASH_REMATCH[3]}"
		if [ -n "$desc" ]; then
			CLASS_DESC="$desc"
		fi
		case "$type_name" in
		feat | feature) CLASS="feat" ;;
		fix | bugfix | hotfix) CLASS="fix" ;;
		perf | performance) CLASS="perf" ;;
		security | sec) CLASS="sec" ;;
		chore | ci | docs | build | test | refactor | style) CLASS="ops" ;;
		*) CLASS="other" ;;
		esac
	fi
}

# render_item <PR号> <描述> <作者>
render_item() {
	if [ -n "$3" ]; then
		printf -- '- %s（#%s，@%s）\n' "$2" "$1" "$3"
	else
		printf -- '- %s（#%s）\n' "$2" "$1"
	fi
}

# extract_verify_section <PR body>：输出「## 验证 / ## 测试 / ## 测试结果 / ## 回归断言」
# 章节里的前若干条 bullet（纯 bash 匹配，避免 awk 对中文正则受 locale 影响）。
extract_verify_section() {
	local body="$1"
	local line item taken=0
	local in_section=0
	while IFS= read -r line; do
		if [ "$in_section" -eq 0 ]; then
			case "$line" in
			"## 验证" | "## 验证 "* | "### 验证" | "### 验证 "* | \
				"## 验证清单" | "## 验证清单 "* | \
				"## 测试" | "## 测试 "* | "### 测试" | "### 测试 "* | \
				"## 测试结果" | "## 测试结果 "* | "### 测试结果" | "### 测试结果 "* | \
				"## 回归断言" | "## 回归断言 "* | "### 回归断言" | "### 回归断言 "*)
				in_section=1
				;;
			esac
			continue
		fi
		case "$line" in
		"## "* | "### "* | "#### "*) break ;;
		esac
		case "$line" in
		"- "* | "* "*)
			item="${line#- }"
			item="${item#\* }"
			case "$item" in
			"[ ] "*) item="${item#??? }" ;;
			"[x] "*) item="${item#??? }" ;;
			esac
			[ -n "$item" ] || continue
			printf '%s\n' "$item"
			taken=$((taken + 1))
			[ "$taken" -ge "$MAX_VERIFY_PER_PR" ] && break
			;;
		esac
	done <<<"$body"
}

# ---------------------------------------------------------------------------
# 采集 PR（gh 主路径 / git 兜底）
# ---------------------------------------------------------------------------

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-relnotes.XXXXXX")" ||
	die "创建临时目录失败"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT INT TERM

: >"$WORK_DIR/prs.all.tsv"
for g in feat fix perf sec ops other; do : >"$WORK_DIR/$g.tsv"; done

# 追加一条 PR：mergedAt \t num \t author \t title（title 放最后，避免字段错位）
add_pr() { # <num> <title> <author> <merged_at>
	printf '%s\t%s\t%s\t%s\n' "$4" "$1" "$3" "$2" >>"$WORK_DIR/prs.all.tsv"
}

MODE="git"
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	MODE="gh"
fi

if [ "$MODE" = "gh" ]; then
	log "拉取 $REPO 自 $FROM_DATE 起合并的 PR（gh pr list）"
	if ! gh pr list --repo "$REPO" --state merged \
		--search "merged:>=$FROM_DATE" \
		--limit "$GH_PR_LIMIT" \
		--json number,mergedAt,mergeCommit,author,title \
		--jq '.[] | [.number, (.mergedAt // ""), (.mergeCommit.oid // ""), (.author.login // ""), .title] | @tsv' \
		>"$WORK_DIR/prs.raw.tsv" 2>"$WORK_DIR/gh.err"; then
		warn "gh pr list 失败，回退到 git 合并提交解析：$(head -2 "$WORK_DIR/gh.err" | tr '\n' ' ')"
		MODE="git"
	fi
fi

if [ "$MODE" = "gh" ]; then
	RAW_LINES="$(wc -l <"$WORK_DIR/prs.raw.tsv" | tr -d ' ')"
	if [ "$RAW_LINES" -ge "$GH_PR_LIMIT" ]; then
		warn "gh pr list 命中上限 $GH_PR_LIMIT 条，可能截断；请用 --from/--to 缩小范围"
	fi
	while IFS=$'\t' read -r num merged oid author title; do
		[ -n "${num:-}" ] || continue
		if [ -n "$oid" ] && git cat-file -e "$oid^{commit}" 2>/dev/null; then
			# 只保留「合入本区间」的 PR：merge commit 是 to 的祖先、且不是 from 的祖先。
			if git merge-base --is-ancestor "$oid" "$TO_SHA" 2>/dev/null &&
				! git merge-base --is-ancestor "$oid" "$FROM_SHA" 2>/dev/null; then
				add_pr "$num" "$title" "$author" "$merged"
			fi
		else
			# 浅克隆拿不到 merge commit，或 rebase-merge 无 mergeCommit：按日期兜底。
			d="${merged%%T*}"
			[ -n "$d" ] || continue
			if { [ "$d" \> "$FROM_DATE" ] || [ "$d" = "$FROM_DATE" ]; } &&
				{ [ "$d" \< "$TO_DATE" ] || [ "$d" = "$TO_DATE" ]; }; then
				warn "PR #$num 的 merge commit 不在本地，按合并日期纳入（请确认）"
				add_pr "$num" "$title" "$author" "$merged"
			fi
		fi
	done <"$WORK_DIR/prs.raw.tsv"
else
	log "未使用 gh（未安装或未登录），回退到 git 合并提交解析"
	while IFS= read -r merge; do
		[ -n "$merge" ] || continue
		subject="$(git log -1 --format=%s "$merge")"
		num="$(printf '%s\n' "$subject" | sed -n 's/^Merge pull request #\([0-9][0-9]*\).*/\1/p')"
		[ -n "$num" ] || continue
		title="$(git log -1 --format=%s "$merge^2" 2>/dev/null || printf '%s' "$subject")"
		author="$(git log -1 --format=%an "$merge^2" 2>/dev/null || printf '%s' '')"
		merged="$(git log -1 --format=%cs "$merge")"
		add_pr "$num" "$title" "$author" "$merged"
	done < <(git rev-list --first-parent --merges --reverse "$FROM_SHA..$TO_SHA")
fi

PR_COUNT="$(wc -l <"$WORK_DIR/prs.all.tsv" | tr -d ' ')"

# 按合并时间升序分类落盘
sort -t"$(printf '\t')" -k1,1 "$WORK_DIR/prs.all.tsv" | while IFS=$'\t' read -r merged num author title; do
	[ -n "${num:-}" ] || continue
	classify_title "$title"
	printf '%s\t%s\t%s\n' "$num" "$CLASS_DESC" "$author" >>"$WORK_DIR/$CLASS.tsv"
done

group_count() {
	if [ -s "$WORK_DIR/$1.tsv" ]; then wc -l <"$WORK_DIR/$1.tsv" | tr -d ' '; else echo 0; fi
}

# ---------------------------------------------------------------------------
# 本版重点：安全 + 新增，最多 5 条；不足 3 条时用修复/性能补到 3 条
# ---------------------------------------------------------------------------

: >"$WORK_DIR/highlights.tsv"
HL_COUNT=0
for g in sec feat; do
	[ "$HL_COUNT" -ge 5 ] && break
	[ -s "$WORK_DIR/$g.tsv" ] || continue
	while IFS=$'\t' read -r num desc author; do
		[ -n "${num:-}" ] || continue
		[ "$HL_COUNT" -ge 5 ] && break
		printf '%s\t%s\t%s\n' "$num" "$desc" "$author" >>"$WORK_DIR/highlights.tsv"
		HL_COUNT=$((HL_COUNT + 1))
	done <"$WORK_DIR/$g.tsv"
done
for g in fix perf; do
	[ "$HL_COUNT" -ge 3 ] && break
	[ -s "$WORK_DIR/$g.tsv" ] || continue
	while IFS=$'\t' read -r num desc author; do
		[ -n "${num:-}" ] || continue
		[ "$HL_COUNT" -ge 3 ] && break
		printf '%s\t%s\t%s\n' "$num" "$desc" "$author" >>"$WORK_DIR/highlights.tsv"
		HL_COUNT=$((HL_COUNT + 1))
	done <"$WORK_DIR/$g.tsv"
done

# ---------------------------------------------------------------------------
# 数据库迁移：区间内新增的 backend/migrations/*.sql
# ---------------------------------------------------------------------------

: >"$WORK_DIR/migrations.tsv"
git diff --name-only --diff-filter=A "$FROM_SHA..$TO_SHA" -- "$MIGRATIONS_DIR" 2>/dev/null |
	while IFS= read -r path; do
		case "$path" in
		*.sql) ;;
		*) continue ;;
		esac
		fname="${path##*/}"
		mserial="${fname%%_*}"
		case "$mserial" in
		'' | *[!0-9]*) mserial="-" ;;
		esac
		printf '%s\t%s\n' "$mserial" "$fname" >>"$WORK_DIR/migrations.tsv"
	done
if [ -s "$WORK_DIR/migrations.tsv" ]; then
	sort -t"$(printf '\t')" -k1,1n "$WORK_DIR/migrations.tsv" -o "$WORK_DIR/migrations.tsv"
fi
MIGRATION_COUNT="$(wc -l <"$WORK_DIR/migrations.tsv" | tr -d ' ')"

# ---------------------------------------------------------------------------
# 验证清单：读 PR 的「## 验证 / ## 测试 ...」章节摘要
# ---------------------------------------------------------------------------

: >"$WORK_DIR/verify.md"
VERIFY_COUNT=0
if [ "$SKIP_VERIFY" -eq 0 ] && [ "$MODE" = "gh" ]; then
	for g in sec feat fix perf; do
		[ -s "$WORK_DIR/$g.tsv" ] || continue
		while IFS=$'\t' read -r num desc author; do
			[ -n "${num:-}" ] || continue
			[ "$VERIFY_COUNT" -ge "$MAX_VERIFY_ITEMS" ] && break
			# </dev/null：防止 gh 读走 while 循环的 stdin（组文件），导致漏掉后续 PR
			body="$(gh pr view "$num" --repo "$REPO" --json body --jq '.body // ""' 2>/dev/null </dev/null || true)"
			[ -n "$body" ] || continue
			while IFS= read -r item; do
				[ -n "$item" ] || continue
				[ "$VERIFY_COUNT" -ge "$MAX_VERIFY_ITEMS" ] && break
				printf -- '- %s（#%s）\n' "$item" "$num" >>"$WORK_DIR/verify.md"
				VERIFY_COUNT=$((VERIFY_COUNT + 1))
			done < <(extract_verify_section "$body")
		done <"$WORK_DIR/$g.tsv"
	done
fi

# ---------------------------------------------------------------------------
# 渲染 notes
# ---------------------------------------------------------------------------

render_group() { # <key> <标题>
	[ -s "$WORK_DIR/$1.tsv" ] || return 0
	printf '### %s（%s 条）\n\n' "$2" "$(group_count "$1")"
	while IFS=$'\t' read -r num desc author; do
		[ -n "${num:-}" ] || continue
		render_item "$num" "$desc" "$author"
	done <"$WORK_DIR/$1.tsv"
	printf '\n'
}

render_collapsed_group() { # <key> <标题>
	[ -s "$WORK_DIR/$1.tsv" ] || return 0
	printf '<details>\n<summary>%s（%s 条，点击展开）</summary>\n\n' "$2" "$(group_count "$1")"
	while IFS=$'\t' read -r num desc author; do
		[ -n "${num:-}" ] || continue
		render_item "$num" "$desc" "$author"
	done <"$WORK_DIR/$1.tsv"
	printf '\n</details>\n\n'
}

NEW_NOTES="$(mktemp "${TMPDIR:-/tmp}/sub2api-notes.XXXXXX")" ||
	die "创建临时 notes 文件失败"
trap 'rm -rf "$WORK_DIR" "$NEW_NOTES"' EXIT INT TERM

# 上线步骤是固定模板；先落成变量，渲染时用单个 printf 写出，避免多写入者乱序。
DEPLOY_STEPS="$(cat <<'EOF'
### 上线步骤（蓝绿 + 1% 金丝雀）

1. 从 GitHub Release 下载本版二进制与 `checksums.txt`，`sha256sum -c checksums.txt` 通过后再用（不要用本地临时构建的产物）。
2. 新二进制先只读校验迁移：`./sub2api -check-migrations`；需要单独执行迁移时用 `./sub2api -migrate-only`（执行前做完整数据备份，不要只备份 schema）。
3. 新端口另起一个 unit（旧实例不动）：启动后 `curl -fsS http://127.0.0.1:<新端口>/` 必须是 `200 text/html`；返回 404 通常是构建漏了 `-tags=embed`。
4. nginx upstream 指向新端口、`weight=1`（1% 金丝雀），`nginx -t` 通过后再 reload。
5. 观察错误率、延迟与 panic 日志；无新增异常再逐步放量到全量，旧实例保留到新版本稳定再停。
EOF
)"

{
	printf '# %s\n\n' "$VERSION"
	printf '> 发布日期：%s · 上一版：`%s` · 区间：`%s..%s` · 合并 PR：%s 个\n\n' \
		"$(date +%F)" "$FROM_TAG" "$FROM_TAG" "$TO_LABEL" "$PR_COUNT"

	printf '## 本版重点\n\n'
	if [ -s "$WORK_DIR/highlights.tsv" ]; then
		hl_idx=0
		while IFS=$'\t' read -r num desc author; do
			[ -n "${num:-}" ] || continue
			hl_idx=$((hl_idx + 1))
			if [ -n "$author" ]; then
				printf '%s. **%s**（#%s，@%s）\n' "$hl_idx" "$desc" "$num" "$author"
			else
				printf '%s. **%s**（#%s）\n' "$hl_idx" "$desc" "$num"
			fi
		done <"$WORK_DIR/highlights.tsv"
		printf '\n'
	else
		printf '本区间没有 feat / security 变更，重点见下方「变更清单」。\n\n'
	fi

	printf '## 变更清单\n\n'
	if [ "$PR_COUNT" -eq 0 ]; then
		printf '本区间没有合并 PR。\n\n'
	else
		render_group feat "新增"
		render_group fix "修复"
		render_group perf "性能"
		render_group sec "安全"
		render_collapsed_group ops "运维 / 工程化"
		render_group other "其他"
	fi

	printf '## 迁移与上线\n\n'
	printf '### 数据库迁移\n\n'
	if [ "$MIGRATION_COUNT" -eq 0 ]; then
		printf '无。\n\n'
	else
		printf '| 编号 | 文件 |\n| --- | --- |\n'
		while IFS=$'\t' read -r mserial fname; do
			[ -n "${fname:-}" ] || continue
			printf '| %s | `%s` |\n' "$mserial" "$fname"
		done <"$WORK_DIR/migrations.tsv"
		printf '\n上线前先跑 `./sub2api -check-migrations`，待执行列表应与上表一致。\n\n'
	fi

	# 注意：这段内容用变量 + 单个 printf 输出。同一次重定向里混用 bash 内建 printf 与
	# 外部 cat 会因为 stdio 缓冲导致写入顺序错乱（cat 先落盘、printf 缓冲后覆盖），
	# 生成的文件必须由同一个写入者按顺序写完。
	printf '%s\n\n' "$DEPLOY_STEPS"

	printf '## 回滚与验证\n\n'
	printf '### 回滚\n\n'
	printf -- '- 流量切回上一版 `%s` 的 upstream；旧进程与资源先保留，不删。\n' "$FROM_TAG"
	if [ "$MIGRATION_COUNT" -eq 0 ]; then
		printf -- '- 本版无数据库迁移，代码回滚没有 schema 依赖。\n'
	else
		printf -- '- 本版包含数据库迁移，回滚代码前先确认迁移是否纯增量（只加列/表可共存）；改列/删列类按迁移文件说明单独处理。\n'
	fi
	printf -- '- 回滚后 `curl -fsS http://127.0.0.1:<端口>/` 仍须 `200 text/html`，并抽查一次真实业务请求。\n\n'

	printf '### 验证清单\n\n'
	if [ -s "$WORK_DIR/verify.md" ]; then
		while IFS= read -r verify_line; do
			[ -n "$verify_line" ] && printf '%s\n' "$verify_line"
		done <"$WORK_DIR/verify.md"
	else
		printf -- '- 各 PR 未提供「## 验证」章节，按发版清单人工确认：首页 200、登录、后台主要页面、充值/计费各走一笔。\n'
	fi
} >"$NEW_NOTES"

[ -s "$NEW_NOTES" ] || die "生成内容为空"

mv "$NEW_NOTES" "$OUT_REL"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

log "已生成 $OUT_REL"
log "  区间：$FROM_TAG..$TO_LABEL（$COMMIT_COUNT 个提交，$PR_COUNT 个合并 PR）"
log "  分组：新增 $(group_count feat) / 修复 $(group_count fix) / 性能 $(group_count perf) / 安全 $(group_count sec) / 运维 $(group_count ops) / 其他 $(group_count other)"
if [ "$MIGRATION_COUNT" -eq 0 ]; then
	log "  迁移：无"
else
	log "  迁移：$MIGRATION_COUNT 个（$(cut -f2 "$WORK_DIR/migrations.tsv" 2>/dev/null | tr '\n' ' '))"
fi
log "  数据源：$([ "$MODE" = "gh" ] && printf 'gh pr list（%s）' "$REPO" || printf 'git 合并提交解析')"

# ---------------------------------------------------------------------------
# 可选：回填 GitHub Release
# ---------------------------------------------------------------------------

# 组装发布 body：已有 release 若带 goreleaser header/footer，则只替换中间正文，
# 保留 `> header` 和 `## 📥 Installation` 安装说明。
compose_publish_body() { # <existing_body> <notes_file>
	local existing="$1" notes_file="$2"
	local -a lines=()
	local l
	while IFS= read -r l; do lines+=("$l"); done <<<"$existing"
	local n=${#lines[@]}
	local has_install=0 starts_quote=0 i
	for ((i = 0; i < n; i++)); do
		if [ "${lines[$i]}" = "## 📥 Installation" ]; then
			has_install=1
		fi
	done
	if [ "$n" -gt 0 ]; then
		case "${lines[0]}" in
		"> "*) starts_quote=1 ;;
		esac
	fi
	if [ "$has_install" -eq 1 ] && [ "$starts_quote" -eq 1 ]; then
		local header_end=-1 footer_start=-1 j
		i=1
		while [ "$i" -lt "$n" ]; do
			if [ -z "${lines[$i]}" ]; then
				header_end="$i"
				break
			fi
			i=$((i + 1))
		done
		for ((i = 0; i < n; i++)); do
			if [ "${lines[$i]}" = "## 📥 Installation" ]; then
				footer_start="$i"
				break
			fi
		done
		j=$((footer_start - 1))
		while [ "$j" -gt "$header_end" ] && [ -z "${lines[$j]}" ]; do j=$((j - 1)); done
		if [ "${lines[$j]}" = "---" ]; then
			footer_start="$j"
		fi
		if [ -n "$header_end" ] && [ "$header_end" -ge 0 ]; then
			local header_text="" footer_text=""
			for ((i = 0; i <= header_end; i++)); do
				header_text+="${lines[$i]}"$'\n'
			done
			for ((i = footer_start; i < n; i++)); do
				footer_text+="${lines[$i]}"$'\n'
			done
			local notes_text
			notes_text="$(cat "$notes_file")"
			printf '%s%s\n\n%s' "$header_text" "$notes_text" "$footer_text"
		else
			printf '%s\n' "$(cat "$notes_file")"
		fi
	else
		printf '%s\n' "$(cat "$notes_file")"
	fi
}

if [ "$PUBLISH" -eq 1 ]; then
	if ! command -v gh >/dev/null 2>&1; then
		die "--publish 需要 gh，但当前机器没有 gh"
	fi
	if ! gh auth status >/dev/null 2>&1; then
		die "--publish 需要已登录的 gh（gh auth login）"
	fi
	if ! gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
		warn "GitHub Release $TAG 不存在（正常：release 由 tag push 触发 release.yml 创建）"
		log "可先创建草稿再回填："
		log "  gh release create $TAG --repo $REPO --draft --notes-file $OUT_REL"
		exit 3
	fi
	existing_body="$(gh release view "$TAG" --repo "$REPO" --json body --jq '.body // ""' 2>/dev/null || true)"
	compose_publish_body "$existing_body" "$OUT_REL" >"$WORK_DIR/publish.md"
	if [ "$DRY_RUN" -eq 1 ]; then
		log "--- --publish --dry-run：以下内容将写入 $TAG（未实际写入）---"
		cat "$WORK_DIR/publish.md"
		exit 0
	fi
	gh release edit "$TAG" --repo "$REPO" --notes-file "$WORK_DIR/publish.md"
	log "已回填 GitHub Release $TAG 的说明"
fi

printf '%s\n' "$OUT_REL"
