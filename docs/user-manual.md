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
- 校验 GitOps metadata 目录结构。
- 生成 SQL Server discovery dry-run 计划。
- 通过环境变量连接串执行只读 SQL Server catalog discovery。
- 基于 `inventory/inventory.json` 生成规则化兼容性分析报告。
- 基于 SQL Server inventory 和 project metadata 生成项目级 TiDB DDL 草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级全量导出/导入计划草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级 CDC 计划草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级 row-count validation plan 草稿。
- 基于 stage 生成本地 PR draft 文件，并通过 dry-run-by-default wrapper 准备 `gh pr create`。
- 计算 ddl、export、import、cdc、validation payload hash。
- 在 approval 通过后执行 metadata-only export/import/CDC/validation worker，把 plan 写成 planned state/evidence 或 validation 结果。
- 在 approval 通过后执行 `worker-executor` dry-run，生成外部 DDL/export/import/CDC/validation 执行器命令。
- 通过 `worker-executor --stage ddl` 或 `sqlserver2tidb-executor apply-ddl` 对 TiDB 执行已 review 的 DDL，通过 `worker-executor --stage validation` 或 `sqlserver2tidb-executor validate-count` 对源/目标对象执行显式行数校验。
- 在 approval 通过后执行 metadata-only validation worker。
- 执行只读 `worker-reconcile --dry-run`，扫描 ready/blocked worker actions。
- 执行 `worker-reconcile --execute-next`，在源集群 lease 保护下执行第一个 ready metadata-only worker action。
- 可选执行 `worker-reconcile --execute-next --state-pr-draft`，为 state/evidence/lease 写回生成项目级 PR draft。
- 通过 `create-worker-state-pr` dry-run 准备 state/evidence/lease 写回 PR 的 git push 和 GitHub 命令。
- 通过 `generate-executor-evidence-pr-draft` 生成 executor-only evidence PR body，并通过 `create-executor-evidence-pr` dry-run 准备 git push 和 GitHub 命令，尤其是 DDL apply evidence。
- 创建上游 SQL Server 集群目录。
- 在上游 SQL Server 集群下创建迁移项目目录。
- 生成基础 YAML/JSON/Markdown 状态文件。
- 生成核心 JSON Schema 文件。
- 单元测试和 CLI smoke test。

当前 CLI 在执行 `discover-sqlserver --connection-string-env ...` 时会连接 SQL Server，并且只读取 catalog metadata。`sqlserver2tidb-executor apply-ddl --execute` 可以显式连接 TiDB，执行已 review 且不含 `TODO` 的 DDL 文件。`sqlserver2tidb-executor export --execute` 也可以显式连接 SQL Server，把一个已审批的导出 work item 写成本地 `file://` CSV 或 HTTP(S) CSV，例如对象存储预签名 URL；CSV 会带内部 `__sqlserver2tidb_null_bitmap` 尾列，用来保留 SQL NULL。`sqlserver2tidb-executor import --execute` 可以显式连接 TiDB，把本地 `file://` CSV 或 HTTP(S) CSV 逐行写入目标表，并在识别该内部尾列时恢复 NULL。`sqlserver2tidb-executor validate-count --execute` 可以显式同时连接 SQL Server 和 TiDB，对一个源/目标对象做 `COUNT(*)` 对比。除此之外，它不会执行原生 `s3://` 对象存储 IO、TiDB Lightning、`IMPORT INTO`、CDC、切流、checksum、sampled hash 或业务 SQL 校验。`discover-sqlserver --dry-run` 只输出计划，不打开数据库连接，也不写 inventory 文件。`analyze-compatibility`、`generate-schema-draft`、`generate-data-plans`、`generate-cdc-plan`、`generate-validation-plan`、`generate-pr-draft`、`create-pr` 的默认 dry-run、`create-worker-state-pr` 的默认 dry-run、`create-executor-evidence-pr` 的默认 dry-run、`worker-executor` 的默认 dry-run、`sqlserver2tidb-executor` 的默认 dry-run、`compute-payload-hash`、`worker-export`、`worker-import`、`worker-cdc`、`worker-validate`、`worker-reconcile --dry-run` 和 `worker-reconcile --execute-next` 只读取并写回或汇报 GitHub metadata 文件。`worker-reconcile --state-pr-draft` 和 `generate-executor-evidence-pr-draft` 只生成 Markdown PR body，不调用 GitHub。`create-pr --execute` 会调用本地 `gh pr create`；`create-worker-state-pr --execute` 和 `create-executor-evidence-pr --execute` 会调用本地 `git` 和 `gh`；`sqlserver2tidb-executor cdc --execute` 当前会返回 not implemented。

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

当前 MVP 可以生成本地 PR draft 文件，不会直接调用 GitHub API。生成的 draft 保存到：

```text
clusters/<source_cluster_id>/prs/<stage>-pr.md
clusters/<source_cluster_id>/projects/<project_id>/prs/<stage>-pr.md
```

这些文件包含标题、建议分支名、review 文件清单、审批文件、reviewer 角色、operator checklist 和建议的 `gh pr create` 命令。

### 3.4 Worker

Worker 是终极形态中的执行器。它从 GitHub repo 拉取已批准 instruction，执行确定性操作，并把状态和证据写回 repo。

