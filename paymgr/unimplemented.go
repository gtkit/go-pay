package paymgr

import (
	"context"
	"fmt"
	"net/http"
)

// UnimplementedProvider 是自定义渠道的可嵌入基座：实现除 Channel 外的
// 全部 Provider 方法，能力方法默认返回 ErrNotSupported 包装错误。
//
// 自定义渠道应嵌入它并只覆写支持的方法。未来 Provider 接口新增能力
// 方法时，嵌入者自动获得默认实现，编译不会被破坏：
//
//	type MyProvider struct {
//		paymgr.UnimplementedProvider
//	}
//
//	func (p *MyProvider) Channel() paymgr.Channel { return "mychannel" }
//	func (p *MyProvider) UnifiedOrder(ctx context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
//		// 只覆写支持的能力
//	}
//
// Channel 不提供默认实现：渠道标识是注册身份，必须由自定义渠道自行实现。
// ACKNotify 默认是 no-op，支持异步通知的渠道必须覆写。
type UnimplementedProvider struct{}

// UnifiedOrder 默认返回 ErrNotSupported。
func (UnimplementedProvider) UnifiedOrder(context.Context, *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
	return nil, fmt.Errorf("%w: UnifiedOrder", ErrNotSupported)
}

// QueryOrder 默认返回 ErrNotSupported。
func (UnimplementedProvider) QueryOrder(context.Context, *QueryOrderRequest) (*QueryOrderResponse, error) {
	return nil, fmt.Errorf("%w: QueryOrder", ErrNotSupported)
}

// CloseOrder 默认返回 ErrNotSupported。
func (UnimplementedProvider) CloseOrder(context.Context, *CloseOrderRequest) error {
	return fmt.Errorf("%w: CloseOrder", ErrNotSupported)
}

// Refund 默认返回 ErrNotSupported。
func (UnimplementedProvider) Refund(context.Context, *RefundRequest) (*RefundResponse, error) {
	return nil, fmt.Errorf("%w: Refund", ErrNotSupported)
}

// QueryRefund 默认返回 ErrNotSupported。
func (UnimplementedProvider) QueryRefund(context.Context, *QueryRefundRequest) (*QueryRefundResponse, error) {
	return nil, fmt.Errorf("%w: QueryRefund", ErrNotSupported)
}

// ParseNotify 默认返回 ErrNotSupported。
func (UnimplementedProvider) ParseNotify(context.Context, *http.Request) (*NotifyResult, error) {
	return nil, fmt.Errorf("%w: ParseNotify", ErrNotSupported)
}

// ParseRefundNotify 默认返回 ErrNotSupported。
func (UnimplementedProvider) ParseRefundNotify(context.Context, *http.Request) (*RefundNotifyResult, error) {
	return nil, fmt.Errorf("%w: ParseRefundNotify", ErrNotSupported)
}

// ACKNotify 默认 no-op，支持异步通知的渠道必须覆写。
func (UnimplementedProvider) ACKNotify(http.ResponseWriter) {}
