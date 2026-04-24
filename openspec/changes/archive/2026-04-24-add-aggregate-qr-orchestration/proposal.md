## Why

`go-pay` 目前只提供单渠道下单能力，业务层如果要做“一个聚合二维码，扫码后再按环境分流到微信或支付宝”的体验，只能在项目外自己重复判断 User-Agent、拼接 `channel + trade_type`、处理不同返回形态，导致接入成本高且容易出现分流不一致。

当前聚合二维码设计还依赖支付宝 PC 页面支付能力，但统一层尚未提供 `TradeTypePage`，因此需要把该前置能力与聚合编排一起纳入本次变更，确保完整链路可落地。

## What Changes

- 新增 `aggregate` 聚合支付编排层，用于识别扫码环境并决定实际下单渠道与交易类型。
- 新增 `aggregate.Service.Resolve(...)`，统一输出“选择渠道 / 跳转 / 二维码 / JSAPI”四类动作结果。
- 新增环境识别与聚合错误定义，包括缺失 OpenID、非法渠道选择、缺失订单构造器等显式错误。
- 为统一支付层新增 `paymgr.TradeTypePage`，表示支付宝 PC 页面支付。
- 为支付宝 provider 新增 `TradeTypePage` 支持，返回 `UnifiedOrderResponse.PayURL`。
- 补充 README、示例代码与测试，覆盖聚合编排与支付宝 page 能力。

## Capabilities

### New Capabilities
- `aggregate-qr-orchestration`: 聚合二维码支付编排，基于访问环境和用户选择决定真实支付单的渠道、交易类型和动作结果。
- `alipay-page-pay`: 支持支付宝 PC 页面支付，并在统一下单响应中返回可跳转的支付链接。

### Modified Capabilities

## Impact

- 受影响代码：
  - `aggregate/` 新包
  - `paymgr/payment.go`
  - `alipay/provider.go`
  - 对应测试、README 和 `example/main.go`
- 对外 API：
  - 新增 `paymgr.TradeTypePage`
  - 新增 `aggregate` 包公开类型与服务 API
- 运行行为：
  - 保持现有单渠道二维码能力不变
  - 新增聚合二维码场景下的统一编排能力
