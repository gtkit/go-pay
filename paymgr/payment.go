// Package manager 提供统一支付接口抽象层。
//
// 设计原则:
//   - 微信支付使用官方 SDK: github.com/wechatpay-apiv3/wechatpay-go
//   - 支付宝使用 smartwalle/alipay/v3
//   - 业务层通过 Provider 接口调用，无需感知底层 SDK 差异
//   - 所有金额使用 int64 分为单位，避免浮点精度问题
package paymgr

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Channel 支付渠道枚举.
type Channel string

const (
	ChannelWechat Channel = "wxpay"
	ChannelAlipay Channel = "alipay"
)

// TradeType 交易类型.
type TradeType string

const (
	TradeTypeNative TradeType = "native" // 扫码支付(PC)
	TradeTypeJSAPI  TradeType = "jsapi"  // 公众号/小程序支付
	TradeTypeApp    TradeType = "app"    // APP 支付
	TradeTypeH5     TradeType = "h5"     // 手机网页支付
	TradeTypePage   TradeType = "page"   // PC 网页支付 / 支付宝收银台
)

// TradeStatus 交易状态.
type TradeStatus string

const (
	TradeStatusPending  TradeStatus = "pending"  // 待支付
	TradeStatusPaid     TradeStatus = "paid"     // 已支付
	TradeStatusClosed   TradeStatus = "closed"   // 已关闭
	TradeStatusRefunded TradeStatus = "refunded" // 已退款
	TradeStatusError    TradeStatus = "error"    // 异常
)

// RefundStatus 退款状态.
//
// 各渠道原始状态映射:
//   - 微信: SUCCESS→Success, CLOSED→Closed, PROCESSING→Processing, ABNORMAL→Abnormal
//   - 支付宝: REFUND_SUCCESS→Success, REFUND_PROCESSING→Processing, REFUND_FAIL/未返回→Error
type RefundStatus string

const (
	RefundStatusProcessing RefundStatus = "processing" // 退款处理中
	RefundStatusSuccess    RefundStatus = "success"    // 退款成功
	RefundStatusClosed     RefundStatus = "closed"     // 退款关闭（未成功）
	RefundStatusAbnormal   RefundStatus = "abnormal"   // 退款异常（需人工介入）
	RefundStatusError      RefundStatus = "error"      // 未知/失败
)

// --- 请求/响应结构 ---

// UnifiedOrderRequest 统一下单请求.
type UnifiedOrderRequest struct {
	OutTradeNo  string            // 商户订单号，必填，需保证全局唯一
	TotalAmount int64             // 订单总金额，单位：分
	Subject     string            // 商品描述/标题
	TradeType   TradeType         // 交易类型
	NotifyURL   string            // 异步通知回调地址
	ReturnURL   string            // 同步跳转地址（仅支付宝 H5/PC 场景使用）
	ClientIP    string            // 用户客户端 IP（微信 H5 支付必填）
	OpenID      string            // 微信 JSAPI 必填，支付宝忽略
	ExpireAt    time.Time         // 订单过期时间，零值表示使用默认值
	Metadata    map[string]string // 附加数据，会在回调中原样返回（微信 attach / 支付宝 passback_params）
}

// Validate 校验请求参数.
func (r *UnifiedOrderRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: unified order request is required", ErrInvalidParam)
	}
	if r.OutTradeNo == "" {
		return fmt.Errorf("%w: out_trade_no is required", ErrInvalidParam)
	}
	if r.TotalAmount <= 0 {
		return fmt.Errorf("%w: total_amount must be positive, got %d", ErrInvalidParam, r.TotalAmount)
	}
	if r.Subject == "" {
		return fmt.Errorf("%w: subject is required", ErrInvalidParam)
	}
	if r.NotifyURL == "" {
		return fmt.Errorf("%w: notify_url is required", ErrInvalidParam)
	}
	return nil
}

