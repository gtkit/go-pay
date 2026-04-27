# go-pay

`go-pay` 是一个 Go 支付聚合库，统一封装了微信支付和支付宝的常见能力，对业务层暴露一致的接口。

当前提供的核心能力：

- 统一下单
- 订单查询
- 关闭订单
- 退款
- 退款查询（v1.1.0+）
- 异步通知解析与应答（含退款通知，v1.1.0+）

项目内部通过 `paymgr.Manager` 管理不同支付渠道，业务方只需要：

1. 初始化各渠道 `Provider`
2. 注册到 `Manager`
3. 按渠道调用统一方法

> **升级到 v1.1.0 注意**：`paymgr.Provider` 接口新增 `QueryRefund` 和 `ParseRefundNotify` 两个方法。
> 官方 `alipay` / `wechat` 两个实现已就位，但若你在项目里自定义实现过 `Provider`，升级后会编译失败，需要补齐这两个方法。
> 详细变更见 [CHANGELOG.md](./CHANGELOG.md)。

## 1. 安装

```bash
go get github.com/gtkit/go-pay
```

要求：

- Go `1.26+`
- 已开通微信支付 / 支付宝商户能力
- 已准备好商户私钥、平台证书、公钥等支付材料

## 2. 项目结构

```text
.
├── aggregate/       # 聚合二维码支付编排
├── alipay/         # 支付宝实现
├── wechat/         # 微信支付实现
├── paymgr/         # 统一抽象层
└── example/        # 简单 HTTP 示例
```

包职责：

- `paymgr`：统一的请求、响应、错误和管理器接口
- `wechat`：微信支付 Provider
- `alipay`：支付宝 Provider
- `aggregate`：聚合二维码入口分流与真实支付单编排

## 3. 支持的渠道与交易类型

### 3.1 支付渠道

在代码里使用以下常量：

```go
paymgr.ChannelWechat // 值为 "wxpay"
paymgr.ChannelAlipay // 值为 "alipay"
```

### 3.2 交易类型

```go
paymgr.TradeTypeNative // 扫码支付
paymgr.TradeTypeJSAPI  // JSAPI / 小程序 / 公众号场景
paymgr.TradeTypeApp    // APP 支付
paymgr.TradeTypeH5     // H5 支付
paymgr.TradeTypePage   // PC 网页支付 / 支付宝收银台
```

当前实现支持情况：

| 渠道 | 支持的交易类型 |
| --- | --- |
| 微信支付 | `app`、`native`、`jsapi`、`h5` |
| 支付宝 | `native`、`jsapi`、`app`、`h5`、`page` |

如果传入未实现的类型，会返回 `paymgr.ErrUnsupportedType`。

### 3.3 下单字段与返回矩阵

| 渠道 | 交易类型 | 适用场景 | 额外必填字段 | 重点返回字段 |
| --- | --- | --- | --- | --- |
| 微信支付 | `app` | 微信开放平台 APP 支付 | 无 | `AppParams` |
| 微信支付 | `jsapi` | 公众号 / 小程序支付 | `OpenID` | `PrepayID`、`JSAPIParams` |
| 微信支付 | `native` | PC 或收银台扫码支付 | 无 | `CodeURL` |
| 微信支付 | `h5` | 移动浏览器 H5 支付 | `ClientIP` | `H5URL` |
| 支付宝 | `app` | 支付宝 APP 支付 | 无 | `AppParams` |
| 支付宝 | `jsapi` | 支付宝小程序支付 | 视业务传 `OpenID` 作为 `buyer_id` | `PrepayID` |
| 支付宝 | `native` | 当面付扫码支付 | 无 | `CodeURL` |
| 支付宝 | `h5` | 手机网站支付 | 建议传 `ReturnURL` | `PayURL` |
| 支付宝 | `page` | PC 收银台页面支付 | 建议传 `ReturnURL` | `PayURL` |

聚合二维码入口由 `aggregate.Service` 编排：微信环境走微信 `jsapi`，支付宝移动端走支付宝 `h5`，支付宝 PC 端走支付宝 `page`，普通浏览器需要业务页面先选择渠道。

## 4. 统一接入流程

最典型的接入顺序如下：

