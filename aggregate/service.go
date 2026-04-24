package aggregate

import (
	"context"
	"fmt"
	"strings"

	"github.com/gtkit/go-pay/paymgr"
)

// Action 表示聚合分流后的下一步动作。
type Action string

const (
	ActionChooseChannel Action = "choose_channel"
	ActionRedirect      Action = "redirect"
	ActionQRCode        Action = "qr_code"
	ActionJSAPI         Action = "jsapi"
)

// ResolveRequest 描述一次聚合支付分流请求。
type ResolveRequest struct {
	UserAgent       string
	SelectedChannel paymgr.Channel
	OpenID          string

	BuildUnifiedOrder func(ch paymgr.Channel, tt paymgr.TradeType) (*paymgr.UnifiedOrderRequest, error)
}

// ResolveResult 描述聚合分流结果。
type ResolveResult struct {
	Env       Env
	Action    Action
	Channel   paymgr.Channel
	TradeType paymgr.TradeType
	Response  *paymgr.UnifiedOrderResponse
}

// Service 负责聚合二维码场景下的支付分流。
type Service struct {
	mgr *paymgr.Manager
}

// NewService 创建聚合支付分流服务。
func NewService(mgr *paymgr.Manager) *Service {
	return &Service{mgr: mgr}
}

// Resolve 按入口环境和用户选择决定聚合支付下一步动作。
func (s *Service) Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: resolve request is required", paymgr.ErrInvalidParam)
	}

	env := DetectEnv(req.UserAgent)
	if (env == EnvBrowserPC || env == EnvBrowserMobile) && req.SelectedChannel == "" {
		return &ResolveResult{
			Env:    env,
			Action: ActionChooseChannel,
		}, nil
	}

	channel, tradeType, action, err := resolveDecision(env, req.UserAgent, req.SelectedChannel)
	if err != nil {
		return nil, err
	}
	if env == EnvWechat && req.OpenID == "" {
		return nil, ErrMissingOpenID
	}
	if req.BuildUnifiedOrder == nil {
		return nil, ErrMissingOrderBuilder
	}

	orderReq, err := req.BuildUnifiedOrder(channel, tradeType)
	if err != nil {
		return nil, err
	}
	if orderReq == nil {
		return nil, fmt.Errorf("%w: build unified order returned nil request", paymgr.ErrInvalidParam)
	}
	if s == nil || s.mgr == nil {
		return nil, fmt.Errorf("%w: aggregate service manager is required", paymgr.ErrInvalidParam)
	}

	orderReq.TradeType = tradeType
	if env == EnvWechat && orderReq.OpenID == "" {
		orderReq.OpenID = req.OpenID
	}

	resp, err := s.mgr.UnifiedOrder(ctx, channel, orderReq)
	if err != nil {
		return nil, err
	}

	return &ResolveResult{
		Env:       env,
		Action:    action,
		Channel:   channel,
		TradeType: tradeType,
		Response:  resp,
	}, nil
}

func resolveDecision(env Env, userAgent string, selected paymgr.Channel) (paymgr.Channel, paymgr.TradeType, Action, error) {
	switch env {
	case EnvWechat:
		return paymgr.ChannelWechat, paymgr.TradeTypeJSAPI, ActionJSAPI, nil
	case EnvAlipay:
		if isMobileUserAgent(strings.ToLower(userAgent)) {
			return paymgr.ChannelAlipay, paymgr.TradeTypeH5, ActionRedirect, nil
		}
		return paymgr.ChannelAlipay, paymgr.TradeTypePage, ActionRedirect, nil
	case EnvBrowserMobile:
		switch selected {
		case paymgr.ChannelWechat:
			return paymgr.ChannelWechat, paymgr.TradeTypeH5, ActionRedirect, nil
		case paymgr.ChannelAlipay:
			return paymgr.ChannelAlipay, paymgr.TradeTypeH5, ActionRedirect, nil
		default:
			return "", "", "", ErrInvalidChannelSelection
		}
	case EnvBrowserPC:
		switch selected {
		case paymgr.ChannelWechat:
			return paymgr.ChannelWechat, paymgr.TradeTypeNative, ActionQRCode, nil
		case paymgr.ChannelAlipay:
			return paymgr.ChannelAlipay, paymgr.TradeTypePage, ActionRedirect, nil
		default:
			return "", "", "", ErrInvalidChannelSelection
		}
	default:
		return "", "", "", fmt.Errorf("%w: unsupported env %q", paymgr.ErrInvalidParam, env)
	}
}
