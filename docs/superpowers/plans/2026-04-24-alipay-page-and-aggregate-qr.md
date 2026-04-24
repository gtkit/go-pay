# Alipay Page And Aggregate QR Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `paymgr.TradeTypePage` for Alipay PC page pay, then build an `aggregate` orchestration layer that routes aggregate QR traffic into the correct real payment flow.

**Architecture:** First extend the existing unified trade-type model and the Alipay provider so PC page pay becomes a first-class payment primitive. Then add a new top-level `aggregate` package that detects runtime environment from `User-Agent`, resolves the target `channel + trade_type`, delegates real order creation through `paymgr.Manager`, and returns a normalized action result to the business layer.

**Tech Stack:** Go 1.26, `github.com/smartwalle/alipay/v3`, existing `paymgr` / `wechat` / `alipay` packages, table-driven unit tests, `go test`

---

## File Structure

- Modify: `paymgr/payment.go`
  Purpose: add `TradeTypePage` to the shared trade-type enum and keep the unified request/response model stable.
- Modify: `paymgr/payment_test.go`
  Purpose: lock the new public trade-type constant and regression-check unified validation.
- Modify: `alipay/provider.go`
  Purpose: add `TradeTypePage` support to `UnifiedOrder` and map the request into `sdk.TradePagePay`.
- Modify: `alipay/provider_test.go`
  Purpose: verify page-pay field mapping and `PayURL` generation without network calls.
- Create: `aggregate/doc.go`
  Purpose: package-level documentation for the new orchestration layer.
- Create: `aggregate/env.go`
  Purpose: environment detection (`wechat`, `alipay`, browser `pc/mobile`) and UA helpers.
- Create: `aggregate/errors.go`
  Purpose: package-specific orchestration errors.
- Create: `aggregate/service.go`
  Purpose: request/result types and the `Resolve` orchestration logic.
- Create: `aggregate/service_test.go`
  Purpose: cover environment detection, `choose_channel`, decision routing, and error paths.
- Modify: `README.md`
  Purpose: document `TradeTypePage`, direct PC page pay, and aggregate QR integration.
- Modify: `example/main.go`
  Purpose: keep the direct-pay example in sync with the expanded `pay_url` semantics.
- Modify: `CHANGELOG.md`
  Purpose: record `TradeTypePage` and aggregate QR orchestration as unreleased features.

### Task 1: Add Shared `TradeTypePage`

**Files:**
- Modify: `paymgr/payment.go`
- Test: `paymgr/payment_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestTradeTypePageConstant(t *testing.T) {
	if TradeTypePage != "page" {
		t.Fatalf("TradeTypePage = %q, want %q", TradeTypePage, "page")
	}
}

func TestUnifiedOrderValidateAllowsTradeTypePage(t *testing.T) {
	req := &UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 100,
		Subject:     "PC page order",
		TradeType:   TradeTypePage,
		NotifyURL:   "https://example.com/notify",
		ReturnURL:   "https://example.com/return",
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./paymgr -run 'TestTradeTypePageConstant|TestUnifiedOrderValidateAllowsTradeTypePage'`
Expected: FAIL to compile because `TradeTypePage` does not exist yet

- [ ] **Step 3: Write minimal implementation**

```go
const (
	TradeTypeNative TradeType = "native" // 扫码支付(PC)
	TradeTypeJSAPI  TradeType = "jsapi"  // 公众号/小程序支付
	TradeTypeApp    TradeType = "app"    // APP 支付
	TradeTypeH5     TradeType = "h5"     // 手机网页支付
	TradeTypePage   TradeType = "page"   // PC 网页支付 / 支付宝收银台
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./paymgr -run 'TestTradeTypePageConstant|TestUnifiedOrderValidateAllowsTradeTypePage'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add paymgr/payment.go paymgr/payment_test.go
git commit -m "feat: add shared page trade type"
```

### Task 2: Add Alipay `TradePagePay` Support

