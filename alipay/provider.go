package alipay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"strconv"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/smartwalle/alipay/v3"
)

// Config 支付宝配置.
type Config struct {
	AppID          string // 支付宝应用 ID
	PrivateKey     string // 应用私钥内容
	PrivateKeyPath string // 应用私钥路径
	IsProduction   bool   // true=生产环境, false=沙箱环境

	// 证书模式（推荐）—— 以下三项全部填写则启用证书模式
	AppCertPublicKey        string // 应用公钥证书内容
	AppCertPublicKeyPath    string // 应用公钥证书路径
	AlipayRootCert          string // 支付宝根证书内容
	AlipayRootCertPath      string // 支付宝根证书路径
	AlipayCertPublicKey     string // 支付宝公钥证书内容
	AlipayCertPublicKeyPath string // 支付宝公钥证书路径

	// 普通公钥模式 —— 仅当证书路径全部为空时使用
	AlipayPublicKey string // 支付宝公钥
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
	// 证书模式三项必须全部提供或全部为空
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
	client *alipay.Client
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

// WithAlipayPublicKey 使用普通公钥模式配置支付宝。
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
func NewProviderWithConfig(cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("alipay: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	privateKey, err := resolvePrivateKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("alipay: load private key: %w", err)
	}

	client, err := alipay.New(cfg.AppID, privateKey, cfg.IsProduction)
	if err != nil {
		return nil, fmt.Errorf("alipay: init client: %w", err)
	}

	// 根据配置选择签名验签方式
	if cfg.UseCertMode() {
		// 证书模式（推荐，安全性更高，支持证书自动续期）
		appCert, err := resolveSource(cfg.AppCertPublicKey, cfg.AppCertPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("alipay: load app cert: %w", err)
		}
		if err := client.LoadAppCertPublicKey(appCert); err != nil {
			return nil, fmt.Errorf("alipay: load app cert: %w", err)
		}
		rootCert, err := resolveSource(cfg.AlipayRootCert, cfg.AlipayRootCertPath)
		if err != nil {
			return nil, fmt.Errorf("alipay: load root cert: %w", err)
		}
		if err := client.LoadAliPayRootCert(rootCert); err != nil {
			return nil, fmt.Errorf("alipay: load root cert: %w", err)
		}
		alipayCert, err := resolveSource(cfg.AlipayCertPublicKey, cfg.AlipayCertPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("alipay: load alipay cert: %w", err)
		}
		if err := client.LoadAlipayCertPublicKey(alipayCert); err != nil {
			return nil, fmt.Errorf("alipay: load alipay cert: %w", err)
		}
	} else {
		// 普通公钥模式
		if err := client.LoadAliPayPublicKey(cfg.AlipayPublicKey); err != nil {
			return nil, fmt.Errorf("alipay: load alipay public key: %w", err)
		}
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

	// 金额转换：分 -> 元（支付宝金额单位为元，保留两位小数）
	amount := centToYuan(req.TotalAmount)

	// 过期时间处理
	var timeoutExpress string
	if !req.ExpireAt.IsZero() {
		duration := time.Until(req.ExpireAt)
		if duration > 0 {
			timeoutExpress = strconv.Itoa(int(duration.Minutes())) + "m"
		}
	}

	// 附加数据
	passbackParams := encodePassbackParams(req.Metadata)

	switch req.TradeType {
	case paymgr.TradeTypeNative:
		// 当面付 —— 生成二维码
		trade := alipay.TradePreCreate{}
		trade.OutTradeNo = req.OutTradeNo
		trade.TotalAmount = amount
		trade.Subject = req.Subject
		trade.NotifyURL = req.NotifyURL
		if timeoutExpress != "" {
			trade.TimeoutExpress = timeoutExpress
		}
		if passbackParams != "" {
			trade.PassbackParams = passbackParams
		}

		result, err := p.client.TradePreCreate(ctx, trade)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		if !result.IsSuccess() {
			return nil, paymgr.NewChannelError(
				paymgr.ChannelAlipay,
				result.SubCode,
				result.SubMsg,
				nil,
			)
		}
		resp.CodeURL = result.QRCode

	case paymgr.TradeTypeJSAPI:
		// 支付宝小程序支付 —— 使用 alipay.trade.create
		trade := alipay.TradeCreate{}
		trade.OutTradeNo = req.OutTradeNo
		trade.TotalAmount = amount
		trade.Subject = req.Subject
		trade.NotifyURL = req.NotifyURL
		// 支付宝 JSAPI 场景下使用 buyer_id
		if req.OpenID != "" {
			trade.BuyerId = req.OpenID
		}
		if timeoutExpress != "" {
			trade.TimeoutExpress = timeoutExpress
		}

		result, err := p.client.TradeCreate(ctx, trade)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		if !result.IsSuccess() {
			return nil, paymgr.NewChannelError(
				paymgr.ChannelAlipay,
				result.SubCode,
				result.SubMsg,
				nil,
			)
		}
		resp.PrepayID = result.TradeNo

	case paymgr.TradeTypeApp:
		// APP 支付 —— 返回签名后的订单字符串
		trade := alipay.TradeAppPay{}
		trade.OutTradeNo = req.OutTradeNo
		trade.TotalAmount = amount
		trade.Subject = req.Subject
		trade.NotifyURL = req.NotifyURL
		if timeoutExpress != "" {
			trade.TimeoutExpress = timeoutExpress
		}
		if passbackParams != "" {
			trade.PassbackParams = passbackParams
		}

		result, err := p.client.TradeAppPay(trade)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		// TradeAppPay 返回的是签名后的完整参数字符串，APP 端直接调起
		resp.AppParams = result

	case paymgr.TradeTypeH5:
		// 手机网站支付
		trade := alipay.TradeWapPay{}
		trade.OutTradeNo = req.OutTradeNo
		trade.TotalAmount = amount
		trade.Subject = req.Subject
		trade.NotifyURL = req.NotifyURL
		if req.ReturnURL != "" {
			trade.ReturnURL = req.ReturnURL
		}
		if timeoutExpress != "" {
			trade.TimeoutExpress = timeoutExpress
		}
		if passbackParams != "" {
			trade.PassbackParams = passbackParams
		}

		result, err := p.client.TradeWapPay(trade)
		if err != nil {
			return nil, wrapAlipayError(err)
		}
		// TradeWapPay 返回跳转 URL
		resp.PayURL = result.String()

	default:
		return nil, fmt.Errorf("%w: %s", paymgr.ErrUnsupportedType, req.TradeType)
	}

	return resp, nil
}

// QueryOrder 查询订单.
func (p *Provider) QueryOrder(ctx context.Context, req *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	trade := alipay.TradeQuery{}
	if req.TransactionID != "" {
		trade.TradeNo = req.TransactionID
	} else {
		trade.OutTradeNo = req.OutTradeNo
	}

	result, err := p.client.TradeQuery(ctx, trade)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if !result.IsSuccess() {
		// 特殊处理：订单不存在
		if result.SubCode == "ACQ.TRADE_NOT_EXIST" {
			return nil, paymgr.ErrOrderNotFound
		}
		return nil, paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			result.SubCode,
			result.SubMsg,
			nil,
		)
	}

	resp := &paymgr.QueryOrderResponse{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    result.OutTradeNo,
		TransactionID: result.TradeNo,
		TradeStatus:   mapAlipayTradeStatus(result.TradeStatus),
		BuyerID:       result.BuyerUserId,
	}

	// 金额转换：元 -> 分
	if result.TotalAmount != "" {
		resp.TotalAmount = yuanToCent(result.TotalAmount)
	}

	// 支付时间解析
	if result.SendPayDate != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", result.SendPayDate); err == nil {
			resp.PaidAt = t
		}
	}

	return resp, nil
}

