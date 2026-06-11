package alipay

// 本文件通过 httptest.Server 模拟支付宝网关，对 Provider 的下单 / 查询 / 关单 /
// 退款 / 通知解析做端到端测试。
//
// 验签方案：测试运行时生成两对 RSA 密钥——
//   - 应用密钥：交给 sdk.New 用于请求签名；
//   - 平台密钥：模拟支付宝侧，测试网关用其私钥对响应业务体（<method>_response
//     字段的原始 JSON 字节）做 SHA256-RSA-PKCS1v15 签名，客户端通过
//     LoadAliPayPublicKey 加载对应公钥完成真实验签，不绕过 SDK 验签逻辑。
//
// 异步通知同理：按支付宝规则（参数升序 k=v 以 & 连接，忽略 sign/sign_type/
// alipay_cert_sn）用平台私钥签名 form。

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	sdk "github.com/smartwalle/alipay/v3"
)

const testGatewayAppID = "2021000000000001"

// testKeyPair 运行时生成的测试密钥，全部测试共享一份以加速执行。
type testKeyPair struct {
	appPrivatePEM  string          // 应用私钥（PKCS8 PEM），用于客户端请求签名
	platformKey    *rsa.PrivateKey // 模拟支付宝平台私钥，用于网关响应/通知签名
	platformPubPEM string          // 平台公钥（PKIX PEM），客户端验签用
}

var loadTestKeyPair = sync.OnceValues(func() (*testKeyPair, error) {
	appKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	appDER, err := x509.MarshalPKCS8PrivateKey(appKey)
	if err != nil {
		return nil, err
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	platformPubDER, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if err != nil {
		return nil, err
	}
	return &testKeyPair{
		appPrivatePEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: appDER})),
		platformKey:    platformKey,
		platformPubPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: platformPubDER})),
	}, nil
})

func testKeys(t *testing.T) *testKeyPair {
	t.Helper()
	keys, err := loadTestKeyPair()
	if err != nil {
		t.Fatalf("generate test keys: %v", err)
	}
	return keys
}

// signSHA256RSA 用测试私钥对 data 做 SHA256-RSA-PKCS1v15 签名并 base64 编码。
func signSHA256RSA(t *testing.T, key *rsa.PrivateKey, data []byte) string {
	t.Helper()
	digest := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// gatewayTest 持有指向 httptest 网关的 Provider，并记录最近一次网关请求表单。
type gatewayTest struct {
	t        *testing.T
	provider *Provider
	keys     *testKeyPair

	mu   sync.Mutex
	body func(method string) string // 按请求 method 构造完整响应体
	form url.Values                 // 最近一次网关请求的表单
}

func newGatewayTest(t *testing.T) *gatewayTest {
	t.Helper()
	gt := &gatewayTest{t: t, keys: testKeys(t)}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gt.mu.Lock()
		gt.form = r.Form
		build := gt.body
		gt.mu.Unlock()
		if build == nil {
			http.Error(w, "no response configured", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		_, _ = w.Write([]byte(build(r.Form.Get("method"))))
	}))
	t.Cleanup(srv.Close)

	client, err := sdk.New(testGatewayAppID, gt.keys.appPrivatePEM, false,
		sdk.WithHTTPClient(srv.Client()),
		sdk.WithSandboxGateway(srv.URL))
	if err != nil {
		t.Fatalf("sdk.New() error = %v", err)
	}
	if err := client.LoadAliPayPublicKey(gt.keys.platformPubPEM); err != nil {
		t.Fatalf("LoadAliPayPublicKey() error = %v", err)
	}

	gt.provider = &Provider{
		cfg:    &Config{AppID: testGatewayAppID},
		client: client,
	}
	return gt
}

// respondSigned 配置网关返回带合法平台签名的业务响应。
func (gt *gatewayTest) respondSigned(biz string) {
	gt.setBody(func(method string) string {
		return `{"` + bizFieldName(method) + `":` + biz +
			`,"sign":"` + signSHA256RSA(gt.t, gt.keys.platformKey, []byte(biz)) + `"}`
	})
}

