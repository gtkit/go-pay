package paymgr_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/gtkit/go-pay/paymgr"
)

// demoProvider 演示自定义渠道的推荐写法：嵌入 UnimplementedProvider，
// 实现 Channel 并只覆写渠道支持的能力，其余方法默认返回 ErrNotSupported。
type demoProvider struct {
	paymgr.UnimplementedProvider
}

func (p *demoProvider) Channel() paymgr.Channel { return "demo" }

func (p *demoProvider) UnifiedOrder(_ context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return &paymgr.UnifiedOrderResponse{
		Channel: "demo",
		CodeURL: "https://pay.example.com/qr/" + req.OutTradeNo,
	}, nil
}

func ExampleUnimplementedProvider() {
	mgr := paymgr.NewManager()
	mgr.Register(&demoProvider{})

	// 覆写过的能力与内置渠道完全同等使用
	resp, err := mgr.UnifiedOrder(context.Background(), "demo", &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-20260101-0001",
		TotalAmount: 9900,
		Subject:     "演示商品",
		TradeType:   paymgr.TradeTypeNative,
		NotifyURL:   "https://merchant.example.com/notify",
	})
	if err != nil {
		fmt.Println("unified order:", err)
		return
	}
	fmt.Println("code url:", resp.CodeURL)

	// 未覆写的能力返回 ErrNotSupported
	_, err = mgr.Refund(context.Background(), "demo", &paymgr.RefundRequest{
		OutTradeNo:   "ORD-20260101-0001",
		OutRefundNo:  "R-0001",
		RefundAmount: 9900,
		TotalAmount:  9900,
	})
	fmt.Println("refund not supported:", errors.Is(err, paymgr.ErrNotSupported))

	// Output:
	// code url: https://pay.example.com/qr/ORD-20260101-0001
	// refund not supported: true
}
