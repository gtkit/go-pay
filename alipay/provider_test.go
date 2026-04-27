package alipay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"maps"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	sdk "github.com/smartwalle/alipay/v3"
)

var _ paymgr.Provider = (*Provider)(nil)

func TestCentToYuan(t *testing.T) {
	tests := []struct {
		name string
		cent int64
		want string
	}{
		{name: "zero", cent: 0, want: "0.00"},
		{name: "small", cent: 29, want: "0.29"},
		{name: "integer", cent: 3300, want: "33.00"},
		{name: "fraction", cent: 3333, want: "33.33"},
		{name: "negative", cent: -105, want: "-1.05"},
		{name: "max_int64", cent: math.MaxInt64, want: "92233720368547758.07"},
		{name: "min_int64", cent: math.MinInt64, want: "-92233720368547758.08"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := centToYuan(tt.cent); got != tt.want {
				t.Fatalf("centToYuan(%d) = %q, want %q", tt.cent, got, tt.want)
			}
		})
	}
}

func TestYuanToCent(t *testing.T) {
	tests := []struct {
		name string
		yuan string
		want int64
	}{
		{name: "small", yuan: "0.29", want: 29},
		{name: "fraction", yuan: "33.33", want: 3333},
		{name: "whole", yuan: "56.43", want: 5643},
		{name: "with_space", yuan: " 1.20 ", want: 120},
		{name: "missing_whole", yuan: ".99", want: 99},
		{name: "negative", yuan: "-1.05", want: -105},
		{name: "extra_precision_truncated", yuan: "1.234", want: 123},
		{name: "extra_zero_precision", yuan: "0.100", want: 10},
		{name: "max_int64", yuan: "92233720368547758.07", want: math.MaxInt64},
		{name: "overflow", yuan: "92233720368547758.08", want: 0},
		{name: "invalid_text", yuan: "abc", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := yuanToCent(tt.yuan); got != tt.want {
				t.Fatalf("yuanToCent(%q) = %d, want %d", tt.yuan, got, tt.want)
			}
		})
	}
}

func TestPassbackParamsRoundTrip(t *testing.T) {
	metadata := map[string]string{
		"plain":   "value",
		"amp":     "a&b",
		"equal":   "a=b",
		"space":   "hello world",
		"unicode": "中文",
	}

	encoded := encodePassbackParams(metadata)
	if encoded == "" {
		t.Fatal("encodePassbackParams returned empty string")
	}
	if encoded == url.QueryEscape(encoded) {
		t.Fatalf("encodePassbackParams(%#v) appears double-escaped: %q", metadata, encoded)
	}

	decoded := decodePassbackParams(encoded)
	if !maps.Equal(decoded, metadata) {
		t.Fatalf("decodePassbackParams(%q) = %#v, want %#v", encoded, decoded, metadata)
	}
}

func TestDecodePassbackParamsBackwardCompatible(t *testing.T) {
	raw := "a%3D1%26b%3D2"
	want := map[string]string{"a": "1", "b": "2"}

	if got := decodePassbackParams(raw); !maps.Equal(got, want) {
		t.Fatalf("decodePassbackParams(%q) = %#v, want %#v", raw, got, want)
	}
}

