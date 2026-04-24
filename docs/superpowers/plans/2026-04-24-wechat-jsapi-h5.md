# Wechat JSAPI H5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add WeChat direct-merchant `payments/jsapi` and `payments/h5` support to the unified payment interface without changing the public `paymgr.Provider` contract.

**Architecture:** Keep the existing `paymgr` abstraction unchanged and extend only the WeChat provider's `UnifiedOrder` trade-type branches. Reuse existing request fields (`OpenID`, `ClientIP`) and response fields (`JSAPIParams`, `H5URL`) so existing callers gain new capability without API breakage.

**Tech Stack:** Go, `github.com/wechatpay-apiv3/wechatpay-go`, table-driven unit tests, `go test`

---

### Task 1: Lock Request Validation Semantics

**Files:**
- Modify: `paymgr/payment.go`
- Test: `paymgr/payment_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestUnifiedOrderRequestValidate_RequiresOpenIDForJSAPI(t *testing.T) {
	req := &UnifiedOrderRequest{
		OutTradeNo:  "ORD-1",
		TotalAmount: 100,
		Subject:     "demo",
		TradeType:   TradeTypeJSAPI,
		NotifyURL:   "https://example.com/notify",
	}

	err := req.Validate()

	if err == nil || !errors.Is(err, ErrInvalidParam) || !strings.Contains(err.Error(), "openid is required") {
		t.Fatalf("expected openid validation error, got %v", err)
	}
}

func TestUnifiedOrderRequestValidate_RequiresClientIPForWechatH5(t *testing.T) {
	req := &UnifiedOrderRequest{
		OutTradeNo:  "ORD-2",
		TotalAmount: 100,
		Subject:     "demo",
		TradeType:   TradeTypeH5,
		NotifyURL:   "https://example.com/notify",
	}

	err := req.Validate()

	if err == nil || !errors.Is(err, ErrInvalidParam) || !strings.Contains(err.Error(), "client_ip is required") {
		t.Fatalf("expected client_ip validation error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./paymgr -run 'TestUnifiedOrderRequestValidate_(RequiresOpenIDForJSAPI|RequiresClientIPForWechatH5)'`
Expected: FAIL because current validation does not enforce these fields

- [ ] **Step 3: Write minimal implementation**

```go
if r.TradeType == TradeTypeJSAPI && r.OpenID == "" {
	return fmt.Errorf("%w: openid is required for jsapi", ErrInvalidParam)
}
if r.TradeType == TradeTypeH5 && r.ClientIP == "" {
	return fmt.Errorf("%w: client_ip is required for h5", ErrInvalidParam)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./paymgr -run 'TestUnifiedOrderRequestValidate_(RequiresOpenIDForJSAPI|RequiresClientIPForWechatH5)'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add paymgr/payment.go paymgr/payment_test.go
git commit -m "test: enforce unified order field requirements"
```

### Task 2: Add WeChat JSAPI Unified Order Support

**Files:**
- Modify: `wechat/provider.go`
- Test: `wechat/provider_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildJSAPIPayParams(t *testing.T) {
	p := &Provider{
		cfg: &Config{AppID: "wx123"},
	}

	got, err := p.buildJSAPIPayParams("prepay-id")
	if err != nil {
		t.Fatalf("buildJSAPIPayParams error: %v", err)
	}
	if !strings.Contains(got, "\"appId\":\"wx123\"") {
		t.Fatalf("expected appId in params, got %s", got)
	}
	if !strings.Contains(got, "\"package\":\"prepay_id=prepay-id\"") {
		t.Fatalf("expected package in params, got %s", got)
	}
	if !strings.Contains(got, "\"signType\":\"RSA\"") {
		t.Fatalf("expected signType in params, got %s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wechat -run TestBuildJSAPIPayParams`
Expected: FAIL because helper does not exist

- [ ] **Step 3: Write minimal implementation**

