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

func TestUnifiedOrderRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     *UnifiedOrderRequest
		wantErr error
	}{
		{name: "nil request", req: nil, wantErr: ErrInvalidParam},
		{name: "missing out_trade_no", req: &UnifiedOrderRequest{
			TotalAmount: 100,
			Subject:     "order",
			NotifyURL:   "https://example.com/notify",
		}, wantErr: ErrInvalidParam},
		{name: "zero total_amount", req: &UnifiedOrderRequest{
			OutTradeNo: "ORD-1",
			Subject:    "order",
			NotifyURL:  "https://example.com/notify",
		}, wantErr: ErrInvalidParam},
		{name: "negative total_amount", req: &UnifiedOrderRequest{
			OutTradeNo:  "ORD-1",
			TotalAmount: -1,
			Subject:     "order",
			NotifyURL:   "https://example.com/notify",
		}, wantErr: ErrInvalidParam},
		{name: "missing subject", req: &UnifiedOrderRequest{
			OutTradeNo:  "ORD-1",
			TotalAmount: 100,
			NotifyURL:   "https://example.com/notify",
		}, wantErr: ErrInvalidParam},
		{name: "missing notify_url", req: &UnifiedOrderRequest{
			OutTradeNo:  "ORD-1",
			TotalAmount: 100,
			Subject:     "order",
		}, wantErr: ErrInvalidParam},
		{name: "valid", req: &UnifiedOrderRequest{
			OutTradeNo:  "ORD-1",
			TotalAmount: 100,
			Subject:     "order",
			TradeType:   TradeTypeNative,
			NotifyURL:   "https://example.com/notify",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestQueryOrderRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     *QueryOrderRequest
		wantErr error
	}{
		{name: "nil request", req: nil, wantErr: ErrInvalidParam},
		{name: "both identifiers empty", req: &QueryOrderRequest{}, wantErr: ErrInvalidParam},
		{name: "out_trade_no only", req: &QueryOrderRequest{OutTradeNo: "ORD-1"}},
		{name: "transaction_id only", req: &QueryOrderRequest{TransactionID: "TX-1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestCloseOrderRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     *CloseOrderRequest
		wantErr error
	}{
		{name: "nil request", req: nil, wantErr: ErrInvalidParam},
		{name: "missing out_trade_no", req: &CloseOrderRequest{}, wantErr: ErrInvalidParam},
		{name: "valid", req: &CloseOrderRequest{OutTradeNo: "ORD-1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestRefundRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     *RefundRequest
		wantErr error
	}{
		{name: "nil request", req: nil, wantErr: ErrInvalidParam},
		{name: "missing order identifiers", req: &RefundRequest{
			OutRefundNo:  "RF-1",
			RefundAmount: 50,
			TotalAmount:  100,
		}, wantErr: ErrInvalidParam},
		{name: "missing out_refund_no", req: &RefundRequest{
			OutTradeNo:   "ORD-1",
			RefundAmount: 50,
			TotalAmount:  100,
		}, wantErr: ErrInvalidParam},
		{name: "zero refund_amount", req: &RefundRequest{
			OutTradeNo:  "ORD-1",
			OutRefundNo: "RF-1",
			TotalAmount: 100,
		}, wantErr: ErrInvalidParam},
		{name: "zero total_amount", req: &RefundRequest{
			OutTradeNo:   "ORD-1",
			OutRefundNo:  "RF-1",
			RefundAmount: 50,
		}, wantErr: ErrInvalidParam},
		{name: "refund exceeds total", req: &RefundRequest{
			OutTradeNo:   "ORD-1",
			OutRefundNo:  "RF-1",
			RefundAmount: 200,
			TotalAmount:  100,
		}, wantErr: ErrInvalidParam},
		{name: "valid with out_trade_no", req: &RefundRequest{
			OutTradeNo:   "ORD-1",
			OutRefundNo:  "RF-1",
			RefundAmount: 50,
			TotalAmount:  100,
		}},
		{name: "valid with transaction_id", req: &RefundRequest{
			TransactionID: "TX-1",
			OutRefundNo:   "RF-1",
			RefundAmount:  100,
			TotalAmount:   100,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestQueryRefundRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     *QueryRefundRequest
		wantErr error
	}{
		{name: "nil request", req: nil, wantErr: ErrInvalidParam},
		{name: "missing out_refund_no", req: &QueryRefundRequest{OutTradeNo: "ORD-1"}, wantErr: ErrInvalidParam},
		{name: "valid", req: &QueryRefundRequest{OutRefundNo: "RF-1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestChannelWechatV2ConstantUniqueAndTyped(t *testing.T) {
	if ChannelWechatV2 != "wxpayv2" {
		t.Fatalf("ChannelWechatV2 = %q, want %q", ChannelWechatV2, "wxpayv2")
	}
	channels := map[Channel]string{
		ChannelWechat:   "ChannelWechat",
		ChannelAlipay:   "ChannelAlipay",
		ChannelWechatV2: "ChannelWechatV2",
	}
	if len(channels) != 3 {
		t.Fatalf("channel constants collide: %+v", channels)
	}
}