// respondUnsigned 配置网关返回不带签名的业务响应；SDK 会把该业务体按错误处理。
func (gt *gatewayTest) respondUnsigned(biz string) {
	gt.setBody(func(method string) string {
		return `{"` + bizFieldName(method) + `":` + biz + `}`
	})
}

func (gt *gatewayTest) setBody(build func(method string) string) {
	gt.mu.Lock()
	gt.body = build
	gt.mu.Unlock()
}

// bizContent 返回最近一次网关请求的 biz_content。
func (gt *gatewayTest) bizContent() string {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	if gt.form == nil {
		gt.t.Fatal("gateway has not received any request")
	}
	return gt.form.Get("biz_content")
}

func bizFieldName(method string) string {
	return strings.ReplaceAll(method, ".", "_") + "_response"
}

// assertChannelError 断言 err 是渠道错误且错误码匹配。
func assertChannelError(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want *paymgr.ChannelError")
	}
	var chErr *paymgr.ChannelError
	if !errors.As(err, &chErr) {
		t.Fatalf("err = %v, want *paymgr.ChannelError", err)
	}
	if chErr.Code != wantCode {
		t.Fatalf("ChannelError.Code = %q, want %q", chErr.Code, wantCode)
	}
	if chErr.Channel != paymgr.ChannelAlipay {
		t.Fatalf("ChannelError.Channel = %q, want %q", chErr.Channel, paymgr.ChannelAlipay)
	}
}

// --- UnifiedOrder ---

func TestUnifiedOrderNative(t *testing.T) {
	tests := []struct {
		name            string
		biz             string
		wantCodeURL     string
		wantChannelCode string
	}{
		{
			name:        "success",
			biz:         `{"code":"10000","msg":"Success","out_trade_no":"ORD-NATIVE-1","qr_code":"https://qr.alipay.com/test123"}`,
			wantCodeURL: "https://qr.alipay.com/test123",
		},
		{
			name:            "business_failure",
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TOTAL_FEE_EXCEED","sub_msg":"订单金额超限"}`,
			wantChannelCode: "ACQ.TOTAL_FEE_EXCEED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			gt.respondSigned(tt.biz)

			resp, err := gt.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
				OutTradeNo:  "ORD-NATIVE-1",
				TotalAmount: 1234,
				Subject:     "Native order",
				TradeType:   paymgr.TradeTypeNative,
				NotifyURL:   "https://api.example.com/notify/alipay",
				ExpireAt:    time.Now().Add(2*time.Minute + 30*time.Second),
				Metadata:    map[string]string{"order_id": "ORD-NATIVE-1"},
			})

			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("UnifiedOrder() error = %v", err)
			}
			if resp.CodeURL != tt.wantCodeURL {
				t.Fatalf("CodeURL = %q, want %q", resp.CodeURL, tt.wantCodeURL)
			}
			if resp.Channel != paymgr.ChannelAlipay {
				t.Fatalf("Channel = %q, want %q", resp.Channel, paymgr.ChannelAlipay)
			}

			bizContent := gt.bizContent()
			if !strings.Contains(bizContent, `"timeout_express":"2m"`) {
				t.Fatalf("biz_content = %q, want timeout_express 2m", bizContent)
			}
			if !strings.Contains(bizContent, `"passback_params":"order_id=ORD-NATIVE-1"`) {
				t.Fatalf("biz_content = %q, want passback_params", bizContent)
			}
		})
	}
}

