// Package v2 实现微信支付 v2（XML 协议）的统一支付接口。
//
// 它以 paymgr.ChannelWechatV2 渠道实现 paymgr.Provider，覆盖统一下单、
// 订单查询、关单、退款、退款查询，以及 APP / JSAPI 二次签名与支付、
// 退款异步通知的验签解密。主要用于兼容仍只能走 v2 的老商户号。
//
// 密钥说明：本包使用的 APIKey 是商户平台的 v2 API 密钥（32 位），
// 与 v3 的 APIv3 密钥（wechat.Config.MchAPIv3Key）不是同一个密钥。
package v2

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/json"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// 编译期断言：Provider 实现 paymgr.Provider 接口。
var _ paymgr.Provider = (*Provider)(nil)

// Channel 返回渠道标识 paymgr.ChannelWechatV2。
func (p *Provider) Channel() paymgr.Channel {
	return paymgr.ChannelWechatV2
}

// UnifiedOrder 统一下单。
//
// 支持 APP / JSAPI / Native / H5(MWEB) 四种交易类型。APP 与 JSAPI 会在下单
// 成功后生成调起支付的二次签名，分别写入响应的 AppParams 与 JSAPIParams；
// Native 返回 CodeURL，H5 返回 H5URL。v2 统一下单要求终端 IP，故 ClientIP 必填。
func (p *Provider) UnifiedOrder(ctx context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.ClientIP == "" {
		return nil, fmt.Errorf("%w: client_ip is required for wechat v2 unified order", paymgr.ErrInvalidParam)
	}

	tradeType, err := mapTradeType(req.TradeType)
	if err != nil {
		return nil, err
	}

	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = p.cfg.NotifyURL
	}

	params := map[string]string{
		"body":             req.Subject,
		"out_trade_no":     req.OutTradeNo,
		"total_fee":        strconv.FormatInt(req.TotalAmount, 10),
		"spbill_create_ip": req.ClientIP,
		"notify_url":       notifyURL,
		"trade_type":       tradeType,
	}
	if !req.ExpireAt.IsZero() {
		params["time_expire"] = formatV2Time(req.ExpireAt)
	}
	if len(req.Metadata) > 0 {
		attach, err := jsonString(req.Metadata)
		if err != nil {
			return nil, fmt.Errorf("wechat/v2: marshal metadata: %w", err)
		}
		params["attach"] = attach
	}

	switch req.TradeType {
	case paymgr.TradeTypeJSAPI:
		if req.OpenID == "" {
			return nil, fmt.Errorf("%w: openid is required for wechat v2 jsapi", paymgr.ErrInvalidParam)
		}
		params["openid"] = req.OpenID
	case paymgr.TradeTypeNative:
		params["product_id"] = req.OutTradeNo
	case paymgr.TradeTypeH5:
		// MWEB 统一下单要求 scene_info，缺失时微信网关会拒绝。
		sceneInfo, err := jsonString(map[string]any{
			"h5_info": map[string]string{"type": "Wap"},
		})
		if err != nil {
			return nil, fmt.Errorf("wechat/v2: marshal scene_info: %w", err)
		}
		params["scene_info"] = sceneInfo
	}

	result, err := p.doRequest(ctx, p.client, "/pay/unifiedorder", params)
	if err != nil {
		return nil, err
	}

	prepayID := result["prepay_id"]
	resp := &paymgr.UnifiedOrderResponse{
		Channel:  paymgr.ChannelWechatV2,
		PrepayID: prepayID,
		ExpireAt: req.ExpireAt,
	}

	switch req.TradeType {
	case paymgr.TradeTypeNative:
		resp.CodeURL = result["code_url"]
	case paymgr.TradeTypeH5:
		resp.H5URL = result["mweb_url"]
	case paymgr.TradeTypeApp:
		resp.AppParams, err = p.buildAppParams(prepayID)
		if err != nil {
			return nil, err
		}
	case paymgr.TradeTypeJSAPI:
		resp.JSAPIParams, err = p.buildJSAPIParams(prepayID)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// QueryOrder 查询订单。
func (p *Provider) QueryOrder(ctx context.Context, req *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	params := map[string]string{}
	if req.TransactionID != "" {
		params["transaction_id"] = req.TransactionID
	} else {
		params["out_trade_no"] = req.OutTradeNo
	}

	result, err := p.doRequest(ctx, p.client, "/pay/orderquery", params)
	if err != nil {
		return nil, err
	}

	return &paymgr.QueryOrderResponse{
		Channel:       paymgr.ChannelWechatV2,
		OutTradeNo:    result["out_trade_no"],
		TransactionID: result["transaction_id"],
		TradeStatus:   mapTradeState(result["trade_state"]),
		TotalAmount:   parseInt64(result["total_fee"]),
		PaidAt:        parseV2Time(result["time_end"]),
		BuyerID:       result["openid"],
	}, nil
}

// CloseOrder 关闭订单。
func (p *Provider) CloseOrder(ctx context.Context, req *paymgr.CloseOrderRequest) error {
	if req == nil || req.OutTradeNo == "" {
		return fmt.Errorf("%w: out_trade_no is required", paymgr.ErrInvalidParam)
	}

	_, err := p.doRequest(ctx, p.client, "/pay/closeorder", map[string]string{
		"out_trade_no": req.OutTradeNo,
	})
	return err
}

// Refund 申请退款。
//
// 退款接口走商户证书双向 TLS，未配置商户证书时返回错误。
func (p *Provider) Refund(ctx context.Context, req *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if p.refundClient == nil {
		return nil, fmt.Errorf("wechat/v2: merchant certificate (apiclient_cert/apiclient_key) is required for refund")
	}

	params := map[string]string{
		"out_refund_no": req.OutRefundNo,
		"total_fee":     strconv.FormatInt(req.TotalAmount, 10),
		"refund_fee":    strconv.FormatInt(req.RefundAmount, 10),
	}
	if req.TransactionID != "" {
		params["transaction_id"] = req.TransactionID
	} else {
		params["out_trade_no"] = req.OutTradeNo
	}
	if req.NotifyURL != "" {
		params["notify_url"] = req.NotifyURL
	}

	result, err := p.doRequest(ctx, p.refundClient, "/secapi/pay/refund", params)
	if err != nil {
		return nil, err
	}

	return &paymgr.RefundResponse{
		Channel:      paymgr.ChannelWechatV2,
		OutRefundNo:  result["out_refund_no"],
		RefundID:     result["refund_id"],
		RefundAmount: parseInt64(result["refund_fee"]),
	}, nil
}

// QueryRefund 查询退款。
//
// v2 退款查询响应中退款明细字段带下标（如 refund_status_0），一笔订单可能
// 对应多笔退款，按 out_refund_no 匹配下标取对应明细。
func (p *Provider) QueryRefund(ctx context.Context, req *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	result, err := p.doRequest(ctx, p.client, "/pay/refundquery", map[string]string{
		"out_refund_no": req.OutRefundNo,
	})
	if err != nil {
		return nil, err
	}

	idx := findRefundIndex(result, req.OutRefundNo)
	return &paymgr.QueryRefundResponse{
		Channel:       paymgr.ChannelWechatV2,
		OutTradeNo:    result["out_trade_no"],
		TransactionID: result["transaction_id"],
		OutRefundNo:   req.OutRefundNo,
		RefundID:      result["refund_id_"+idx],
		RefundStatus:  mapRefundStatus(result["refund_status_"+idx]),
		RefundAmount:  parseInt64(result["refund_fee_"+idx]),
		TotalAmount:   parseInt64(result["total_fee"]),
	}, nil
}

// ParseNotify 解析支付异步通知。
//
// 读取 v2 XML 通知、按 sign_type 验签后映射为 paymgr.NotifyResult。
// result_code 为 SUCCESS 时交易状态为已支付，否则为异常。
func (p *Provider) ParseNotify(_ context.Context, r *http.Request) (*paymgr.NotifyResult, error) {
	m, err := readNotify(r)
	if err != nil {
		return nil, err
	}
	if m["return_code"] != "SUCCESS" {
		return nil, fmt.Errorf("%w: return_code=%s", paymgr.ErrInvalidNotify, m["return_code"])
	}
	if !verifySign(m, p.cfg.APIKey, notifySignType(m)) {
		return nil, fmt.Errorf("%w: wechat v2 notify sign mismatch", paymgr.ErrInvalidSign)
	}

	status := paymgr.TradeStatusError
	if m["result_code"] == "SUCCESS" {
		status = paymgr.TradeStatusPaid
	}

	result := &paymgr.NotifyResult{
		Channel:       paymgr.ChannelWechatV2,
		OutTradeNo:    m["out_trade_no"],
		TransactionID: m["transaction_id"],
		TradeStatus:   status,
		TotalAmount:   parseInt64(m["total_fee"]),
		PaidAt:        parseV2Time(m["time_end"]),
		BuyerID:       m["openid"],
	}
	if attach := m["attach"]; attach != "" {
		metadata := make(map[string]string)
		if err := json.Unmarshal([]byte(attach), &metadata); err == nil {
			result.Metadata = metadata
		}
	}
	return result, nil
}

// ParseRefundNotify 解析退款异步通知。
//
// 退款通知不携带签名，其真实性由 req_info 的 AES-256-ECB 加密保证：
// 先校验外层 return_code，再用 MD5(APIKey) 作密钥解密 req_info，
// 解密后的 XML 映射为 paymgr.RefundNotifyResult。
func (p *Provider) ParseRefundNotify(_ context.Context, r *http.Request) (*paymgr.RefundNotifyResult, error) {
	m, err := readNotify(r)
	if err != nil {
		return nil, err
	}
	if m["return_code"] != "SUCCESS" {
		return nil, fmt.Errorf("%w: return_code=%s", paymgr.ErrInvalidNotify, m["return_code"])
	}
	reqInfo := m["req_info"]
	if reqInfo == "" {
		return nil, fmt.Errorf("%w: missing req_info in refund notify", paymgr.ErrInvalidNotify)
	}

	plain, err := decryptRefundReqInfo(reqInfo, p.cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt req_info: %v", paymgr.ErrInvalidNotify, err)
	}
	info, err := decodeXML(plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidNotify, err)
	}

	return &paymgr.RefundNotifyResult{
		Channel:             paymgr.ChannelWechatV2,
		OutTradeNo:          info["out_trade_no"],
		TransactionID:       info["transaction_id"],
		OutRefundNo:         info["out_refund_no"],
		RefundID:            info["refund_id"],
		RefundStatus:        mapRefundStatus(info["refund_status"]),
		RefundAmount:        parseInt64(info["refund_fee"]),
		TotalAmount:         parseInt64(info["total_fee"]),
		RefundedAt:          parseV2Time(info["success_time"]),
		UserReceivedAccount: info["refund_recv_accout"],
	}, nil
}

