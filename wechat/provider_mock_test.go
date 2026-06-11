package wechat

// 本文件通过 httptest 模拟微信支付 APIv3 服务端，跑通真实的请求签名 / 应答验签 /
// 回调验签解密链路：
//   - 应答侧：测试服务器用"平台"私钥按 SDK 规则（timestamp\nnonce\nbody\n）签名
//     Wechatpay-Signature 等应答头，core.Client 用对应公钥完成验签；
//   - 回调侧：业务 JSON 用 AEAD_AES_256_GCM（APIv3 Key）加密为 resource，
//     请求头用平台私钥签名，notify.Handler 完成验签与解密。
// 所有密钥均为测试运行时生成，不含任何真实凭据。

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/json"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
)

// testPubKeyID 测试用微信支付公钥 ID（充当应答/回调验签的 serial）。
const testPubKeyID = "PUB_KEY_ID_TEST00000000000000000000000"

// 共享测试密钥：商户私钥 + 模拟微信"平台"私钥，整个包的测试只生成一次。
var (
	testKeysOnce      sync.Once
	testKeysErr       error
	sharedMerchantKey *rsa.PrivateKey
	sharedPlatformKey *rsa.PrivateKey
)

func sharedTestKeys(t *testing.T) (merchant, platform *rsa.PrivateKey) {
	t.Helper()
	testKeysOnce.Do(func() {
		sharedMerchantKey, testKeysErr = rsa.GenerateKey(rand.Reader, 2048)
		if testKeysErr != nil {
			return
		}
		sharedPlatformKey, testKeysErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testKeysErr != nil {
		t.Fatalf("generate shared test keys: %v", testKeysErr)
	}
	return sharedMerchantKey, sharedPlatformKey
}

// rewriteTransport 把 SDK 写死的 https://api.mch.weixin.qq.com 请求改写到测试服务器。
type rewriteTransport struct {
	target *url.URL
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// mockWechatEnv 持有完整可用的 Provider 与模拟微信服务端。
type mockWechatEnv struct {
	t           *testing.T
	provider    *Provider
	merchantKey *rsa.PrivateKey
	platformKey *rsa.PrivateKey
	mux         *http.ServeMux
}

func newMockWechatEnv(t *testing.T) *mockWechatEnv {
	t.Helper()
	merchantKey, platformKey := sharedTestKeys(t)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	cfg := baseConfig(merchantKey)
	cfg.WechatPayPublicKeyID = testPubKeyID
	cfg.WechatPayPublicKey = &platformKey.PublicKey

	client, err := core.NewClient(t.Context(),
		option.WithWechatPayPublicKeyAuthCipher(
			cfg.MchID, cfg.MchCertSerialNumber, merchantKey, testPubKeyID, &platformKey.PublicKey,
		),
		option.WithHTTPClient(&http.Client{Transport: rewriteTransport{target: target}}),
	)
	if err != nil {
		t.Fatalf("core.NewClient() error = %v", err)
	}

	handler, err := notify.NewRSANotifyHandler(
		cfg.MchAPIv3Key,
		verifiers.NewSHA256WithRSAPubkeyVerifier(testPubKeyID, platformKey.PublicKey),
	)
	if err != nil {
		t.Fatalf("notify.NewRSANotifyHandler() error = %v", err)
	}

	return &mockWechatEnv{
		t: t,
		provider: &Provider{
			cfg:           cfg,
			client:        client,
			privateKey:    merchantKey,
			notifyHandler: handler,
		},
		merchantKey: merchantKey,
		platformKey: platformKey,
		mux:         mux,
	}
}

// signPlatform 用测试平台私钥按微信规则签名 timestamp\nnonce\nbody\n。
func (e *mockWechatEnv) signPlatform(timestamp, nonce, body string) string {
	message := timestamp + "\n" + nonce + "\n" + body + "\n"
	hashed := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, e.platformKey, crypto.SHA256, hashed[:])
	if err != nil {
		e.t.Errorf("sign platform message: %v", err)
		return ""
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// writeSigned 写出带合法微信应答签名头的响应，供 SDK 验签通过。
func (e *mockWechatEnv) writeSigned(w http.ResponseWriter, status int, body string) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	const nonce = "mock-response-nonce"
	w.Header().Set("Wechatpay-Timestamp", timestamp)
	w.Header().Set("Wechatpay-Nonce", nonce)
	w.Header().Set("Wechatpay-Serial", testPubKeyID)
	w.Header().Set("Wechatpay-Signature", e.signPlatform(timestamp, nonce, body))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != "" {
		_, _ = w.Write([]byte(body))
	}
}

// handle 在指定路由上返回固定的已签名应答。
func (e *mockWechatEnv) handle(pattern string, status int, body string) {
	e.mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
		e.writeSigned(w, status, body)
	})
}

// notifyRequest 构造一条合法的微信回调请求：业务 JSON 用 AEAD_AES_256_GCM
// 加密进 resource，请求头用平台私钥签名。
func (e *mockWechatEnv) notifyRequest(eventType string, plaintext []byte) *http.Request {
	e.t.Helper()

	block, err := aes.NewCipher([]byte(e.provider.cfg.MchAPIv3Key))
	if err != nil {
		e.t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		e.t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	const (
		gcmNonce       = "0123456789ab" // GCM 标准 12 字节 nonce
		associatedData = "transaction"
	)
	ciphertext := aead.Seal(nil, []byte(gcmNonce), plaintext, []byte(associatedData))

	body := mustJSON(e.t, map[string]any{
		"id":            "EV-10086",
		"create_time":   time.Now().Format(time.RFC3339),
		"event_type":    eventType,
		"resource_type": "encrypt-resource",
		"summary":       "测试通知",
		"resource": map[string]string{
			"algorithm":       "AEAD_AES_256_GCM",
			"ciphertext":      base64.StdEncoding.EncodeToString(ciphertext),
			"associated_data": associatedData,
			"nonce":           gcmNonce,
			"original_type":   "transaction",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "https://merchant.example.com/pay/notify", bytes.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	const headerNonce = "mock-notify-nonce"
	req.Header.Set("Wechatpay-Timestamp", timestamp)
	req.Header.Set("Wechatpay-Nonce", headerNonce)
	req.Header.Set("Wechatpay-Serial", testPubKeyID)
	req.Header.Set("Wechatpay-Signature", e.signPlatform(timestamp, headerNonce, string(body)))
	return req
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

// verifyMerchantSign 用商户公钥校验 SHA256withRSA 签名。
func verifyMerchantSign(t *testing.T, pub *rsa.PublicKey, message, signB64 string) {
	t.Helper()
	sig, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		t.Fatalf("decode sign: %v", err)
	}
	hashed := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], sig); err != nil {
		t.Fatalf("VerifyPKCS1v15() error = %v", err)
	}
}

// --- UnifiedOrder ---

func TestUnifiedOrderAppSuccess(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/app", http.StatusOK, `{"prepay_id":"wx-prepay-app-1"}`)

	resp, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-APP-1",
		TotalAmount: 100,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypeApp,
		NotifyURL:   "https://example.com/notify",
		ExpireAt:    time.Now().Add(time.Hour),
		Metadata:    map[string]string{"order_source": "test"},
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.Channel != paymgr.ChannelWechat {
		t.Fatalf("Channel = %q, want %q", resp.Channel, paymgr.ChannelWechat)
	}
	if resp.PrepayID != "wx-prepay-app-1" {
		t.Fatalf("PrepayID = %q, want %q", resp.PrepayID, "wx-prepay-app-1")
	}

	var params map[string]string
	if err := json.Unmarshal([]byte(resp.AppParams), &params); err != nil {
		t.Fatalf("unmarshal AppParams: %v", err)
	}
	if params["appid"] != env.provider.cfg.AppID ||
		params["partnerid"] != env.provider.cfg.MchID ||
		params["prepayid"] != "wx-prepay-app-1" ||
		params["package"] != "Sign=WXPay" {
		t.Fatalf("unexpected app params: %+v", params)
	}
	message := params["appid"] + "\n" + params["timestamp"] + "\n" + params["noncestr"] + "\n" + params["prepayid"] + "\n"
	verifyMerchantSign(t, &env.merchantKey.PublicKey, message, params["sign"])
}

func TestUnifiedOrderAppMissingPrepayID(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/app", http.StatusOK, `{}`)

	resp, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-APP-2",
		TotalAmount: 100,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypeApp,
		NotifyURL:   "https://example.com/notify",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.PrepayID != "" {
		t.Fatalf("PrepayID = %q, want empty", resp.PrepayID)
	}
	// prepay_id 缺失时不得下发签了名但必然调起失败的空参数
	if resp.AppParams != "" {
		t.Fatalf("AppParams = %q, want empty when prepay_id missing", resp.AppParams)
	}
}

func TestUnifiedOrderJSAPISuccess(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/jsapi", http.StatusOK, `{"prepay_id":"wx-prepay-jsapi-1"}`)

	resp, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-JSAPI-1",
		TotalAmount: 200,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypeJSAPI,
		NotifyURL:   "https://example.com/notify",
		OpenID:      "openid-jsapi-1",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.PrepayID != "wx-prepay-jsapi-1" {
		t.Fatalf("PrepayID = %q, want %q", resp.PrepayID, "wx-prepay-jsapi-1")
	}

	var params map[string]string
	if err := json.Unmarshal([]byte(resp.JSAPIParams), &params); err != nil {
		t.Fatalf("unmarshal JSAPIParams: %v", err)
	}
	if params["package"] != "prepay_id=wx-prepay-jsapi-1" || params["signType"] != "RSA" {
		t.Fatalf("unexpected jsapi params: %+v", params)
	}
	message := params["appId"] + "\n" + params["timeStamp"] + "\n" + params["nonceStr"] + "\n" + params["package"] + "\n"
	verifyMerchantSign(t, &env.merchantKey.PublicKey, message, params["paySign"])
}

func TestUnifiedOrderNative(t *testing.T) {
	tests := []struct {
		name     string
		respBody string
		wantURL  string
	}{
		{name: "成功返回code_url", respBody: `{"code_url":"weixin://wxpay/bizpayurl?pr=abc123"}`, wantURL: "weixin://wxpay/bizpayurl?pr=abc123"},
		{name: "缺code_url不panic", respBody: `{}`, wantURL: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newMockWechatEnv(t)
			env.handle("POST /v3/pay/transactions/native", http.StatusOK, tt.respBody)

			resp, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
				OutTradeNo:  "ORD-NATIVE-1",
				TotalAmount: 300,
				Subject:     "测试商品",
				TradeType:   paymgr.TradeTypeNative,
				NotifyURL:   "https://example.com/notify",
			})
			if err != nil {
				t.Fatalf("UnifiedOrder() error = %v", err)
			}
			if resp.CodeURL != tt.wantURL {
				t.Fatalf("CodeURL = %q, want %q", resp.CodeURL, tt.wantURL)
			}
		})
	}
}