func TestUnifiedOrderJSAPI(t *testing.T) {
	tests := []struct {
		name            string
		biz             string
		wantPrepayID    string
		wantChannelCode string
	}{
		{
			name:         "success",
			biz:          `{"code":"10000","msg":"Success","out_trade_no":"ORD-JSAPI-1","trade_no":"2026061122001400001234567890"}`,
			wantPrepayID: "2026061122001400001234567890",
		},
		{
			name:            "business_failure",
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.BUYER_NOT_EXIST","sub_msg":"买家不存在"}`,
			wantChannelCode: "ACQ.BUYER_NOT_EXIST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			gt.respondSigned(tt.biz)

			resp, err := gt.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
				OutTradeNo:  "ORD-JSAPI-1",
				TotalAmount: 1234,
				Subject:     "JSAPI order",
				TradeType:   paymgr.TradeTypeJSAPI,
				NotifyURL:   "https://api.example.com/notify/alipay",
				OpenID:      "2088102177846880",
				ExpireAt:    time.Now().Add(15*time.Minute + 30*time.Second),
				Metadata:    map[string]string{"order_id": "ORD-JSAPI-1"},
			})

			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("UnifiedOrder() error = %v", err)
			}
			if resp.PrepayID != tt.wantPrepayID {
				t.Fatalf("PrepayID = %q, want %q", resp.PrepayID, tt.wantPrepayID)
			}

			bizContent := gt.bizContent()
			if !strings.Contains(bizContent, `"product_code":"JSAPI_PAY"`) {
				t.Fatalf("biz_content = %q, want product_code JSAPI_PAY", bizContent)
			}
			if !strings.Contains(bizContent, `"op_app_id":"`+testGatewayAppID+`"`) {
				t.Fatalf("biz_content = %q, want op_app_id", bizContent)
			}
			if !strings.Contains(bizContent, `"buyer_id":"2088102177846880"`) {
				t.Fatalf("biz_content = %q, want buyer_id", bizContent)
			}
			if !strings.Contains(bizContent, `"timeout_express":"15m"`) {
				t.Fatalf("biz_content = %q, want timeout_express 15m", bizContent)
			}
			if !strings.Contains(bizContent, `"passback_params":"order_id=ORD-JSAPI-1"`) {
				t.Fatalf("biz_content = %q, want passback_params", bizContent)
			}
		})
	}
}

