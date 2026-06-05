package v2

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/json"
)

const testAppID = "wxtestappid000000"

// signedResponse 用商户密钥（MD5）签名并编码为 v2 XML 响应。
func signedResponse(t *testing.T, params map[string]string) []byte {
	t.Helper()
	if params["return_code"] == "" {
		params["return_code"] = "SUCCESS"
	}
	if params["result_code"] == "" {
		params["result_code"] = "SUCCESS"
	}
	params["sign"] = sign(params, officialKey, SignTypeMD5)
	data, err := encodeXML(params)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return data
}

// newServerProvider 构造一个请求打到 httptest.Server 的 Provider。
func newServerProvider(t *testing.T, body []byte) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	p, err := NewProvider(t.Context(),
		WithAppID(testAppID),
		WithMerchant("10000100", officialKey),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()), // 注入后 client 与 refundClient 均可用，便于测退款
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func validOrder(tt paymgr.TradeType) *paymgr.UnifiedOrderRequest {
	return &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-1",
		TotalAmount: 100,
		Subject:     "测试商品",
		TradeType:   tt,
		NotifyURL:   "https://example.com/notify",
		ClientIP:    "127.0.0.1",
	}
}

func TestChannel(t *testing.T) {
	p := newServerProvider(t, nil)
	if p.Channel() != paymgr.ChannelWechatV2 {
		t.Fatalf("Channel = %q, want %q", p.Channel(), paymgr.ChannelWechatV2)
	}
}

func TestUnifiedOrderNative(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"prepay_id": "wx-prepay", "code_url": "weixin://wxpay/bizpayurl?pr=abc",
		"trade_type": "NATIVE",
	})
	p := newServerProvider(t, resp)

	got, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeNative))
	if err != nil {
		t.Fatalf("UnifiedOrder: %v", err)
	}
	if got.CodeURL != "weixin://wxpay/bizpayurl?pr=abc" {
		t.Fatalf("CodeURL = %q", got.CodeURL)
	}
}

func TestUnifiedOrderH5(t *testing.T) {
	// 捕获请求，验证 MWEB 必传的 scene_info / trade_type 已发出
	var gotReq map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotReq, _ = decodeXML(data)
		_, _ = w.Write(signedResponse(t, map[string]string{
			"appid": testAppID, "mch_id": "10000100",
			"prepay_id": "wx-prepay", "mweb_url": "https://wx.tenpay.com/cgi-bin/mmpayweb",
			"trade_type": "MWEB",
		}))
	}))
	t.Cleanup(srv.Close)

	p, err := NewProvider(t.Context(),
		WithAppID(testAppID), WithMerchant("10000100", officialKey),
		WithBaseURL(srv.URL), WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	got, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeH5))
	if err != nil {
		t.Fatalf("UnifiedOrder: %v", err)
	}
	if got.H5URL != "https://wx.tenpay.com/cgi-bin/mmpayweb" {
		t.Fatalf("H5URL = %q", got.H5URL)
	}
	if gotReq["trade_type"] != "MWEB" {
		t.Errorf("trade_type = %q, want MWEB", gotReq["trade_type"])
	}
	if gotReq["scene_info"] == "" {
		t.Error("scene_info missing in MWEB request")
	}
}

func TestUnifiedOrderAppSecondSign(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"prepay_id": "wx-prepay-app", "trade_type": "APP",
	})
	p := newServerProvider(t, resp)

	got, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeApp))
	if err != nil {
		t.Fatalf("UnifiedOrder: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(got.AppParams), &m); err != nil {
		t.Fatalf("unmarshal AppParams: %v", err)
	}
	if m["package"] != "Sign=WXPay" {
		t.Errorf("package = %q, want Sign=WXPay", m["package"])
	}
	if m["partnerid"] != "10000100" {
		t.Errorf("partnerid = %q, want 10000100", m["partnerid"])
	}
	if m["prepayid"] != "wx-prepay-app" {
		t.Errorf("prepayid = %q", m["prepayid"])
	}
	// 二次签名自洽：sign() 跳过 sign 字段，重算应与携带值一致
	if want := sign(m, officialKey, SignTypeMD5); m["sign"] != want {
		t.Errorf("app sign = %q, want %q", m["sign"], want)
	}
}

