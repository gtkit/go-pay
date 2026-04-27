package paymgr

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type contractProvider struct {
	ch           Channel
	calls        int
	unifiedOrder func(context.Context, *UnifiedOrderRequest) (*UnifiedOrderResponse, error)
}

func (p *contractProvider) Channel() Channel {
	return p.ch
}

func (p *contractProvider) UnifiedOrder(ctx context.Context, req *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
	p.calls++
	if p.unifiedOrder != nil {
		return p.unifiedOrder(ctx, req)
	}
	return &UnifiedOrderResponse{Channel: p.ch}, nil
}

func (p *contractProvider) QueryOrder(context.Context, *QueryOrderRequest) (*QueryOrderResponse, error) {
	return nil, nil
}

func (p *contractProvider) CloseOrder(context.Context, *CloseOrderRequest) error {
	return nil
}

func (p *contractProvider) Refund(context.Context, *RefundRequest) (*RefundResponse, error) {
	return nil, nil
}

func (p *contractProvider) QueryRefund(context.Context, *QueryRefundRequest) (*QueryRefundResponse, error) {
	return nil, nil
}

func (p *contractProvider) ParseNotify(context.Context, *http.Request) (*NotifyResult, error) {
	return nil, nil
}

func (p *contractProvider) ParseRefundNotify(context.Context, *http.Request) (*RefundNotifyResult, error) {
	return nil, nil
}

func (p *contractProvider) ACKNotify(http.ResponseWriter) {}

func TestManagerUnifiedOrderValidatesBeforeProvider(t *testing.T) {
	mgr := NewManager()
	provider := &contractProvider{ch: ChannelAlipay}
	mgr.Register(provider)

	tests := []struct {
		name string
		req  *UnifiedOrderRequest
	}{
		{name: "nil_request", req: nil},
		{name: "empty_request", req: &UnifiedOrderRequest{}},
		{name: "non_positive_amount", req: &UnifiedOrderRequest{
			OutTradeNo:  "ORD-CONTRACT-1",
			TotalAmount: 0,
			Subject:     "contract order",
			TradeType:   TradeTypePage,
			NotifyURL:   "https://example.com/notify",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider.calls = 0

			_, err := mgr.UnifiedOrder(t.Context(), ChannelAlipay, tt.req)
			if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("UnifiedOrder() error = %v, want wrapped ErrInvalidParam", err)
			}
			if provider.calls != 0 {
				t.Fatalf("provider calls = %d, want 0", provider.calls)
			}
		})
	}
}

func TestManagerUnifiedOrderRequiresRegisteredProvider(t *testing.T) {
	mgr := NewManager()

	_, err := mgr.UnifiedOrder(t.Context(), ChannelAlipay, validContractOrder(TradeTypePage))
	if err == nil {
		t.Fatal("UnifiedOrder() error = nil, want missing provider error")
	}
}

func TestManagerUnifiedOrderDispatchesAndPropagatesProviderError(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &contractProvider{
		ch: ChannelAlipay,
		unifiedOrder: func(_ context.Context, req *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
			if req.TradeType != TradeTypePage {
				t.Fatalf("TradeType = %q, want %q", req.TradeType, TradeTypePage)
			}
			return nil, wantErr
		},
	}
	mgr := NewManager()
	mgr.Register(provider)

	_, err := mgr.UnifiedOrder(t.Context(), ChannelAlipay, validContractOrder(TradeTypePage))
	if !errors.Is(err, wantErr) {
		t.Fatalf("UnifiedOrder() error = %v, want provider error", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestManagerUnifiedOrderReturnsProviderResponse(t *testing.T) {
	provider := &contractProvider{
		ch: ChannelWechat,
		unifiedOrder: func(context.Context, *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
			return &UnifiedOrderResponse{
				Channel: ChannelWechat,
				CodeURL: "weixin://wxpay/bizpayurl?pr=test",
			}, nil
		},
	}
	mgr := NewManager()
	mgr.Register(provider)

	resp, err := mgr.UnifiedOrder(t.Context(), ChannelWechat, validContractOrder(TradeTypeNative))
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v, want nil", err)
	}
	if resp.Channel != ChannelWechat || resp.CodeURL == "" {
		t.Fatalf("UnifiedOrder() response = %+v, want wechat code_url", resp)
	}
}

func validContractOrder(tradeType TradeType) *UnifiedOrderRequest {
	return &UnifiedOrderRequest{
		OutTradeNo:  "ORD-CONTRACT-1",
		TotalAmount: 100,
		Subject:     "contract order",
		TradeType:   tradeType,
		NotifyURL:   "https://example.com/notify",
	}
}
