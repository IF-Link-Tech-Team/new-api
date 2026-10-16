# SQL 写入诊断复现

这是 #5 发布评估的诊断附件，不进入 `go test ./...` 常规测试集，不是性能基准。完整 CI 和业务验收见[集成报告](../../integration-acceptance-20261016.md)。

1. 分别 checkout 报告中冻结的 #4、#5 提交到两个独立目录；不要改写这些分支。
2. 准备两个仅本机可达、库名以 `cilai_pr3_` 开头的 PostgreSQL 16 合成测试库。测试会迁移/清理 fixture 表，不得使用真实用户数据、生产快照或生产连接。也可串行使用同一私有库；两个版本不能并发操作同一库。
3. 建立 Go overlay JSON，将各工作区内不存在的 `service/zz_sql_diagnostic_test.go` 绝对路径映射到本目录 `sql-write-diagnostic.go.txt` 的绝对路径：`{"Replace":{"/absolute/checkout/service/zz_sql_diagnostic_test.go":"/absolute/evidence/sql-write-diagnostic.go.txt"}}`。Windows 使用对应的绝对路径；这不会修改原工作区中的 Go 文件。
4. 显式清空 `SQL_DSN`、`LOG_SQL_DSN`；设置 `GOWORK=off`、`CILAI_TEST_POSTGRES_DSN` 为上面的私有连接。#4 设置 `CILAI_DIAGNOSTIC_RECEIPTS=no`，#5 设置为 `yes`，以便等待完整持久化退款终态后读取计数并关闭 fixture。
5. 用 Go 1.25.1 在各工作区根目录执行 `go test -overlay /absolute/overlay.json ./service -run '^TestIsolatedSQLWriteDiagnostic$' -count=1 -timeout=120s -v`。六个场景应通过，`DML phase=...` 输出各阶段表/操作次数；复核最终钱包/订阅与 Key 断言。与 CSV 比较前必须确认版本、设置及统计边界相同。

本次使用已核对身份的私有 PostgreSQL 16.14、loopback 57416，合成用户/Key ID 801。日志和私有集群身份检查留在调用者工作区 `artifacts/integration-acceptance-20261016/`；未上传数据库、真实 Key 或支付凭据。GORM trace 只记录成功执行的 DML；没有计入 SELECT、事务控制、缓存和调用链外统计，也没有测量并发吞吐、真实硬件延迟或故障容量。