func TestUnifiedOrderH5Success(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/h5", http.StatusOK, `{"h5_url":"https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb?prepay_id=h5-1"}`)

	resp, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-H5-1",
		TotalAmount: 400,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypeH5,
		NotifyURL:   "https://example.com/notify",
		ClientIP:    "203.0.113.10",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if !strings.Contains(resp.H5URL, "prepay_id=h5-1") {
		t.Fatalf("H5URL = %q, want containing prepay_id=h5-1", resp.H5URL)
	}
}

func TestUnifiedOrderUnsupportedType(t *testing.T) {
	p := &Provider{}
	_, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 100,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypePage,
		NotifyURL:   "https://example.com/notify",
	})
	if !errors.Is(err, paymgr.ErrUnsupportedType) {
		t.Fatalf("UnifiedOrder() error = %v, want wrapped ErrUnsupportedType", err)
	}
}

func TestUnifiedOrderAPIError(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/app", http.StatusBadRequest,
		`{"code":"PARAM_ERROR","message":"参数错误"}`)

	_, err := env.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-ERR-1",
		TotalAmount: 100,
		Subject:     "测试商品",
		TradeType:   paymgr.TradeTypeApp,
		NotifyURL:   "https://example.com/notify",
	})
	chErr, ok := errors.AsType[*paymgr.ChannelError](err)
	if !ok {
		t.Fatalf("UnifiedOrder() error = %v, want *paymgr.ChannelError", err)
	}
	if chErr.Code != "PARAM_ERROR" {
		t.Fatalf("ChannelError.Code = %q, want %q", chErr.Code, "PARAM_ERROR")
	}
}

