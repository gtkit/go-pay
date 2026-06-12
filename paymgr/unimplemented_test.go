package paymgr

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// orderOnlyProvider 模拟只支持下单能力的自定义渠道：
// 嵌入 UnimplementedProvider，仅覆写 Channel 与 UnifiedOrder。
type orderOnlyProvider struct {
	UnimplementedProvider
}

func (p *orderOnlyProvider) Channel() Channel { return "custom-order-only" }

func (p *orderOnlyProvider) UnifiedOrder(context.Context, *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
	return &UnifiedOrderResponse{Channel: "custom-order-only"}, nil
}

// 编译期断言：嵌入基座 + Channel + 部分覆写即满足完整 Provider。
var _ Provider = (*orderOnlyProvider)(nil)

// 编译期断言：完整 Provider 可按能力小接口使用。
var (
	_ OrderProvider  = (Provider)(nil)
	_ RefundProvider = (Provider)(nil)
	_ NotifyParser   = (Provider)(nil)
)

func TestUnimplementedProviderDefaults(t *testing.T) {
	p := &orderOnlyProvider{}
	ctx := t.Context()

	// 覆写的方法正常工作
	resp, err := p.UnifiedOrder(ctx, &UnifiedOrderRequest{})
	if err != nil || resp == nil {
		t.Fatalf("UnifiedOrder() = (%v, %v), want overridden implementation", resp, err)
	}

	// 未覆写的方法返回含方法名的 ErrNotSupported
	tests := []struct {
		method string
		call   func() error
	}{
		{"QueryOrder", func() error { _, err := p.QueryOrder(ctx, nil); return err }},
		{"CloseOrder", func() error { return p.CloseOrder(ctx, nil) }},
		{"Refund", func() error { _, err := p.Refund(ctx, nil); return err }},
		{"QueryRefund", func() error { _, err := p.QueryRefund(ctx, nil); return err }},
		{"ParseNotify", func() error { _, err := p.ParseNotify(ctx, nil); return err }},
		{"ParseRefundNotify", func() error { _, err := p.ParseRefundNotify(ctx, nil); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("%s() error = %v, want wrapped ErrNotSupported", tt.method, err)
			}
			if !strings.Contains(err.Error(), tt.method) {
				t.Fatalf("%s() error = %v, want method name in message", tt.method, err)
			}
		})
	}
}

func TestUnimplementedProviderACKNotifyNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	(&orderOnlyProvider{}).ACKNotify(rec)
	if rec.Body.Len() != 0 {
		t.Fatalf("ACKNotify() wrote %q, want no output", rec.Body.String())
	}
}

func TestUnimplementedProviderViaManager(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&orderOnlyProvider{})

	// 注册后未覆写的能力经 Manager 调用同样返回 ErrNotSupported
	_, err := mgr.Refund(t.Context(), "custom-order-only", &RefundRequest{
		OutTradeNo: "ORD-1", OutRefundNo: "R-1", RefundAmount: 1, TotalAmount: 1,
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Refund() error = %v, want wrapped ErrNotSupported", err)
	}
}