```go
ctx := context.Background()

mgr := paymgr.NewManager()

wechatProvider, err := wechat.NewProvider(
	ctx,
	wechat.WithAppID("wx1234567890abcdef"),
	wechat.WithMerchant(
		"1900000001",
		"3775B6A45ACD588826D15E583A95F5DD********",
		"your-apiv3-key-32-characters-long",
	),
	wechat.WithMerchantPrivateKeyPath("/path/to/apiclient_key.pem"),
	wechat.WithPlatformCertificatePath("/path/to/wechatpay_cert.pem"),
)
if err != nil {
	return err
}
mgr.Register(wechatProvider)

alipayProvider, err := alipay.NewProvider(
	alipay.WithAppID("2021000000000001"),
	alipay.WithPrivateKeyPath("/path/to/alipay_app_private_key.pem"),
	alipay.WithProduction(true),
	alipay.WithCertModePaths(
		"/path/to/appCertPublicKey.crt",
		"/path/to/alipayRootCert.crt",
		"/path/to/alipayCertPublicKey_RSA2.crt",
	),
)
if err != nil {
	return err
}
mgr.Register(alipayProvider)
```

后续所有能力都通过 `mgr` 调用：

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelWechat, req)
queryResp, err := mgr.QueryOrder(ctx, paymgr.ChannelAlipay, queryReq)
err = mgr.CloseOrder(ctx, paymgr.ChannelWechat, closeReq)
refundResp, err := mgr.Refund(ctx, paymgr.ChannelAlipay, refundReq)
notifyResult, err := mgr.ParseNotify(ctx, paymgr.ChannelWechat, r)
err = mgr.ACKNotify(paymgr.ChannelWechat, w)
```

## 5. 怎么添加配置

本项目没有内置读取 YAML / TOML / JSON / ENV 的逻辑。接入方仍然需要先把自己的配置文件读到业务配置结构体里，再传给支付库。

也就是说：

- 你自己的项目负责“从配置中心 / 环境变量 / 配置文件读取”
- `go-pay` 负责“校验并使用这些配置初始化支付客户端”

当前推荐的接入方式是函数选项模式：

- 微信：`wechat.NewProvider(ctx, wechat.WithXXX(...), ...)`
- 支付宝：`alipay.NewProvider(alipay.WithXXX(...), ...)`

这样有几个好处：

- 初始化代码可读性更好
- 配置来源更灵活，路径、原始 PEM 文本、已解析对象都可以分别设置
- 不会把所有字段都堆在一个大结构体里

同时为了兼容旧用法，`*wechat.Config` 和 `*alipay.Config` 仍然可以直接传给 `NewProvider(...)`，或者使用 `NewProviderWithConfig(...)`。

下面分别说明微信和支付宝的配置方式。

## 6. 微信支付配置

### 6.1 推荐方式：函数选项模式

推荐写法：

```go
provider, err := wechat.NewProvider(
	ctx,
	wechat.WithAppID(cfg.Pay.Wechat.AppID),
	wechat.WithMerchant(
		cfg.Pay.Wechat.MchID,
		cfg.Pay.Wechat.MchCertSerialNumber,
		cfg.Pay.Wechat.MchAPIv3Key,
	),
	wechat.WithMerchantPrivateKeyPath(cfg.Pay.Wechat.MchPrivateKeyPath),
	wechat.WithPlatformCertificatePath(cfg.Pay.Wechat.WechatPayCertificatePath),
)
```

当前可用的主要选项：

| 选项 | 说明 |
| --- | --- |
| `wechat.WithAppID(appID)` | 设置微信应用 `appid` |
| `wechat.WithMerchant(mchID, serial, apiV3Key)` | 设置商户号、商户证书序列号、APIv3 Key |
| `wechat.WithMerchantPrivateKeyPath(path)` | 通过文件路径设置商户私钥 |
| `wechat.WithMerchantPrivateKeyPEM(pem)` | 通过 PEM 文本设置商户私钥 |
| `wechat.WithMerchantPrivateKey(key)` | 直接设置已解析的私钥对象 |
| `wechat.WithPlatformCertificatePath(path)` | 通过文件路径设置微信支付平台证书 |
| `wechat.WithPlatformCertificatePEM(pem)` | 通过 PEM 文本设置微信支付平台证书 |
| `wechat.WithPlatformCertificate(cert)` | 直接设置已解析的平台证书对象 |

### 6.2 必填信息

微信初始化必须提供这些信息：

| 项 | 是否必填 | 说明 |
| --- | --- | --- |
| `AppID` | 是 | 微信应用 `appid` |
| `MchID` | 是 | 微信商户号 |
| `MchCertSerialNumber` | 是 | 商户证书序列号 |
| `MchAPIv3Key` | 是 | APIv3 密钥，主要用于敏感信息解密和回调解密 |
| 商户私钥 | 三选一 | 路径 / PEM 文本 / `*rsa.PrivateKey` |
| 微信平台证书 | 三选一 | 路径 / PEM 文本 / `*x509.Certificate` |

### 6.3 配置文件映射示例

如果你自己的项目配置文件是这样的：

```yaml
pay:
  wechat:
    app_id: wx1234567890abcdef
    mch_id: "1900000001"
    mch_cert_serial_number: "3775B6A45ACD588826D15E583A95F5DD********"
    mch_apiv3_key: "your-apiv3-key-32-characters-long"
    mch_private_key_path: "/data/keys/wechat/apiclient_key.pem"
    wechatpay_certificate_path: "/data/keys/wechat/wechatpay_cert.pem"
