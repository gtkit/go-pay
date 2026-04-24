# Aggregate QR Payment Design

**Date:** 2026-04-24

## Goal

为 `go-pay` 增加“聚合二维码支付编排能力”，同时保留现有“单独渠道二维码支付”能力。

目标效果：

- 单独二维码继续沿用现有 `channel + native` 模式
- 聚合二维码不直接承载微信/支付宝原始支付码，而是承载业务入口 URL
- 用户扫码进入业务入口后，由库根据环境和用户选择决定创建哪一种真实支付单
- `go-pay` 只负责支付能力编排，不负责聚合会话存储和落地页托管

## Context

当前仓库已经具备：

- 微信 `native` 下单，返回 `CodeURL`
- 支付宝 `native` 下单，返回 `CodeURL`
- 微信 `jsapi` / `h5`
- 支付宝 `h5`

此外，另有一份已确认的设计文档用于补充支付宝 `TradeTypePage`：

- [2026-04-24-alipay-tradepagepay-design.md](/Users/xiaozhaofu/go/src/my-gtkit-package/go-pay/docs/superpowers/specs/2026-04-24-alipay-tradepagepay-design.md)

本设计依赖该能力存在，因为聚合支付在 PC 环境选择支付宝时将走 `TradeTypePage`。

## Non-Goals

本次不做以下内容：

- 不生成二维码图片，只返回二维码内容字符串或聚合入口 URL
- 不托管 HTML 落地页
- 不托管 HTTP 路由
- 不管理聚合 token / 主订单状态 / 过期控制
- 不内置 Redis / DB / 内存存储
- 不替业务层获取微信 `OpenID`
- 不改变现有 `paymgr.Provider` 接口
- 不把聚合能力塞进 `wechat` 或 `alipay` provider

## Scope

本次设计覆盖两类能力：

### 1. 单独二维码

保持当前能力不变：

- 微信：`ChannelWechat + TradeTypeNative`
- 支付宝：`ChannelAlipay + TradeTypeNative`

库继续返回 `UnifiedOrderResponse.CodeURL`，二维码图片渲染由调用方负责。

### 2. 聚合二维码

新增聚合支付编排能力：

- 聚合二维码内容为业务入口 URL，例如 `https://pay.example.com/check/{ordertoken}`
- 扫码后先进入业务项目自己的入口页或入口 handler
- 入口层调用 `go-pay` 的聚合编排能力，决定下一步该创建什么真实支付单

## Approaches Considered

### Option A: 高层编排型（推荐）

做法：

- 新增 `aggregate` 包
- 由 `aggregate.Service` 负责：
  - 环境识别
  - 根据环境和用户选择决定目标 `channel + trade_type`
  - 通过现有 `paymgr.Manager` 创建真实支付单
  - 返回统一动作结果

优点：

- 聚合规则沉淀在库里，而不是散落在业务里
- 继续复用现有统一下单能力
- 不侵入业务存储层

缺点：

- 需要新增一层聚合编排模型

### Option B: 计划型

做法：

- 库只输出“应该走哪个 `channel + trade_type`”
- 真实下单由业务层自行调用 `Manager.UnifiedOrder(...)`

优点：

- 库更薄

缺点：

- 业务层仍要重复写编排代码
- 统一动作结果无法沉淀到库里

### Option C: Helper 型

做法：

- 只提供 `DetectEnv(...)` 之类的工具函数

优点：

- 改动最小

缺点：

- 聚合支付的核心逻辑仍在业务层
- 库没有真正提供“聚合支付能力”

## Recommendation

采用 **Option A**。

原因：

- 这最符合 `go-pay` 的定位：沉淀支付能力，而不是沉淀页面模板或业务订单系统
- 业务层继续掌控主订单和聚合 token
- 渠道识别、下单路由和动作结果收敛为统一 API

## Responsibilities

### `go-pay` 负责

- 识别请求环境
- 决定支付应走哪个 `channel + trade_type`
- 根据业务层提供的订单构造函数创建真实支付单
- 返回统一动作结果

### 业务项目负责

- 保存 `ordertoken -> 主订单`
- 提供聚合入口 URL 和路由
- 提供普通浏览器下的支付方式选择页
- 获取并传入微信 `OpenID`
- 生成二维码图片
- 管理主订单状态、幂等、过期控制

