## ADDED Requirements

### Requirement: Aggregate service detects payment entry environment
系统 MUST 能根据扫码入口的 User-Agent 识别支付环境，并区分微信、支付宝、普通移动浏览器和普通 PC 浏览器。

#### Scenario: Detect WeChat environment
- **WHEN** User-Agent 包含 `MicroMessenger`
- **THEN** `DetectEnv(...)` MUST 返回 `wechat`

#### Scenario: Detect Alipay environment
- **WHEN** User-Agent 包含 `AlipayClient`
- **THEN** `DetectEnv(...)` MUST 返回 `alipay`

#### Scenario: Detect browser mobile environment
- **WHEN** User-Agent 不包含微信或支付宝标识且被识别为移动端
- **THEN** `DetectEnv(...)` MUST 返回 `browser_mobile`

#### Scenario: Detect browser PC environment
- **WHEN** User-Agent 不包含微信或支付宝标识且未被识别为移动端
- **THEN** `DetectEnv(...)` MUST 返回 `browser_pc`

### Requirement: Aggregate service resolves channel and trade type by decision table
系统 MUST 基于入口环境、用户显式选择以及微信 OpenID 可用性，按固定决策表创建真实支付单，并返回统一动作结果。

#### Scenario: WeChat environment uses JSAPI
- **WHEN** 环境为 `wechat` 且提供了 `OpenID`
- **THEN** 系统 MUST 创建 `ChannelWechat + TradeTypeJSAPI` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `jsapi`

#### Scenario: Alipay mobile environment redirects to H5 payment
- **WHEN** 环境为 `alipay` 且当前访问设备为移动端
- **THEN** 系统 MUST 创建 `ChannelAlipay + TradeTypeH5` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `redirect`

#### Scenario: Alipay PC environment redirects to page payment
- **WHEN** 环境为 `alipay` 且当前访问设备为 PC
- **THEN** 系统 MUST 创建 `ChannelAlipay + TradeTypePage` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `redirect`

#### Scenario: Browser without selected channel requires user choice
- **WHEN** 环境为 `browser_mobile` 或 `browser_pc` 且未提供 `SelectedChannel`
- **THEN** 系统 MUST 返回 `choose_channel` 动作
- **THEN** 系统 MUST NOT 创建真实支付单

#### Scenario: Browser mobile selected WeChat uses WeChat H5
- **WHEN** 环境为 `browser_mobile` 且 `SelectedChannel` 为 `wxpay`
- **THEN** 系统 MUST 创建 `ChannelWechat + TradeTypeH5` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `redirect`

#### Scenario: Browser mobile selected Alipay uses Alipay H5
- **WHEN** 环境为 `browser_mobile` 且 `SelectedChannel` 为 `alipay`
- **THEN** 系统 MUST 创建 `ChannelAlipay + TradeTypeH5` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `redirect`

#### Scenario: Browser PC selected WeChat uses Native QR code
- **WHEN** 环境为 `browser_pc` 且 `SelectedChannel` 为 `wxpay`
- **THEN** 系统 MUST 创建 `ChannelWechat + TradeTypeNative` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `qr_code`

#### Scenario: Browser PC selected Alipay uses page payment
- **WHEN** 环境为 `browser_pc` 且 `SelectedChannel` 为 `alipay`
- **THEN** 系统 MUST 创建 `ChannelAlipay + TradeTypePage` 的真实支付单
- **THEN** 返回结果的动作 MUST 为 `redirect`

### Requirement: Aggregate service exposes explicit validation and passthrough errors
聚合服务 MUST 为自身输入问题返回显式错误，并保留业务层和统一支付层原始错误。

#### Scenario: Missing OpenID in WeChat environment
- **WHEN** 环境为 `wechat` 且未提供 `OpenID`
- **THEN** 系统 MUST 返回 `ErrMissingOpenID`

#### Scenario: Invalid selected channel
- **WHEN** 普通浏览器环境下 `SelectedChannel` 非空且既不是 `wxpay` 也不是 `alipay`
- **THEN** 系统 MUST 返回 `ErrInvalidChannelSelection`

#### Scenario: Missing unified order builder
- **WHEN** 当前分支需要创建真实支付单且 `BuildUnifiedOrder` 为 `nil`
- **THEN** 系统 MUST 返回 `ErrMissingOrderBuilder`

#### Scenario: Business builder error is preserved
- **WHEN** `BuildUnifiedOrder(...)` 返回错误
- **THEN** 系统 MUST 原样返回该错误

#### Scenario: Unified order error is preserved
- **WHEN** `Manager.UnifiedOrder(...)` 返回错误
- **THEN** 系统 MUST 原样返回该错误
