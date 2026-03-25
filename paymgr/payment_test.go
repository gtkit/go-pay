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