当前 MVP 实现了显式指定 project 的 `worker-export`、`worker-import`、`worker-cdc` 和 `worker-validate`。它们都需要 approval 和 payload hash 匹配；`worker-export`、`worker-import`、`worker-cdc` 和 `worker-validate` 还要求对应 plan 已经是 `reviewed` 或 `approved`。当前还实现了 `worker-executor`，用于在同一 approval/hash gate 后生成外部执行器命令，默认 dry-run。当前还实现了 `worker-reconcile --dry-run` 和 `worker-reconcile --execute-next`；后者会获取源集群级 lease，并执行第一个 ready metadata-only action，且 DDL action 在 `schema/schema-diff.json` 未 `reviewed` 时会被视为 blocked，export、import、CDC 或 validation action 在对应 plan 仍为 `draft` 时也会被视为 blocked。加上 `--state-pr-draft` 后，reconcile 单步执行可以生成 state/evidence/lease 写回的 PR body 草稿。`create-worker-state-pr` 可以默认 dry-run 地准备 bot branch、commit、push 和 GitHub PR 命令；如果存在 `evidence/executor-<stage>-run.json`，会一并纳入提交文件列表；dry-run 会提示 PR body 文件列表是否需要刷新，只有显式 `--execute` 时才会刷新 body 并调用本地 `git` 和 `gh`。对于 DDL 这类 executor-only 阶段，`generate-executor-evidence-pr-draft` 可以把 `evidence/executor-ddl-run.json` 转成 PR body，`create-executor-evidence-pr` 可以默认 dry-run 地准备 evidence PR 的 bot branch、commit、push 和 GitHub PR 命令。

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
bin/sqlserver2tidb-executor
```

安装到指定目录：

```bash
make install PREFIX="$HOME/.local"
```

会写入：

```text
$HOME/.local/bin/sqlserver2tidb
$HOME/.local/bin/sqlserver2tidb-executor
```

本地构建 release 归档：

```bash
make dist VERSION=v0.1.0
```

每个归档包含两个二进制、`README.md`、`LICENSE`，以及 `docs/` 下的核心文档。

只构建指定平台归档：

```bash
DIST_TARGETS="linux/amd64 darwin/arm64" make dist VERSION=v0.1.0
```

归档会写入 `dist/`。

发布二进制时，推送形如 `v0.1.0` 的 tag 会触发 release workflow，为 Linux、macOS 和 Windows 构建归档、生成 checksums，并创建 GitHub Release。

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
- `global/schemas/` 保存 JSON Schema，包括 cluster、project、migration、export、import、CDC 和 validation plan metadata。
- `global/templates/` 保存模板。
- `clusters/` 保存所有上游 SQL Server 集群。

初始化后建议立即执行结构校验：

```bash
bin/sqlserver2tidb validate-repo --root .
```

如果仓库结构完整，命令会输出：

```text
repository is valid at . (5 dirs, 13 files checked)
```

如果缺少必需文件、必需目录、file schema policy 映射，或者 inventory JSON 无法解析或 status 不是 `pending` / `discovered`，或者 cluster/project 目录名与元数据 ID 不一致，或者 `source-profile.yaml`、cluster state、CDC checkpoint mode/phase/status/updated_at、worker lease phase、active worker lease 必需字段、project state phase/status/updated_at、export/import state phase/status/updated_at、validation status state/phase/updated_at、state payload hash、schema diff status/generated_at/summary counts、evidence JSON ownership/status/generated_at、executor evidence JSON、`plan/migration-plan.yaml`、approval 文件中的 `project_id` / `source_cluster_id` / action / mode / status / payload hash 与项目元数据不一致，或者 approval 的 `approved_at` 非空但不是 RFC3339，或者已 approved 的 approval 缺少 reviewer / `payload_hash` / `approved_at`，或者 export/import/CDC/validation plan 缺少 status 或 status 不是 `draft` / `reviewed` / `approved`，或者 export/import/CDC plan 中已有 work item 但缺少执行必需字段，或者 export plan 使用当前 executor 不支持的 `format`，或者 import plan 使用当前 executor 不支持的 `engine`，或者 export chunk / import job / CDC source / validation check 出现重复标识，或者 import job 引用了不存在的 export chunk，或者 import job 的 `source_uri` 与所依赖 export chunk 的 `output_uri` 不一致，或者 export chunk predicate 仍包含 `TODO`，或者 `plan/validation-plan.yaml` 中的 `row_count` / `row-count` 检查项缺少 `id`、`source_object`、`target_object`，或 predicate / target predicate 仍包含 `TODO`，命令会返回非零退出码，并列出问题。空的 draft plan 列表仍然是合法的初始化状态；`phase: idle` 的 worker lease 可以为空闲占位文件，但 `export`、`import`、`cdc` 或 `validation` 这类 active phase 必须包含非空 `holder`、`lease_id`、`project_id`、`expires_at` 和 `renewed_at`，`project_id` 必须引用同一源集群下已存在的项目目录，两个时间字段必须是 RFC3339，且 `expires_at` 不能早于 `renewed_at`。示例：

```text
repository validation failed at .:
- missing required file: global/policies/execution-policy.yaml
```

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
  prs/
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
- `--owner` 至少指定一个责任方，多个 owner 用逗号分隔。
- 如果 `clusters/<source_cluster_id>/` 已存在，命令会失败，不会覆盖已有状态文件。
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
    validation-approval.yaml
    cutover-approval.yaml
  prs/
```

`state/migration-state.yaml` 的 `phase` 只接受 `planning`、`ddl`、`export`、`import`、`cdc`、`validation`、`cutover` 或 `completed`；`status` 只接受 `not_started`、`planned`、`running`、`completed` 或 `failed`；`updated_at` 必须非空并使用 RFC3339。
`state/export-chunks.yaml` 和 `state/import-jobs.yaml` 如果存在顶层 `phase`，必须分别是 `export` 和 `import`；如果存在顶层 `status`，目前只接受 `planned`；如果存在 `updated_at`，它必须使用 RFC3339。
`state/validation-status.yaml` 的 `status` 只接受 `pending`、`passed` 或 `failed`；如果存在顶层 `phase`，它必须是 `validation`；如果存在 `updated_at`，它必须使用 RFC3339。

如果 `clusters/<source_cluster_id>/projects/<project_id>/` 已存在，命令会失败，不会覆盖已有项目状态文件。

`--owner` 至少指定一个项目责任方，多个 owner 用逗号分隔。

`--mode` 只允许 `offline`、`short-downtime`、`low-downtime`；默认是 `short-downtime`。

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

### 9.4 生成 PR draft

项目级 PR draft 示例：

```bash
bin/sqlserver2tidb generate-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

输出文件：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/prs/schema-pr.md
```

集群级 discovery PR draft 示例：

```bash
bin/sqlserver2tidb generate-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --stage discovery
```

输出文件：

```text
clusters/prod-sqlserver-a/prs/discovery-pr.md
```

支持的 stage：

| Stage | Scope | 典型用途 |
| --- | --- | --- |
| `discovery` | source cluster | review inventory 和兼容性报告 |
| `schema` | project | review TiDB DDL 草稿和 schema diff |
| `plan` | project | review 迁移计划和阶段策略 |
| `export` | project | review 全量导出计划和 approval |
| `import` | project | review 全量导入计划和 approval |
| `cdc` | project | review CDC 计划和 approval |
| `validation` | project | review 数据校验计划和报告 |
| `cutover` | project | review 切流 runbook、approval 和 evidence |

注意：该命令只生成 PR 正文草稿和建议命令，不会创建 GitHub PR，不会 merge PR，也不会检查 GitHub approval 状态。

### 9.5 创建 GitHub PR

`create-pr` 会读取已经生成的 PR draft，并构造确定性的 `gh pr create` 命令。

默认 dry-run：

```bash
bin/sqlserver2tidb create-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

dry-run 只打印命令，不调用 GitHub API。确认分支已经 push 且 body file 正确后，显式执行：

```bash
bin/sqlserver2tidb create-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema \
  --execute
