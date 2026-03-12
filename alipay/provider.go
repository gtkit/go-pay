package alipay

import (
	"context"
	"fmt"
	"net/http"

	"strconv"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/smartwalle/alipay/v3"
)

// Config 支付宝配置.
type Config struct {
	AppID        string // 支付宝应用 ID
	PrivateKey   string // 应用私钥（PKCS1 格式，去除头尾和换行）
	IsProduction bool   // true=生产环境, false=沙箱环境

	// 证书模式（推荐）—— 以下三项全部填写则启用证书模式
	AppCertPublicKeyPath    string // 应用公钥证书路径
	AlipayRootCertPath      string // 支付宝根证书路径
	AlipayCertPublicKeyPath string // 支付宝公钥证书路径

	// 普通公钥模式 —— 仅当证书路径全部为空时使用
	AlipayPublicKey string // 支付宝公钥
}

// Validate 校验配置完整性.
func (c *Config) Validate() error {
	if c.AppID == "" {
		return fmt.Errorf("alipay: app_id is required")
	}
	if c.PrivateKey == "" {
		return fmt.Errorf("alipay: private_key is required")
	}
	// 证书模式三项必须全部提供或全部为空
	hasCert := c.AppCertPublicKeyPath != "" || c.AlipayRootCertPath != "" || c.AlipayCertPublicKeyPath != ""
	allCert := c.AppCertPublicKeyPath != "" && c.AlipayRootCertPath != "" && c.AlipayCertPublicKeyPath != ""
	if hasCert && !allCert {
		return fmt.Errorf("alipay: cert mode requires all three cert paths (app_cert, root_cert, alipay_cert)")
	}
	if !hasCert && c.AlipayPublicKey == "" {
		return fmt.Errorf("alipay: either cert paths or alipay_public_key is required")
	}
	return nil
}

// UseCertMode 是否使用证书模式.
func (c *Config) UseCertMode() bool {
	return c.AppCertPublicKeyPath != "" && c.AlipayRootCertPath != "" && c.AlipayCertPublicKeyPath != ""
}

// Provider 支付宝支付提供者.
type Provider struct {
	cfg    *Config
	client *alipay.Client
}

// NewProvider 创建支付宝支付提供者.
func NewProvider(cfg *Config) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	client, err := alipay.New(cfg.AppID, cfg.PrivateKey, cfg.IsProduction)
	if err != nil {
		return nil, fmt.Errorf("alipay: init client: %w", err)
	}

	// 根据配置选择签名验签方式
	if cfg.UseCertMode() {
		// 证书模式（推荐，安全性更高，支持证书自动续期）
		if err := client.LoadAppCertPublicKeyFromFile(cfg.AppCertPublicKeyPath); err != nil {
			return nil, fmt.Errorf("alipay: load app cert: %w", err)
		}
		if err := client.LoadAliPayRootCertFromFile(cfg.AlipayRootCertPath); err != nil {
			return nil, fmt.Errorf("alipay: load root cert: %w", err)
		}
		if err := client.LoadAlipayCertPublicKeyFromFile(cfg.AlipayCertPublicKeyPath); err != nil {
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
	var passbackParams string
	if len(req.Metadata) > 0 {
		// 支付宝 passback_params 为 URL encode 字符串，此处简化为 key=value
		for k, v := range req.Metadata {
			if passbackParams != "" {
				passbackParams += "&"
			}
			passbackParams += k + "=" + v
		}
	}

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
		metadata := make(map[string]string)
		// 简单解析 key=value&key2=value2 格式
		for _, pair := range splitParams(noti.PassbackParams) {
			k, v := splitKV(pair)
			if k != "" {
				metadata[k] = v
			}
		}
		if len(metadata) > 0 {
			result.Metadata = metadata
		}
	}

	return result, nil
}

// ACKNotify 回写成功响应
//
// 支付宝需返回纯文本 "success".
func (p *Provider) ACKNotify(w http.ResponseWriter) {
	alipay.ACKNotification(w)
}

// --- 内部辅助函数 ---

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
	yuan := float64(cent) / 100.0
	return strconv.FormatFloat(yuan, 'f', 2, 64)
}

// yuanToCent 元转分.
func yuanToCent(yuan string) int64 {
	f, err := strconv.ParseFloat(yuan, 64)
	if err != nil {
		return 0
	}
	// 乘 100 后四舍五入，避免浮点精度问题
	return int64(f*100 + 0.5)
}

// splitParams 按 & 分割参数.
func splitParams(s string) []string {
	var result []string
	start := 0
	for i := range len(s) {
		if s[i] == '&' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// splitKV 按第一个 = 分割键值对.
func splitKV(s string) (string, string) {
	for i := range len(s) {
		if s[i] == '=' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
