# Changelog

本文件记录 `go-pay` 的公开变更。版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Fixed

## [v1.6.0] - 2026-06-12

### Added

- 新增能力小接口 `paymgr.OrderProvider` / `paymgr.RefundProvider` / `paymgr.NotifyParser`，`paymgr.Provider` 重组为其组合（方法集不变，既有实现与调用方不受影响）；业务代码可按能力声明依赖
- 新增可嵌入基座 `paymgr.UnimplementedProvider`：自定义渠道嵌入后只需覆写支持的能力，未覆写的方法返回 `ErrNotSupported`，未来接口新增方法不再破坏外部实现的编译
- README 新增"自定义渠道接入"章节：最小实现步骤、契约 checklist 与 `aggregate` 聚合层的渠道边界说明
- 三个渠道的 `Config`（`alipay` / `wechat` / `wechat/v2`）实现 `fmt.Stringer` / `fmt.GoStringer`：以 `%v` / `%+v` / `%s` / `%#v` 打印时输出脱敏摘要，私钥与 API 密钥显示 `"****"`、证书内容仅标注 `<set>`，避免日志误打明文密钥（注意：`json.Marshal` 与反射遍历不受保护）

### Fixed

- 支付宝与微信 v3 Provider 直连调用（绕过 Manager）时补齐请求参数校验：nil 请求、非法金额、空退款单号等现在返回 `paymgr.ErrInvalidParam` 包装错误，不再 panic 或把非法请求发往渠道（与微信 v2 行为对齐）
- `paymgr.Manager.Register` 传入 nil Provider（含 typed nil）时安全忽略，不再 panic

## [v1.5.0] - 2026-06-11

### Added

- 新增哨兵错误 `paymgr.ErrChannelNotRegistered`，渠道未注册时可用 `errors.Is` 精确判断（原为裸字符串错误）
- 新增 `paymgr.CloseOrderRequest.Validate()`，与其余请求类型的校验契约对齐，直连 Provider 的调用方也能获得同等校验
- `paymgr.Manager` 零值即可直接使用（`var m paymgr.Manager` 后 `Register` 不再 panic）

### Changed

- **回调身份校验加强**：三渠道 `ParseNotify` / `ParseRefundNotify` 在验签之外增加事件类型与商户/应用身份校验——微信 v3 核对 `event_type` 前缀与解密后 `mchid`/`appid`，微信 v2 核对报文 `appid`/`mch_id`，支付宝核对 `app_id`；不符返回 `paymgr.ErrInvalidNotify`。退款通知错投支付端点、同商户号下其它应用的通知由"静默通过"改为报错
- 支付宝渠道 HTTP 客户端显式注入 30 秒超时（原使用无超时的 `http.DefaultClient`，网关挂起会导致调用方 goroutine 永久阻塞）
- 支付宝下单 `ExpireAt` 距今不足 1 分钟（含已过期）时返回 `paymgr.ErrInvalidParam`（原静默忽略或生成网关必拒的 `"0m"`）
- 微信 v2 支付通知 `result_code=FAIL` 时，失败原因 `err_code` / `err_code_des` 写入 `NotifyResult.Metadata` 便于排障
- 三渠道 `NewProviderWithConfig` 值拷贝传入的 `*Config`，构造后修改原 Config 不再影响 Provider、不再构成数据竞争

### Fixed

- 支付宝 `Refund` 响应的 `RefundAmount` 改为本次请求的退款金额——原实现误用渠道返回的 `refund_fee`（该字段是交易累计退款总额），多次部分退款时金额错误；同一退款单号重复调用的幂等返回（`fund_change=N`）语义在 GoDoc 与 README 中明确
- 微信 v2 `QueryRefund` 在响应明细中找不到目标退款单号时返回 `ErrOrderNotFound`——原实现回退取第 0 笔明细，一笔订单退款超过 10 笔时会返回其它退款单的状态与金额
- 微信 v3 `UnifiedOrder`（APP / Native）与 `Refund` 对 SDK 响应指针字段增加 nil 守卫，渠道返回成功但缺字段时不再 panic
- 微信 v2 `WithNotifyURL` 配置的默认通知地址现在真实生效——原实现的回退逻辑位于参数校验之后，永不可达
- `aggregate.Service.Resolve` 的全部纯校验移到 `BuildUnifiedOrder` 回调之前执行，服务配置错误时不再触发带副作用（如订单落库）的回调
- 支付宝 JSAPI 下单补传 `passback_params`，调用方 Metadata 不再被静默丢弃
- 支付宝 `QueryRefund` 缺 `OutRefundNo` 时返回 `ErrInvalidParam`，不再与"退款单不存在"混淆
- 微信 v2 网关返回非 200 时错误信息携带 HTTP 状态码，不再只报 XML 解析失败
- 微信 v2 `Refund` 缺商户证书的错误挂接 `paymgr.ErrInvalidParam` 哨兵，可被 `errors.Is` 判断

## [v1.4.3] - 2026-06-05

> 注：本版本及 v1.4.1 包含新增导出 API，按 SemVer 应升 MINOR 版本号；tag 已发布不可变更，特此说明。

### Added

- 新增微信支付 v2（XML 协议）渠道实现 `wechat/v2`，以 `paymgr.ChannelWechatV2`（`wxpayv2`）接入统一抽象层，兼容仅支持 v2 的老商户号。覆盖统一下单（APP / JSAPI / Native / H5）、订单查询、关单、退款、退款查询；内置 APP 与 JSAPI/小程序调起支付二次签名；支持支付回调验签与退款回调 AES-256-ECB 解密；签名算法可配（`v2.WithSignType`，默认 MD5，可切 HMAC-SHA256）。退款接口需配置商户 API 证书（`v2.WithCertPEM` / `v2.WithCertPath`）。零新增第三方依赖：HTTP 用标准库 `net/http`，随机串复用 `wechatpay-go/utils`，JSON 复用 `github.com/gtkit/json`