func TestUnifiedOrderJSAPISecondSign(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"prepay_id": "wx-prepay-jsapi", "trade_type": "JSAPI",
	})
	p := newServerProvider(t, resp)

	req := validOrder(paymgr.TradeTypeJSAPI)
	req.OpenID = "openid-123"
	got, err := p.UnifiedOrder(t.Context(), req)
	if err != nil {
		t.Fatalf("UnifiedOrder: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(got.JSAPIParams), &m); err != nil {
		t.Fatalf("unmarshal JSAPIParams: %v", err)
	}
	if m["package"] != "prepay_id=wx-prepay-jsapi" {
		t.Errorf("package = %q", m["package"])
	}
	if m["signType"] != "MD5" {
		t.Errorf("signType = %q, want MD5", m["signType"])
	}
	// 验证 paySign：重算前移除 paySign 字段
	paySign := m["paySign"]
	delete(m, "paySign")
	if want := sign(m, officialKey, SignTypeMD5); paySign != want {
		t.Errorf("paySign = %q, want %q", paySign, want)
	}
}

func TestUnifiedOrderMissingClientIP(t *testing.T) {
	p := newServerProvider(t, nil)
	req := validOrder(paymgr.TradeTypeNative)
	req.ClientIP = ""
	if _, err := p.UnifiedOrder(t.Context(), req); !errors.Is(err, paymgr.ErrInvalidParam) {
		t.Fatalf("error = %v, want ErrInvalidParam", err)
	}
}

func TestUnifiedOrderMissingOpenID(t *testing.T) {
	p := newServerProvider(t, nil)
	if _, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeJSAPI)); !errors.Is(err, paymgr.ErrInvalidParam) {
		t.Fatalf("error = %v, want ErrInvalidParam", err)
	}
}

func TestUnifiedOrderUpstreamFail(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"result_code": "FAIL", "err_code": "ORDERPAID", "err_code_des": "订单已支付",
	})
	p := newServerProvider(t, resp)

	_, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeNative))
	var chErr *paymgr.ChannelError
	if !errors.As(err, &chErr) || chErr.Code != "ORDERPAID" {
		t.Fatalf("error = %v, want ChannelError ORDERPAID", err)
	}
}

func TestUnifiedOrderSignMismatch(t *testing.T) {
	// 构造签名错误的响应
	params := map[string]string{
		"appid": testAppID, "mch_id": "10000100", "return_code": "SUCCESS",
		"result_code": "SUCCESS", "prepay_id": "x", "sign": "DEADBEEF",
	}
	data, _ := encodeXML(params)
	p := newServerProvider(t, data)

	if _, err := p.UnifiedOrder(t.Context(), validOrder(paymgr.TradeTypeNative)); !errors.Is(err, paymgr.ErrInvalidSign) {
		t.Fatalf("error = %v, want ErrInvalidSign", err)
	}
}

func TestQueryOrder(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"out_trade_no": "ORD-1", "transaction_id": "42000", "trade_state": "SUCCESS",
		"total_fee": "100", "time_end": "20180608103454", "openid": "op-1",
	})
	p := newServerProvider(t, resp)

	got, err := p.QueryOrder(t.Context(), &paymgr.QueryOrderRequest{OutTradeNo: "ORD-1"})
	if err != nil {
		t.Fatalf("QueryOrder: %v", err)
	}
	if got.TradeStatus != paymgr.TradeStatusPaid {
		t.Errorf("TradeStatus = %q, want paid", got.TradeStatus)
	}
	if got.TransactionID != "42000" || got.TotalAmount != 100 || got.BuyerID != "op-1" {
		t.Errorf("response = %+v", got)
	}
}

