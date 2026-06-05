package v2

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// v2TimeLayout 是微信 v2 的时间格式（北京时间，无分隔符）。
const v2TimeLayout = "20060102150405"

// beijing 是微信 v2 时间字段使用的东八区时区。
var beijing = time.FixedZone("CST", 8*60*60)

// md5Hex 返回输入的 MD5 摘要的小写十六进制字符串。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// formatV2Time 将时间格式化为微信 v2 的东八区时间串。
func formatV2Time(t time.Time) string {
	return t.In(beijing).Format(v2TimeLayout)
}

// parseV2Time 解析微信 v2 东八区时间串，无法解析时返回零值。
//
// 兼容两种格式：下单/查询的 time_end 为 "20060102150405"，
// 退款通知的 success_time 为 "2006-01-02 15:04:05"。
func parseV2Time(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{v2TimeLayout, "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, beijing); err == nil {
			return t
		}
	}
	return time.Time{}
}

// decryptRefundReqInfo 解密微信 v2 退款通知中的 req_info 字段。
//
// 算法：对 base64 解码后的密文做 AES-256-ECB 解密，密钥为 MD5(apiKey)
// 的小写十六进制串（32 字节，恰为 AES-256 所需长度），最后去除 PKCS#7 填充。
func decryptRefundReqInfo(reqInfo, apiKey string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(reqInfo)
	if err != nil {
		return nil, fmt.Errorf("decode req_info base64: %w", err)
	}

	block, err := aes.NewCipher([]byte(md5Hex(apiKey)))
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}

	bs := block.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%bs != 0 {
		return nil, fmt.Errorf("req_info ciphertext length %d is not a multiple of block size %d", len(ciphertext), bs)
	}

	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += bs {
		block.Decrypt(plaintext[i:i+bs], ciphertext[i:i+bs])
	}

	return pkcs7Unpad(plaintext, bs)
}

// pkcs7Unpad 去除 PKCS#7 填充，填充非法时返回错误（不 panic）。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	n := len(data)
	if n == 0 || n%blockSize != 0 {
		return nil, fmt.Errorf("pkcs7: invalid data length %d", n)
	}
	pad := int(data[n-1])
	if pad <= 0 || pad > blockSize || pad > n {
		return nil, fmt.Errorf("pkcs7: invalid padding size %d", pad)
	}
	for _, b := range data[n-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("pkcs7: inconsistent padding bytes")
		}
	}
	return data[:n-pad], nil
}
