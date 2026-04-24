## 1. 支付宝 Page 支付基础能力

- [x] 1.1 为 `paymgr` 新增 `TradeTypePage` 及对应测试，明确其语义是支付宝 PC 页面支付
- [x] 1.2 先补支付宝 `TradeTypePage` 的失败测试，覆盖 `PayURL` 返回与 `ReturnURL` / `Metadata` / `ExpireAt` 映射
- [x] 1.3 在 `alipay.Provider.UnifiedOrder(...)` 中实现 `TradeTypePage`
- [x] 1.4 运行 `paymgr` 与 `alipay` 相关测试，确认 `TradeTypePage` 行为稳定且未影响现有交易类型

## 2. 聚合二维码编排层

- [x] 2.1 新增 `aggregate` 包的失败测试，覆盖 `DetectEnv(...)` 和决策表中的主要分流路径
- [x] 2.2 新增聚合显式错误测试，覆盖缺失 OpenID、非法渠道选择、缺失订单构造器、业务构造错误透传和统一下单错误透传
- [x] 2.3 实现 `aggregate` 包的环境识别、动作类型、服务和错误定义
- [x] 2.4 运行 `aggregate` 相关测试，确认分流决策与错误语义符合规范

## 3. 文档与整体验证

- [x] 3.1 更新 README 与示例代码，补充支付宝 `page` 与聚合二维码接入说明
- [x] 3.2 更新变更记录，记录 `TradeTypePage` 与聚合二维码编排能力
- [x] 3.3 运行 `openspec validate add-aggregate-qr-orchestration --type change --strict --json --no-interactive`
- [x] 3.4 运行仓库验证命令，确认新增能力与现有单渠道能力均通过验证