func TestUnifiedOrderExpireAt(t *testing.T) {
	tests := []struct {
		name        string
		expireAt    time.Time
		wantErr     error
		wantTimeout string
	}{
		{
			name:     "already_expired",
			expireAt: time.Now().Add(-time.Minute),
			wantErr:  paymgr.ErrInvalidParam,
		},
		{
			name:     "less_than_one_minute",
			expireAt: time.Now().Add(30 * time.Second),
			wantErr:  paymgr.ErrInvalidParam,
		},
		{
			name:        "at_least_one_minute",
			expireAt:    time.Now().Add(90 * time.Second),
			wantTimeout: `"timeout_express":"1m"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			gt.respondSigned(`{"code":"10000","msg":"Success","out_trade_no":"ORD-EXP-1","qr_code":"https://qr.alipay.com/exp"}`)

			_, err := gt.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
				OutTradeNo:  "ORD-EXP-1",
				TotalAmount: 100,
				Subject:     "expire test",
				TradeType:   paymgr.TradeTypeNative,
				NotifyURL:   "https://api.example.com/notify/alipay",
				ExpireAt:    tt.expireAt,
			})

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("UnifiedOrder() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnifiedOrder() error = %v", err)
			}
			if bizContent := gt.bizContent(); !strings.Contains(bizContent, tt.wantTimeout) {
				t.Fatalf("biz_content = %q, want %q", bizContent, tt.wantTimeout)
			}
		})
	}
}

func TestUnifiedOrderUnsupportedType(t *testing.T) {
	gt := newGatewayTest(t)

	_, err := gt.provider.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-X-1",
		TotalAmount: 100,
		Subject:     "unsupported",
		TradeType:   paymgr.TradeType("balance"),
		NotifyURL:   "https://api.example.com/notify/alipay",
	})
	if !errors.Is(err, paymgr.ErrUnsupportedType) {
		t.Fatalf("UnifiedOrder() error = %v, want ErrUnsupportedType", err)
	}
}

// --- QueryOrder ---

func TestQueryOrderGateway(t *testing.T) {
	tests := []struct {
		name            string
		req             *paymgr.QueryOrderRequest
		biz             string
		wantErr         error
		wantChannelCode string
		want            *paymgr.QueryOrderResponse
		wantBizContains string
	}{
		{
			name: "success_by_out_trade_no",
			req:  &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-1"},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234500001","out_trade_no":"ORD-Q-1",` +
				`"trade_status":"TRADE_SUCCESS","total_amount":"12.34","send_pay_date":"2026-06-11 10:00:00","buyer_user_id":"2088102177846880"}`,
			want: &paymgr.QueryOrderResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-Q-1",
				TransactionID: "2026061122001400001234500001",
				TradeStatus:   paymgr.TradeStatusPaid,
				TotalAmount:   1234,
				PaidAt:        time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC),
				BuyerID:       "2088102177846880",
			},
			wantBizContains: `"out_trade_no":"ORD-Q-1"`,
		},
		{
			name: "success_by_transaction_id_wait_buyer_pay",
			req:  &paymgr.QueryOrderRequest{TransactionID: "2026061122001400001234500002"},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234500002","out_trade_no":"ORD-Q-2",` +
				`"trade_status":"WAIT_BUYER_PAY"}`,
			want: &paymgr.QueryOrderResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-Q-2",
				TransactionID: "2026061122001400001234500002",
				TradeStatus:   paymgr.TradeStatusPending,
			},
			wantBizContains: `"trade_no":"2026061122001400001234500002"`,
		},
		{
			name: "trade_closed",
			req:  &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-3"},
			biz:  `{"code":"10000","msg":"Success","trade_no":"T3","out_trade_no":"ORD-Q-3","trade_status":"TRADE_CLOSED"}`,
			want: &paymgr.QueryOrderResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-Q-3",
				TransactionID: "T3",
				TradeStatus:   paymgr.TradeStatusClosed,
			},
		},
		{
			name: "trade_finished_maps_to_paid",
			req:  &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-4"},
			biz:  `{"code":"10000","msg":"Success","trade_no":"T4","out_trade_no":"ORD-Q-4","trade_status":"TRADE_FINISHED"}`,
			want: &paymgr.QueryOrderResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-Q-4",
				TransactionID: "T4",
				TradeStatus:   paymgr.TradeStatusPaid,
			},
		},
		{
			name: "unknown_status_maps_to_error",
			req:  &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-5"},
			biz:  `{"code":"10000","msg":"Success","trade_no":"T5","out_trade_no":"ORD-Q-5","trade_status":"SOMETHING_NEW"}`,
			want: &paymgr.QueryOrderResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-Q-5",
				TransactionID: "T5",
				TradeStatus:   paymgr.TradeStatusError,
			},
		},
		{
			name:    "trade_not_exist",
			req:     &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-MISSING"},
			biz:     `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_NOT_EXIST","sub_msg":"交易不存在"}`,
			wantErr: paymgr.ErrOrderNotFound,
		},
		{
			name:            "other_business_failure",
			req:             &paymgr.QueryOrderRequest{OutTradeNo: "ORD-Q-ERR"},
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.SYSTEM_ERROR","sub_msg":"系统繁忙"}`,
			wantChannelCode: "ACQ.SYSTEM_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			gt.respondSigned(tt.biz)

			resp, err := gt.provider.QueryOrder(t.Context(), tt.req)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("QueryOrder() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("QueryOrder() error = %v", err)
			}
			if *resp != *tt.want {
				t.Fatalf("QueryOrder() = %+v, want %+v", *resp, *tt.want)
			}
			if tt.wantBizContains != "" {
				if bizContent := gt.bizContent(); !strings.Contains(bizContent, tt.wantBizContains) {
					t.Fatalf("biz_content = %q, want %q", bizContent, tt.wantBizContains)
				}
			}
		})
	}
}

// --- CloseOrder ---

