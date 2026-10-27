# P1：API 模型与渠道定价扩展

状态：开发及本地验收；未部署，未切换生产权威。需求依据为平台仓库 30 号方案（2026-10-26）。

## 数据与权限合同

- 复用原生 `channels` / `abilities` / 模型映射。新增 `api_channel_catalogs` 单例修订号及 `api_channel_prices` 追加式价格行。普通启动仅扩表，不复制价格、不停售商品、不调整权益。
- 价格键为统一模型 × 渠道；四项均需显式填写（允许零），单位为 USD / 百万 Token。转换为原生 billingexpr 表达式，沿用官方 Token 归一化、预扣、结算与退款。
- GET `/api/subscription/admin/api-channel-prices` 需要管理员及 `billing:read`；POST 还需 Root 及 `billing:write`。浏览器沿用原生认证/CSRF。响应采用裁剪 DTO，不发送供应商密钥或访问令牌；成本仅管理员可见。
- POST 要求 `expected_revision`、`changes`、非空 `reason`。数据库 CAS 原子分配全局修订号；旧修订返回 409；价格与顺序同一事务提交；行内保存操作者与原因。
- 渠道映射必须与明确确认的上游模型一致；映射漂移后该组合不可售。停用的最新行不会被旧启用行复活。
- 每模型所有启用行一起继承原渠道优先级，或全部设置不同序号。模型顺序高于渠道整体优先级；售卖许可不扩大普通钱包的分组权限。

## 请求生命周期

请求开始冻结该模型完整的候选价格修订。实际渠道价覆盖原生系统模型价和旧 wallet 覆盖，不叠加分组倍率。跨渠道重试从冻结候选集中取实际渠道价格；更贵渠道在提交前检查并补预留，同一请求只创建一个持久退款凭据。全部失败或余额不足时走原退款链。

消费日志公开部分仅增加价格 ID / 修订号；尝试渠道及价格身份留在管理员日志。请求中途改价不会改变当前请求和备用渠道价格；下一请求读取新版本。

## 激活与尚未完成的范围

`IFLINK_API_CHANNEL_PRICING` 是本次扩展的临时切换开关：空或 `legacy` 保持原计费；`enabled` 才以新目录为权威；非法值拒绝请求。后台在未激活时明确显示“迁移预览”。不能在未回填和对账时启用。

本批支持四分量 Token 计费入口：chat/completions、completions、messages、responses、embeddings、rerank、moderations 及对应 Gemini Token 路径。独立图片、音频、Realtime、异步 task 等入口在新模式明确拒绝；不能把该批当成这些计费维度的适配。Responses WebSocket 复用同一价格适配器，尚无真实上游新价验收。

P2 商品×模型×渠道倍率尚未接入：过渡期商品仍与原权益价格簿允许集合相交，沿用原模型倍率。P3 自动同步、P4 生产清单/回填/切换、P5 单价兜底、P6 旧入口清理/公开价格展示仍有后续工作。旧公开价格页面可能仍反映原生系统价；本批不启用生产新模式，也不宣称所有页面已成为新权威。

## 本地验证（2026-10-26）

- Go 1.26.10。`go test ./model ./service ./relay ./relay/helper ./controller ./middleware -count=1`：3081 项通过。
- 三库价格专项：SQLite 3.50.4（Go modernc）、MySQL 8.4.13、PostgreSQL 16.14。以 `API_CHANNEL_TEST_MYSQL_DSN` / `API_CHANNEL_TEST_POSTGRES_DSN` 指向独立合成测试库运行 `go test ./model -run '^TestAPIChannelCatalogDatabaseMatrix$' -count=1`。覆盖新建/代表性旧行升级、两次迁移、CAS 竞争、停售不复活、映射漂移、公开列表分组隔离、旧渠道密钥/旧价格簿保留。
- 完整启动矩阵：基线 `256b5758f1c26820e56dc3389fa22a05561b3a6a` 未改源码编译；SQLite / MySQL / PostgreSQL 各覆盖空库及基线程序建库升级。候选程序每个库连续启动两次，`GET /api/status` 成功；目录修订保持 0；升级库原 `options` 合成哨兵保留。脚本与原始日志位于本机 `/tmp/iflink-api-startup-matrix/`，未使用生产数据。专项测试另校验旧渠道与价格簿。
- HTTP 端到端：A 成功扣 610；A 失败 B 成功扣 1220；请求中途发布 B 的 100 倍价仍扣 1220；全部失败退全额；余额不足 B 时不提交；停售和跨组均在上游提交前拒绝、不创建预留。故意设分组倍率 7 验证不叠加；原渠道整体优先级与模型顺序相反以验证覆盖。
- 权限 API：普通管理员写权限 / Root 只读权限都不能发布；Root 写权限可发布；过期版本 409；响应不泄漏供应商密钥和 PAT。
- 前端：`bun run typecheck`、相关文件 oxlint、oxfmt、`bun run build`；`bun run test src/features/api-channel-prices/__tests__/editing.test.tsx` 5 项通过。覆盖独立价格、旧 revision、显式填价、复制后独立编辑、孤立行停售及未配置禁用行不生成零价。
- 实际 IAB 浏览器登录合成本地库，填写/复制/修改两渠道不同价格与顺序，发布后回读正确。1280×720 桌面、390×844 窄屏编辑及列表截图检查无重叠或不可读内容；窄屏表格横向滚动。截图见 `verification/api-channel-pricing/`。

上游兼容边界：保持原生系统模型价格持久化不变；新价经 `ModelPriceHelper`、渠道策略及原生 BillingSession 适配；不复制表达式解析器或另建模型资料系统。将来升级仍需复核重试时序、映射、分组和 Token 归一化。

## UI 复用说明

复用 `components/dialog`（弹层布局及窄屏行为）、`StaticDataTable`（列表/空态）、`SectionPageLayout`、Form/RHF/Zod，以及 `features/model-pricing/pricing-amount-input.tsx`。未复用整个原生 `model-pricing-panel.tsx`：该面板编辑模型全局费率/表达式，保存对象和 API 都不含渠道与目录修订；本批增加渠道编辑业务容器，仍使用原生金额输入和计费引擎。复制行为只把四项表单初值拷入另一渠道，不涉及剪贴板，不另写通用复制组件。

## PR 需求来源

本 fork 关闭了 GitHub Issues。用户于本轮明确允许“以 30 号方案作为需求依据”，免建 Issue；此次是 IF.Link 自有 fork 的商业配置扩展，不向上游提交运营配置需求。