```go
func (p *Provider) buildJSAPIPayParams(prepayID string) (string, error) {
	// build nonce/timestamp
	// sign message: appid + "\n" + timestamp + "\n" + nonce + "\n" + "prepay_id="+prepayID + "\n"
	// return JSON with appId, timeStamp, nonceStr, package, signType, paySign
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wechat -run TestBuildJSAPIPayParams`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wechat/provider.go wechat/provider_test.go
git commit -m "feat: add wechat jsapi pay params"
```

### Task 3: Add WeChat H5 URL Handling And Trade-Type Branches

**Files:**
- Modify: `wechat/provider.go`
- Test: `wechat/provider_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestUnifiedOrderUnsupportedTypeMessageIncludesJSAPIAndH5(t *testing.T) {
	p := &Provider{}

	_, err := p.UnifiedOrder(context.Background(), &paymgr.UnifiedOrderRequest{
		TradeType: paymgr.TradeTypeJSAPI,
	})

	if err == nil {
		t.Fatal("expected error")
	}
}
```

```go
func TestDerefStringHandlesNil(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wechat -run 'TestUnifiedOrderUnsupportedTypeMessageIncludesJSAPIAndH5|TestDerefStringHandlesNil'`
Expected: FAIL because JSAPI/H5 branches are not implemented and supported type text is stale

- [ ] **Step 3: Write minimal implementation**

```go
case paymgr.TradeTypeJSAPI:
	svc := jsapi.JsapiApiService{Client: p.client}
	result, _, err := svc.Prepay(ctx, jsapi.PrepayRequest{... Openid: core.String(req.OpenID) ...})
	resp.PrepayID = *result.PrepayId
	resp.JSAPIParams, err = p.buildJSAPIPayParams(*result.PrepayId)

case paymgr.TradeTypeH5:
	svc := h5.H5ApiService{Client: p.client}
	result, _, err := svc.Prepay(ctx, h5.PrepayRequest{... SceneInfo: &h5.SceneInfo{PayerClientIp: core.String(req.ClientIP)} ...})
	resp.H5URL = derefString(result.H5Url)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wechat -run 'TestUnifiedOrderUnsupportedTypeMessageIncludesJSAPIAndH5|TestDerefStringHandlesNil'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wechat/provider.go wechat/provider_test.go
git commit -m "feat: add wechat jsapi and h5 unified order"
```

### Task 4: Document The New WeChat Support Matrix

**Files:**
- Modify: `README.md`
- Modify: `example/main.go`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Write the failing documentation expectations**

```text
README must state that WeChat now supports app, native, jsapi, h5.
Example request docs must mention OpenID for jsapi and ClientIP for h5.
CHANGELOG must describe the newly exposed trade types.
```

- [ ] **Step 2: Verify docs are stale**

Run: `grep -n '微信支付 | `app`、`native`' README.md`
Expected: Match found, proving docs still describe the old matrix

- [ ] **Step 3: Update documentation**

```markdown
| 微信支付 | `app`、`native`、`jsapi`、`h5` |
```

```go
// JSAPI requires OpenID.
// H5 requires ClientIP.
```

- [ ] **Step 4: Verify docs mention the new support**

Run: `grep -n '微信支付 | `app`、`native`、`jsapi`、`h5`' README.md`
Expected: Match found for the new matrix

- [ ] **Step 5: Commit**

```bash
git add README.md example/main.go CHANGELOG.md
git commit -m "docs: document wechat jsapi and h5 support"
```

### Task 5: Final Verification

**Files:**
- Test: `paymgr/payment_test.go`
- Test: `wechat/provider_test.go`

- [ ] **Step 1: Run focused package tests**

Run: `go test ./paymgr ./wechat`
Expected: PASS

- [ ] **Step 2: Run full repository tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Inspect final diff**

Run: `git diff --stat`
Expected: Only the planned files changed

- [ ] **Step 4: Commit final verification-ready state**

```bash
git add paymgr/payment.go paymgr/payment_test.go wechat/provider.go wechat/provider_test.go README.md example/main.go CHANGELOG.md docs/superpowers/plans/2026-04-24-wechat-jsapi-h5.md
git commit -m "feat: add wechat jsapi and h5 support"
```