// UnifiedOrderResponse 统一下单响应.
type UnifiedOrderResponse struct {
	Channel     Channel   // 支付渠道
	PrepayID    string    // 预支付交易会话标识（微信）
	CodeURL     string    // 二维码链接（Native 扫码支付）
	PayURL      string    // 支付跳转链接（支付宝 PC/H5）
	H5URL       string    // H5 支付链接（微信 H5）
	AppParams   string    // APP 调起支付参数（JSON 字符串）
	JSAPIParams string    // JSAPI 调起支付参数（JSON 字符串）
	ExpireAt    time.Time // 订单过期时间
}

// QueryOrderRequest 订单查询请求.
type QueryOrderRequest struct {
	OutTradeNo    string // 商户订单号（二选一）
	TransactionID string // 渠道交易号（二选一）
}

// Validate 校验查询请求.
func (r *QueryOrderRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: query order request is required", ErrInvalidParam)
	}
	if r.OutTradeNo == "" && r.TransactionID == "" {
		return fmt.Errorf("%w: out_trade_no or transaction_id is required", ErrInvalidParam)
	}
	return nil
}

// QueryOrderResponse 订单查询响应.
type QueryOrderResponse struct {
	Channel       Channel     // 支付渠道
	OutTradeNo    string      // 商户订单号
	TransactionID string      // 渠道交易号
	TradeStatus   TradeStatus // 交易状态
	TotalAmount   int64       // 订单总金额，单位：分
	PaidAt        time.Time   // 支付完成时间
	BuyerID       string      // 买家标识（微信 openid / 支付宝 buyer_id）
}

// CloseOrderRequest 关闭订单请求.
type CloseOrderRequest struct {
	OutTradeNo string // 商户订单号，必填
}

// RefundRequest 退款请求.
type RefundRequest struct {
	OutTradeNo    string // 商户订单号（二选一）
	TransactionID string // 渠道交易号（二选一）
	OutRefundNo   string // 商户退款单号，必填，需保证唯一
	RefundAmount  int64  // 退款金额，单位：分
	TotalAmount   int64  // 原订单总金额，单位：分（支付宝需要）
	Reason        string // 退款原因
	NotifyURL     string // 退款异步通知地址（可选）
}

// Validate 校验退款请求.
func (r *RefundRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: refund request is required", ErrInvalidParam)
	}
	if r.OutTradeNo == "" && r.TransactionID == "" {
		return fmt.Errorf("%w: out_trade_no or transaction_id is required for refund", ErrInvalidParam)
	}
	if r.OutRefundNo == "" {
		return fmt.Errorf("%w: out_refund_no is required", ErrInvalidParam)
	}
	if r.RefundAmount <= 0 {
		return fmt.Errorf("%w: refund_amount must be positive, got %d", ErrInvalidParam, r.RefundAmount)
	}
	if r.TotalAmount <= 0 {
		return fmt.Errorf("%w: total_amount must be positive for refund", ErrInvalidParam)
	}
	if r.RefundAmount > r.TotalAmount {
		return fmt.Errorf("%w: refund_amount(%d) cannot exceed total_amount(%d)", ErrInvalidParam, r.RefundAmount, r.TotalAmount)
	}
	return nil
}

// RefundResponse 退款响应.
type RefundResponse struct {
	Channel      Channel // 支付渠道
	OutRefundNo  string  // 商户退款单号
	RefundID     string  // 渠道退款单号
	RefundAmount int64   // 退款金额，单位：分
}

// QueryRefundRequest 退款查询请求.
//
// OutRefundNo 必填；OutTradeNo / TransactionID 仅支付宝需要（二选一）。
type QueryRefundRequest struct {
	OutTradeNo    string // 商户订单号（支付宝用，与 TransactionID 二选一）
	TransactionID string // 渠道交易号（支付宝用，与 OutTradeNo 二选一）
	OutRefundNo   string // 商户退款单号，必填
}

// Validate 校验退款查询请求.
func (r *QueryRefundRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: query refund request is required", ErrInvalidParam)
	}
	if r.OutRefundNo == "" {
		return fmt.Errorf("%w: out_refund_no is required", ErrInvalidParam)
	}
	return nil
}

