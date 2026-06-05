package v2

import "testing"

// 微信支付 v2 官方签名示例，用作 MD5 已知向量。
const (
	officialKey     = "192006250b4c09247ec02edce69f6a2d"
	officialMD5Sign = "9A0A8659F005D6984697E2CA0A9CF3B7"
)

func officialParams() map[string]string {
	return map[string]string{
		"appid":       "wxd930ea5d5a258f4f",
		"mch_id":      "10000100",
		"device_info": "1000",
		"body":        "test",
		"nonce_str":   "ibuaiVcKdpRxkhJA",
	}
}

func TestSignMD5OfficialVector(t *testing.T) {
	got := sign(officialParams(), officialKey, SignTypeMD5)
	if got != officialMD5Sign {
		t.Fatalf("sign(MD5) = %s, want %s", got, officialMD5Sign)
	}
}

func TestBuildSignStringSkipsEmptyAndSign(t *testing.T) {
	params := map[string]string{
		"b":    "2",
		"a":    "1",
		"c":    "",  // 空值应跳过
		"sign": "X", // sign 字段应跳过
	}
	got := buildSignString(params)
	want := "a=1&b=2"
	if got != want {
		t.Fatalf("buildSignString = %q, want %q", got, want)
	}
}

func TestSignTypeSelectsAlgorithm(t *testing.T) {
	params := officialParams()
	md5Sign := sign(params, officialKey, SignTypeMD5)
	hmacSign := sign(params, officialKey, SignTypeHMACSHA256)
	if md5Sign == hmacSign {
		t.Fatal("MD5 and HMAC-SHA256 produced identical signatures")
	}
	if len(hmacSign) != 64 {
		t.Fatalf("HMAC-SHA256 hex length = %d, want 64", len(hmacSign))
	}
	if len(md5Sign) != 32 {
		t.Fatalf("MD5 hex length = %d, want 32", len(md5Sign))
	}
}

func TestVerifySign(t *testing.T) {
	for _, st := range []SignType{SignTypeMD5, SignTypeHMACSHA256} {
		params := officialParams()
		params["sign"] = sign(params, officialKey, st)
		if !verifySign(params, officialKey, st) {
			t.Fatalf("verifySign(%s) = false, want true", st)
		}

		// 篡改密钥应验签失败
		if verifySign(params, "00000000000000000000000000000000", st) {
			t.Fatalf("verifySign(%s) with wrong key = true, want false", st)
		}
	}
}

func TestVerifySignMissingSign(t *testing.T) {
	if verifySign(officialParams(), officialKey, SignTypeMD5) {
		t.Fatal("verifySign without sign field = true, want false")
	}
}

func BenchmarkSignMD5(b *testing.B) {
	params := officialParams()
	b.ReportAllocs()
	for b.Loop() {
		_ = sign(params, officialKey, SignTypeMD5)
	}
}

func BenchmarkSignHMACSHA256(b *testing.B) {
	params := officialParams()
	b.ReportAllocs()
	for b.Loop() {
		_ = sign(params, officialKey, SignTypeHMACSHA256)
	}
}