func TestCloseOrderGateway(t *testing.T) {
	tests := []struct {
		name            string
		biz             string
		unsigned        bool // 网关返回不带签名的业务体，SDK 层报错
		wantChannelCode string
	}{
		{
			name: "success",
			biz:  `{"code":"10000","msg":"Success","trade_no":"T1","out_trade_no":"ORD-C-1"}`,
		},
		{
			name: "already_closed_is_idempotent",
			biz:  `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_HAS_CLOSE","sub_msg":"交易已关闭"}`,
		},
		{
			name:            "other_business_failure",
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_STATUS_ERROR","sub_msg":"交易状态异常"}`,
			wantChannelCode: "ACQ.TRADE_STATUS_ERROR",
		},
		{
			name:            "sdk_error_on_unsigned_failure",
			biz:             `{"code":"20000","msg":"Service Currently Unavailable","sub_code":"isp.unknow-error","sub_msg":"系统繁忙"}`,
			unsigned:        true,
			wantChannelCode: "SDK_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			if tt.unsigned {
				gt.respondUnsigned(tt.biz)
			} else {
				gt.respondSigned(tt.biz)
			}

			err := gt.provider.CloseOrder(t.Context(), &paymgr.CloseOrderRequest{OutTradeNo: "ORD-C-1"})

			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("CloseOrder() error = %v", err)
			}
		})
	}
}

// --- Refund ---

func TestRefundGateway(t *testing.T) {
	tests := []struct {
		name            string
		req             *paymgr.RefundRequest
		biz             string
		wantChannelCode string
		want            *paymgr.RefundResponse
		wantBizContains []string
	}{
		{
			// refund_fee 是该笔交易的累计退款总额（此处构造为 99.99 元），
			// RefundAmount 必须取本次请求金额 100 分，而非累计值 9999 分。
			name: "success_uses_request_amount_not_accumulated_refund_fee",
			req: &paymgr.RefundRequest{
				OutTradeNo:   "ORD-R-1",
				OutRefundNo:  "RFD-R-1",
				RefundAmount: 100,
				TotalAmount:  20000,
				Reason:       "用户取消",
			},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234600001","out_trade_no":"ORD-R-1",` +
				`"fund_change":"Y","refund_fee":"99.99"}`,
			want: &paymgr.RefundResponse{
				Channel:      paymgr.ChannelAlipay,
				OutRefundNo:  "RFD-R-1",
				RefundID:     "2026061122001400001234600001",
				RefundAmount: 100,
			},
			wantBizContains: []string{
				`"refund_amount":"1.00"`,
				`"out_request_no":"RFD-R-1"`,
				`"out_trade_no":"ORD-R-1"`,
				`"refund_reason":"用户取消"`,
			},
		},
		{
			name: "success_by_transaction_id",
			req: &paymgr.RefundRequest{
				TransactionID: "2026061122001400001234600002",
				OutRefundNo:   "RFD-R-2",
				RefundAmount:  250,
				TotalAmount:   1000,
			},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234600002","out_trade_no":"ORD-R-2",` +
				`"fund_change":"Y","refund_fee":"2.50"}`,
			want: &paymgr.RefundResponse{
				Channel:      paymgr.ChannelAlipay,
				OutRefundNo:  "RFD-R-2",
				RefundID:     "2026061122001400001234600002",
				RefundAmount: 250,
			},
			wantBizContains: []string{`"trade_no":"2026061122001400001234600002"`},
		},
		{
			// 同一 out_request_no 重复退款时支付宝幂等返回成功且 fund_change=N
			// （本次未发生资金变化），不应视为错误，金额仍取本次请求值。
			name: "idempotent_retry_fund_change_n",
			req: &paymgr.RefundRequest{
				OutTradeNo:   "ORD-R-1",
				OutRefundNo:  "RFD-R-1",
				RefundAmount: 100,
				TotalAmount:  20000,
			},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234600001","out_trade_no":"ORD-R-1",` +
				`"fund_change":"N","refund_fee":"1.00"}`,
			want: &paymgr.RefundResponse{
				Channel:      paymgr.ChannelAlipay,
				OutRefundNo:  "RFD-R-1",
				RefundID:     "2026061122001400001234600001",
				RefundAmount: 100,
			},
		},
		{
			name: "business_failure",
			req: &paymgr.RefundRequest{
				OutTradeNo:   "ORD-R-3",
				OutRefundNo:  "RFD-R-3",
				RefundAmount: 100,
				TotalAmount:  100,
			},
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_NOT_ALLOW_REFUND","sub_msg":"交易不允许退款"}`,
			wantChannelCode: "ACQ.TRADE_NOT_ALLOW_REFUND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			gt.respondSigned(tt.biz)

			resp, err := gt.provider.Refund(t.Context(), tt.req)

			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("Refund() error = %v", err)
			}
			if *resp != *tt.want {
				t.Fatalf("Refund() = %+v, want %+v", *resp, *tt.want)
			}
			bizContent := gt.bizContent()
			for _, want := range tt.wantBizContains {
				if !strings.Contains(bizContent, want) {
					t.Fatalf("biz_content = %q, want %q", bizContent, want)
				}
			}
		})
	}
}