```

限制：

- 必须先运行 `generate-pr-draft`。
- 不会自动创建或 push 分支。
- 不会 merge PR。
- 不会 approval PR。
- 不会绕过 GitHub branch protection 或 CODEOWNERS。

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

当前 MVP 会创建占位文件，并支持两种 discovery 模式：

- dry-run：只根据 `cluster.yaml` 和目录结构输出计划，不连接 SQL Server，不写入 inventory 文件。
- catalog discovery：通过环境变量中的 SQL Server 连接串读取当前连接数据库的 `sys.*` catalog，并写回 `inventory/inventory.json`。

dry-run 命令：

```bash
bin/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --dry-run
```

dry-run 输出包括：

- 将来真实 discovery 会更新的目标文件。
- 将来真实 discovery 会读取的 SQL Server catalog 范围。
- 明确的 no-write/no-connect 说明。

真实 catalog discovery 命令：

```bash
bin/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
```

要求：

- `SQLSERVER2TIDB_SQLSERVER_DSN` 由 secret manager、CI secret 或本地安全方式注入。
- 连接串不得提交到 GitHub。
- SQL Server 账号只需要读取 catalog 的权限。
- 当前实现发现的是连接串指向的当前数据库。

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

当前 MVP 已支持确定性规则分析，输入是：

```text
clusters/<source_cluster_id>/inventory/inventory.json
```

输出是：

```text
clusters/<source_cluster_id>/inventory/schema-issues.yaml
clusters/<source_cluster_id>/inventory/compatibility-report.md
```

命令：

```bash
bin/sqlserver2tidb analyze-compatibility \
  --root . \
  --source-cluster-id prod-sqlserver-a
```

当前内置规则覆盖：

- `xml` column。
- `rowversion` / `timestamp` column。
- computed column。
- filtered index。
- included columns。
- trigger。
- procedure/function/routine。

LLM 可以把规则命中结果解释成更易读的迁移建议，但不能决定是否忽略 blocker。

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

当前 MVP 已支持项目级 schema draft：

```bash
bin/sqlserver2tidb generate-schema-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

输入：

- `clusters/<source_cluster_id>/inventory/inventory.json`
- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/schema/tidb-ddl/*.sql`
- `clusters/<source_cluster_id>/projects/<project_id>/schema/conversion-report.md`
- `clusters/<source_cluster_id>/projects/<project_id>/schema/schema-diff.json`

当前实现特点：

- 只处理 `project.yaml` 中指定的 source database 和 source schemas。
- 单 schema project 默认保留原表名；多 schema project 会用 `<schema>_<table>` 作为目标表名，避免表名碰撞。
- 常见 SQL Server 类型会映射到 TiDB/MySQL 类型，例如 `int` -> `INT`、`nvarchar` -> `VARCHAR(255)`、`datetime2` -> `DATETIME`。
- `xml`、`rowversion/timestamp`、`datetimeoffset`、`sql_variant`、spatial 类型等会生成 DDL 草稿，但标记为 manual review。
- 生成器不会连接 TiDB，也不会执行 DDL。

示例 DDL：

```sql
-- Generated by sqlserver2tidb. Review before execution.
-- Source: sales.dbo.orders
CREATE TABLE IF NOT EXISTS `app`.`orders` (
  `id` INT,
  `customer_name` VARCHAR(255),
  `payload` TEXT /* TODO: SQL Server xml requires manual mapping */
);
```

这一步的输出应该进入 Schema PR，由 DBA 和 App Owner review 后才能继续后续执行阶段。LLM 可以根据 `conversion-report.md` 和 `schema-diff.json` 生成解释或候选改写，但不能直接批准或执行 DDL。

### 10.5 阶段 4：迁移计划

`plan/migration-plan.yaml` 定义：

- 迁移模式：offline / short-downtime / low-downtime。
- 全量导出格式：CSV / Parquet。
- 导入方式：TiDB Lightning / `IMPORT INTO`。
- CDC 方案：SQL Server CDC / Debezium。
- validation 策略。
- cutover 条件。
- required approvals。

当前 MVP 已支持从 inventory 和 project metadata 生成项目级全量导出/导入计划草稿：

```bash
bin/sqlserver2tidb generate-data-plans \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full \
  --chunk-size-rows 1000000 \
  --export-format csv \
  --import-engine sql-insert
```

输入：

- `clusters/<source_cluster_id>/inventory/inventory.json`
- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`
- `--object-uri-prefix`
- `--chunk-size-rows`
- `--export-format`
- `--import-engine`

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/plan/export-plan.yaml`
- `clusters/<source_cluster_id>/projects/<project_id>/plan/import-plan.yaml`

当前实现特点：

- 只处理 `project.yaml` 中指定的 source database 和 source schemas。
- 根据 inventory 中的 `row_count` 和 `--chunk-size-rows` 估算 export chunk 数。
- 为每个 export chunk 生成对应 import job。
- 只生成当前内置 executor 支持的计划：`--export-format csv`、`--import-engine sql-insert`，以及 `file://`、`http://`、`https://` URI 前缀。
- 单 schema project 默认保留原表名；多 schema project 会用 `<schema>_<table>` 作为目标表名。
- 单 chunk 表会生成 `1 = 1` predicate；需要拆成多个 chunk 的表仍会先写成 `TODO` split predicate，必须由 DBA 或 operator 根据主键、唯一键、时间列或分桶策略 review；approval 后 `worker-export` 和 `worker-executor --stage export` 会拒绝仍包含 `TODO` 的 predicate。
- 不连接 SQL Server，不连接 TiDB，不读取业务数据，不写对象存储，也不执行 `IMPORT INTO`。

示例 `plan/export-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
format: csv
object_uri_prefix: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full
chunk_size_rows: 1000000
tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    row_count_estimate: 2500000
    chunks:
      - id: dbo.orders.000001
        estimated_rows: 1000000
        predicate: "TODO: choose stable split predicate for sales.dbo.orders chunk 1"
        output_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv
```

示例 `plan/import-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
engine: sql-insert
mode: append
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv
    depends_on_export_chunk: dbo.orders.000001
```

当前 MVP 也支持从 inventory 和 project metadata 生成项目级 CDC 计划草稿：

```bash
bin/sqlserver2tidb generate-cdc-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --mode sqlserver-cdc \
  --retention-hours 168 \
  --apply-batch-size 1000
```

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/plan/cdc-plan.yaml`

当前 `generate-cdc-plan` 只生成追踪表清单和 checkpoint 策略，不启用 SQL Server CDC，不创建 Debezium connector，不读取 LSN，不写 Kafka，也不向 TiDB 回放增量。

示例 `plan/cdc-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
mode: sqlserver-cdc
retention_hours_required: 168
source_database: sales
source_schemas:
  - dbo
target_database: app
checkpoint_scope: source-cluster
checkpoint_file: ../../../state/cdc-checkpoint.yaml
tracked_tables:
  - source_object: sales.dbo.orders
    target_object: app.orders
    apply_batch_size: 1000
    status: draft
```

当前 MVP 也支持从 inventory 和 project metadata 生成项目级 row-count validation plan 草稿：

```bash
bin/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/plan/validation-plan.yaml`

当前 `generate-validation-plan` 会为 project 范围内每张表生成一个 `row_count` 检查项。它不连接 SQL Server 或 TiDB，不执行行数校验，只把源/目标对象映射写成可 review 的 GitHub 文件。

示例 `plan/validation-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
checks:
  - id: dbo.orders.row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