```

那你在业务代码里可以这样组装：

```go
wechatProvider, err := wechat.NewProvider(
	ctx,
	wechat.WithAppID(cfg.Pay.Wechat.AppID),
	wechat.WithMerchant(
		cfg.Pay.Wechat.MchID,
		cfg.Pay.Wechat.MchCertSerialNumber,
		cfg.Pay.Wechat.MchAPIv3Key,
	),
	wechat.WithMerchantPrivateKeyPath(cfg.Pay.Wechat.MchPrivateKeyPath),
	wechat.WithPlatformCertificatePath(cfg.Pay.Wechat.WechatPayCertificatePath),
)
if err != nil {
	return fmt.Errorf("init wechat provider: %w", err)
}
```

### 6.4 兼容方式：结构体配置

如果你更喜欢先组装结构体，也可以：

```go
wechatProvider, err := wechat.NewProvider(ctx, &wechat.Config{
	AppID:                    cfg.Pay.Wechat.AppID,
	MchID:                    cfg.Pay.Wechat.MchID,
	MchCertSerialNumber:      cfg.Pay.Wechat.MchCertSerialNumber,
	MchAPIv3Key:              cfg.Pay.Wechat.MchAPIv3Key,
	MchPrivateKeyPath:        cfg.Pay.Wechat.MchPrivateKeyPath,
	WechatPayCertificatePath: cfg.Pay.Wechat.WechatPayCertificatePath,
})
```

或者显式写成：

```go
wechatProvider, err := wechat.NewProviderWithConfig(ctx, &wechat.Config{...})
```

### 6.5 回调证书说明

`wechat.NewProvider` 初始化时不仅会初始化商户侧 client，还会初始化通知验签处理器 `notify.Handler`。

和之前不同的是，微信平台证书现在已经正式进入配置项，不需要再去改源码里的固定路径。

例如：

```go
wechat.WithPlatformCertificatePath("/path/to/wechatpay_cert.pem")
```

### 6.6 微信支持的方法

微信 Provider 当前实现的方法：

- `UnifiedOrder`
- `QueryOrder`
- `CloseOrder`
- `Refund`
- `QueryRefund`
- `ParseNotify`
- `ParseRefundNotify`（微信独立的退款异步通知）
- `ACKNotify`

其中下单只支持：

- `paymgr.TradeTypeApp`
- `paymgr.TradeTypeJSAPI`
- `paymgr.TradeTypeNative`
- `paymgr.TradeTypeH5`

## 7. 支付宝配置

### 7.1 推荐方式：函数选项模式

推荐写法：

```go
provider, err := alipay.NewProvider(
	alipay.WithAppID(cfg.Pay.Alipay.AppID),
	alipay.WithPrivateKeyPath(cfg.Pay.Alipay.PrivateKeyPath),
	alipay.WithProduction(cfg.Pay.Alipay.IsProduction),
	alipay.WithCertModePaths(
		cfg.Pay.Alipay.AppCertPublicKeyPath,
		cfg.Pay.Alipay.AlipayRootCertPath,
		cfg.Pay.Alipay.AlipayCertPublicKeyPath,
	),
)
```

当前可用的主要选项：

| 选项 | 说明 |
| --- | --- |
| `alipay.WithAppID(appID)` | 设置支付宝应用 ID |
| `alipay.WithProduction(bool)` | 设置生产或沙箱环境 |
| `alipay.WithPrivateKey(key)` | 直接设置应用私钥内容 |
| `alipay.WithPrivateKeyPath(path)` | 通过文件路径设置应用私钥 |
| `alipay.WithCertMode(appCert, rootCert, alipayCert)` | 通过证书内容启用证书模式 |
| `alipay.WithCertModePaths(appCertPath, rootCertPath, alipayCertPath)` | 通过证书路径启用证书模式 |
| `alipay.WithAlipayPublicKey(publicKey)` | 使用普通公钥模式 |

### 7.2 必填信息

| 项 | 是否必填 | 说明 |
| --- | --- | --- |
| `AppID` | 是 | 支付宝应用 ID |
| `PrivateKey` 或 `PrivateKeyPath` | 是 | 应用私钥内容或文件路径 |
| `IsProduction` | 是 | `true` 生产，`false` 沙箱 |
| 证书模式三件套 | 证书模式必填 | 应用公钥证书、支付宝根证书、支付宝公钥证书 |
| `AlipayPublicKey` | 普通公钥模式必填 | 当不使用证书模式时必填 |

### 7.3 两种配置模式

#### 方式一：证书模式

推荐使用证书模式。只要应用公钥证书、支付宝根证书、支付宝公钥证书三项都提供，就会走证书模式。

示例：

```go
alipayProvider, err := alipay.NewProvider(
	alipay.WithAppID(cfg.Pay.Alipay.AppID),
	alipay.WithPrivateKeyPath(cfg.Pay.Alipay.PrivateKeyPath),
	alipay.WithProduction(true),
	alipay.WithCertModePaths(
		cfg.Pay.Alipay.AppCertPublicKeyPath,
		cfg.Pay.Alipay.AlipayRootCertPath,
		cfg.Pay.Alipay.AlipayCertPublicKeyPath,
	),
)
if err != nil {
	return fmt.Errorf("init alipay provider: %w", err)
}
```

#### 方式二：普通公钥模式

如果不使用证书模式，则必须提供 `AlipayPublicKey`：

```go
alipayProvider, err := alipay.NewProvider(
	alipay.WithAppID(cfg.Pay.Alipay.AppID),
	alipay.WithPrivateKeyPath(cfg.Pay.Alipay.PrivateKeyPath),
	alipay.WithProduction(false),
	alipay.WithAlipayPublicKey(cfg.Pay.Alipay.AlipayPublicKey),
)
if err != nil {
	return fmt.Errorf("init alipay provider: %w", err)
}
```

### 7.4 兼容方式：结构体配置

如果你希望继续使用结构体，也可以：

```go
alipayProvider, err := alipay.NewProvider(&alipay.Config{
	AppID:                   cfg.Pay.Alipay.AppID,
	PrivateKeyPath:          cfg.Pay.Alipay.PrivateKeyPath,
	IsProduction:            cfg.Pay.Alipay.IsProduction,
	AppCertPublicKeyPath:    cfg.Pay.Alipay.AppCertPublicKeyPath,
	AlipayRootCertPath:      cfg.Pay.Alipay.AlipayRootCertPath,
	AlipayCertPublicKeyPath: cfg.Pay.Alipay.AlipayCertPublicKeyPath,
	AlipayPublicKey:         cfg.Pay.Alipay.AlipayPublicKey,
})
```

或者显式调用：

```go
alipayProvider, err := alipay.NewProviderWithConfig(&alipay.Config{...})
```

### 7.5 支付宝支持的方法

支付宝 Provider 当前实现的方法：

- `UnifiedOrder`
- `QueryOrder`
- `CloseOrder`
- `Refund`
- `QueryRefund`
- `ParseNotify`（当 `GmtRefund` 或 `RefundFee` 非空时，`TradeStatus` 会映射为 `TradeStatusRefunded`）
- `ParseRefundNotify`（支付宝无独立退款通知端点，本方法直接返回 `paymgr.ErrNotSupported`；请使用 `ParseNotify` 识别退款事件）
- `ACKNotify`

下单支持的交易类型：

- `paymgr.TradeTypeNative`
- `paymgr.TradeTypeJSAPI`
- `paymgr.TradeTypeApp`
- `paymgr.TradeTypeH5`
- `paymgr.TradeTypePage`

## 8. 初始化并注册 Provider

完整示例：

```go
package main

