# Alipay TradePagePay Design

**Date:** 2026-04-24

## Goal

为 `go-pay` 的统一下单接口新增支付宝 PC 页面支付能力，基于 `github.com/smartwalle/alipay/v3` 的 `TradePagePay` 接口实现，并保持最小 API 侵入。

目标效果：

- 调用方继续通过 `paymgr.Manager.UnifiedOrder(...)` 发起下单
- 新增统一交易类型 `paymgr.TradeTypePage`
- 支付宝渠道下返回 `UnifiedOrderResponse.PayURL`
- 不新增高级 PC 页面专有参数

## Context

当前统一层已经支持：

- 微信：`app`、`jsapi`、`native`、`h5`
- 支付宝：`native`、`jsapi`、`app`、`h5`

支付宝底层 SDK 还提供 `TradePagePay`，用于 PC 收银台网页支付。现有统一响应结构已经有 `PayURL` 字段，且支付宝 H5 已在复用该字段，因此接入 `TradePagePay` 不需要引入新的响应结构。

## Non-Goals

本次不做以下内容：

- 不暴露 `QRPayMode`
- 不暴露 `QRCodeWidth`
- 不暴露 `IntegrationType`
- 不新增 `alipay.Provider.PagePay(...)` 渠道专有扩展方法
- 不调整 `paymgr.Provider` 接口方法集合
- 不为微信引入 `page` 交易类型实现

## Approaches Considered

### Option A: 新增统一 `TradeTypePage`

做法：

- 在 `paymgr.TradeType` 中新增 `TradeTypePage`
- 在 `alipay.Provider.UnifiedOrder` 中新增 `TradeTypePage` 分支
- 继续复用 `UnifiedOrderResponse.PayURL`

优点：

- 语义准确，调用方能明确表达“PC 页面支付”
- 统一层保持完整，不需要分渠道走特殊方法
- 与现有 H5 跳转 URL 模型兼容，改动小

缺点：

- 需要扩充公共枚举、测试和文档

### Option B: 复用 `TradeTypeH5`

做法：

- 调用方继续传 `TradeTypeH5`
- 支付宝内部根据场景切到 `TradePagePay`

优点：

- 表面上改动更少

缺点：

- 语义错误，H5 与 PC 页面支付场景不同
- 调用方无法区分手机网页和 PC 收银台
- 文档和接入代码会长期混乱

### Option C: 仅加 `alipay.Provider.PagePay(...)`

做法：

- 保持统一层不变
- 只在支付宝 provider 增加渠道专有方法

优点：

- 不动 `paymgr` 结构

缺点：

- 破坏统一抽象收益
- 调用方需要按渠道写分支
- 与本项目定位不一致

## Recommendation

采用 **Option A**。

原因：

- 这是最符合当前库定位的方案：统一抽象中继续承载跨渠道的主支付能力
- `TradePagePay` 的输出可以直接映射到现有 `PayURL`
- 本次无需引入复杂的 PC 页面高级参数，能够保持 API 稳定和最小实现

## Proposed API

### Trade Type

在 `paymgr/payment.go` 中新增：

```go
TradeTypePage TradeType = "page" // PC 网页支付 / 支付宝收银台
```

### Request

继续复用 `paymgr.UnifiedOrderRequest`，不新增字段。

本次 `TradeTypePage` 使用的字段：

- `OutTradeNo`
- `TotalAmount`
- `Subject`
- `NotifyURL`
- `ReturnURL`
- `ExpireAt`
- `Metadata`

不使用的字段：

- `ClientIP`
- `OpenID`

### Response

继续复用 `paymgr.UnifiedOrderResponse`：

- 支付宝 `TradeTypePage` 返回 `PayURL`

不新增任何返回字段。

## Provider Behavior

### Alipay

在 `alipay.Provider.UnifiedOrder(...)` 中新增 `TradeTypePage` 分支：

- 构造 `alipay.TradePagePay`
- 映射订单号、金额、标题、回调地址、返回地址、附加参数、超时时间
- 调用 `p.client.TradePagePay(...)`
- 将返回 URL 写入 `resp.PayURL`