```

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

当前 MVP 的 `worker-export` 只在 export approval 和 payload hash 匹配、且 `plan/export-plan.yaml` 的 status 已经是 `reviewed` 或 `approved` 后，把 `plan/export-plan.yaml` 写成 planned 状态和 evidence。它会拒绝 draft export plan，也会拒绝仍包含 `TODO` predicate 的 export chunk。`worker-import` 同样要求 `plan/import-plan.yaml` 的 status 已经是 `reviewed` 或 `approved`。它们不执行导出/导入、不连接 SQL Server 或 TiDB、不写对象存储。

先计算 export payload hash：

```bash
bin/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

把 hash 写入：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/export-approval.yaml
```

并设置 `action: export`、`status: approved` 和非空 `approved_by` 后运行：

```bash
bin/sqlserver2tidb worker-export \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

当前 `worker-export` 写回：

- `state/export-chunks.yaml`
- `evidence/precheck.json`

预览真实导出执行器命令：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --source-connection-string-env SQLSERVER_READONLY_DSN
```

默认只打印 `sqlserver2tidb-executor export ...` 命令。只有显式加 `--execute`，才会调用外部执行器 binary，并向被调用的 executor 子命令传入 `--execute`。

示例：

```yaml
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
phase: export
status: planned
payload_hash: sha256:...
chunks:
  - id: dbo.orders.000001
    status: planned
    source_object: sales.dbo.orders
    target_object: app.orders
    output_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv
    estimated_rows: 1000000
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

当前 MVP 的 `worker-import` 只在 import approval 和 payload hash 匹配后，把 `plan/import-plan.yaml` 写成 planned 状态和 evidence。它会拒绝没有 jobs 或缺少必需字段的 import plan。它不执行 TiDB Lightning 或 `IMPORT INTO`，也不连接 TiDB。

先计算 import payload hash：

```bash
bin/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage import
```

把 hash 写入：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/import-approval.yaml
```

并设置 `action: import`、`status: approved` 和非空 `approved_by` 后运行：

```bash
bin/sqlserver2tidb worker-import \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

当前 `worker-import` 写回：

- `state/import-jobs.yaml`
- `evidence/import-summary.json`

预览真实导入执行器命令：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage import
```

默认只打印 `sqlserver2tidb-executor import ...` 命令。只有显式加 `--execute`，才会调用外部执行器 binary，并向被调用的 executor 子命令传入 `--execute`。

### 10.8 阶段 7：CDC 增量回放

CDC checkpoint 是源集群级：

```text
clusters/<source_cluster_id>/state/cdc-checkpoint.yaml
```

`validate-repo` 会校验 checkpoint 归属的 `source_cluster_id`，并要求初始化状态里的 `capture_mode` 或 worker 写回状态里的 `mode` 与 `cluster.yaml` 的 `cdc.mode` 一致。checkpoint 顶层 `phase` 可在初始化时省略，出现时必须是 `cdc`；`status` 只接受 `not_started`、`planned`、`running`、`caught_up` 或 `failed`；`updated_at` 必须非空并使用 RFC3339。

原因：

- CDC 是 SQL Server 源集群能力。
- 多个 project 可能共享同一个源端 CDC 配置。
- LSN/retention 风险属于源集群级风险。

终极形态支持：

- SQL Server CDC direct reader。
- Debezium SQL Server Connector。
- Kafka-based replay。

LLM 不参与 LSN 判断，也不参与 offset 决策。

当前 MVP 的 `worker-cdc` 只在 `plan/cdc-plan.yaml` 已经是 `reviewed` 或 `approved`、cdc approval 通过且 payload hash 匹配后，把 `plan/cdc-plan.yaml` 写成 planned 状态和 evidence。它会拒绝 draft CDC plan、没有 tracked tables 或缺少必需字段的 CDC plan。它不启用 SQL Server CDC，不启动 Debezium，不读取 LSN，不判断 catch-up，也不向 TiDB 回放增量。

先计算 cdc payload hash：

```bash
bin/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage cdc
```

把 hash 写入：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/cdc-approval.yaml
```

并确认 `plan/cdc-plan.yaml` 已经由 PR review 改成 `status: reviewed` 或 `status: approved`，再设置 `action: cdc`、`status: approved` 和非空 `approved_by` 后运行：

```bash
bin/sqlserver2tidb worker-cdc \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

当前 `worker-cdc` 写回：

- 项目级 `state/migration-state.yaml`
- 源集群级 `state/cdc-checkpoint.yaml`
- 项目级 `evidence/cdc-catchup.json`

预览真实 CDC 执行器命令：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage cdc
```

默认只打印 `sqlserver2tidb-executor cdc ...` 命令。只有显式加 `--execute`，才会调用外部执行器 binary，并向被调用的 executor 子命令传入 `--execute`。

示例项目状态：

```yaml
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
phase: cdc
status: planned
payload_hash: sha256:...
cdc_plan: plan/cdc-plan.yaml
tracked_tables: 1
```

示例源集群 checkpoint：

```yaml
source_cluster_id: prod-sqlserver-a
phase: cdc
status: planned
project_id: sales-db-to-tidb-prod-a
payload_hash: sha256:...
mode: sqlserver-cdc
checkpoint_scope: source-cluster
checkpoints: []
```

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

当前 MVP 已支持 metadata-only validation worker。它不会连接 SQL Server 或 TiDB，只校验当前仓库里的迁移元数据是否满足进入后续执行的最低条件，包括 schema diff 可解析、DDL 已生成、manual review 已清空、conversion report 存在、validation plan 存在，row-count 检查项的 `id`、`source_object`、`target_object` 已填写，并且 predicate / target predicate 不再包含 `TODO`。真实数据校验目前提供行数校验路径：先用 `generate-validation-plan` 生成 `plan/validation-plan.yaml`，再通过 PR review 补充或确认检查项，并把 validation plan 标成 `reviewed` 或 `approved`。validation approval/hash 通过后，`worker-executor --stage validation` 会为 `type: row_count` 或 `type: row-count` 的检查项生成 `sqlserver2tidb-executor validate-count` 命令。

先生成 validation plan 草稿：

```bash
bin/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令读取当前 inventory 和 project metadata，为 project 范围内每张表写入一个 row-count 检查项。它只写 GitHub metadata 文件，不连接数据库。

先计算 validation payload hash：

```bash
bin/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage validation
```

把输出的 `payload hash` 写入：

```text
clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/approvals/validation-approval.yaml
```

并设置：

```yaml
action: validation
status: approved
approved_by:
  - dba-team
payload_hash: sha256:...
```

然后运行 validation worker：

```bash
bin/sqlserver2tidb worker-validate \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

approval gate：

- `action` 必须是 `validation`。
- `status` 必须是 `approved`。
- `approved_by` 不能为空。
- `payload_hash` 必须匹配当前 payload。

payload hash 覆盖：