import (
	"context"
	"fmt"

	"github.com/gtkit/go-pay/alipay"
	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/go-pay/wechat"
)

func InitPay(ctx context.Context, cfg *Config) (*paymgr.Manager, error) {
	mgr := paymgr.NewManager()

	wechatProvider, err := wechat.NewProvider(
		ctx,
		wechat.WithAppID(cfg.Pay.Wechat.AppID),
		wechat.WithMerchant(
			cfg.Pay.Wechat.MchID,
			cfg.Pay.Wechat.MchCertSerialNumber,
			cfg.Pay.Wechat.MchAPIv3Key,
		),
		wechat.WithMerchantPrivateKeyPath(cfg.Pay.Wechat.MchPrivateKeyPath),
		wechat.WithPlatformCertificatePath(cfg.Pay.Wechat.WechatPayCertificatePath),
	)
	if err != nil {
		return nil, fmt.Errorf("init wechat provider: %w", err)
	}
	mgr.Register(wechatProvider)

	alipayProvider, err := alipay.NewProvider(
		alipay.WithAppID(cfg.Pay.Alipay.AppID),
		alipay.WithPrivateKeyPath(cfg.Pay.Alipay.PrivateKeyPath),
		alipay.WithProduction(cfg.Pay.Alipay.IsProduction),
		alipay.WithCertModePaths(
			cfg.Pay.Alipay.AppCertPublicKeyPath,
			cfg.Pay.Alipay.AlipayRootCertPath,
			cfg.Pay.Alipay.AlipayCertPublicKeyPath,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("init alipay provider: %w", err)
	}
	mgr.Register(alipayProvider)

	return mgr, nil
}
```

可用辅助方法：

- `mgr.Register(p)`：注册渠道
- `mgr.Deregister(ch)`：取消注册
- `mgr.Provider(ch)`：获取底层 Provider
- `mgr.Channels()`：列出已注册渠道

## 9. 怎么调用需要的方法

这里按业务里最常见的几个方法分别说明。

### 9.1 统一下单 `UnifiedOrder`

方法签名：

```go
func (m *Manager) UnifiedOrder(ctx context.Context, ch Channel, req *UnifiedOrderRequest) (*UnifiedOrderResponse, error)
```

请求结构：

```go
type UnifiedOrderRequest struct {
	OutTradeNo  string
	TotalAmount int64
	Subject     string
	TradeType   TradeType
	NotifyURL   string
	ReturnURL   string
	ClientIP    string
	OpenID      string
	ExpireAt    time.Time
	Metadata    map[string]string
}
```

字段说明：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `OutTradeNo` | 是 | 商户订单号，必须唯一 |
| `TotalAmount` | 是 | 金额，单位分 |
| `Subject` | 是 | 商品描述 |
| `TradeType` | 视场景 | 交易类型 |
| `NotifyURL` | 是 | 异步通知地址 |
| `ReturnURL` | 否 | 支付宝 H5 / PC 页面支付同步跳转地址 |
| `ClientIP` | 某些渠道场景需要 | 用户 IP；微信 H5 必填 |
| `OpenID` | 某些渠道场景需要 | 微信 JSAPI 或支付宝买家标识场景 |
| `ExpireAt` | 否 | 订单过期时间 |
| `Metadata` | 否 | 附加数据，回调时会带回 |

#### 微信 APP 下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelWechat, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250001",
	TotalAmount: 100,
	Subject:     "VIP月卡",
	TradeType:   paymgr.TradeTypeApp,
	NotifyURL:   "https://api.example.com/pay/notify/wechat",
	ExpireAt:    time.Now().Add(30 * time.Minute),
	Metadata: map[string]string{
		"uid": "10001",
	},
})
if err != nil {
	return err
}

// 微信 APP 支付时重点取这个字段
appParams := resp.AppParams
```

返回值重点字段：

- `resp.PrepayID`
- `resp.AppParams`

其中 `AppParams` 是 JSON 字符串，APP 端解析后传给微信 SDK。

#### 微信 JSAPI 下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelWechat, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250005",
	TotalAmount: 100,
	Subject:     "小程序订单",
	TradeType:   paymgr.TradeTypeJSAPI,
	NotifyURL:   "https://api.example.com/pay/notify/wechat",
	OpenID:      "oUpF8uMuAJO_M2pxb1Q9zNjWeS6o",
})
if err != nil {
	return err
}

