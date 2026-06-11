package v2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gtkit/go-pay/paymgr"
)

// genCertPEM 生成一对自签证书与私钥的 PEM 文本，用于测试商户证书加载。
func genCertPEM(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-merchant"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{AppID: "a", MchID: "m", APIKey: officialKey}, false},
		{"valid_hmac", Config{AppID: "a", MchID: "m", APIKey: officialKey, SignType: SignTypeHMACSHA256}, false},
		{"no_appid", Config{MchID: "m", APIKey: officialKey}, true},
		{"no_mchid", Config{AppID: "a", APIKey: officialKey}, true},
		{"no_apikey", Config{AppID: "a", MchID: "m"}, true},
		{"short_apikey", Config{AppID: "a", MchID: "m", APIKey: "123"}, true},
		{"bad_signtype", Config{AppID: "a", MchID: "m", APIKey: officialKey, SignType: "SHA1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewProviderNilConfig(t *testing.T) {
	if _, err := NewProviderWithConfig(t.Context(), nil); err == nil {
		t.Fatal("NewProviderWithConfig(nil) error = nil, want error")
	}
	var c *Config
	if err := c.apply(&Config{}); err == nil {
		t.Fatal("(*Config)(nil).apply error = nil, want error")
	}
}

func TestNewProviderWithOptions(t *testing.T) {
	p, err := NewProvider(t.Context(),
		WithAppID("a"),
		WithMerchant("m", officialKey),
		WithSignType(SignTypeHMACSHA256),
		WithNotifyURL("https://example.com/n"),
		WithBaseURL("https://example.com"),
		nil, // nil option 应被跳过
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.signType() != SignTypeHMACSHA256 {
		t.Errorf("signType = %q, want HMAC-SHA256", p.signType())
	}
	if p.cfg.NotifyURL != "https://example.com/n" {
		t.Errorf("NotifyURL = %q", p.cfg.NotifyURL)
	}
	if p.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
}

func TestNewProviderWithConfigCopiesConfig(t *testing.T) {
	cfg := &Config{AppID: "a", MchID: "m", APIKey: officialKey}
	p, err := NewProviderWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewProviderWithConfig: %v", err)
	}
	if p.cfg == cfg {
		t.Fatal("provider should hold a copy of config, not the original pointer")
	}

	// 构造后修改原 Config 字段不得影响 Provider
	cfg.AppID = "mutated"
	cfg.SignType = SignTypeHMACSHA256
	if p.cfg.AppID != "a" {
		t.Fatalf("provider cfg.AppID = %q after mutating original, want %q", p.cfg.AppID, "a")
	}
	if p.signType() != SignTypeMD5 {
		t.Fatalf("signType = %q after mutating original, want MD5", p.signType())
	}
}

func TestNewProviderStructConfig(t *testing.T) {
	// *Config 实现 Option，结构体配置方式应可用，且默认 BaseURL/SignType 生效
	p, err := NewProvider(t.Context(), &Config{AppID: "a", MchID: "m", APIKey: officialKey})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want default", p.baseURL)
	}
	if p.signType() != SignTypeMD5 {
		t.Errorf("signType = %q, want MD5 default", p.signType())
	}
	if p.refundClient != nil {
		t.Error("refundClient should be nil without cert")
	}
}

func TestNewProviderWithCertPEM(t *testing.T) {
	certPEM, keyPEM := genCertPEM(t)
	p, err := NewProvider(t.Context(),
		WithAppID("a"),
		WithMerchant("m", officialKey),
		WithCertPEM(certPEM, keyPEM),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.refundClient == nil {
		t.Fatal("refundClient = nil, want mTLS client built from cert")
	}
}

func TestNewProviderWithCertPath(t *testing.T) {
	certPEM, keyPEM := genCertPEM(t)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "apiclient_cert.pem")
	keyPath := filepath.Join(dir, "apiclient_key.pem")
	if err := os.WriteFile(certPath, []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewProvider(t.Context(),
		WithAppID("a"),
		WithMerchant("m", officialKey),
		WithCertPath(certPath, keyPath),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.refundClient == nil {
		t.Fatal("refundClient = nil, want mTLS client built from cert path")
	}
}

func TestNewProviderBadCert(t *testing.T) {
	_, err := NewProvider(t.Context(),
		WithAppID("a"),
		WithMerchant("m", officialKey),
		WithCertPEM("not-a-cert", "not-a-key"),
	)
	if err == nil {
		t.Fatal("NewProvider with bad cert error = nil, want error")
	}
}

// 确保实现满足 paymgr.Provider 接口（编译期已断言，这里运行期再确认）。
func TestProviderImplementsInterface(t *testing.T) {
	var _ paymgr.Provider = (*Provider)(nil)
}