**Files:**
- Modify: `alipay/provider.go`
- Test: `alipay/provider_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func newSignedTestProvider(t *testing.T) *Provider {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	client, err := sdk.New("2021000000000001", string(pemBytes), false)
	if err != nil {
		t.Fatalf("sdk.New() error = %v", err)
	}

	return &Provider{
		client: client,
		cfg:    &Config{AppID: "2021000000000001"},
	}
}

func TestBuildTradePagePayMapsFields(t *testing.T) {
	req := &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 1234,
		Subject:     "PC page order",
		NotifyURL:   "https://api.example.com/notify/alipay",
		ReturnURL:   "https://www.example.com/pay/return",
		Metadata: map[string]string{
			"order_id": "ORD-PAGE-1",
		},
	}

	trade := buildTradePagePay(req, "12.34", "25m", "order_id=ORD-PAGE-1")

	if trade.OutTradeNo != "ORD-PAGE-1" {
		t.Fatalf("OutTradeNo = %q, want %q", trade.OutTradeNo, "ORD-PAGE-1")
	}
	if trade.ReturnURL != "https://www.example.com/pay/return" {
		t.Fatalf("ReturnURL = %q, want %q", trade.ReturnURL, "https://www.example.com/pay/return")
	}
	if trade.TimeoutExpress != "25m" {
		t.Fatalf("TimeoutExpress = %q, want %q", trade.TimeoutExpress, "25m")
	}
	if trade.PassbackParams != "order_id=ORD-PAGE-1" {
		t.Fatalf("PassbackParams = %q, want %q", trade.PassbackParams, "order_id=ORD-PAGE-1")
	}
}

func TestUnifiedOrderPageReturnsPayURL(t *testing.T) {
	p := newSignedTestProvider(t)

	resp, err := p.UnifiedOrder(t.Context(), &paymgr.UnifiedOrderRequest{
		OutTradeNo:  "ORD-PAGE-1",
		TotalAmount: 1234,
		Subject:     "PC page order",
		TradeType:   paymgr.TradeTypePage,
		NotifyURL:   "https://api.example.com/notify/alipay",
		ReturnURL:   "https://www.example.com/pay/return",
	})
	if err != nil {
		t.Fatalf("UnifiedOrder() error = %v", err)
	}
	if resp.PayURL == "" {
		t.Fatal("PayURL = empty")
	}

	u, err := url.Parse(resp.PayURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	values := u.Query()
	if got := values.Get("method"); got != "alipay.trade.page.pay" {
		t.Fatalf("method = %q, want %q", got, "alipay.trade.page.pay")
	}
	if got := values.Get("return_url"); got != "https://www.example.com/pay/return" {
		t.Fatalf("return_url = %q, want %q", got, "https://www.example.com/pay/return")
	}
	if bizContent := values.Get("biz_content"); !strings.Contains(bizContent, `"out_trade_no":"ORD-PAGE-1"`) {
		t.Fatalf("biz_content = %q, want out_trade_no", bizContent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./alipay -run 'TestBuildTradePagePayMapsFields|TestUnifiedOrderPageReturnsPayURL'`
Expected: FAIL because `TradeTypePage` handling and `buildTradePagePay` do not exist yet

- [ ] **Step 3: Write minimal implementation**

```go
func buildTradePagePay(req *paymgr.UnifiedOrderRequest, amount, timeoutExpress, passbackParams string) alipay.TradePagePay {
	trade := alipay.TradePagePay{}
	trade.OutTradeNo = req.OutTradeNo
	trade.TotalAmount = amount
	trade.Subject = req.Subject
	trade.NotifyURL = req.NotifyURL
	if req.ReturnURL != "" {
		trade.ReturnURL = req.ReturnURL
	}
	if timeoutExpress != "" {
		trade.TimeoutExpress = timeoutExpress
	}
	if passbackParams != "" {
		trade.PassbackParams = passbackParams
	}
	return trade
}
```

```go
case paymgr.TradeTypePage:
	trade := buildTradePagePay(req, amount, timeoutExpress, passbackParams)
	result, err := p.client.TradePagePay(trade)
	if err != nil {
		return nil, wrapAlipayError(err)
	}
	resp.PayURL = result.String()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./alipay -run 'TestBuildTradePagePayMapsFields|TestUnifiedOrderPageReturnsPayURL'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add alipay/provider.go alipay/provider_test.go
git commit -m "feat: add alipay page pay support"
```

### Task 3: Create Aggregate Package Skeleton

