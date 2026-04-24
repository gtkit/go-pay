package wechat

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gtkit/go-pay/paymgr"

	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/app"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/h5"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// Config 微信支付配置
type Config struct {
	AppID                    string            // 开放平台应用的 appid（微信开放平台注册的移动应用）
	MchID                    string            // 商户号
	MchCertSerialNumber      string            // 商户证书序列号
	MchAPIv3Key              string            // 商户 APIv3 密钥（用于回调解密）
	MchPrivateKeyPath        string            // 商户私钥文件路径（PEM 格式）
	MchPrivateKeyPEM         string            // 商户私钥 PEM 文本
	MchPrivateKey            *rsa.PrivateKey   // 商户私钥（直接提供则优先）
	WechatPayCertificatePath string            // 微信支付平台证书路径
	WechatPayCertificatePEM  string            // 微信支付平台证书 PEM 文本
	WechatPayCertificate     *x509.Certificate // 微信支付平台证书（直接提供则优先）
}

// Option 用于函数选项模式配置微信 Provider。
//
// *Config 也实现了该接口，因此旧的结构体配置调用方式仍可继续使用：
//
//	wechat.NewProvider(ctx, &wechat.Config{...})
type Option interface {
	apply(*Config) error
}

type optionFunc func(*Config) error

func (f optionFunc) apply(cfg *Config) error {
	return f(cfg)
}

func (c *Config) apply(dst *Config) error {
	if c == nil {
		return fmt.Errorf("wechat: config is required")
	}
	*dst = *c
	return nil
}

// Provider 微信支付提供者（以 APP 支付为主）.
type Provider struct {
	cfg           *Config
	client        *core.Client
	privateKey    *rsa.PrivateKey
	notifyHandler *notify.Handler
}

// Validate 校验配置完整性.
func (c *Config) Validate() error {
	if c.AppID == "" {
		return fmt.Errorf("wechat: app_id is required")
	}
	if c.MchID == "" {
		return fmt.Errorf("wechat: mch_id is required")
	}
	if c.MchCertSerialNumber == "" {
		return fmt.Errorf("wechat: mch_cert_serial_number is required")
	}
	if c.MchAPIv3Key == "" {
		return fmt.Errorf("wechat: mch_apiv3_key is required")
	}
	if c.MchPrivateKey == nil && c.MchPrivateKeyPEM == "" && c.MchPrivateKeyPath == "" {
		return fmt.Errorf("wechat: mch_private_key, mch_private_key_pem or mch_private_key_path is required")
	}
	if c.WechatPayCertificate == nil && c.WechatPayCertificatePEM == "" && c.WechatPayCertificatePath == "" {
		return fmt.Errorf("wechat: wechatpay_certificate, wechatpay_certificate_pem or wechatpay_certificate_path is required")
	}
	return nil
}

// WithAppID 设置微信应用 AppID。
func WithAppID(appID string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AppID = appID
		return nil
	})
}

// WithMerchant 设置微信商户信息。
func WithMerchant(mchID, certSerialNumber, apiV3Key string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.MchID = mchID
		cfg.MchCertSerialNumber = certSerialNumber
		cfg.MchAPIv3Key = apiV3Key
		return nil
	})
}

// WithMerchantPrivateKeyPath 通过文件路径设置商户私钥。
func WithMerchantPrivateKeyPath(path string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.MchPrivateKeyPath = path
		return nil
	})
}

// WithMerchantPrivateKeyPEM 通过 PEM 文本设置商户私钥。
func WithMerchantPrivateKeyPEM(privateKeyPEM string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.MchPrivateKeyPEM = privateKeyPEM
		return nil
	})
}

// WithMerchantPrivateKey 直接设置已解析的商户私钥。
func WithMerchantPrivateKey(privateKey *rsa.PrivateKey) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.MchPrivateKey = privateKey
		return nil
	})
}

