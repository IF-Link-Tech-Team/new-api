# Auto 标记与前端 CI 验收

日期：2026-10-16。分支 `codex/fix-auto-badge-20261016`，基线为 PR #3 的已冻结提交 `dae9a04a231e3e96f9f0d2cdb93ee53d6228ee59`。本 PR 以该架构分支为 base，独立展示后续差异，不修改 PR #3；合并架构分支后可将 base 改为 main。

## 产品依据与变更

[上游 Auto 功能 PR #6590](https://github.com/QuantumNous/new-api/pull/6590)明确要求“令牌表格和分组下拉框增加 Auto 及 Auto 倍率的流光边框，并支持减少动态效果设置”。提交 `0ab02020603d22e5613bc4cf46bfab06f8567769` 的历史说明也保留了这项要求，但最后包含 `temporarily disable AutoGroupBadge in api-key-group-cell`。这是实际实现与保留的展示契约不一致，未发现词来自研业务规则要求隐藏已配置 Auto Key 的身份。

恢复 `ApiKeyGroupCell` 中现有的 `AutoGroupBadge`，复用已有本地化、静态边框和减少动态效果逻辑；Cross-group 标记和倍率组件继续沿用现有行为。没有修改 Key 的分组、自动路由、倍率或 fallback 规则。

原三项失败断言完整保留：有倍率时两个边框、减少动态效果时没有动画层、缺少倍率时仍保留 Auto 身份。新增语言从英文切为中文时，无倍率的 Auto 标记随之更新且保持静态的回归用例。

## 本地证据

独立 Windows 工作区，Bun 1.4.3，冻结依赖；DOM 使用 happy-dom，无后端服务、数据库、真实支付或外部模型调用。

| 检查 | 结果 |
|---|---|
| 修复前，加入新回归后运行目标文件 | 1 pass / 4 fail；包含原三项失败及新语言切换失败 |
| 修复后目标文件 | 5 pass / 0 fail |
| 完整隔离运行器 `bun run test` | 179 pass / 0 fail，41/41 文件通过 |
| typecheck、受影响文件 oxlint、格式、生产构建 | 通过；构建仅生成本地产物 |

CI 继续使用同一 Ubuntu runner、Bun 1.3.14 比较 base/head，保留所有断言及非零失败退出码。远端结果另在 Draft PR 的 Proof of Work 中记录，不能把本地结果冒充远端 CI。

没有部署、合并或生产操作。AI 辅助实现，等待人工审阅。
