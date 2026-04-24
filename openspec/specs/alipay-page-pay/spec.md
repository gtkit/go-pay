# alipay-page-pay Specification

## Purpose
TBD - created by archiving change add-aggregate-qr-orchestration. Update Purpose after archive.
## Requirements
### Requirement: Unified order exposes Alipay page payment
系统 MUST 提供统一交易类型 `page`，用于表达支付宝 PC 页面支付，而不是复用 `h5`。

#### Scenario: Caller requests Alipay page payment
- **WHEN** 调用方向统一下单请求中传入 `ChannelAlipay` 和 `TradeTypePage`
- **THEN** 系统 MUST 将该请求识别为支付宝 PC 页面支付场景

#### Scenario: Non-Alipay provider does not support page payment
- **WHEN** 调用方向非支付宝渠道传入 `TradeTypePage`
- **THEN** 不支持该交易类型的 provider MUST 返回 `ErrUnsupportedType`

### Requirement: Alipay page payment returns a redirect URL
当支付宝 provider 处理 `TradeTypePage` 时，系统 MUST 调用支付宝页面支付能力并在统一响应中返回跳转 URL。

#### Scenario: Successful page payment order returns PayURL
- **WHEN** 支付宝页面支付下单成功
- **THEN** `UnifiedOrderResponse.PayURL` MUST 包含可直接跳转的支付链接

#### Scenario: Return URL and metadata are forwarded
- **WHEN** 请求中提供 `ReturnURL`、`Metadata` 或 `ExpireAt`
- **THEN** 系统 MUST 将这些字段按现有支付宝统一下单语义映射到页面支付请求

