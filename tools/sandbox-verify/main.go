// Command sandbox-verify 在支付宝沙箱环境跑一遍 v1.3.0 重写后的核心路径，输出 JSON 报告。
//
// 用法：
//
//	export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
//	export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
//	export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
//	export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
//	export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
//	# 可选：
//	export ALIPAY_SANDBOX_BUYER_ID="2088xxxxxxxxxxxx"      # 测 JSAPI 时填
//	export ALIPAY_SANDBOX_NOTIFY_URL="https://example.com/notify"
//	export ALIPAY_SANDBOX_RETURN_URL="https://example.com/return"
//
//	go run ./tools/sandbox-verify
//
// 工具仅做不需要人工干预的路径验证，**不**做：真实支付完成、回调验签、退款全流程。
// 详见 tools/sandbox-verify/README.md。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gtkit/go-pay/alipay"
	"github.com/gtkit/go-pay/paymgr"
)

const (
	envAppID          = "ALIPAY_SANDBOX_APP_ID"
	envPrivateKeyPath = "ALIPAY_SANDBOX_PRIVATE_KEY_PATH"
	envAppCertPath    = "ALIPAY_SANDBOX_APP_CERT_PATH"
	envRootCertPath   = "ALIPAY_SANDBOX_ROOT_CERT_PATH"
	envPublicCertPath = "ALIPAY_SANDBOX_PUBLIC_CERT_PATH"
	envBuyerID        = "ALIPAY_SANDBOX_BUYER_ID"
	envNotifyURL      = "ALIPAY_SANDBOX_NOTIFY_URL"
	envReturnURL      = "ALIPAY_SANDBOX_RETURN_URL"
)

type config struct {
	AppID          string
	PrivateKeyPath string
	AppCertPath    string
	RootCertPath   string
	PublicCertPath string
	BuyerID        string
	NotifyURL      string
	ReturnURL      string
}

type checkResult struct {
	Name   string         `json:"name"`
	Status string         `json:"status"` // pass / fail / skip
	Detail map[string]any `json:"detail,omitempty"`
	Error  string         `json:"error,omitempty"`
	Note   string         `json:"note,omitempty"`
}

type report struct {
	GoPayVersion string        `json:"go_pay_version"`
	GeneratedAt  string        `json:"generated_at"`
	Environment  envSnapshot   `json:"environment"`
	Summary      summary       `json:"summary"`
	Results      []checkResult `json:"results"`
}

type envSnapshot struct {
	AppID     string `json:"app_id_masked"`
	IsProd    bool   `json:"is_prod"`
	NotifyURL string `json:"notify_url,omitempty"`
	ReturnURL string `json:"return_url,omitempty"`
	BuyerID   string `json:"buyer_id_masked,omitempty"`
}

type summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置加载失败：", err)
		fmt.Fprintln(os.Stderr, "\n请确保设置以下必填环境变量：")
		fmt.Fprintln(os.Stderr, "  "+envAppID)
		fmt.Fprintln(os.Stderr, "  "+envPrivateKeyPath)
		fmt.Fprintln(os.Stderr, "  "+envAppCertPath)
		fmt.Fprintln(os.Stderr, "  "+envRootCertPath)
		fmt.Fprintln(os.Stderr, "  "+envPublicCertPath)
		os.Exit(2)
	}

	rep := &report{
		GoPayVersion: "v1.3.0",
		GeneratedAt:  time.Now().Format(time.RFC3339),
		Environment: envSnapshot{
			AppID:     mask(cfg.AppID),
			IsProd:    false,
			NotifyURL: cfg.NotifyURL,
			ReturnURL: cfg.ReturnURL,
			BuyerID:   mask(cfg.BuyerID),
		},
	}

	rep.Results = append(rep.Results, checkPublicKeySoftDegradation(cfg))

	provider, err := newCertModeProvider(cfg)
	if err != nil {
		rep.Results = append(rep.Results, checkResult{
			Name:   "init_cert_mode",
			Status: "fail",
			Error:  err.Error(),
			Note:   "证书模式初始化失败，后续所有依赖 provider 的检查跳过。请验证证书文件路径与商户 APPID 匹配",
		})
		finalize(rep)
		return
	}
	rep.Results = append(rep.Results, checkResult{
		Name:   "init_cert_mode",
		Status: "pass",
	})

	ctx := context.Background()

	rep.Results = append(rep.Results,
		checkUnifiedOrder(ctx, provider, cfg, paymgr.TradeTypePage),
		checkUnifiedOrder(ctx, provider, cfg, paymgr.TradeTypeH5),
		checkUnifiedOrder(ctx, provider, cfg, paymgr.TradeTypeApp),
		checkUnifiedOrder(ctx, provider, cfg, paymgr.TradeTypeNative),
	)

	if cfg.BuyerID != "" {
		rep.Results = append(rep.Results, checkUnifiedOrder(ctx, provider, cfg, paymgr.TradeTypeJSAPI))
	} else {
		rep.Results = append(rep.Results, checkResult{
			Name:   "trade_type_jsapi",
			Status: "skip",
			Note:   "未设置 " + envBuyerID + "，跳过 JSAPI 验证",
		})
	}

	rep.Results = append(rep.Results, checkQueryNonexistentOrder(ctx, provider))
	rep.Results = append(rep.Results, checkCloseNonexistentOrder(ctx, provider))

	finalize(rep)
}

