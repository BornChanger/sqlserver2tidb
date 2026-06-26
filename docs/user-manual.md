# SQL Server to TiDB Migration Agent 用户手册

版本：0.1
适用仓库：https://github.com/BornChanger/sqlserver2tidb
适用范围：按终极目标设计的 SQL Server 到 TiDB 迁移 GitOps Agent

## 1. 产品定位

`sqlserver2tidb` 是一个面向 SQL Server 到 TiDB 迁移的 GitOps 控制工具。它的终极目标不是替代 DBA 或应用 owner，而是把迁移过程标准化为可审查、可审批、可恢复、可追踪的工程流程。

核心思路：

```text
GitHub repo 保存迁移计划、状态、审批和证据
GitHub PR 承载人工 review 和 approval
Worker 只执行已批准的 instruction
LLM 只生成解释、候选方案和文档，不直接执行迁移
```

仓库 `BornChanger/sqlserver2tidb` 是迁移控制面的 source of truth。所有迁移 metadata 都存放在该仓库中。

## 2. 当前状态与终极目标

### 2.1 当前 MVP 已实现

当前代码已实现：

- `sqlserver2tidb` Go CLI。
- 初始化 GitOps metadata 目录。
- 创建上游 SQL Server 集群目录。
- 在上游 SQL Server 集群下创建迁移项目目录。
- 生成基础 YAML/JSON/Markdown 状态文件。
- 生成核心 JSON Schema 文件。
- 单元测试和 CLI smoke test。

当前 CLI 不会连接真实 SQL Server 或 TiDB，也不会执行真实导出、导入、CDC 或切流。

### 2.2 终极目标

终极形态包含：

- SQL Server discovery。
- 兼容性分析。
- LLM 辅助解释和 schema 改写候选生成。
- SQL Server 到 TiDB schema conversion。
- 全量导出计划和执行。
- TiDB Lightning 或 `IMPORT INTO` 导入。
- SQL Server CDC / Debezium 增量回放。
- 数据校验。
- GitHub PR 审批。
- Worker reconcile loop。
- cutover 编排。
- post-cutover 观察。
- 完整审计证据。

## 3. 核心概念

### 3.1 上游 SQL Server 集群

上游 SQL Server 集群是 metadata 的一级组织单位。

路径：

```text
clusters/<source_cluster_id>/
```

一个上游 SQL Server 集群目录保存：

- 源端连接 profile。
- 源端 inventory。
- 兼容性报告。
- SQL Server CDC checkpoint。
- worker lease。
- 该源集群下的多个迁移项目。

示例：

```text
clusters/prod-sqlserver-a/
```

### 3.2 迁移项目

迁移项目是一次可以独立计划、审批、执行、校验和切流的业务迁移单元。

路径：

```text
clusters/<source_cluster_id>/projects/<project_id>/
```

推荐按业务系统、database、schema 或可独立 cutover 的表组划分 project。

不要默认按单表划分 project，除非这是超大表、归档表或特殊风险表。

### 3.3 GitHub PR

PR 是审批边界。Agent 或 CLI 可以生成文件，但这些文件必须进入 PR，由 DBA、应用 owner、SRE 等角色 review 后，worker 才能执行对应步骤。

### 3.4 Worker

Worker 是终极形态中的执行器。它从 GitHub repo 拉取已批准 instruction，执行确定性操作，并把状态和证据写回 repo。

当前 MVP 尚未实现真实 worker。

### 3.5 LLM

LLM 用于：

- 解释兼容性风险。
- 生成 schema 改写候选。
- 生成迁移计划草案。
- 生成 PR 描述。
- 总结 validation mismatch。
- 提供故障排查建议。

LLM 不允许直接决定或执行：

- DDL。
- import。
- CDC apply。
- cutover。
- cleanup。
- rollback。

## 4. 角色与职责

| 角色 | 职责 |
| --- | --- |
| DBA | 源库权限、CDC、schema review、数据校验 |
| App Owner | 业务表范围、SQL 兼容性、应用回归、cutover 决策 |
| SRE | Worker 部署、网络、对象存储、监控、告警、切流窗口 |
| Migration Operator | 运行 CLI、维护 PR、协调迁移阶段 |
| Security Reviewer | secret reference、权限边界、审计要求 |
| LLM Agent | 生成解释、候选方案、报告和 PR 文本 |

