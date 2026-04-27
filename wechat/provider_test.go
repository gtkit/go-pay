package wechat

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/h5"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

var _ paymgr.Provider = (*Provider)(nil)

func TestParseTimeRFC3339(t *testing.T) {
	input := "2023-06-08T10:34:56+08:00"
	want, err := time.Parse(time.RFC3339, input)
	if err != nil {
		t.Fatalf("parse expected time: %v", err)
	}

	got := parseTime(input)
	if !got.Equal(want) {
		t.Fatalf("parseTime(%q) = %v, want %v", input, got, want)
	}
}

func TestParseTimeInvalidReturnsZero(t *testing.T) {
	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Fatalf("parseTime returned %v, want zero time", got)
	}
}

func TestMapWechatRefundStatus(t *testing.T) {
	success := refunddomestic.STATUS_SUCCESS
	closed := refunddomestic.STATUS_CLOSED
	processing := refunddomestic.STATUS_PROCESSING
	abnormal := refunddomestic.STATUS_ABNORMAL

	tests := []struct {
		name string
		in   *refunddomestic.Status
		want paymgr.RefundStatus
	}{
		{name: "nil", in: nil, want: paymgr.RefundStatusError},
		{name: "success", in: &success, want: paymgr.RefundStatusSuccess},
		{name: "closed", in: &closed, want: paymgr.RefundStatusClosed},
		{name: "processing", in: &processing, want: paymgr.RefundStatusProcessing},
		{name: "abnormal", in: &abnormal, want: paymgr.RefundStatusAbnormal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapWechatRefundStatus(tt.in); got != tt.want {
				t.Fatalf("mapWechatRefundStatus(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMapWechatRefundStatusString(t *testing.T) {
	tests := map[string]paymgr.RefundStatus{
		"SUCCESS":    paymgr.RefundStatusSuccess,
		"CLOSED":     paymgr.RefundStatusClosed,
		"PROCESSING": paymgr.RefundStatusProcessing,
		"ABNORMAL":   paymgr.RefundStatusAbnormal,
		"":           paymgr.RefundStatusError,
		"UNKNOWN":    paymgr.RefundStatusError,
	}
	for in, want := range tests {
		if got := mapWechatRefundStatusString(in); got != want {
			t.Fatalf("mapWechatRefundStatusString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrapWechatErrorPreservesAPIErrorCode(t *testing.T) {
	err := &core.APIError{
		Code:    "ORDERNOTEXIST",
		Message: "order does not exist",
	}

	wrapped := wrapWechatError(err)

	chErr, ok := errors.AsType[*paymgr.ChannelError](wrapped)
	if !ok {
		t.Fatalf("wrapWechatError(%v) did not return ChannelError", err)
	}
	if chErr.Code != "ORDERNOTEXIST" {
		t.Fatalf("ChannelError.Code = %q, want %q", chErr.Code, "ORDERNOTEXIST")
	}
	if chErr.Message != "order does not exist" {
		t.Fatalf("ChannelError.Message = %q, want %q", chErr.Message, "order does not exist")
	}
}

func TestUnifiedOrderRequiresOpenIDForJSAPI(t *testing.T) {
	p := &Provider{}

	_, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-1",
		TotalAmount: 100,
		Subject:     "demo",
		TradeType:   paymgr.TradeTypeJSAPI,
		NotifyURL:   "https://example.com/notify",
	})
	if err == nil {
		t.Fatal("UnifiedOrder() error = nil, want error")
	}
	if !errors.Is(err, paymgr.ErrInvalidParam) {
		t.Fatalf("UnifiedOrder() error = %v, want wrapped ErrInvalidParam", err)
	}
	if !strings.Contains(err.Error(), "openid is required") {
		t.Fatalf("UnifiedOrder() error = %v, want openid validation message", err)
	}
}

func TestUnifiedOrderRequiresClientIPForH5(t *testing.T) {
	p := &Provider{}

	_, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-2",
		TotalAmount: 100,
		Subject:     "demo",
		TradeType:   paymgr.TradeTypeH5,
		NotifyURL:   "https://example.com/notify",
	})
	if err == nil {
		t.Fatal("UnifiedOrder() error = nil, want error")
	}
	if !errors.Is(err, paymgr.ErrInvalidParam) {
		t.Fatalf("UnifiedOrder() error = %v, want wrapped ErrInvalidParam", err)
	}
	if !strings.Contains(err.Error(), "client_ip is required") {
		t.Fatalf("UnifiedOrder() error = %v, want client_ip validation message", err)
	}
}

func TestBuildJSAPIPayParams(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	p := &Provider{
		cfg:        &Config{AppID: "wx1234567890"},
		privateKey: privateKey,
	}

	got, err := p.buildJSAPIPayParams("prepay-id")
	if err != nil {
		t.Fatalf("buildJSAPIPayParams() error = %v", err)
	}

	var params struct {
		AppID     string `json:"appId"`
		TimeStamp string `json:"timeStamp"`
		NonceStr  string `json:"nonceStr"`
		Package   string `json:"package"`
		SignType  string `json:"signType"`
		PaySign   string `json:"paySign"`
	}
	if err := json.Unmarshal([]byte(got), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if params.AppID != "wx1234567890" {
		t.Fatalf("appId = %q, want %q", params.AppID, "wx1234567890")
	}
	if params.Package != "prepay_id=prepay-id" {
		t.Fatalf("package = %q, want %q", params.Package, "prepay_id=prepay-id")
	}
	if params.SignType != "RSA" {
		t.Fatalf("signType = %q, want %q", params.SignType, "RSA")
	}
	if params.TimeStamp == "" || params.NonceStr == "" || params.PaySign == "" {
		t.Fatalf("unexpected empty jsapi params: %+v", params)
	}

	signature, err := base64.StdEncoding.DecodeString(params.PaySign)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	message := params.AppID + "\n" + params.TimeStamp + "\n" + params.NonceStr + "\n" + params.Package + "\n"
	sum := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(&privateKey.PublicKey, crypto.SHA256, sum[:], signature); err != nil {
		t.Fatalf("VerifyPKCS1v15() error = %v", err)
	}
}

func TestBuildH5SceneInfo(t *testing.T) {
	sceneInfo := buildH5SceneInfo(&paymgr.UnifiedOrderRequest{
		ClientIP: "10.20.30.40",
	})
	if sceneInfo == nil {
		t.Fatal("buildH5SceneInfo() = nil")
	}
	if sceneInfo.PayerClientIp == nil || *sceneInfo.PayerClientIp != "10.20.30.40" {
		t.Fatalf("PayerClientIp = %v, want %q", sceneInfo.PayerClientIp, "10.20.30.40")
	}
	if sceneInfo.H5Info == nil {
		t.Fatal("H5Info = nil")
	}
	if sceneInfo.H5Info.Type == nil || *sceneInfo.H5Info.Type != "Wap" {
		t.Fatalf("H5Info.Type = %v, want %q", sceneInfo.H5Info.Type, "Wap")
	}
}

func TestBuildH5SceneInfoReturnsRequiredShape(t *testing.T) {
	sceneInfo := buildH5SceneInfo(&paymgr.UnifiedOrderRequest{
		ClientIP: "10.20.30.40",
	})

	if _, err := sceneInfo.MarshalJSON(); err != nil {
		t.Fatalf("SceneInfo.MarshalJSON() error = %v", err)
	}
}

var _ = h5.SceneInfo{}
