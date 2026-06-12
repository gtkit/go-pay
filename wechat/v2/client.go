package v2

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gtkit/go-pay/paymgr"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// defaultBaseURL 是微信支付 v2 的正式环境网关地址。
const defaultBaseURL = "https://api.mch.weixin.qq.com"

// maxResponseBytes 限制单次响应体读取上限，防止异常响应耗尽内存。
const maxResponseBytes = 1 << 20 // 1 MiB

// Config 微信支付 v2 配置。
//
// 注意：APIKey 是商户平台设置的 v2 API 密钥（32 位），
// 与微信支付 v3 的 APIv3 密钥（MchAPIv3Key）是两个不同的密钥，不可混用。
// Config 含 API 密钥与商户证书等敏感凭据。fmt 系列打印（%v / %+v / %s / %#v）
// 经 String / GoString 输出脱敏摘要；但 json.Marshal 与反射遍历仍会暴露
// 字段原文，请勿将 Config 序列化输出。
type Config struct {
	AppID     string   // 公众号/小程序/开放平台应用的 AppID
	MchID     string   // 商户号（APP 二次签名中的 partnerid）
	APIKey    string   // v2 API 密钥（32 位），用于签名与退款回调解密
	SignType  SignType // 签名算法，留空默认 MD5
	NotifyURL string   // 默认异步通知地址，下单未显式提供时使用

	// 商户 API 证书（apiclient_cert.pem + apiclient_key.pem），仅退款等
	// /secapi 接口需要。PEM 文本与文件路径二选一：同时提供 CertPEM+KeyPEM
	// 走文本，否则用 CertPath+KeyPath。未配置则无法调用 Refund。
	CertPEM  string // 商户证书 PEM 文本
	KeyPEM   string // 商户私钥 PEM 文本
	CertPath string // 商户证书文件路径
	KeyPath  string // 商户私钥文件路径

	// BaseURL 覆盖网关地址，留空使用正式环境，主要用于测试注入。
	BaseURL string
	// Client 覆盖底层 HTTP 客户端，留空使用内置默认值。注入时退款也复用该
	// 客户端（由注入方负责配置商户证书），主要用于测试。
	Client *http.Client
}

// String 实现 fmt.Stringer，输出脱敏后的配置摘要：密钥显示 "****"，
// 证书 PEM 与 HTTP 客户端仅标注 <set>，路径与 ID 原样输出。
//
// 脱敏仅对 fmt 系列打印生效，json.Marshal 与反射遍历仍会暴露原文；
// Config 新增字段时需同步维护本方法。
func (c Config) String() string {
	return fmt.Sprintf("v2.Config{AppID:%q, MchID:%q, APIKey:%s, SignType:%q, NotifyURL:%q, "+
		"CertPEM:%s, KeyPEM:%s, CertPath:%q, KeyPath:%q, BaseURL:%q, Client:%s}",
		c.AppID, c.MchID, redact(c.APIKey), c.SignType, c.NotifyURL,
		presence(c.CertPEM != ""), redact(c.KeyPEM), c.CertPath, c.KeyPath,
		c.BaseURL, presence(c.Client != nil))
}

// GoString 实现 fmt.GoStringer，%#v 同样输出脱敏摘要。
func (c Config) GoString() string { return c.String() }

// redact 敏感字段脱敏：空值显示 ""，非空显示 "****"。
func redact(s string) string {
	if s == "" {
		return `""`
	}
	return `"****"`
}

// presence 大块内容仅标注是否已配置。
func presence(set bool) string {
	if set {
		return "<set>"
	}
	return "<nil>"
}

// Option 用于函数选项模式配置 v2 Provider。
//
// *Config 也实现了该接口，因此结构体配置方式仍可使用：
//
//	v2.NewProvider(ctx, &v2.Config{...})
type Option interface {
	apply(*Config) error
}

type optionFunc func(*Config) error

func (f optionFunc) apply(cfg *Config) error { return f(cfg) }

func (c *Config) apply(dst *Config) error {
	if c == nil {
		return fmt.Errorf("wechat/v2: config is required")
	}
	*dst = *c
	return nil
}

// WithAppID 设置应用 AppID。
func WithAppID(appID string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.AppID = appID
		return nil
	})
}

// WithMerchant 设置商户号与 v2 API 密钥。
func WithMerchant(mchID, apiKey string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.MchID = mchID
		cfg.APIKey = apiKey
		return nil
	})
}

// WithSignType 设置签名算法（默认 MD5）。
func WithSignType(st SignType) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.SignType = st
		return nil
	})
}

// WithNotifyURL 设置默认异步通知地址。
func WithNotifyURL(url string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.NotifyURL = url
		return nil
	})
}

// WithCertPEM 通过 PEM 文本设置商户 API 证书与私钥（退款所需）。
func WithCertPEM(certPEM, keyPEM string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.CertPEM = certPEM
		cfg.KeyPEM = keyPEM
		return nil
	})
}

// WithCertPath 通过文件路径设置商户 API 证书与私钥（退款所需）。
func WithCertPath(certPath, keyPath string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.CertPath = certPath
		cfg.KeyPath = keyPath
		return nil
	})
}

// WithBaseURL 覆盖网关地址（主要用于测试）。
func WithBaseURL(url string) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.BaseURL = url
		return nil
	})
}

// WithHTTPClient 注入自定义 HTTP 客户端（主要用于测试）。
func WithHTTPClient(client *http.Client) Option {
	return optionFunc(func(cfg *Config) error {
		cfg.Client = client
		return nil
	})
}

