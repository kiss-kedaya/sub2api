# 独立签名策略与预算迁移

本次只改 signer 和示例策略，没有改运行服务、真实策略、真实数据库或 PostgreSQL 迁移。

## 给支付接线主代理的契约

- 后台必须停止自动授权：不得再把收款/归集地址同步到 `crypto.signer.policy`。后台可保存归集目的地，但 signer 仅接受独立文件中已审批的目标。
- 后台不得编辑 signer 的 `hot_index`，也不得把用户填写的归集接收地址作为 gas 付款地址。`GET /hot-address` 原样返回 `index`、`evm`、`solana`，表示文件策略 `hot_index` 对应的 **hot seed gas 出资钱包**。查询付款 nonce/余额时用这个地址。归集目标可以是另一个冷钱包。
- `kind=gas` 无条件要求 native、有效 `gas_index` 和对应 deposit seed 派生地址。无论目标是否命中白名单，都忽略请求的 `index`，使用文件 `hot_index` 和 hot seed 签名。
- `/sign/evm`、`/sign/sol` 的请求字段和成功响应 `signed_tx` 不变。`/healthz` 原 `ok/unlocked` 不变，新增 `signing_enabled`；解锁成功不代表已经有签名授权。

## 独立文件与启动行为

新增参数：

| 参数 | 行为 |
| --- | --- |
| `-policy` | 独立审批的本地绝对路径，文件只读；不提供时只保留健康、解锁和充值地址派生，签名与 gas 地址接口返回 503 |
| `-state` | signer 独占权限目录内的预算 SQLite 绝对路径；有策略时必须提供，文件必须预先初始化 |
| `-init-state` | 独立离线操作：在 `-state` 新建零用量账本后退出；已有任何同名文件均拒绝覆盖，不需要连接业务库 |
| `-socket-mode` | 仅接受 `0600` 或 `0660`，默认 `0600`；不同 UID 通过专用共享 IPC 组连接时显式使用 `0660`；其它值在任何初始化、DB 连接或 socket 操作前拒绝 |

指定策略但路径、JSON、地址、费率上限等无效时直接启动失败，不回退 DB 策略。未提供策略时只在启动读取旧 DB 策略用于诊断；DB 记录有效也不会开启签名。每个请求都不再读 DB 策略。策略在启动时加载为固定快照，**修改审批文件后必须由独立运维重启 signer**，后台保存或本轮调用不会改变已批准策略。

Linux 文件要求：策略 `0440/0444`，所在目录禁止 group/world 写入；预算文件 `0600`，所在目录 `0700`。两者拒绝符号链接文件和直接父目录。目录及其所有上级路径也必须由可信运维控制，不能把它们放在 web 可替换的目录树中。

**文件权限检查不能证明不同进程的身份隔离**：必须用不同 OS UID/ACL，把策略目录与状态目录从 web 容器/用户的可写范围移除；策略推荐仅向 signer 只读挂载，预算仅向 signer 可写挂载，web 不挂载。signer 不能与 web 共用能 chmod/替换这些文件的身份。Windows 需要独立账户 ACL，仅设置只读属性不构成权限边界。程序没有“通过配置路径证明 web 无写权限”的能力；部署验收要从实际 web 身份验证。

### 不同 UID 的 socket 接线（Linux）

默认 socket `0600` 仅允许同 UID 访问。部署不同 UID 时，单独建共享 IPC 组，例如 `sub2api-signer-ipc`，只加入 signer 和 web 两个服务身份；通过 `-socket-mode 0660` 放开这个组的 socket 连接权限。参数不会自动创建组、添加成员或 chown，部署方必须确保 socket 的实际属组正确。

建议权限布局如下；共享的是 IPC 连接权，**不是策略或账本的写权限**：

