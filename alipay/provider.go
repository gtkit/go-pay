package alipay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-pay/gopay"
	alipaylegacy "github.com/go-pay/gopay/alipay"
	alipayv3 "github.com/go-pay/gopay/alipay/v3"
	"github.com/gtkit/go-pay/paymgr"
)

// Config 支付宝配置.
type Config struct {
	AppID          string // 支付宝应用 ID
	PrivateKey     string // 应用私钥内容
	PrivateKeyPath string // 应用私钥路径
	IsProduction   bool   // true=生产环境, false=沙箱环境

	// 证书模式（gopay v3 唯一支持的模式）—— 以下三项全部填写则启用证书模式
	AppCertPublicKey        string // 应用公钥证书内容
	AppCertPublicKeyPath    string // 应用公钥证书路径
	AlipayRootCert          string // 支付宝根证书内容
	AlipayRootCertPath      string // 支付宝根证书路径
	AlipayCertPublicKey     string // 支付宝公钥证书内容
	AlipayCertPublicKeyPath string // 支付宝公钥证书路径

	// 普通公钥模式 —— 自 v1.3.0 起软降级，运行时返回 ErrNotSupported；
	// 字段保留仅用于编译兼容，实际接入请使用证书模式（WithCertMode / WithCertModePaths）。
	AlipayPublicKey string
}

// Option 用于函数选项模式配置支付宝 Provider。
//
// *Config 也实现了该接口，因此旧的结构体配置调用方式仍可继续使用：
//
//	alipay.NewProvider(&alipay.Config{...})
type Option interface {
	apply(*Config) error
}

type optionFunc func(*Config) error

func (f optionFunc) apply(cfg *Config) error {
	return f(cfg)
}

func (c *Config) apply(dst *Config) error {
	if c == nil {
		return fmt.Errorf("alipay: config is required")
	}
	*dst = *c
	return nil
}

// Validate 校验配置完整性.
func (c *Config) Validate() error {
	if c.AppID == "" {
		return fmt.Errorf("alipay: app_id is required")
	}
	if c.PrivateKey == "" && c.PrivateKeyPath == "" {
		return fmt.Errorf("alipay: private_key or private_key_path is required")
	}
	hasCert := c.hasAppCert() || c.hasRootCert() || c.hasAlipayCert()
	allCert := c.hasAppCert() && c.hasRootCert() && c.hasAlipayCert()
	if hasCert && !allCert {
		return fmt.Errorf("alipay: cert mode requires app cert, root cert and alipay cert")
	}
	if !hasCert && c.AlipayPublicKey == "" {
		return fmt.Errorf("alipay: either cert paths or alipay_public_key is required")
	}
	return nil
}

// UseCertMode 是否使用证书模式.
func (c *Config) UseCertMode() bool {
	return c.hasAppCert() && c.hasRootCert() && c.hasAlipayCert()
}

// Provider 支付宝支付提供者.
type Provider struct {
	cfg    *Config
	client *alipayv3.ClientV3
}

// WithAppID 设置支付宝应用 ID。
func WithAppID(appID string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AppID = appID
		return nil
	})
}

// WithProduction 设置运行环境。
func WithProduction(isProduction bool) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.IsProduction = isProduction
		return nil
	})
}

// WithPrivateKey 设置应用私钥内容。
func WithPrivateKey(privateKey string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.PrivateKey = privateKey
		return nil
	})
}

// WithPrivateKeyPath 通过文件路径设置应用私钥。
func WithPrivateKeyPath(path string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.PrivateKeyPath = path
		return nil
	})
}

// WithCertMode 使用证书模式配置支付宝。
func WithCertMode(appCert, rootCert, alipayCert string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AppCertPublicKey = appCert
		cfg.AlipayRootCert = rootCert
		cfg.AlipayCertPublicKey = alipayCert
		return nil
	})
}

// WithCertModePaths 使用证书文件路径配置支付宝。
func WithCertModePaths(appCertPath, rootCertPath, alipayCertPath string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AppCertPublicKeyPath = appCertPath
		cfg.AlipayRootCertPath = rootCertPath
		cfg.AlipayCertPublicKeyPath = alipayCertPath
		return nil
	})
}

