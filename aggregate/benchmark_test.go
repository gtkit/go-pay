package aggregate_test

import (
	"context"
	"testing"

	"github.com/gtkit/go-pay/aggregate"
	"github.com/gtkit/go-pay/paymgr"
)

func BenchmarkDetectEnv(b *testing.B) {
	userAgent := "Mozilla/5.0 AlipayClient/10.5.96 Mobile"

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = aggregate.DetectEnv(userAgent)
	}
}

func BenchmarkServiceResolve(b *testing.B) {
	svc := newExampleService()
	ctx := context.Background()

	b.Run("choose_channel", func(b *testing.B) {
		req := &aggregate.ResolveRequest{
			UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		}

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, err := svc.Resolve(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("create_order", func(b *testing.B) {
		req := &aggregate.ResolveRequest{
			UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			SelectedChannel: paymgr.ChannelAlipay,
			BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
				return buildExampleOrder(ch, tradeType)
			},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, err := svc.Resolve(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("create_order_parallel", func(b *testing.B) {
		req := &aggregate.ResolveRequest{
			UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			SelectedChannel: paymgr.ChannelAlipay,
			BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
				return buildExampleOrder(ch, tradeType)
			},
		}

		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := svc.Resolve(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}
