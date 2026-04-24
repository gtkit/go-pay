package aggregate

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gtkit/go-pay/paymgr"
)

func TestDetectEnv(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want Env
	}{
		{name: "wechat", ua: "Mozilla/5.0 MicroMessenger/8.0.49", want: EnvWechat},
		{name: "alipay", ua: "Mozilla/5.0 AlipayClient/10.5.96", want: EnvAlipay},
		{name: "browser pc", ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36", want: EnvBrowserPC},
		{name: "browser mobile", ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604.1", want: EnvBrowserMobile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectEnv(tt.ua); got != tt.want {
				t.Fatalf("DetectEnv(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

func TestResolveChooseChannelForBrowserPCWithoutSelection(t *testing.T) {
	svc := NewService(paymgr.NewManager())

	result, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if result.Action != ActionChooseChannel {
		t.Fatalf("Action = %q, want %q", result.Action, ActionChooseChannel)
	}
	if result.Env != EnvBrowserPC {
		t.Fatalf("Env = %q, want %q", result.Env, EnvBrowserPC)
	}
}

func TestResolveRequiresBuilderWhenOrderMustBeCreated(t *testing.T) {
	svc := NewService(paymgr.NewManager())

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:       "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/124.0 Mobile Safari/537.36",
		SelectedChannel: paymgr.ChannelAlipay,
	})
	if !errors.Is(err, ErrMissingOrderBuilder) {
		t.Fatalf("Resolve() error = %v, want ErrMissingOrderBuilder", err)
	}
}

type recordingProvider struct {
	ch           paymgr.Channel
	unifiedOrder func(*paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error)
}

func (p recordingProvider) Channel() paymgr.Channel {
	return p.ch
}

func (p recordingProvider) UnifiedOrder(_ context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
	if p.unifiedOrder != nil {
		return p.unifiedOrder(req)
	}

	resp := &paymgr.UnifiedOrderResponse{Channel: p.ch}
	switch req.TradeType {
	case paymgr.TradeTypeJSAPI:
		resp.JSAPIParams = `{"appId":"wx123"}`
	case paymgr.TradeTypeNative:
		resp.CodeURL = "weixin://wxpay/bizpayurl?pr=test"
	case paymgr.TradeTypeH5:
		if p.ch == paymgr.ChannelWechat {
			resp.H5URL = "https://wx.example.com/h5"
		} else {
			resp.PayURL = "https://open.alipay.com/h5"
		}
	case paymgr.TradeTypePage:
		resp.PayURL = "https://open.alipay.com/page"
	}
	return resp, nil
}

func (p recordingProvider) QueryOrder(context.Context, *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	return nil, nil
}

func (p recordingProvider) CloseOrder(context.Context, *paymgr.CloseOrderRequest) error {
	return nil
}

func (p recordingProvider) Refund(context.Context, *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	return nil, nil
}

func (p recordingProvider) QueryRefund(context.Context, *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	return nil, nil
}

func (p recordingProvider) ParseNotify(context.Context, *http.Request) (*paymgr.NotifyResult, error) {
	return nil, nil
}

func (p recordingProvider) ParseRefundNotify(context.Context, *http.Request) (*paymgr.RefundNotifyResult, error) {
	return nil, nil
}

func (p recordingProvider) ACKNotify(http.ResponseWriter) {}

func newTestService(providers ...recordingProvider) *Service {
	mgr := paymgr.NewManager()
	if len(providers) == 0 {
		providers = []recordingProvider{
			{ch: paymgr.ChannelWechat},
			{ch: paymgr.ChannelAlipay},
		}
	}
	for _, provider := range providers {
		mgr.Register(provider)
	}
	return NewService(mgr)
}

func TestResolveDecisionMatrix(t *testing.T) {
	tests := []struct {
		name        string
		userAgent   string
		selected    paymgr.Channel
		openID      string
		wantEnv     Env
		wantAction  Action
		wantChannel paymgr.Channel
		wantTrade   paymgr.TradeType
	}{
		{
			name:        "wechat jsapi",
			userAgent:   "Mozilla/5.0 MicroMessenger/8.0.49",
			openID:      "openid-123",
			wantEnv:     EnvWechat,
			wantAction:  ActionJSAPI,
			wantChannel: paymgr.ChannelWechat,
			wantTrade:   paymgr.TradeTypeJSAPI,
		},
		{
			name:        "alipay mobile h5",
			userAgent:   "Mozilla/5.0 AlipayClient/10.5.96 Mobile",
			wantEnv:     EnvAlipay,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypeH5,
		},
		{
			name:        "alipay pc page",
			userAgent:   "Mozilla/5.0 AlipayClient/10.5.96",
			wantEnv:     EnvAlipay,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypePage,
		},
		{
			name:        "browser pc wechat native",
			userAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			selected:    paymgr.ChannelWechat,
			wantEnv:     EnvBrowserPC,
			wantAction:  ActionQRCode,
			wantChannel: paymgr.ChannelWechat,
			wantTrade:   paymgr.TradeTypeNative,
		},
		{
			name:        "browser pc alipay page",
			userAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			selected:    paymgr.ChannelAlipay,
			wantEnv:     EnvBrowserPC,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypePage,
		},
		{
			name:        "browser mobile wechat h5",
			userAgent:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604.1",
			selected:    paymgr.ChannelWechat,
			wantEnv:     EnvBrowserMobile,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelWechat,
			wantTrade:   paymgr.TradeTypeH5,
		},
		{
			name:        "browser mobile alipay h5",
			userAgent:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604.1",
			selected:    paymgr.ChannelAlipay,
			wantEnv:     EnvBrowserMobile,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypeH5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			var gotChannel paymgr.Channel
			var gotTradeType paymgr.TradeType

			result, err := svc.Resolve(t.Context(), &ResolveRequest{
				UserAgent:       tt.userAgent,
				SelectedChannel: tt.selected,
				OpenID:          tt.openID,
				BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
					gotChannel = ch
					gotTradeType = tradeType
					return &paymgr.UnifiedOrderRequest{
						OutTradeNo:  "ORD-AGG-1",
						TotalAmount: 100,
						Subject:     "aggregate order",
						TradeType:   tradeType,
						NotifyURL:   "https://example.com/notify",
						ReturnURL:   "https://example.com/return",
						ClientIP:    "203.0.113.10",
						OpenID:      tt.openID,
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("Resolve() error = %v, want nil", err)
			}
			if gotChannel != tt.wantChannel || gotTradeType != tt.wantTrade {
				t.Fatalf("builder got (%q, %q), want (%q, %q)", gotChannel, gotTradeType, tt.wantChannel, tt.wantTrade)
			}
			if result.Env != tt.wantEnv {
				t.Fatalf("Env = %q, want %q", result.Env, tt.wantEnv)
			}
			if result.Action != tt.wantAction {
				t.Fatalf("Action = %q, want %q", result.Action, tt.wantAction)
			}
			if result.Channel != tt.wantChannel {
				t.Fatalf("Channel = %q, want %q", result.Channel, tt.wantChannel)
			}
			if result.TradeType != tt.wantTrade {
				t.Fatalf("TradeType = %q, want %q", result.TradeType, tt.wantTrade)
			}
		})
	}
}

func TestResolveValidationErrors(t *testing.T) {
	svc := newTestService()
	builder := func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
		return &paymgr.UnifiedOrderRequest{
			OutTradeNo:  "ORD-AGG-ERR",
			TotalAmount: 100,
			Subject:     "aggregate order",
			TradeType:   tradeType,
			NotifyURL:   "https://example.com/notify",
			ReturnURL:   "https://example.com/return",
			ClientIP:    "203.0.113.10",
		}, nil
	}

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:         "Mozilla/5.0 MicroMessenger/8.0.49",
		BuildUnifiedOrder: builder,
	})
	if !errors.Is(err, ErrMissingOpenID) {
		t.Fatalf("Resolve() error = %v, want ErrMissingOpenID", err)
	}

	_, err = svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		SelectedChannel:   paymgr.Channel("unionpay"),
		BuildUnifiedOrder: builder,
	})
	if !errors.Is(err, ErrInvalidChannelSelection) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidChannelSelection", err)
	}
}

func TestResolvePropagatesBuilderError(t *testing.T) {
	svc := newTestService()
	wantErr := errors.New("build order failed")

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		SelectedChannel: paymgr.ChannelAlipay,
		BuildUnifiedOrder: func(paymgr.Channel, paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
			return nil, wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resolve() error = %v, want builder error", err)
	}
}

func TestResolvePropagatesUnifiedOrderError(t *testing.T) {
	wantErr := errors.New("unified order failed")
	svc := newTestService(
		recordingProvider{ch: paymgr.ChannelWechat},
		recordingProvider{
			ch: paymgr.ChannelAlipay,
			unifiedOrder: func(*paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
				return nil, wantErr
			},
		},
	)

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		SelectedChannel: paymgr.ChannelAlipay,
		BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
			return &paymgr.UnifiedOrderRequest{
				OutTradeNo:  "ORD-AGG-ERR",
				TotalAmount: 100,
				Subject:     "aggregate order",
				TradeType:   tradeType,
				NotifyURL:   "https://example.com/notify",
				ReturnURL:   "https://example.com/return",
				ClientIP:    "203.0.113.10",
			}, nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resolve() error = %v, want unified order error", err)
	}
}
