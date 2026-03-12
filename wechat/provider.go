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
	"fmt"
	"net/http"

	"github.com/gtkit/go-pay/manager"

	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/app"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// Config 微信支付配置
type Config struct {
	AppID               string          // 开放平台应用的 appid（微信开放平台注册的移动应用）
	MchID               string          // 商户号
	MchCertSerialNumber string          // 商户证书序列号
	MchAPIv3Key         string          // 商户 APIv3 密钥（用于回调解密）
	MchPrivateKeyPath   string          // 商户私钥文件路径（PEM 格式）
	MchPrivateKey       *rsa.PrivateKey // 商户私钥（直接提供则忽略 Path）
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
	if c.MchPrivateKey == nil && c.MchPrivateKeyPath == "" {
		return fmt.Errorf("wechat: mch_private_key or mch_private_key_path is required")
	}
	return nil
}

// NewProvider 创建微信支付提供者.
//
// 初始化时会自动注册平台证书下载器，后续自动轮转平台证书。
func NewProvider(ctx context.Context, cfg *Config) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 加载商户私钥
	privateKey := cfg.MchPrivateKey
	if privateKey == nil {
		var err error
		privateKey, err = utils.LoadPrivateKeyWithPath(cfg.MchPrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("wechat: load private key: %w", err)
		}
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
	wechatPayCert, err := utils.LoadCertificateWithPath("/path/to/wechatpay_cert.pem")
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

// Channel 实现 manager.Provider 接口.
func (p *Provider) Channel() manager.Channel {
	return manager.ChannelWechat
}

// UnifiedOrder 统一下单
//
// 主要支持 APP 支付，同时保留 Native 扫码支付能力。
func (p *Provider) UnifiedOrder(ctx context.Context, req *manager.UnifiedOrderRequest) (*manager.UnifiedOrderResponse, error) {
	resp := &manager.UnifiedOrderResponse{
		Channel: manager.ChannelWechat,
	}

	// 过期时间
	var timeExpire *time.Time
	if !req.ExpireAt.IsZero() {
		timeExpire = &req.ExpireAt
	}

	// 附加数据（JSON 序列化后存入 attach 字段，回调时原样返回）
	var attach *string
	if len(req.Metadata) > 0 {
		data, _ := json.Marshal(req.Metadata)
		attach = new(string(data))
	}

	switch req.TradeType {
	case manager.TradeTypeApp:
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

	case manager.TradeTypeNative:
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

	default:
		return nil, fmt.Errorf("%w: wechat provider supports app and native, got %s",
			manager.ErrUnsupportedType, req.TradeType)
	}

	return resp, nil
}

// QueryOrder 查询订单
//
// 使用 app.AppApiService 查询，返回通用 pays.Transaction。
// 微信支付的查询 API 与支付方式无关，底层调用相同的 endpoint。
func (p *Provider) QueryOrder(ctx context.Context, req *manager.QueryOrderRequest) (*manager.QueryOrderResponse, error) {
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

	resp := &manager.QueryOrderResponse{
		Channel:       manager.ChannelWechat,
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
func (p *Provider) CloseOrder(ctx context.Context, req *manager.CloseOrderRequest) error {
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
func (p *Provider) Refund(ctx context.Context, req *manager.RefundRequest) (*manager.RefundResponse, error) {
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

	return &manager.RefundResponse{
		Channel:      manager.ChannelWechat,
		OutRefundNo:  derefString(result.OutRefundNo),
		RefundID:     derefString(result.RefundId),
		RefundAmount: derefInt64(result.Amount.Refund),
	}, nil
}

// ParseNotify 解析异步通知
//
// 回调通知的 Transaction 结构与支付方式无关（APP/JSAPI/Native 共用同一格式），
// 使用 pays.Transaction 通用结构体。
func (p *Provider) ParseNotify(ctx context.Context, r *http.Request) (*manager.NotifyResult, error) {
	if p.notifyHandler == nil {
		return nil, fmt.Errorf("wechat: notify handler not initialized, " +
			"please configure it in NewProvider (see comments for setup instructions)")
	}

	// 解析并验签回调通知，解密后反序列化为 payments.Transaction
	var transaction payments.Transaction
	_, err := p.notifyHandler.ParseNotifyRequest(ctx, r, &transaction)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", manager.ErrInvalidNotify, err)
	}

	result := &manager.NotifyResult{
		Channel:       manager.ChannelWechat,
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
	nonceStr := generateNonceStr()

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

// --- 内部辅助函数 ---

// generateNonceStr 生成 32 位随机字符串
func generateNonceStr() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// mapWechatTradeState 微信交易状态映射到统一状态
func mapWechatTradeState(state string) manager.TradeStatus {
	switch state {
	case "SUCCESS":
		return manager.TradeStatusPaid
	case "NOTPAY", "USERPAYING":
		return manager.TradeStatusPending
	case "CLOSED", "REVOKED", "PAYERROR":
		return manager.TradeStatusClosed
	case "REFUND":
		return manager.TradeStatusRefunded
	default:
		return manager.TradeStatusError
	}
}

// wrapWechatError 包装微信 SDK 错误为统一错误类型
func wrapWechatError(err error) error {
	if err == nil {
		return nil
	}
	// 尝试判断是否为 API 错误
	if core.IsAPIError(err, "") {
		return manager.NewChannelError(
			manager.ChannelWechat,
			"API_ERROR",
			err.Error(),
			err,
		)
	}
	return manager.NewChannelError(manager.ChannelWechat, "UNKNOWN", err.Error(), err)
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
	// 定义时间格式（必须与字符串格式匹配）
	layout := "2006-01-02 15:04:05"

	// 转换为 time.Time
	if res, err := time.Parse(layout, t); err == nil {
		return res
	}
	return time.Time{}
}