// WithPlatformCertificatePath 通过文件路径设置微信支付平台证书。
func WithPlatformCertificatePath(path string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.WechatPayCertificatePath = path
		return nil
	})
}

// WithPlatformCertificatePEM 通过 PEM 文本设置微信支付平台证书。
func WithPlatformCertificatePEM(certificatePEM string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.WechatPayCertificatePEM = certificatePEM
		return nil
	})
}

// WithPlatformCertificate 直接设置已解析的平台证书。
func WithPlatformCertificate(certificate *x509.Certificate) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.WechatPayCertificate = certificate
		return nil
	})
}

// NewProvider 创建微信支付提供者.
//
// 初始化时会自动注册平台证书下载器，后续自动轮转平台证书。
func NewProvider(ctx context.Context, opts ...Option) (*Provider, error) {
	cfg := &Config{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt.apply(cfg); err != nil {
			return nil, err
		}
	}
	return NewProviderWithConfig(ctx, cfg)
}

// NewProviderWithConfig 使用结构体配置创建微信支付提供者。
func NewProviderWithConfig(ctx context.Context, cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wechat: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 加载商户私钥
	privateKey, err := resolvePrivateKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("wechat: load private key: %w", err)
	}

	// 初始化 client
	// WithWechatPayAutoAuthCipher 一次性完成：
	// 1. 注册请求签名（使用商户私钥）
	// 2. 注册应答验签（自动下载并定时刷新平台证书）
	// 3. 注册敏感信息加解密
	client, err := core.NewClient(
		ctx,
		option.WithWechatPayAutoAuthCipher(
			cfg.MchID,
			cfg.MchCertSerialNumber,
			privateKey,
			cfg.MchAPIv3Key,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("wechat: init client: %w", err)
	}

	p := &Provider{
		cfg:        cfg,
		client:     client,
		privateKey: privateKey,
	}

	// ===== 初始化回调通知处理器 =====
	//
	// 生产环境中必须配置以下方式之一（取消注释并填入你的证书/公钥路径）：
	//
	// 方式1：使用本地管理的微信支付平台证书
	//
	wechatPayCert, err := resolvePlatformCertificate(cfg)
	if err != nil {
		return nil, fmt.Errorf("wechat: load platform cert: %w", err)
	}
	certificateVisitor := core.NewCertificateMapWithList([]*x509.Certificate{wechatPayCert})
	p.notifyHandler, err = notify.NewRSANotifyHandler(
		cfg.MchAPIv3Key,
		verifiers.NewSHA256WithRSAVerifier(certificateVisitor),
	)
	if err != nil {
		return nil, fmt.Errorf("wechat: init notify handler: %w", err)
	}

	//
	// 方式2：使用 CombinedVerifier（同时支持证书和公钥，适合过渡期）
	//
	//  p.notifyHandler, err = notify.NewRSANotifyHandler(
	//      cfg.MchAPIv3Key,
	//      verifiers.NewSHA256WithRSACombinedVerifier(
	//          certificateVisitor, wechatPayPublicKeyID, *wechatPayPublicKey),
	//  )

	return p, nil
}

// Channel 实现 paymgr.Provider 接口.
func (p *Provider) Channel() paymgr.Channel {
	return paymgr.ChannelWechat
}

