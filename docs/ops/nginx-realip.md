# old 机 nginx access.log 记录真实客户端 IP

> 适用：`old` 机（系统 nginx 监听 `127.0.0.1:8443` / `127.0.0.1:17778`，前面是雷池 tengine）。
> 本文只是仓库内的操作说明，**不包含也不授权任何生产机自动改动**；执行人按下面顺序手工上线。

## 1. 现状与问题

真实链路（2026-10-14 现场口径）：

```
Cloudflare ──443──► 雷池 tengine（net=host，占 80/443）──► https://127.0.0.1:8443 ──► 系统 nginx ──► sub2api
                         ▲
直连业务口 17777 ────────┘──► 127.0.0.1:17778 ──► 系统 nginx ──► sub2api
```

系统 nginx 看到的 TCP 对端永远是 `127.0.0.1`（雷池），所以默认 `$remote_addr` 是回环地址。
后果：

- `access.log` 里全是 `127.0.0.1`，按 IP 排查、封禁、审计都失效；
- 管理端 401/403 探测告警（`admin_auth_probe`）按来源 IP 聚合，拿不到真实 IP 就聚合不到探测者；
- 雷池/CF 的真实客户端 IP 只在 `X-Forwarded-For` / `CF-Connecting-IP` 里，nginx 默认不解析。

## 2. 目标

让 nginx 的 `$remote_addr`（以及 `$http_x_forwarded_for`）在两种入口下都反映真实客户端 IP：

- 经 Cloudflare 的 443；
- 直连 `17777` 的大用户绕 CF 流量。

## 3. 前提确认（只读）

在 old 机执行，先确认现状，不改任何配置：

```bash
# 1) 确认 nginx 是系统 nginx，且监听在回环高口（80/443 属于 safeline-tengine 容器）
ss -lntp | grep -E ':(80|443|8443|17778|17777)\b'
nginx -T 2>/dev/null | grep -nE 'set_real_ip_from|real_ip_header|real_ip_recursive' || echo 'no realip configured'
docker ps --format '{{.Names}}' | grep -i safeline

# 2) 看当前 access.log 的客户端列（第一列）
sudo tail -n 5 /var/log/nginx/access.log

# 3) 抓一次从雷池到 nginx 的请求头，确认 XFF 链（只抓 20 个包，不改配置）
sudo timeout 15 tcpdump -A -s0 -i lo 'tcp port 8443' -c 20
```

期望看到两种可能之一：

- `X-Forwarded-For: <真实客户端>, <CF 边缘 IP>` —— 雷池用 `$proxy_add_x_forwarded_for` 追加；
- `X-Forwarded-For: <CF 边缘 IP>` 或只有 `CF-Connecting-IP: <真实客户端>` —— 雷池覆盖/透传方式不同。

**按实际抓到的链选第 4 节的方案，不要照抄。**

## 4. 方案

所有片段都放在 `http { }` 级别（`conf.d/*.conf` 或 `sites-enabled` 里的 server 外层），
不要只写进某一个 server，两个入口都要覆盖。

### 方案 A：雷池保留并追加 XFF（最常见）

```nginx
# /etc/nginx/conf.d/realip.conf
# 只信任直接对端（雷池在本机回环）和 Cloudflare 网段；其余地址一律不信任。
set_real_ip_from 127.0.0.1;
set_real_ip_from ::1;

# Cloudflare 官方 IPv4/IPv6 网段（上线前用 https://www.cloudflare.com/ips/ 重新核对）
# 下面是最小集合示例，务必替换成当时官网完整列表。
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
set_real_ip_from 103.22.200.0/22;
set_real_ip_from 103.31.4.0/22;
set_real_ip_from 141.101.64.0/18;
set_real_ip_from 108.162.192.0/18;
set_real_ip_from 190.93.240.0/20;
set_real_ip_from 188.114.96.0/20;
set_real_ip_from 197.234.240.0/22;
set_real_ip_from 198.41.128.0/17;
set_real_ip_from 162.158.0.0/15;
set_real_ip_from 104.16.0.0/13;
set_real_ip_from 104.24.0.0/14;
set_real_ip_from 172.64.0.0/13;
set_real_ip_from 131.0.72.0/22;
set_real_ip_from 2400:cb00::/32;
set_real_ip_from 2606:4700::/32;
set_real_ip_from 2803:f800::/32;
set_real_ip_from 2405:b500::/32;
set_real_ip_from 2405:8100::/32;
set_real_ip_from 2a06:98c0::/29;
set_real_ip_from 2c0f:f248::/32;

real_ip_header X-Forwarded-For;
real_ip_recursive on;
```

`real_ip_recursive on` 从右往左跳过受信代理（雷池/CF 边缘），取第一个不受信地址：

