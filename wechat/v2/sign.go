package v2

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// SignType 微信支付 v2 的签名算法类型。
type SignType string

const (
	// SignTypeMD5 使用 MD5 签名，是 v2 兼容性最好的默认算法。
	SignTypeMD5 SignType = "MD5"
	// SignTypeHMACSHA256 使用 HMAC-SHA256 签名。
	SignTypeHMACSHA256 SignType = "HMAC-SHA256"
)

// buildSignString 按微信 v2 规则拼接待签名字符串。
//
// 规则：剔除空值与 sign 字段后，按参数名 ASCII 字典序排序，
// 拼接为 key1=value1&key2=value2（值不做 URL 编码）。
func buildSignString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return b.String()
}

// sign 计算微信 v2 签名：在待签名串末尾追加 &key=<apiKey>，
// 按 SignType 计算 MD5 或 HMAC-SHA256，结果转为大写十六进制。
func sign(params map[string]string, apiKey string, st SignType) string {
	source := buildSignString(params) + "&key=" + apiKey

	switch st {
	case SignTypeHMACSHA256:
		h := hmac.New(sha256.New, []byte(apiKey))
		h.Write([]byte(source))
		return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	default:
		sum := md5.Sum([]byte(source))
		return strings.ToUpper(hex.EncodeToString(sum[:]))
	}
}

// verifySign 用相同规则重算签名并与报文携带的 sign 比对。
//
// 报文未携带 sign 时返回 false。
func verifySign(params map[string]string, apiKey string, st SignType) bool {
	want, ok := params["sign"]
	if !ok || want == "" {
		return false
	}
	return hmac.Equal([]byte(sign(params, apiKey, st)), []byte(strings.ToUpper(want)))
}
