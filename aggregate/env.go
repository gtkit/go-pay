package aggregate

import "strings"

// Env 表示聚合支付入口识别出的运行环境。
type Env string

const (
	EnvWechat        Env = "wechat"
	EnvAlipay        Env = "alipay"
	EnvBrowserPC     Env = "browser_pc"
	EnvBrowserMobile Env = "browser_mobile"
)

// DetectEnv 根据 User-Agent 识别当前聚合支付入口环境。
func DetectEnv(userAgent string) Env {
	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "micromessenger"):
		return EnvWechat
	case strings.Contains(ua, "alipayclient"):
		return EnvAlipay
	case isMobileUserAgent(ua):
		return EnvBrowserMobile
	default:
		return EnvBrowserPC
	}
}

func isMobileUserAgent(userAgent string) bool {
	for _, token := range []string{"mobile", "android", "iphone", "ipad"} {
		if strings.Contains(userAgent, token) {
			return true
		}
	}
	return false
}