**Files:**
- Create: `aggregate/doc.go`
- Create: `aggregate/env.go`
- Create: `aggregate/errors.go`
- Create: `aggregate/service.go`
- Test: `aggregate/service_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestDetectEnv(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want Env
	}{
		{name: "wechat", ua: "Mozilla/5.0 MicroMessenger/8.0.49", want: EnvWechat},
		{name: "alipay", ua: "Mozilla/5.0 AlipayClient/10.5.96", want: EnvAlipay},
		{name: "browser pc", ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36", want: EnvBrowserPC},
		{name: "browser mobile", ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604.1", want: EnvBrowserMobile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectEnv(tt.ua); got != tt.want {
				t.Fatalf("DetectEnv(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

func TestResolveChooseChannelForBrowserPCWithoutSelection(t *testing.T) {
	svc := NewService(paymgr.NewManager())

	result, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Action != ActionChooseChannel {
		t.Fatalf("Action = %q, want %q", result.Action, ActionChooseChannel)
	}
	if result.Env != EnvBrowserPC {
		t.Fatalf("Env = %q, want %q", result.Env, EnvBrowserPC)
	}
}

func TestResolveRequiresBuilderWhenOrderMustBeCreated(t *testing.T) {
	svc := NewService(paymgr.NewManager())

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:       "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/124.0 Mobile Safari/537.36",
		SelectedChannel: paymgr.ChannelAlipay,
	})
	if !errors.Is(err, ErrMissingOrderBuilder) {
		t.Fatalf("Resolve() error = %v, want ErrMissingOrderBuilder", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./aggregate -run 'TestDetectEnv|TestResolveChooseChannelForBrowserPCWithoutSelection|TestResolveRequiresBuilderWhenOrderMustBeCreated'`
Expected: FAIL because the `aggregate` package does not exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// Package aggregate provides aggregate QR routing on top of paymgr primitives.
package aggregate
```

```go
package aggregate

import "strings"

type Env string

const (
	EnvWechat        Env = "wechat"
	EnvAlipay        Env = "alipay"
	EnvBrowserPC     Env = "browser_pc"
	EnvBrowserMobile Env = "browser_mobile"
)

func DetectEnv(userAgent string) Env {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "micromessenger"):
		return EnvWechat
	case strings.Contains(ua, "alipayclient"):
		return EnvAlipay
	case isMobileUserAgent(ua):
		return EnvBrowserMobile
	default:
		return EnvBrowserPC
	}
}

func isMobileUserAgent(ua string) bool {
	for _, token := range []string{"mobile", "android", "iphone", "ipad"} {
		if strings.Contains(ua, token) {
			return true
		}
	}
	return false
}
```

```go
package aggregate

import "errors"

var (
	ErrMissingOpenID          = errors.New("aggregate: openid is required for wechat jsapi")
	ErrInvalidChannelSelection = errors.New("aggregate: invalid selected channel")
	ErrMissingOrderBuilder    = errors.New("aggregate: build unified order callback is required")
)
```

```go
package aggregate

import (
	"context"
	"fmt"

	"github.com/gtkit/go-pay/paymgr"
)

type Action string

const (
	ActionChooseChannel Action = "choose_channel"
	ActionRedirect      Action = "redirect"
	ActionQRCode        Action = "qr_code"
	ActionJSAPI         Action = "jsapi"
)

type ResolveRequest struct {
	UserAgent       string
	SelectedChannel paymgr.Channel
	OpenID          string

	BuildUnifiedOrder func(ch paymgr.Channel, tt paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error)
}

type ResolveResult struct {
	Env       Env
	Action    Action
	Channel   paymgr.Channel
	TradeType paymgr.TradeType
	Response  *paymgr.UnifiedOrderResponse
}

type Service struct {
	mgr *paymgr.Manager
}

func NewService(mgr *paymgr.Manager) *Service {
	return &Service{mgr: mgr}
}

func (s *Service) Resolve(_ context.Context, req *ResolveRequest) (*ResolveResult, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: resolve request is required", paymgr.ErrInvalidParam)
	}

	env := DetectEnv(req.UserAgent)
	if (env == EnvBrowserPC || env == EnvBrowserMobile) && req.SelectedChannel == "" {
		return &ResolveResult{
			Env:    env,
			Action: ActionChooseChannel,
		}, nil
	}

	if req.BuildUnifiedOrder == nil {
		return nil, ErrMissingOrderBuilder
	}

	return nil, ErrMissingOrderBuilder
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./aggregate -run 'TestDetectEnv|TestResolveChooseChannelForBrowserPCWithoutSelection|TestResolveRequiresBuilderWhenOrderMustBeCreated'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add aggregate/doc.go aggregate/env.go aggregate/errors.go aggregate/service.go aggregate/service_test.go
git commit -m "feat: add aggregate package skeleton"
```

### Task 4: Implement Aggregate Decision Engine

**Files:**
- Modify: `aggregate/service.go`
- Test: `aggregate/service_test.go`

- [ ] **Step 1: Write the failing tests**

```go
type recordingProvider struct {
	ch paymgr.Channel
}

