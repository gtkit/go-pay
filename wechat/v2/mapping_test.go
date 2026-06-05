package v2

import (
	"testing"

	"github.com/gtkit/go-pay/paymgr"
)

func TestMapTradeType(t *testing.T) {
	tests := []struct {
		in      paymgr.TradeType
		want    string
		wantErr bool
	}{
		{paymgr.TradeTypeApp, "APP", false},
		{paymgr.TradeTypeJSAPI, "JSAPI", false},
		{paymgr.TradeTypeNative, "NATIVE", false},
		{paymgr.TradeTypeH5, "MWEB", false},
		{paymgr.TradeTypePage, "", true},
	}
	for _, tt := range tests {
		got, err := mapTradeType(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("mapTradeType(%q) = (%q, %v), want (%q, err=%v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestMapTradeState(t *testing.T) {
	tests := map[string]paymgr.TradeStatus{
		"SUCCESS":    paymgr.TradeStatusPaid,
		"NOTPAY":     paymgr.TradeStatusPending,
		"USERPAYING": paymgr.TradeStatusPending,
		"CLOSED":     paymgr.TradeStatusClosed,
		"REVOKED":    paymgr.TradeStatusClosed,
		"PAYERROR":   paymgr.TradeStatusClosed,
		"REFUND":     paymgr.TradeStatusRefunded,
		"UNKNOWN":    paymgr.TradeStatusError,
	}
	for in, want := range tests {
		if got := mapTradeState(in); got != want {
			t.Errorf("mapTradeState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapRefundStatus(t *testing.T) {
	tests := map[string]paymgr.RefundStatus{
		"SUCCESS":     paymgr.RefundStatusSuccess,
		"PROCESSING":  paymgr.RefundStatusProcessing,
		"REFUNDCLOSE": paymgr.RefundStatusClosed,
		"CHANGE":      paymgr.RefundStatusAbnormal,
		"WHATEVER":    paymgr.RefundStatusError,
	}
	for in, want := range tests {
		if got := mapRefundStatus(in); got != want {
			t.Errorf("mapRefundStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotifySignType(t *testing.T) {
	if got := notifySignType(map[string]string{"sign_type": "HMAC-SHA256"}); got != SignTypeHMACSHA256 {
		t.Errorf("notifySignType with field = %q, want HMAC-SHA256", got)
	}
	// 未声明 sign_type 时回退 MD5（贴合微信通知历史行为）
	if got := notifySignType(map[string]string{}); got != SignTypeMD5 {
		t.Errorf("notifySignType fallback = %q, want MD5", got)
	}
}

func TestFindRefundIndex(t *testing.T) {
	m := map[string]string{
		"refund_count":    "2",
		"out_refund_no_0": "R-A",
		"out_refund_no_1": "R-B",
	}
	if got := findRefundIndex(m, "R-B"); got != "1" {
		t.Errorf("findRefundIndex = %q, want 1", got)
	}
	if got := findRefundIndex(m, "R-NOTFOUND"); got != "0" {
		t.Errorf("findRefundIndex fallback = %q, want 0", got)
	}
}
