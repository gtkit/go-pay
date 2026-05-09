# notify-listener

支付宝沙箱回调验证工具。配合 `tools/sandbox-verify` + `ngrok` 完成 v1.3.x 上线前最关键的 **`ParseNotify` 验签 + 解析** 真实链路验证。

## 验证什么

| 项 | 期望 |
|---|---|
| 验签（`alipay.VerifySignWithCert`） | 通过——证明 v1.3.0 混合方案（v3 子包做请求 + 老版 alipay 做验签）在真实 v3 网关推送下可用 |
| 字段解析（`out_trade_no` / `trade_no` / `trade_status` / `total_amount` / `gmt_payment` / `buyer_id` / `passback_params`） | 全部填到 `NotifyResult` 对应字段 |
| 退款事件识别（`gmt_refund` / `refund_fee` 非空 → `TradeStatusRefunded`）| 触发退款回调时映射为 `TradeStatusRefunded` |
| `ACKNotify` 写 "success" | 支付宝不再重试 |

## 使用流程

### 1. 启动 ngrok 暴露公网地址

```bash
brew install ngrok      # 如未安装
ngrok config add-authtoken YOUR_NGROK_TOKEN  # 首次需注册 https://ngrok.com 拿 token
ngrok http 8080
```

记录 ngrok 给的 https 公网地址，例如 `https://abc123.ngrok-free.app`。

### 2. 启动 notify-listener

新开一个终端窗口：

```bash
export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
# 可选：默认 :8080
# export ALIPAY_NOTIFY_LISTENER_ADDR=":8080"

cd /path/to/go-pay
GOWORK=off go run ./tools/notify-listener
```

启动后会在终端显示监听状态。

### 3. 用 sandbox-verify 生成带 ngrok 回调的 PayURL

新开第三个终端：

```bash
export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
export ALIPAY_SANDBOX_NOTIFY_URL="https://abc123.ngrok-free.app/notify"   # ⬅️ ngrok 地址 + /notify
export ALIPAY_SANDBOX_RETURN_URL="https://abc123.ngrok-free.app/return"   # 可选

cd /path/to/go-pay
GOWORK=off go run ./tools/sandbox-verify
```

报告里 `trade_type_page` 的 `pay_url_head` 会包含 `notify_url=https%3A%2F%2Fabc123.ngrok-free.app%2Fnotify`。

### 4. 浏览器打开 PayURL，沙箱买家支付

把 `trade_type_page` 完整的 PayURL（不仅是 head 部分——这里需要完整的 URL，工具的 head 截断了）复制到浏览器。

> **提示**：`tools/sandbox-verify/main.go` 当前打印 `pay_url_head` 是截断版。如果你需要完整 URL，可以临时把 `headOf(resp.PayURL, 120)` 改成直接 `resp.PayURL`，或在工具里加 `pay_url_full` 字段。

或者更简单：在 `notify-listener` 里看不到完整 PayURL 也没关系——直接用支付宝沙箱版手机 APP 扫描 `trade_type_native` 的 `code_url`（这个是完整 URL，不会截断）也能触发回调。

支付完成后，浏览器会跳转到你的 `return_url`（如果设了），同时支付宝会**异步推送通知到 `/notify`**。

### 5. 看 notify-listener 输出

期望看到：

```
[#1] 11:50:23 收到 POST /notify
   📨 raw form (前 200 字节): gmt_create=2026-05-09+11%3A50%3A20&charset=utf-8&...
   📨 重点字段: trade_status,trade_no,out_trade_no,total_amount,...
   ✅ ParseNotify 成功
   📦 NotifyResult: {
       "channel": "alipay",
       "out_trade_no": "SBX-PAGE-...",
       "transaction_id": "2026050922001407221234567890",
       "trade_status": "paid",
       "total_amount": 1,
       "buyer_id": "2088722035201234",
       "paid_at": "2026-05-09T11:50:20+08:00",
       "metadata": null
     }
   🎉 状态: 支付成功
   ✅ ACKNotify 已写入 "success"
--------------------------------------------------------------------------------
```

如果 ParseNotify 失败：

```
[#1] 11:50:23 收到 POST /notify
   ❌ ParseNotify 失败: payment: invalid signature: signature mismatch
   📋 errors.Is(err, ErrInvalidSign): true
   ⚠ 不要 ACKNotify——错误响应让支付宝重试
   --- HTTP 请求 dump ---
   ...
   --- body (完整) ---
   ...
```

把完整输出贴给我分析。

## 测退款回调

完成支付后，立即用 sandbox-verify 跑一次 Refund（或自己写个调用），等待支付宝再次推送通知到同一个 `/notify`。这次 `gmt_refund` 或 `refund_fee` 非空，期望看到：

```
[#2] 11:55:11 收到 POST /notify
   ...
   ✅ ParseNotify 成功
   📦 NotifyResult: {
       "trade_status": "refunded",   ⬅️ 关键：被映射为 refunded
       ...
     }
   💸 状态: 退款（gmt_refund 或 refund_fee 非空）
```

## 路由

| 路径 | 用途 |
|---|---|
| `POST /notify` | 异步通知入口，验签 + 解析 + ACK |
| `GET /return` | 同步跳转入口，返回简单提示页（浏览器看的） |
| `GET /health` | 健康检查 |

## 安全

- 工具仅读取证书 / 私钥文件**路径**，不输出凭证内容
- 私钥从未离开你本地环境
- 工具留在工作树本地，不入 git（同 sandbox-verify）