// WithAlipayPublicKey 设置普通公钥模式（自 v1.3.0 起软降级）。
//
// gopay v3 SDK 仅支持证书模式，因此该 Option 在 NewProvider 阶段会触发 ErrNotSupported。
// 字段与 Option 保留仅用于编译兼容，实际接入请改用 WithCertMode / WithCertModePaths。
func WithAlipayPublicKey(publicKey string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AlipayPublicKey = publicKey
		return nil
	})
}

// NewProvider 创建支付宝支付提供者.
func NewProvider(opts ...Option) (*Provider, error) {
	cfg := &Config{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt.apply(cfg); err != nil {
			return nil, err
		}
	}
	return NewProviderWithConfig(cfg)
}

// NewProviderWithConfig 使用结构体配置创建支付宝支付提供者。
//
// 自 v1.3.0 起底层 SDK 切换为 github.com/go-pay/gopay/alipay/v3，仅支持证书模式。
// 仅传 AlipayPublicKey（普通公钥模式）的配置会返回 paymgr.ErrNotSupported 包装错误。
func NewProviderWithConfig(cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("alipay: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if !cfg.UseCertMode() {
		return nil, fmt.Errorf("%w: alipay raw public key mode is not supported by gopay v3 SDK; "+
			"please switch to certificate mode via WithCertMode / WithCertModePaths "+
			"(provide app cert, alipay root cert and alipay public cert)",
			paymgr.ErrNotSupported)
	}

	privateKey, err := resolvePrivateKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("alipay: load private key: %w", err)
	}

	client, err := alipayv3.NewClientV3(cfg.AppID, privateKey, cfg.IsProduction)
	if err != nil {
		return nil, fmt.Errorf("alipay: init client: %w", err)
	}

	appCert, err := resolveSourceBytes(cfg.AppCertPublicKey, cfg.AppCertPublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("alipay: load app cert: %w", err)
	}
	rootCert, err := resolveSourceBytes(cfg.AlipayRootCert, cfg.AlipayRootCertPath)
	if err != nil {
		return nil, fmt.Errorf("alipay: load root cert: %w", err)
	}
	publicCert, err := resolveSourceBytes(cfg.AlipayCertPublicKey, cfg.AlipayCertPublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("alipay: load alipay cert: %w", err)
	}
	if err := client.SetCert(appCert, rootCert, publicCert); err != nil {
		return nil, fmt.Errorf("alipay: set cert: %w", err)
	}

	return &Provider{
		cfg:    cfg,
		client: client,
	}, nil
}

// Channel 实现 paymgr.Provider 接口.
func (p *Provider) Channel() paymgr.Channel {
	return paymgr.ChannelAlipay
}