- `project.yaml`
- `schema/conversion-report.md`
- `schema/schema-diff.json`
- `schema/tidb-ddl/`
- `plan/validation-plan.yaml`

当前 worker 检查：

- `schema-diff.json` 可解析。
- `schema/tidb-ddl/` 下存在 SQL 文件。
- schema diff 中 `manual_review_items` 为 0。
- `schema/conversion-report.md` 存在。
- `plan/validation-plan.yaml` 存在。

如果 approval 未通过或 hash 不匹配，worker 直接退出，不写 `state/` 或 `evidence/`。如果 approval 通过，worker 写回 `state/validation-status.yaml` 和 `evidence/validation-report.md`。如果检查失败，worker 会写入 failed 状态，CLI 返回非零退出码。

行数校验计划示例：

```yaml
status: draft
checks:
  - id: orders-row-count
    type: row_count
    source_object: sales.dbo.orders
    target_object: app.orders
    predicate: "id >= 1"
    target_predicate: "id >= 1"
```

approval 通过后生成 validation executor 命令：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage validation \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

对单个对象执行行数校验 dry-run：

```bash
bin/sqlserver2tidb-executor validate-count \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --predicate "id >= 1" \
  --target-predicate "id >= 1"
```

显式执行行数校验：

```bash
export SQLSERVER2TIDB_SOURCE_CONNECTION_STRING='server=sqlserver-a.internal;user id=readonly;password=REDACTED;database=sales;encrypt=true'
export SQLSERVER2TIDB_TARGET_CONNECTION_STRING='user:password@tcp(tidb.example.internal:4000)/app?charset=utf8mb4&parseTime=true'

bin/sqlserver2tidb-executor validate-count \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --predicate "id >= 1" \
  --target-predicate "id >= 1"
```

执行模式会对源端和目标端分别运行 count 查询。以上面的对象为例：

```sql
SELECT COUNT(*) FROM [sales].[dbo].[orders] WHERE id >= 1;
SELECT COUNT(*) FROM `app`.`orders` WHERE id >= 1;
```

如果行数不同，命令返回非零退出码并输出 mismatch。当前实现不做 checksum、sampled hash、业务 SQL 或分桶聚合校验。

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

worker lease 是上游 SQL Server 集群级，避免多个 worker 同时操作同一个源集群。`phase: idle` 表示空闲占位；当 phase 是 `export`、`import`、`cdc` 或 `validation` 时，lease 必须带有非空 `holder`、`lease_id`、`project_id`、`expires_at` 和 `renewed_at`，`project_id` 必须引用同一源集群下已存在的项目目录，两个时间字段必须是 RFC3339，且 `expires_at` 不能早于 `renewed_at`，否则 `validate-repo` 会拒绝该仓库状态。

当前 MVP 实现了 `worker-export`、`worker-import`、`worker-cdc` 和 `worker-validate`。它们不是完整 reconcile loop，也不会抢占 worker lease。它们只针对一个显式指定的 project 执行 approval gate、payload hash 校验和 metadata-only 状态写回，并要求对应 plan 已经是 `reviewed` 或 `approved`。

当前 MVP 也实现了只读扫描：

```bash
bin/sqlserver2tidb worker-reconcile --root . --dry-run
```

该命令扫描所有 `clusters/<source_cluster_id>/projects/<project_id>/`，对 `ddl`、`export`、`import`、`cdc` 和 `validation` 分别执行 approval/hash gate，输出：

- `ready`：approval 已通过、`approved_by` 非空且 payload hash 匹配。
- `blocked`：approval 未通过、hash 不匹配或 payload 文件缺失，并给出原因。
- ready metadata action 对应的单项目 worker 命令。
- ready DDL executor action 对应的 `worker-executor --stage ddl` 命令。

`worker-reconcile --dry-run` 不执行 worker，不获取 lease，不写 state/evidence，不创建分支或 PR。它是完整 reconcile loop 的只读预演。

当前 MVP 还支持单步执行：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --execute-next \
  --holder agent-a \
  --lease-ttl 15m \
  --state-pr-draft
```

该命令按 source cluster / project / stage 顺序选择第一个 ready metadata-only action，也就是 `export`、`import`、`cdc` 或 `validation`，先写入源集群级 `state/worker-lease.yaml`，再调用对应 metadata-only worker。`ddl` 是 executor-only action，会在 dry-run 中展示，但不会被 `--execute-next` 自动选择。当前同一 `--holder` 可以续租自己的未过期 lease；不同 holder 在 lease 过期前会被拒绝。加上 `--state-pr-draft` 后，它会在项目 `prs/` 目录下写入 `reconcile-<stage>-state-pr.md`，用于审阅本次 state/evidence/lease 写回。它仍然不连接 SQL Server/TiDB/Kafka/对象存储，也不创建 GitHub branch 或 PR。

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
- 文件 schema 是否通过，包括 migration/export/import/CDC/validation plan 的 policy 映射。
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

当前 MVP 可以只读连接 SQL Server catalog 生成 inventory，可以从 inventory 生成 TiDB DDL 草稿、全量导出/导入计划草稿、CDC 计划草稿和 row-count validation plan 草稿，并执行 metadata-only export/import/CDC/validation worker。`worker-executor` 可以在 approval/hash gate 后生成外部执行器命令；当前 export 只接受 `format: csv`，import 只接受 `engine: sql-insert`，避免把 Parquet、Lightning 或 `IMPORT INTO` 这类未来路径误交给内置 executor。`sqlserver2tidb-executor` 当前已经可以解析这些 work item 并 dry-run 输出上下文。`apply-ddl --execute` 支持把已 review 且不含 `TODO` 的 DDL 文件执行到 TiDB。`export --execute` 支持 SQL Server 到本地 `file://` CSV 或 HTTP(S) CSV 的最小真实导出路径，并通过内部 null bitmap 尾列保留 SQL NULL，但还不支持原生 `s3://` 客户端或 Parquet。`import --execute` 支持本地 `file://` CSV 或 HTTP(S) CSV 到 TiDB 的流式逐行 insert 路径，会识别并排除内部 null bitmap 尾列，并用 `--import-batch-size` 分批提交事务，但还不支持 Lightning 或 `IMPORT INTO`。`worker-executor --stage validation` 可以在 validation approval/hash gate 后生成 `validate-count` 命令，`validate-count --execute` 支持单对象 SQL Server/TiDB 行数对比。`cdc --execute` 仍显式返回 not implemented，不会回放 CDC。checksum、sampled hash 和业务 SQL 数据校验仍是后续能力。

### 15.5 可以把 LLM 接进来吗？

可以，但 LLM 只能生成草案和解释。所有输出必须经过 GitHub PR review 后才能成为 worker 可执行 instruction。

## 16. 命令参考

### 16.1 init-repo

```bash
bin/sqlserver2tidb init-repo --root .
```

### 16.2 validate-repo

```bash
bin/sqlserver2tidb validate-repo --root .
```