## 5. 安装与构建

### 5.1 前置条件

本地开发需要：

- Go 1.22+
- Git
- 可访问 `https://github.com/BornChanger/sqlserver2tidb`

终极形态运行还需要：

- SQL Server 网络访问。
- TiDB 网络访问。
- 对象存储访问，例如 S3/GCS/NFS。
- Secret manager，例如 Vault/KMS/Kubernetes Secret。
- 可选 Kafka/Debezium。

### 5.2 克隆仓库

```bash
git clone https://github.com/BornChanger/sqlserver2tidb.git
cd sqlserver2tidb
```

### 5.3 构建 CLI

```bash
make build
```

生成二进制：

```text
bin/sqlserver2tidb
```

### 5.4 运行测试

```bash
make test
```

## 6. 初始化迁移控制仓库

在仓库根目录执行：

```bash
bin/sqlserver2tidb init-repo --root .
```

该命令会创建：

```text
global/
  policies/
  schemas/
  templates/
clusters/
```

其中：

- `global/policies/` 保存审批和执行策略。
- `global/schemas/` 保存 JSON Schema。
- `global/templates/` 保存模板。
- `clusters/` 保存所有上游 SQL Server 集群。

## 7. 创建上游 SQL Server 集群

命令：

```bash
bin/sqlserver2tidb create-cluster \
  --root . \
  --cluster-id prod-sqlserver-a \
  --display-name "prod SQL Server A" \
  --listener sqlserver-a.internal \
  --port 1433 \
  --secret-ref vault://migration/prod-sqlserver-a/readonly \
  --owner dba-team,sre-team
```

生成目录：

```text
clusters/prod-sqlserver-a/
  cluster.yaml
  source-profile.yaml
  inventory/
    inventory.json
    compatibility-report.md
    schema-issues.yaml
    source-ddl/
  state/
    cdc-checkpoint.yaml
    worker-lease.yaml
  projects/
```

`cluster.yaml` 示例：

```yaml
cluster_id: prod-sqlserver-a
display_name: "prod SQL Server A"
source:
  type: sqlserver
  host_group: prod-sqlserver-a
  listener: sqlserver-a.internal
  port: 1433
  secret_ref: vault://migration/prod-sqlserver-a/readonly
cdc:
  mode: sqlserver-cdc
  retention_hours_required: 168
owners:
  - dba-team
  - sre-team
```

注意：

- `secret_ref` 只能是 secret 引用，不能是明文密码。
- `cluster-id` 使用小写字母、数字和 `-`。
- CDC checkpoint 是源集群级状态，不放在单个项目下。

## 8. 创建迁移项目

命令：

```bash
bin/sqlserver2tidb create-project \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --display-name "sales DB to TiDB prod A" \
  --source-database sales \
  --source-schema dbo \
  --target-name tidb-prod-a \
  --target-database app \
  --target-secret-ref vault://migration/tidb-prod-a/migrate-user \
  --owner dba-team,app-team
```

生成目录：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/
  project.yaml
  schema/
    tidb-ddl/
    conversion-report.md
    schema-diff.json
  plan/
    migration-plan.yaml
    export-plan.yaml
    import-plan.yaml
    cdc-plan.yaml
    validation-plan.yaml
    cutover-runbook.md
  state/
    migration-state.yaml
    export-chunks.yaml
    import-jobs.yaml
    validation-status.yaml
  evidence/
    precheck.json
    import-summary.json
    cdc-catchup.json
    validation-report.md
    cutover-evidence.md
    post-cutover-report.md
  approvals/
    ddl-approval.yaml
    export-approval.yaml
    import-approval.yaml
    cdc-approval.yaml
    cutover-approval.yaml
```

`project.yaml` 示例：

```yaml
project_id: sales-db-to-tidb-prod-a
display_name: "sales DB to TiDB prod A"
source_cluster_id: prod-sqlserver-a
source:
  type: sqlserver
  database: sales
  schemas:
    - dbo