// --- QueryOrder / CloseOrder ---

func TestQueryOrderByTransactionID(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("GET /v3/pay/transactions/id/TX-Q-1", http.StatusOK, string(mustJSON(t, map[string]any{
		"mchid":          env.provider.cfg.MchID,
		"appid":          env.provider.cfg.AppID,
		"out_trade_no":   "ORD-Q-1",
		"transaction_id": "TX-Q-1",
		"trade_state":    "SUCCESS",
		"success_time":   "2024-05-20T13:29:35+08:00",
		"amount":         map[string]any{"total": 100},
		"payer":          map[string]any{"openid": "openid-q-1"},
	})))

	resp, err := env.provider.QueryOrder(t.Context(), &paymgr.QueryOrderRequest{TransactionID: "TX-Q-1"})
	if err != nil {
		t.Fatalf("QueryOrder() error = %v", err)
	}
	if resp.OutTradeNo != "ORD-Q-1" || resp.TransactionID != "TX-Q-1" {
		t.Fatalf("unexpected order ids: %+v", resp)
	}
	if resp.TradeStatus != paymgr.TradeStatusPaid {
		t.Fatalf("TradeStatus = %q, want %q", resp.TradeStatus, paymgr.TradeStatusPaid)
	}
	if resp.TotalAmount != 100 {
		t.Fatalf("TotalAmount = %d, want 100", resp.TotalAmount)
	}
	if resp.BuyerID != "openid-q-1" {
		t.Fatalf("BuyerID = %q, want %q", resp.BuyerID, "openid-q-1")
	}
	want, _ := time.Parse(time.RFC3339, "2024-05-20T13:29:35+08:00")
	if !resp.PaidAt.Equal(want) {
		t.Fatalf("PaidAt = %v, want %v", resp.PaidAt, want)
	}
}

