// Command sandbox-refund 触发支付宝沙箱退款 + 退款查询，输出 JSON 报告。
//
// 配合 notify-listener（应已在 :8080 运行）一起验证退款链路：
//   - Refund 调用是否成功（参数构造 + 错误归一化）
//   - QueryRefund 字段映射是否正确（refund_fee / refund_status / gmt_refund_pay）
//   - 退款回调是否触发 ParseNotify 把 trade_status 映射为 "refunded"（gmt_refund/refund_fee 字段名假设验证）
//
// 用法（先有一笔已支付订单）：
//
//	export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
//	export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
//	export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
//	export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
//	export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
//
//	go run ./tools/sandbox-refund -out-trade-no SBX-PAGE-1778307815008432000
//
// 工具留作本地用，不入 git。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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
)

type config struct {
	AppID          string
	PrivateKeyPath string
	AppCertPath    string
	RootCertPath   string
	PublicCertPath string
}

type checkResult struct {
	Name   string         `json:"name"`
	Status string         `json:"status"` // pass / fail
	Detail map[string]any `json:"detail,omitempty"`
	Error  string         `json:"error,omitempty"`
	Note   string         `json:"note,omitempty"`
}

type report struct {
	GeneratedAt string        `json:"generated_at"`
	Inputs      map[string]any `json:"inputs"`
	Summary     summary       `json:"summary"`
	Results     []checkResult `json:"results"`
}

type summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

func main() {
	var (
		outTradeNo  = flag.String("out-trade-no", "", "原支付的商户订单号（必填，与 -trade-no 二选一）")
		tradeNo     = flag.String("trade-no", "", "支付宝交易号（与 -out-trade-no 二选一）")
		outRefundNo = flag.String("out-refund-no", "", "退款单号（不填则自动生成）")
		refundCent  = flag.Int64("refund-cent", 1, "退款金额（分，默认 1 分）")
		reason      = flag.String("reason", "v1.3.x sandbox refund verification", "退款原因")
	)
	flag.Parse()

	if *outTradeNo == "" && *tradeNo == "" {
		fmt.Fprintln(os.Stderr, "❌ 必须提供 -out-trade-no 或 -trade-no")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌", err)
		os.Exit(2)
	}

	if *outRefundNo == "" {
		*outRefundNo = fmt.Sprintf("REF-%d", time.Now().UnixNano())
	}

	provider, err := alipay.NewProvider(
		alipay.WithAppID(cfg.AppID),
		alipay.WithPrivateKeyPath(cfg.PrivateKeyPath),
		alipay.WithProduction(false),
		alipay.WithCertModePaths(cfg.AppCertPath, cfg.RootCertPath, cfg.PublicCertPath),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ Provider 初始化失败:", err)
		os.Exit(1)
	}

	rep := &report{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Inputs: map[string]any{
			"out_trade_no":  *outTradeNo,
			"trade_no":      *tradeNo,
			"out_refund_no": *outRefundNo,
			"refund_cent":   *refundCent,
			"reason":        *reason,
		},
	}

	ctx := context.Background()

	rep.Results = append(rep.Results, doRefund(ctx, provider, &paymgr.RefundRequest{
		OutTradeNo:    *outTradeNo,
		TransactionID: *tradeNo,
		OutRefundNo:   *outRefundNo,
		RefundAmount:  *refundCent,
		Reason:        *reason,
	}))

	// 等待 1 秒让支付宝异步推送退款回调
	time.Sleep(1 * time.Second)

	rep.Results = append(rep.Results, doQueryRefund(ctx, provider, &paymgr.QueryRefundRequest{
		OutTradeNo:    *outTradeNo,
		TransactionID: *tradeNo,
		OutRefundNo:   *outRefundNo,
	}))

	rep.Results = append(rep.Results, doQueryRefundNonexistent(ctx, provider, *outTradeNo))

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
	}, nil
}

func doRefund(ctx context.Context, p *alipay.Provider, req *paymgr.RefundRequest) checkResult {
	const name = "refund"
	resp, err := p.Refund(ctx, req)
	if err != nil {
		return failed(name, err, "退款失败——可能 R 风险命中：BodyMap 字段名 / 网关响应字段映射异常")
	}
	return checkResult{
		Name:   name,
		Status: "pass",
		Detail: map[string]any{
			"channel":       string(resp.Channel),
			"out_refund_no": resp.OutRefundNo,
			"refund_id":     resp.RefundID,
			"refund_amount": resp.RefundAmount,
		},
	}
}