func TestCloseOrder(t *testing.T) {
	resp := signedResponse(t, map[string]string{"appid": testAppID, "mch_id": "10000100"})
	p := newServerProvider(t, resp)
	if err := p.CloseOrder(t.Context(), &paymgr.CloseOrderRequest{OutTradeNo: "ORD-1"}); err != nil {
		t.Fatalf("CloseOrder: %v", err)
	}
}

func TestRefundSuccess(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"out_refund_no": "R-1", "refund_id": "50000", "refund_fee": "100",
	})
	p := newServerProvider(t, resp)

	got, err := p.Refund(t.Context(), &paymgr.RefundRequest{
		OutTradeNo:   "ORD-1",
		OutRefundNo:  "R-1",
		RefundAmount: 100,
		TotalAmount:  100,
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if got.RefundID != "50000" || got.RefundAmount != 100 {
		t.Errorf("response = %+v", got)
	}
}

func TestRefundMissingCert(t *testing.T) {
	// 不注入 client、不配证书 → refundClient 为 nil
	p, err := NewProvider(t.Context(),
		WithAppID(testAppID),
		WithMerchant("10000100", officialKey),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	_, err = p.Refund(t.Context(), &paymgr.RefundRequest{
		OutTradeNo: "ORD-1", OutRefundNo: "R-1", RefundAmount: 100, TotalAmount: 100,
	})
	if err == nil {
		t.Fatal("Refund without cert error = nil, want error")
	}
}

func TestRefundAmountExceedsTotal(t *testing.T) {
	p := newServerProvider(t, nil)
	_, err := p.Refund(t.Context(), &paymgr.RefundRequest{
		OutTradeNo: "ORD-1", OutRefundNo: "R-1", RefundAmount: 200, TotalAmount: 100,
	})
	if !errors.Is(err, paymgr.ErrInvalidParam) {
		t.Fatalf("error = %v, want ErrInvalidParam", err)
	}
}

func TestQueryRefund(t *testing.T) {
	resp := signedResponse(t, map[string]string{
		"appid": testAppID, "mch_id": "10000100",
		"out_trade_no": "ORD-1", "transaction_id": "42000",
		"refund_count":    "1",
		"out_refund_no_0": "R-1", "refund_id_0": "50000",
		"refund_status_0": "PROCESSING", "refund_fee_0": "100",
		"total_fee": "100",
	})
	p := newServerProvider(t, resp)

	got, err := p.QueryRefund(t.Context(), &paymgr.QueryRefundRequest{OutRefundNo: "R-1"})
	if err != nil {
		t.Fatalf("QueryRefund: %v", err)
	}
	if got.RefundStatus != paymgr.RefundStatusProcessing {
		t.Errorf("RefundStatus = %q, want processing", got.RefundStatus)
	}
	if got.RefundID != "50000" || got.RefundAmount != 100 {
		t.Errorf("response = %+v", got)
	}
}

func TestParseNotify(t *testing.T) {
	params := map[string]string{
		"appid": testAppID, "mch_id": "10000100", "return_code": "SUCCESS",
		"result_code": "SUCCESS", "out_trade_no": "ORD-1", "transaction_id": "42000",
		"total_fee": "100", "time_end": "20180608103454", "openid": "op-1",
	}
	params["sign"] = sign(params, officialKey, SignTypeMD5)
	data, _ := encodeXML(params)
	p := newServerProvider(t, nil)

	r := httptest.NewRequest(http.MethodPost, "/notify", bytes.NewReader(data))
	got, err := p.ParseNotify(t.Context(), r)
	if err != nil {
		t.Fatalf("ParseNotify: %v", err)
	}
	if got.TradeStatus != paymgr.TradeStatusPaid || got.OutTradeNo != "ORD-1" {
		t.Fatalf("result = %+v", got)
	}
}

func TestParseNotifyFallbackMD5UnderHMACConfig(t *testing.T) {
	// Provider 配置 HMAC-SHA256，但通知用 MD5 签名且不带 sign_type，
	// 应回退到 MD5 验签并通过（贴合微信支付结果通知的历史行为）。
	p, err := NewProvider(t.Context(),
		WithAppID(testAppID), WithMerchant("10000100", officialKey),
		WithSignType(SignTypeHMACSHA256),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	params := map[string]string{
		"appid": testAppID, "return_code": "SUCCESS", "result_code": "SUCCESS",
		"out_trade_no": "ORD-1", "transaction_id": "42000", "total_fee": "100",
	}
	params["sign"] = sign(params, officialKey, SignTypeMD5) // 通知用 MD5 签
	data, _ := encodeXML(params)

	r := httptest.NewRequest(http.MethodPost, "/notify", bytes.NewReader(data))
	got, err := p.ParseNotify(t.Context(), r)
	if err != nil {
		t.Fatalf("ParseNotify should accept MD5-signed notify under HMAC config: %v", err)
	}
	if got.TradeStatus != paymgr.TradeStatusPaid {
		t.Fatalf("TradeStatus = %q, want paid", got.TradeStatus)
	}
}

func TestParseNotifySignMismatch(t *testing.T) {
	params := map[string]string{
		"appid": testAppID, "return_code": "SUCCESS", "result_code": "SUCCESS",
		"out_trade_no": "ORD-1", "sign": "DEADBEEF",
	}
	data, _ := encodeXML(params)
	p := newServerProvider(t, nil)

	r := httptest.NewRequest(http.MethodPost, "/notify", bytes.NewReader(data))
	if _, err := p.ParseNotify(t.Context(), r); !errors.Is(err, paymgr.ErrInvalidSign) {
		t.Fatalf("error = %v, want ErrInvalidSign", err)
	}
}

func TestParseRefundNotify(t *testing.T) {
	innerXML, _ := encodeXML(map[string]string{
		"out_trade_no": "ORD-1", "transaction_id": "42000",
		"out_refund_no": "R-1", "refund_id": "50000",
		"refund_status": "SUCCESS", "refund_fee": "100", "total_fee": "100",
		"success_time": "2018-06-08 10:34:54", "refund_recv_accout": "招商银行信用卡0403",
	})
	reqInfo := encryptECBForTest(t, innerXML, officialKey)
	outer, _ := encodeXML(map[string]string{
		"return_code": "SUCCESS", "appid": testAppID, "mch_id": "10000100", "req_info": reqInfo,
	})
	p := newServerProvider(t, nil)

	r := httptest.NewRequest(http.MethodPost, "/refund-notify", bytes.NewReader(outer))
	got, err := p.ParseRefundNotify(t.Context(), r)
	if err != nil {
		t.Fatalf("ParseRefundNotify: %v", err)
	}
	if got.RefundStatus != paymgr.RefundStatusSuccess {
		t.Errorf("RefundStatus = %q, want success", got.RefundStatus)
	}
	if got.OutRefundNo != "R-1" || got.RefundID != "50000" || got.RefundAmount != 100 {
		t.Errorf("result = %+v", got)
	}
	if got.RefundedAt.IsZero() {
		t.Error("RefundedAt is zero, want parsed time")
	}
}

func TestParseRefundNotifyDecryptFail(t *testing.T) {
	outer, _ := encodeXML(map[string]string{
		"return_code": "SUCCESS", "appid": testAppID, "req_info": "bm90LXZhbGlkLWNpcGhlcg==",
	})
	p := newServerProvider(t, nil)

	r := httptest.NewRequest(http.MethodPost, "/refund-notify", bytes.NewReader(outer))
	if _, err := p.ParseRefundNotify(t.Context(), r); !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("error = %v, want ErrInvalidNotify", err)
	}
}

func TestACKNotify(t *testing.T) {
	p := newServerProvider(t, nil)
	rec := httptest.NewRecorder()
	p.ACKNotify(rec)

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("SUCCESS")) {
		t.Fatalf("ACK body = %q, want contains SUCCESS", body)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
