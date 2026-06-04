# wechat-notify-listener

本地 HTTP 服务，验证「微信支付公钥」模式下的**回调验签链路**（公钥验签 + APIv3/AES 解密 + 解析 + ACK）。

配合 [`wechat-pubkey-verify`](../wechat-pubkey-verify) 产出的 `code_url` 真实支付一笔即可触发支付通知；真实退款一笔可触发退款通知。

## 路由

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/notify` | 支付通知，调 `ParseNotify` + `ACKNotify` |
| POST | `/refund-notify` | 退款通知，调 `ParseRefundNotify` + `ACKNotify` |
| GET | `/health` | 健康检查 |

验签或解密失败时**不**返回 ACK（让微信重试），并打印错误供排查。

## 环境变量

与 `wechat-pubkey-verify` 完全一致（公钥模式七项必填），额外：

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `WECHAT_NOTIFY_LISTENER_ADDR` | 否 | 监听地址，默认 `:8080` |

## 运行

```bash
# 设置与 wechat-pubkey-verify 相同的 WECHAT_PUBKEY_* 环境变量
go run ./tools/wechat-notify-listener
```

本机服务需经公网可达地址（如 `ngrok http 8080`）才能收到微信回调，
并把下单时的 `notify_url` 指向该公网地址的 `/notify`。

## 安全

源码不含任何凭据，全部走环境变量。
