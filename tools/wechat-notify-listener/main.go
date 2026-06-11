// Command wechat-notify-listener 在本地启动 HTTP 服务接收微信支付异步通知，
// 调用 wechat.Provider 的 ParseNotify / ParseRefundNotify 完成公钥验签 + AES 解密 + 解析，
// 打印结果后用 ACKNotify 回写 {"code":"SUCCESS"}。
//
// 用途：验证「微信支付公钥」模式下回调验签链路（公钥验签 + APIv3 解密）。
// 配合 ngrok 暴露公网地址 + wechat-pubkey-verify 产出的 code_url 真实支付一笔即可触发。
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
//	# 可选：默认 :8080
//	export WECHAT_NOTIFY_LISTENER_ADDR=":8080"
//
//	go run ./tools/wechat-notify-listener
//
// 路由：
//
//	POST /notify         支付通知，调 ParseNotify + ACKNotify
//	POST /refund-notify   退款通知，调 ParseRefundNotify + ACKNotify
//	GET  /health         健康检查
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
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
	envAddr           = "WECHAT_NOTIFY_LISTENER_ADDR"
	defaultAddr       = ":8080"
)

var notifyCount atomic.Int64

type config struct {
	AppID          string
	MchID          string
	MchCertSerial  string
	APIv3Key       string
	PrivateKeyPath string
	PublicKeyID    string
	PublicKeyPath  string
	Addr           string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ 配置加载失败:", err)
		fmt.Fprintln(os.Stderr, "\n请确保设置以下必填环境变量:")
		for _, k := range []string{
			envAppID, envMchID, envMchCertSerial, envAPIv3Key,
			envPrivateKeyPath, envPublicKeyID, envPublicKeyPath,
		} {
			fmt.Fprintln(os.Stderr, "  "+k)
		}
		os.Exit(2)
	}

	ctx := context.Background()
	provider, err := wechat.NewProvider(
		ctx,
		wechat.WithAppID(cfg.AppID),
		wechat.WithMerchant(cfg.MchID, cfg.MchCertSerial, cfg.APIv3Key),
		wechat.WithMerchantPrivateKeyPath(cfg.PrivateKeyPath),
		wechat.WithPublicKeyID(cfg.PublicKeyID),
		wechat.WithPublicKeyPath(cfg.PublicKeyPath),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ Provider 初始化失败:", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/notify", payNotifyHandler(provider))
	mux.HandleFunc("/refund-notify", refundNotifyHandler(provider))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Println("===== go-pay 微信公钥模式回调验证器 =====")
		fmt.Printf("listening on %s\n", cfg.Addr)
		fmt.Println("等待微信异步通知...（Ctrl+C 退出）")
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
	addr := os.Getenv(envAddr)
	if addr == "" {
		addr = defaultAddr
	}
	return &config{
		AppID:          required[envAppID],
		MchID:          required[envMchID],
		MchCertSerial:  required[envMchCertSerial],
		APIv3Key:       required[envAPIv3Key],
		PrivateKeyPath: required[envPrivateKeyPath],
		PublicKeyID:    required[envPublicKeyID],
		PublicKeyPath:  required[envPublicKeyPath],
		Addr:           addr,
	}, nil
}

func payNotifyHandler(p *wechat.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seq := notifyCount.Add(1)
		fmt.Printf("\n[#%d] %s 收到 POST /notify\n", seq, time.Now().Format("15:04:05"))
		body := rewindBody(w, r)
		printBodyPreview(body)

		result, err := p.ParseNotify(r.Context(), r)
		if err != nil {
			fmt.Printf("   ❌ ParseNotify 失败: %v\n", err)
			fmt.Printf("   📋 errors.Is(err, ErrInvalidNotify): %v\n", errors.Is(err, paymgr.ErrInvalidNotify))
			fmt.Println("   ⚠ 不要 ACK——错误响应让微信重试")
			http.Error(w, "parse notify failed", http.StatusBadRequest)
			return
		}
		fmt.Println("   ✅ ParseNotify 成功（公钥验签 + 解密通过）")
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
		p.ACKNotify(w)
		fmt.Println("   ✅ ACKNotify 已写入 SUCCESS")
		fmt.Println(strings.Repeat("-", 80))
	}
}

func refundNotifyHandler(p *wechat.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seq := notifyCount.Add(1)
		fmt.Printf("\n[#%d] %s 收到 POST /refund-notify\n", seq, time.Now().Format("15:04:05"))
		body := rewindBody(w, r)
		printBodyPreview(body)

		result, err := p.ParseRefundNotify(r.Context(), r)
		if err != nil {
			fmt.Printf("   ❌ ParseRefundNotify 失败: %v\n", err)
			fmt.Printf("   📋 errors.Is(err, ErrInvalidNotify): %v\n", errors.Is(err, paymgr.ErrInvalidNotify))
			http.Error(w, "parse refund notify failed", http.StatusBadRequest)
			return
		}
		fmt.Println("   ✅ ParseRefundNotify 成功（公钥验签 + 解密通过）")
		out, _ := json.MarshalIndent(map[string]any{
			"channel":       string(result.Channel),
			"out_trade_no":  result.OutTradeNo,
			"out_refund_no": result.OutRefundNo,
			"refund_id":     result.RefundID,
			"refund_status": string(result.RefundStatus),
			"refund_amount": result.RefundAmount,
			"total_amount":  result.TotalAmount,
			"user_received": result.UserReceivedAccount,
			"refunded_at":   result.RefundedAt.Format(time.RFC3339),
		}, "   ", "  ")
		fmt.Printf("   📦 RefundNotifyResult: %s\n", string(out))
		p.ACKNotify(w)
		fmt.Println("   ✅ ACKNotify 已写入 SUCCESS")
		fmt.Println(strings.Repeat("-", 80))
	}
}

// rewindBody 读取并复位请求 body，使后续 ParseNotify 仍可读取（限 1 MiB，
// 超限报错而非静默截断，避免截断 body 导致混淆的验签失败）。
func rewindBody(w http.ResponseWriter, r *http.Request) []byte {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		fmt.Printf("   ❌ 读取 body 失败: %v\n", err)
		return nil
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	return body
}

func printBodyPreview(body []byte) {
	s := string(body)
	if len(s) > 200 {
		s = s[:200] + "...(truncated)"
	}
	fmt.Printf("   📨 raw body (前 200 字节): %s\n", s)
}
