# models.dev 价格覆盖预览

在 `backend` 目录运行合成样例。

```text
go run ./cmd/models-pricing-preview -snapshot cmd/models-pricing-preview/fixture-snapshot.json -inventory cmd/models-pricing-preview/fixture-inventory.json
```

`-snapshot` 接收固定的 models.dev `api.json` 原始文件。`-inventory` 接收脱敏 JSON，顶层为 `rows` 数组。各行填写 `provider_id`、实际 `billing_model_id`、`mode`、`currency`、`unit`、`required_fields`。`row_id` 可选。`structure` 默认 `flat`。`explicit_overrides` 可选，将已由显式价卡覆盖的**必需项**映射为 `group` 或 `channel`。清单按实际计量维度填写，覆盖单个字段时仍检查其余字段。

首方供应商 ID 与模型键按快照原文精确匹配。预览比较扁平 `token` 报价，清单币种为 `USD`，单位为 `per_million_tokens`。快照沿用 models.dev 扁平 `cost` 的美元每百万 token 单位，并检查来源的显式币种、单位和报价结构声明。支持的字段有 `input`、`output`、`cache_read`、`cache_write`、`reasoning`。超出形状、币种或单位的条目会标记为 `incompatible`。

报告直接输出到标准输出。`input_row` 为从 1 开始的数组下标，输入字节由 `snapshot_sha256` 和 `inventory_sha256` 标识。`summary` 按行统计。`fields` 逐项给出状态、原因或显式覆盖来源。整行由显式价卡覆盖时为 `explicit_override`，否则遇到不兼容时为 `incompatible`，遇到缺项时为 `missing`，其余为 `comparable`。报价里的 `null` 表示该项没有可比较的来源报价，数值 `0` 表示快照明确列零。输入错误写标准错误并以非零状态退出。