// UnifiedOrder 统一下单.
func (p *Provider) UnifiedOrder(ctx context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
	resp := &paymgr.UnifiedOrderResponse{
		Channel: paymgr.ChannelAlipay,
	}

	amount := centToYuan(req.TotalAmount)

	var timeoutExpress string
	if !req.ExpireAt.IsZero() {
		duration := time.Until(req.ExpireAt)
		if duration > 0 {
			timeoutExpress = strconv.Itoa(int(duration.Minutes())) + "m"
		}
	}

	passbackParams := encodePassbackParams(req.Metadata)

	switch req.TradeType {
	case paymgr.TradeTypeNative:
		bm := buildAlipayCommonBody(req, amount, timeoutExpress, passbackParams)
		aliRsp, err := p.client.TradePrecreate(ctx, bm)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		if rspErr := aliRspError(aliRsp.StatusCode, aliRsp.ErrResponse); rspErr != nil {
			return nil, rspErr
		}
		resp.CodeURL = aliRsp.QrCode

	case paymgr.TradeTypeJSAPI:
		bm := buildAlipayCommonBody(req, amount, timeoutExpress, passbackParams)
		// product_code=JSAPI_PAY 标识小程序支付场景；op_app_id 是实际操作的小程序 ID，
		// 单应用场景下与主 AppID 一致——若未来出现「一个商户主体下多个小程序」场景，
		// 可给 paymgr.UnifiedOrderRequest 新增可选字段覆盖此处默认值。
		bm.Set("product_code", "JSAPI_PAY")
		bm.Set("op_app_id", p.cfg.AppID)
		if req.OpenID != "" {
			bm.Set("buyer_id", req.OpenID)
		}
		aliRsp, err := p.client.TradeCreate(ctx, bm)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		if rspErr := aliRspError(aliRsp.StatusCode, aliRsp.ErrResponse); rspErr != nil {
			return nil, rspErr
		}
		resp.PrepayID = aliRsp.TradeNo

	case paymgr.TradeTypeApp:
		bm := buildAlipayCommonBody(req, amount, timeoutExpress, passbackParams)
		bm.Set("product_code", "QUICK_MSECURITY_PAY")
		orderStr, err := p.client.TradeAppPay(ctx, bm)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		resp.AppParams = orderStr

	case paymgr.TradeTypeH5:
		bm := buildAlipayCommonBody(req, amount, timeoutExpress, passbackParams)
		bm.Set("product_code", "QUICK_WAP_WAY")
		if req.ReturnURL != "" {
			bm.Set("return_url", req.ReturnURL)
		}
		payURL, err := p.client.TradeWapPay(ctx, bm)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		resp.PayURL = payURL

	case paymgr.TradeTypePage:
		bm := buildAlipayCommonBody(req, amount, timeoutExpress, passbackParams)
		bm.Set("product_code", "FAST_INSTANT_TRADE_PAY")
		if req.ReturnURL != "" {
			bm.Set("return_url", req.ReturnURL)
		}
		payURL, err := p.client.TradePagePay(ctx, bm)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		resp.PayURL = payURL

	default:
		return nil, fmt.Errorf("%w: %s", paymgr.ErrUnsupportedType, req.TradeType)
	}

	return resp, nil
}

// QueryOrder 查询订单.
func (p *Provider) QueryOrder(ctx context.Context, req *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	bm := gopay.BodyMap{}
	if req.TransactionID != "" {
		bm.Set("trade_no", req.TransactionID)
	} else {
		bm.Set("out_trade_no", req.OutTradeNo)
	}

	aliRsp, err := p.client.TradeQuery(ctx, bm)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if aliRsp.StatusCode != http.StatusOK {
		if aliRsp.ErrResponse.Code == "ACQ.TRADE_NOT_EXIST" {
			return nil, paymgr.ErrOrderNotFound
		}
		return nil, paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			aliRsp.ErrResponse.Code,
			aliRsp.ErrResponse.Message,
			nil,
		)
	}

	resp := &paymgr.QueryOrderResponse{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    aliRsp.OutTradeNo,
		TransactionID: aliRsp.TradeNo,
		TradeStatus:   mapAlipayTradeStatus(aliRsp.TradeStatus),
		BuyerID:       aliRsp.BuyerUserId,
	}

	if aliRsp.TotalAmount != "" {
		resp.TotalAmount = yuanToCent(aliRsp.TotalAmount)
	}

	if aliRsp.SendPayDate != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", aliRsp.SendPayDate); err == nil {
			resp.PaidAt = t
		}
	}

	return resp, nil
}

// CloseOrder 关闭订单.
func (p *Provider) CloseOrder(ctx context.Context, req *paymgr.CloseOrderRequest) error {
	bm := gopay.BodyMap{}
	bm.Set("out_trade_no", req.OutTradeNo)

	aliRsp, err := p.client.TradeClose(ctx, bm)
	if err != nil {
		return wrapAlipayError(err)
	}
	if aliRsp.StatusCode != http.StatusOK {
		// 已关闭的订单重复关闭不算错误
		if aliRsp.ErrResponse.Code == "ACQ.TRADE_HAS_CLOSE" {
			return nil
		}
		return paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			aliRsp.ErrResponse.Code,
			aliRsp.ErrResponse.Message,
			nil,
		)
	}
	return nil
}

