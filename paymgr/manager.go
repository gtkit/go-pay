package paymgr

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
)

// Manager 支付管理器，统一管理多个支付渠道。
//
// Manager 可并发安全使用。运行时允许 Register / Deregister 与读操作并发执行。
//
// 使用方式:
//
//	mgr := payment.NewManager()
//	mgr.Register(wechatProvider)
//	mgr.Register(alipayProvider)
//
//	// 下单
//	resp, err := mgr.UnifiedOrder(ctx, pay.ChannelWechat, req)
//
//	// 处理回调（在 HTTP handler 中根据路由区分渠道）
//	result, err := mgr.ParseNotify(ctx, pay.ChannelWechat, r)
type Manager struct {
	mu        sync.RWMutex
	providers map[Channel]Provider
}

// NewManager 创建支付管理器.
func NewManager() *Manager {
	return &Manager{
		providers: make(map[Channel]Provider),
	}
}

// Register 注册支付渠道提供者
//
// 同一渠道重复注册会覆盖旧的提供者。
func (m *Manager) Register(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[p.Channel()] = p
}

// Deregister 注销支付渠道提供者
//
// 注销后该渠道的所有操作（下单/查询/退款/回调）都会返回错误。
// 适用场景: 运行时临时下线某个渠道、切换前先清理旧实例。
//
// 返回 true 表示确实删除了，false 表示该渠道本来就不存在。
func (m *Manager) Deregister(ch Channel) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.providers[ch]
	if ok {
		delete(m.providers, ch)
	}
	return ok
}

// Provider 获取指定渠道的提供者.
func (m *Manager) Provider(ch Channel) (Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[ch]
	if !ok {
		return nil, fmt.Errorf("payment: channel %q not registered", ch)
	}
	return p, nil
}

// Channels 返回当前已注册的所有渠道（监控/健康检查用）
func (m *Manager) Channels() []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Keys(m.providers))
}

// UnifiedOrder 统一下单.
func (m *Manager) UnifiedOrder(ctx context.Context, ch Channel, req *UnifiedOrderRequest) (*UnifiedOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	p, err := m.Provider(ch)
	if err != nil {
		return nil, err
	}
	return p.UnifiedOrder(ctx, req)
}

// QueryOrder 查询订单.
func (m *Manager) QueryOrder(ctx context.Context, ch Channel, req *QueryOrderRequest) (*QueryOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	p, err := m.Provider(ch)
	if err != nil {
		return nil, err
	}
	return p.QueryOrder(ctx, req)
}

// CloseOrder 关闭订单.
func (m *Manager) CloseOrder(ctx context.Context, ch Channel, req *CloseOrderRequest) error {
	if req == nil {
		return fmt.Errorf("%w: close order request is required", ErrInvalidParam)
	}
	if req.OutTradeNo == "" {
		return fmt.Errorf("%w: out_trade_no is required", ErrInvalidParam)
	}
	p, err := m.Provider(ch)
	if err != nil {
		return err
	}
	return p.CloseOrder(ctx, req)
}

// Refund 申请退款.
func (m *Manager) Refund(ctx context.Context, ch Channel, req *RefundRequest) (*RefundResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	p, err := m.Provider(ch)
	if err != nil {
		return nil, err
	}
	return p.Refund(ctx, req)
}

// ParseNotify 解析异步通知.
func (m *Manager) ParseNotify(ctx context.Context, ch Channel, r *http.Request) (*NotifyResult, error) {
	p, err := m.Provider(ch)
	if err != nil {
		return nil, err
	}
	return p.ParseNotify(ctx, r)
}

// ACKNotify 响应异步通知.
func (m *Manager) ACKNotify(ch Channel, w http.ResponseWriter) error {
	p, err := m.Provider(ch)
	if err != nil {
		return err
	}
	p.ACKNotify(w)
	return nil
}
