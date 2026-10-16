# #3 + #4 集成验收与 #5 发布风险评估

日期：2026-10-16。集成 Draft PR：[IF-Link-Tech-Team/new-api #6](https://github.com/IF-Link-Tech-Team/new-api/pull/6)，目标 `main`，分支 `codex/integration-architecture-auto-20261016`。由 Codex 按维护者要求准备，等待人工复核、合并与发布决定。

## 结论与冻结范围

**#6 达到可人工合并状态：完整 CI 通过，未发现 #3 与 #4 集成产生的功能或计费规则回归。** 本分支直接创建自 #4 最终提交，没有重新实现或改写原提交；追加内容仅为验收证据与发布文档。#5 的业务代码不包含在此次集成中。

| 对象 | 冻结提交 | 处理 |
|---|---|---|
| main 基线 | `42b8280358241580076ce7fe00350fbd5890098e` | 已核对远端；是集成分支祖先，无冲突 |
| #3 架构重构 | `dae9a04a231e3e96f9f0d2cdb93ee53d6228ee59` | 原提交保留 |
| #4 Auto 展示修复 | `e4722ebab24d6b23baa283e7b789df3a7d24f6c8` | 集成分支起点，原提交保留 |
| #5 退款补偿 | `34b6db14ff72e665d36a56e75b8d5f654400cf3a` | 仅在独立 detached 工作区评估，原分支未修改 |

人工合并与生产发布是两个判断。#6 **仍保留原异步退款最终失败后缺少持久化恢复的已知缺口**，并未借集成引入 #5。#5 的账务幂等测试已通过，但容量、外部告警送达和完整迁移/回退演练证据仍不足，当前不能判定其生产就绪。发布前必须处置这一退款风险，不能把本报告当成无条件上线批准。

## 完整 CI 与独立验证

[本次完整 CI 38865362440](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440) 在 PR #6 的 main 合并结果上执行，业务 head 为 `e4722ebab`。Ubuntu 24.04.5、Go 1.25.1、Bun 1.3.14，三个会计 job 各用独立 `postgres:16` 服务与合成数据，开启 Go race detector。[PR 格式检查](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362608)通过。文档提交后的最终 CI 状态见 PR #6 检查栏。

| 检查 | 实际结果 |
|---|---|
| [后端完整 CI](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440/job/116856822862) | 主模块与独立 relaykit 的 vet、build、make test 均通过，共 40 个有测试的包 |
| [前端完整 CI](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440/job/116856823040) | 冻结依赖安装、main/head 类型检查、完整测试通过；head 179/179，41/41 文件 |
| [PG model + race](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440/job/116856823126) | 47 个叶子用例，51 条父/子 pass 事件，0 失败/跳过 |
| [PG service + race](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440/job/116856823056) | 3 个叶子用例，0 失败/跳过 |
| [PG controller + race](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38865362440/job/116856822981) | 10 个叶子用例，12 条父/子 pass 事件，0 失败/跳过 |
| 本地完整主模块，关闭缓存 | 34 个测试包，1,214 个叶子用例、1,300 条 pass 事件，11 个显式跳过，0 失败 |
| 本地独立 relaykit，关闭缓存 | 6 个测试包，243 个叶子用例、271 条 pass 事件，0 失败/跳过 |
| 本地前端生产构建 | Windows、Bun 1.4.3；冻结安装后 `build:check`（类型检查 + Rsbuild）通过，仅生成本地产物 |

同一 CI runner 对 main/head 使用完全相同运行时和断言：

| 运行方式 | main | 集成 head |
|---|---|---|
| 原始 `bun test` | 118 pass / 16 fail，另有 13 errors | 179 pass / 0 fail |
| 每文件独立 Bun 进程 | 175 pass / 3 fail | 179 pass / 0 fail |

main 的三项真实失败均为 Auto 标记契约；原始批量运行还受跨文件 mock 污染影响。#4 恢复已有组件并新增一项语言切换回归，原三项断言保留。隔离运行器不重试、不筛除失败文件，仍保留非零失败退出码。CI artifact `frontend-base-head-comparison` 保存四份原始对比日志，不能把 main 的失败误报为集成失败。

11 个本地主模块跳过项中，三个新增 PostgreSQL 并发用例已由上述 CI 实际执行。其余八项没有据此宣称通过：课堂 200 账户 HTTP 场景、旧 schema PostgreSQL preflight、既有月订阅 PostgreSQL 并发、Key 长度 MySQL/PostgreSQL 迁移、生产快照迁移不变量、refresh hash MySQL/PostgreSQL 配置迁移。后者依赖各自独立开关/环境，不属于本次 CI 已执行范围。未访问生产快照；未验证真实浏览器 E2E、真实支付、付费上游或 MySQL 实例。

## 功能、锁顺序与计费规则复核

| 边界 | 代码与回归证据 | 结论 |
|---|---|---|
| 登录、Key 认证和 API | `middleware/auth_test.go`、`service/auth_session_test.go`、`controller/auth_flow_test.go`；路由/鉴权实现相对 main 无改动，公开函数签名保留 | 全量后端测试通过，没有扩展权限或改接口 |
| 模型、商品倍率与 fallback | `service/product_billing_test.go`、`service/cilai_route_policy_test.go`、`controller/billing_chains_e2e_test.go`；表达式、商品快照和渠道策略实现未修改 | Fresh/Cached/Output、倍率只应用一次和合成 HTTP 调用链通过 |
| Key 资金池边界 | `service/billing_pool_isolation_test.go`；无绑定 Key 只扣钱包，绑定 Key 只扣对应权益，失败不能跨池或退回钱包 | 既有规则保留；#4 只恢复视觉身份，不改 Auto 路由 |
| 套餐、升级、流量包 | PG 实际执行 `TestValidateV1ContractRejectsFrozenRuleDrift`、`TestMonthlyTiersShareOneConcurrentSafePurchaseWindow`、`TestMonthlyUpgradeKeepsSubscriptionKeyAndExpiryAndOnlyAddsDifference`、`TestRepeatedPackPurchasesProvisionIndependentPoolsAndKeys` | 套餐互斥窗口、升级差额/原 Key/到期时间、重复包独立权益与 Key 断言通过 |
| 订阅预扣/退款 | `model/subscription_reservation.go`；`model/subscription.go` 的 `RefundSubscriptionPreConsume`；PG 确定性交错测试 | 同一池→原预扣记录的锁顺序保留；不同请求争额度、重复 request_id、退款/重放均通过，未观察到未处理死锁、重复扣减或额度丢失 |
| 结算/退款竞态 | `service/billing_session_lifecycle_test.go`，PG + race | 退款先发生则禁止迟到结算；结算先发生则不误退，最终池/Key/钱包断言通过 |
| 支付回调/人工补单 | `model/topup_quota_test.go`、PG model/controller 回调并发测试 | 同订单只入账一次；错误签名、金额/商户/渠道不符和溢出不入账 |
| 充值额度口径 | `model/topup_quota.go` 集中回调与补单单位 | 保留各 provider 历史单位；IFLinkPay 按精确到分的 Money 入账，修复整数 Amount 舍入导致的不一致，不改变价格或 QuotaPerUnit |
| 商品展示/既有权益 | `controller/cilai_product_price.go` 仅澄清注释；商品可见性/权益和 Schema 定义未修改 | 没有新增商品迁移、改价或重新绑定 Key |

锁序复核重点：预扣先普通查询 request_id，再按 `end_time, id` 锁池，之后锁定重读预扣记录；退款先普通定位不可变池 ID，再锁池、锁记录。重放元数据查询使用独立 GORM statement，不在记录锁后重新申请池锁。测试通过不能证明所有可能的并发交错；本报告针对已知交叉锁序及覆盖的事务链路作结论。

**已知退款缺口仍在 #6：**`TestSubscriptionBillingSessionExhaustedRefundRequiresExplicitRecovery` 明确证明三次重试耗尽后仅日志/内存终态，订阅记录仍为 consumed。旧七天清理不区分是否成功结算，不能将所有 consumed 自动退款。它不是 #4 集成引入的回归，也不是本轮扩大实现的理由；发布前必须明确值守对账和证据保留措施，或另行完成 #5 集成验收。

## #5：SQL 写入开销的隔离测量

在 #4 与 #5 的冻结提交上，用同一份 [诊断脚本](acceptance/20261016/sql-write-diagnostic.go.txt)通过 Go overlay 运行；未向产品代码或常规测试集加入脚本。Windows Go 1.25.1 连接私有 loopback PostgreSQL 16.14，`SQL_DSN`/`LOG_SQL_DSN` 清空，显式使用 `CILAI_TEST_POSTGRES_DSN`。连接前检查库名 `cilai_pr3_*`、loopback、无 fallback 和版本 16；额外核对私有 data_directory、用户及端口 57416。数据均为合成用户、Key 和权益。每个版本六个用例全部通过，并核对最终额度。

测量关闭 Redis、批量更新和信任旁路：统计 GORM 实际成功执行的 INSERT/UPDATE/DELETE，排除 fixture、SELECT、BEGIN/COMMIT、调用链外的日志/用量统计及后台任务维护；**不是延迟、WAL 字节、磁盘 IOPS 或吞吐量测试**。明细见 [CSV](acceptance/20261016/sql-write-counts.csv)。

| 合成 BillingSession 路径 | #4 DML | #5 DML | 增量 |
|---|---:|---:|---:|
| 钱包：预扣 100，结算 100 | 2 | 6 | +4 |
| 钱包：预扣 100，结算 60 | 4 | 8 | +4 |
| 钱包：预扣 100，追加到 160 后退款 | 6 | 13 | +7 |
| 订阅：预扣 100，结算 100 | 3 | 7 | +4 |
| 订阅：预扣 100，结算 60 | 5 | 9 | +4 |
| 订阅：预扣 100，追加到 160 后退款 | 9 | 16 | +7 |

新增凭据写入对应：预扣 `INSERT prepared + UPDATE reserved` 两次；成功结算 `UPDATE settling + UPDATE settled` 两次（即使差额为零）；每次追加预扣两次；成功退款意图、事务内 `cache_pending`、缓存后 `succeeded` 三次。每次失败记录还需更新 attempts/error/retry，缓存阶段重试不再改资金。与原来启用批量更新的运行方式相比，还新增同步用户/Key SQL 写入，不能用表中“+4”估算全部线上增量；Key 预扣校验也改为读 SQL。

`BillingRefund` 为每次正额预扣创建一行，不仅失败请求才建行。至少保留 30 天的终态凭据；非终态不删。当前有主键及 request_id/user_id/status/next_retry_at/created_at/updated_at 六个单列索引，status 和 updated_at 频繁更新。未来应按实际请求量估算 30 天存储、WAL、索引维护与 vacuum 开销。没有执行代表性负载、数据库故障下的持续积压压测或大表 EXPLAIN，因此未报告 QPS/性能百分比，也未擅自增加索引。

## #5：积压容量与告警有效性

代码依据为冻结 #5 的 `service/billing_refund_task.go`、`model/billing_refund.go`、`service/system_task.go`、`service/user_notify.go` 和 `service/notify-limit.go`。

- 请求失败先异步尝试处理一条；定时任务负责重启及失败恢复。后台任务仅在 master 节点启动，按类型持有一个数据库租约，每轮最多 **100 条、串行处理**，不是每个节点 100 条。多个 master 不能线性扩容同一任务类型。
- 下一轮按上一轮完成后的 UpdatedAt 等待 60 秒，调度还存在 15 秒轮询与其他任务开销。若一条平均耗时为 t 秒，饱和批次理想吞吐为 `100 / (60 + 100t + 调度开销)` 条/秒，而不是固定 100 条/分钟。单条 SQL 尝试 30 秒、失败记录另 10 秒；100 条耗尽这些预算就可能消耗约 67 分钟。缓存、查 due、统计、清理和外部通知不全受这些 context 约束，**这不是整轮超时上限**。
- 失败从 60 秒指数退避到最多一小时，持续重试，不在第三次丢弃。60 秒租约由约 20 秒心跳续期，并非任务截止时间。租约丢失/重复投递仍靠退款凭据行锁和同事务资金标记避免重复入账，但重复扫描/等待会消耗连接和容量。
- `status IN (...) AND next_retry_at <= now ORDER BY next_retry_at,id LIMIT 100`、告警 OR 查询及清理子查询目前没有专用复合/部分索引。单列索引不能据此证明大表容量；应在代表性状态分布与保留量的隔离库用 EXPLAIN/BUFFERS 后决定。积压时跳过终态清理，legacy consumed 也保留，长期故障会增加表量。
- pending/failed/cache_pending 即使尚未到重试时间，也会让对账任务显示失败。失败次数≥3、review 立即、prepared/reserved/settling 六小时未推进会计入 Alerts。持久化 task 结果先保存，再尝试通知 root；复用 Root 对账 API，具备可查询和审阅路径。
- **持久化异常可查询不等于告警已送达。** 默认限制为同用户/通知类型 10 分钟 2 次；发送失败会消耗限额，没有投递回执/确认升级。未配置收件地址的通知可能返回 nil；Redis 限流故障也可能阻断发送。计数和通知在本轮批次结束之后，严重积压会延迟告警；数据库完全不可用时，凭据查询及任务结果落库本身也可能失败。

#5 已有的[完整后端及三组 PG + race CI](https://github.com/IF-Link-Tech-Team/new-api/actions/runs/38845741923)通过，覆盖事务内退款、失败恢复、真实 COMMIT 确认丢失、缓存阶段不重复入账、Root 权限和清理保护；其前端仍是未包含 #4 的三项 Auto 基线失败。本轮未改动 #5，也未把它标成全绿。

**#5 发布前仍需补证/处置：**在约定业务负载与故障恢复目标下验证写入和积压排空容量；在隔离环境实际验证通知送达、限流/失败场景，并明确独立任务停摆/数据库故障监控及值守人；执行新旧版本迁移与非终态回退演练。这些是发布门槛，不在本轮新增基础设施或修改原 PR。

## #5：迁移与旧版本回退风险

1. 新表注册在标准和快速 AutoMigrate，原有业务列未改。隔离测试实际创建了新表；没有据此宣称完整标准/快速启动迁移、真实大表耗时或多节点升级演练通过。AutoMigrate 仍会检查其他既有模型，需从已备份并可恢复的 Schema 做预演；先完成迁移，再允许任何 #5 节点处理请求。
2. 新版本中的非终态凭据不清理，终态至少 30 天；原预扣只在 refunded 或有持久化 settled 依据时清理，并排除未完成退款。旧 consumed 结果不明会保留。这是必要保护，也意味着增长必须有容量预算。
3. **旧 master 维护任务会在启动及后续维护中按年龄删除七天原预扣，忽略新表。** 滚动升级时不能让这种旧清理继续与新补偿共存；代码没有独立的预扣清理开关，不应在清单中虚构一个开关。若不能证明旧清理已停止，应中止混合版本方案。
4. 旧程序不处理 BillingRefund；保留表并不代表回退安全。prepared/reserved/settling/review 需要独立核对，不能按超时批量退款；pending/failed/cache_pending 需要由兼容 worker 完成或转入受控人工处置。`confirm_settled` 只记录关闭、不修复余额，必须先有独立对账与纠正依据。
5. 回退不能 DROP 新表、清空任务/非终态记录、直接恢复上线前数据库覆盖新增交易，或让旧清理毁掉幂等证据。若无法保留兼容补偿和证据，应停止新写入并暂停回退，交由维护者按凭据核对。程序版本回退不能替代资金对账。

最简操作门禁见[生产发布与回退清单](release-rollback-checklist-20261016.md)。本轮没有访问生产数据库、合并 main、部署、重启业务服务或发生真实现金交易；仅启动并关闭私有测试 PostgreSQL。#3/#4/#5 的提交与 PR 保持原状。
