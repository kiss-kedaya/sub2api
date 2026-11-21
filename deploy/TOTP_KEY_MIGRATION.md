# 运维：把 TOTP 安装密钥从数据库迁移到部署配置

目标：让 `TOTP_ENCRYPTION_KEY`（64 位十六进制，32 字节）成为 2FA、支付配置与钱包密文的唯一解密来源，数据库里的过渡行只作为回退，最后删除。

> 不要轮换密钥。新值无法解密任何已有密文。本迁移只是把**同一个值**换一个存放位置。

## 加载顺序（本次改动后）

1. 部署配置：`TOTP_ENCRYPTION_KEY` 环境变量，或 `config.yaml` 的 `totp.encryption_key`。**优先，权威。**
2. 数据库 `security_secrets.totp_encryption_key`（`key='totp_encryption_key'`）。仅在部署配置缺失时使用，启动日志打印 `Warning: ... using the legacy installation key stored in security_secrets`。
3. 两边都没有：若已有任何持久密文，启动直接失败并提示缺少原密钥（不会生成新密钥、不会清除任何数据）；若数据库是全新的，则生成一条并写入数据库（首次启动兼容路径，日志会提示导出）。

部署配置存在但与数据库行不一致时：**以部署配置为准**，启动日志 WARN。此时代码不会改写数据库行，方便回滚。

## 迁移步骤

1. **备份数据库**（至少 `security_secrets` 与 `users` 两张表，生产要求完整备份）。
2. 在服务器上导出过渡密钥（只读，不写库）：

   ```bash
   docker exec <容器名> /app/sub2api -export-totp-key -confirm
   ```

   输出包含：
   - `TOTP_ENCRYPTION_KEY=<64 hex>`
   - 用该密钥实际解密了多少条 `users.totp_secret_encrypted`（失败条数 > 0 时禁止继续）。
   - 如果部署环境已经设置了不同的 `TOTP_ENCRYPTION_KEY`，会直接警告（环境值优先，已有 2FA 会失效）。

3. 把 `TOTP_ENCRYPTION_KEY=...` 追加到部署环境文件（如 `/opt/sub2api/.env`，或 `config.yaml` 的 `totp.encryption_key`）。不要贴进聊天工具、不要提交到 git、不要留在 shell history。
4. 重启**每一台**实例。启动日志不应再出现 `using the legacy installation key stored in security_secrets`；出现说明该实例没读到环境变量。
5. 验证：管理员 2FA 登录一次；打开后台加密支付页确认钱包状态/地址正常。
6. 所有实例都确认读 env 之后，删除过渡行：

   ```sql
   DELETE FROM security_secrets WHERE key = 'totp_encryption_key';
   ```

7. 再重启一次，确认启动无 TOTP 密钥相关 WARN，2FA 与钱包仍正常。

## 回滚

删除数据库行之前：把环境变量去掉并重启即可回到数据库回退路径（行还在，原值不变）。

删除数据库行之后：必须从备份恢复该行，或把 `TOTP_ENCRYPTION_KEY` 原值重新写回部署配置。**没有原值时任何已有 2FA/钱包密文都无法解密**，只能走各用户重新绑定 2FA、钱包重新初始化（需人工确认资金地址）。

## 多实例提示

- 灰度/滚动发布期间，新老二进制可以共存：老二进制读数据库行，新二进制优先读 env；只要 env 与数据库行是同一个值，行为一致。
- 数据库行删除必须等所有实例都升级并重启完成。