// Refund 申请退款.
func (p *Provider) Refund(ctx context.Context, req *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	bm := gopay.BodyMap{}
	bm.Set("out_request_no", req.OutRefundNo).
		Set("refund_amount", centToYuan(req.RefundAmount))
	if req.Reason != "" {
		bm.Set("refund_reason", req.Reason)
	}
	if req.TransactionID != "" {
		bm.Set("trade_no", req.TransactionID)
	} else {
		bm.Set("out_trade_no", req.OutTradeNo)
	}

	aliRsp, err := p.client.TradeRefund(ctx, bm)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if rspErr := aliRspError(aliRsp.StatusCode, aliRsp.ErrResponse); rspErr != nil {
		return nil, rspErr
	}

	return &paymgr.RefundResponse{
		Channel:      paymgr.ChannelAlipay,
		OutRefundNo:  req.OutRefundNo,
		RefundID:     aliRsp.TradeNo, // 支付宝退款无独立退款号，复用交易号
		RefundAmount: yuanToCent(aliRsp.RefundFee),
	}, nil
}

// QueryRefund 查询退款.
//
// 调用 alipay.trade.fastpay.refund.query，按退款请求号（OutRequestNo 即商户退款单号）查询。
// 未查到退款记录时返回 ErrOrderNotFound。
func (p *Provider) QueryRefund(ctx context.Context, req *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	bm := gopay.BodyMap{}
	bm.Set("out_request_no", req.OutRefundNo)
	if req.TransactionID != "" {
		bm.Set("trade_no", req.TransactionID)
	} else {
		bm.Set("out_trade_no", req.OutTradeNo)
	}

	aliRsp, err := p.client.TradeFastPayRefundQuery(ctx, bm)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if aliRsp.StatusCode != http.StatusOK {
		if aliRsp.ErrResponse.Code == "ACQ.TRADE_NOT_EXIST" {
			return nil, paymgr.ErrOrderNotFound
		}
		return nil, paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			aliRsp.ErrResponse.Code,
			aliRsp.ErrResponse.Message,
			nil,
		)
	}

	resp := &paymgr.QueryRefundResponse{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    aliRsp.OutTradeNo,
		TransactionID: aliRsp.TradeNo,
		OutRefundNo:   aliRsp.OutRequestNo,
		RefundID:      aliRsp.TradeNo, // 支付宝无独立退款号
		RefundStatus:  mapAlipayRefundStatus(aliRsp.RefundStatus),
	}
	if aliRsp.RefundAmount != "" {
		resp.RefundAmount = yuanToCent(aliRsp.RefundAmount)
	}
	if aliRsp.TotalAmount != "" {
		resp.TotalAmount = yuanToCent(aliRsp.TotalAmount)
	}
	if aliRsp.GmtRefundPay != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", aliRsp.GmtRefundPay); err == nil {
			resp.RefundedAt = t
		}
	}
	return resp, nil
}

