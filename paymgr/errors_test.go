package paymgr

import (
	"errors"
	"testing"
)

func TestChannelErrorError(t *testing.T) {
	cause := errors.New("sdk failure")
	tests := []struct {
		name string
		err  *ChannelError
		want string
	}{
		{
			name: "with cause",
			err:  NewChannelError(ChannelWechat, "ORDERPAID", "order already paid", cause),
			want: "payment[wxpay]: code=ORDERPAID, msg=order already paid, cause=sdk failure",
		},
		{
			name: "without cause",
			err:  NewChannelError(ChannelAlipay, "ACQ.TRADE_HAS_SUCCESS", "trade has success", nil),
			want: "payment[alipay]: code=ACQ.TRADE_HAS_SUCCESS, msg=trade has success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChannelErrorUnwrap(t *testing.T) {
	cause := errors.New("sdk failure")

	err := NewChannelError(ChannelWechat, "SYSTEM_ERROR", "system error", cause)
	if err.Unwrap() != cause {
		t.Fatalf("Unwrap() = %v, want %v", err.Unwrap(), cause)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(err, cause) = false, want true")
	}

	noCause := NewChannelError(ChannelAlipay, "SYSTEM_ERROR", "system error", nil)
	if noCause.Unwrap() != nil {
		t.Fatalf("Unwrap() = %v, want nil", noCause.Unwrap())
	}
}

func TestNewChannelError(t *testing.T) {
	cause := errors.New("sdk failure")

	err := NewChannelError(ChannelWechat, "ORDERPAID", "order already paid", cause)
	if err.Channel != ChannelWechat {
		t.Fatalf("Channel = %q, want %q", err.Channel, ChannelWechat)
	}
	if err.Code != "ORDERPAID" {
		t.Fatalf("Code = %q, want %q", err.Code, "ORDERPAID")
	}
	if err.Message != "order already paid" {
		t.Fatalf("Message = %q, want %q", err.Message, "order already paid")
	}
	if err.Err != cause {
		t.Fatalf("Err = %v, want %v", err.Err, cause)
	}
}