jsapiParams := resp.JSAPIParams
```

重点返回：

- `resp.PrepayID`
- `resp.JSAPIParams`：JSON 字符串，前端解析后用于调起微信 JSAPI / 小程序支付

#### 微信 H5 下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelWechat, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250006",
	TotalAmount: 100,
	Subject:     "微信H5订单",
	TradeType:   paymgr.TradeTypeH5,
	NotifyURL:   "https://api.example.com/pay/notify/wechat",
	ClientIP:    "203.0.113.10",
})
if err != nil {
	return err
}

h5URL := resp.H5URL
```

重点返回：

- `resp.H5URL`：微信 H5 拉起支付链接

当前实现会按最常见的移动浏览器场景构造 `scene_info.h5_info.type = "Wap"`。

#### 微信 Native 扫码下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelWechat, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250002",
	TotalAmount: 100,
	Subject:     "扫码支付订单",
	TradeType:   paymgr.TradeTypeNative,
	NotifyURL:   "https://api.example.com/pay/notify/wechat",
})
if err != nil {
	return err
}

codeURL := resp.CodeURL
```

重点返回：

- `resp.CodeURL`：二维码内容

#### 支付宝 APP 下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelAlipay, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250003",
	TotalAmount: 100,
	Subject:     "支付宝APP订单",
	TradeType:   paymgr.TradeTypeApp,
	NotifyURL:   "https://api.example.com/pay/notify/alipay",
})
if err != nil {
	return err
}

orderString := resp.AppParams
```