| 对象 | owner:group | mode / 说明 |
| --- | --- | --- |
| signer 服务 | `sub2api-signer:sub2api-signer-ipc` | 独立 UID，以 IPC 组为有效主组；另加入仅 signer 可读策略的 `sub2api-signer-policy` 组 |
| web 服务 | `sub2api-web:sub2api-web` | 独立 UID，仅附加 IPC 组；不得加入策略读取组 |
| socket 目录 `/run/sub2api-signer` | `sub2api-signer:sub2api-signer-ipc` | `0750`，signer 可创建 socket，web 只能遍历/查找，不能替换 socket |
| socket `/run/sub2api-signer/signer.sock` | `sub2api-signer:sub2api-signer-ipc` | 启动参数 `-socket /run/sub2api-signer/signer.sock -socket-mode 0660`；由 signer chmod 后才启动 HTTP 服务 |
| state 目录 `/var/lib/sub2api-signer` | `sub2api-signer:sub2api-signer-ipc` | **始终 `0700`**，即使属组为 IPC 组也不给组访问权 |
| state 文件 `/var/lib/sub2api-signer/budget.sqlite` | signer 拥有 | **始终 `0600`**；web 不可读写，也不能访问 SQLite journal 等辅助文件 |
| policy 目录 `/etc/sub2api-signer` | `root:sub2api-signer-policy` | `0750`，仅 root 可改目录，web 无权限 |
| policy 文件 `/etc/sub2api-signer/policy.json` | `root:sub2api-signer-policy` | `0440`，root 管理审批，signer 只读，web 无权限；容器内仍只读挂载 |

可用 systemd 的 signer `User=sub2api-signer`、`Group=sub2api-signer-ipc`、`SupplementaryGroups=sub2api-signer-policy`、`RuntimeDirectory=sub2api-signer`、`RuntimeDirectoryMode=0750`，并给 web 的 `SupplementaryGroups` 加 IPC 组。变更组成员后需要重启对应服务以取得新的进程组身份；容器部署要对齐共享 socket 的数值 GID。若改用 setgid 的专用 socket 目录继承组，也必须核对最终 socket 属组，不能假定父目录组会自动继承。

验收从实际 web UID 检查 `/healthz` 或 `/hot-address` 能连接，同时确认不能读取/修改 state、不能读取/替换 policy、不能删除或替换 socket。不要用 `0777/0666` socket、放宽 state 的 `0700/0600`、共享策略写权限或让 web 与 signer 共用 UID 来修复连接。Windows 的 AF_UNIX 跳过 POSIX chmod，`-socket-mode` 仍严格校验，但访问控制必须使用 Windows ACL，不能依赖 `0660` 建立隔离。

## EVM 预算字段与兼容

`deploy/signer-policy.example.json` 仅是示例，不是真实批准策略。

新增 `evm_chains`，key 为正的十进制 chain ID。每链必填：

- `max_gas_limit`：21000 到 1000000。
- `max_gas_price`：正的 uint256 十进制整数，单位 wei/gas，同时限制签出交易的 `GasFeeCap/GasTipCap`。
- `max_fee`：正的 uint256 十进制整数，单位 wei，限制 `gas_limit * fee_cap`。

未配置的链、不完整的上限、缺失 token/native 的单笔或日限额都拒签。RPC 提供的费率只能在审批上限内使用。当前仍签 EIP-1559 交易，未新增 legacy 类型或更改客户端协议。

- native 转账单笔与日用量均为 `value + gas_limit * fee_cap`。
- ERC20 同时占用 token 数额预算和 native 最高手续费预算；两项在一个事务内检查、更新。
- 保留原有 `max_per_tx/max_daily` token key：`native` 或小写 EVM 合约；它们的日用量跨 EVM 链共同累计。
- 可选 `evm:56:native`、`evm:56:0x...` 等链作用域 key；只要任一限额使用该 key，单笔与日限额都必须完整配置，不会因另一项缺失回退到旧 key。
- 切换旧 key 到链作用域 key 会改变计数桶，应在停签后跨 UTC 日界线切换，或由独立运维保守迁入已签用量；不能当作无损热更新。
- 金额、费率及费用乘积/总成本超过 uint256 拒绝；JSON nonce/gas_limit 超 uint64、index 超 uint32 拒绝，派生 index 限定非 hardened 的 `0..2^31-1`。