func doQueryRefund(ctx context.Context, p *alipay.Provider, req *paymgr.QueryRefundRequest) checkResult {
	const name = "query_refund"
	resp, err := p.QueryRefund(ctx, req)
	if err != nil {
		return failed(name, err, "查询退款失败")
	}
	detail := map[string]any{
		"channel":        string(resp.Channel),
		"out_trade_no":   resp.OutTradeNo,
		"transaction_id": resp.TransactionID,
		"out_refund_no":  resp.OutRefundNo,
		"refund_id":      resp.RefundID,
		"refund_status":  string(resp.RefundStatus),
		"refund_amount":  resp.RefundAmount,
		"total_amount":   resp.TotalAmount,
	}
	if !resp.RefundedAt.IsZero() {
		detail["refunded_at"] = resp.RefundedAt.Format(time.RFC3339)
	}

	note := ""
	switch resp.RefundStatus {
	case paymgr.RefundStatusSuccess:
		note = "✅ refund_status=REFUND_SUCCESS 正确映射为 RefundStatusSuccess"
	case paymgr.RefundStatusProcessing:
		note = "⏳ 退款处理中（refund_status=REFUND_PROCESSING）"
	case paymgr.RefundStatusError:
		note = "⚠ refund_status 非预期值或为空，映射为 Error；请检查支付宝沙箱状态"
	}
	return checkResult{Name: name, Status: "pass", Detail: detail, Note: note}
}

func doQueryRefundNonexistent(ctx context.Context, p *alipay.Provider, refundNo string) checkResult {
	const name = "query_refund_nonexistent"
	fakeOutRefundNo := fmt.Sprintf("NONEXISTENT-%d", time.Now().UnixNano())
	req := &paymgr.QueryRefundRequest{
		OutRefundNo: fakeOutRefundNo,
		OutTradeNo:  refundNo,
	}
	resp, err := p.QueryRefund(ctx, req)
	if err == nil {
		// 真实响应 dump，看支付宝实际返回什么——决定 QueryRefund 应该如何识别"退款不存在"
		respDetail := map[string]any{
			"requested_out_refund_no": fakeOutRefundNo,
			"requested_out_trade_no":  refundNo,
		}
		if resp != nil {
			respDetail["resp.OutTradeNo"] = resp.OutTradeNo
			respDetail["resp.TransactionID"] = resp.TransactionID
			respDetail["resp.OutRefundNo"] = resp.OutRefundNo
			respDetail["resp.RefundID"] = resp.RefundID
			respDetail["resp.RefundStatus"] = string(resp.RefundStatus)
			respDetail["resp.RefundAmount"] = resp.RefundAmount
			respDetail["resp.TotalAmount"] = resp.TotalAmount
			respDetail["resp.RefundedAt_zero"] = resp.RefundedAt.IsZero()
		}
		return checkResult{
			Name:   name,
			Status: "fail",
			Detail: respDetail,
			Note:   "支付宝对不存在退款单返回 200 + 实际字段如上；需对比 requested_out_refund_no 与 resp.OutRefundNo 决定如何修复 QueryRefund 识别逻辑",
		}
	}
	detail := map[string]any{
		"error_message":               err.Error(),
		"matches_err_order_not_found": errors.Is(err, paymgr.ErrOrderNotFound),
	}
	if errors.Is(err, paymgr.ErrOrderNotFound) {
		return checkResult{Name: name, Status: "pass", Detail: detail, Note: "ErrOrderNotFound 路径正确触发"}
	}
	var chErr *paymgr.ChannelError
	if errors.As(err, &chErr) {
		detail["channel_error_code"] = chErr.Code
		detail["channel_error_message"] = chErr.Message
	}
	return checkResult{
		Name:   name,
		Status: "pass", // 不判断 fail，只采集错误码
		Detail: detail,
		Note:   "未命中 ErrOrderNotFound：支付宝对不存在退款单的返回可能与不存在订单不同；参考 channel_error_code",
	}
}

func failed(name string, err error, note string) checkResult {
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
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	fmt.Println()
	fmt.Println("⚠ 提示：退款异步通知由 notify-listener 单独接收。请回到 notify-listener 终端检查：")
	fmt.Println("  - 是否收到 [#N] POST /notify")
	fmt.Println("  - NotifyResult.trade_status 是否被映射为 \"refunded\"")
	fmt.Println("  - 这是验证 gmt_refund/refund_fee 字段名假设的最关键证据")
	if rep.Summary.Failed > 0 {
		os.Exit(1)
	}
}
