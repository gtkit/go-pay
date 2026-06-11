package alipay

// 本文件覆盖 Config 校验、函数选项与 NewProvider / NewProviderWithConfig 构造路径。
// 所有密钥与证书均在运行时生成：私钥/公钥复用 provider_gateway_test.go 的
// testKeys；证书模式使用平台密钥现场签发的自签名证书。

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
)

// testCertPEM 用平台密钥生成自签名证书（SHA256WithRSA），同时充当
// 应用公钥证书 / 支付宝根证书 / 支付宝公钥证书。
func testCertPEM(t *testing.T) string {
	t.Helper()
	keys := testKeys(t)

	tmpl := &x509.Certificate{
		SerialNumber:       big.NewInt(1),
		Subject:            pkix.Name{CommonName: "go-pay-alipay-test"},
		NotBefore:          time.Now().Add(-time.Hour),
		NotAfter:           time.Now().Add(time.Hour),
		SignatureAlgorithm: x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &keys.platformKey.PublicKey, keys.platformKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// writeTempFile 将内容写入临时文件并返回路径。
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string // 为空表示期望通过
	}{
		{
			name: "public_key_mode_ok",
			cfg:  &Config{AppID: "app-1", PrivateKey: "key", AlipayPublicKey: "pub"},
		},
		{
			name: "private_key_path_ok",
			cfg:  &Config{AppID: "app-1", PrivateKeyPath: "/path/key.pem", AlipayPublicKey: "pub"},
		},
		{
			name: "cert_mode_ok",
			cfg: &Config{
				AppID: "app-1", PrivateKey: "key",
				AppCertPublicKey: "app-cert", AlipayRootCert: "root-cert", AlipayCertPublicKey: "alipay-cert",
			},
		},
		{
			name:    "missing_app_id",
			cfg:     &Config{PrivateKey: "key", AlipayPublicKey: "pub"},
			wantErr: "app_id is required",
		},
		{
			name:    "missing_private_key",
			cfg:     &Config{AppID: "app-1", AlipayPublicKey: "pub"},
			wantErr: "private_key or private_key_path is required",
		},
		{
			name: "partial_cert_mode",
			cfg: &Config{
				AppID: "app-1", PrivateKey: "key",
				AppCertPublicKey: "app-cert",
			},
			wantErr: "cert mode requires app cert, root cert and alipay cert",
		},
		{
			name:    "no_cert_no_public_key",
			cfg:     &Config{AppID: "app-1", PrivateKey: "key"},
			wantErr: "either cert paths or alipay_public_key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigUseCertMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{
			name: "all_cert_contents",
			cfg:  &Config{AppCertPublicKey: "a", AlipayRootCert: "r", AlipayCertPublicKey: "c"},
			want: true,
		},
		{
			name: "all_cert_paths",
			cfg:  &Config{AppCertPublicKeyPath: "a.crt", AlipayRootCertPath: "r.crt", AlipayCertPublicKeyPath: "c.crt"},
			want: true,
		},
		{
			name: "partial_cert",
			cfg:  &Config{AppCertPublicKey: "a", AlipayRootCert: "r"},
			want: false,
		},
		{
			name: "public_key_mode",
			cfg:  &Config{AlipayPublicKey: "pub"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.UseCertMode(); got != tt.want {
				t.Fatalf("UseCertMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewProviderPublicKeyMode(t *testing.T) {
	keys := testKeys(t)

	p, err := NewProvider(
		nil, // nil Option 应被跳过
		WithAppID(testGatewayAppID),
		WithProduction(false),
		WithPrivateKey(keys.appPrivatePEM),
		WithAlipayPublicKey(keys.platformPubPEM),
	)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if p.Channel() != paymgr.ChannelAlipay {
		t.Fatalf("Channel() = %q, want %q", p.Channel(), paymgr.ChannelAlipay)
	}
	if p.cfg.AppID != testGatewayAppID {
		t.Fatalf("cfg.AppID = %q, want %q", p.cfg.AppID, testGatewayAppID)
	}
}

func TestNewProviderPrivateKeyPath(t *testing.T) {
	keys := testKeys(t)
	keyPath := writeTempFile(t, "app_private_key.txt", keys.appPrivatePEM)

	p, err := NewProvider(
		WithAppID(testGatewayAppID),
		WithPrivateKeyPath(keyPath),
		WithAlipayPublicKey(keys.platformPubPEM),
	)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if p == nil {
		t.Fatal("NewProvider() = nil")
	}
}

func TestNewProviderCertMode(t *testing.T) {
	keys := testKeys(t)
	certPEM := testCertPEM(t)

	t.Run("with_cert_contents", func(t *testing.T) {
		p, err := NewProvider(
			WithAppID(testGatewayAppID),
			WithPrivateKey(keys.appPrivatePEM),
			WithCertMode(certPEM, certPEM, certPEM),
		)
		if err != nil {
			t.Fatalf("NewProvider() error = %v", err)
		}
		if !p.cfg.UseCertMode() {
			t.Fatal("UseCertMode() = false, want true")
		}
	})

	t.Run("with_cert_paths", func(t *testing.T) {
		appCertPath := writeTempFile(t, "app_cert.crt", certPEM)
		rootCertPath := writeTempFile(t, "root_cert.crt", certPEM)
		alipayCertPath := writeTempFile(t, "alipay_cert.crt", certPEM)

		p, err := NewProvider(
			WithAppID(testGatewayAppID),
			WithPrivateKey(keys.appPrivatePEM),
			WithCertModePaths(appCertPath, rootCertPath, alipayCertPath),
		)
		if err != nil {
			t.Fatalf("NewProvider() error = %v", err)
		}
		if !p.cfg.UseCertMode() {
			t.Fatal("UseCertMode() = false, want true")
		}
	})
}

func TestNewProviderWithStructConfigOption(t *testing.T) {
	keys := testKeys(t)

	t.Run("config_as_option", func(t *testing.T) {
		p, err := NewProvider(&Config{
			AppID:           testGatewayAppID,
			PrivateKey:      keys.appPrivatePEM,
			AlipayPublicKey: keys.platformPubPEM,
		})
		if err != nil {
			t.Fatalf("NewProvider() error = %v", err)
		}
		if p == nil {
			t.Fatal("NewProvider() = nil")
		}
	})

	t.Run("nil_config_as_option", func(t *testing.T) {
		if _, err := NewProvider((*Config)(nil)); err == nil {
			t.Fatal("NewProvider((*Config)(nil)) error = nil, want error")
		}
	})
}

func TestNewProviderWithConfigErrors(t *testing.T) {
	keys := testKeys(t)
	certPEM := testCertPEM(t)

	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "nil_config",
			cfg:     nil,
			wantErr: "config is required",
		},
		{
			name:    "invalid_config",
			cfg:     &Config{},
			wantErr: "app_id is required",
		},
		{
			name: "private_key_path_not_exist",
			cfg: &Config{
				AppID:           testGatewayAppID,
				PrivateKeyPath:  filepath.Join(t.TempDir(), "missing_key.txt"),
				AlipayPublicKey: keys.platformPubPEM,
			},
			wantErr: "load private key",
		},
		{
			name: "malformed_private_key",
			cfg: &Config{
				AppID:           testGatewayAppID,
				PrivateKey:      "not-a-private-key",
				AlipayPublicKey: keys.platformPubPEM,
			},
			wantErr: "init client",
		},
		{
			name: "malformed_app_cert",
			cfg: &Config{
				AppID:               testGatewayAppID,
				PrivateKey:          keys.appPrivatePEM,
				AppCertPublicKey:    "not-a-cert",
				AlipayRootCert:      certPEM,
				AlipayCertPublicKey: certPEM,
			},
			wantErr: "load app cert",
		},
		{
			name: "root_cert_path_not_exist",
			cfg: &Config{
				AppID:               testGatewayAppID,
				PrivateKey:          keys.appPrivatePEM,
				AppCertPublicKey:    certPEM,
				AlipayRootCertPath:  filepath.Join(t.TempDir(), "missing_root.crt"),
				AlipayCertPublicKey: certPEM,
			},
			wantErr: "load root cert",
		},
		{
			name: "malformed_alipay_cert",
			cfg: &Config{
				AppID:               testGatewayAppID,
				PrivateKey:          keys.appPrivatePEM,
				AppCertPublicKey:    certPEM,
				AlipayRootCert:      certPEM,
				AlipayCertPublicKey: "not-a-cert",
			},
			wantErr: "load alipay cert",
		},
		{
			name: "malformed_alipay_public_key",
			cfg: &Config{
				AppID:           testGatewayAppID,
				PrivateKey:      keys.appPrivatePEM,
				AlipayPublicKey: "not-a-public-key",
			},
			wantErr: "load alipay public key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProviderWithConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewProviderWithConfig() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

// TestNewProviderWithConfigCopiesConfig 构造后修改原 Config 不应影响 Provider。
func TestNewProviderWithConfigCopiesConfig(t *testing.T) {
	keys := testKeys(t)
	cfg := &Config{
		AppID:           testGatewayAppID,
		PrivateKey:      keys.appPrivatePEM,
		AlipayPublicKey: keys.platformPubPEM,
	}

	p, err := NewProviderWithConfig(cfg)
	if err != nil {
		t.Fatalf("NewProviderWithConfig() error = %v", err)
	}

	cfg.AppID = "changed-after-new"
	if p.cfg.AppID != testGatewayAppID {
		t.Fatalf("cfg.AppID = %q, want %q (config should be copied)", p.cfg.AppID, testGatewayAppID)
	}
}