**参数兼容不等于旧策略可继续签名**：旧策略没有链/费率上限、只有单笔没有日限额时，现在明确拒签，必须审批后补齐。

## Solana 边界

默认未配置 `solana`，该接口拒签；这不表示收款扫描已支持 Solana。显式开启需提供 `max_fee_lamports`，SPL 还需 `ata_rent_lamports`。预算 key 必须是 `solana:native` 和 `solana:<原样 mint>`，不与 EVM 的 wei 共用计数。SPL 每次保守预留完整 ATA rent，实际账户已存在也不退预算；当前构造器只支持 6 位 decimals，不能把别的 mint 当成已支持。

SPL `transferChecked` 修正为 `source ATA, mint, destination ATA, owner`；base58 目标和 mint 按大小写精确匹配，EVM 按地址字节匹配。native/SPL 金额及 native 总预算超过 uint64 拒绝。Solana 本身没有本接口可设置的 gas fee cap；上述 fee/rent 是独立审批的保守预留值，启用前须按目标网络核定，不能声称它提供 EVM 同样的链上费用硬上限。

## 预算账本、失败和并发

只新增 signer 本地 SQLite 表 `signer_budget(singleton, version, state)`，版本 1。**不新增生产 PostgreSQL schema/迁移**；旧 settings 的 `crypto.signer.state` 不再参与限额授权，保留数据供迁移核对，不能靠修改它重置额度。

同一组种子的全部 signer 进程必须使用**同一台主机、同一个绝对路径的本地账本**。SQLite `BEGIN IMMEDIATE` 跨进程锁住完整“读额度—校验—签名—写额度—提交”，每次从持久层重新读，连接统一设置 busy timeout 与 synchronous FULL，分钟次数也持久化。启动不会根据进程内旧缓存覆盖状态。状态缺失、损坏、读/写/提交/锁超时、时钟倒退全部拒绝签出；下一 UTC 日才清空当日总量。

签名构造失败、额度拒绝不消耗预算；持久化失败不返回签名。提交成功但进程退出/HTTP 响应丢失时，预算保守占用；不要自动退款，否则已流出的签名可与释放的额度并用。重复请求保守重复计费，本接口没有交易任务幂等键，支付任务的恢复和去重由主代理处理。

账本不可放 NFS/网络共享，不支持不同主机各复制一份账本签同一组种子。若需要跨主机签名，需要另行设计独立、受控的共享事务账本；不能复用 web 可写 settings 证明隔离。具备可信运维权限的人主动恢复旧磁盘快照、复制账本或更换种子/账本组合仍能回退历史，程序不能对抗这种权限；必须把状态文件纳入不可回退的运维流程，禁止普通重启脚本重建它。

## 主代理迁移文档必须包含

1. 停止旧签名/归集入口，记录旧 UTC 当日已签额度与在途签名；停止后台自动授权和 hot_index 编辑。
2. 独立复核链、token、目标、gas 出资派生索引、单笔/日/费率上限，部署只读文件；从 web 实际身份确认无法改文件、替换目录或接触预算账本。
3. 新安装可离线 `-init-state`；现有安装不能在当日直接新建零账本。推荐停签直到下一 UTC 日再初始化/恢复，旧系统尚未广播的签名仍应按风险保守占用下一日预算或等其不可再执行。若必须当日迁移，需独立运维先迁入保守累计额并核对，不提供后台清零/导入接口。
4. 保留旧状态作为审计证据；使用 `-policy/-state` 启动后核对 `signing_enabled`，确认 `/hot-address` 与 nonce/余额查询地址一致，测试未审批目标/链、超费率/预算都拒签。
5. 重启和多个进程验证额度不重置；演练磁盘失败和签名失败。真实小额交易/广播与生产上线需要另行授权，本次只完成本地签名和故障回归。

回滚二进制到旧版本会恢复 DB 策略/旧额度逻辑，不能视为安全回滚。出现问题先停签，保留新账本与签名任务证据，再独立决定兼容策略；不允许自动切回能绕过预算的旧 signer。
