# Changelog

本文件记录 `go-pay` 的公开变更。版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- `wechat.Provider.UnifiedOrder` 新增对 `paymgr.TradeTypeJSAPI` 和 `paymgr.TradeTypeH5` 的支持，分别接入官方 `payments/jsapi` 与 `payments/h5`。
- 微信 JSAPI 下单成功后返回 `UnifiedOrderResponse.JSAPIParams`，用于前端调起微信支付。
- 微信 H5 下单成功后返回 `UnifiedOrderResponse.H5URL`，用于移动浏览器拉起支付。
- `paymgr.TradeTypePage` 新增支付宝 PC 页面支付语义，`alipay.Provider.UnifiedOrder` 现支持 `TradeTypePage` 并返回 `UnifiedOrderResponse.PayURL`。
- 新增 `aggregate` 包，用于聚合二维码场景下的入口环境识别、渠道分流和统一动作结果编排。

### Changed

- 微信直连下单支持矩阵从 `app`、`native` 扩展为 `app`、`jsapi`、`native`、`h5`。
- `example/main.go` 与 README 示例补充了微信 JSAPI/H5 的必填字段说明：JSAPI 需要 `OpenID`，H5 需要 `ClientIP`。
- README 与示例新增支付宝 `page` 和聚合二维码编排说明，`pay_url` 字段语义扩展为支付宝 H5 / PC 页面支付跳转链接。

## [v1.1.0] - 2026-04-22

### Added

- `paymgr.Provider` 新增方法 `QueryRefund(ctx, *QueryRefundRequest) (*QueryRefundResponse, error)`，用于按商户退款单号查询退款状态。
- `paymgr.Provider` 新增方法 `ParseRefundNotify(ctx, *http.Request) (*RefundNotifyResult, error)`，用于解析退款异步通知。
- `paymgr` 包新增类型 `QueryRefundRequest` / `QueryRefundResponse` / `RefundNotifyResult`。
- `alipay.Provider` 实现 `QueryRefund`（调用 `alipay.trade.fastpay.refund.query`）。
- `wechat.Provider` 实现 `QueryRefund`（调用 `refunddomestic.QueryByOutRefundNo`）和 `ParseRefundNotify`（解密 `REFUND.SUCCESS` / `REFUND.ABNORMAL` / `REFUND.CLOSED` 事件）。
- `example/main.go` 新增 `GET /api/v1/refund` 和 `POST /api/v1/notify/refund/wechat` 路由，演示两个新方法的用法。
- README 新增 `9.5 查询退款` 和 `9.7 处理退款异步通知` 两节。

### Changed

- `alipay.ParseNotify`：当回调中 `GmtRefund` 或 `RefundFee` 非空时，`TradeStatus` 会显式映射为 `TradeStatusRefunded`。支付宝没有独立的退款通知端点，退款事件仍走原支付通知 URL。

### Fixed

- `alipay.decodePassbackParams`：修复对整串 URL-encoded 遗留格式（如 `"a%3D1%26b%3D2"`）的兼容分支走不到的问题。之前 `url.ParseQuery` 会把这类输入当成单 key 返回而提前 return，fallback 的 `QueryUnescape` 永远没机会执行；现通过新辅助函数 `hasEncodedSeparator` 探测后正确路由到 fallback 分支。该 bug 自 v1.0.3 起存在，仅影响 `ParseNotify` 返回的自定义 `Metadata`，不影响验签、金额、交易状态。

### Breaking Changes

- `paymgr.Provider` 接口新增两个方法。**自定义 Provider 实现者升级后会编译失败**，需要补齐 `QueryRefund` 和 `ParseRefundNotify`。
- `alipay.Provider.ParseRefundNotify` 按设计返回 `%w: ErrNotSupported`。支付宝退款结果复用支付通知端点，请在 `ParseNotify` 的返回值上检查 `TradeStatus == TradeStatusRefunded`。

### 迁移指南

如果你只是作为调用方使用 `alipay.Provider` / `wechat.Provider`，升级到 v1.1.0 无需任何代码改动。

如果你实现过自定义 `paymgr.Provider`：

```go
// 补齐这两个方法即可：
func (p *YourProvider) QueryRefund(ctx context.Context, req *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
    // 调用你接入的渠道查询接口
}

func (p *YourProvider) ParseRefundNotify(ctx context.Context, r *http.Request) (*paymgr.RefundNotifyResult, error) {
    // 若该渠道无独立退款通知，可直接返回 paymgr.ErrNotSupported
    return nil, fmt.Errorf("%w: ...", paymgr.ErrNotSupported)
}
```

## [v1.0.3] - earlier

详见 `git log v1.0.3`。

[v1.1.0]: https://github.com/gtkit/go-pay/releases/tag/v1.1.0
[v1.0.3]: https://github.com/gtkit/go-pay/releases/tag/v1.0.3