// --- QueryRefund ---

func TestQueryRefundGateway(t *testing.T) {
	tests := []struct {
		name            string
		req             *paymgr.QueryRefundRequest
		biz             string // 为空表示请求不应到达网关
		wantErr         error
		wantChannelCode string
		want            *paymgr.QueryRefundResponse
		wantBizContains string
	}{
		{
			name:    "nil_request",
			req:     nil,
			wantErr: paymgr.ErrInvalidParam,
		},
		{
			name:    "missing_out_refund_no",
			req:     &paymgr.QueryRefundRequest{OutTradeNo: "ORD-QR-1"},
			wantErr: paymgr.ErrInvalidParam,
		},
		{
			name: "success_by_out_trade_no",
			req:  &paymgr.QueryRefundRequest{OutTradeNo: "ORD-QR-1", OutRefundNo: "RFD-QR-1"},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234700001","out_trade_no":"ORD-QR-1",` +
				`"out_request_no":"RFD-QR-1","refund_amount":"1.00","total_amount":"12.34","refund_status":"REFUND_SUCCESS",` +
				`"gmt_refund_pay":"2026-06-11 10:00:00"}`,
			want: &paymgr.QueryRefundResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-QR-1",
				TransactionID: "2026061122001400001234700001",
				OutRefundNo:   "RFD-QR-1",
				RefundID:      "2026061122001400001234700001",
				RefundStatus:  paymgr.RefundStatusSuccess,
				RefundAmount:  100,
				TotalAmount:   1234,
				RefundedAt:    time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC),
			},
			wantBizContains: `"out_trade_no":"ORD-QR-1"`,
		},
		{
			name: "success_by_transaction_id",
			req:  &paymgr.QueryRefundRequest{TransactionID: "2026061122001400001234700002", OutRefundNo: "RFD-QR-2"},
			biz: `{"code":"10000","msg":"Success","trade_no":"2026061122001400001234700002","out_trade_no":"ORD-QR-2",` +
				`"out_request_no":"RFD-QR-2","refund_amount":"0.50","total_amount":"1.00"}`,
			want: &paymgr.QueryRefundResponse{
				Channel:       paymgr.ChannelAlipay,
				OutTradeNo:    "ORD-QR-2",
				TransactionID: "2026061122001400001234700002",
				OutRefundNo:   "RFD-QR-2",
				RefundID:      "2026061122001400001234700002",
				RefundStatus:  paymgr.RefundStatusError, // 未返回 refund_status 归类为 Error
				RefundAmount:  50,
				TotalAmount:   100,
			},
			wantBizContains: `"trade_no":"2026061122001400001234700002"`,
		},
		{
			name:    "empty_out_request_no_means_not_found",
			req:     &paymgr.QueryRefundRequest{OutTradeNo: "ORD-QR-3", OutRefundNo: "RFD-QR-MISSING"},
			biz:     `{"code":"10000","msg":"Success","trade_no":"","out_trade_no":"","out_request_no":""}`,
			wantErr: paymgr.ErrOrderNotFound,
		},
		{
			name:    "trade_not_exist",
			req:     &paymgr.QueryRefundRequest{OutTradeNo: "ORD-QR-4", OutRefundNo: "RFD-QR-4"},
			biz:     `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_NOT_EXIST","sub_msg":"交易不存在"}`,
			wantErr: paymgr.ErrOrderNotFound,
		},
		{
			name:            "other_business_failure",
			req:             &paymgr.QueryRefundRequest{OutTradeNo: "ORD-QR-5", OutRefundNo: "RFD-QR-5"},
			biz:             `{"code":"40004","msg":"Business Failed","sub_code":"ACQ.SYSTEM_ERROR","sub_msg":"系统繁忙"}`,
			wantChannelCode: "ACQ.SYSTEM_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)
			if tt.biz != "" {
				gt.respondSigned(tt.biz)
			}

			resp, err := gt.provider.QueryRefund(t.Context(), tt.req)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("QueryRefund() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.wantChannelCode != "" {
				assertChannelError(t, err, tt.wantChannelCode)
				return
			}
			if err != nil {
				t.Fatalf("QueryRefund() error = %v", err)
			}
			if *resp != *tt.want {
				t.Fatalf("QueryRefund() = %+v, want %+v", *resp, *tt.want)
			}
			if tt.wantBizContains != "" {
				if bizContent := gt.bizContent(); !strings.Contains(bizContent, tt.wantBizContains) {
					t.Fatalf("biz_content = %q, want %q", bizContent, tt.wantBizContains)
				}
			}
		})
	}
}