重点返回：

- `resp.AppParams`：支付宝签名后的订单字符串，APP 端直接调用 SDK

#### 支付宝 H5 下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelAlipay, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250004",
	TotalAmount: 100,
	Subject:     "支付宝H5订单",
	TradeType:   paymgr.TradeTypeH5,
	NotifyURL:   "https://api.example.com/pay/notify/alipay",
	ReturnURL:   "https://www.example.com/pay/return",
})
if err != nil {
	return err
}

payURL := resp.PayURL
```

重点返回：

- `resp.PayURL`：跳转支付链接

#### 支付宝 PC 页面下单

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelAlipay, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250007",
	TotalAmount: 100,
	Subject:     "支付宝PC订单",
	TradeType:   paymgr.TradeTypePage,
	NotifyURL:   "https://api.example.com/pay/notify/alipay",
	ReturnURL:   "https://www.example.com/pay/return",
})
if err != nil {
	return err
}

pageURL := resp.PayURL
```

重点返回：

- `resp.PayURL`：支付宝收银台跳转链接

#### 聚合二维码编排

聚合二维码不直接承载微信或支付宝原始支付码，而是承载你自己的业务入口 URL。扫码进入入口页后，可以通过 `aggregate.Service` 统一决定真实下单渠道与交易类型。

```go
resolver := aggregate.NewService(mgr)

result, err := resolver.Resolve(ctx, &aggregate.ResolveRequest{
	UserAgent:       r.UserAgent(),
	SelectedChannel: paymgr.Channel(r.URL.Query().Get("channel")),
	OpenID:          openID,
	BuildUnifiedOrder: func(ch paymgr.Channel, tt paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
		return &paymgr.UnifiedOrderRequest{
			OutTradeNo:  orderNum,
			TotalAmount: amount,
			Subject:     subject,
			TradeType:   tt,
			NotifyURL:   notifyURL,
			ReturnURL:   returnURL,
			ClientIP:    clientIP,
			OpenID:      openID,
		}, nil
	},
})
if err != nil {
	return err
}
```

`result.Action` 的语义：

- `choose_channel`：普通浏览器尚未选择支付渠道，此时由你的页面展示“微信 / 支付宝”入口
- `redirect`：跳转支付链接，支付宝取 `result.Response.PayURL`，微信 H5 取 `result.Response.H5URL`
- `qr_code`：返回二维码内容，取 `result.Response.CodeURL`
- `jsapi`：返回微信前端调起参数，取 `result.Response.JSAPIParams`

当前决策表固定为：

- 微信环境：微信 `JSAPI`
- 支付宝环境：移动端走支付宝 `H5`，PC 走支付宝 `page`
- 普通浏览器移动端：用户选微信走微信 `H5`，选支付宝走支付宝 `H5`
- 普通浏览器 PC：用户选微信走微信 `native`，选支付宝走支付宝 `page`

### 9.2 查询订单 `QueryOrder`

方法签名：

```go
func (m *Manager) QueryOrder(ctx context.Context, ch Channel, req *QueryOrderRequest) (*QueryOrderResponse, error)
```

请求结构：

```go
type QueryOrderRequest struct {
	OutTradeNo    string
	TransactionID string
}
```

说明：

- `OutTradeNo` 和 `TransactionID` 二选一
- 至少传一个

示例：

```go
resp, err := mgr.QueryOrder(ctx, paymgr.ChannelWechat, &paymgr.QueryOrderRequest{
	OutTradeNo: "ORD202603250001",
})
if err != nil {
	return err
}

fmt.Println(resp.TradeStatus)
fmt.Println(resp.TransactionID)
fmt.Println(resp.TotalAmount)
```

常见返回字段：

- `TradeStatus`
- `TransactionID`
- `PaidAt`
- `BuyerID`

统一状态值：

- `pending`
- `paid`
- `closed`
- `refunded`
- `error`

### 9.3 关闭订单 `CloseOrder`

方法签名：

```go
func (m *Manager) CloseOrder(ctx context.Context, ch Channel, req *CloseOrderRequest) error
```

请求结构：

```go
type CloseOrderRequest struct {
	OutTradeNo string
}
```

示例：

```go
err := mgr.CloseOrder(ctx, paymgr.ChannelWechat, &paymgr.CloseOrderRequest{
	OutTradeNo: "ORD202603250001",
})
if err != nil {
	return err
}
```