- 经 CF：`client, cf-edge` → cf-edge 受信跳过 → `client`；
- 直连 17777：`client`（雷池追加）→ 直接取；
- 攻击者自己伪造 `X-Forwarded-For: 1.2.3.4`：雷池仍会把自己看到的真实对端追加到右边，递归时先命中真实对端，伪造值不会胜出（上线后必须按第 6 节验证）。

### 方案 B：雷池覆盖 XFF、只透传 CF 边缘 IP

如果第 3 节抓包发现 XFF 里没有真实客户端，只有 CF 边缘 IP / `CF-Connecting-IP`：

- 443 server（只接 CF）改用 `real_ip_header CF-Connecting-IP;`，并仍信任回环 + CF 网段；
- 17777 server（直连）继续用 `X-Forwarded-For`；
- 两个 server 分开写，不要把 `CF-Connecting-IP` 放到 17777 上，否则直连流量拿不到该头会退回回环地址。

## 5. 上线顺序（old 机）

1. 备份：`sudo cp -a /etc/nginx /root/nginx-backup-$(date +%Y%m%d%H%M)`；
2. 新建 `/etc/nginx/conf.d/realip.conf`（方案 A 或 B，二选一），确认没有和已有 `set_real_ip_from` 冲突；
3. `sudo nginx -t`，报错就停，恢复备份；
4. `sudo systemctl reload nginx`（不要 restart，避免断长连接/SSE）；
5. 按第 6 节验证两类入口；
6. 观察 10 分钟：`tail -f /var/log/nginx/access.log`，确认第一列不再是 `127.0.0.1`，且没有把正常管理员 IP 记成 CF 边缘 IP；
7. 记录上线时间与 Cloudflare 网段版本到变更单。

不要在同一次操作里改业务路由、upstream 或雷池站点配置；真实 IP 修复单独发。

## 6. 验证

从**两台不同公网出口**的机器（或手机热点）各打一次：

```bash
# 经 Cloudflare
curl -s -o /dev/null -w '%{http_code}\n' https://kedaya.ai/health
# 直连 17777（大用户绕 CF 的入口）
curl -s -o /dev/null -w '%{http_code}\n' http://<old 公网 IP>:17777/health
```

在 old 机看日志第一列：

```bash
sudo tail -n 20 /var/log/nginx/access.log
```

- 两行都应是你两台机器的**真实公网 IP**（不是 `127.0.0.1`，也不是 Cloudflare 边缘 IP）；
- 伪造测试：`curl -H 'X-Forwarded-For: 198.51.100.7' http://<old IP>:17777/health`，日志里应是发起机器的真实 IP，而不是 `198.51.100.7`；
- 若直连入口显示 CF 边缘 IP 或回环，说明雷池没追加/覆盖了 XFF，回到第 3 节重新抓包并按方案 B 调整。

## 7. 回滚

```bash
# 方式一：删掉本次新增片段
sudo rm -f /etc/nginx/conf.d/realip.conf

# 方式二：整目录恢复备份（更稳）
sudo rm -rf /etc/nginx && sudo cp -a /root/nginx-backup-<时间戳> /etc/nginx

sudo nginx -t && sudo systemctl reload nginx
sudo tail -n 5 /var/log/nginx/access.log   # 第一列应回到 127.0.0.1
```

回滚只影响日志里的客户端 IP，不影响业务路由；如果 reload 后出现 502/504，先 `nginx -t` 和 `docker ps | grep safeline` 确认链路，不要继续改别的配置。

## 8. 与告警的联动

真实 IP 生效后，管理端 401/403 探测告警才会按真实来源 IP 聚合：

- 设置项 API：`GET/PUT /api/v1/admin/settings/admin-auth-probe`
  （`enabled` 默认 true，`threshold` 默认 20，`distinct_paths` 默认 5，窗口默认 5 分钟）；
- 命中后写 `ops_alert_events`（规则名 `admin_auth_probe_detected`）并记审计动作
  `admin.auth_probe.alert`；
- 事件只含 IP、UA 前 64 字符、命中路径、状态码统计，不含任何 token/header 值。

## 9. 已知边界

- 雷池自身如果配置了「获取真实 IP」并改写 XFF，nginx 侧只能按它给的链解析；先抓包再改。
- `old` 的 `80/443` 属于 `safeline-tengine` 容器，`nginx -T` 里看不到它的配置，别把 `real_ip` 写进容器里（容器重建会丢）。
- 不做 `proxy_protocol` 改造；本次只解析 HTTP 头，不碰雷池监听方式。
- 不把 Cloudflare 网段写成 `0.0.0.0/0`；错误信任所有来源会让伪造 XFF 生效。
