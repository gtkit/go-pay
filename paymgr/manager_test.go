package paymgr

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

type stubProvider struct {
	ch Channel
}

func (p stubProvider) Channel() Channel {
	return p.ch
}

func (p stubProvider) UnifiedOrder(context.Context, *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
	return nil, nil
}

func (p stubProvider) QueryOrder(context.Context, *QueryOrderRequest) (*QueryOrderResponse, error) {
	return nil, nil
}

func (p stubProvider) CloseOrder(context.Context, *CloseOrderRequest) error {
	return nil
}

func (p stubProvider) Refund(context.Context, *RefundRequest) (*RefundResponse, error) {
	return nil, nil
}

func (p stubProvider) QueryRefund(context.Context, *QueryRefundRequest) (*QueryRefundResponse, error) {
	return nil, nil
}

func (p stubProvider) ParseNotify(context.Context, *http.Request) (*NotifyResult, error) {
	return nil, nil
}

func (p stubProvider) ParseRefundNotify(context.Context, *http.Request) (*RefundNotifyResult, error) {
	return nil, nil
}

func (p stubProvider) ACKNotify(http.ResponseWriter) {}

func TestManagerCloseOrderNilRequest(t *testing.T) {
	mgr := NewManager()

	err := mgr.CloseOrder(t.Context(), ChannelWechat, nil)
	if err == nil {
		t.Fatal("CloseOrder() returned nil error")
	}
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("CloseOrder() error = %v, want wrapped ErrInvalidParam", err)
	}
}

func TestManagerConcurrentRegisterAndLookup(t *testing.T) {
	mgr := NewManager()
	providers := []Provider{
		stubProvider{ch: ChannelWechat},
		stubProvider{ch: ChannelAlipay},
	}

	var wg sync.WaitGroup
	for i := range 32 {
		provider := providers[i%len(providers)]
		wg.Go(func() {
			for range 256 {
				mgr.Register(provider)
				if _, err := mgr.Provider(provider.Channel()); err != nil {
					t.Errorf("Provider(%q) error = %v", provider.Channel(), err)
				}
				_ = mgr.Channels()
			}
		})
	}
	wg.Wait()
}
