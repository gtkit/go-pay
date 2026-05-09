// Command notify-listener 在本地启动 HTTP 服务接收支付宝异步通知，
// 调用 alipay.Provider.ParseNotify 完成验签与解析，把结果实时打印到 stdout，
// 然后调 ACKNotify 给支付宝回写 "success"。
//
// 用途：v1.3.0 沙箱端到端验证 ParseNotify 链路（验签 + 字段解析 + ACK）。
// 配合 ngrok 暴露公网地址 + 沙箱 PayURL 真实支付一笔即可触发。
//
// 用法：
//
//	export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
//	export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
//	export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
//	export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
//	export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
//	# 可选：默认 :8080
//	export ALIPAY_NOTIFY_LISTENER_ADDR=":8080"
//
//	go run ./tools/notify-listener
//
// 路由：
//
//	POST /notify    异步通知入口，调 ParseNotify + ACKNotify
//	GET  /return    同步跳转入口（浏览器支付完会跳转），返回简单提示页
//	GET  /health    健康检查
//
// 工具保留在工作树本地使用，不入 git。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
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
	envAddr           = "ALIPAY_NOTIFY_LISTENER_ADDR"
	defaultAddr       = ":8080"
)

var notifyCount atomic.Int64

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ 配置加载失败:", err)
		fmt.Fprintln(os.Stderr, "\n请确保设置以下必填环境变量:")
		for _, k := range []string{envAppID, envPrivateKeyPath, envAppCertPath, envRootCertPath, envPublicCertPath} {
			fmt.Fprintln(os.Stderr, "  "+k)
		}
		os.Exit(2)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/notify", notifyHandler(provider))
	mux.HandleFunc("/return", returnHandler)
	mux.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Println(banner())
		fmt.Printf("listening on %s\n", cfg.Addr)
		fmt.Println("waiting for sandbox notifications... (Ctrl+C to stop)")
		fmt.Println(strings.Repeat("=", 80))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "❌ HTTP server 异常:", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\n收到退出信号，关闭 server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	fmt.Printf("总共收到 %d 笔通知\n", notifyCount.Load())
}

type config struct {
	AppID          string
	PrivateKeyPath string
	AppCertPath    string
	RootCertPath   string
	PublicCertPath string
	Addr           string
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
	addr := os.Getenv(envAddr)
	if addr == "" {
		addr = defaultAddr
	}
	return &config{
		AppID:          required[envAppID],
		PrivateKeyPath: required[envPrivateKeyPath],
		AppCertPath:    required[envAppCertPath],
		RootCertPath:   required[envRootCertPath],
		PublicCertPath: required[envPublicCertPath],
		Addr:           addr,
	}, nil
}

func notifyHandler(p *alipay.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seq := notifyCount.Add(1)
		fmt.Printf("\n[#%d] %s 收到 POST /notify\n", seq, time.Now().Format("15:04:05"))

		// 把原始请求 body 也存一份，方便排查
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("   ❌ 读取 body 失败: %v\n", err)
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		// 把读过的 body 重新放回去，让 ParseNotify 能再读一次
		r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		// 同时打印 raw body 关键字段（form 编码）
		printFormPreview(bodyBytes)

		result, err := p.ParseNotify(r.Context(), r)
		if err != nil {
			fmt.Printf("   ❌ ParseNotify 失败: %v\n", err)
			fmt.Printf("   📋 errors.Is(err, ErrInvalidSign): %v\n", errors.Is(err, paymgr.ErrInvalidSign))
			fmt.Printf("   📋 errors.Is(err, ErrInvalidNotify): %v\n", errors.Is(err, paymgr.ErrInvalidNotify))
			fmt.Println("   ⚠ 不要 ACKNotify——错误响应让支付宝重试")
			dumpRequest(r, bodyBytes)
			http.Error(w, "parse notify failed", http.StatusBadRequest)
			return
		}

		fmt.Println("   ✅ ParseNotify 成功")
		printResult(result)

		p.ACKNotify(w)
		fmt.Println("   ✅ ACKNotify 已写入 \"success\"")
		fmt.Println(strings.Repeat("-", 80))
	}
}

func printFormPreview(body []byte) {
	const interestingKeys = "trade_status,trade_no,out_trade_no,total_amount,gmt_payment,gmt_refund,refund_fee,buyer_id,sign_type,charset"
	form := string(body)
	if len(form) > 200 {
		form = form[:200] + "...(truncated)"
	}
	fmt.Printf("   📨 raw form (前 200 字节): %s\n", form)
	fmt.Printf("   📨 重点字段: %s\n", interestingKeys)
}

func printResult(result *paymgr.NotifyResult) {
	out, _ := json.MarshalIndent(map[string]any{
		"channel":        string(result.Channel),
		"out_trade_no":   result.OutTradeNo,
		"transaction_id": result.TransactionID,
		"trade_status":   string(result.TradeStatus),
		"total_amount":   result.TotalAmount,
		"buyer_id":       result.BuyerID,
		"paid_at":        result.PaidAt.Format(time.RFC3339),
		"metadata":       result.Metadata,
	}, "   ", "  ")
	fmt.Printf("   📦 NotifyResult: %s\n", string(out))

	switch result.TradeStatus {
	case paymgr.TradeStatusPaid:
		fmt.Println("   🎉 状态: 支付成功")
	case paymgr.TradeStatusRefunded:
		fmt.Println("   💸 状态: 退款（gmt_refund 或 refund_fee 非空）")
	case paymgr.TradeStatusPending:
		fmt.Println("   ⏳ 状态: 待支付")
	case paymgr.TradeStatusClosed:
		fmt.Println("   🔒 状态: 已关闭")
	default:
		fmt.Printf("   ❓ 状态: %s（未知或异常）\n", result.TradeStatus)
	}
}

func dumpRequest(r *http.Request, body []byte) {
	dump, err := httputil.DumpRequest(r, false)
	if err == nil {
		fmt.Printf("   --- HTTP 请求 dump ---\n%s\n", string(dump))
	}
	fmt.Printf("   --- body (完整) ---\n%s\n", string(body))
}

func returnHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html><body>
<h1>支付完成（return_url 跳转）</h1>
<p>这只是浏览器同步跳转，业务确认状态请等异步通知或主动查询订单。</p>
<pre>` + r.URL.RawQuery + `</pre>
</body></html>`))
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func banner() string {
	return strings.Join([]string{
		"========================================",
		" go-pay v1.3.x ParseNotify 沙箱验证器",
		"========================================",
	}, "\n")
}