适用场景：

- 订单超时未支付，主动关闭
- 用户取消订单后关闭支付单

### 9.4 退款 `Refund`

方法签名：

```go
func (m *Manager) Refund(ctx context.Context, ch Channel, req *RefundRequest) (*RefundResponse, error)
```

请求结构：

```go
type RefundRequest struct {
	OutTradeNo    string
	TransactionID string
	OutRefundNo   string
	RefundAmount  int64
	TotalAmount   int64
	Reason        string
	NotifyURL     string
}
```

字段要求：

- `OutTradeNo` 和 `TransactionID` 二选一
- `OutRefundNo` 必填，且需唯一
- `RefundAmount` 必须大于 0
- `TotalAmount` 必须大于 0
- `RefundAmount <= TotalAmount`

示例：

```go
resp, err := mgr.Refund(ctx, paymgr.ChannelAlipay, &paymgr.RefundRequest{
	OutTradeNo:   "ORD202603250003",
	OutRefundNo:  "REF202603250001",
	RefundAmount: 100,
	TotalAmount:  100,
	Reason:       "用户申请退款",
	NotifyURL:    "https://api.example.com/pay/refund/notify/alipay",
})
if err != nil {
	return err
}

fmt.Println(resp.OutRefundNo)
fmt.Println(resp.RefundID)
fmt.Println(resp.RefundAmount)
```

### 9.5 查询退款 `QueryRefund`

方法签名：

```go
func (m *Manager) QueryRefund(ctx context.Context, ch Channel, req *QueryRefundRequest) (*QueryRefundResponse, error)
```

请求结构：

```go
type QueryRefundRequest struct {
	OutTradeNo    string // 支付宝用；与 TransactionID 二选一
	TransactionID string // 支付宝用；与 OutTradeNo 二选一
	OutRefundNo   string // 必填
}
```

响应结构：

```go
type QueryRefundResponse struct {
	Channel       Channel
	OutTradeNo    string
	TransactionID string
	OutRefundNo   string
	RefundID      string
	RefundStatus  RefundStatus // processing / success / closed / abnormal / error
	RefundAmount  int64        // 分
	TotalAmount   int64        // 分
	RefundedAt    time.Time    // 退款成功时才有值
}
```

字段要求：

- `OutRefundNo` 必填
- 支付宝渠道需要额外提供 `OutTradeNo` 或 `TransactionID`（微信可留空）

示例：

```go
resp, err := mgr.QueryRefund(ctx, paymgr.ChannelWechat, &paymgr.QueryRefundRequest{
	OutRefundNo: "REF20250305001",
})
if err != nil {
	return err
}

if resp.RefundStatus == paymgr.RefundStatusSuccess {
	// 退款成功
}
```

### 9.6 处理异步通知 `ParseNotify` + `ACKNotify`

方法签名：

```go
func (m *Manager) ParseNotify(ctx context.Context, ch Channel, r *http.Request) (*NotifyResult, error)
func (m *Manager) ACKNotify(ch Channel, w http.ResponseWriter) error
```

推荐处理顺序：

1. 根据回调路由确定渠道
2. 调用 `ParseNotify`
3. 校验订单和金额
4. 做幂等更新
5. 调用 `ACKNotify`

示例：

```go
func handleWechatNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := mgr.ParseNotify(ctx, paymgr.ChannelWechat, r)
	if err != nil {
		http.Error(w, "invalid notify", http.StatusBadRequest)
		return
	}

	// 1. 查订单
	// 2. 校验金额
	// 3. 幂等更新支付状态
	// 4. 触发后续业务
	_ = result

	if err := mgr.ACKNotify(paymgr.ChannelWechat, w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
```

通知解析结果：

```go
type NotifyResult struct {
	Channel       Channel
	OutTradeNo    string
	TransactionID string
	TradeStatus   TradeStatus
	TotalAmount   int64
	PaidAt        time.Time
	BuyerID       string
	Metadata      map[string]string
}
```

注意：

- `ParseNotify` 成功不代表业务已经处理完成，只代表验签和解析通过
- 业务层必须自己做订单存在性校验、金额校验、状态幂等控制
- `ACKNotify` 必须在业务确认处理完成后再回写

不同平台的成功应答：

- 微信：返回 JSON `{"code":"SUCCESS","message":"OK"}`
- 支付宝：返回纯文本 `success`

`Manager` 已经帮你按渠道封装好了，不需要业务自己区分响应格式。

### 9.7 处理退款异步通知 `ParseRefundNotify`

方法签名：

```go
func (m *Manager) ParseRefundNotify(ctx context.Context, ch Channel, r *http.Request) (*RefundNotifyResult, error)
```