// ParseNotify 解析异步通知.
//
// gopay/alipay/v3 子包未提供独立的 notify 验签 API，此处复用 gopay 老版 alipay 包的
// ParseNotifyToBodyMap + VerifySignWithCert（异步通知验签机制与协议代次无关，
// 始终基于支付宝公钥证书 RSA-SHA256 验签）。
func (p *Provider) ParseNotify(_ context.Context, r *http.Request) (*paymgr.NotifyResult, error) {
	bm, err := alipaylegacy.ParseNotifyToBodyMap(r)
	if err != nil {
		return nil, fmt.Errorf("%w: parse form: %v", paymgr.ErrInvalidNotify, err)
	}

	cert, err := resolveSourceBytes(p.cfg.AlipayCertPublicKey, p.cfg.AlipayCertPublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: load alipay cert: %v", paymgr.ErrInvalidSign, err)
	}
	ok, err := alipaylegacy.VerifySignWithCert(cert, bm)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidSign, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: signature mismatch", paymgr.ErrInvalidSign)
	}

	result := &paymgr.NotifyResult{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    bm.GetString("out_trade_no"),
		TransactionID: bm.GetString("trade_no"),
		TradeStatus:   mapAlipayTradeStatus(bm.GetString("trade_status")),
		BuyerID:       bm.GetString("buyer_id"),
	}

	// 退款事件识别：
	// 支付宝退款没有独立通知端点，退款结果与支付结果共用同一个 notify_url。
	// 当 gmt_refund 或 refund_fee 非空时，本次回调是退款事件；此时原始 trade_status
	// 在全额退款下为 TRADE_CLOSED、在部分退款下仍为 TRADE_SUCCESS，
	// 业务层无法仅凭交易状态区分，因此在此显式覆盖为 Refunded。
	if bm.GetString("gmt_refund") != "" || bm.GetString("refund_fee") != "" {
		result.TradeStatus = paymgr.TradeStatusRefunded
	}

	if amt := bm.GetString("total_amount"); amt != "" {
		result.TotalAmount = yuanToCent(amt)
	}
	if gp := bm.GetString("gmt_payment"); gp != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", gp); err == nil {
			result.PaidAt = t
		}
	}
	if pp := bm.GetString("passback_params"); pp != "" {
		metadata := decodePassbackParams(pp)
		if len(metadata) > 0 {
			result.Metadata = metadata
		}
	}

	return result, nil
}

// ParseRefundNotify 解析退款异步通知.
//
// 支付宝没有独立的退款异步通知端点，退款结果通过与支付相同的 notify_url 回调。
// 请使用 ParseNotify 解析并检查 TradeStatus == TradeStatusRefunded。
func (p *Provider) ParseRefundNotify(_ context.Context, _ *http.Request) (*paymgr.RefundNotifyResult, error) {
	return nil, fmt.Errorf("%w: alipay refund result is delivered via the payment notify endpoint; "+
		"call ParseNotify and check TradeStatus==%s instead",
		paymgr.ErrNotSupported, paymgr.TradeStatusRefunded)
}

// ACKNotify 回写成功响应。
//
// 支付宝需返回纯文本 "success".
func (p *Provider) ACKNotify(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("success"))
}

// --- 内部辅助函数 ---

// buildAlipayCommonBody 构造 gopay v3 通用 BodyMap 参数（共用字段）。
func buildAlipayCommonBody(req *paymgr.UnifiedOrderRequest, amount, timeoutExpress, passbackParams string) gopay.BodyMap {
	bm := gopay.BodyMap{}
	bm.Set("out_trade_no", req.OutTradeNo).
		Set("total_amount", amount).
		Set("subject", req.Subject)
	if req.NotifyURL != "" {
		bm.Set("notify_url", req.NotifyURL)
	}
	if timeoutExpress != "" {
		bm.Set("timeout_express", timeoutExpress)
	}
	if passbackParams != "" {
		bm.Set("passback_params", passbackParams)
	}
	return bm
}

// aliRspError 把 v3 RESTful 非 200 响应统一转换为 paymgr.ChannelError。
func aliRspError(statusCode int, errRsp alipayv3.ErrResponse) error {
	if statusCode == http.StatusOK {
		return nil
	}
	return paymgr.NewChannelError(paymgr.ChannelAlipay, errRsp.Code, errRsp.Message, nil)
}

// mapAlipayRefundStatus 支付宝退款状态映射.
func mapAlipayRefundStatus(status string) paymgr.RefundStatus {
	switch status {
	case "REFUND_SUCCESS":
		return paymgr.RefundStatusSuccess
	case "REFUND_PROCESSING":
		return paymgr.RefundStatusProcessing
	case "REFUND_FAIL":
		return paymgr.RefundStatusError
	default:
		return paymgr.RefundStatusError
	}
}

// mapAlipayTradeStatus 支付宝交易状态映射.
func mapAlipayTradeStatus(status string) paymgr.TradeStatus {
	switch status {
	case "WAIT_BUYER_PAY":
		return paymgr.TradeStatusPending
	case "TRADE_SUCCESS":
		return paymgr.TradeStatusPaid
	case "TRADE_FINISHED":
		return paymgr.TradeStatusPaid // TRADE_FINISHED 表示交易完结（不可退款），但属于已支付
	case "TRADE_CLOSED":
		return paymgr.TradeStatusClosed
	default:
		return paymgr.TradeStatusError
	}
}

