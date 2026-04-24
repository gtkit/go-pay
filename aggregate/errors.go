package aggregate

import "errors"

var (
	// ErrMissingOpenID 表示微信 JSAPI 分流时缺少 OpenID。
	ErrMissingOpenID = errors.New("aggregate: openid is required for wechat jsapi")
	// ErrInvalidChannelSelection 表示浏览器环境下的渠道选择非法。
	ErrInvalidChannelSelection = errors.New("aggregate: invalid selected channel")
	// ErrMissingOrderBuilder 表示缺少真实支付单构造回调。
	ErrMissingOrderBuilder = errors.New("aggregate: build unified order callback is required")
)