// CloseOrder 关闭订单.
func (p *Provider) CloseOrder(ctx context.Context, req *paymgr.CloseOrderRequest) error {
	trade := alipay.TradeClose{}
	trade.OutTradeNo = req.OutTradeNo

	result, err := p.client.TradeClose(ctx, trade)
	if err != nil {
		return wrapAlipayError(err)
	}
	if !result.IsSuccess() {
		// 已关闭的订单重复关闭不算错误
		if result.SubCode == "ACQ.TRADE_HAS_CLOSE" {
			return nil
		}
		return paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			result.SubCode,
			result.SubMsg,
			nil,
		)
	}
	return nil
}

// Refund 申请退款.
func (p *Provider) Refund(ctx context.Context, req *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	trade := alipay.TradeRefund{}
	trade.OutRequestNo = req.OutRefundNo
	trade.RefundAmount = centToYuan(req.RefundAmount)
	trade.RefundReason = req.Reason

	if req.TransactionID != "" {
		trade.TradeNo = req.TransactionID
	} else {
		trade.OutTradeNo = req.OutTradeNo
	}

	result, err := p.client.TradeRefund(ctx, trade)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if !result.IsSuccess() {
		return nil, paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			result.SubCode,
			result.SubMsg,
			nil,
		)
	}

	refundAmount := yuanToCent(result.RefundFee)

	return &paymgr.RefundResponse{
		Channel:      paymgr.ChannelAlipay,
		OutRefundNo:  req.OutRefundNo,
		RefundID:     result.TradeNo, // 支付宝退款无单独退款号，使用交易号
		RefundAmount: refundAmount,
	}, nil
}

