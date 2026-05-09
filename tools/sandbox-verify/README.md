# sandbox-verify

支付宝沙箱环境自动化验证工具，覆盖 `go-pay` v1.3.0 重写后的核心代码路径。**用于发版前回归 / SDK 升级冒烟测试**。

## 验证矩阵

| # | 检查项 | 内容 | 是否需要网络 |
|---|---|---|---|
| 1 | `raw_public_key_soft_degradation` | 仅传 `WithAlipayPublicKey` 应返回 `paymgr.ErrNotSupported` | 否 |
| 2 | `init_cert_mode` | 证书模式 `NewProvider` 应成功 | 否（仅本地证书加载） |
| 3 | `trade_type_page` | PC 页面支付下单，应返回非空 `PayURL` | ✅ |
| 4 | `trade_type_h5` | 手机网站支付下单，应返回非空 `PayURL` | ✅ |
| 5 | `trade_type_app` | APP 支付下单，应返回非空 `AppParams` | ✅ |
| 6 | `trade_type_native` | 当面付下单，应返回非空 `CodeURL` | ✅ |
| 7 | `trade_type_jsapi` | 小程序支付下单（需 `BUYER_ID`，否则跳过） | ✅ |
| 8 | `query_nonexistent_order` | 查询不存在订单应返回 `ErrOrderNotFound` —— **失败时报告会指出 alipay/provider.go 哪行判断要修** | ✅ |
| 9 | `close_nonexistent_order` | 关闭不存在订单（采集错误码格式） | ✅ |

**不验证**（需要人工配合）：

- 真实支付完成（要扫码登录沙箱买家账号）
- 回调验签（要 ngrok / 公网回调地址 + 真实支付）
- 退款全流程（要先有已支付订单）

这些路径建议沙箱手动跑一遍 + 灰度上线观察。

## 使用方式

### 1. 前置：沙箱必须用「公钥证书」加签方式

如果沙箱当前是普通公钥模式，先到沙箱后台「研发服务 → 接口加签方式」切换为「**公钥证书**」并下载三个证书。详见 README 第 7.3 节。

### 2. 配置环境变量

必填：

```bash
export ALIPAY_SANDBOX_APP_ID="2021xxxxxxxxxx"
export ALIPAY_SANDBOX_PRIVATE_KEY_PATH="/path/to/private_key.pem"
export ALIPAY_SANDBOX_APP_CERT_PATH="/path/to/appPublicCert.crt"
export ALIPAY_SANDBOX_ROOT_CERT_PATH="/path/to/alipayRootCert.crt"
export ALIPAY_SANDBOX_PUBLIC_CERT_PATH="/path/to/alipayPublicCert.crt"
```

可选：

```bash
export ALIPAY_SANDBOX_BUYER_ID="2088xxxxxxxxxxxx"      # 测 JSAPI 必填，否则跳过
export ALIPAY_SANDBOX_NOTIFY_URL="https://example.com/notify"  # 不填用 fake URL
export ALIPAY_SANDBOX_RETURN_URL="https://example.com/return"  # 同上
```

### 3. 跑工具

```bash
cd /path/to/go-pay
GOWORK=off go run ./tools/sandbox-verify
```

输出 JSON 报告到 stdout。失败项会有 `note` 字段说明定位与修复建议。

### 4. 报告样例

```json
{
  "go_pay_version": "v1.3.0",
  "generated_at": "2026-05-09T10:30:00+08:00",
  "environment": {
    "app_id_masked": "2021********0001",
    "is_prod": false
  },
  "summary": {
    "total": 9,
    "passed": 8,
    "failed": 1,
    "skipped": 0
  },
  "results": [
    {
      "name": "trade_type_page",
      "status": "pass",
      "detail": {
        "out_trade_no": "SBX-PAGE-1715234567890123456",
        "pay_url_len": 583,
        "pay_url_head": "https://openapi-sandbox.dl.alipaydev.com/v3/alipay/trade/page/pay?..."
      }
    },
    {
      "name": "query_nonexistent_order",
      "status": "fail",
      "detail": {
        "error_message": "payment[alipay]: code=NOT_FOUND, msg=...",
        "matches_err_order_not_found": false,
        "channel_error_code": "NOT_FOUND",
        "channel_error_message": "trade not found"
      },
      "note": "⚠ R1 风险命中：v3 协议错误码不再是 ACQ.TRADE_NOT_EXIST，需修 alipay/provider.go..."
    }
  ]
}
```

退出码：
- `0` 全部通过
- `1` 有 fail 项
- `2` 配置错误

### 5. 把报告发我

把 JSON 输出（**整个报告**，含 detail）贴给我即可——已经做了 mask 处理，不含密钥。

## 安全约束

- 工具**只读取**证书 / 私钥文件路径，**不输出**任何凭证内容
- APPID 在报告中做 mask（`2021********0001`）
- BuyerID 同上 mask
- 工具本身入 git，但**配置（环境变量值）永远不入 git**

## 常见失败排查

| 现象 | 可能原因 | 处理 |
|---|---|---|
| `init_cert_mode` 失败：`get app_cert_sn` | 证书文件路径错或文件损坏 | 验证三个证书文件可读 + 是 PEM 格式 |
| `trade_type_*` 全 fail，错误含 `signature verification failed` | 商户号与证书不匹配 | 确认私钥 + 证书都是同一沙箱应用生成的 |
| `query_nonexistent_order` 状态 fail | v3 协议错误码 prefix 变了（**这是 R1 真实命中**） | 看 `channel_error_code` 实际值，把 `alipay/provider.go` 中 `"ACQ.TRADE_NOT_EXIST"` 替换为实际值 |
| `trade_type_jsapi` 跳过 | 未设置 `ALIPAY_SANDBOX_BUYER_ID` | 不测 JSAPI 可忽略，要测的话补上沙箱买家 buyer_id |

## 未来扩展（如需）

- 支持 `--filter trade_type_page` 只跑某项
- 支持 `--output report.json` 写文件
- 加 `refund-flow` 子命令：自动下单 → 等待 stdin 输入「已支付」→ 跑退款 → 退款查询
- 加 `notify-listener` 子命令：启动本地 HTTP server + 提示用户配 ngrok，验证回调验签

按需添加。当前最小化版本足够覆盖 v1.3.0 上线前最高优先级的盲点。
