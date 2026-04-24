package paymgr

import (
	"errors"
	"testing"
)

func TestUnifiedOrderValidateWrapsInvalidParam(t *testing.T) {
	req := &UnifiedOrderRequest{}

	err := req.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil error")
	}
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Validate() error = %v, want wrapped ErrInvalidParam", err)
	}
}

func TestRefundValidateWrapsInvalidParam(t *testing.T) {
	req := &RefundRequest{}

	err := req.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil error")
	}
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Validate() error = %v, want wrapped ErrInvalidParam", err)
	}
}

func TestTradeTypePageConstant(t *testing.T) {
	if TradeTypePage != "page" {
		t.Fatalf("TradeTypePage = %q, want %q", TradeTypePage, "page")
	}
}

func TestUnifiedOrderValidateAllowsTradeTypePage(t *testing.T) {
	req := &UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 100,
		Subject:     "PC page order",
		TradeType:   TradeTypePage,
		NotifyURL:   "https://example.com/notify",
		ReturnURL:   "https://example.com/return",
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}
