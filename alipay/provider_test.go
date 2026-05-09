package alipay

import (
	"context"
	"errors"
	"maps"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	alipayv3 "github.com/go-pay/gopay/alipay/v3"
	"github.com/gtkit/go-pay/paymgr"
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

func TestMapAlipayTradeStatus(t *testing.T) {
	tests := map[string]paymgr.TradeStatus{
		"WAIT_BUYER_PAY": paymgr.TradeStatusPending,
		"TRADE_SUCCESS":  paymgr.TradeStatusPaid,
		"TRADE_FINISHED": paymgr.TradeStatusPaid,
		"TRADE_CLOSED":   paymgr.TradeStatusClosed,
		"":               paymgr.TradeStatusError,
		"UNKNOWN":        paymgr.TradeStatusError,
	}
	for in, want := range tests {
		if got := mapAlipayTradeStatus(in); got != want {
			t.Fatalf("mapAlipayTradeStatus(%q) = %q, want %q", in, got, want)
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

func TestACKNotifyWritesSuccess(t *testing.T) {
	p := &Provider{}
	w := httptest.NewRecorder()
	p.ACKNotify(w)

	if got := w.Code; got != 200 {
		t.Fatalf("ACKNotify status = %d, want 200", got)
	}
	if got := w.Body.String(); got != "success" {
		t.Fatalf("ACKNotify body = %q, want %q", got, "success")
	}
}

// TestNewProviderRawPublicKeyOnlyReturnsErrNotSupported 验证「Option 路径下普通公钥模式软降级」。
// gopay v3 SDK 不支持普通公钥模式，NewProvider 应返回 ErrNotSupported 包装错误，
// 文案指引调用方升级到证书模式。
func TestNewProviderRawPublicKeyOnlyReturnsErrNotSupported(t *testing.T) {
	_, err := NewProvider(
		WithAppID("2021000000000001"),
		WithPrivateKey("dummy-private-key-pem-content"),
		WithAlipayPublicKey("dummy-alipay-public-key-pem-content"),
	)
	if err == nil {
		t.Fatal("NewProvider returned nil error for raw public key mode")
	}
	if !errors.Is(err, paymgr.ErrNotSupported) {
		t.Fatalf("NewProvider err = %v, want errors.Is(err, paymgr.ErrNotSupported)", err)
	}
}

// TestNewProviderWithConfigRawPublicKeyOnlyReturnsErrNotSupported 验证「Config struct 路径下普通公钥模式软降级」。
func TestNewProviderWithConfigRawPublicKeyOnlyReturnsErrNotSupported(t *testing.T) {
	_, err := NewProviderWithConfig(&Config{
		AppID:           "2021000000000001",
		PrivateKey:      "dummy-private-key-pem-content",
		AlipayPublicKey: "dummy-alipay-public-key-pem-content",
	})
	if err == nil {
		t.Fatal("NewProviderWithConfig returned nil error for raw public key mode")
	}
	if !errors.Is(err, paymgr.ErrNotSupported) {
		t.Fatalf("NewProviderWithConfig err = %v, want errors.Is(err, paymgr.ErrNotSupported)", err)
	}
}

func TestProviderChannelReturnsAlipay(t *testing.T) {
	p := &Provider{}
	if got := p.Channel(); got != paymgr.ChannelAlipay {
		t.Fatalf("Channel() = %q, want %q", got, paymgr.ChannelAlipay)
	}
}

func TestBuildAlipayCommonBodyMapsAllFields(t *testing.T) {
	bm := buildAlipayCommonBody(&paymgr.UnifiedOrderRequest{
		OutTradeNo: "ORD-1",
		Subject:    "iPhone 16",
		NotifyURL:  "https://api.example.com/notify",
	}, "12.34", "25m", "order_id=ORD-1")

	cases := map[string]string{
		"out_trade_no":    "ORD-1",
		"total_amount":    "12.34",
		"subject":         "iPhone 16",
		"notify_url":      "https://api.example.com/notify",
		"timeout_express": "25m",
		"passback_params": "order_id=ORD-1",
	}
	for key, want := range cases {
		if got := bm.GetString(key); got != want {
			t.Fatalf("bm[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestBuildAlipayCommonBodyOmitsEmptyOptionalFields(t *testing.T) {
	bm := buildAlipayCommonBody(&paymgr.UnifiedOrderRequest{
		OutTradeNo: "ORD-2",
		Subject:    "test",
	}, "1.00", "", "")

	for _, key := range []string{"notify_url", "timeout_express", "passback_params"} {
		if got := bm.GetString(key); got != "" {
			t.Fatalf("bm[%q] = %q, want empty (optional field)", key, got)
		}
	}
}

func TestAliRspErrorReturnsNilOn200(t *testing.T) {
	if err := aliRspError(200, alipayv3.ErrResponse{}); err != nil {
		t.Fatalf("aliRspError(200) = %v, want nil", err)
	}
}

func TestAliRspErrorWrapsNon200AsChannelError(t *testing.T) {
	err := aliRspError(400, alipayv3.ErrResponse{
		Code:    "INVALID_PARAMETER",
		Message: "out_trade_no missing",
	})
	if err == nil {
		t.Fatal("aliRspError(400) returned nil")
	}
	var chErr *paymgr.ChannelError
	if !errors.As(err, &chErr) {
		t.Fatalf("err = %T, want *paymgr.ChannelError", err)
	}
	if chErr.Channel != paymgr.ChannelAlipay {
		t.Fatalf("chErr.Channel = %q, want %q", chErr.Channel, paymgr.ChannelAlipay)
	}
	if chErr.Code != "INVALID_PARAMETER" {
		t.Fatalf("chErr.Code = %q, want %q", chErr.Code, "INVALID_PARAMETER")
	}
	if chErr.Message != "out_trade_no missing" {
		t.Fatalf("chErr.Message = %q, want %q", chErr.Message, "out_trade_no missing")
	}
}

func TestWrapAlipayErrorPassesNilThrough(t *testing.T) {
	if err := wrapAlipayError(nil); err != nil {
		t.Fatalf("wrapAlipayError(nil) = %v, want nil", err)
	}
}

func TestWrapAlipayErrorPreservesUnderlyingError(t *testing.T) {
	underlying := errors.New("network unreachable")
	wrapped := wrapAlipayError(underlying)
	if !errors.Is(wrapped, underlying) {
		t.Fatalf("errors.Is(wrapped, underlying) = false, want true")
	}
	var chErr *paymgr.ChannelError
	if !errors.As(wrapped, &chErr) {
		t.Fatalf("wrapped = %T, want *paymgr.ChannelError", wrapped)
	}
	if chErr.Code != "SDK_ERROR" {
		t.Fatalf("chErr.Code = %q, want %q", chErr.Code, "SDK_ERROR")
	}
}

func TestResolveSourceTakesValueWhenProvided(t *testing.T) {
	got, err := resolveSource("inline-content", "/some/ignored/path")
	if err != nil {
		t.Fatalf("resolveSource() error = %v", err)
	}
	if got != "inline-content" {
		t.Fatalf("resolveSource() = %q, want %q", got, "inline-content")
	}
}

func TestResolveSourceFallsBackToFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(tmp, []byte("file-content"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := resolveSource("", tmp)
	if err != nil {
		t.Fatalf("resolveSource() error = %v", err)
	}
	if got != "file-content" {
		t.Fatalf("resolveSource() = %q, want %q", got, "file-content")
	}
}

func TestResolveSourceErrorsWhenBothEmpty(t *testing.T) {
	if _, err := resolveSource("", ""); err == nil {
		t.Fatal("resolveSource(\"\", \"\") returned nil error")
	}
}

func TestResolveSourceBytesTakesValueWhenProvided(t *testing.T) {
	got, err := resolveSourceBytes("inline-bytes", "/some/ignored/path")
	if err != nil {
		t.Fatalf("resolveSourceBytes() error = %v", err)
	}
	if string(got) != "inline-bytes" {
		t.Fatalf("resolveSourceBytes() = %q, want %q", string(got), "inline-bytes")
	}
}

// TestUnifiedOrderUnsupportedTypeReturnsErrUnsupportedType 验证 trade type 兜底分支。
func TestUnifiedOrderUnsupportedTypeReturnsErrUnsupportedType(t *testing.T) {
	p := &Provider{}
	_, err := p.UnifiedOrder(context.Background(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-1",
		TotalAmount: 100,
		Subject:     "test",
		TradeType:   paymgr.TradeType("unknown"),
		NotifyURL:   "https://example.com/notify",
	})
	if err == nil {
		t.Fatal("UnifiedOrder returned nil error for unsupported trade type")
	}
	if !errors.Is(err, paymgr.ErrUnsupportedType) {
		t.Fatalf("UnifiedOrder err = %v, want errors.Is(err, paymgr.ErrUnsupportedType)", err)
	}
}
