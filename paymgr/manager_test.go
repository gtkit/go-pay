package paymgr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

func TestManagerUnifiedOrderUnregisteredWechatV2(t *testing.T) {
	mgr := NewManager()
	req := &UnifiedOrderRequest{
		OutTradeNo:  "ORD-V2-1",
		TotalAmount: 100,
		Subject:     "v2 placeholder",
		TradeType:   TradeTypeJSAPI,
		NotifyURL:   "https://example.com/notify",
		OpenID:      "openid-stub",
	}

	_, err := mgr.UnifiedOrder(t.Context(), ChannelWechatV2, req)
	if err == nil {
		t.Fatal("UnifiedOrder() returned nil error for unregistered ChannelWechatV2")
	}
	if !errors.Is(err, ErrChannelNotRegistered) {
		t.Fatalf("UnifiedOrder() error = %v, want errors.Is ErrChannelNotRegistered", err)
	}
	if !strings.Contains(err.Error(), "wxpayv2") {
		t.Fatalf("UnifiedOrder() error = %v, want contains %q", err, "wxpayv2")
	}
}

func TestManagerZeroValueRegister(t *testing.T) {
	var m Manager
	m.Register(stubProvider{ch: ChannelWechat})

	p, err := m.Provider(ChannelWechat)
	if err != nil {
		t.Fatalf("Provider() error = %v, want nil", err)
	}
	if p.Channel() != ChannelWechat {
		t.Fatalf("Channel() = %q, want %q", p.Channel(), ChannelWechat)
	}
}

func TestManagerRegisterNilProvider(t *testing.T) {
	mgr := NewManager()

	mgr.Register(nil)
	if got := mgr.Channels(); len(got) != 0 {
		t.Fatalf("Channels() = %v after Register(nil), want empty", got)
	}

	var typedNil *stubProvider
	mgr.Register(typedNil)
	if got := mgr.Channels(); len(got) != 0 {
		t.Fatalf("Channels() = %v after Register(typed nil), want empty", got)
	}
}

func TestManagerProviderUnregistered(t *testing.T) {
	mgr := NewManager()

	_, err := mgr.Provider(ChannelAlipay)
	if !errors.Is(err, ErrChannelNotRegistered) {
		t.Fatalf("Provider() error = %v, want errors.Is ErrChannelNotRegistered", err)
	}
	if !strings.Contains(err.Error(), string(ChannelAlipay)) {
		t.Fatalf("Provider() error = %v, want contains %q", err, ChannelAlipay)
	}
}

func TestManagerDeregister(t *testing.T) {
	mgr := NewManager()
	mgr.Register(stubProvider{ch: ChannelWechat})

	if !mgr.Deregister(ChannelWechat) {
		t.Fatal("Deregister() = false for registered channel, want true")
	}
	if mgr.Deregister(ChannelWechat) {
		t.Fatal("Deregister() = true for absent channel, want false")
	}
	if _, err := mgr.Provider(ChannelWechat); !errors.Is(err, ErrChannelNotRegistered) {
		t.Fatalf("Provider() after Deregister error = %v, want ErrChannelNotRegistered", err)
	}
}

func TestManagerChannels(t *testing.T) {
	var empty Manager
	if got := empty.Channels(); len(got) != 0 {
		t.Fatalf("Channels() = %v for zero-value Manager, want empty", got)
	}

	mgr := NewManager()
	mgr.Register(stubProvider{ch: ChannelWechat})
	mgr.Register(stubProvider{ch: ChannelAlipay})

	got := mgr.Channels()
	if len(got) != 2 || !slices.Contains(got, ChannelWechat) || !slices.Contains(got, ChannelAlipay) {
		t.Fatalf("Channels() = %v, want [wxpay alipay]", got)
	}
}

func TestManagerProxyMethods(t *testing.T) {
	tests := []struct {
		name string
		// valid 用合法请求调用代理方法
		valid func(ctx context.Context, m *Manager, ch Channel) error
		// invalid 用非法请求调用代理方法；nil 表示该方法没有请求校验
		invalid func(ctx context.Context, m *Manager, ch Channel) error
	}{
		{
			name: "QueryOrder",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.QueryOrder(ctx, ch, &QueryOrderRequest{OutTradeNo: "ORD-1"})
				return err
			},
			invalid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.QueryOrder(ctx, ch, nil)
				return err
			},
		},
		{
			name: "CloseOrder",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				return m.CloseOrder(ctx, ch, &CloseOrderRequest{OutTradeNo: "ORD-1"})
			},
			invalid: func(ctx context.Context, m *Manager, ch Channel) error {
				return m.CloseOrder(ctx, ch, &CloseOrderRequest{})
			},
		},
		{
			name: "Refund",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.Refund(ctx, ch, &RefundRequest{
					OutTradeNo:   "ORD-1",
					OutRefundNo:  "RF-1",
					RefundAmount: 50,
					TotalAmount:  100,
				})
				return err
			},
			invalid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.Refund(ctx, ch, nil)
				return err
			},
		},
		{
			name: "QueryRefund",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.QueryRefund(ctx, ch, &QueryRefundRequest{OutRefundNo: "RF-1"})
				return err
			},
			invalid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.QueryRefund(ctx, ch, &QueryRefundRequest{})
				return err
			},
		},
		{
			name: "ParseNotify",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.ParseNotify(ctx, ch, httptest.NewRequest(http.MethodPost, "/notify", nil))
				return err
			},
		},
		{
			name: "ParseRefundNotify",
			valid: func(ctx context.Context, m *Manager, ch Channel) error {
				_, err := m.ParseRefundNotify(ctx, ch, httptest.NewRequest(http.MethodPost, "/refund-notify", nil))
				return err
			},
		},
		{
			name: "ACKNotify",
			valid: func(_ context.Context, m *Manager, ch Channel) error {
				return m.ACKNotify(ch, httptest.NewRecorder())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManager()
			mgr.Register(stubProvider{ch: ChannelWechat})

			if err := tt.valid(t.Context(), mgr, ChannelWechat); err != nil {
				t.Fatalf("%s() error = %v, want nil", tt.name, err)
			}
			if err := tt.valid(t.Context(), mgr, ChannelAlipay); !errors.Is(err, ErrChannelNotRegistered) {
				t.Fatalf("%s() unregistered error = %v, want errors.Is ErrChannelNotRegistered", tt.name, err)
			}
			if tt.invalid != nil {
				if err := tt.invalid(t.Context(), mgr, ChannelWechat); !errors.Is(err, ErrInvalidParam) {
					t.Fatalf("%s() invalid request error = %v, want wrapped ErrInvalidParam", tt.name, err)
				}
			}
		})
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
