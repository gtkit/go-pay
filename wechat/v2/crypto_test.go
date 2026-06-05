package v2

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"testing"
	"time"
)

// encryptECBForTest 以与 decryptRefundReqInfo 相同的密钥派生与分组方式加密，
// 用于构造退款通知解密的测试样本（仅测试使用）。
func encryptECBForTest(t *testing.T, plaintext []byte, apiKey string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(md5Hex(apiKey)))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	bs := block.BlockSize()
	pad := bs - len(plaintext)%bs
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)

	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += bs {
		block.Encrypt(out[i:i+bs], padded[i:i+bs])
	}
	return base64.StdEncoding.EncodeToString(out)
}

func TestDecryptRefundReqInfoRoundTrip(t *testing.T) {
	apiKey := officialKey
	plaintext := []byte(`<root><out_refund_no>R123</out_refund_no><refund_status>SUCCESS</refund_status></root>`)

	cipher := encryptECBForTest(t, plaintext, apiKey)
	got, err := decryptRefundReqInfo(cipher, apiKey)
	if err != nil {
		t.Fatalf("decryptRefundReqInfo error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypted = %q, want %q", got, plaintext)
	}
}

func TestDecryptRefundReqInfoErrors(t *testing.T) {
	t.Run("invalid_base64", func(t *testing.T) {
		if _, err := decryptRefundReqInfo("!!!not-base64!!!", officialKey); err == nil {
			t.Fatal("expected error for invalid base64")
		}
	})
	t.Run("not_block_aligned", func(t *testing.T) {
		bad := base64.StdEncoding.EncodeToString([]byte("123")) // 3 字节，非 16 倍数
		if _, err := decryptRefundReqInfo(bad, officialKey); err == nil {
			t.Fatal("expected error for non block-aligned ciphertext")
		}
	})
	t.Run("wrong_key", func(t *testing.T) {
		cipher := encryptECBForTest(t, []byte("hello world data"), officialKey)
		// 换密钥解密：要么 padding 校验失败报错，要么解出乱码——必须不等于明文
		got, err := decryptRefundReqInfo(cipher, "00000000000000000000000000000000")
		if err == nil && string(got) == "hello world data" {
			t.Fatal("decryption with wrong key recovered plaintext")
		}
	})
}

func TestPkcs7UnpadErrors(t *testing.T) {
	bs := 16
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"bad_length", make([]byte, 7)},
		{"zero_pad", append(make([]byte, 15), 0)},
		{"pad_too_large", append(make([]byte, 15), 17)},
		{"inconsistent", append(bytes.Repeat([]byte{1}, 14), 2, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pkcs7Unpad(tt.data, bs); err == nil {
				t.Fatalf("pkcs7Unpad(%v) error = nil, want error", tt.data)
			}
		})
	}
}

func TestParseV2Time(t *testing.T) {
	tests := []struct {
		name  string
		input string
		zero  bool
	}{
		{"unified_format", "20180608103454", false},
		{"refund_format", "2018-06-08 10:34:54", false},
		{"empty", "", true},
		{"invalid", "not-a-time", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseV2Time(tt.input)
			if tt.zero != got.IsZero() {
				t.Fatalf("parseV2Time(%q).IsZero() = %v, want %v", tt.input, got.IsZero(), tt.zero)
			}
		})
	}
}

func TestFormatV2Time(t *testing.T) {
	tm := time.Date(2018, 6, 8, 10, 34, 54, 0, beijing)
	if got := formatV2Time(tm); got != "20180608103454" {
		t.Fatalf("formatV2Time = %q, want 20180608103454", got)
	}
}
