package main

import (
	"context"

	"log"
	"net/http"

	"time"

	"github.com/gtkit/go-pay/alipay"
	"github.com/gtkit/go-pay/paymgr"
	"github.com/gtkit/go-pay/wechat"
	"github.com/gtkit/json"
)

// ----- 全局支付管理器 -----

var payMgr *paymgr.Manager

func initpay() {
	payMgr = paymgr.NewManager()

	ctx := context.Background()

	// --- 初始化微信支付（APP 支付） ---
	// AppID 是在微信开放平台注册的移动应用的 appid，不是公众号/小程序的 appid
	wechatProvider, err := wechat.NewProvider(
		ctx,
		wechat.WithAppID("wx1234567890abcdef"),
		wechat.WithMerchant(
			"1900000001",
			"3775B6A45ACD588826D15E583A95F5DD********",
			"your-apiv3-key-32-characters-long",
		),
		wechat.WithMerchantPrivateKeyPath("/path/to/apiclient_key.pem"),
		wechat.WithPlatformCertificatePath("/path/to/wechatpay_cert.pem"),
	)
	if err != nil {
		log.Fatalf("init wechat provider: %v", err)
	}
	payMgr.Register(wechatProvider)

	// --- 初始化支付宝（证书模式） ---
	alipayProvider, err := alipay.NewProvider(
		alipay.WithAppID("2021000000000001"),
		alipay.WithPrivateKey("MIIEvQIBADANBgkqhki..."),
		alipay.WithProduction(true),
		alipay.WithCertModePaths(
			"/path/to/appCertPublicKey.crt",
			"/path/to/alipayRootCert.crt",
			"/path/to/alipayCertPublicKey_RSA2.crt",
		),
	)
	if err != nil {
		log.Fatalf("init alipay provider: %v", err)
	}
	payMgr.Register(alipayProvider)

	log.Println("pay providers initialized (wechat=app, alipay=cert)")
}

// ----- HTTP Handlers -----

