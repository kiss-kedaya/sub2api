# 发版说明

每个 tag 对应一份 `<tag>.md`，例如 v2.0.9 对应 `v2.0.9.md`。

## 规则

1. 打 tag **之前**先把 `docs/release-notes/<tag>.md` 写好并提交。
2. 文件内容会原样成为 GitHub Release 的正文，**不要**在里面再写安装说明——
   安装段落由 `.goreleaser.yaml` 的 `release.footer` 自动追加。
3. 说明为空会导致 Release workflow 直接失败，避免发出没有说明的版本。

## 优先级

Release workflow 取说明的顺序：

1. `docs/release-notes/<tag>.md`（推荐，可评审、可回看）
2. annotated tag message 的 body（兼容旧 tag）

两者都为空时 workflow 报错退出。

## 格式

开头一句话讲清这版是什么，然后按「新增 / 修复 / 配置变更 / 升级注意」分节。
提到迁移的写清编号，提到行为变更的写清改前改后。