// QueryRefund 查询退款.
//
// 调用 alipay.trade.fastpay.refund.query，按退款请求号（OutRequestNo 即商户退款单号）查询。
// 未查到退款记录时返回 ErrOrderNotFound。
func (p *Provider) QueryRefund(ctx context.Context, req *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	trade := alipay.TradeFastPayRefundQuery{}
	trade.OutRequestNo = req.OutRefundNo
	if req.TransactionID != "" {
		trade.TradeNo = req.TransactionID
	} else {
		trade.OutTradeNo = req.OutTradeNo
	}

	result, err := p.client.TradeFastPayRefundQuery(ctx, trade)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	if !result.IsSuccess() {
		if result.SubCode == "ACQ.TRADE_NOT_EXIST" {
			return nil, paymgr.ErrOrderNotFound
		}
		return nil, paymgr.NewChannelError(
			paymgr.ChannelAlipay,
			result.SubCode,
			result.SubMsg,
			nil,
		)
	}

	resp := &paymgr.QueryRefundResponse{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    result.OutTradeNo,
		TransactionID: result.TradeNo,
		OutRefundNo:   result.OutRequestNo,
		RefundID:      result.TradeNo, // 支付宝无独立退款号，退款单通过交易号 + 退款请求号联合定位
		RefundStatus:  mapAlipayRefundStatus(result.RefundStatus),
	}
	if result.RefundAmount != "" {
		resp.RefundAmount = yuanToCent(result.RefundAmount)
	}
	if result.TotalAmount != "" {
		resp.TotalAmount = yuanToCent(result.TotalAmount)
	}
	if result.GMTRefundPay != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", result.GMTRefundPay); err == nil {
			resp.RefundedAt = t
		}
	}
	return resp, nil
}

// ParseNotify 解析异步通知.
//
// smartwalle/alipay 的 DecodeNotification 内部已完成验签。
func (p *Provider) ParseNotify(ctx context.Context, r *http.Request) (*paymgr.NotifyResult, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("%w: parse form: %v", paymgr.ErrInvalidNotify, err)
	}

	// DecodeNotification 内部调用 VerifySign 验签
	noti, err := p.client.DecodeNotification(ctx, r.Form)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidSign, err)
	}

	result := &paymgr.NotifyResult{
		Channel:       paymgr.ChannelAlipay,
		OutTradeNo:    noti.OutTradeNo,
		TransactionID: noti.TradeNo,
		TradeStatus:   mapAlipayTradeStatus(noti.TradeStatus),
		BuyerID:       noti.BuyerId,
	}

	// 退款事件识别：
	// 支付宝退款没有独立通知端点，退款结果与支付结果共用同一个 notify_url。
	// 当 GmtRefund 或 RefundFee 非空时，本次回调是退款事件；此时原始 trade_status
	// 在全额退款下为 TRADE_CLOSED、在部分退款下仍为 TRADE_SUCCESS，
	// 业务层无法仅凭交易状态区分，因此在此显式覆盖为 Refunded。
	if noti.GmtRefund != "" || noti.RefundFee != "" {
		result.TradeStatus = paymgr.TradeStatusRefunded
	}

	// 金额
	if noti.TotalAmount != "" {
		result.TotalAmount = yuanToCent(noti.TotalAmount)
	}

	// 支付时间
	if noti.GmtPayment != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", noti.GmtPayment); err == nil {
			result.PaidAt = t
		}
	}

	// 附加数据
	if noti.PassbackParams != "" {
		metadata := decodePassbackParams(noti.PassbackParams)
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

// ACKNotify 回写成功响应
//
// 支付宝需返回纯文本 "success".
func (p *Provider) ACKNotify(w http.ResponseWriter) {
	alipay.ACKNotification(w)
}

// --- 内部辅助函数 ---

// mapAlipayRefundStatus 支付宝退款状态映射.
//
// 支付宝文档: refund_status 仅在退款处理中/成功时返回具体值，未返回该字段表示
// 退款请求未收到或退款失败，统一归类为 Error。
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
func mapAlipayTradeStatus(status alipay.TradeStatus) paymgr.TradeStatus {
	switch status {
	case alipay.TradeStatusWaitBuyerPay:
		return paymgr.TradeStatusPending
	case alipay.TradeStatusSuccess:
		return paymgr.TradeStatusPaid
	case alipay.TradeStatusFinished:
		return paymgr.TradeStatusPaid // TRADE_FINISHED 表示交易完结（不可退款），但属于已支付
	case alipay.TradeStatusClosed:
		return paymgr.TradeStatusClosed
	default:
		return paymgr.TradeStatusError
	}
}

// wrapAlipayError 包装支付宝 SDK 错误.
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

	// 优先按标准 query string 解析
	if values, err := url.ParseQuery(raw); err == nil && len(values) > 0 && !hasEncodedSeparator(values) {
		return flattenValues(values)
	}

	// 兜底：兼容整串被 URL-encode 过一次的旧版格式（例如 "a%3D1%26b%3D2"）。
	// 这类输入里 & / = 都被转义过，ParseQuery 会把整串当成一个 key，
	// 需要先 QueryUnescape 再解析。
	if decoded, err := url.QueryUnescape(raw); err == nil && decoded != raw {
		if values, err := url.ParseQuery(decoded); err == nil && len(values) > 0 && !hasEncodedSeparator(values) {
			return flattenValues(values)
		}
	}

	return nil
}

// hasEncodedSeparator 判断 ParseQuery 的结果是否疑似"整串被 URL-encode 过"。
// 正常 query string 的 key 里不会出现 = 或 &，若出现则说明原始分隔符仍是转义态，
// 这次解析得到的 key 其实是未分隔的整串，需要走兜底路径。
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
