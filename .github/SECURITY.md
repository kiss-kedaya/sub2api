# Security Policy

[English](#english) | [中文](#中文)

## English

### Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues, pull requests, or discussions.**

Report them privately through GitHub's private vulnerability reporting:

**[Report a vulnerability](https://github.com/kiss-kedaya/sub2api/security/advisories/new)**

(Repository → **Security** tab → **Report a vulnerability**.) Only the maintainers can see the report, and the advisory becomes the place to discuss, fix, and eventually publish the issue.

A useful report includes:

- The affected component and version (release tag or commit)
- Impact: what an attacker can do and which privileges they need
- Minimal reproduction steps or a proof of concept
- Any non-default configuration required to trigger the issue
- A suggested fix, if you have one

### Supported Versions

Releases are frequent, and fixes ship in a new release rather than being backported.

| Version | Supported |
| ------- | --------- |
| Latest release | ✅ |
| Older releases | ❌ |

Please confirm that the issue reproduces on the latest release or on `main` before reporting.

### Scope

In scope: the backend, frontend, official Docker images, and deployment files in this repository.

Usually out of scope:

- Issues that require already-compromised admin credentials or server access
- Risks arising from explicitly weakening documented security settings (for example, disabling `security.url_allowlist`)
- Vulnerabilities in upstream AI providers or third-party dependencies with no impact specific to this fork (report those upstream)
- Missing best-practice headers or scanner output without a demonstrated impact

### Disclosure Process

1. We acknowledge the report and assess its validity and severity.
2. We develop a fix privately in the advisory and may ask you to verify it.
3. We publish a release containing the fix, then publish the GitHub Security Advisory (with a CVE when applicable), crediting you unless you prefer to stay anonymous.

Please keep details private until the advisory is published.

## 中文

### 报告漏洞

**请不要通过公开的 Issue、Pull Request 或 Discussion 报告安全漏洞。**

请使用 GitHub 私密漏洞报告功能提交：

**[提交漏洞报告](https://github.com/kiss-kedaya/sub2api/security/advisories/new)**

（仓库 → **Security** 标签页 → **Report a vulnerability**。）报告仅维护者可见，后续的讨论、修复与公告发布都在该安全公告中进行。

一份有用的报告应包含：

- 受影响的组件与版本（release tag 或 commit）
- 影响面：攻击者能做什么、需要什么权限
- 最小复现步骤或 PoC
- 触发问题所需的任何非默认配置
- 如已有修复建议，一并附上

### 支持的版本

发布较频繁，修复随新版本发布，不做向后移植。

| 版本 | 是否支持 |
| ------- | --------- |
| 最新 release | ✅ |
| 更早的 release | ❌ |

报告前请先确认问题能在最新 release 或 `main` 上复现。

### 范围

在范围内：本仓库的后端、前端、官方 Docker 镜像与部署文件。

通常不在范围内：

- 需要已泄露的管理员凭据或服务器访问权限才能触发的问题
- 主动关闭文档中记录的安全设置（例如关闭 `security.url_allowlist`）带来的风险
- 上游 AI 供应商或第三方依赖自身的漏洞，且与本分支无关（请向上游报告）
- 没有可证明影响的“缺少安全响应头”或扫描器输出

### 披露流程

1. 我们确认收到报告，并评估其有效性与严重程度。
2. 我们在安全公告中私下修复，可能请你验证。
3. 我们发布包含修复的版本，然后发布 GitHub Security Advisory（适用时附带 CVE），并致谢报告者（除非你希望匿名）。

在公告发布前，请对细节保密。