func TestQueryOrderByOutTradeNo(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("GET /v3/pay/transactions/out-trade-no/ORD-Q-2", http.StatusOK, string(mustJSON(t, map[string]any{
		"mchid":          env.provider.cfg.MchID,
		"appid":          env.provider.cfg.AppID,
		"out_trade_no":   "ORD-Q-2",
		"transaction_id": "TX-Q-2",
		"trade_state":    "NOTPAY",
	})))

	resp, err := env.provider.QueryOrder(t.Context(), &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-2"})
	if err != nil {
		t.Fatalf("QueryOrder() error = %v", err)
	}
	if resp.TradeStatus != paymgr.TradeStatusPending {
		t.Fatalf("TradeStatus = %q, want %q", resp.TradeStatus, paymgr.TradeStatusPending)
	}
	if resp.TotalAmount != 0 {
		t.Fatalf("TotalAmount = %d, want 0", resp.TotalAmount)
	}
	if !resp.PaidAt.IsZero() {
		t.Fatalf("PaidAt = %v, want zero", resp.PaidAt)
	}
}

func TestCloseOrder(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/out-trade-no/ORD-C-1/close", http.StatusNoContent, "")

	if err := env.provider.CloseOrder(t.Context(), &paymgr.CloseOrderRequest{OutTradeNo: "ORD-C-1"}); err != nil {
		t.Fatalf("CloseOrder() error = %v", err)
	}
}

func TestCloseOrderAPIError(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/pay/transactions/out-trade-no/ORD-C-2/close", http.StatusNotFound,
		`{"code":"ORDER_NOT_EXIST","message":"订单不存在"}`)

	err := env.provider.CloseOrder(t.Context(), &paymgr.CloseOrderRequest{OutTradeNo: "ORD-C-2"})
	chErr, ok := errors.AsType[*paymgr.ChannelError](err)
	if !ok {
		t.Fatalf("CloseOrder() error = %v, want *paymgr.ChannelError", err)
	}
	if chErr.Code != "ORDER_NOT_EXIST" {
		t.Fatalf("ChannelError.Code = %q, want %q", chErr.Code, "ORDER_NOT_EXIST")
	}
}

// --- Refund / QueryRefund ---

