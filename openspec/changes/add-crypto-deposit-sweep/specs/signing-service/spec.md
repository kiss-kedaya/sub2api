## ADDED Requirements

### Requirement: 签名服务必须是独立进程且持有全部私钥材料
系统 SHALL 提供独立二进制 `cmd/signer`；种子、扩展私钥、热钱包私钥 MUST 只存在于签名服务进程与其加密存储；签名服务 MUST 通过本机受限通道（unix socket）通信，MUST NOT 暴露公网端口。

#### Scenario: 部署形态
- **WHEN** 检查进程列表与监听端口
- **THEN** signer MUST 以独立 unit 运行且只监听本机 socket

### Requirement: 签名请求必须白名单与限额双控
签名服务 SHALL 只对以下操作签名：派生地址（只读）、归集转账到配置的热钱包白名单地址；单笔限额与日累计限额 MUST 可配；速率 MUST 限流。

#### Scenario: 合法归集签名
- **WHEN** Web 进程请求对某子地址→热钱包的转账签名，且在白名单与限额内
- **THEN** 签名服务 MUST 返回签名交易

#### Scenario: 非法目标
- **WHEN** 目标地址不在白名单
- **THEN** MUST 拒绝并审计

### Requirement: 签名服务必须全量审计
系统 SHALL 记录每次派生与签名的审计记录（时间、操作、地址、金额、结果）；审计 MUST 不包含种子或私钥原文。

#### Scenario: 审计可查
- **WHEN** 管理员查询签名审计
- **THEN** MUST 能看到每次签名的时间/地址/金额/结果，MUST NOT 看到任何私钥材料

### Requirement: 种子必须加密存储且支持离线备份
种子 MUST 以加密形式存储（密钥与数据分离，如 systemd 凭据/环境变量）；MUST 提供一次性种子生成工具用于初始化；备份 MUST 支持离线（助记词抄录）。

#### Scenario: 初始化
- **WHEN** 首次部署运行种子生成
- **THEN** MUST 输出助记词供离线备份，且存储的为加密形式

#### Scenario: 数据分离
- **WHEN** 审查 signer 配置
- **THEN** 加密密钥 MUST NOT 与密文同文件存放