// UnifiedOrder 统一下单
//
// 当前支持 APP、JSAPI、小程序/公众号、Native 扫码和 H5 支付。
func (p *Provider) UnifiedOrder(ctx context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
	resp := &paymgr.UnifiedOrderResponse{
		Channel: paymgr.ChannelWechat,
	}

	// 过期时间
	var timeExpire *time.Time
	if !req.ExpireAt.IsZero() {
		timeExpire = &req.ExpireAt
	}

	// 附加数据（JSON 序列化后存入 attach 字段，回调时原样返回）
	var attach *string
	if len(req.Metadata) > 0 {
		data, err := json.Marshal(req.Metadata)
		if err != nil {
			return nil, fmt.Errorf("wechat: marshal metadata: %w", err)
		}
		attach = core.String(string(data))
	}

	switch req.TradeType {
	case paymgr.TradeTypeApp:
		svc := app.AppApiService{Client: p.client}

		result, _, err := svc.Prepay(ctx, app.PrepayRequest{
			Appid:       core.String(p.cfg.AppID),
			Mchid:       core.String(p.cfg.MchID),
			Description: core.String(req.Subject),
			OutTradeNo:  core.String(req.OutTradeNo),
			TimeExpire:  timeExpire,
			Attach:      attach,
			NotifyUrl:   core.String(req.NotifyURL),
			Amount: &app.Amount{
				Total:    core.Int64(req.TotalAmount),
				Currency: core.String("CNY"),
			},
		})
		if err != nil {
			return nil, wrapWechatError(err)
		}
		resp.PrepayID = *result.PrepayId

		// 生成 APP 调起支付的签名参数
		appParams, err := p.buildAppPayParams(*result.PrepayId)
		if err != nil {
			return nil, fmt.Errorf("wechat: build app pay params: %w", err)
		}
		resp.AppParams = appParams

	case paymgr.TradeTypeJSAPI:
		if req.OpenID == "" {
			return nil, fmt.Errorf("%w: openid is required for wechat jsapi", paymgr.ErrInvalidParam)
		}

		svc := jsapi.JsapiApiService{Client: p.client}
		result, _, err := svc.Prepay(ctx, jsapi.PrepayRequest{
			Appid:       core.String(p.cfg.AppID),
			Mchid:       core.String(p.cfg.MchID),
			Description: core.String(req.Subject),
			OutTradeNo:  core.String(req.OutTradeNo),
			TimeExpire:  timeExpire,
			Attach:      attach,
			NotifyUrl:   core.String(req.NotifyURL),
			Amount: &jsapi.Amount{
				Total:    core.Int64(req.TotalAmount),
				Currency: core.String("CNY"),
			},
			Payer: &jsapi.Payer{
				Openid: core.String(req.OpenID),
			},
		})
		if err != nil {
			return nil, wrapWechatError(err)
		}
		resp.PrepayID = derefString(result.PrepayId)

		jsapiParams, err := p.buildJSAPIPayParams(resp.PrepayID)
		if err != nil {
			return nil, fmt.Errorf("wechat: build jsapi pay params: %w", err)
		}
		resp.JSAPIParams = jsapiParams

	case paymgr.TradeTypeNative:
		svc := native.NativeApiService{Client: p.client}
		result, _, err := svc.Prepay(ctx, native.PrepayRequest{
			Appid:       core.String(p.cfg.AppID),
			Mchid:       core.String(p.cfg.MchID),
			Description: core.String(req.Subject),
			OutTradeNo:  core.String(req.OutTradeNo),
			TimeExpire:  timeExpire,
			Attach:      attach,
			NotifyUrl:   core.String(req.NotifyURL),
			Amount: &native.Amount{
				Total:    core.Int64(req.TotalAmount),
				Currency: core.String("CNY"),
			},
		})
		if err != nil {
			return nil, wrapWechatError(err)
		}
		resp.CodeURL = *result.CodeUrl

	case paymgr.TradeTypeH5:
		if req.ClientIP == "" {
			return nil, fmt.Errorf("%w: client_ip is required for wechat h5", paymgr.ErrInvalidParam)
		}

		svc := h5.H5ApiService{Client: p.client}
		result, _, err := svc.Prepay(ctx, h5.PrepayRequest{
			Appid:       core.String(p.cfg.AppID),
			Mchid:       core.String(p.cfg.MchID),
			Description: core.String(req.Subject),
			OutTradeNo:  core.String(req.OutTradeNo),
			TimeExpire:  timeExpire,
			Attach:      attach,
			NotifyUrl:   core.String(req.NotifyURL),
			Amount: &h5.Amount{
				Total:    core.Int64(req.TotalAmount),
				Currency: core.String("CNY"),
			},
			SceneInfo: buildH5SceneInfo(req),
		})
		if err != nil {
			return nil, wrapWechatError(err)
		}
		resp.H5URL = derefString(result.H5Url)

	default:
		return nil, fmt.Errorf("%w: wechat provider supports app, jsapi, native and h5, got %s",
			paymgr.ErrUnsupportedType, req.TradeType)
	}

	return resp, nil
}