## Runtime Rules

### 单独二维码

调用方明确指定渠道：

- 微信：`ChannelWechat + TradeTypeNative`
- 支付宝：`ChannelAlipay + TradeTypeNative`

### 聚合二维码

二维码内容不是原始渠道支付码，而是你的业务 URL。

扫码后分流规则固定如下：

- 微信内打开：直接创建微信 `JSAPI`
- 支付宝内打开：
  - 手机环境：创建支付宝 `H5`
  - PC 环境：创建支付宝 `page`
- 普通浏览器打开：
  - `PC`：先展示“微信支付 / 支付宝支付”选择
    - 选微信：创建微信 `native`
    - 选支付宝：创建支付宝 `page`
  - `手机`：先展示“微信支付 / 支付宝支付”选择
    - 选微信：创建微信 `H5`
    - 选支付宝：创建支付宝 `H5`

## Relationship To Existing `checkPay`

参考目标是：

- 保留“聚合码先进入业务入口，再按环境分流”的思路

但不直接照搬 `/checkPay` 的旧逻辑：

- 旧逻辑：微信环境走微信，否则直接走支付宝
- 新逻辑：升级为三类环境判断
  - `MicroMessenger`
  - `AlipayClient`
  - 其他环境，再区分 `PC / 手机`

## Package Layout

建议新增：

```text
aggregate/
```

不要放入：

- `paymgr`
- `wechat`
- `alipay`

原因：

- `paymgr` 是渠道统一抽象
- `aggregate` 是跨渠道编排层
- 这是不同职责

## Proposed API

### Environment Type

```go
type Env string

const (
	EnvWechat        Env = "wechat"
	EnvAlipay        Env = "alipay"
	EnvBrowserPC     Env = "browser_pc"
	EnvBrowserMobile Env = "browser_mobile"
)
```

### Action Type

```go
type Action string

const (
	ActionChooseChannel Action = "choose_channel"
	ActionRedirect      Action = "redirect"
	ActionQRCode        Action = "qr_code"
	ActionJSAPI         Action = "jsapi"
)
```

### Resolve Request

```go
type ResolveRequest struct {
	UserAgent       string
	SelectedChannel paymgr.Channel
	OpenID          string

	BuildUnifiedOrder func(ch paymgr.Channel, tt paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error)
}
```

字段含义：

- `UserAgent`：扫码进入后的终端环境
- `SelectedChannel`：普通浏览器场景下用户手动选择的支付渠道；未选择时为空
- `OpenID`：微信环境走 `JSAPI` 时由业务层传入
- `BuildUnifiedOrder`：由业务层根据自己的主订单数据构造真实支付请求

### Resolve Result

```go
type ResolveResult struct {
	Env       Env
	Action    Action
	Channel   paymgr.Channel
	TradeType paymgr.TradeType
	Response  *paymgr.UnifiedOrderResponse
}
```

### Service API

```go
func DetectEnv(userAgent string) Env

func NewService(mgr *paymgr.Manager) *Service

func (s *Service) Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
```

### Optional Helpers

为减少业务层分支判断，可提供只读 helper：

```go
func (r *ResolveResult) RedirectURL() string
func (r *ResolveResult) QRCode() string
```

## Decision Table

| Env | SelectedChannel | Result |
| --- | --- | --- |
| `wechat` | ignored | `ChannelWechat + TradeTypeJSAPI` |
| `alipay` + mobile | ignored | `ChannelAlipay + TradeTypeH5` |
| `alipay` + pc | ignored | `ChannelAlipay + TradeTypePage` |
| `browser_mobile` | empty | `ActionChooseChannel` |
| `browser_mobile` | `wxpay` | `ChannelWechat + TradeTypeH5` |
| `browser_mobile` | `alipay` | `ChannelAlipay + TradeTypeH5` |
| `browser_pc` | empty | `ActionChooseChannel` |
| `browser_pc` | `wxpay` | `ChannelWechat + TradeTypeNative` |
| `browser_pc` | `alipay` | `ChannelAlipay + TradeTypePage` |

## Action Mapping

真实支付单创建完成后，动作类型映射如下：