func loadConfig() (*config, error) {
	required := map[string]string{
		envAppID:          os.Getenv(envAppID),
		envPrivateKeyPath: os.Getenv(envPrivateKeyPath),
		envAppCertPath:    os.Getenv(envAppCertPath),
		envRootCertPath:   os.Getenv(envRootCertPath),
		envPublicCertPath: os.Getenv(envPublicCertPath),
	}
	var missing []string
	for k, v := range required {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return &config{
		AppID:          required[envAppID],
		PrivateKeyPath: required[envPrivateKeyPath],
		AppCertPath:    required[envAppCertPath],
		RootCertPath:   required[envRootCertPath],
		PublicCertPath: required[envPublicCertPath],
		BuyerID:        os.Getenv(envBuyerID),
		NotifyURL:      os.Getenv(envNotifyURL),
		ReturnURL:      os.Getenv(envReturnURL),
	}, nil
}

func newCertModeProvider(cfg *config) (*alipay.Provider, error) {
	return alipay.NewProvider(
		alipay.WithAppID(cfg.AppID),
		alipay.WithPrivateKeyPath(cfg.PrivateKeyPath),
		alipay.WithProduction(false),
		alipay.WithCertModePaths(cfg.AppCertPath, cfg.RootCertPath, cfg.PublicCertPath),
	)
}

func checkPublicKeySoftDegradation(cfg *config) checkResult {
	const name = "raw_public_key_soft_degradation"
	_, err := alipay.NewProvider(
		alipay.WithAppID(cfg.AppID),
		alipay.WithPrivateKeyPath(cfg.PrivateKeyPath),
		alipay.WithProduction(false),
		alipay.WithAlipayPublicKey("dummy-public-key-for-soft-degradation-test"),
	)
	if err == nil {
		return checkResult{
			Name:   name,
			Status: "fail",
			Note:   "未触发软降级！预期 ErrNotSupported，实际 nil error",
		}
	}
	if !errors.Is(err, paymgr.ErrNotSupported) {
		return checkResult{
			Name:   name,
			Status: "fail",
			Error:  err.Error(),
			Note:   "返回了错误，但不是 paymgr.ErrNotSupported（可能是 Validate 提前拒绝）",
		}
	}
	return checkResult{
		Name:   name,
		Status: "pass",
		Detail: map[string]any{
			"matches_err_not_supported": true,
			"error_message":             err.Error(),
		},
	}
}

func checkUnifiedOrder(ctx context.Context, p *alipay.Provider, cfg *config, tt paymgr.TradeType) checkResult {
	name := fmt.Sprintf("trade_type_%s", tt)
	req := &paymgr.UnifiedOrderRequest{
		OutTradeNo:  fmt.Sprintf("SBX-%s-%d", strings.ToUpper(string(tt)), time.Now().UnixNano()),
		TotalAmount: 1, // 1 分
		Subject:     fmt.Sprintf("v1.3.0 sandbox %s", tt),
		TradeType:   tt,
		NotifyURL:   defaultIfEmpty(cfg.NotifyURL, "https://example.com/notify"),
		ReturnURL:   defaultIfEmpty(cfg.ReturnURL, "https://example.com/return"),
	}
	if tt == paymgr.TradeTypeJSAPI {
		req.OpenID = cfg.BuyerID
	}

	resp, err := p.UnifiedOrder(ctx, req)
	if err != nil {
		return failedCheck(name, err, "下单失败，可能 R1 风险命中：BodyMap 字段名 / 请求参数 / 沙箱配置异常")
	}

	detail := map[string]any{
		"out_trade_no": req.OutTradeNo,
	}
	switch tt {
	case paymgr.TradeTypePage, paymgr.TradeTypeH5:
		detail["pay_url_len"] = len(resp.PayURL)
		detail["pay_url"] = resp.PayURL // 完整 URL，用于浏览器打开真实支付
		if resp.PayURL == "" {
			return checkResult{Name: name, Status: "fail", Detail: detail, Note: "PayURL 为空"}
		}
	case paymgr.TradeTypeApp:
		detail["app_params_len"] = len(resp.AppParams)
		detail["app_params_head"] = headOf(resp.AppParams, 120)
		if resp.AppParams == "" {
			return checkResult{Name: name, Status: "fail", Detail: detail, Note: "AppParams 为空"}
		}
	case paymgr.TradeTypeNative:
		detail["code_url"] = resp.CodeURL
		if resp.CodeURL == "" {
			return checkResult{Name: name, Status: "fail", Detail: detail, Note: "CodeURL 为空"}
		}
	case paymgr.TradeTypeJSAPI:
		detail["prepay_id"] = resp.PrepayID
		if resp.PrepayID == "" {
			return checkResult{Name: name, Status: "fail", Detail: detail, Note: "PrepayID 为空"}
		}
	}

	return checkResult{Name: name, Status: "pass", Detail: detail}
}

func checkQueryNonexistentOrder(ctx context.Context, p *alipay.Provider) checkResult {
	const name = "query_nonexistent_order"
	req := &paymgr.QueryOrderRequest{
		OutTradeNo: fmt.Sprintf("NONEXISTENT-%d", time.Now().UnixNano()),
	}
	_, err := p.QueryOrder(ctx, req)
	if err == nil {
		return checkResult{
			Name:   name,
			Status: "fail",
			Note:   "查询不存在订单返回 nil error，预期 ErrOrderNotFound 或 ChannelError",
		}
	}

	detail := map[string]any{
		"error_message":               err.Error(),
		"matches_err_order_not_found": errors.Is(err, paymgr.ErrOrderNotFound),
	}

	if errors.Is(err, paymgr.ErrOrderNotFound) {
		return checkResult{Name: name, Status: "pass", Detail: detail}
	}

	var chErr *paymgr.ChannelError
	if errors.As(err, &chErr) {
		detail["channel_error_code"] = chErr.Code
		detail["channel_error_message"] = chErr.Message
		detail["channel"] = string(chErr.Channel)
	}

	return checkResult{
		Name:   name,
		Status: "fail",
		Detail: detail,
		Note:   "⚠ R1 风险命中：v3 协议错误码不再是 ACQ.TRADE_NOT_EXIST，需修 alipay/provider.go QueryOrder/QueryRefund 的判断逻辑（替换为 detail 中实际看到的 code）",
	}
}

func checkCloseNonexistentOrder(ctx context.Context, p *alipay.Provider) checkResult {
	const name = "close_nonexistent_order"
	req := &paymgr.CloseOrderRequest{
		OutTradeNo: fmt.Sprintf("NONEXISTENT-CLOSE-%d", time.Now().UnixNano()),
	}
	err := p.CloseOrder(ctx, req)
	if err == nil {
		return checkResult{
			Name:   name,
			Status: "pass",
			Detail: map[string]any{
				"note": "支付宝对不存在订单的 Close 返回成功，与现有 ACQ.TRADE_HAS_CLOSE 兼容路径行为一致",
			},
		}
	}

	detail := map[string]any{"error_message": err.Error()}
	var chErr *paymgr.ChannelError
	if errors.As(err, &chErr) {
		detail["channel_error_code"] = chErr.Code
		detail["channel_error_message"] = chErr.Message
	}

	return checkResult{
		Name:   name,
		Status: "pass", // 不判断 pass/fail，仅采集错误码
		Detail: detail,
		Note:   "采集错误码格式以验证 v3 协议错误码 prefix（参考此处 channel_error_code）",
	}
}

func finalize(rep *report) {
	for _, r := range rep.Results {
		rep.Summary.Total++
		switch r.Status {
		case "pass":
			rep.Summary.Passed++
		case "fail":
			rep.Summary.Failed++
		case "skip":
			rep.Summary.Skipped++
		}
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal report failed:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
	if rep.Summary.Failed > 0 {
		os.Exit(1)
	}
}

func failedCheck(name string, err error, note string) checkResult {
	detail := map[string]any{"error_message": err.Error()}
	var chErr *paymgr.ChannelError
	if errors.As(err, &chErr) {
		detail["channel_error_code"] = chErr.Code
		detail["channel_error_message"] = chErr.Message
	}
	return checkResult{
		Name:   name,
		Status: "fail",
		Detail: detail,
		Error:  err.Error(),
		Note:   note,
	}
}

func headOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func mask(s string) string {
	if len(s) < 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