// QueryOrder 查询订单
//
// 使用 app.AppApiService 查询，返回通用 pays.Transaction。
// 微信支付的查询 API 与支付方式无关，底层调用相同的 endpoint。
func (p *Provider) QueryOrder(ctx context.Context, req *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	svc := app.AppApiService{Client: p.client}

	var (
		result *payments.Transaction
		err    error
	)

	if req.TransactionID != "" {
		result, _, err = svc.QueryOrderById(ctx, app.QueryOrderByIdRequest{
			Mchid:         core.String(p.cfg.MchID),
			TransactionId: core.String(req.TransactionID),
		})
	} else {
		result, _, err = svc.QueryOrderByOutTradeNo(ctx, app.QueryOrderByOutTradeNoRequest{
			Mchid:      core.String(p.cfg.MchID),
			OutTradeNo: core.String(req.OutTradeNo),
		})
	}
	if err != nil {
		return nil, wrapWechatError(err)
	}

	resp := &paymgr.QueryOrderResponse{
		Channel:       paymgr.ChannelWechat,
		OutTradeNo:    derefString(result.OutTradeNo),
		TransactionID: derefString(result.TransactionId),
		TradeStatus:   mapWechatTradeState(derefString(result.TradeState)),
	}

	if result.Amount != nil {
		resp.TotalAmount = derefInt64(result.Amount.Total)
	}

	if result.SuccessTime != nil {
		resp.PaidAt = parseTime(*result.SuccessTime)
	}

	// APP 支付场景下 Payer 中的 openid 是用户在开放平台下的 openid
	if result.Payer != nil && result.Payer.Openid != nil {
		resp.BuyerID = *result.Payer.Openid
	}

	return resp, nil
}

// CloseOrder 关闭订单
func (p *Provider) CloseOrder(ctx context.Context, req *paymgr.CloseOrderRequest) error {
	svc := app.AppApiService{Client: p.client}
	_, err := svc.CloseOrder(ctx, app.CloseOrderRequest{
		Mchid:      core.String(p.cfg.MchID),
		OutTradeNo: core.String(req.OutTradeNo),
	})
	if err != nil {
		return wrapWechatError(err)
	}

	return nil
}

// Refund 申请退款
//
// 退款接口与支付方式无关，使用 refunddomestic 包。
func (p *Provider) Refund(ctx context.Context, req *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	svc := refunddomestic.RefundsApiService{Client: p.client}

	createReq := refunddomestic.CreateRequest{
		OutRefundNo: core.String(req.OutRefundNo),
		Reason:      core.String(req.Reason),
		NotifyUrl:   core.String(req.NotifyURL),
		Amount: &refunddomestic.AmountReq{
			Refund:   core.Int64(req.RefundAmount),
			Total:    core.Int64(req.TotalAmount),
			Currency: core.String("CNY"),
		},
	}

	if req.TransactionID != "" {
		createReq.TransactionId = core.String(req.TransactionID)
	} else {
		createReq.OutTradeNo = core.String(req.OutTradeNo)
	}

	result, _, err := svc.Create(ctx, createReq)
	if err != nil {
		return nil, wrapWechatError(err)
	}

	return &paymgr.RefundResponse{
		Channel:      paymgr.ChannelWechat,
		OutRefundNo:  derefString(result.OutRefundNo),
		RefundID:     derefString(result.RefundId),
		RefundAmount: derefInt64(result.Amount.Refund),
	}, nil
}