该命令检查必需目录、必需文件、`global/policies/file-schema-policy.yaml` 中的 plan schema 映射，以及已填写的 export/import/CDC/validation plan work item 的关键字段、唯一性、import/export 依赖关系、import source URI 与 export output URI 的一致性和未清理的 `TODO` predicate。它不会连接 SQL Server 或 TiDB，也不会执行数据迁移。

### 16.3 create-cluster

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

### 16.4 create-project

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

### 16.5 discover-sqlserver

dry-run：

```bash
bin/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --dry-run
```

该命令不会连接 SQL Server，也不会写文件。它用于提前 review discovery 将覆盖的 catalog 范围和目标 inventory 文件。

真实 catalog discovery：

```bash
bin/sqlserver2tidb discover-sqlserver \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
```

该命令会连接 SQL Server，读取当前数据库的 catalog metadata，并写回 `inventory/inventory.json`。连接串只从环境变量读取，不会写入 repo。

### 16.6 analyze-compatibility

```bash
bin/sqlserver2tidb analyze-compatibility \
  --root . \
  --source-cluster-id prod-sqlserver-a
```

该命令读取 `inventory/inventory.json`，写回 `inventory/schema-issues.yaml` 和 `inventory/compatibility-report.md`。

### 16.7 generate-schema-draft

```bash
bin/sqlserver2tidb generate-schema-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `schema/tidb-ddl/`、`schema/conversion-report.md` 和 `schema/schema-diff.json`。它只生成草稿，不连接 TiDB，不执行 DDL。

### 16.8 generate-data-plans

```bash
bin/sqlserver2tidb generate-data-plans \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full \
  --chunk-size-rows 1000000 \
  --export-format csv \
  --import-engine sql-insert
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/export-plan.yaml` 和 `plan/import-plan.yaml`。它只生成当前内置 executor 支持的 CSV/sql-insert 草稿，`--object-uri-prefix` 必须使用 `file://`、`http://` 或 `https://` 前缀；不连接 SQL Server 或 TiDB，不执行导出或导入。`--chunk-size-rows` 默认是 `1000000`，`--export-format` 默认是 `csv`，`--import-engine` 默认是 `sql-insert`。

### 16.9 generate-cdc-plan

```bash
bin/sqlserver2tidb generate-cdc-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --mode sqlserver-cdc \
  --retention-hours 168 \
  --apply-batch-size 1000
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/cdc-plan.yaml`。它只生成草稿，不启用 SQL Server CDC，不启动 Debezium，不读取 LSN，不连接 TiDB，也不执行增量回放。`--mode` 默认是 `sqlserver-cdc`，`--retention-hours` 默认是 `168`，`--apply-batch-size` 默认是 `1000`。

### 16.9.1 generate-validation-plan

```bash
bin/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/validation-plan.yaml`。它为 project 范围内每张表生成一个 `row_count` 检查项，只生成草稿，不连接 SQL Server 或 TiDB，也不执行校验。

### 16.10 generate-pr-draft

项目级 stage：

```bash
bin/sqlserver2tidb generate-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

集群级 stage：

```bash
bin/sqlserver2tidb generate-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --stage discovery
```

该命令生成本地 PR body draft，并在 stdout 输出 PR 标题、建议分支名、body file 路径和 review 文件数量。它不会调用 GitHub API。

### 16.11 create-pr

dry-run：

```bash
bin/sqlserver2tidb create-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema
```

真实调用 `gh pr create`：

```bash
bin/sqlserver2tidb create-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage schema \
  --execute
```

该命令要求对应 PR draft 已存在。默认 dry-run，只打印将执行的 `gh pr create`。只有加 `--execute` 才会调用本地 GitHub CLI。

### 16.12 compute-payload-hash

```bash
bin/sqlserver2tidb compute-payload-hash \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

该命令计算指定 stage 的 payload hash。当前支持 `ddl`、`export`、`import`、`cdc` 和 `validation` stage。这个 hash 应写入对应 approval 文件，防止审批后 payload 文件被修改。

### 16.13 worker-export

```bash
bin/sqlserver2tidb worker-export \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令先检查 `approvals/export-approval.yaml`，只有 approval 通过且 payload hash 匹配时才读取 `plan/export-plan.yaml`，并写回 `state/export-chunks.yaml` 和 `evidence/precheck.json`。它只写 planned 状态，不执行真实导出。

### 16.14 worker-import

```bash
bin/sqlserver2tidb worker-import \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令先检查 `approvals/import-approval.yaml`，只有 approval 通过且 payload hash 匹配时才读取 `plan/import-plan.yaml`，并写回 `state/import-jobs.yaml` 和 `evidence/import-summary.json`。它只写 planned 状态，不执行真实导入。

### 16.15 worker-cdc

```bash
bin/sqlserver2tidb worker-cdc \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令先检查 `approvals/cdc-approval.yaml`，只有 approval 通过、payload hash 匹配，且 `plan/cdc-plan.yaml` 已经是 `reviewed` 或 `approved` 时才写回项目级 `state/migration-state.yaml`、源集群级 `state/cdc-checkpoint.yaml` 和项目级 `evidence/cdc-catchup.json`。它只写 planned 状态，不启用或执行真实 CDC。

### 16.16 worker-validate

```bash
bin/sqlserver2tidb worker-validate \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令先检查 `approvals/validation-approval.yaml`，只有 approval 通过、payload hash 匹配，且 `plan/validation-plan.yaml` 已经是 `reviewed` 或 `approved` 时才执行 validation checks。执行后写回 `state/validation-status.yaml` 和 `evidence/validation-report.md`。

### 16.17 worker-executor

dry-run：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

export dry-run：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

真实调用外部执行器：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --source-connection-string-env SQLSERVER_READONLY_DSN \
  --execute
```

validation stage 示例：

```bash
bin/sqlserver2tidb worker-executor \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage validation \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

该命令支持 `ddl`、`export`、`import`、`cdc` 和 `validation`。它复用对应 stage 的 approval/hash gate，只有 approval 通过、payload hash 匹配，且 DDL 的 `schema/schema-diff.json` 已经是 `reviewed`，或 export/import/CDC/validation plan 已经是 `reviewed` 或 `approved` 时才生成执行器命令。默认外部 binary 是 `sqlserver2tidb-executor`，可以通过 `--executor-binary` 覆盖。`--source-connection-string-env`、`--target-connection-string-env` 和 `--import-batch-size` 会被渲染进生成的 executor 命令，不写入 GitHub metadata。默认 dry-run 只打印命令；只有加 `--execute` 才会调用外部 binary，并在 executor 子命令后自动注入 `--execute`，让随仓库提供的 executor 离开 dry-run 模式。执行模式会写回 `evidence/executor-<stage>-run.json`，记录 payload hash、每条命令、输出、exit code、每条命令的开始/结束时间和耗时；如果某条命令失败，会先写 failed evidence，再返回非零退出码。`ddl` stage 会为 `schema/tidb-ddl/*.sql` 生成 `apply-ddl` 命令；审批后的 export/import/CDC plan 如果没有任何 work item，会直接失败；export chunk predicate 如果仍包含 `TODO` 也会失败；export plan 只有 `format: csv` 才会生成当前 executor 命令，import plan 只有 `engine: sql-insert` 才会生成当前 executor 命令；`validation` stage 会为 `row_count` 检查生成 `validate-count` 命令，如果审批后的 validation plan 没有任何支持的 row-count 检查，也会直接失败。当前随仓库提供的 `sqlserver2tidb-executor apply-ddl --execute` 可以把已 review DDL 执行到 TiDB；`export --execute` 支持 SQL Server 到本地 `file://` CSV 或 HTTP(S) CSV，并写入内部 null bitmap 尾列；`import --execute` 支持本地 `file://` CSV 或 HTTP(S) CSV 到 TiDB 的流式逐行 insert，并识别该尾列恢复 NULL；`cdc --execute` 仍返回 not implemented。