// wrapAlipayError 包装 SDK 错误（非业务错误，例如网络/序列化）。
func wrapAlipayError(err error) error {
	if err == nil {
		return nil
	}
	return paymgr.NewChannelError(paymgr.ChannelAlipay, "SDK_ERROR", err.Error(), err)
}

// centToYuan 分转元，返回两位小数字符串.
func centToYuan(cent int64) string {
	sign := ""
	var abs uint64
	if cent < 0 {
		sign = "-"
		abs = uint64(-(cent + 1))
		abs++
	} else {
		abs = uint64(cent)
	}
	return fmt.Sprintf("%s%d.%02d", sign, abs/100, abs%100)
}

// yuanToCent 元转分.
func yuanToCent(yuan string) int64 {
	const maxInt64 = int64(^uint64(0) >> 1)

	s := strings.TrimSpace(yuan)
	if s == "" {
		return 0
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0
	}

	parts := strings.SplitN(s, ".", 3)
	if len(parts) > 2 {
		return 0
	}

	wholePart := parts[0]
	if wholePart == "" {
		wholePart = "0"
	}

	fractionPart := "00"
	if len(parts) == 2 {
		fractionPart = parts[1]
		switch {
		case len(fractionPart) == 0:
			fractionPart = "00"
		case len(fractionPart) == 1:
			fractionPart += "0"
		case len(fractionPart) >= 2:
			fractionPart = fractionPart[:2]
		}
	}

	whole, err := strconv.ParseInt(wholePart, 10, 64)
	if err != nil || whole < 0 {
		return 0
	}
	fraction, err := strconv.ParseInt(fractionPart, 10, 64)
	if err != nil || fraction < 0 {
		return 0
	}

	if whole > (maxInt64-fraction)/100 {
		return 0
	}

	total := whole*100 + fraction
	if negative {
		return -total
	}
	return total
}

func encodePassbackParams(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}

	values := make(url.Values, len(metadata))
	for k, v := range metadata {
		values.Set(k, v)
	}
	return values.Encode()
}

func decodePassbackParams(raw string) map[string]string {
	if raw == "" {
		return nil
	}

	if values, err := url.ParseQuery(raw); err == nil && len(values) > 0 && !hasEncodedSeparator(values) {
		return flattenValues(values)
	}

	if decoded, err := url.QueryUnescape(raw); err == nil && decoded != raw {
		if values, err := url.ParseQuery(decoded); err == nil && len(values) > 0 && !hasEncodedSeparator(values) {
			return flattenValues(values)
		}
	}

	return nil
}

func hasEncodedSeparator(values url.Values) bool {
	for k := range values {
		if strings.ContainsAny(k, "=&") {
			return true
		}
	}
	return false
}

func flattenValues(values url.Values) map[string]string {
	result := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) == 0 {
			result[k] = ""
			continue
		}
		result[k] = v[0]
	}
	return result
}

func (c *Config) hasAppCert() bool {
	return c.AppCertPublicKey != "" || c.AppCertPublicKeyPath != ""
}

func (c *Config) hasRootCert() bool {
	return c.AlipayRootCert != "" || c.AlipayRootCertPath != ""
}

func (c *Config) hasAlipayCert() bool {
	return c.AlipayCertPublicKey != "" || c.AlipayCertPublicKeyPath != ""
}

func resolvePrivateKey(cfg *Config) (string, error) {
	return resolveSource(cfg.PrivateKey, cfg.PrivateKeyPath)
}

func resolveSource(value, path string) (string, error) {
	if value != "" {
		return value, nil
	}
	if path == "" {
		return "", fmt.Errorf("missing source value")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resolveSourceBytes(value, path string) ([]byte, error) {
	if value != "" {
		return []byte(value), nil
	}
	if path == "" {
		return nil, fmt.Errorf("missing source value")
	}
	return os.ReadFile(path)
}