func TestMapAlipayRefundStatus(t *testing.T) {
	tests := map[string]paymgr.RefundStatus{
		"REFUND_SUCCESS":    paymgr.RefundStatusSuccess,
		"REFUND_PROCESSING": paymgr.RefundStatusProcessing,
		"REFUND_FAIL":       paymgr.RefundStatusError,
		"":                  paymgr.RefundStatusError,
		"UNKNOWN":           paymgr.RefundStatusError,
	}
	for in, want := range tests {
		if got := mapAlipayRefundStatus(in); got != want {
			t.Fatalf("mapAlipayRefundStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRefundNotifyReturnsNotSupported(t *testing.T) {
	p := &Provider{}
	_, err := p.ParseRefundNotify(context.Background(), nil)
	if err == nil {
		t.Fatal("ParseRefundNotify returned nil error")
	}
	if !errors.Is(err, paymgr.ErrNotSupported) {
		t.Fatalf("ParseRefundNotify err = %v, want wrapped ErrNotSupported", err)
	}
}

func newSignedTestProvider(t *testing.T) *Provider {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	client, err := sdk.New("2021000000000001", string(pemBytes), false)
	if err != nil {
		t.Fatalf("sdk.New() error = %v", err)
	}

	return &Provider{
		client: client,
		cfg:    &Config{AppID: "2021000000000001"},
	}
}

func TestBuildTradePagePayMapsFields(t *testing.T) {
	trade := buildTradePagePay(&paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 1234,
		Subject:     "PC page order",
		NotifyURL:   "https://api.example.com/notify/alipay",
		ReturnURL:   "https://www.example.com/pay/return",
	}, "12.34", "25m", "order_id=ORD-PAGE-1")

	if trade.OutTradeNo != "ORD-PAGE-1" {
		t.Fatalf("OutTradeNo = %q, want %q", trade.OutTradeNo, "ORD-PAGE-1")
	}
	if trade.TotalAmount != "12.34" {
		t.Fatalf("TotalAmount = %q, want %q", trade.TotalAmount, "12.34")
	}
	if trade.ProductCode != "FAST_INSTANT_TRADE_PAY" {
		t.Fatalf("ProductCode = %q, want %q", trade.ProductCode, "FAST_INSTANT_TRADE_PAY")
	}
	if trade.NotifyURL != "https://api.example.com/notify/alipay" {
		t.Fatalf("NotifyURL = %q, want %q", trade.NotifyURL, "https://api.example.com/notify/alipay")
	}
	if trade.ReturnURL != "https://www.example.com/pay/return" {
		t.Fatalf("ReturnURL = %q, want %q", trade.ReturnURL, "https://www.example.com/pay/return")
	}
	if trade.TimeoutExpress != "25m" {
		t.Fatalf("TimeoutExpress = %q, want %q", trade.TimeoutExpress, "25m")
	}
	if trade.PassbackParams != "order_id=ORD-PAGE-1" {
		t.Fatalf("PassbackParams = %q, want %q", trade.PassbackParams, "order_id=ORD-PAGE-1")
	}
}

func TestUnifiedOrderPageReturnsPayURL(t *testing.T) {
	p := newSignedTestProvider(t)

	resp, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 1234,
		Subject:     "PC page order",
		TradeType:   paymgr.TradeTypePage,
		NotifyURL:   "https://api.example.com/notify/alipay",
		ReturnURL:   "https://www.example.com/pay/return",
		ExpireAt:    time.Now().Add(25*time.Minute + 10*time.Second),
		Metadata: map[string]string{
			"order_id": "ORD-PAGE-1",
		},
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.PayURL == "" {
		t.Fatal("PayURL = empty")
	}

	u, err := url.Parse(resp.PayURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	values := u.Query()
	if got := values.Get("method"); got != "alipay.trade.page.pay" {
		t.Fatalf("method = %q, want %q", got, "alipay.trade.page.pay")
	}
	if got := values.Get("return_url"); got != "https://www.example.com/pay/return" {
		t.Fatalf("return_url = %q, want %q", got, "https://www.example.com/pay/return")
	}
	bizContent := values.Get("biz_content")
	if !strings.Contains(bizContent, `"out_trade_no":"ORD-PAGE-1"`) {
		t.Fatalf("biz_content = %q, want out_trade_no", bizContent)
	}
	if !strings.Contains(bizContent, `"product_code":"FAST_INSTANT_TRADE_PAY"`) {
		t.Fatalf("biz_content = %q, want product_code", bizContent)
	}
	if !strings.Contains(bizContent, `"timeout_express":"25m"`) {
		t.Fatalf("biz_content = %q, want timeout_express", bizContent)
	}
	if !strings.Contains(bizContent, `"passback_params":"order_id=ORD-PAGE-1"`) {
		t.Fatalf("biz_content = %q, want passback_params", bizContent)
	}
}

func TestUnifiedOrderH5ReturnsPayURLWithProductCode(t *testing.T) {
	p := newSignedTestProvider(t)

	resp, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-H5-1",
		TotalAmount: 1234,
		Subject:     "H5 order",
		TradeType:   paymgr.TradeTypeH5,
		NotifyURL:   "https://api.example.com/notify/alipay",
		ReturnURL:   "https://www.example.com/pay/return",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.PayURL == "" {
		t.Fatal("PayURL = empty")
	}

	u, err := url.Parse(resp.PayURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	bizContent := u.Query().Get("biz_content")
	if !strings.Contains(bizContent, `"product_code":"QUICK_WAP_WAY"`) {
		t.Fatalf("biz_content = %q, want product_code", bizContent)
	}
}

func TestUnifiedOrderAppReturnsAppParamsWithProductCode(t *testing.T) {
	p := newSignedTestProvider(t)

	resp, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-APP-1",
		TotalAmount: 1234,
		Subject:     "APP order",
		TradeType:   paymgr.TradeTypeApp,
		NotifyURL:   "https://api.example.com/notify/alipay",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.AppParams == "" {
		t.Fatal("AppParams = empty")
	}
	values, err := url.ParseQuery(resp.AppParams)
	if err != nil {
		t.Fatalf("url.ParseQuery() error = %v", err)
	}
	bizContent := values.Get("biz_content")
	if !strings.Contains(bizContent, `"product_code":"QUICK_MSECURITY_PAY"`) {
		t.Fatalf("biz_content = %q, want product_code", bizContent)
	}
}