- `TradeTypeJSAPI` -> `ActionJSAPI`
- `TradeTypeNative` -> `ActionQRCode`
- `TradeTypeH5` -> `ActionRedirect`
- `TradeTypePage` -> `ActionRedirect`

## Validation And Errors

### Missing OpenID

当环境为微信，且需要走 `JSAPI`，但业务层没有提供 `OpenID` 时：

- 返回 `ErrMissingOpenID`

不允许静默降级成 `H5` 或 `native`。

### Invalid Channel Selection

当普通浏览器环境下，`SelectedChannel` 非空，但不是：

- `paymgr.ChannelWechat`
- `paymgr.ChannelAlipay`

则返回：

- `ErrInvalidChannelSelection`

### Missing Builder

如果当前分支需要创建真实支付单，但 `BuildUnifiedOrder == nil`：

- 返回 `ErrMissingOrderBuilder`

### Business Build Error

如果 `BuildUnifiedOrder(...)` 返回错误：

- 原样返回

原因：

- 这属于业务层订单构造失败，不应包装成渠道错误

### Unified Order Error

如果 `Manager.UnifiedOrder(...)` 返回错误：

- 原样返回

原因：

- 继续保留现有 `paymgr.ChannelError` / `ErrUnsupportedType` / `ErrInvalidParam` 的判断能力

## Redirect Semantics

### Redirect Result

`ActionRedirect` 时：

- 支付宝 `page` / `h5` 从 `Response.PayURL` 取目标地址
- 微信 `h5` 从 `Response.H5URL` 取目标地址

### QR Result

`ActionQRCode` 时：

- 从 `Response.CodeURL` 取二维码内容

### JSAPI Result

`ActionJSAPI` 时：

- 从 `Response.JSAPIParams` 取微信前端调起参数

## Dependency On `TradeTypePage`

本设计要求 `paymgr` 已新增：

```go
TradeTypePage
```

以及支付宝 provider 已支持：

- `ChannelAlipay + TradeTypePage`

因此，聚合编排能力应在 `TradeTypePage` 实现之后落地。

## Testing Strategy

采用 TDD。

### Unit Tests

需要覆盖：

- `DetectEnv(...)`
  - 微信 UA
  - 支付宝 UA
  - PC 浏览器 UA
  - 手机浏览器 UA
- `Resolve(...)`
  - 微信环境 -> `JSAPI`
  - 支付宝手机环境 -> `H5`
  - 支付宝 PC 环境 -> `page`
  - 普通浏览器未选渠道 -> `ActionChooseChannel`
  - PC 选微信 -> `native`
  - PC 选支付宝 -> `page`
  - 手机选微信 -> `H5`
  - 手机选支付宝 -> `H5`
  - 缺失 `OpenID`
  - 非法渠道选择
  - `BuildUnifiedOrder` 返回错误

### Integration Expectations

需要确认：

- 现有单独二维码能力不受影响
- 聚合结果正确复用现有 `Manager.UnifiedOrder(...)`
- 全量 `go test ./...` 通过

## Risks

### Risk 1: User-Agent 判断不稳定

缓解：

- 只将 UA 作为支付入口分流依据
- 支付是否成功仍以渠道异步通知为准

### Risk 2: 微信 `OpenID` 缺失

缓解：

- 明确要求微信环境必须传入 `OpenID`
- 用显式错误阻止隐式降级

### Risk 3: 聚合能力与业务边界混乱

缓解：

- 本设计明确不做：
  - 页面
  - 存储
  - token 管理
  - 订单状态

## Implementation Scope

预计新增或修改：

- `aggregate/` 新包
- `paymgr` 测试或少量公共错误定义（如共享错误）
- `README.md`
- `example/` 中的聚合支付示例说明

另一个前置依赖变更：

- `paymgr/payment.go` 增加 `TradeTypePage`
- `alipay/provider.go` 支持 `TradeTypePage`

## Acceptance Criteria

满足以下条件视为完成：

- 存在独立的 `aggregate` 编排层
- 能识别微信 / 支付宝 / PC 浏览器 / 手机浏览器环境
- 能按本设计的决策表创建真实支付单
- 单独二维码能力保持不变
- 聚合结果可统一表达为：
  - 选择渠道
  - 跳转
  - 二维码
  - JSAPI
- 文档和示例同步更新
- 全量测试通过