// handleCreateOrder 创建订单（统一下单）
//
// POST /api/v1/orders
//
// 请求体示例（微信 APP 支付）:
//
//	{
//	  "channel": "wechat",
//	  "trade_type": "app",
//	  "amount": 100,
//	  "subject": "VIP月卡"
//	}
//
// 请求体示例（支付宝 APP 支付）:
//
//	{
//	  "channel": "alipay",
//	  "trade_type": "app",
//	  "amount": 100,
//	  "subject": "VIP月卡"
//	}
//
// 返回: 微信返回 app_params（JSON），APP 端解析后传给微信 SDK 调起支付
//
//	支付宝返回 app_params（签名字符串），APP 端直接传给支付宝 SDK
func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req struct {
		Channel   paymgr.Channel   `json:"channel"`
		TradeType paymgr.TradeType `json:"trade_type"`
		Amount    int64            `json:"amount"` // 分
		Subject   string           `json:"subject"`
		ReturnURL string           `json:"return_url"` // 支付宝同步跳转（可选）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 生成商户订单号（生产中应使用 Snowflake 或类似算法）
	outTradeNo := "ORD" + time.Now().Format("20060102150405") + "001"

	ctx := r.Context()
	resp, err := payMgr.UnifiedOrder(ctx, req.Channel, &paymgr.UnifiedOrderRequest{
		OutTradeNo:  outTradeNo,
		TotalAmount: req.Amount,
		Subject:     req.Subject,
		TradeType:   req.TradeType,
		NotifyURL:   "https://yourdomain.com/api/v1/notify/" + string(req.Channel),
		ReturnURL:   req.ReturnURL,
		ExpireAt:    time.Now().Add(30 * time.Minute),
		Metadata: map[string]string{
			"order_id": outTradeNo,
		},
	})
	if err != nil {
		log.Printf("create order error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// APP 端根据 channel 使用对应的参数:
	// - 微信: 解析 app_params JSON，传入微信 SDK 的 PayReq
	// - 支付宝: 直接将 app_params 字符串传入支付宝 SDK
	writeJSON(w, http.StatusOK, map[string]any{
		"out_trade_no": outTradeNo,
		"channel":      resp.Channel,
		"app_params":   resp.AppParams, // APP 支付参数
		"code_url":     resp.CodeURL,   // Native 扫码 URL（如果是扫码支付）
		"pay_url":      resp.PayURL,    // 支付宝 H5 跳转 URL
	})
}

// handleQueryOrder 查询订单
//
// GET /api/v1/orders?channel=wechat&out_trade_no=ORD20250305001
func handleQueryOrder(w http.ResponseWriter, r *http.Request) {
	channel := paymgr.Channel(r.URL.Query().Get("channel"))
	outTradeNo := r.URL.Query().Get("out_trade_no")

	ctx := r.Context()
	resp, err := payMgr.QueryOrder(ctx, channel, &paymgr.QueryOrderRequest{
		OutTradeNo: outTradeNo,
	})
	if err != nil {
		log.Printf("query order error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleWechatNotify 微信支付异步通知
//
// POST /api/v1/notify/wechat
func handleWechatNotify(w http.ResponseWriter, r *http.Request) {
	handleNotify(w, r, paymgr.ChannelWechat)
}

// handleAlipayNotify 支付宝异步通知
//
// POST /api/v1/notify/alipay
func handleAlipayNotify(w http.ResponseWriter, r *http.Request) {
	handleNotify(w, r, paymgr.ChannelAlipay)
}

// handleNotify 通用通知处理
//
// 核心原则:
//  1. 验签（SDK 内部已处理）
//  2. 校验金额
//  3. 幂等处理（同一通知可能推送多次）
//  4. 更新订单状态
//  5. 回写 ACK
func handleNotify(w http.ResponseWriter, r *http.Request, ch paymgr.Channel) {
	ctx := r.Context()

	result, err := payMgr.ParseNotify(ctx, ch, r)
	if err != nil {
		log.Printf("[%s] parse notify error: %v", ch, err)
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}

	log.Printf("[%s] notify: out_trade_no=%s, transaction_id=%s, status=%s, amount=%d fen",
		ch, result.OutTradeNo, result.TransactionID, result.TradeStatus, result.TotalAmount)

	// ----- 核心业务逻辑（伪代码） -----
	//
	// order, err := orderRepo.GetByOutTradeNo(ctx, result.OutTradeNo)
	// if err != nil {
	//     log.Printf("order not found: %s", result.OutTradeNo)
	//     // 仍然 ACK，避免重复推送一个不存在的订单
	// }
	//
	// // 幂等: 已处理过则直接 ACK
	// if order.Status == "paid" {
	//     payMgr.ACKNotify(ch, w)
	//     return
	// }
	//
	// // 校验金额（安全关键）
	// if order.Amount != result.TotalAmount {
	//     log.Printf("ALERT: amount mismatch for %s, expected=%d, got=%d",
	//         result.OutTradeNo, order.Amount, result.TotalAmount)
	//     // 记录异常但仍 ACK，避免重复推送
	// }
	//
	// // 更新订单
	// orderRepo.UpdatePaid(ctx, order.ID, result.TransactionID, result.PaidAt)
	//
	// // 后续业务（发货/开通VIP等）
	// go processAfterpay(order)
	// ----------------------------------

	// 回写成功响应
	if err := payMgr.ACKNotify(ch, w); err != nil {
		log.Printf("[%s] ack notify error: %v", ch, err)
	}
}

// handleQueryRefund 查询退款
//
// GET /api/v1/refund?channel=wechat&out_refund_no=REF20250305001
//
// 支付宝查询退款时还需附带原订单号: &out_trade_no=ORD20250305001
func handleQueryRefund(w http.ResponseWriter, r *http.Request) {
	channel := paymgr.Channel(r.URL.Query().Get("channel"))
	outRefundNo := r.URL.Query().Get("out_refund_no")
	outTradeNo := r.URL.Query().Get("out_trade_no")

	ctx := r.Context()
	resp, err := payMgr.QueryRefund(ctx, channel, &paymgr.QueryRefundRequest{
		OutRefundNo: outRefundNo,
		OutTradeNo:  outTradeNo,
	})
	if err != nil {
		log.Printf("query refund error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleWechatRefundNotify 微信退款异步通知
//
// POST /api/v1/notify/refund/wechat
//
// 支付宝没有独立的退款通知端点，退款结果复用支付通知端点，
// 通过 handleAlipayNotify + 判断 TradeStatus == paymgr.TradeStatusRefunded 处理。
func handleWechatRefundNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := payMgr.ParseRefundNotify(ctx, paymgr.ChannelWechat, r)
	if err != nil {
		log.Printf("[wechat] parse refund notify error: %v", err)
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}

	log.Printf("[wechat] refund notify: out_refund_no=%s, refund_id=%s, status=%s, refund_amount=%d fen",
		result.OutRefundNo, result.RefundID, result.RefundStatus, result.RefundAmount)

	// 业务层: 幂等更新退款单状态；result.RefundStatus == paymgr.RefundStatusSuccess 时标记退款完成。

	if err := payMgr.ACKNotify(paymgr.ChannelWechat, w); err != nil {
		log.Printf("[wechat] ack refund notify error: %v", err)
	}
}

// handleRefund 退款
//
// POST /api/v1/refund
//
//	{
//	  "channel": "wechat",
//	  "out_trade_no": "ORD20250305001",
//	  "out_refund_no": "REF20250305001",
//	  "refund_amount": 50,
//	  "total_amount": 100,
//	  "reason": "用户申请退款"
//	}
func handleRefund(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var req struct {
		Channel      paymgr.Channel `json:"channel"`
		OutTradeNo   string         `json:"out_trade_no"`
		OutRefundNo  string         `json:"out_refund_no"`
		RefundAmount int64          `json:"refund_amount"` // 分
		TotalAmount  int64          `json:"total_amount"`  // 分
		Reason       string         `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx := r.Context()
	resp, err := payMgr.Refund(ctx, req.Channel, &paymgr.RefundRequest{
		OutTradeNo:   req.OutTradeNo,
		OutRefundNo:  req.OutRefundNo,
		RefundAmount: req.RefundAmount,
		TotalAmount:  req.TotalAmount,
		Reason:       req.Reason,
		NotifyURL:    "https://yourdomain.com/api/v1/notify/refund/" + string(req.Channel),
	})
	if err != nil {
		log.Printf("refund error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ----- 路由注册 -----

func main() {
	initpay()

	mux := http.NewServeMux()

	// 业务接口
	mux.HandleFunc("POST /api/v1/orders", handleCreateOrder)
	mux.HandleFunc("GET /api/v1/orders", handleQueryOrder)
	mux.HandleFunc("POST /api/v1/refund", handleRefund)
	mux.HandleFunc("GET /api/v1/refund", handleQueryRefund)

	// 支付回调（必须 HTTPS，且路径不附带额外参数）
	mux.HandleFunc("POST /api/v1/notify/wechat", handleWechatNotify)
	mux.HandleFunc("POST /api/v1/notify/alipay", handleAlipayNotify)
	// 微信退款通知（支付宝的退款结果随 handleAlipayNotify 一并回来，不需要单独路由）
	mux.HandleFunc("POST /api/v1/notify/refund/wechat", handleWechatRefundNotify)

	log.Println("server starting on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

// writeJSON 写 JSON 响应
func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
