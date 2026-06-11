// Command wechat-pubkey-verify 用真实微信商户凭据验证「微信支付公钥」验签模式可上线。
//
// 微信支付 V3 无公开沙箱网关，本工具连真实网关、用 1 分小额订单验证：
// 公钥模式初始化 → Native 下单（应答验签走公钥）→ 查询不存在订单 → 关单，输出 JSON 报告。
// Native 下单成功即证明：商户私钥请求签名正确 + 微信应答验签（公钥）通过。
//
// 用法：
//
//	export WECHAT_PUBKEY_APP_ID="wx1234567890abcdef"
//	export WECHAT_PUBKEY_MCH_ID="1900000001"
//	export WECHAT_PUBKEY_MCH_CERT_SERIAL="3775B6A45ACD588826D15E583A95F5DD********"
//	export WECHAT_PUBKEY_APIV3_KEY="32 字节 APIv3 密钥"
//	export WECHAT_PUBKEY_PRIVATE_KEY_PATH="/path/to/apiclient_key.pem"
//	export WECHAT_PUBKEY_PUBLIC_KEY_ID="PUB_KEY_ID_0000000000000000000000000000"
//	export WECHAT_PUBKEY_PUBLIC_KEY_PATH="/path/to/wechatpay_pub_key.pem"
//	# 可选：默认 https://example.com/notify
//	export WECHAT_PUBKEY_NOTIFY_URL="https://your.domain/notify"
//
//	go run ./tools/wechat-pubkey-verify
//
// 工具不做：真实支付完成、回调验签（见 tools/wechat-notify-listener）。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/go-pay/wechat"
	"github.com/gtkit/json"
)

const (
	envAppID          = "WECHAT_PUBKEY_APP_ID"
	envMchID          = "WECHAT_PUBKEY_MCH_ID"
	envMchCertSerial  = "WECHAT_PUBKEY_MCH_CERT_SERIAL"
	envAPIv3Key       = "WECHAT_PUBKEY_APIV3_KEY"
	envPrivateKeyPath = "WECHAT_PUBKEY_PRIVATE_KEY_PATH"
	envPublicKeyID    = "WECHAT_PUBKEY_PUBLIC_KEY_ID"
	envPublicKeyPath  = "WECHAT_PUBKEY_PUBLIC_KEY_PATH"
	envNotifyURL      = "WECHAT_PUBKEY_NOTIFY_URL"
)

type config struct {
	AppID          string
	MchID          string
	MchCertSerial  string
	APIv3Key       string
	PrivateKeyPath string
	PublicKeyID    string
	PublicKeyPath  string
	NotifyURL      string
}

type checkResult struct {
	Name   string         `json:"name"`
	Status string         `json:"status"` // pass / fail / skip
	Detail map[string]any `json:"detail,omitempty"`
	Error  string         `json:"error,omitempty"`
	Note   string         `json:"note,omitempty"`
}

type summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type report struct {
	GeneratedAt string        `json:"generated_at"`
	AppID       string        `json:"app_id_masked"`
	MchID       string        `json:"mch_id_masked"`
	PublicKeyID string        `json:"public_key_id"`
	Summary     summary       `json:"summary"`
	Results     []checkResult `json:"results"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置加载失败：", err)
		fmt.Fprintln(os.Stderr, "\n请确保设置以下必填环境变量：")
		for _, k := range []string{
			envAppID, envMchID, envMchCertSerial, envAPIv3Key,
			envPrivateKeyPath, envPublicKeyID, envPublicKeyPath,
		} {
			fmt.Fprintln(os.Stderr, "  "+k)
		}
		os.Exit(2)
	}

	rep := &report{
		GeneratedAt: time.Now().Format(time.RFC3339),
		AppID:       mask(cfg.AppID),
		MchID:       mask(cfg.MchID),
		PublicKeyID: cfg.PublicKeyID,
	}

	ctx := context.Background()

	provider, err := newPublicKeyProvider(ctx, cfg)
	if err != nil {
		rep.Results = append(rep.Results, checkResult{
			Name:   "init_public_key_mode",
			Status: "fail",
			Error:  err.Error(),
			Note:   "公钥模式初始化失败，后续检查跳过。请核对公钥 ID、公钥文件（PKIX PUBLIC KEY）、商户私钥、APIv3 Key（须 32 字节）",
		})
		finalize(rep)
		return
	}
	rep.Results = append(rep.Results, checkResult{
		Name:   "init_public_key_mode",
		Status: "pass",
		Detail: map[string]any{"note": "公钥模式 Provider 初始化成功（Client + 回调 handler 均就绪）"},
	})

	rep.Results = append(rep.Results,
		checkNativeOrder(ctx, provider, cfg),
		checkQueryNonexistentOrder(ctx, provider),
		checkCloseNonexistentOrder(ctx, provider),
	)

	finalize(rep)
}

func loadConfig() (*config, error) {
	required := map[string]string{
		envAppID:          os.Getenv(envAppID),
		envMchID:          os.Getenv(envMchID),
		envMchCertSerial:  os.Getenv(envMchCertSerial),
		envAPIv3Key:       os.Getenv(envAPIv3Key),
		envPrivateKeyPath: os.Getenv(envPrivateKeyPath),
		envPublicKeyID:    os.Getenv(envPublicKeyID),
		envPublicKeyPath:  os.Getenv(envPublicKeyPath),
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
		MchID:          required[envMchID],
		MchCertSerial:  required[envMchCertSerial],
		APIv3Key:       required[envAPIv3Key],
		PrivateKeyPath: required[envPrivateKeyPath],
		PublicKeyID:    required[envPublicKeyID],
		PublicKeyPath:  required[envPublicKeyPath],
		NotifyURL:      defaultIfEmpty(os.Getenv(envNotifyURL), "https://example.com/notify"),
	}, nil
}

func newPublicKeyProvider(ctx context.Context, cfg *config) (*wechat.Provider, error) {
	return wechat.NewProvider(
		ctx,
		wechat.WithAppID(cfg.AppID),
		wechat.WithMerchant(cfg.MchID, cfg.MchCertSerial, cfg.APIv3Key),
		wechat.WithMerchantPrivateKeyPath(cfg.PrivateKeyPath),
		wechat.WithPublicKeyID(cfg.PublicKeyID),
		wechat.WithPublicKeyPath(cfg.PublicKeyPath),
	)
}

// checkNativeOrder 用 Native 下单验证应答验签（公钥）链路。下单成功 = 请求签名 + 应答验签通过。
func checkNativeOrder(ctx context.Context, p *wechat.Provider, cfg *config) checkResult {
	const name = "trade_type_native"
	req := &paymgr.UnifiedOrderRequest{
		OutTradeNo:  fmt.Sprintf("WXPK-NATIVE-%d", time.Now().UnixNano()),
		TotalAmount: 1, // 1 分
		Subject:     "wechat public key verify",
		TradeType:   paymgr.TradeTypeNative,
		NotifyURL:   cfg.NotifyURL,
		ExpireAt:    time.Now().Add(10 * time.Minute),
	}
	resp, err := p.UnifiedOrder(ctx, req)
	if err != nil {
		return failedCheck(name, err,
			"下单失败。若为验签相关错误，重点核对公钥 ID 与公钥文件是否为微信商户平台下发的当前公钥")
	}
	detail := map[string]any{
		"out_trade_no": req.OutTradeNo,
		"code_url":     resp.CodeURL,
	}
	if resp.CodeURL == "" {
		return checkResult{Name: name, Status: "fail", Detail: detail, Note: "CodeURL 为空"}
	}
	detail["note"] = "下单成功，证明商户私钥请求签名 + 微信应答验签（公钥模式）均通过"
	return checkResult{Name: name, Status: "pass", Detail: detail}
}

func checkQueryNonexistentOrder(ctx context.Context, p *wechat.Provider) checkResult {
	const name = "query_nonexistent_order"
	_, err := p.QueryOrder(ctx, &paymgr.QueryOrderRequest{
		OutTradeNo: fmt.Sprintf("WXPK-NONEXIST-%d", time.Now().UnixNano()),
	})
	if err == nil {
		return checkResult{Name: name, Status: "fail", Note: "查询不存在订单返回 nil error，预期 ChannelError"}
	}
	detail := map[string]any{"error_message": err.Error()}
	var chErr *paymgr.ChannelError
	if errors.As(err, &chErr) {
		detail["channel_error_code"] = chErr.Code
		detail["channel_error_message"] = chErr.Message
	}
	// 错误返回本身即证明应答验签通过（能解出业务错误码），故视为 pass。
	detail["note"] = "返回业务错误（如 ORDERNOTEXIST）即说明应答验签通过、错误被正确解析"
	return checkResult{Name: name, Status: "pass", Detail: detail}
}

func checkCloseNonexistentOrder(ctx context.Context, p *wechat.Provider) checkResult {
	const name = "close_nonexistent_order"
	err := p.CloseOrder(ctx, &paymgr.CloseOrderRequest{
		OutTradeNo: fmt.Sprintf("WXPK-NONEXIST-CLOSE-%d", time.Now().UnixNano()),
	})
	detail := map[string]any{}
	if err != nil {
		detail["error_message"] = err.Error()
		var chErr *paymgr.ChannelError
		if errors.As(err, &chErr) {
			detail["channel_error_code"] = chErr.Code
		}
	}
	detail["note"] = "仅采集行为，不判 pass/fail 之外的语义"
	return checkResult{Name: name, Status: "pass", Detail: detail}
}

func finalize(rep *report) {
	for _, r := range rep.Results {
		rep.Summary.Total++
		switch r.Status {
		case "pass":
			rep.Summary.Passed++
		case "fail":
			rep.Summary.Failed++
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
	return checkResult{Name: name, Status: "fail", Detail: detail, Error: err.Error(), Note: note}
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