### 16.18 sqlserver2tidb-executor

DDL dry-run：

```bash
bin/sqlserver2tidb-executor apply-ddl \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
```

DDL 执行：

```bash
bin/sqlserver2tidb-executor apply-ddl \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

执行模式会读取 DDL 文件，拒绝仍包含 `TODO` 的文件，并用目标连接串执行其中的 SQL 语句。

导出 dry-run：

```bash
bin/sqlserver2tidb-executor export \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --chunk-id dbo.orders.000001 \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --output-uri https://object-store.example/migration/prod/full/dbo.orders.000001.csv
```

导出执行到本地 CSV：

```bash
export SQLSERVER2TIDB_SOURCE_CONNECTION_STRING='server=sqlserver-a.internal;user id=readonly;password=REDACTED;database=sales;encrypt=true'

bin/sqlserver2tidb-executor export \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --chunk-id dbo.orders.000001 \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --output-uri file:///tmp/sqlserver2tidb/dbo.orders.000001.csv \
  --predicate "id >= 1 AND id < 1000"
```

也可以用 `--source-connection-string-env <ENV_NAME>` 指定其他环境变量。执行模式会拒绝仍包含 `TODO` 的 predicate，并且当前只接受 `file://`、`http://` 和 `https://` 输出 URI。

导入 dry-run：

```bash
bin/sqlserver2tidb-executor import \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --job-id import-dbo.orders.000001 \
  --target-object app.orders \
  --source-uri https://object-store.example/migration/prod/full/dbo.orders.000001.csv
```

导入执行本地 CSV 到 TiDB：

```bash
export SQLSERVER2TIDB_TARGET_CONNECTION_STRING='user:password@tcp(tidb.example.internal:4000)/app?charset=utf8mb4&parseTime=true'

bin/sqlserver2tidb-executor import \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --job-id import-dbo.orders.000001 \
  --target-object app.orders \
  --source-uri file:///tmp/sqlserver2tidb/dbo.orders.000001.csv \
  --import-batch-size 1000
```

也可以用 `--target-connection-string-env <ENV_NAME>` 指定其他环境变量。当前实现读取 CSV header 作为目标列名；如果 header 最后一列是内部 `__sqlserver2tidb_null_bitmap`，import 会把该列从目标列中排除，并根据 bitmap 把对应字段恢复为 NULL。随后它流式读取 CSV 行，并按 `--import-batch-size` 分批事务提交 `INSERT`。它不调用 TiDB Lightning 或 `IMPORT INTO`。

行数校验 dry-run：

```bash
bin/sqlserver2tidb-executor validate-count \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --predicate "id >= 1" \
  --target-predicate "id >= 1"
```

执行行数校验：

```bash
export SQLSERVER2TIDB_SOURCE_CONNECTION_STRING='server=sqlserver-a.internal;user id=readonly;password=REDACTED;database=sales;encrypt=true'
export SQLSERVER2TIDB_TARGET_CONNECTION_STRING='user:password@tcp(tidb.example.internal:4000)/app?charset=utf8mb4&parseTime=true'

bin/sqlserver2tidb-executor validate-count \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --predicate "id >= 1" \
  --target-predicate "id >= 1"
```

也可以分别用 `--source-connection-string-env <ENV_NAME>` 和 `--target-connection-string-env <ENV_NAME>` 指定其他环境变量。执行模式会拒绝仍包含 `TODO` 的 source predicate 或 target predicate，并在源端和目标端的 `COUNT(*)` 不一致时返回非零退出码。

CDC dry-run：

```bash
bin/sqlserver2tidb-executor cdc \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --apply-batch-size 1000
```

当前 binary 默认只做参数解析和 dry-run 输出。`export --execute` 会连接 SQL Server，并把 CSV 写到本地 `file://` 或 HTTP(S) URL；它会在 header 尾部增加内部 `__sqlserver2tidb_null_bitmap` 列，用来保留每行 NULL 位置。它不会使用原生 S3/GCS/Azure Blob SDK，也不会生成 Parquet。`import --execute` 会连接 TiDB，并从本地 `file://` 或 HTTP(S) CSV 流式逐行插入，按 batch size 分批提交；如果 CSV 带内部 null bitmap 尾列，会恢复 NULL 并不把该内部列写入目标表。它不会调用 Lightning 或 `IMPORT INTO`。`validate-count --execute` 会连接 SQL Server 和 TiDB，比较单对象 `COUNT(*)`。`cdc --execute` 会返回 not implemented，用来防止误执行尚未实现的 CDC 路径。

### 16.19 worker-reconcile

只读扫描：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --dry-run
```

单步执行：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --execute-next \
  --holder agent-a \
  --lease-ttl 15m \
  --state-pr-draft
```

`--dry-run` 扫描所有 source cluster 和 project，对 `ddl`、`export`、`import`、`cdc`、`validation` 计算 ready/blocked 状态，并为 ready action 输出对应命令。`ddl` 的命令是 `worker-executor --stage ddl`；其他 metadata-only stage 会输出对应的单项目 worker 命令。它不执行 worker，不获取 worker lease，不写 state/evidence，也不创建 GitHub PR。

`--execute-next` 只会选择第一个 ready metadata-only action，也就是 `export`、`import`、`cdc` 或 `validation`，获取或续租 source-cluster 级 worker lease，然后执行对应 metadata-only worker。`ddl` 是 executor-only action，需要显式通过 `worker-executor --stage ddl` 执行。`--holder` 必填，`--lease-ttl` 默认是 `15m`。该模式会写 `state/worker-lease.yaml` 和被选中 worker 对应的 state/evidence 文件；active lease 会包含 `holder`、`lease_id`、`phase`、`project_id`、`expires_at` 和 `renewed_at`，时间字段使用 RFC3339。

`--state-pr-draft` 在 `--execute-next` 模式下生效。启用后，命令会额外生成项目级 PR body：

```text
clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md
```

