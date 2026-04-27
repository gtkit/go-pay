package aggregate_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gtkit/go-pay/aggregate"
	"github.com/gtkit/go-pay/paymgr"
)

type exampleProvider struct {
	ch paymgr.Channel
}

func (p exampleProvider) Channel() paymgr.Channel {
	return p.ch
}

func (p exampleProvider) UnifiedOrder(_ context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
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

func (p exampleProvider) QueryOrder(context.Context, *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	return nil, nil
}

func (p exampleProvider) CloseOrder(context.Context, *paymgr.CloseOrderRequest) error {
	return nil
}

func (p exampleProvider) Refund(context.Context, *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	return nil, nil
}

func (p exampleProvider) QueryRefund(context.Context, *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	return nil, nil
}

func (p exampleProvider) ParseNotify(context.Context, *http.Request) (*paymgr.NotifyResult, error) {
	return nil, nil
}

func (p exampleProvider) ParseRefundNotify(context.Context, *http.Request) (*paymgr.RefundNotifyResult, error) {
	return nil, nil
}

func (p exampleProvider) ACKNotify(http.ResponseWriter) {}

func newExampleService() *aggregate.Service {
	mgr := paymgr.NewManager()
	mgr.Register(exampleProvider{ch: paymgr.ChannelWechat})
	mgr.Register(exampleProvider{ch: paymgr.ChannelAlipay})
	return aggregate.NewService(mgr)
}

func buildExampleOrder(_ paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
	return &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-EXAMPLE-1",
		TotalAmount: 100,
		Subject:     "aggregate example order",
		TradeType:   tradeType,
		NotifyURL:   "https://example.com/notify",
		ReturnURL:   "https://example.com/return",
		ClientIP:    "203.0.113.10",
		OpenID:      "openid-123",
	}, nil
}

func ExampleDetectEnv() {
	fmt.Println(aggregate.DetectEnv("Mozilla/5.0 MicroMessenger/8.0.49"))
	// Output:
	// wechat
}

func ExampleNewService() {
	fmt.Println(newExampleService() != nil)
	// Output:
	// true
}

func ExampleService_Resolve() {
	result, err := newExampleService().Resolve(context.Background(), &aggregate.ResolveRequest{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		SelectedChannel: paymgr.ChannelAlipay,
		BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
			return buildExampleOrder(ch, tradeType)
		},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.Env)
	fmt.Println(result.Action)
	fmt.Println(result.Channel)
	fmt.Println(result.TradeType)
	fmt.Println(result.Response.PayURL)
	// Output:
	// browser_pc
	// redirect
	// alipay
	// page
	// https://open.alipay.com/page
}
