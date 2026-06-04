package wechat

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
)

func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}

func testPublicKeyPEM(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testCertificate(t *testing.T, priv *rsa.PrivateKey) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return cert
}

// baseConfig 返回除平台证书/公钥外其余必填项齐全的配置。
func baseConfig(priv *rsa.PrivateKey) *Config {
	return &Config{
		AppID:               "wx1234567890abcdef",
		MchID:               "1900000001",
		MchCertSerialNumber: "SERIAL",
		MchAPIv3Key:         "0123456789abcdef0123456789abcdef", // APIv3 Key 必须正好 32 字节（AES-256）
		MchPrivateKey:       priv,
	}
}

func TestConfigValidatePublicKeyMode(t *testing.T) {
	priv := testPrivateKey(t)

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // 子串；空表示期望通过
	}{
		{
			name:   "仅公钥通过",
			mutate: func(c *Config) { c.WechatPayPublicKeyID = "PUB_KEY_ID_1"; c.WechatPayPublicKeyPEM = "pem" },
		},
		{
			name:   "仅平台证书通过",
			mutate: func(c *Config) { c.WechatPayCertificatePEM = "pem" },
		},
		{
			name:    "公钥来源缺公钥ID",
			mutate:  func(c *Config) { c.WechatPayPublicKeyPEM = "pem" },
			wantErr: "wechat_pay_public_key_id is required",
		},
		{
			name:    "证书与公钥均缺",
			mutate:  func(c *Config) {},
			wantErr: "platform certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig(priv)
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPublicKeyOptions(t *testing.T) {
	priv := testPrivateKey(t)
	cfg := &Config{}
	opts := []Option{
		WithPublicKeyID("PUB_KEY_ID_1"),
		WithPublicKeyPath("/keys/pub.pem"),
		WithPublicKeyPEM("pem-text"),
		WithPublicKey(&priv.PublicKey),
	}
	for _, o := range opts {
		if err := o.apply(cfg); err != nil {
			t.Fatalf("apply() error = %v", err)
		}
	}
	if cfg.WechatPayPublicKeyID != "PUB_KEY_ID_1" ||
		cfg.WechatPayPublicKeyPath != "/keys/pub.pem" ||
		cfg.WechatPayPublicKeyPEM != "pem-text" ||
		cfg.WechatPayPublicKey != &priv.PublicKey {
		t.Fatalf("public key options not applied: %+v", cfg)
	}
}

func TestConfigValidateAPIv3KeyLength(t *testing.T) {
	priv := testPrivateKey(t)

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "正好32字节通过", key: "0123456789abcdef0123456789abcdef", wantErr: false},
		{name: "33字节报错", key: "0123456789abcdef0123456789abcdefX", wantErr: true},
		{name: "31字节报错", key: "0123456789abcdef0123456789abcde", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig(priv)
			cfg.MchAPIv3Key = tt.key
			cfg.WechatPayCertificatePEM = "pem" // 满足验签侧二选一
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "must be exactly 32 bytes") {
					t.Fatalf("Validate() error = %v, want 32 bytes error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestConfigUsePublicKey(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "ID与PEM齐全", cfg: Config{WechatPayPublicKeyID: "id", WechatPayPublicKeyPEM: "pem"}, want: true},
		{name: "ID与对象齐全", cfg: Config{WechatPayPublicKeyID: "id", WechatPayPublicKey: &rsa.PublicKey{}}, want: true},
		{name: "缺ID", cfg: Config{WechatPayPublicKeyPEM: "pem"}, want: false},
		{name: "缺来源", cfg: Config{WechatPayPublicKeyID: "id"}, want: false},
		{name: "都缺", cfg: Config{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.usePublicKey(); got != tt.want {
				t.Fatalf("usePublicKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolvePublicKey(t *testing.T) {
	priv := testPrivateKey(t)
	pemText := testPublicKeyPEM(t, &priv.PublicKey)

	t.Run("对象优先", func(t *testing.T) {
		got, err := resolvePublicKey(&Config{WechatPayPublicKey: &priv.PublicKey, WechatPayPublicKeyPEM: "garbage"})
		if err != nil {
			t.Fatalf("resolvePublicKey() error = %v", err)
		}
		if got != &priv.PublicKey {
			t.Fatal("resolvePublicKey() did not return the provided object")
		}
	})

	t.Run("PEM文本", func(t *testing.T) {
		got, err := resolvePublicKey(&Config{WechatPayPublicKeyPEM: pemText})
		if err != nil {
			t.Fatalf("resolvePublicKey() error = %v", err)
		}
		if got.N.Cmp(priv.N) != 0 {
			t.Fatal("resolvePublicKey() returned mismatched key")
		}
	})

	t.Run("文件路径", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pubkey.pem")
		if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		got, err := resolvePublicKey(&Config{WechatPayPublicKeyPath: path})
		if err != nil {
			t.Fatalf("resolvePublicKey() error = %v", err)
		}
		if got.N.Cmp(priv.N) != 0 {
			t.Fatal("resolvePublicKey() returned mismatched key")
		}
	})

	t.Run("缺失来源", func(t *testing.T) {
		if _, err := resolvePublicKey(&Config{}); err == nil {
			t.Fatal("resolvePublicKey() error = nil, want error")
		}
	})
}

func TestBuildAuthCipherSelectsVerifier(t *testing.T) {
	priv := testPrivateKey(t)

	t.Run("公钥模式选用公钥验签器", func(t *testing.T) {
		cfg := baseConfig(priv)
		cfg.WechatPayPublicKeyID = "PUB_KEY_ID_1"
		cfg.WechatPayPublicKey = &priv.PublicKey

		_, verifier, err := buildAuthCipher(cfg, priv)
		if err != nil {
			t.Fatalf("buildAuthCipher() error = %v", err)
		}
		if _, ok := verifier.(*verifiers.SHA256WithRSAPubkeyVerifier); !ok {
			t.Fatalf("verifier type = %T, want *verifiers.SHA256WithRSAPubkeyVerifier", verifier)
		}
	})

	t.Run("证书模式选用证书验签器", func(t *testing.T) {
		cfg := baseConfig(priv)
		cfg.WechatPayCertificate = testCertificate(t, priv)

		_, verifier, err := buildAuthCipher(cfg, priv)
		if err != nil {
			t.Fatalf("buildAuthCipher() error = %v", err)
		}
		if _, ok := verifier.(*verifiers.SHA256WithRSAVerifier); !ok {
			t.Fatalf("verifier type = %T, want *verifiers.SHA256WithRSAVerifier", verifier)
		}
	})
}

// TestNewProviderWithConfigPublicKeyMode 验证公钥模式下 Provider 完整初始化
// （Client + 回调 notify handler）成功，且不依赖网络（公钥模式无需下载平台证书）。
func TestNewProviderWithConfigPublicKeyMode(t *testing.T) {
	priv := testPrivateKey(t)
	cfg := baseConfig(priv)
	cfg.WechatPayPublicKeyID = "PUB_KEY_ID_0000000000000000000000000000"
	cfg.WechatPayPublicKey = &priv.PublicKey

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := NewProviderWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewProviderWithConfig() error = %v", err)
	}
	if p.client == nil || p.notifyHandler == nil {
		t.Fatal("provider not fully initialized")
	}
}