target:
  type: tidb
  name: tidb-prod-a
  database: app
  secret_ref: vault://migration/tidb-prod-a/migrate-user
mode: short-downtime
status: planning
owners:
  - dba-team
  - app-team
```

## 9. GitHub PR 工作流

### 9.1 推荐分支模型

```text
main
  受保护分支，只接收 review 后的 PR

agent/<project_id>/<stage>
  Agent 或 operator 生成的变更分支

worker/<project_id>/<stage>
  Worker 写回状态或 evidence 的分支
```

### 9.2 PR 类型

| PR 类型 | 文件 | 审批人 |
| --- | --- | --- |
| Discovery PR | `inventory/`、compatibility report | DBA |
| Schema PR | `schema/tidb-ddl/`、schema diff | DBA、App Owner |
| Plan PR | `plan/` | DBA、SRE、App Owner |
| Export PR | `plan/export-plan.yaml`、approval | DBA、SRE |
| Import PR | `plan/import-plan.yaml`、approval | DBA、SRE |
| CDC PR | `plan/cdc-plan.yaml`、approval | DBA、SRE |
| Validation PR | validation plan/result | DBA、App Owner |
| Cutover PR | cutover runbook、approval、evidence | DBA、SRE、App Owner |

### 9.3 审批规则

建议：

- DDL：至少 DBA + App Owner。
- Export：至少 DBA 或 Migration Operator。
- Import：至少 DBA + SRE。
- CDC：至少 DBA + SRE。
- Cutover：至少 DBA + SRE + App Owner，且双人审批。

## 10. 终极迁移流程

### 10.1 阶段 0：准备

目标：

- 确认源 SQL Server 集群。
- 确认目标 TiDB。
- 准备 secret reference。
- 初始化 repo。
- 创建 cluster 和 project。

产物：

- `cluster.yaml`
- `project.yaml`

### 10.2 阶段 1：Discovery

终极形态中，agent 会连接 SQL Server catalog，生成：

```text
inventory/inventory.json
inventory/source-ddl/
inventory/compatibility-report.md
inventory/schema-issues.yaml
```

当前 MVP 只创建占位文件。

Discovery 应覆盖：

- database/schema/table。
- row count 和 size。
- primary key、unique key、index。
- foreign key、check/default constraint。
- identity、computed column。
- view、trigger、procedure、function。
- SQL Server 特殊类型。

### 10.3 阶段 2：兼容性分析

Agent 根据规则识别风险：

- T-SQL procedure/function。
- trigger。
- computed column。
- filtered index。
- included column。
- identity 语义。
- rowversion。
- datetimeoffset。
- uniqueidentifier。
- xml。
- geometry/geography。

LLM 可以把规则命中结果解释成报告，但不能决定是否忽略 blocker。

### 10.4 阶段 3：Schema 转换

终极形态产物：

```text
schema/tidb-ddl/
schema/conversion-report.md
schema/schema-diff.json
```

处理原则：

- 确定性规则先生成 TiDB DDL 草案。
- LLM 只生成候选改写和解释。
- DDL 必须在 shadow TiDB 集群 dry run。
- 最终 DDL 必须通过 PR review。

### 10.5 阶段 4：迁移计划

`plan/migration-plan.yaml` 定义：

- 迁移模式：offline / short-downtime / low-downtime。
- 全量导出格式：CSV / Parquet。
- 导入方式：TiDB Lightning / `IMPORT INTO`。
- CDC 方案：SQL Server CDC / Debezium。
- validation 策略。
- cutover 条件。
- required approvals。

### 10.6 阶段 5：全量导出

终极形态支持：

- bcp。
- Spark。
- Azure Data Factory。
- 自研 JDBC exporter。

状态写入：

```text
state/export-chunks.yaml
```

示例：

```yaml
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
chunks:
  - id: "000001"
    predicate: "id >= 1 AND id < 1000000"
    file_uri: "s3://migration/prod-sqlserver-a/full/app.orders.000001.parquet"
    row_count: 998231
    checksum: "sha256:..."
    status: exported
