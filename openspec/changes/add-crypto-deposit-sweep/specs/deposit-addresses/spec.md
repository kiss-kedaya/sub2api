## ADDED Requirements

### Requirement: 地址派生必须由签名服务独占执行
系统 SHALL 通过签名服务按 BIP39/BIP32 派生地址；Web 进程 MUST NOT 持有种子、扩展私钥或任何私钥材料。EVM 地址 MUST 使用 `m/44'/60'/0'/0/{index}`，Solana 地址 MUST 使用 `m/44'/501'/{index}'/0'`，`index` 从 0 递增且全库唯一。

#### Scenario: 批量派生
- **WHEN** Web 进程请求派生 N 个新地址
- **THEN** 签名服务 MUST 返回 N 个地址及其 derivation_index
- **THEN** 同一 index 重复派生 MUST 返回相同地址（确定性）

#### Scenario: Web 进程无种子
- **WHEN** 审查 Web 进程代码与配置
- **THEN** MUST NOT 存在种子、助记词、私钥的读取或存储路径

### Requirement: 地址分配必须惰性且幂等
系统 SHALL 在用户首次请求直充地址时从地址池分配；同一用户在同一链族（EVM / Solana）MUST 始终得到同一地址；重复请求 MUST 幂等返回既有分配，不得消耗新池位。

#### Scenario: 首次分配
- **WHEN** 用户首次打开直充页且池中有空闲地址
- **THEN** 系统 MUST 分配一个地址并持久化 user_id 绑定
- **THEN** 返回该地址、链列表和二维码数据

#### Scenario: 重复请求
- **WHEN** 同一用户再次请求
- **THEN** MUST 返回既有绑定地址，MUST NOT 分配新地址

#### Scenario: 并发分配
- **WHEN** 同一用户并发发起两次首次请求
- **THEN** 系统 MUST 通过唯一约束/事务保证只产生一条绑定

#### Scenario: 池耗尽
- **WHEN** 池中空闲地址不足
- **THEN** 系统 MUST 触发同步补货（派生一批）；补货失败 MUST 返回可重试错误，MUST NOT 分配空地址

### Requirement: EVM 地址必须三链复用
系统 SHALL 让一个 EVM 派生地址同时用于 bsc、polygon、plasma；Solana 使用独立地址。直充页 MUST 按启用链展示对应网络与地址。

#### Scenario: 展示多链
- **WHEN** 用户打开直充页且启用链为 bsc/polygon/plasma/solana
- **THEN** 页面 MUST 显示一个 0x 地址（标注 BSC/Polygon/Plasma）和一个 Solana 地址

### Requirement: 地址状态必须可管理且回收不重发
系统 SHALL 支持地址停用（禁用后不再作为充值入口展示、扫描仍记录但标记）；回收地址 MUST 归档而 MUST NOT 重新分配给其他用户。

#### Scenario: 停用地址
- **WHEN** 管理员停用某地址
- **THEN** 该地址 MUST 从对应用户的直充页隐藏
- **THEN** 后续向该地址的转账 MUST 仍被记录（防止资金丢失）并进入人工对账

#### Scenario: 回收
- **WHEN** 地址归集完成且长期（默认 90 天）无到账
- **THEN** 状态 MAY 置为 retired 归档
- **THEN** MUST NOT 重新分配给其他用户