响应结构：

```go
type RefundNotifyResult struct {
	Channel             Channel
	OutTradeNo          string
	TransactionID       string
	OutRefundNo         string
	RefundID            string
	RefundStatus        RefundStatus
	RefundAmount        int64     // 分
	TotalAmount         int64     // 分
	RefundedAt          time.Time
	UserReceivedAccount string    // 仅微信返回，如 "招商银行信用卡0403"
}
```

#### 微信

微信退款会独立推送异步通知到 `RefundRequest.NotifyURL`，`event_type` 为 `REFUND.SUCCESS` / `REFUND.ABNORMAL` / `REFUND.CLOSED`。使用本方法解析：

```go
func handleWechatRefundNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := mgr.ParseRefundNotify(ctx, paymgr.ChannelWechat, r)
	if err != nil {
		http.Error(w, "invalid notify", http.StatusBadRequest)
		return
	}

	// 幂等更新退款单状态；RefundStatusSuccess 才代表退款入账成功
	_ = result

	_ = mgr.ACKNotify(paymgr.ChannelWechat, w)
}
```

#### 支付宝

支付宝没有独立的退款异步通知端点，退款结果会复用支付通知端点（即 `UnifiedOrderRequest.NotifyURL`）推送回来。直接调用 `ParseRefundNotify` 会返回 `paymgr.ErrNotSupported`。

正确做法：使用 `ParseNotify` 并通过 `TradeStatus == paymgr.TradeStatusRefunded` 识别退款事件。当回调的 `gmt_refund` 或 `refund_fee` 字段非空时，`go-pay` 会自动把 `TradeStatus` 映射为 `TradeStatusRefunded`。

```go
result, err := mgr.ParseNotify(ctx, paymgr.ChannelAlipay, r)
if err != nil { /* ... */ }

switch result.TradeStatus {
case paymgr.TradeStatusPaid:
	// 支付成功
case paymgr.TradeStatusRefunded:
	// 退款成功（部分或全额）
}
```

## 10. 常见错误与排查

### 10.1 渠道未注册

如果调用时报错：

```text
payment: channel "xxx" not registered
```

说明你还没有执行：

```go
mgr.Register(provider)
```

或者传错了渠道值。

### 10.2 请求参数校验失败

例如：

- `payment: out_trade_no is required`
- `payment: total_amount must be positive`
- `payment: notify_url is required`

这类错误来自 `paymgr` 的统一校验逻辑，先检查你传入的请求字段。

### 10.3 微信回调处理器初始化失败

重点排查：

- 商户私钥是否正确
- APIv3 Key 是否正确
- 微信平台证书是否已通过 `WithPlatformCertificatePath` / `WithPlatformCertificatePEM` / `WithPlatformCertificate` 正确传入

### 10.4 渠道错误

项目会把底层 SDK 错误包装成 `paymgr.ChannelError`：

```go
type ChannelError struct {
	Channel Channel
	Code    string
	Message string
	Err     error
}
```

你可以这样判断：

```go
var chErr *paymgr.ChannelError
if errors.As(err, &chErr) {
	fmt.Println(chErr.Channel)
	fmt.Println(chErr.Code)
	fmt.Println(chErr.Message)
}
```

## 11. 推荐接入方式

生产接入建议：

- 把 `paymgr.Manager` 做成应用级单例，在服务启动时初始化
- 配置从环境变量、配置文件或配置中心读取，不要写死在代码里
- 商户私钥和证书放在安全目录，不要提交到仓库
- 回调处理必须做金额校验和幂等
- 订单号、退款单号必须全局唯一
- HTTP 回调地址必须使用可公网访问的 HTTPS 地址

## 12. 参考示例

可以直接参考项目里的示例：

- [example/main.go](/Users/xiaozhaofu/go/src/go-pay/example/main.go)

这个示例展示了：

- 初始化并注册微信 / 支付宝 Provider
- 创建订单
- 查询订单
- 处理退款
- 处理支付回调

## 13. 当前实现备注

在接入前建议先了解当前代码边界：

- 微信 Provider 当前覆盖 `app`、`jsapi`、`native`、`h5` 四种直连下单场景
- 当前推荐用函数选项模式初始化，结构体配置作为兼容方式保留
- 示例代码展示的是接入方式，不是可直接上线的完整业务代码

如果你要在自己的业务项目中使用，通常下一步会做两件事：

1. 把证书、密钥、回调地址抽到你自己的配置系统里
2. 在订单服务里封装一层业务适配，把 `go-pay` 的统一请求/响应转换成你自己的领域对象