// QueryRefundResponse 退款查询响应.
type QueryRefundResponse struct {
	Channel       Channel      // 支付渠道
	OutTradeNo    string       // 商户订单号
	TransactionID string       // 渠道交易号
	OutRefundNo   string       // 商户退款单号
	RefundID      string       // 渠道退款单号
	RefundStatus  RefundStatus // 退款状态
	RefundAmount  int64        // 本次退款金额，单位：分
	TotalAmount   int64        // 原订单总金额，单位：分
	RefundedAt    time.Time    // 退款完成时间（成功时有值）
}

// RefundNotifyResult 退款异步通知解析结果.
//
// 目前仅微信支付提供独立的退款异步通知。支付宝的退款结果通过
// 与支付相同的 notify_url 回调，使用 Provider.ParseNotify 解析即可
// （GmtRefund / RefundFee 非空时，TradeStatus 会被映射为 TradeStatusRefunded）。
type RefundNotifyResult struct {
	Channel             Channel      // 支付渠道
	OutTradeNo          string       // 商户订单号
	TransactionID       string       // 渠道交易号
	OutRefundNo         string       // 商户退款单号
	RefundID            string       // 渠道退款单号
	RefundStatus        RefundStatus // 退款状态
	RefundAmount        int64        // 本次退款金额，单位：分
	TotalAmount         int64        // 原订单总金额，单位：分
	RefundedAt          time.Time    // 退款完成时间
	UserReceivedAccount string       // 退款入账方（微信返回，如 "招商银行信用卡0403"）
}

// NotifyResult 回调通知解析结果.
type NotifyResult struct {
	Channel       Channel           // 支付渠道
	OutTradeNo    string            // 商户订单号
	TransactionID string            // 渠道交易号
	TradeStatus   TradeStatus       // 交易状态
	TotalAmount   int64             // 订单总金额，单位：分
	PaidAt        time.Time         // 支付完成时间
	BuyerID       string            // 买家标识
	Metadata      map[string]string // 附加数据
}

// --- 核心接口 ---

// Provider 统一支付提供者接口
//
// 所有支付渠道必须实现此接口，业务层只依赖此接口。
// 设计上每个方法都接收 context.Context，支持超时控制和链路追踪。
type Provider interface {
	// Channel 返回当前提供者的支付渠道标识
	Channel() Channel

	// UnifiedOrder 统一下单
	//
	// 根据 TradeType 创建预支付订单，返回唤起支付所需的参数。
	UnifiedOrder(ctx context.Context, req *UnifiedOrderRequest) (*UnifiedOrderResponse, error)

	// QueryOrder 查询订单
	//
	// 通过商户订单号或渠道交易号查询订单状态。
	QueryOrder(ctx context.Context, req *QueryOrderRequest) (*QueryOrderResponse, error)

	// CloseOrder 关闭订单
	//
	// 关闭未支付的订单，已支付的订单不能关闭。
	CloseOrder(ctx context.Context, req *CloseOrderRequest) error

	// Refund 申请退款
	//
	// 对已支付的订单发起退款，支持部分退款。
	Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)

	// QueryRefund 查询退款
	//
	// 按商户退款单号查询退款状态。
	QueryRefund(ctx context.Context, req *QueryRefundRequest) (*QueryRefundResponse, error)

	// ParseNotify 解析异步通知
	//
	// 从 HTTP 请求中解析并验签支付结果通知。
	// 验签通过返回 NotifyResult，验签失败返回 error。
	ParseNotify(ctx context.Context, r *http.Request) (*NotifyResult, error)

	// ParseRefundNotify 解析退款异步通知
	//
	// 微信支付：解析独立的退款通知（event_type 为 REFUND.*）。
	// 支付宝：由于退款结果复用支付通知端点，此方法返回 ErrNotSupported；
	// 接入方应使用 ParseNotify 接收退款事件并通过 TradeStatus 判断。
	ParseRefundNotify(ctx context.Context, r *http.Request) (*RefundNotifyResult, error)

	// ACKNotify 响应异步通知
	//
	// 向支付平台回写"已收到通知"的成功响应。
	// 必须在 ParseNotify 成功后调用，否则平台会持续重发通知。
	ACKNotify(w http.ResponseWriter)
}
