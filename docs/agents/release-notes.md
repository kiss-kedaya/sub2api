# 发版说明（Release Notes）规范

面向发版执行人。目标：GitHub Release 的说明不再是空描述或只有标题，而是**一条命令生成、可评审、可回看**。

- 生成脚本：`scripts/release/gen-release-notes.sh`
- 模板：`docs/release-notes/TEMPLATE.md`
- 产物：`docs/release-notes/v<版本号>.md`（提交进仓库，`release.yml` 会把它作为 release body）
- 手动 CI：`.github/workflows/release-notes.yml`（只生成 + 上传 artifact，不发布）

## 1. 一条命令生成

```bash
# 1) 生成（默认区间 = 上一个 tag..HEAD，默认只写文件、不发布）
scripts/release/gen-release-notes.sh 2.3.153

# 2) 指定区间（复盘已发布版本 / 补数据）
scripts/release/gen-release-notes.sh 2.3.153 --from v2.3.151 --to v2.3.152

# 3) 生成并回填已存在的 GitHub Release（release 不存在时只提示 draft 创建命令）
scripts/release/gen-release-notes.sh 2.3.153 --publish

# 4) 发布前预览将写入 release 的内容，不实际写
scripts/release/gen-release-notes.sh 2.3.153 --publish --dry-run
```

脚本做的事：

1. 解析版本号（接受 `2.3.153` 或 `v2.3.153`）与区间（`--from` 默认 `git describe --tags --abbrev=0`，`--to` 默认 `HEAD`）。
2. 用 `gh` 拉区间内合并的 PR（`gh pr list --state merged --search "merged:>=<from 日期>"`），再用 merge commit 的祖先关系过滤，只保留真正落在这个区间的 PR。
3. 按 PR 标题前缀分组：`feat`→新增、`fix`→修复、`perf`→性能、`security`/`sec`→安全、`chore`/`ci`/`docs`→运维/工程化（折叠）。
4. 扫 `backend/migrations/` 区间内**新增**的 `*.sql`，列出编号与文件名。
5. 填固定模板：本版重点、变更清单、迁移与上线、回滚与验证；验证清单从 PR 的 `## 验证`（或 `## 测试` / `## 测试结果` / `## 回归断言`）章节摘要。
6. 只写 `docs/release-notes/v<版本号>.md`，不改 git 历史；重复执行覆盖同名文件（幂等）。

`gh` 未安装或未登录时自动回退到 `git` 合并提交解析（会少 PR 正文里的验证章节），不中断生成。

### 常用参数

| 参数 | 说明 |
| --- | --- |
| `<版本号>` | 必填，三段式，如 `2.3.153`（带 `v` 也行） |
| `--from <tag>` | 起始 ref；默认上一个 tag |
| `--to <ref>` | 结束 ref；默认 `HEAD` |
| `--output <path>` | 输出路径；默认 `docs/release-notes/v<版本号>.md` |
| `--publish` | 回填 GitHub Release 说明（`gh release edit --notes-file`） |
| `--dry-run` | 配合 `--publish`，只打印将写入的内容 |
| `--no-verify` | 跳过读取 PR 验证章节 |
| `--repo <owner/repo>` | gh 目标仓库，默认 `kiss-kedaya/sub2api` |
| `--force` | 覆盖有未提交改动的手写 notes（防误删） |

## 2. 标准发版步骤

```bash
# 0. 确认工作区干净、基于最新 main
git fetch origin && git switch main && git pull --ff-only
```

1. **生成** notes（tag 之前）：

   ```bash
   scripts/release/gen-release-notes.sh 2.3.153
   ```

2. **人工评审** `docs/release-notes/v2.3.153.md`：
   - 本版重点是否抓住用户能感知的变化；
   - 分组是否缺项（脚本按前缀归类，标题写错就会落到「其他」）；
   - 迁移表是否与 `./sub2api -check-migrations` 的待执行列表一致；
   - 上线/回滚段落是否需要按本版补充（例如特殊开关、灰度比例）。
   - 需要细调可直接改文件，脚本只在生成时覆盖。

3. **提交**（只提交 notes，不夹带业务改动）：

   ```bash
   git add docs/release-notes/v2.3.153.md
   git commit -m "docs: release notes for 2.3.153"
   git push origin main
   ```

4. **打 tag**（annotation 至少写一句；有 notes 文件时 release.yml 优先用文件内容）：

   ```bash
   git tag -a v2.3.153 -m "release v2.3.153"
   git push origin v2.3.153
   ```

   `release.yml` 会读 `docs/release-notes/v2.3.153.md` 作为 release body（goreleaser 另外补 header 与安装说明），**没有 notes 文件时 release 步骤会直接失败**。

5. **（可选）回填已有 Release**：仅当 release 已存在但说明为空/需要纠正时：

   ```bash
   scripts/release/gen-release-notes.sh 2.3.153 --publish --dry-run   # 先预览
   scripts/release/gen-release-notes.sh 2.3.153 --publish             # 再回填
   ```

   回填会保留 goreleaser 自动加的 `> header` 与 `## 📥 Installation` 安装段，只替换中间正文。release 不存在时脚本只打印 draft 创建命令，不自动发版。