## [v1.4.2] - 2026-06-04

### Fixed

- 微信支付 `Config.Validate()` 增加 `MchAPIv3Key` 必须为 32 字节的校验，将原本延迟到初始化阶段的 `crypto/aes: invalid key size` 错误前移为明确提示

## [v1.4.1] - 2026-06-04

### Added

- 微信支付新增「微信支付公钥」验签模式：配置公钥 ID 与公钥（路径 / PEM / `*rsa.PublicKey`）即自动启用，适配 2024 年起只下发公钥的新进件商户。新增 `wechat.WithPublicKeyID` / `WithPublicKeyPath` / `WithPublicKeyPEM` / `WithPublicKey` 选项，与现有平台证书选项二选一、公钥优先

## [v1.4.0] - 2026-05-09

### Changed

- ⚠ 路线调整：支付宝底层 SDK 由 `github.com/go-pay/gopay/alipay/v3`（v1.3.x 短暂引入）切回 `github.com/smartwalle/alipay/v3 v3.2.29`。**理由**：核实后 smartwalle 仍在活跃维护，对本项目核心场景（下单 / 查询 / 退款 / 通知）覆盖完整；单 SDK 包尺寸约 4,000 行，比 v1.3.x 时的双 SDK 方案约 21,000 行更轻量，且 OpenAPI 1.0 协议本身原生支持公钥 / 公钥证书两种加签模式。
- 渠道错误的原始错误来源回归 smartwalle 错误（`SubCode` / `SubMsg`），与 v1.2.x 一致；下游 `errors.Is(err, paymgr.ErrXxx)` / `errors.As(err, &chErr); chErr.Code == "ACQ.*"` 判断仍稳定。
- README 第 7 章「支付宝配置」恢复证书模式 + 公钥模式两种说明；第 13 章「当前实现备注」更新底层 SDK 描述；第 15 章升级指南改写为 v1.4.0 路线调整说明。

### Fixed

- 恢复支付宝**普通公钥模式**（仅设置 `Config.AlipayPublicKey` 字段）支持。v1.3.x 时被软降级为 `paymgr.ErrNotSupported` 的配置在 v1.4.0 后正常初始化运行——下游公钥商户号无需切换证书模式即可继续工作。
- 保留 v1.3.1 修复：JSAPI 下单（`TradeTypeJSAPI`）补齐 `product_code=JSAPI_PAY` 与 `op_app_id`（默认取主 AppID），避免支付宝网关返回「missing required parameter」。
- 保留 v1.3.2 修复：`QueryRefund` 在退款单不存在时（支付宝返回 200 + `out_request_no` 为空）显式返回 `paymgr.ErrOrderNotFound`，避免下游收到空响应误判。

### Removed

- `paymgr.ErrNotSupported` 不再用于支付宝公钥模式软降级（公钥模式现已恢复支持，调用 `NewProvider(WithAlipayPublicKey(...))` 不再返回此错误）。

### Notes

- `paymgr.ChannelWechatV2` 常量保留为微信 V2 协议扩展点占位（v1.3.0 引入，与底层 SDK 选型无关；业务方按需自行实装 V2 provider）。
- 工具集 `tools/sandbox-verify/` `tools/notify-listener/` `tools/sandbox-refund/` 保留——未来 SDK 升级回归仍可重用；`sandbox-verify` 的 `raw_public_key_soft_degradation` 检查项改名为 `raw_public_key_mode_works` 并调整为「期望成功」语义。

## [v1.3.2] - 2026-05-09

### Fixed

- 修复 `alipay.Provider.QueryRefund` 在「退款单不存在」时返回空响应而不是 `paymgr.ErrOrderNotFound` 的边界条件 bug。支付宝对不存在的退款单返回 HTTP 200 + 所有关键字段为空字符串（不返回 `ACQ.TRADE_NOT_EXIST` 错误码），原代码看到 200 即构造响应返回，下游 `errors.Is(err, ErrOrderNotFound)` 判断永远 false。修复后响应 200 但 `out_request_no` 为空时显式返回 `ErrOrderNotFound`，与 `QueryOrder` 行为一致。该 bug 自 v1.2.x smartwalle SDK 时代潜伏，沙箱集成验证捕获。

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

[v1.6.0]: https://github.com/gtkit/go-pay/releases/tag/v1.6.0
[v1.5.0]: https://github.com/gtkit/go-pay/releases/tag/v1.5.0
[v1.4.3]: https://github.com/gtkit/go-pay/releases/tag/v1.4.3
[v1.4.2]: https://github.com/gtkit/go-pay/releases/tag/v1.4.2
[v1.4.1]: https://github.com/gtkit/go-pay/releases/tag/v1.4.1
[v1.4.0]: https://github.com/gtkit/go-pay/releases/tag/v1.4.0
[v1.3.2]: https://github.com/gtkit/go-pay/releases/tag/v1.3.2
[v1.3.1]: https://github.com/gtkit/go-pay/releases/tag/v1.3.1
[v1.3.0]: https://github.com/gtkit/go-pay/releases/tag/v1.3.0
[v1.2.1]: https://github.com/gtkit/go-pay/releases/tag/v1.2.1
[v1.2.0]: https://github.com/gtkit/go-pay/releases/tag/v1.2.0
[v1.1.0]: https://github.com/gtkit/go-pay/releases/tag/v1.1.0
[v1.0.3]: https://github.com/gtkit/go-pay/releases/tag/v1.0.3
