# 事故记录 2026-11-17：2.3.93 内嵌前端不完整导致全站 404

## 现象

kedaya.ai 打开后页面报：

```
ProfileBarkCard.vue_vue_type_script_setup_true_lang-ozemzjxE.js: 404
CryptoView-BVN9_5vp.js: 404
user-CRJCmYXn.js: 404
useStepUp-Dtq8RSyw.js: 404
Chunk load error persists after reload. Please clear browser cache.
TypeError: Failed to fetch dynamically imported module: https://kedaya.ai/assets/CryptoView-BVN9_5vp.js
```

首页 `/` 返回 200，入口 `index-DvhtksiI.js` 也 200，但只要路由懒加载就炸，等于全站不可用。

## 根因

**2.3.93 的发布制品内嵌前端不完整**：入口 JS 里引用的分包文件不在二进制内嵌的 `dist` 里。两台机器（old 8432 / new 18532）跑的是同一份坏包，所以两台都 404；不是单机问题、不是 nginx 问题、不是浏览器缓存问题。

同一批分包在 **2.3.92** 制品里存在（`curl` 200），说明是构建/打包环节丢了文件，而不是引用写错。

## 处置（已生效）

1. 启用上一版备用色：old `sub2api-bg-2.3.92`（8431）、new `sub2api-bg-2.3.92`（18531），原进程一直在跑，无需重建。
2. 先确认备用色自己能把分包服务出来：`curl http://<ip>:8431/assets/CryptoView-BVN9_5vp.js` → 200。
3. 切权重：8431 / 18531 设 `weight=100`，8432 / 18532 设 `down`（不是 `backup`，坏包不该再接任何流量）。
4. `nginx -t` + reload，逐个复验之前 404 的四个分包与首页：全部 200，连打 10 次稳定 200。

回滚没有动数据库：2.3.92 → 2.3.93 之间**没有任何迁移文件**（`git diff --name-only v2.3.92..v2.3.93 -- backend/migrations` 为空），schema 兼容。

## 预防

发版切权重之前，在候选机上对**新端口**做一次制品完整性检查：

```bash
scripts/verify-embedded-assets.sh http://10.254.0.2:18532
```

脚本会取首页入口 JS，抽出它对 `assets/*.js` 的全部引用，逐个 `curl`，任何一个非 200 就直接失败。只有全绿才允许 `weight=100`。

## 教训

- 入口 JS 200 不代表前端可用，懒加载分包才是最容易丢的部分。
- 出这种事**不要现场重建**，先切回上一版备用色，再离线查构建。
- 发布工作流现有的 "Verify embedded frontend matches the release commit" 只校验 dist 的提交标记，**不校验入口引用的分包是否真的都在**，所以没拦住这次事故。