// --- ParseNotify ---

// signNotifyValues 按支付宝通知验签规则对 values 签名并写入 sign 字段：
// 除 sign/sign_type/alipay_cert_sn 外的参数以 k=v 升序用 & 连接后签名。
func signNotifyValues(t *testing.T, key *rsa.PrivateKey, values url.Values) {
	t.Helper()
	pairs := make([]string, 0, len(values))
	for k, vs := range values {
		if k == "sign" || k == "sign_type" || k == "alipay_cert_sn" {
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, k+"="+v)
		}
	}
	sort.Strings(pairs)
	values.Set("sign", signSHA256RSA(t, key, []byte(strings.Join(pairs, "&"))))
}

func notifyRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://merchant.example.com/notify/alipay", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestParseNotify(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]string
		tamper  func(url.Values) // 签名后篡改，用于触发验签失败
		wantErr error
		check   func(*testing.T, *paymgr.NotifyResult)
	}{
		{
			name: "payment_success",
			fields: map[string]string{
				"app_id":          testGatewayAppID,
				"notify_type":     "trade_status_sync",
				"out_trade_no":    "ORD-N-1",
				"trade_no":        "2026061122001400001234800001",
				"trade_status":    "TRADE_SUCCESS",
				"total_amount":    "12.34",
				"gmt_payment":     "2026-06-11 10:00:00",
				"buyer_id":        "2088102177846880",
				"passback_params": "order_id=ORD-N-1",
			},
			check: func(t *testing.T, result *paymgr.NotifyResult) {
				t.Helper()
				if result.Channel != paymgr.ChannelAlipay {
					t.Fatalf("Channel = %q, want %q", result.Channel, paymgr.ChannelAlipay)
				}
				if result.OutTradeNo != "ORD-N-1" {
					t.Fatalf("OutTradeNo = %q, want %q", result.OutTradeNo, "ORD-N-1")
				}
				if result.TransactionID != "2026061122001400001234800001" {
					t.Fatalf("TransactionID = %q", result.TransactionID)
				}
				if result.TradeStatus != paymgr.TradeStatusPaid {
					t.Fatalf("TradeStatus = %q, want %q", result.TradeStatus, paymgr.TradeStatusPaid)
				}
				if result.TotalAmount != 1234 {
					t.Fatalf("TotalAmount = %d, want 1234", result.TotalAmount)
				}
				if want := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC); !result.PaidAt.Equal(want) {
					t.Fatalf("PaidAt = %v, want %v", result.PaidAt, want)
				}
				if result.BuyerID != "2088102177846880" {
					t.Fatalf("BuyerID = %q", result.BuyerID)
				}
				if want := map[string]string{"order_id": "ORD-N-1"}; !maps.Equal(result.Metadata, want) {
					t.Fatalf("Metadata = %#v, want %#v", result.Metadata, want)
				}
			},
		},
		{
			name: "app_id_mismatch",
			fields: map[string]string{
				"app_id":       "2021000000009999",
				"out_trade_no": "ORD-N-2",
				"trade_no":     "T2",
				"trade_status": "TRADE_SUCCESS",
			},
			wantErr: paymgr.ErrInvalidNotify,
		},
		{
			name: "refund_event_by_gmt_refund",
			fields: map[string]string{
				"app_id":       testGatewayAppID,
				"out_trade_no": "ORD-N-3",
				"trade_no":     "T3",
				"trade_status": "TRADE_SUCCESS", // 部分退款后交易状态仍为成功
				"gmt_refund":   "2026-06-11 11:00:00",
			},
			check: func(t *testing.T, result *paymgr.NotifyResult) {
				t.Helper()
				if result.TradeStatus != paymgr.TradeStatusRefunded {
					t.Fatalf("TradeStatus = %q, want %q", result.TradeStatus, paymgr.TradeStatusRefunded)
				}
			},
		},
		{
			name: "refund_event_by_refund_fee",
			fields: map[string]string{
				"app_id":       testGatewayAppID,
				"out_trade_no": "ORD-N-4",
				"trade_no":     "T4",
				"trade_status": "TRADE_CLOSED", // 全额退款后交易状态为关闭
				"refund_fee":   "12.34",
			},
			check: func(t *testing.T, result *paymgr.NotifyResult) {
				t.Helper()
				if result.TradeStatus != paymgr.TradeStatusRefunded {
					t.Fatalf("TradeStatus = %q, want %q", result.TradeStatus, paymgr.TradeStatusRefunded)
				}
			},
		},
		{
			name: "tampered_form_fails_verification",
			fields: map[string]string{
				"app_id":       testGatewayAppID,
				"out_trade_no": "ORD-N-5",
				"trade_no":     "T5",
				"trade_status": "TRADE_SUCCESS",
				"total_amount": "12.34",
			},
			tamper: func(values url.Values) {
				values.Set("total_amount", "9999.00")
			},
			wantErr: paymgr.ErrInvalidSign,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := newGatewayTest(t)

			values := url.Values{}
			for k, v := range tt.fields {
				values.Set(k, v)
			}
			values.Set("sign_type", "RSA2")
			signNotifyValues(t, gt.keys.platformKey, values)
			if tt.tamper != nil {
				tt.tamper(values)
			}

			result, err := gt.provider.ParseNotify(t.Context(), notifyRequest(t, values.Encode()))

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseNotify() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNotify() error = %v", err)
			}
			tt.check(t, result)
		})
	}
}

func TestParseNotifyBadForm(t *testing.T) {
	gt := newGatewayTest(t)

	_, err := gt.provider.ParseNotify(t.Context(), notifyRequest(t, "a=%zz"))
	if !errors.Is(err, paymgr.ErrInvalidNotify) {
		t.Fatalf("ParseNotify() error = %v, want ErrInvalidNotify", err)
	}
}

// --- 其他接口 ---

func TestChannel(t *testing.T) {
	p := &Provider{}
	if got := p.Channel(); got != paymgr.ChannelAlipay {
		t.Fatalf("Channel() = %q, want %q", got, paymgr.ChannelAlipay)
	}
}

func TestACKNotify(t *testing.T) {
	p := &Provider{}
	rec := httptest.NewRecorder()
	p.ACKNotify(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "success" {
		t.Fatalf("body = %q, want %q", got, "success")
	}
}