// hasCert 报告是否提供了商户证书来源（PEM 或路径任一组完整）。
func (c *Config) hasCert() bool {
	return (c.CertPEM != "" && c.KeyPEM != "") || (c.CertPath != "" && c.KeyPath != "")
}

// Validate 校验配置完整性。
func (c *Config) Validate() error {
	if c.AppID == "" {
		return fmt.Errorf("wechat/v2: app_id is required")
	}
	if c.MchID == "" {
		return fmt.Errorf("wechat/v2: mch_id is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("wechat/v2: api_key is required")
	}
	if len(c.APIKey) != 32 {
		return fmt.Errorf("wechat/v2: api_key must be exactly 32 bytes, got %d", len(c.APIKey))
	}
	if c.SignType != "" && c.SignType != SignTypeMD5 && c.SignType != SignTypeHMACSHA256 {
		return fmt.Errorf("wechat/v2: unsupported sign_type %q", c.SignType)
	}
	return nil
}

// Provider 微信支付 v2 提供者，实现 paymgr.Provider 接口。
type Provider struct {
	cfg          *Config
	baseURL      string
	client       *http.Client // 普通接口客户端
	refundClient *http.Client // 退款 mTLS 客户端；既未注入也无证书时为 nil
}

// NewProvider 以函数选项创建 v2 Provider。
func NewProvider(ctx context.Context, opts ...Option) (*Provider, error) {
	cfg := &Config{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt.apply(cfg); err != nil {
			return nil, err
		}
	}
	return NewProviderWithConfig(ctx, cfg)
}

// NewProviderWithConfig 使用结构体配置创建 v2 Provider。
//
// 传入的 cfg 会被值拷贝，Provider 构造后修改原 Config 的字段不影响其行为；
// 注入的 Client 指针指向的对象仍与调用方共享，构造后请勿原地修改。
func NewProviderWithConfig(_ context.Context, cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wechat/v2: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfgCopy := *cfg
	cfg = &cfgCopy

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	// 退款客户端：注入优先，否则在配置了商户证书时构建带客户端证书的 mTLS 客户端。
	refundClient := cfg.Client
	if refundClient == nil && cfg.hasCert() {
		var err error
		refundClient, err = buildRefundClient(cfg)
		if err != nil {
			return nil, err
		}
	}

	return &Provider{
		cfg:          cfg,
		baseURL:      baseURL,
		client:       client,
		refundClient: refundClient,
	}, nil
}

// buildRefundClient 加载商户证书，构建用于 /secapi 接口的双向 TLS 客户端。
func buildRefundClient(cfg *Config) (*http.Client, error) {
	cert, err := loadCert(cfg)
	if err != nil {
		return nil, fmt.Errorf("wechat/v2: load merchant cert: %w", err)
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			},
		},
	}, nil
}

// loadCert 按配置从 PEM 文本或文件路径加载商户证书。
func loadCert(cfg *Config) (tls.Certificate, error) {
	switch {
	case cfg.CertPEM != "" && cfg.KeyPEM != "":
		return tls.X509KeyPair([]byte(cfg.CertPEM), []byte(cfg.KeyPEM))
	case cfg.CertPath != "" && cfg.KeyPath != "":
		return tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	default:
		return tls.Certificate{}, fmt.Errorf("missing merchant certificate")
	}
}

// signType 返回生效的签名算法，未配置时为 MD5。
func (p *Provider) signType() SignType {
	if p.cfg.SignType == "" {
		return SignTypeMD5
	}
	return p.cfg.SignType
}

// doRequest 完成一次 v2 接口调用：补全公共字段与签名、发送 XML 请求、
// 校验通信/业务状态码并验签，返回响应参数表。
func (p *Provider) doRequest(ctx context.Context, client *http.Client, path string, params map[string]string) (map[string]string, error) {
	params["appid"] = p.cfg.AppID
	params["mch_id"] = p.cfg.MchID
	params["sign_type"] = string(p.signType())
	nonce, err := utils.GenerateNonce()
	if err != nil {
		return nil, fmt.Errorf("wechat/v2: generate nonce: %w", err)
	}
	params["nonce_str"] = nonce
	params["sign"] = sign(params, p.cfg.APIKey, p.signType())

	body, err := encodeXML(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("wechat/v2: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wechat/v2: do request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	// 网关异常（502/503 等）时 body 通常不是 XML，先报状态码保留排障信息。
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wechat/v2: unexpected http status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("wechat/v2: read response: %w", err)
	}

	result, err := decodeXML(data)
	if err != nil {
		return nil, err
	}
	return p.checkResponse(result)
}

// checkResponse 校验响应的通信状态、签名与业务状态。
func (p *Provider) checkResponse(m map[string]string) (map[string]string, error) {
	if m["return_code"] != "SUCCESS" {
		return nil, paymgr.NewChannelError(paymgr.ChannelWechatV2, "COMM_ERROR", m["return_msg"], nil)
	}
	if !verifySign(m, p.cfg.APIKey, p.signType()) {
		return nil, fmt.Errorf("%w: wechat v2 response sign mismatch", paymgr.ErrInvalidSign)
	}
	if m["result_code"] != "SUCCESS" {
		return nil, paymgr.NewChannelError(paymgr.ChannelWechatV2, m["err_code"], m["err_code_des"], nil)
	}
	return m, nil
}
