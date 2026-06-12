package wechat

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfigStringRedactsSecrets(t *testing.T) {
	const (
		fakeAPIv3Key = "FAKE0TEST0APIV30KEY0000000000000" // 32 位占位密钥
		fakeKeyPEM   = "FAKE-TEST-MCH-PRIVATE-KEY-PEM"
	)
	cfg := Config{
		AppID:                   "wx0123456789abcdef",
		MchID:                   "1900000001",
		MchCertSerialNumber:     "5157F09EFDC096DE15EBE81A47057A72",
		MchAPIv3Key:             fakeAPIv3Key,
		MchPrivateKeyPEM:        fakeKeyPEM,
		MchPrivateKeyPath:       "/etc/wechat/apiclient_key.pem",
		WechatPayCertificatePEM: "FAKE-PLATFORM-CERT",
		WechatPayPublicKeyID:    "PUB_KEY_ID_0123456789",
	}

	tests := []struct {
		name string
		out  string
	}{
		{"percent_v_value", fmt.Sprintf("%v", cfg)},
		{"percent_plus_v_value", fmt.Sprintf("%+v", cfg)},
		{"string_method", cfg.String()},
		{"percent_hash_v_value", fmt.Sprintf("%#v", cfg)},
		{"percent_v_pointer", fmt.Sprintf("%v", &cfg)},
		{"percent_plus_v_pointer", fmt.Sprintf("%+v", &cfg)},
		{"embedded_by_value", fmt.Sprintf("%+v", struct{ C Config }{C: cfg})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.out, fakeAPIv3Key) || strings.Contains(tt.out, fakeKeyPEM) {
				t.Fatalf("output leaks secret: %s", tt.out)
			}
			if strings.Contains(tt.out, "FAKE-PLATFORM-CERT") {
				t.Fatalf("output leaks cert content: %s", tt.out)
			}
			if !strings.Contains(tt.out, `"****"`) {
				t.Fatalf("output missing redaction marker: %s", tt.out)
			}
			if !strings.Contains(tt.out, cfg.AppID) ||
				!strings.Contains(tt.out, cfg.MchID) ||
				!strings.Contains(tt.out, cfg.WechatPayPublicKeyID) {
				t.Fatalf("output missing non-sensitive fields: %s", tt.out)
			}
		})
	}
}

func TestConfigStringEmptyFields(t *testing.T) {
	out := fmt.Sprintf("%v", Config{})
	if strings.Contains(out, "****") || strings.Contains(out, "<set>") {
		t.Fatalf("empty config should not show redaction or presence markers: %s", out)
	}
}