// QueryRefund 查询退款
//
// 通过商户退款单号查询退款状态，调用 refunddomestic.QueryByOutRefundNo。
func (p *Provider) QueryRefund(ctx context.Context, req *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	svc := refunddomestic.RefundsApiService{Client: p.client}

	result, _, err := svc.QueryByOutRefundNo(ctx, refunddomestic.QueryByOutRefundNoRequest{
		OutRefundNo: core.String(req.OutRefundNo),
	})
	if err != nil {
		return nil, wrapWechatError(err)
	}

	resp := &paymgr.QueryRefundResponse{
		Channel:       paymgr.ChannelWechat,
		OutRefundNo:   derefString(result.OutRefundNo),
		OutTradeNo:    derefString(result.OutTradeNo),
		TransactionID: derefString(result.TransactionId),
		RefundID:      derefString(result.RefundId),
		RefundStatus:  mapWechatRefundStatus(result.Status),
	}
	if result.Amount != nil {
		resp.RefundAmount = derefInt64(result.Amount.Refund)
		resp.TotalAmount = derefInt64(result.Amount.Total)
	}
	if result.SuccessTime != nil {
		resp.RefundedAt = *result.SuccessTime
	}
	return resp, nil
}

// ParseNotify 解析异步通知
//
// 回调通知的 Transaction 结构与支付方式无关（APP/JSAPI/Native 共用同一格式），
// 使用 pays.Transaction 通用结构体。
func (p *Provider) ParseNotify(ctx context.Context, r *http.Request) (*paymgr.NotifyResult, error) {
	if p.notifyHandler == nil {
		return nil, fmt.Errorf("wechat: notify handler not initialized, " +
			"please configure it in NewProvider (see comments for setup instructions)")
	}

	// 解析并验签回调通知，解密后反序列化为 payments.Transaction
	var transaction payments.Transaction
	_, err := p.notifyHandler.ParseNotifyRequest(ctx, r, &transaction)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidNotify, err)
	}

	result := &paymgr.NotifyResult{
		Channel:       paymgr.ChannelWechat,
		OutTradeNo:    derefString(transaction.OutTradeNo),
		TransactionID: derefString(transaction.TransactionId),
		TradeStatus:   mapWechatTradeState(derefString(transaction.TradeState)),
	}

	if transaction.Amount != nil {
		result.TotalAmount = derefInt64(transaction.Amount.Total)
	}

	if transaction.SuccessTime != nil {
		result.PaidAt = parseTime(*transaction.SuccessTime)
	}

	if transaction.Payer != nil && transaction.Payer.Openid != nil {
		result.BuyerID = *transaction.Payer.Openid
	}

	// 解析附加数据
	if transaction.Attach != nil && *transaction.Attach != "" {
		metadata := make(map[string]string)
		if err := json.Unmarshal([]byte(*transaction.Attach), &metadata); err == nil {
			result.Metadata = metadata
		}
	}

	return result, nil
}

// refundNotifyResource 微信退款异步通知解密后的业务数据体。
//
// 官方字段定义参考 https://pay.weixin.qq.com/doc/v3/merchant/4012791449。
// 注意退款通知中状态字段名为 refund_status，与 QueryRefund 响应中的
// status 字段不同，因此不能直接复用 refunddomestic.Refund。
type refundNotifyResource struct {
	Mchid               string `json:"mchid"`
	OutTradeNo          string `json:"out_trade_no"`
	TransactionID       string `json:"transaction_id"`
	OutRefundNo         string `json:"out_refund_no"`
	RefundID            string `json:"refund_id"`
	RefundStatus        string `json:"refund_status"`
	SuccessTime         string `json:"success_time"`
	UserReceivedAccount string `json:"user_received_account"`
	Amount              struct {
		Total       int64 `json:"total"`
		Refund      int64 `json:"refund"`
		PayerTotal  int64 `json:"payer_total"`
		PayerRefund int64 `json:"payer_refund"`
	} `json:"amount"`
}