默认流程不需要 `--publish`：tag push 时 release body 已经来自仓库里的 notes 文件。

## 3. PR 标题规范

生成脚本靠标题前缀分组，**标题前缀必须写对**：

```
<type>(<可选 scope>): <中文一句话>
```

- `feat` → 新增；`fix` → 修复；`perf` → 性能；`security` / `sec` → 安全；`chore` / `ci` / `docs` → 运维/工程化（折叠）。
- 不在上表的前缀（如 `refactor`、`test`）会归到「运维 / 工程化」；无法识别前缀的标题落到「其他」，需要尽量避免。
- 描述用中文、一句话说清「改了什么、影响谁」；不要只写「优化」「修复问题」。
- 冒号用半角 `:`（中文冒号 `：` 也能识别，但统一用半角）。
- scope 可选，用于区分模块：`feat(crypto): ...`、`fix(admin): ...`。scope 不进 notes 正文，只用于分类。

示例：

```
feat(auth): 五分钟 2FA 不透明凭证，密码登录免重复二次验证
fix(admin): 账号测试接口不再强制 step-up 2FA
perf(usage): 14 天连接追踪裁剪走增量查询
security(api): 管理 API Key 禁止调用 secret-free 路由
chore(ci): 发版说明改为脚本生成
```

需要 PR 正文提供验证摘要时，加一段（脚本会摘取前两条 bullet）：

```markdown
## 验证

- 真实请求：管理端逐模型测试不再 403
- 回归：`go test ./internal/...` 通过
```

## 4. 数据库迁移编号规则

- 新迁移文件名：`backend/migrations/NNN_描述.sql`（`NNN` 为数字编号，描述用 snake_case）；含 `CREATE INDEX CONCURRENTLY` 的用 `_notx.sql` 后缀。
- 编号取 **main 上当前最大编号 + 1**（即下一个未被占用的号）：

  ```bash
  git fetch origin main --tags
  ls backend/migrations/*.sql | sed 's#.*/##; s/_.*//' | sort -n | tail -1
  # 输出 N，新文件用 N+1
  ```

- 不要回收历史空号（例如中间断号），避免与并行分支撞号；也不要改已合并迁移的文件名/内容（有 SHA256 checksum，改了就启动失败）。
- 迁移必须是幂等的（`IF NOT EXISTS` / `IF EXISTS`），正向迁移，回滚另开新文件。
- 提交前在本地跑一次（或至少确认 SQL 可重复执行）：

  ```bash
  ./sub2api -check-migrations   # 只读校验
  ./sub2api -migrate-only       # 显式执行（先备份）
  ```

生成的 notes 会自动把区间内新增迁移列成「编号 + 文件名」表格，发版前与该表核对待执行列表。

## 5. 手动 CI（人工核对用）

`.github/workflows/release-notes.yml`，**只读、手动触发、不发布**：

1. GitHub → Actions → `Release Notes` → `Run workflow`；
2. 填 `version`（如 `2.3.153`），可选填 `from` / `to`；
3. 跑完下载 artifact `release-notes-v<版本号>`，或直接看 job summary 里的生成内容；
4. 核对无误后人工把文件提交进仓库（CI 不会提交，也不会改 Release）。

该 workflow 只有 `contents: read` + `pull-requests: read`，不使用 `--publish`，默认不会自动运行。

## 6. 故障排查

| 现象 | 原因 / 处理 |
| --- | --- |
| 报「未使用 gh，回退到 git 合并提交解析」 | 本机没装 `gh` 或未 `gh auth login`；结果仍可用，但缺 PR 正文验证摘要。 |
| PR 没被收进 notes | 合并时间不在检索窗口，或 merge commit 不是本区间祖先；用 `--from/--to` 明确区间后重跑。 |
| 有 PR 但分组是「其他」 | 标题没写 `feat:` / `fix:` 等前缀，改标题后重跑（release notes 以合并时的标题为准）。 |
| 迁移表为空但 `-check-migrations` 有待执行 | 迁移文件不在 `backend/migrations/` 或不是本区间新增（例如用了旧文件名）；核对 `git diff --diff-filter=A`。 |
| `--publish` 报 release 不存在 | 正常：release 由 tag push 创建。先打 tag 并等 `release.yml` 完成，或按脚本提示 `gh release create --draft`。 |
| 覆盖手写 notes 被拒绝 | 文件有未提交改动；确认要覆盖加 `--force`。 |

## 7. 边界

- 脚本只读 git 与 GitHub API、只写 `docs/release-notes/` 下的目标文件；不改业务代码、不改 tag、不改 git 历史。
- `--publish` 是唯一会写 GitHub 的动作，默认关闭；CI 不启用。
- 不把内网地址、凭据、上游域名写进 notes（仓库是 public，`sensitive-check` 会扫）。
