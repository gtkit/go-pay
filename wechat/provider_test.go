package wechat

import (
	"errors"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

func TestParseTimeRFC3339(t *testing.T) {
	input := "2023-06-08T10:34:56+08:00"
	want, err := time.Parse(time.RFC3339, input)
	if err != nil {
		t.Fatalf("parse expected time: %v", err)
	}

	got := parseTime(input)
	if !got.Equal(want) {
		t.Fatalf("parseTime(%q) = %v, want %v", input, got, want)
	}
}

func TestParseTimeInvalidReturnsZero(t *testing.T) {
	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Fatalf("parseTime returned %v, want zero time", got)
	}
}

func TestMapWechatRefundStatus(t *testing.T) {
	success := refunddomestic.STATUS_SUCCESS
	closed := refunddomestic.STATUS_CLOSED
	processing := refunddomestic.STATUS_PROCESSING
	abnormal := refunddomestic.STATUS_ABNORMAL

	tests := []struct {
		name string
		in   *refunddomestic.Status
		want paymgr.RefundStatus
	}{
		{name: "nil", in: nil, want: paymgr.RefundStatusError},
		{name: "success", in: &success, want: paymgr.RefundStatusSuccess},
		{name: "closed", in: &closed, want: paymgr.RefundStatusClosed},
		{name: "processing", in: &processing, want: paymgr.RefundStatusProcessing},
		{name: "abnormal", in: &abnormal, want: paymgr.RefundStatusAbnormal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapWechatRefundStatus(tt.in); got != tt.want {
				t.Fatalf("mapWechatRefundStatus(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMapWechatRefundStatusString(t *testing.T) {
	tests := map[string]paymgr.RefundStatus{
		"SUCCESS":    paymgr.RefundStatusSuccess,
		"CLOSED":     paymgr.RefundStatusClosed,
		"PROCESSING": paymgr.RefundStatusProcessing,
		"ABNORMAL":   paymgr.RefundStatusAbnormal,
		"":           paymgr.RefundStatusError,
		"UNKNOWN":    paymgr.RefundStatusError,
	}
	for in, want := range tests {
		if got := mapWechatRefundStatusString(in); got != want {
			t.Fatalf("mapWechatRefundStatusString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrapWechatErrorPreservesAPIErrorCode(t *testing.T) {
	err := &core.APIError{
		Code:    "ORDERNOTEXIST",
		Message: "order does not exist",
	}

	wrapped := wrapWechatError(err)

	chErr, ok := errors.AsType[*paymgr.ChannelError](wrapped)
	if !ok {
		t.Fatalf("wrapWechatError(%v) did not return ChannelError", err)
	}
	if chErr.Code != "ORDERNOTEXIST" {
		t.Fatalf("ChannelError.Code = %q, want %q", chErr.Code, "ORDERNOTEXIST")
	}
	if chErr.Message != "order does not exist" {
		t.Fatalf("ChannelError.Message = %q, want %q", chErr.Message, "order does not exist")
	}
}