// ParseRefundNotify 解析退款异步通知
//
// 微信退款通知的 event_type 为 REFUND.SUCCESS / REFUND.ABNORMAL / REFUND.CLOSED，
// resource 解密后的 JSON 字段与支付通知不同（状态字段名为 refund_status）。
func (p *Provider) ParseRefundNotify(ctx context.Context, r *http.Request) (*paymgr.RefundNotifyResult, error) {
	if p.notifyHandler == nil {
		return nil, fmt.Errorf("wechat: notify handler not initialized, " +
			"please configure it in NewProvider (see comments for setup instructions)")
	}

	var res refundNotifyResource
	if _, err := p.notifyHandler.ParseNotifyRequest(ctx, r, &res); err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidNotify, err)
	}

	result := &paymgr.RefundNotifyResult{
		Channel:             paymgr.ChannelWechat,
		OutTradeNo:          res.OutTradeNo,
		TransactionID:       res.TransactionID,
		OutRefundNo:         res.OutRefundNo,
		RefundID:            res.RefundID,
		RefundStatus:        mapWechatRefundStatusString(res.RefundStatus),
		RefundAmount:        res.Amount.Refund,
		TotalAmount:         res.Amount.Total,
		UserReceivedAccount: res.UserReceivedAccount,
	}
	if res.SuccessTime != "" {
		result.RefundedAt = parseTime(res.SuccessTime)
	}
	return result, nil
}