该 PR body 会列出 payload hash、lease id、建议 branch、项目 state/evidence 文件、源集群 `state/worker-lease.yaml`；如果 stage 是 `cdc`，还会列出源集群 `state/cdc-checkpoint.yaml`。它只是 PR 草稿，不会创建 branch、commit、push 或 GitHub PR。

### 16.20 create-worker-state-pr

dry-run：

```bash
bin/sqlserver2tidb create-worker-state-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export
```

真实创建本地 branch、commit、push 并调用 `gh pr create`：

```bash
bin/sqlserver2tidb create-worker-state-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage export \
  --execute
```

该命令要求 `worker-reconcile --execute-next --state-pr-draft` 已经生成对应的 `reconcile-<stage>-state-pr.md`，并要求被提交的 state/evidence/lease 文件已经存在。如果已存在 `evidence/executor-<stage>-run.json`，命令会把该执行证据一并加入 `git add` 文件列表。默认 dry-run 只打印 `git switch`、`git add`、`git commit`、`git push` 和 `gh pr create` 命令，并提示 PR body 文件列表是否需要刷新；dry-run 不会修改 PR body。只有加 `--execute` 才会在提交前刷新 PR body、修改本地 git checkout、推送分支并调用 GitHub CLI。它不会 merge PR、approve PR、绕过 branch protection，也不会判断 worker 结果是否业务正确。

### 16.21 generate-executor-evidence-pr-draft / create-executor-evidence-pr

为 executor-only evidence 生成 PR body：

```bash
bin/sqlserver2tidb generate-executor-evidence-pr-draft \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl
```

dry-run 准备 git/gh 命令：

```bash
bin/sqlserver2tidb create-executor-evidence-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl
```

真实创建本地 branch、commit、push 并调用 `gh pr create`：

```bash
bin/sqlserver2tidb create-executor-evidence-pr \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage ddl \
  --execute
```

`generate-executor-evidence-pr-draft` 要求 `evidence/executor-<stage>-run.json` 已存在，并要求对应 stage approval 已通过、payload hash 与当前 metadata 匹配，且 DDL 的 `schema/schema-diff.json` 或对应 stage plan 仍处于已 review 状态。它会校验 evidence 内的 `source_cluster_id`、`project_id`、`stage` 和 `payload_hash`；evidence status 只接受 `succeeded` 或 `failed`，command id 不能重复。它会拒绝没有 executor command 记录的 evidence；每条 command 记录必须包含 `id`、非空 `args`、`shell_command`、`exit_code`、`started_at`、`completed_at` 和 `duration_ms`。时间字段必须是 RFC3339Nano，`completed_at` 不能早于 `started_at`，`duration_ms` 必须非负。当 evidence status 是 `succeeded` 时，所有 command 的 `exit_code` 都必须是 `0`；当 status 是 `failed` 时，至少一条 command 的 `exit_code` 必须非 0。校验通过后，它会写入 `prs/executor-<stage>-evidence-pr.md`，并在 PR body 里渲染命令 ID、exit code、时间戳和耗时摘要表。`create-executor-evidence-pr` 默认 dry-run，只打印 `git switch`、`git add`、`git commit`、`git push` 和 `gh pr create` 命令；只有加 `--execute` 才会修改本地 git checkout、推送分支并调用 GitHub CLI。它不会 merge PR、approve PR、绕过 branch protection，也不会判断 executor 输出是否业务正确。

## 17. 推荐落地顺序

1. 使用当前 CLI 初始化 repo。
2. 执行 `validate-repo` 确认 metadata 结构完整。
3. 为一个测试 SQL Server 集群创建 cluster。
4. 对测试 SQL Server 集群执行 `discover-sqlserver --dry-run`，review discovery 范围。
5. 通过安全环境变量执行真实 `discover-sqlserver`，生成 inventory。
6. 为一个小 database 创建 project。
7. 通过 PR review metadata 文件。
8. 对已有 inventory 执行 `analyze-compatibility`，生成规则化兼容性报告。
9. 执行 `generate-schema-draft`，生成 DDL 草稿、转换报告和 schema diff，并通过 Schema PR review。
10. 执行 `generate-data-plans`，生成全量导出/导入计划草稿。
11. 执行 `generate-cdc-plan` 和 `generate-validation-plan`，生成 CDC 与 validation plan 草稿，并通过 Plan/Export/Import/CDC/Validation PR review。
12. 对 discovery/schema/plan/export/import/cdc 等阶段执行 `generate-pr-draft`，生成 PR body 草稿和建议命令。
13. 执行 `create-pr` dry-run 检查命令，再用 `create-pr --execute` 创建 GitHub PR。
14. 将 `schema/schema-diff.json` 通过 PR review 标成 `reviewed`，执行 `compute-payload-hash --stage ddl`，把 hash 写入 ddl approval，approval 通过后用 `worker-executor --stage ddl` dry-run 检查 DDL 执行命令。
15. 执行 `compute-payload-hash --stage export`，把 hash 写入 export approval，approval 通过后运行 `worker-export` 写 planned export state/evidence。
16. 执行 `compute-payload-hash --stage import`，把 hash 写入 import approval，approval 通过后运行 `worker-import` 写 planned import state/evidence。
17. 将 `plan/cdc-plan.yaml` 通过 PR review 标成 `reviewed` 或 `approved`，执行 `compute-payload-hash --stage cdc`，把 hash 写入 cdc approval，approval 通过后运行 `worker-cdc` 写 planned CDC state/evidence。
18. 将 `plan/validation-plan.yaml` 通过 PR review 标成 `reviewed` 或 `approved`，执行 `compute-payload-hash --stage validation`，把 hash 写入 validation approval。
19. 执行 `worker-reconcile --dry-run`，确认 ready/blocked action 与预期一致。
20. 执行 `worker-reconcile --execute-next --holder <agent-id> --state-pr-draft`，用 lease-backed 单步 reconcile 执行第一个 ready metadata-only action，并生成 state PR body。
21. 执行 `create-worker-state-pr` dry-run 检查 git/gh 命令，再按需执行 `create-worker-state-pr --execute` 创建 state/evidence 写回 PR。
22. 对 ddl/export/import/cdc/validation 执行 `worker-executor` dry-run，检查外部执行器命令和当前 plan 是否一致。
23. 按需执行 `worker-executor --execute`，生成 `evidence/executor-<stage>-run.json`。
24. 对 executor-only evidence 执行 `generate-executor-evidence-pr-draft` 和 `create-executor-evidence-pr` dry-run，再按需执行 `create-executor-evidence-pr --execute` 创建 evidence PR。
25. approval 通过后执行 `worker-validate`，生成 validation state 和 evidence。
26. 将 `sqlserver2tidb-executor export/import` 从本地 CSV 扩展到生产对象存储格式和导入引擎，扩展 `validate-count` 到 checksum / sampled hash / 业务 SQL 校验，并实现/审查真实 CDC 行为。
27. 最后接入 cutover orchestration。