```

### 10.7 阶段 6：全量导入

终极形态支持：

- TiDB Lightning。
- `IMPORT INTO`。

状态写入：

```text
state/import-jobs.yaml
evidence/import-summary.json
```

导入 gate：

- DDL PR 已合并。
- import approval 已批准。
- 目标表为空或符合导入要求。
- 文件 checksum 已确认。
- TiDB 集群容量已确认。

### 10.8 阶段 7：CDC 增量回放

CDC checkpoint 是源集群级：

```text
clusters/<source_cluster_id>/state/cdc-checkpoint.yaml
```

原因：

- CDC 是 SQL Server 源集群能力。
- 多个 project 可能共享同一个源端 CDC 配置。
- LSN/retention 风险属于源集群级风险。

终极形态支持：

- SQL Server CDC direct reader。
- Debezium SQL Server Connector。
- Kafka-based replay。

LLM 不参与 LSN 判断，也不参与 offset 决策。

### 10.9 阶段 8：数据校验

产物：

```text
state/validation-status.yaml
evidence/validation-report.md
```

校验类型：

- row count。
- 聚合校验。
- sampled hash。
- 业务 SQL 校验。
- 大表分桶校验。

LLM 可以解释 mismatch，但 pass/fail 必须由确定性校验结果决定。

### 10.10 阶段 9：Cutover

cutover 前置条件：

- import 完成。
- CDC 追平。
- validation 通过。
- cutover approval 通过。
- 应用 owner 确认窗口。
- 回滚边界明确。

cutover 产物：

```text
plan/cutover-runbook.md
approvals/cutover-approval.yaml
evidence/cutover-evidence.md
evidence/post-cutover-report.md
```

### 10.11 阶段 10：稳定期

观察：

- TiDB QPS。
- p95/p99 latency。
- error rate。
- slow query。
- TiKV/TiDB CPU、IO、memory。
- 应用业务指标。

稳定后才能清理临时文件和 CDC 配置。

## 11. Worker 运行模型

终极形态 worker reconcile 流程：

```text
1. 拉取 main 分支
2. 扫描 clusters/<source_cluster_id>/cluster.yaml
3. 扫描 clusters/<source_cluster_id>/projects/<project_id>/project.yaml
4. 校验 schema
5. 检查 approval 文件和 PR merge 状态
6. 获取 cluster-level worker lease
7. 执行一个幂等 step
8. 写回 state/evidence 分支
9. 创建 PR 或 bot commit
10. 下一轮 reconcile
```

worker lease：

```text
clusters/<source_cluster_id>/state/worker-lease.yaml
```

worker lease 是上游 SQL Server 集群级，避免多个 worker 同时操作同一个源集群。

## 12. LLM 使用说明

### 12.1 LLM 可以做什么

- 生成兼容性解释。
- 生成 schema 改写候选。
- 生成 migration plan 草案。
- 生成 PR 描述。
- 生成 validation report narrative。
- 生成故障排查建议。

### 12.2 LLM 不能做什么

- 不能直接执行 SQL。
- 不能直接修改数据库。
- 不能判断 cutover 是否允许。
- 不能判断 validation 是否通过。
- 不能决定是否清理源端对象。
- 不能绕过 PR 审批。

### 12.3 LLM 输出要求

所有 LLM 输出必须：

- 落成 GitHub 文件。
- 记录输入摘要和生成时间。
- 通过 schema/lint/dry run。
- 进入 PR。
- 由人 review 后合并。

## 13. 安全要求

### 13.1 Secret

禁止提交：

- SQL Server password。
- TiDB password。
- token。
- private key。
- 完整连接串。

只能提交：

```yaml
secret_ref: vault://migration/prod-sqlserver-a/readonly
```

### 13.2 GitHub 权限

建议：

- `main` 开启 branch protection。
- CODEOWNERS 管控关键目录。
- cutover PR 要求双人审批。
- worker 不能直接绕过 approval gate。

### 13.3 执行权限

SQL Server：

- discovery 使用只读权限。
- export 使用只读数据权限。
- CDC setup 使用独立高权限审批账号。
- CDC reader 使用读取 CDC 的专用账号。

TiDB：

- DDL 使用迁移专用账号。
- import 使用导入专用账号。
- validation 使用只读账号。
- apply 使用目标表 DML 权限。

## 14. 排障手册

### 14.1 create-project 失败：source cluster 不存在

现象：

```text
create project: source cluster "xxx" does not exist
```

处理：

1. 先运行 `create-cluster`。
2. 确认目录存在：

```text
clusters/<source_cluster_id>/cluster.yaml
```

### 14.2 PR 无法合并

检查：

- CODEOWNERS 是否要求额外审批。
- approval 文件是否存在。
- 文件 schema 是否通过。
- 是否修改了受保护路径。

### 14.3 CDC checkpoint 过期

如果 checkpoint 早于 SQL Server CDC min LSN，则增量缺失。

处理：

- 停止 cutover。
- 重新做全量。
- 或由 DBA 制定人工补偿方案。

### 14.4 数据校验失败

处理：

1. 确认源端是否仍有写入。
2. 检查时间字段时区。
3. 检查 decimal 精度。
4. 检查 NULL 和空字符串。
5. 检查 delete 是否被 CDC 捕获。
6. 生成修复 PR，不要直接修目标库。

### 14.5 导入失败

检查：

- 文件是否存在。
- checksum 是否一致。
- 目标表是否为空。
- schema 是否匹配。
- TiDB 权限是否足够。
- TiDB Lightning / `IMPORT INTO` job 错误。

## 15. FAQ

### 15.1 为什么 metadata 不存在数据库里？

因为迁移控制状态低频、需要 review、需要审计。GitHub repo 天然提供 diff、review、approval、history 和 rollback 记录。

高频日志和 CDC 精细 offset 不放 GitHub，只周期性写 checkpoint snapshot。

### 15.2 为什么按上游 SQL Server 集群组织？

因为 inventory、CDC、源端权限、worker lease 都是源集群级边界。一个源集群下可以有多个独立迁移项目。

### 15.3 一个 project 应该多大？

一个 project 最好对应一次真实 cutover。推荐按业务系统、database、schema 或可独立切流的表组划分。

### 15.4 当前能直接迁移数据吗？

当前 MVP 不能直接连接 SQL Server 或 TiDB。它先提供 GitOps metadata 结构和 CLI。真实 discovery、export、import、CDC、validation、worker 将作为后续能力加入。

### 15.5 可以把 LLM 接进来吗？

可以，但 LLM 只能生成草案和解释。所有输出必须经过 GitHub PR review 后才能成为 worker 可执行 instruction。

## 16. 命令参考

### 16.1 init-repo

```bash
bin/sqlserver2tidb init-repo --root .
```

### 16.2 create-cluster

```bash
bin/sqlserver2tidb create-cluster \
  --root . \
  --cluster-id prod-sqlserver-a \
  --display-name "prod SQL Server A" \
  --listener sqlserver-a.internal \
  --port 1433 \
  --secret-ref vault://migration/prod-sqlserver-a/readonly \
  --cdc-mode sqlserver-cdc \
  --retention-hours 168 \
  --owner dba-team,sre-team
```

### 16.3 create-project

```bash
bin/sqlserver2tidb create-project \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --display-name "sales DB to TiDB prod A" \
  --source-database sales \
  --source-schema dbo \
  --target-name tidb-prod-a \
  --target-database app \
  --target-secret-ref vault://migration/tidb-prod-a/migrate-user \
  --mode short-downtime \
  --owner dba-team,app-team
```

## 17. 推荐落地顺序

1. 使用当前 CLI 初始化 repo。
2. 为一个测试 SQL Server 集群创建 cluster。
3. 为一个小 database 创建 project。
4. 通过 PR review metadata 文件。
5. 后续接入 discovery dry-run。
6. 接入 compatibility analyzer。
7. 接入 schema conversion draft。
8. 接入 validation-only worker。
9. 再接入 export/import/CDC。
10. 最后接入 cutover orchestration。
