## Context

`go-pay` 当前已经提供统一下单、查询、退款与回调解析，但能力边界仍停留在“调用方明确指定渠道和交易类型”这一层。对于聚合二维码场景，业务层需要自己完成扫码入口环境识别、渠道选择、`channel + trade_type` 路由，以及不同支付返回形态的归一化处理。

同时，聚合设计要求支付宝 PC 环境能够落到收银台页面支付，但统一层尚未提供 `TradeTypePage` 和对应的支付宝实现，因此本次变更需要先补齐该基础能力，再在其上新增聚合编排层。

约束如下：

- 这是 Go 扩展包，导出面要尽量小，不能把业务存储、页面托管、二维码渲染塞进库内。
- 现有 `paymgr.Provider` 接口保持不变，新增能力应复用 `Manager.UnifiedOrder(...)`。
- 现有单渠道 `native` / `jsapi` / `app` / `h5` 行为必须保持兼容。

## Goals / Non-Goals

**Goals:**

- 为统一支付层新增 `paymgr.TradeTypePage`，支持支付宝 PC 页面支付。
- 在 `alipay.Provider.UnifiedOrder(...)` 中支持 `TradeTypePage`，并通过现有 `PayURL` 返回跳转链接。
- 新增独立 `aggregate` 包，基于 User-Agent 和用户选择决定真实下单渠道与交易类型。
- 为聚合结果统一表达四类动作：选择渠道、跳转、二维码、JSAPI。
- 用显式错误覆盖聚合场景下的关键输入问题，例如缺失 OpenID、非法渠道选择、缺失订单构造器。
- 保持 README、示例与测试和行为一致。

**Non-Goals:**

- 不生成二维码图片，只返回二维码内容字符串或入口 URL。
- 不管理聚合 token、订单状态、幂等或过期存储。
- 不托管落地页、HTTP 路由或前端页面。
- 不替业务层获取微信 OpenID。
- 不修改 `paymgr.Provider` 接口，也不为微信新增 `page` 支付。
- 不新增与当前场景无关的高级支付宝页面参数。

## Decisions

### 1. 新增统一交易类型 `TradeTypePage`

选择在 `paymgr` 层新增 `TradeTypePage`，而不是复用 `TradeTypeH5` 或引入支付宝专有扩展方法。

原因：

- PC 收银台支付与手机 H5 支付语义不同，复用 `h5` 会让上层分流和文档长期混乱。
- 统一抽象已经覆盖跨渠道主支付能力，新增一个交易类型比扩展 provider 私有方法更符合库定位。
- 现有 `UnifiedOrderResponse.PayURL` 已可承载支付宝页面跳转结果，无需新增响应结构。

备选方案：

- 复用 `TradeTypeH5`：放弃，语义错误。
- 仅新增 `alipay.Provider.PagePay(...)`：放弃，会破坏统一抽象。

### 2. 聚合编排独立放入 `aggregate` 包

聚合逻辑放在新包 `aggregate/` 中，由 `aggregate.Service` 持有 `*paymgr.Manager`，对外提供：

- `DetectEnv(userAgent string) Env`
- `NewService(mgr *paymgr.Manager) *Service`
- `(*Service).Resolve(ctx, req) (*ResolveResult, error)`

原因：

- 聚合支付是跨渠道编排，不属于 `wechat` 或 `alipay` provider 职责。
- `paymgr` 负责统一渠道能力，`aggregate` 负责在统一能力之上做路由决策，两者分层更清晰。
- 通过 `BuildUnifiedOrder` 回调把主订单数据构造责任留在业务层，避免库侵入业务订单模型。

备选方案：

- 只暴露 `DetectEnv(...)` 等 helper：放弃，业务层仍要重复写编排逻辑。
- 把聚合逻辑塞进 `paymgr.Manager`：放弃，会让基础抽象耦合业务入口分流。

### 3. 聚合结果统一暴露动作类型，不额外引入复杂包装

`ResolveResult` 保留最少必要字段：

- `Env`
- `Action`
- `Channel`
- `TradeType`
- `Response`

不在首版额外增加大量 helper 或二次包装结构；调用方从 `Response` 上读取 `PayURL` / `H5URL` / `CodeURL` / `JSAPIParams` 即可。

原因：

- 已有 `UnifiedOrderResponse` 足够表达结果，再包一层 URL/QRCode/JSAPI 参数对象会扩大导出面。
- 聚合层的核心价值是“分流决策”，不是重新定义渠道响应协议。

备选方案：

- 再封装 `RedirectURL()` / `QRCode()` 等 helper：暂不作为首版必需能力，避免无必要导出面增长。

### 4. 聚合错误使用显式 sentinel error

在 `aggregate/errors.go` 中定义明确错误：

- `ErrMissingOpenID`
- `ErrInvalidChannelSelection`
- `ErrMissingOrderBuilder`

并遵循两条规则：

- 业务层 `BuildUnifiedOrder(...)` 返回的错误原样透传。
- `Manager.UnifiedOrder(...)` 返回的错误原样透传。

原因：

- 只有聚合层自己的输入错误才适合定义为新错误。
- 渠道错误、参数错误与业务构造错误已经有现成语义，继续包装会损失调用方判断能力。

### 5. UA 识别保持保守规则，优先保证分流稳定

`DetectEnv(...)` 使用稳定、最小的一组 User-Agent 特征：

- 包含 `MicroMessenger` -> `EnvWechat`
- 包含 `AlipayClient` -> `EnvAlipay`
- 其他场景再判断是否移动端，返回 `EnvBrowserMobile` 或 `EnvBrowserPC`

是否移动端由内部 helper 判断常见移动终端标记，例如 `Mobile`、`Android`、`iPhone`、`iPad`。

原因：

- 聚合入口只需要做支付入口分流，不需要实现复杂设备识别系统。
- 规则越复杂，误判越多；保守判断更适合库代码。

## Risks / Trade-offs

- [User-Agent 识别存在误判] -> 仅把 UA 用于入口分流，真实支付结果仍以渠道下单和异步通知为准。
- [新增 `TradeTypePage` 会扩大公共 API] -> 只新增一个常量，不调整接口签名；文档中明确其仅用于支付宝 PC 页面支付。
- [聚合能力容易越界到业务层] -> 通过 `BuildUnifiedOrder` 回调把订单构造、token、页面与存储责任留给调用方。
- [当前工作区已有未提交改动] -> 本次实现只触碰与 `TradeTypePage`、`aggregate`、文档说明直接相关的文件，避免和现有微信改动互相污染。

## Migration Plan

1. 先新增 `TradeTypePage` 及支付宝 provider 支持，并通过单元测试验证 `PayURL` 返回与字段映射。
2. 再新增 `aggregate` 包与环境分流逻辑，用假 provider / fake manager 场景测试决策表。
3. 同步更新 README 与示例，补充聚合二维码接入方式与 `TradeTypePage` 使用方式。
4. 全量运行库级验证命令，确保现有单渠道能力未回归。

本次为向后兼容增强，不涉及数据迁移；回滚时可直接移除新增代码与文档，无需状态修复。

## Open Questions

- 无。当前设计边界已经足够进入实现阶段。
