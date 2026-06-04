# wechat-pubkey-verify

用真实微信商户凭据验证「微信支付公钥」验签模式可上线。

微信支付 V3 **无公开沙箱网关**，本工具连真实网关、用 1 分小额订单做最小验证：

1. `init_public_key_mode` — 公钥模式 Provider 初始化（Client + 回调 handler 就绪）
2. `trade_type_native` — Native 下单。**成功即证明：商户私钥请求签名 + 微信应答验签（公钥）均通过**
3. `query_nonexistent_order` — 查询不存在订单，能解出业务错误码即说明应答验签通过
4. `close_nonexistent_order` — 关单行为采集

> 本工具不做：真实支付完成、回调验签。回调验签见 [`wechat-notify-listener`](../wechat-notify-listener)。

## 环境变量

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `WECHAT_PUBKEY_APP_ID` | 是 | 微信应用 `appid` |
| `WECHAT_PUBKEY_MCH_ID` | 是 | 商户号 |
| `WECHAT_PUBKEY_MCH_CERT_SERIAL` | 是 | 商户证书序列号（公钥模式下仍用于请求签名）|
| `WECHAT_PUBKEY_APIV3_KEY` | 是 | APIv3 密钥，**必须正好 32 字节** |
| `WECHAT_PUBKEY_PRIVATE_KEY_PATH` | 是 | 商户私钥 PEM 文件路径 |
| `WECHAT_PUBKEY_PUBLIC_KEY_ID` | 是 | 微信支付公钥 ID（`PUB_KEY_ID_xxx`）|
| `WECHAT_PUBKEY_PUBLIC_KEY_PATH` | 是 | 微信支付公钥 PEM 文件路径（PKIX `PUBLIC KEY`）|
| `WECHAT_PUBKEY_NOTIFY_URL` | 否 | 下单回调地址，默认 `https://example.com/notify` |

## 运行

```bash
export WECHAT_PUBKEY_APP_ID="wx1234567890abcdef"
export WECHAT_PUBKEY_MCH_ID="1900000001"
export WECHAT_PUBKEY_MCH_CERT_SERIAL="3775B6A45ACD588826D15E583A95F5DD********"
export WECHAT_PUBKEY_APIV3_KEY="your-apiv3-key-exactly-32-bytes!"
export WECHAT_PUBKEY_PRIVATE_KEY_PATH="/path/to/apiclient_key.pem"
export WECHAT_PUBKEY_PUBLIC_KEY_ID="PUB_KEY_ID_0000000000000000000000000000"
export WECHAT_PUBKEY_PUBLIC_KEY_PATH="/path/to/wechatpay_pub_key.pem"

go run ./tools/wechat-pubkey-verify
```

输出为 JSON 报告；任一检查 `fail` 时进程退出码非 0。
`trade_type_native` 返回的 `code_url` 可生成二维码真实支付一笔，配合 `wechat-notify-listener` 验证回调。

## 安全

源码不含任何凭据，全部走环境变量。请勿将真实密钥写入脚本或提交入库。