### Wechat

`wechat.Provider.UnifiedOrder(...)` 不新增 `TradeTypePage` 分支。

调用方若在微信渠道上传入 `TradeTypePage`，继续返回 `paymgr.ErrUnsupportedType`。

## Field Mapping

支付宝 `TradePagePay` 的最小映射策略：

- `OutTradeNo` -> `Trade.OutTradeNo`
- `TotalAmount` -> `Trade.TotalAmount`
- `Subject` -> `Trade.Subject`
- `NotifyURL` -> `Trade.NotifyURL`
- `ReturnURL` -> `Trade.ReturnURL`
- `ExpireAt` -> `Trade.TimeoutExpress`
- `Metadata` -> `Trade.PassbackParams`

金额仍沿用现有分转元逻辑。

`ReturnURL` 为空时不强制传值，保持与现有支付宝 H5 分支一致。

## Validation

统一请求校验不新增 `TradeTypePage` 专属必填规则。

理由：

- 现有 `UnifiedOrderRequest.Validate()` 已覆盖本次必需字段
- `ReturnURL` 对 `TradePagePay` 来说不是绝对必填
- 不需要像微信 `jsapi` / `h5` 那样追加渠道特有强校验

## Backward Compatibility

本次改动是向后兼容的：

- 现有 `TradeTypeNative` / `TradeTypeJSAPI` / `TradeTypeApp` / `TradeTypeH5` 行为不变
- `paymgr.Provider` 接口方法集合不变
- 调用方仅在需要支付宝 PC 页面支付时才会使用 `TradeTypePage`

唯一的公共 API 变化是新增一个 `TradeType` 常量。

## Documentation Changes

需要同步更新：

- `README.md`
  - 交易类型列表增加 `TradeTypePage`
  - 支付宝支持矩阵增加 `page`
  - 新增支付宝 PC 页面支付示例
- `example/main.go`
  - 返回 `pay_url` 的注释由“支付宝 H5”扩展为“支付宝 H5 / PC 页面支付”
- `CHANGELOG.md`
  - 记录新增支付宝 `TradeTypePage` 支持

## Testing Strategy

采用 TDD，先写失败测试，再补实现。

### Unit Tests

需要补充：

- `paymgr/payment_test.go`
  - 新增 `TradeTypePage` 常量后的兼容性验证
- `alipay/provider_test.go`
  - `TradeTypePage` 返回 `PayURL`
  - `ReturnURL` 能正确映射
  - `Metadata` 会进入 `PassbackParams`
  - `ExpireAt` 会映射为 `TimeoutExpress`

### Regression Expectations

必须确认：

- 现有支付宝 `TradeTypeH5` 仍返回 `PayURL`
- 微信渠道对 `TradeTypePage` 仍返回 `ErrUnsupportedType`
- 全量 `go test ./...` 通过

## Risks

### Risk 1: H5 与 Page 语义混淆

缓解：

- 独立新增 `TradeTypePage`
- README 中明确区分“手机网页支付”和“PC 页面支付”

### Risk 2: 未来需要支付宝 Page 专有参数

缓解：

- 本次明确限定为最小可用版
- 若后续出现真实需求，再评估是否为 `UnifiedOrderRequest` 增加高级字段，或新增支付宝扩展接口

### Risk 3: 调用方误以为微信也支持 `page`

缓解：

- 支持矩阵文档明确按渠道区分
- 微信渠道保持 `ErrUnsupportedType`

## Implementation Scope

本次预计修改文件：

- `paymgr/payment.go`
- `paymgr/payment_test.go`
- `alipay/provider.go`
- `alipay/provider_test.go`
- `README.md`
- `example/main.go`
- `CHANGELOG.md`

## Acceptance Criteria

满足以下条件视为完成：

- 存在 `paymgr.TradeTypePage`
- 支付宝 `UnifiedOrder` 支持 `TradeTypePage`
- 返回值通过 `UnifiedOrderResponse.PayURL` 暴露
- 文档和示例同步更新
- 全量测试通过