func TestRefund(t *testing.T) {
	tests := []struct {
		name     string
		req      *paymgr.RefundRequest
		wantBody string // 请求体中必须出现的字段名
	}{
		{
			name: "按transaction_id退款",
			req: &paymgr.RefundRequest{
				TransactionID: "TX-R-1",
				OutRefundNo:   "REF-1",
				RefundAmount:  50,
				TotalAmount:   100,
				Reason:        "测试退款",
				NotifyURL:     "https://example.com/refund-notify",
			},
			wantBody: `"transaction_id"`,
		},
		{
			name: "按out_trade_no退款",
			req: &paymgr.RefundRequest{
				OutTradeNo:   "ORD-R-2",
				OutRefundNo:  "REF-2",
				RefundAmount: 50,
				TotalAmount:  100,
				Reason:       "测试退款",
				NotifyURL:    "https://example.com/refund-notify",
			},
			wantBody: `"out_trade_no"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newMockWechatEnv(t)
			env.mux.HandleFunc("POST /v3/refund/domestic/refunds", func(w http.ResponseWriter, r *http.Request) {
				reqBody, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(reqBody), tt.wantBody) {
					t.Errorf("refund request body = %s, want containing %s", reqBody, tt.wantBody)
				}
				env.writeSigned(w, http.StatusOK, string(mustJSON(t, map[string]any{
					"refund_id":     "WX-REFUND-1",
					"out_refund_no": tt.req.OutRefundNo,
					"amount":        map[string]any{"total": 100, "refund": 50},
				})))
			})

			resp, err := env.provider.Refund(t.Context(), tt.req)
			if err != nil {
				t.Fatalf("Refund() error = %v", err)
			}
			if resp.RefundID != "WX-REFUND-1" {
				t.Fatalf("RefundID = %q, want %q", resp.RefundID, "WX-REFUND-1")
			}
			if resp.OutRefundNo != tt.req.OutRefundNo {
				t.Fatalf("OutRefundNo = %q, want %q", resp.OutRefundNo, tt.req.OutRefundNo)
			}
			if resp.RefundAmount != 50 {
				t.Fatalf("RefundAmount = %d, want 50", resp.RefundAmount)
			}
		})
	}
}

// TestRefundNullAmount 是"响应 amount 为 null 时不 panic"修复的回归测试。
func TestRefundNullAmount(t *testing.T) {
	env := newMockWechatEnv(t)
	env.handle("POST /v3/refund/domestic/refunds", http.StatusOK,
		`{"refund_id":"WX-REFUND-NULL","out_refund_no":"REF-NULL","amount":null}`)

	resp, err := env.provider.Refund(t.Context(), &paymgr.RefundRequest{
		TransactionID: "TX-R-NULL",
		OutRefundNo:   "REF-NULL",
		RefundAmount:  50,
		TotalAmount:   100,
	})
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	if resp.RefundAmount != 0 {
		t.Fatalf("RefundAmount = %d, want 0 when amount is null", resp.RefundAmount)
	}
}

func TestQueryRefundStatusMapping(t *testing.T) {
	tests := []struct {
		status string
		want   paymgr.RefundStatus
	}{
		{status: "SUCCESS", want: paymgr.RefundStatusSuccess},
		{status: "CLOSED", want: paymgr.RefundStatusClosed},
		{status: "PROCESSING", want: paymgr.RefundStatusProcessing},
		{status: "ABNORMAL", want: paymgr.RefundStatusAbnormal},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			env := newMockWechatEnv(t)
			body := map[string]any{
				"refund_id":      "WX-REFUND-Q",
				"out_refund_no":  "REF-Q-1",
				"transaction_id": "TX-Q-1",
				"out_trade_no":   "ORD-Q-1",
				"status":         tt.status,
				"create_time":    "2024-05-20T13:00:00+08:00",
				"amount":         map[string]any{"total": 100, "refund": 50},
			}
			if tt.status == "SUCCESS" {
				body["success_time"] = "2024-05-20T13:29:35+08:00"
			}
			env.handle("GET /v3/refund/domestic/refunds/REF-Q-1", http.StatusOK, string(mustJSON(t, body)))

			resp, err := env.provider.QueryRefund(t.Context(), &paymgr.QueryRefundRequest{OutRefundNo: "REF-Q-1"})
			if err != nil {
				t.Fatalf("QueryRefund() error = %v", err)
			}
			if resp.RefundStatus != tt.want {
				t.Fatalf("RefundStatus = %q, want %q", resp.RefundStatus, tt.want)
			}
			if resp.RefundAmount != 50 || resp.TotalAmount != 100 {
				t.Fatalf("amounts = (%d, %d), want (50, 100)", resp.RefundAmount, resp.TotalAmount)
			}
			if resp.OutTradeNo != "ORD-Q-1" || resp.TransactionID != "TX-Q-1" || resp.RefundID != "WX-REFUND-Q" {
				t.Fatalf("unexpected refund ids: %+v", resp)
			}
			if tt.status == "SUCCESS" && resp.RefundedAt.IsZero() {
				t.Fatal("RefundedAt is zero, want success_time")
			}
		})
	}
}

// --- ParseNotify / ParseRefundNotify ---

// testTransactionPlaintext 构造支付通知解密后的业务 JSON。
func testTransactionPlaintext(t *testing.T, mchID, appID string) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{
		"mchid":          mchID,
		"appid":          appID,
		"out_trade_no":   "ORD-N-1",
		"transaction_id": "TX-N-1",
		"trade_state":    "SUCCESS",
		"success_time":   "2024-05-20T13:29:35+08:00",
		"amount":         map[string]any{"total": 100},
		"payer":          map[string]any{"openid": "openid-n-1"},
		"attach":         `{"order_source":"test"}`,
	})
}

// testRefundNotifyPlaintext 构造退款通知解密后的业务 JSON。
func testRefundNotifyPlaintext(t *testing.T, mchID string) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{
		"mchid":                 mchID,
		"out_trade_no":          "ORD-RN-1",
		"transaction_id":        "TX-RN-1",
		"out_refund_no":         "REF-RN-1",
		"refund_id":             "WX-REFUND-RN-1",
		"refund_status":         "SUCCESS",
		"success_time":          "2024-05-20T14:00:00+08:00",
		"user_received_account": "招商银行信用卡0403",
		"amount": map[string]any{
			"total":        100,
			"refund":       50,
			"payer_total":  100,
			"payer_refund": 50,
		},
	})
}

func TestParseNotifySuccess(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("TRANSACTION.SUCCESS",
		testTransactionPlaintext(t, env.provider.cfg.MchID, env.provider.cfg.AppID))

	result, err := env.provider.ParseNotify(t.Context(), req)
	if err != nil {
		t.Fatalf("ParseNotify() error = %v", err)
	}
	if result.Channel != paymgr.ChannelWechat {
		t.Fatalf("Channel = %q, want %q", result.Channel, paymgr.ChannelWechat)
	}
	if result.OutTradeNo != "ORD-N-1" || result.TransactionID != "TX-N-1" {
		t.Fatalf("unexpected notify ids: %+v", result)
	}
	if result.TradeStatus != paymgr.TradeStatusPaid {
		t.Fatalf("TradeStatus = %q, want %q", result.TradeStatus, paymgr.TradeStatusPaid)
	}
	if result.TotalAmount != 100 {
		t.Fatalf("TotalAmount = %d, want 100", result.TotalAmount)
	}
	if result.BuyerID != "openid-n-1" {
		t.Fatalf("BuyerID = %q, want %q", result.BuyerID, "openid-n-1")
	}
	want, _ := time.Parse(time.RFC3339, "2024-05-20T13:29:35+08:00")
	if !result.PaidAt.Equal(want) {
		t.Fatalf("PaidAt = %v, want %v", result.PaidAt, want)
	}
	if result.Metadata["order_source"] != "test" {
		t.Fatalf("Metadata = %v, want order_source=test", result.Metadata)
	}
}

func TestParseNotifyRejectsRefundEvent(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("REFUND.SUCCESS", testRefundNotifyPlaintext(t, env.provider.cfg.MchID))

	_, err := env.provider.ParseNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
	if !strings.Contains(err.Error(), "event_type") {
		t.Fatalf("ParseNotify() error = %v, want event_type message", err)
	}
}

func TestParseNotifyMchIDMismatch(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("TRANSACTION.SUCCESS",
		testTransactionPlaintext(t, "1999999999", env.provider.cfg.AppID))

	_, err := env.provider.ParseNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
	if !strings.Contains(err.Error(), "mchid mismatch") {
		t.Fatalf("ParseNotify() error = %v, want mchid mismatch message", err)
	}
}

func TestParseNotifyAppIDMismatch(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("TRANSACTION.SUCCESS",
		testTransactionPlaintext(t, env.provider.cfg.MchID, "wx-other-app"))

	_, err := env.provider.ParseNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
	if !strings.Contains(err.Error(), "appid mismatch") {
		t.Fatalf("ParseNotify() error = %v, want appid mismatch message", err)
	}
}

func TestParseNotifyBadSignature(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("TRANSACTION.SUCCESS",
		testTransactionPlaintext(t, env.provider.cfg.MchID, env.provider.cfg.AppID))
	req.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString([]byte("forged")))

	_, err := env.provider.ParseNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
}

func TestParseNotifyHandlerNotInitialized(t *testing.T) {
	p := &Provider{}

	if _, err := p.ParseNotify(t.Context(), httptest.NewRequest(http.MethodPost, "/notify", nil)); err == nil ||
		!strings.Contains(err.Error(), "notify handler not initialized") {
		t.Fatalf("ParseNotify() error = %v, want not initialized error", err)
	}
	if _, err := p.ParseRefundNotify(t.Context(), httptest.NewRequest(http.MethodPost, "/notify", nil)); err == nil ||
		!strings.Contains(err.Error(), "notify handler not initialized") {
		t.Fatalf("ParseRefundNotify() error = %v, want not initialized error", err)
	}
}

func TestParseRefundNotifySuccess(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("REFUND.SUCCESS", testRefundNotifyPlaintext(t, env.provider.cfg.MchID))

	result, err := env.provider.ParseRefundNotify(t.Context(), req)
	if err != nil {
		t.Fatalf("ParseRefundNotify() error = %v", err)
	}
	if result.Channel != paymgr.ChannelWechat {
		t.Fatalf("Channel = %q, want %q", result.Channel, paymgr.ChannelWechat)
	}
	if result.OutTradeNo != "ORD-RN-1" || result.TransactionID != "TX-RN-1" ||
		result.OutRefundNo != "REF-RN-1" || result.RefundID != "WX-REFUND-RN-1" {
		t.Fatalf("unexpected refund notify ids: %+v", result)
	}
	if result.RefundStatus != paymgr.RefundStatusSuccess {
		t.Fatalf("RefundStatus = %q, want %q", result.RefundStatus, paymgr.RefundStatusSuccess)
	}
	if result.RefundAmount != 50 || result.TotalAmount != 100 {
		t.Fatalf("amounts = (%d, %d), want (50, 100)", result.RefundAmount, result.TotalAmount)
	}
	if result.UserReceivedAccount != "招商银行信用卡0403" {
		t.Fatalf("UserReceivedAccount = %q, want %q", result.UserReceivedAccount, "招商银行信用卡0403")
	}
	want, _ := time.Parse(time.RFC3339, "2024-05-20T14:00:00+08:00")
	if !result.RefundedAt.Equal(want) {
		t.Fatalf("RefundedAt = %v, want %v", result.RefundedAt, want)
	}
}

func TestParseRefundNotifyRejectsTransactionEvent(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("TRANSACTION.SUCCESS",
		testTransactionPlaintext(t, env.provider.cfg.MchID, env.provider.cfg.AppID))

	_, err := env.provider.ParseRefundNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseRefundNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
	if !strings.Contains(err.Error(), "event_type") {
		t.Fatalf("ParseRefundNotify() error = %v, want event_type message", err)
	}
}

func TestParseRefundNotifyMchIDMismatch(t *testing.T) {
	env := newMockWechatEnv(t)
	req := env.notifyRequest("REFUND.SUCCESS", testRefundNotifyPlaintext(t, "1999999999"))

	_, err := env.provider.ParseRefundNotify(t.Context(), req)
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseRefundNotify() error = %v, want wrapped ErrInvalidNotify", err)
	}
	if !strings.Contains(err.Error(), "mchid mismatch") {
		t.Fatalf("ParseRefundNotify() error = %v, want mchid mismatch message", err)
	}
}

// --- Channel / ACKNotify ---

func TestChannel(t *testing.T) {
	p := &Provider{}
	if got := p.Channel(); got != paymgr.ChannelWechat {
		t.Fatalf("Channel() = %q, want %q", got, paymgr.ChannelWechat)
	}
}

func TestACKNotify(t *testing.T) {
	p := &Provider{}
	rec := httptest.NewRecorder()
	p.ACKNotify(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if body := rec.Body.String(); body != `{"code":"SUCCESS","message":"OK"}` {
		t.Fatalf("body = %s, want success ack", body)
	}
}

// --- Config.Validate 必填字段分支 ---

func TestConfigValidateRequiredFields(t *testing.T) {
	priv, _ := sharedTestKeys(t)

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "缺AppID", mutate: func(c *Config) { c.AppID = "" }, wantErr: "app_id is required"},
		{name: "缺MchID", mutate: func(c *Config) { c.MchID = "" }, wantErr: "mch_id is required"},
		{name: "缺证书序列号", mutate: func(c *Config) { c.MchCertSerialNumber = "" }, wantErr: "mch_cert_serial_number is required"},
		{name: "缺APIv3Key", mutate: func(c *Config) { c.MchAPIv3Key = "" }, wantErr: "mch_apiv3_key is required"},
		{name: "缺商户私钥", mutate: func(c *Config) { c.MchPrivateKey = nil }, wantErr: "mch_private_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig(priv)
			cfg.WechatPayCertificatePEM = "pem" // 满足验签侧二选一
			tt.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// --- NewProvider / 构造与凭据解析 ---

func TestNewProviderWithOptions(t *testing.T) {
	priv, platform := sharedTestKeys(t)

	p, err := NewProvider(t.Context(),
		nil, // nil Option 应被忽略
		WithAppID("wx1234567890abcdef"),
		WithMerchant("1900000001", "SERIAL", "0123456789abcdef0123456789abcdef"),
		WithMerchantPrivateKey(priv),
		WithPublicKeyID(testPubKeyID),
		WithPublicKey(&platform.PublicKey),
	)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if p.client == nil || p.notifyHandler == nil {
		t.Fatal("provider not fully initialized")
	}
}

func TestNewProviderWithConfigOption(t *testing.T) {
	priv, platform := sharedTestKeys(t)
	cfg := baseConfig(priv)
	cfg.WechatPayPublicKeyID = testPubKeyID
	cfg.WechatPayPublicKey = &platform.PublicKey

	p, err := NewProvider(t.Context(), cfg) // *Config 自身实现 Option
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if p.cfg == cfg {
		t.Fatal("provider should hold a copy of config, not the original pointer")
	}

	// 构造后修改原 Config 字段不得影响 Provider
	originalAppID := cfg.AppID
	cfg.AppID = "wx-mutated"
	if p.cfg.AppID != originalAppID {
		t.Fatalf("provider cfg.AppID = %q after mutating original, want %q", p.cfg.AppID, originalAppID)
	}
}

func TestNewProviderErrors(t *testing.T) {
	priv, _ := sharedTestKeys(t)

	t.Run("nil配置Option", func(t *testing.T) {
		if _, err := NewProvider(t.Context(), (*Config)(nil)); err == nil ||
			!strings.Contains(err.Error(), "config is required") {
			t.Fatalf("NewProvider() error = %v, want config required error", err)
		}
	})

	t.Run("nil配置结构体", func(t *testing.T) {
		if _, err := NewProviderWithConfig(t.Context(), nil); err == nil ||
			!strings.Contains(err.Error(), "config is required") {
			t.Fatalf("NewProviderWithConfig() error = %v, want config required error", err)
		}
	})

	t.Run("配置校验失败", func(t *testing.T) {
		if _, err := NewProviderWithConfig(t.Context(), &Config{}); err == nil ||
			!strings.Contains(err.Error(), "app_id is required") {
			t.Fatalf("NewProviderWithConfig() error = %v, want validate error", err)
		}
	})

	t.Run("商户私钥PEM非法", func(t *testing.T) {
		cfg := baseConfig(priv)
		cfg.MchPrivateKey = nil
		cfg.MchPrivateKeyPEM = "not-a-pem"
		cfg.WechatPayPublicKeyID = testPubKeyID
		cfg.WechatPayPublicKey = &priv.PublicKey
		if _, err := NewProviderWithConfig(t.Context(), cfg); err == nil ||
			!strings.Contains(err.Error(), "load private key") {
			t.Fatalf("NewProviderWithConfig() error = %v, want load private key error", err)
		}
	})

	t.Run("微信公钥PEM非法", func(t *testing.T) {
		cfg := baseConfig(priv)
		cfg.WechatPayPublicKeyID = testPubKeyID
		cfg.WechatPayPublicKeyPEM = "not-a-pem"
		if _, err := NewProviderWithConfig(t.Context(), cfg); err == nil ||
			!strings.Contains(err.Error(), "load public key") {
			t.Fatalf("NewProviderWithConfig() error = %v, want load public key error", err)
		}
	})

	t.Run("平台证书PEM非法", func(t *testing.T) {
		cfg := baseConfig(priv)
		cfg.WechatPayCertificatePEM = "not-a-pem"
		if _, err := NewProviderWithConfig(t.Context(), cfg); err == nil ||
			!strings.Contains(err.Error(), "load platform cert") {
			t.Fatalf("NewProviderWithConfig() error = %v, want load platform cert error", err)
		}
	})
}

func TestResolvePrivateKey(t *testing.T) {
	priv, _ := sharedTestKeys(t)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	t.Run("对象优先", func(t *testing.T) {
		got, err := resolvePrivateKey(&Config{MchPrivateKey: priv, MchPrivateKeyPEM: "garbage"})
		if err != nil {
			t.Fatalf("resolvePrivateKey() error = %v", err)
		}
		if got != priv {
			t.Fatal("resolvePrivateKey() did not return the provided object")
		}
	})

	t.Run("PEM文本", func(t *testing.T) {
		got, err := resolvePrivateKey(&Config{MchPrivateKeyPEM: pemText})
		if err != nil {
			t.Fatalf("resolvePrivateKey() error = %v", err)
		}
		if got.N.Cmp(priv.N) != 0 {
			t.Fatal("resolvePrivateKey() returned mismatched key")
		}
	})

	t.Run("文件路径", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mch_private_key.pem")
		if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		got, err := resolvePrivateKey(&Config{MchPrivateKeyPath: path})
		if err != nil {
			t.Fatalf("resolvePrivateKey() error = %v", err)
		}
		if got.N.Cmp(priv.N) != 0 {
			t.Fatal("resolvePrivateKey() returned mismatched key")
		}
	})

	t.Run("缺失来源", func(t *testing.T) {
		if _, err := resolvePrivateKey(&Config{}); err == nil {
			t.Fatal("resolvePrivateKey() error = nil, want error")
		}
	})
}

func TestResolvePlatformCertificate(t *testing.T) {
	priv, _ := sharedTestKeys(t)
	cert := testCertificate(t, priv)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

	t.Run("对象优先", func(t *testing.T) {
		got, err := resolvePlatformCertificate(&Config{WechatPayCertificate: cert, WechatPayCertificatePEM: "garbage"})
		if err != nil {
			t.Fatalf("resolvePlatformCertificate() error = %v", err)
		}
		if got != cert {
			t.Fatal("resolvePlatformCertificate() did not return the provided object")
		}
	})

	t.Run("PEM文本", func(t *testing.T) {
		got, err := resolvePlatformCertificate(&Config{WechatPayCertificatePEM: pemText})
		if err != nil {
			t.Fatalf("resolvePlatformCertificate() error = %v", err)
		}
		if !got.Equal(cert) {
			t.Fatal("resolvePlatformCertificate() returned mismatched certificate")
		}
	})

	t.Run("文件路径", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "platform_cert.pem")
		if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		got, err := resolvePlatformCertificate(&Config{WechatPayCertificatePath: path})
		if err != nil {
			t.Fatalf("resolvePlatformCertificate() error = %v", err)
		}
		if !got.Equal(cert) {
			t.Fatal("resolvePlatformCertificate() returned mismatched certificate")
		}
	})

	t.Run("缺失来源", func(t *testing.T) {
		if _, err := resolvePlatformCertificate(&Config{}); err == nil {
			t.Fatal("resolvePlatformCertificate() error = nil, want error")
		}
	})
}

func TestMerchantAndCertificateOptions(t *testing.T) {
	priv, _ := sharedTestKeys(t)
	cert := testCertificate(t, priv)

	cfg := &Config{}
	opts := []Option{
		WithAppID("wx-app"),
		WithMerchant("1900000009", "SERIAL-9", "key"),
		WithMerchantPrivateKeyPath("/keys/mch.pem"),
		WithMerchantPrivateKeyPEM("mch-pem"),
		WithPlatformCertificatePath("/certs/platform.pem"),
		WithPlatformCertificatePEM("cert-pem"),
		WithPlatformCertificate(cert),
	}
	for _, o := range opts {
		if err := o.apply(cfg); err != nil {
			t.Fatalf("apply() error = %v", err)
		}
	}

	if cfg.AppID != "wx-app" ||
		cfg.MchID != "1900000009" ||
		cfg.MchCertSerialNumber != "SERIAL-9" ||
		cfg.MchAPIv3Key != "key" ||
		cfg.MchPrivateKeyPath != "/keys/mch.pem" ||
		cfg.MchPrivateKeyPEM != "mch-pem" ||
		cfg.WechatPayCertificatePath != "/certs/platform.pem" ||
		cfg.WechatPayCertificatePEM != "cert-pem" ||
		cfg.WechatPayCertificate != cert {
		t.Fatalf("options not applied: %+v", cfg)
	}
}
