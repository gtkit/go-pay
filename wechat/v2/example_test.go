package v2_test

import (
	"context"
	"fmt"

	v2 "github.com/gtkit/go-pay/wechat/v2"
)

// ExampleNewProvider 演示创建微信支付 v2 Provider 并发起 Native 下单。
//
// APIKey 是商户平台的 v2 API 密钥（32 位），与 v3 的 APIv3 密钥不同。
// 退款接口还需通过 v2.WithCertPEM 或 v2.WithCertPath 配置商户 API 证书。
func ExampleNewProvider() {
	ctx := context.Background()

	provider, err := v2.NewProvider(ctx,
		v2.WithAppID("wxYOUR_APPID"),
		v2.WithMerchant("YOUR_MCH_ID", "00000000000000000000000000000000"),
		v2.WithNotifyURL("https://example.com/wechat/notify"),
		// 退款需要商户证书：
		// v2.WithCertPath("apiclient_cert.pem", "apiclient_key.pem"),
	)
	if err != nil {
		fmt.Println("init:", err)
		return
	}

	fmt.Println(provider.Channel())
	// Output: wxpayv2
}
