package wechat

import (
	"errors"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
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
