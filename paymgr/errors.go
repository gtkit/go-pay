package paymgr

import (
	"errors"
	"fmt"
)

// 哨兵错误，用于 errors.Is 判断
var (
	ErrInvalidParam    = errors.New("payment: invalid param")
	ErrOrderNotFound   = errors.New("payment: order not found")
	ErrOrderClosed     = errors.New("payment: order already closed")
	ErrOrderPaid       = errors.New("payment: order already paid")
	ErrRefundFailed    = errors.New("payment: refund failed")
	ErrInvalidSign     = errors.New("payment: invalid signature")
	ErrInvalidNotify   = errors.New("payment: invalid notification")
	ErrUnsupportedType = errors.New("payment: unsupported trade type")
)

// ChannelError 渠道级错误，包含渠道原始错误码和描述
type ChannelError struct {
	Channel Channel // 支付渠道
	Code    string  // 渠道错误码（如微信 ORDERPAID、支付宝 ACQ.TRADE_HAS_SUCCESS）
	Message string  // 渠道错误描述
	Err     error   // 底层 SDK 原始错误
}

func (e *ChannelError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("payment[%s]: code=%s, msg=%s, cause=%v", e.Channel, e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("payment[%s]: code=%s, msg=%s", e.Channel, e.Code, e.Message)
}

func (e *ChannelError) Unwrap() error {
	return e.Err
}

// NewChannelError 创建渠道错误
func NewChannelError(ch Channel, code, message string, err error) *ChannelError {
	return &ChannelError{
		Channel: ch,
		Code:    code,
		Message: message,
		Err:     err,
	}
}