// ACKNotify 回写成功响应
//
// 微信支付回调需返回 HTTP 200 + JSON body:
//
//	{"code":"SUCCESS","message":"OK"}
func (p *Provider) ACKNotify(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"OK"}`))
}

// --- APP 调起支付参数构建 ---

// buildAppPayParams 生成 APP 调起微信支付所需的完整签名参数
//
// APP 端 SDK 需要以下字段:
//   - appid:     应用ID
//   - partnerid: 商户号
//   - prepayid:  预支付交易会话ID
//   - package:   固定值 "Sign=WXPay"
//   - noncestr:  随机字符串
//   - timestamp: 时间戳（秒级）
//   - sign:      SHA256withRSA 签名
//
// 签名串拼接格式（每行一个字段，以 \n 结尾）:
//
//	appid\ntimestamp\nnoncestr\nprepayid\n
//
// 参考文档: https://pay.weixin.qq.com/doc/v3/merchant/4012791455
func (p *Provider) buildAppPayParams(prepayID string) (string, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonceStr, err := generateNonceStr()
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// 构造待签名字符串
	message := p.cfg.AppID + "\n" + timestamp + "\n" + nonceStr + "\n" + prepayID + "\n"

	// 使用商户私钥进行 SHA256withRSA 签名
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("rsa sign: %w", err)
	}
	sign := base64.StdEncoding.EncodeToString(signature)

	// 返回 JSON 格式，APP 端解析后直接传给微信 SDK
	params := map[string]string{
		"appid":     p.cfg.AppID,
		"partnerid": p.cfg.MchID,
		"prepayid":  prepayID,
		"package":   "Sign=WXPay",
		"noncestr":  nonceStr,
		"timestamp": timestamp,
		"sign":      sign,
	}

	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// buildJSAPIPayParams 生成 JSAPI 调起微信支付所需的签名参数。
func (p *Provider) buildJSAPIPayParams(prepayID string) (string, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonceStr, err := generateNonceStr()
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	packageValue := "prepay_id=" + prepayID
	message := p.cfg.AppID + "\n" + timestamp + "\n" + nonceStr + "\n" + packageValue + "\n"
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("rsa sign: %w", err)
	}

	params := map[string]string{
		"appId":     p.cfg.AppID,
		"timeStamp": timestamp,
		"nonceStr":  nonceStr,
		"package":   packageValue,
		"signType":  "RSA",
		"paySign":   base64.StdEncoding.EncodeToString(signature),
	}

	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- 内部辅助函数 ---

func buildH5SceneInfo(req *paymgr.UnifiedOrderRequest) *h5.SceneInfo {
	return &h5.SceneInfo{
		PayerClientIp: core.String(req.ClientIP),
		H5Info: &h5.H5Info{
			Type: core.String("Wap"),
		},
	}
}

// generateNonceStr 生成 32 位随机字符串
func generateNonceStr() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// mapWechatRefundStatus 将 QueryRefund 返回的 Status 指针映射为统一退款状态.
func mapWechatRefundStatus(s *refunddomestic.Status) paymgr.RefundStatus {
	if s == nil {
		return paymgr.RefundStatusError
	}
	return mapWechatRefundStatusString(string(*s))
}

// mapWechatRefundStatusString 将微信退款状态字符串映射为统一退款状态.
//
// 同时覆盖 QueryRefund 响应中的 status 字段和退款异步通知中的 refund_status 字段，
// 两者取值相同：SUCCESS / CLOSED / PROCESSING / ABNORMAL。
func mapWechatRefundStatusString(state string) paymgr.RefundStatus {
	switch state {
	case "SUCCESS":
		return paymgr.RefundStatusSuccess
	case "CLOSED":
		return paymgr.RefundStatusClosed
	case "PROCESSING":
		return paymgr.RefundStatusProcessing
	case "ABNORMAL":
		return paymgr.RefundStatusAbnormal
	default:
		return paymgr.RefundStatusError
	}
}

// mapWechatTradeState 微信交易状态映射到统一状态
func mapWechatTradeState(state string) paymgr.TradeStatus {
	switch state {
	case "SUCCESS":
		return paymgr.TradeStatusPaid
	case "NOTPAY", "USERPAYING":
		return paymgr.TradeStatusPending
	case "CLOSED", "REVOKED", "PAYERROR":
		return paymgr.TradeStatusClosed
	case "REFUND":
		return paymgr.TradeStatusRefunded
	default:
		return paymgr.TradeStatusError
	}
}

// wrapWechatError 包装微信 SDK 错误为统一错误类型
func wrapWechatError(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := errors.AsType[*core.APIError](err); ok {
		code := apiErr.Code
		if code == "" {
			code = "API_ERROR"
		}
		message := apiErr.Message
		if message == "" {
			message = apiErr.Error()
		}
		return paymgr.NewChannelError(
			paymgr.ChannelWechat,
			code,
			message,
			err,
		)
	}
	return paymgr.NewChannelError(paymgr.ChannelWechat, "UNKNOWN", err.Error(), err)
}

// 指针解引用辅助函数，避免 nil panic
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// 转换时间字符串为 time.Time.
func parseTime(t string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if res, err := time.Parse(layout, t); err == nil {
			return res
		}
	}
	return time.Time{}
}

func resolvePrivateKey(cfg *Config) (*rsa.PrivateKey, error) {
	switch {
	case cfg.MchPrivateKey != nil:
		return cfg.MchPrivateKey, nil
	case cfg.MchPrivateKeyPEM != "":
		return utils.LoadPrivateKey(cfg.MchPrivateKeyPEM)
	case cfg.MchPrivateKeyPath != "":
		return utils.LoadPrivateKeyWithPath(cfg.MchPrivateKeyPath)
	default:
		return nil, fmt.Errorf("missing merchant private key")
	}
}

func resolvePlatformCertificate(cfg *Config) (*x509.Certificate, error) {
	switch {
	case cfg.WechatPayCertificate != nil:
		return cfg.WechatPayCertificate, nil
	case cfg.WechatPayCertificatePEM != "":
		return utils.LoadCertificate(cfg.WechatPayCertificatePEM)
	case cfg.WechatPayCertificatePath != "":
		return utils.LoadCertificateWithPath(cfg.WechatPayCertificatePath)
	default:
		return nil, fmt.Errorf("missing platform certificate")
	}
}