// ACKNotify 向微信回写成功应答，避免通知重发。
func (p *Provider) ACKNotify(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<xml><return_code><![CDATA[SUCCESS]]></return_code><return_msg><![CDATA[OK]]></return_msg></xml>`))
}

// --- 二次签名 ---

// buildAppParams 生成 APP 调起支付的二次签名参数（JSON 字符串）。
//
// 字段 appid/partnerid/prepayid/package(=Sign=WXPay)/noncestr/timestamp
// 按 v2 签名规则（字典序拼接 + &key=APIKey + SignType）签名，结果置于 sign。
func (p *Provider) buildAppParams(prepayID string) (string, error) {
	nonce, err := utils.GenerateNonce()
	if err != nil {
		return "", fmt.Errorf("wechat/v2: generate nonce: %w", err)
	}
	params := map[string]string{
		"appid":     p.cfg.AppID,
		"partnerid": p.cfg.MchID,
		"prepayid":  prepayID,
		"package":   "Sign=WXPay",
		"noncestr":  nonce,
		"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
	}
	params["sign"] = sign(params, p.cfg.APIKey, p.signType())
	return jsonString(params)
}

// buildJSAPIParams 生成 JSAPI/小程序调起支付的二次签名参数（JSON 字符串）。
//
// 字段 appId/timeStamp/nonceStr/package(=prepay_id=xxx)/signType 按 v2 签名
// 规则签名，结果置于 paySign，signType 与下单签名算法一致。
func (p *Provider) buildJSAPIParams(prepayID string) (string, error) {
	nonce, err := utils.GenerateNonce()
	if err != nil {
		return "", fmt.Errorf("wechat/v2: generate nonce: %w", err)
	}
	params := map[string]string{
		"appId":     p.cfg.AppID,
		"timeStamp": strconv.FormatInt(time.Now().Unix(), 10),
		"nonceStr":  nonce,
		"package":   "prepay_id=" + prepayID,
		"signType":  string(p.signType()),
	}
	params["paySign"] = sign(params, p.cfg.APIKey, p.signType())
	return jsonString(params)
}

// --- 内部辅助 ---

// jsonString 将值序列化为 JSON 字符串。
func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readNotify 读取并解析异步通知请求体为参数表。
func readNotify(r *http.Request) (map[string]string, error) {
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", paymgr.ErrInvalidNotify, err)
	}
	m, err := decodeXML(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paymgr.ErrInvalidNotify, err)
	}
	return m, nil
}

// notifySignType 取支付通知声明的签名算法。
//
// 微信 v2 支付结果通知历史上固定用 MD5 签名（即便下单用了 HMAC-SHA256），
// 且常不回传 sign_type 字段；因此报文未声明 sign_type 时回退到 MD5，
// 而非 Provider 配置的算法，以贴合微信网关的实际行为。
func notifySignType(m map[string]string) SignType {
	if v := m["sign_type"]; v != "" {
		return SignType(v)
	}
	return SignTypeMD5
}

// findRefundIndex 在退款查询响应中按 out_refund_no 定位明细下标，未命中返回 "0"。
func findRefundIndex(m map[string]string, outRefundNo string) string {
	count, _ := strconv.Atoi(m["refund_count"])
	for i := range count {
		s := strconv.Itoa(i)
		if m["out_refund_no_"+s] == outRefundNo {
			return s
		}
	}
	return "0"
}

// parseInt64 将字符串解析为 int64，非法时返回 0。
func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// mapTradeType 将统一交易类型映射为 v2 的 trade_type。
func mapTradeType(t paymgr.TradeType) (string, error) {
	switch t {
	case paymgr.TradeTypeApp:
		return "APP", nil
	case paymgr.TradeTypeJSAPI:
		return "JSAPI", nil
	case paymgr.TradeTypeNative:
		return "NATIVE", nil
	case paymgr.TradeTypeH5:
		return "MWEB", nil
	default:
		return "", fmt.Errorf("%w: wechat v2 supports app, jsapi, native and h5, got %s",
			paymgr.ErrUnsupportedType, t)
	}
}

// mapTradeState 将 v2 trade_state 映射为统一交易状态。
func mapTradeState(state string) paymgr.TradeStatus {
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

// mapRefundStatus 将 v2 退款状态映射为统一退款状态。
//
// 覆盖退款查询的 refund_status_$n 与退款通知的 refund_status，取值相同：
// SUCCESS / PROCESSING / REFUNDCLOSE / CHANGE。
func mapRefundStatus(state string) paymgr.RefundStatus {
	switch state {
	case "SUCCESS":
		return paymgr.RefundStatusSuccess
	case "PROCESSING":
		return paymgr.RefundStatusProcessing
	case "REFUNDCLOSE":
		return paymgr.RefundStatusClosed
	case "CHANGE":
		return paymgr.RefundStatusAbnormal
	default:
		return paymgr.RefundStatusError
	}
}
