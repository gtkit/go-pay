package alipay

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfigStringRedactsSecrets(t *testing.T) {
	const fakeSecret = "FAKE-TEST-PRIVATE-KEY-DO-NOT-USE"
	cfg := Config{
		AppID:            "2021000100000001",
		PrivateKey:       fakeSecret,
		PrivateKeyPath:   "/etc/alipay/app.pem",
		IsProduction:     true,
		AppCertPublicKey: "FAKE-APP-CERT",
		AlipayPublicKey:  "FAKE-ALIPAY-PUBKEY",
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
			if strings.Contains(tt.out, fakeSecret) {
				t.Fatalf("output leaks private key: %s", tt.out)
			}
			if strings.Contains(tt.out, "FAKE-APP-CERT") || strings.Contains(tt.out, "FAKE-ALIPAY-PUBKEY") {
				t.Fatalf("output leaks cert content: %s", tt.out)
			}
			if !strings.Contains(tt.out, `"****"`) {
				t.Fatalf("output missing redaction marker: %s", tt.out)
			}
			if !strings.Contains(tt.out, cfg.AppID) || !strings.Contains(tt.out, cfg.PrivateKeyPath) {
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