func (p recordingProvider) Channel() paymgr.Channel { return p.ch }

func (p recordingProvider) UnifiedOrder(_ context.Context, req *paymgr.UnifiedOrderRequest) (*paymgr.UnifiedOrderResponse, error) {
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

func (recordingProvider) QueryOrder(context.Context, *paymgr.QueryOrderRequest) (*paymgr.QueryOrderResponse, error) {
	return nil, nil
}
func (recordingProvider) CloseOrder(context.Context, *paymgr.CloseOrderRequest) error { return nil }
func (recordingProvider) Refund(context.Context, *paymgr.RefundRequest) (*paymgr.RefundResponse, error) {
	return nil, nil
}
func (recordingProvider) QueryRefund(context.Context, *paymgr.QueryRefundRequest) (*paymgr.QueryRefundResponse, error) {
	return nil, nil
}
func (recordingProvider) ParseNotify(context.Context, *http.Request) (*paymgr.NotifyResult, error) { return nil, nil }
func (recordingProvider) ParseRefundNotify(context.Context, *http.Request) (*paymgr.RefundNotifyResult, error) {
	return nil, nil
}
func (recordingProvider) ACKNotify(http.ResponseWriter) {}

func newTestService() *Service {
	mgr := paymgr.NewManager()
	mgr.Register(recordingProvider{ch: paymgr.ChannelWechat})
	mgr.Register(recordingProvider{ch: paymgr.ChannelAlipay})
	return NewService(mgr)
}

func TestResolveDecisionMatrix(t *testing.T) {
	tests := []struct {
		name        string
		userAgent   string
		selected    paymgr.Channel
		openID      string
		wantEnv     Env
		wantAction  Action
		wantChannel paymgr.Channel
		wantTrade   paymgr.TradeType
	}{
		{
			name:        "wechat jsapi",
			userAgent:   "Mozilla/5.0 MicroMessenger/8.0.49",
			openID:      "openid-123",
			wantEnv:     EnvWechat,
			wantAction:  ActionJSAPI,
			wantChannel: paymgr.ChannelWechat,
			wantTrade:   paymgr.TradeTypeJSAPI,
		},
		{
			name:        "alipay mobile h5",
			userAgent:   "Mozilla/5.0 AlipayClient/10.5.96 Mobile",
			wantEnv:     EnvAlipay,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypeH5,
		},
		{
			name:        "browser pc wechat native",
			userAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			selected:    paymgr.ChannelWechat,
			wantEnv:     EnvBrowserPC,
			wantAction:  ActionQRCode,
			wantChannel: paymgr.ChannelWechat,
			wantTrade:   paymgr.TradeTypeNative,
		},
		{
			name:        "browser pc alipay page",
			userAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
			selected:    paymgr.ChannelAlipay,
			wantEnv:     EnvBrowserPC,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypePage,
		},
		{
			name:        "browser mobile alipay h5",
			userAgent:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604.1",
			selected:    paymgr.ChannelAlipay,
			wantEnv:     EnvBrowserMobile,
			wantAction:  ActionRedirect,
			wantChannel: paymgr.ChannelAlipay,
			wantTrade:   paymgr.TradeTypeH5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			var gotChannel paymgr.Channel
			var gotTradeType paymgr.TradeType

			result, err := svc.Resolve(t.Context(), &ResolveRequest{
				UserAgent:       tt.userAgent,
				SelectedChannel: tt.selected,
				OpenID:          tt.openID,
				BuildUnifiedOrder: func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
					gotChannel = ch
					gotTradeType = tradeType
					return &paymgr.UnifiedOrderRequest{
						OutTradeNo:  "ORD-AGG-1",
						TotalAmount: 100,
						Subject:     "aggregate order",
						TradeType:   tradeType,
						NotifyURL:   "https://example.com/notify",
						ReturnURL:   "https://example.com/return",
						ClientIP:    "203.0.113.10",
						OpenID:      "openid-123",
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if gotChannel != tt.wantChannel || gotTradeType != tt.wantTrade {
				t.Fatalf("builder got (%q, %q), want (%q, %q)", gotChannel, gotTradeType, tt.wantChannel, tt.wantTrade)
			}
			if result.Env != tt.wantEnv {
				t.Fatalf("Env = %q, want %q", result.Env, tt.wantEnv)
			}
			if result.Action != tt.wantAction {
				t.Fatalf("Action = %q, want %q", result.Action, tt.wantAction)
			}
			if result.Channel != tt.wantChannel {
				t.Fatalf("Channel = %q, want %q", result.Channel, tt.wantChannel)
			}
			if result.TradeType != tt.wantTrade {
				t.Fatalf("TradeType = %q, want %q", result.TradeType, tt.wantTrade)
			}
		})
	}
}

func TestResolveValidationErrors(t *testing.T) {
	svc := newTestService()
	builder := func(ch paymgr.Channel, tradeType paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
		return &paymgr.UnifiedOrderRequest{
			OutTradeNo:  "ORD-AGG-ERR",
			TotalAmount: 100,
			Subject:     "aggregate order",
			TradeType:   tradeType,
			NotifyURL:   "https://example.com/notify",
			ReturnURL:   "https://example.com/return",
			ClientIP:    "203.0.113.10",
		}, nil
	}

	_, err := svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:         "Mozilla/5.0 MicroMessenger/8.0.49",
		BuildUnifiedOrder: builder,
	})
	if !errors.Is(err, ErrMissingOpenID) {
		t.Fatalf("Resolve() error = %v, want ErrMissingOpenID", err)
	}

	_, err = svc.Resolve(t.Context(), &ResolveRequest{
		UserAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36",
		SelectedChannel:   paymgr.Channel("unionpay"),
		BuildUnifiedOrder: builder,
	})
	if !errors.Is(err, ErrInvalidChannelSelection) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidChannelSelection", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./aggregate -run 'TestResolveDecisionMatrix|TestResolveValidationErrors'`
Expected: FAIL because `Resolve` only supports the skeleton paths

- [ ] **Step 3: Write minimal implementation**

```go
package aggregate

import (
	"context"
	"fmt"
	"strings"

	"github.com/gtkit/go-pay/paymgr"
)

func (s *Service) Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: resolve request is required", paymgr.ErrInvalidParam)
	}

	env := DetectEnv(req.UserAgent)
	if (env == EnvBrowserPC || env == EnvBrowserMobile) && req.SelectedChannel == "" {
		return &ResolveResult{Env: env, Action: ActionChooseChannel}, nil
	}

	channel, tradeType, action, err := resolveDecision(env, req.UserAgent, req.SelectedChannel)
	if err != nil {
		return nil, err
	}
	if env == EnvWechat && req.OpenID == "" {
		return nil, ErrMissingOpenID
	}
	if req.BuildUnifiedOrder == nil {
		return nil, ErrMissingOrderBuilder
	}

	orderReq, err := req.BuildUnifiedOrder(channel, tradeType)
	if err != nil {
		return nil, err
	}
	if orderReq == nil {
		return nil, fmt.Errorf("%w: build unified order returned nil request", paymgr.ErrInvalidParam)
	}

	orderReq.TradeType = tradeType
	if env == EnvWechat && orderReq.OpenID == "" {
		orderReq.OpenID = req.OpenID
	}

	resp, err := s.mgr.UnifiedOrder(ctx, channel, orderReq)
	if err != nil {
		return nil, err
	}

	return &ResolveResult{
		Env:       env,
		Action:    action,
		Channel:   channel,
		TradeType: tradeType,
		Response:  resp,
	}, nil
}

func resolveDecision(env Env, userAgent string, selected paymgr.Channel) (paymgr.Channel, paymgr.TradeType, Action, error) {
	switch env {
	case EnvWechat:
		return paymgr.ChannelWechat, paymgr.TradeTypeJSAPI, ActionJSAPI, nil
	case EnvAlipay:
		if isMobileUserAgent(strings.ToLower(userAgent)) {
			return paymgr.ChannelAlipay, paymgr.TradeTypeH5, ActionRedirect, nil
		}
		return paymgr.ChannelAlipay, paymgr.TradeTypePage, ActionRedirect, nil
	case EnvBrowserMobile:
		switch selected {
		case paymgr.ChannelWechat:
			return paymgr.ChannelWechat, paymgr.TradeTypeH5, ActionRedirect, nil
		case paymgr.ChannelAlipay:
			return paymgr.ChannelAlipay, paymgr.TradeTypeH5, ActionRedirect, nil
		default:
			return "", "", "", ErrInvalidChannelSelection
		}
	case EnvBrowserPC:
		switch selected {
		case paymgr.ChannelWechat:
			return paymgr.ChannelWechat, paymgr.TradeTypeNative, ActionQRCode, nil
		case paymgr.ChannelAlipay:
			return paymgr.ChannelAlipay, paymgr.TradeTypePage, ActionRedirect, nil
		default:
			return "", "", "", ErrInvalidChannelSelection
		}
	default:
		return "", "", "", fmt.Errorf("%w: unsupported env %q", paymgr.ErrInvalidParam, env)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./aggregate -run 'TestResolveDecisionMatrix|TestResolveValidationErrors'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add aggregate/service.go aggregate/service_test.go
git commit -m "feat: add aggregate qr resolver"
```

### Task 5: Update Docs And Example

**Files:**
- Modify: `README.md`
- Modify: `example/main.go`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Write the failing documentation expectations**

```text
README must list TradeTypePage in the trade-type enum and the Alipay support matrix.
README must add an Alipay PC page-pay example and an aggregate QR orchestration example.
example/main.go must describe pay_url as Alipay H5 / PC page pay.
CHANGELOG must mention both TradeTypePage and aggregate QR orchestration.
```

- [ ] **Step 2: Verify docs are stale**

Run: `grep -n 'TradeTypePage\\|aggregate' README.md example/main.go CHANGELOG.md`
Expected: No `TradeTypePage` direct-pay docs and no aggregate orchestration docs yet

- [ ] **Step 3: Update documentation**

```markdown
paymgr.TradeTypePage   // PC 网页支付 / 支付宝收银台
```

```markdown
| 支付宝 | `native`、`jsapi`、`app`、`h5`、`page` |
```

```go
resp, err := mgr.UnifiedOrder(ctx, paymgr.ChannelAlipay, &paymgr.UnifiedOrderRequest{
	OutTradeNo:  "ORD202603250007",
	TotalAmount: 100,
	Subject:     "支付宝PC订单",
	TradeType:   paymgr.TradeTypePage,
	NotifyURL:   "https://api.example.com/pay/notify/alipay",
	ReturnURL:   "https://www.example.com/pay/return",
})
if err != nil {
	return err
}

pageURL := resp.PayURL
```

```go
resolver := aggregate.NewService(mgr)
result, err := resolver.Resolve(ctx, &aggregate.ResolveRequest{
	UserAgent:       r.UserAgent(),
	SelectedChannel: paymgr.Channel(r.URL.Query().Get("channel")),
	OpenID:          openID,
	BuildUnifiedOrder: func(ch paymgr.Channel, tt paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error) {
		return &paymgr.UnifiedOrderRequest{
			OutTradeNo:  orderNum,
			TotalAmount: amount,
			Subject:     subject,
			TradeType:   tt,
			NotifyURL:   notifyURL,
			ReturnURL:   returnURL,
			ClientIP:    clientIP,
			OpenID:      openID,
		}, nil
	},
})
```

```go
"pay_url": resp.PayURL, // 支付宝 H5 / PC 页面支付跳转 URL
```

- [ ] **Step 4: Verify docs mention the new behavior**

Run: `grep -n 'TradeTypePage\\|aggregate.NewService\\|支付宝 H5 / PC 页面支付' README.md example/main.go CHANGELOG.md`
Expected: Matches found in all three files

- [ ] **Step 5: Commit**

```bash
git add README.md example/main.go CHANGELOG.md
git commit -m "docs: add page pay and aggregate qr usage"
```

### Task 6: Final Verification

**Files:**
- Test: `paymgr/payment_test.go`
- Test: `alipay/provider_test.go`
- Test: `aggregate/service_test.go`

- [ ] **Step 1: Run focused package tests**

Run: `go test ./paymgr ./alipay ./aggregate`
Expected: PASS

- [ ] **Step 2: Run full repository tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Inspect final diff**

Run: `git diff --stat`
Expected: Only the planned files changed

- [ ] **Step 4: Commit final verification-ready state**

```bash
git add paymgr/payment.go paymgr/payment_test.go alipay/provider.go alipay/provider_test.go aggregate/doc.go aggregate/env.go aggregate/errors.go aggregate/service.go aggregate/service_test.go README.md example/main.go CHANGELOG.md docs/superpowers/plans/2026-04-24-alipay-page-and-aggregate-qr.md
git commit -m "feat: add alipay page pay and aggregate qr orchestration"
```
