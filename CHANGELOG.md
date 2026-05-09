# Changelog

本文件记录 `go-pay` 的公开变更。版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Fixed

## [v1.3.1] - 2026-05-09

### Fixed

- 修复支付宝 JSAPI 下单缺失 `product_code` 和 `op_app_id` 必填参数导致网关拒绝的问题。`product_code` 固定填 `JSAPI_PAY`，`op_app_id` 默认取 `Config.AppID`（单商户单小程序场景一致）。该 bug 自 v1.2.x smartwalle SDK 时代潜伏，因新 gopay v3 网关参数校验更严而暴露；v1.3.0 沙箱验证发现并修复。下游单应用场景零代码变更。

## [v1.3.0] - 2026-05-09

### Added

- `paymgr` 新增 `ChannelWechatV2` 常量（值 `"wxpayv2"`），作为微信 V2 协议扩展点占位。本库**不实装** V2 provider，业务方如有 V2 商户号需求可自行实现 `paymgr.Provider` 接口注册到 manager（详见 README 第 14 章）。

### Changed

- ⚠ 支付宝底层 SDK 由 `github.com/smartwalle/alipay/v3`（OpenAPI 1.0 网关）切换为 `github.com/go-pay/gopay/alipay/v3`（OpenAPI v3 RESTful + 必要的 1.0 网关方法）。下游公开 API（`alipay.NewProvider` / `alipay.NewProviderWithConfig` / `alipay.Config` / `alipay.WithXxx`）签名 100% 不变；`paymgr` 8 个统一 API 输入输出语义保持稳定。
- 渠道错误的原始错误来源从 smartwalle 错误变为 gopay v3 `ErrResponse`，错误文案格式可能与 v1.2.x 略有差异。下游若对错误文案做字符串匹配，建议改为结构化 `errors.As(err, &chErr); chErr.Code == "..."` 判断（见 README 第 15.3 节升级指南）。
- README 顶部重写项目定位段，明确「业务统一抽象层」身份；新增第 14 章「微信 V2 接入扩展点」、第 15 章「v1.3.0 升级指南」；第 7 章支付宝配置改写为证书模式优先。

### Deprecated

- ⚠ 支付宝**普通公钥模式**（仅设置 `Config.AlipayPublicKey` 字段、未提供证书）软降级。`alipay.WithAlipayPublicKey(...)` Option 与 `Config.AlipayPublicKey` 字段保留仅用于编译兼容，运行时 `NewProvider` / `NewProviderWithConfig` 会返回包装了 `paymgr.ErrNotSupported` 的错误，文案明确指引使用证书模式（`WithCertMode` / `WithCertModePaths`）。原因：支付宝 OpenAPI v3 协议要求用证书计算 `cert_sn` 序列号，普通公钥模式无法生成 sn，是协议级约束。下游升级方法见 README 第 7.3.2 节。

## [v1.2.1] - 2026-04-27

### Added

- 新增 `aggregate` 包的 Example 测试和 Benchmark，用于展示聚合二维码编排的典型调用方式并记录核心路径性能基线。
- 新增 `paymgr.Manager` 与 `Provider` 的契约测试，覆盖统一下单前置校验、未注册渠道、Provider 错误透传和响应返回行为。
- 新增 GitHub Actions CI 门禁，自动运行 `go vet`、race 测试、coverage smoke 和 `golangci-lint`。

### Changed

- README 新增下单字段与返回矩阵，明确微信 / 支付宝各交易类型的额外必填字段和重点返回字段。

## [v1.2.0] - 2026-04-24

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

### Fixed

- `alipay.Provider.UnifiedOrder` 现为 `app`、`h5`、`page` 三种跳转/调起支付场景补齐 `product_code`，避免生成可签名但网关拒绝的请求。
- `aggregate.Service.Resolve` 在需要真实下单时会显式校验 `Manager` 是否为空，避免调用方错误初始化时触发空指针 panic。

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

[v1.3.1]: https://github.com/gtkit/go-pay/releases/tag/v1.3.1
[v1.3.0]: https://github.com/gtkit/go-pay/releases/tag/v1.3.0
[v1.2.1]: https://github.com/gtkit/go-pay/releases/tag/v1.2.1
[v1.2.0]: https://github.com/gtkit/go-pay/releases/tag/v1.2.0
[v1.1.0]: https://github.com/gtkit/go-pay/releases/tag/v1.1.0
[v1.0.3]: https://github.com/gtkit/go-pay/releases/tag/v1.0.3
