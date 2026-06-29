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
- 本地 `doctor` 预检：校验 metadata repo，并检查 `git`、`gh`、`sqlserver2tidb-executor` 是否在 `PATH` 上可用。
- 校验 GitOps metadata 目录结构。
- 生成 SQL Server discovery dry-run 计划。
- 通过环境变量连接串执行只读 SQL Server catalog discovery。
- 基于 `inventory/inventory.json` 生成规则化兼容性分析报告。
- 基于 SQL Server inventory 和 project metadata 生成项目级 TiDB DDL 草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级全量导出/导入计划草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级 CDC 计划草稿。
- 基于 SQL Server inventory 和 project metadata 生成项目级 validation plan 草稿，默认包含 row-count，也可显式生成 exact-numeric checksum、sampled-hash 和 bucketed-count scalar-query 草稿。
- 基于 stage 生成本地 PR draft 文件，并通过 dry-run-by-default wrapper 准备 `gh pr create`。
- 计算 ddl、export、import、cdc、validation、cutover payload hash。
- 在 approval 通过后执行 metadata-only export/import/CDC/validation/cutover worker，把 plan、evidence 或 cutover gate 写成状态文件。
- 在 approval 通过后执行 `worker-executor` dry-run，生成外部 DDL/export/import/CDC/validation 执行器命令。
- 通过 `worker-executor --stage ddl` 或 `sqlserver2tidb-executor apply-ddl` 对 TiDB 执行已 review 的 DDL，通过 `worker-executor --stage validation` 或 `sqlserver2tidb-executor validate-count` 对源/目标对象执行显式行数校验。
- 在 approval 通过后执行 metadata-only validation worker。
- 执行只读 `worker-reconcile --dry-run`，扫描 ready/blocked worker actions。
- 执行 `worker-reconcile --execute-next`，在源集群 lease 保护下执行第一个 ready metadata-only worker action。
- 执行 `worker-reconcile --loop`，在同一 lease holder 下连续处理 ready metadata-only worker actions，直到无 ready action 或达到最大迭代次数。
- 执行 `worker-agent`，把同一 bounded reconcile loop 作为本地进程或容器 worker 的稳定入口运行。
- 可选执行 `worker-reconcile --execute-next --state-pr-draft`，为 state/evidence/lease 写回生成项目级 PR draft。
- 通过 `create-worker-state-pr` dry-run 准备 state/evidence/lease 写回 PR 的 git push 和 GitHub 命令。
- 提供多阶段 Dockerfile，可构建包含 `sqlserver2tidb` 和 `sqlserver2tidb-executor` 的非 root CLI 镜像。
- 通过 `generate-executor-evidence-pr-draft` 生成 executor-only evidence PR body，并通过 `create-executor-evidence-pr` dry-run 准备 git push 和 GitHub 命令，尤其是 DDL apply evidence。
- 创建上游 SQL Server 集群目录。
- 在上游 SQL Server 集群下创建迁移项目目录。
- 生成基础 YAML/JSON/Markdown 状态文件。
- 生成核心 JSON Schema 文件。
- 单元测试和 CLI smoke test。

当前 CLI 在执行 `discover-sqlserver --connection-string-env ...` 时会连接 SQL Server，并且只读取 catalog metadata。`sqlserver2tidb-executor apply-ddl --execute` 可以显式连接 TiDB，执行已 review 且不含 `TODO` 的 DDL 文件。`sqlserver2tidb-executor export --execute` 也可以显式连接 SQL Server，把一个已审批的导出 work item 写成本地 `file://` CSV、HTTP(S) CSV、原生 `s3://` CSV、原生 `gs://` CSV 或原生 `azblob://` CSV；CSV 会带内部 `__sqlserver2tidb_null_bitmap` 尾列，用来保留 SQL NULL。S3 使用 AWS Signature V4；GCS 使用 HMAC 凭证；Azure Blob 使用 Shared Key。`sqlserver2tidb-executor import --execute` 可以显式连接 TiDB；默认 `--engine sql-insert` 会把本地 `file://`、HTTP(S)、S3、GCS 或 Azure Blob CSV 逐行写入目标表，并在识别该内部尾列时恢复 NULL；显式 `--engine tidb-import-into` 会执行 TiDB `IMPORT INTO ... FROM FILE`，支持绝对本地路径、本地 `file://`、`s3://`、`gs://` file location，本地/file/S3/GCS CSV 会读取 header 并把内部尾列映射成 TiDB user variable 以跳过写入。Azure Blob 当前只用于 `sql-insert` CSV 路径，不用于 TiDB `IMPORT INTO`。`sqlserver2tidb-executor validate-count --execute` 可以显式同时连接 SQL Server 和 TiDB，对一个源/目标对象做 `COUNT(*)` 对比；`sqlserver2tidb-executor validate-query --execute` 可以执行已 review 的源端/目标端 SQL 标量查询并比较结果，用于 `checksum`、`sampled_hash`、`bucketed_count` 和 `business_sql` 检查项。`sqlserver2tidb-executor cdc-lsn --execute` 可以显式连接 SQL Server，读取当前 CDC max LSN，并在提供源对象时读取 capture instance min LSN；`sqlserver2tidb-executor cdc --execute` 可以在显式提供 `--from-lsn` / `--to-lsn` 的情况下读取 SQL Server CDC 变更并向 TiDB 执行 upsert/delete。除此之外，它不会执行 TiDB Lightning、自动审批/merge PR、真实应用流量切换、通用 row digest 或 bucketed sampled hash 策略。`worker-cutover` 只在 GitHub 文件里记录 cutover gate 通过和 completed 状态，不修改 DNS、代理、应用配置或数据库连接。`cdc-orchestrator` 默认负责探测 max LSN 并生成下一段 range PR 草稿；显式加 `--apply-approved` 时，才会消费已经通过 approval/hash gate 的当前 CDC range，执行 CDC apply 并基于 evidence 推进 checkpoint。`discover-sqlserver --dry-run` 只输出计划，不打开数据库连接，也不写 inventory 文件。`analyze-compatibility`、`generate-schema-draft`、`generate-data-plans`、`generate-cdc-plan`、`prepare-cdc-range`、`advance-cdc-checkpoint`、`generate-validation-plan`、`generate-pr-draft`、`create-pr` 的默认 dry-run、`create-worker-state-pr` 的默认 dry-run、`create-executor-evidence-pr` 的默认 dry-run、`worker-executor` 的默认 dry-run、`sqlserver2tidb-executor` 的默认 dry-run、`compute-payload-hash`、`worker-export`、`worker-import`、`worker-cdc`、`worker-validate`、`worker-cutover`、`worker-reconcile --dry-run`、`worker-reconcile --execute-next`、`worker-reconcile --loop` 和 `worker-agent` 只读取并写回或汇报 GitHub metadata 文件。`worker-reconcile --state-pr-draft`、`worker-agent --state-pr-draft` 和 `generate-executor-evidence-pr-draft` 只生成 Markdown PR body，不调用 GitHub。`create-pr --execute` 会调用本地 `gh pr create`；`create-worker-state-pr --execute` 和 `create-executor-evidence-pr --execute` 会调用本地 `git` 和 `gh`。

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

当前 MVP 实现了显式指定 project 的 `worker-export`、`worker-import`、`worker-cdc`、`worker-validate` 和 `worker-cutover`。它们都需要 approval 和 payload hash 匹配；`worker-export`、`worker-import`、`worker-cdc` 和 `worker-validate` 还要求对应 plan 已经是 `reviewed` 或 `approved`。`worker-cutover` 要求 cutover runbook 已 review、export/import/validation executor evidence 成功、validation worker 状态 passed，非 offline 项目还要求 CDC executor evidence 成功且源集群 CDC checkpoint 为 `caught_up`。当前还实现了 `worker-executor`，用于在同一 approval/hash gate 后生成外部执行器命令，默认 dry-run。当前还实现了 `worker-reconcile --dry-run`、`worker-reconcile --execute-next`、`worker-reconcile --loop` 和 `worker-agent`；`--execute-next` 会获取源集群级 lease，并执行第一个 ready metadata-only action，`--loop` 会用同一个 holder 连续执行 ready metadata-only actions，直到没有 ready action 或达到最大迭代次数，`worker-agent` 则把同一 loop 包装成本地进程/容器 worker 入口。DDL action 在 `schema/schema-diff.json` 未 `reviewed` 时会被视为 blocked，export、import、CDC 或 validation action 在对应 plan 仍为 `draft` 时也会被视为 blocked，cutover action 在证据或 checkpoint gate 未满足时会被视为 blocked；同一 approved payload hash 如果已经有非空 stage state，也会被视为 blocked，避免 reconcile loop 重复运行同一个动作。加上 `--state-pr-draft` 后，reconcile 单步执行或 worker agent 可以生成 state/evidence/lease 写回的 PR body 草稿。`create-worker-state-pr` 可以默认 dry-run 地准备 bot branch、commit、push 和 GitHub PR 命令；如果存在 `evidence/executor-<stage>-run.json`，会先校验 approval、payload hash、已 review 的执行指令和 executor command 结构，再纳入提交文件列表；dry-run 会提示 PR body 文件列表是否需要刷新，只有显式 `--execute` 时才会刷新 body 并调用本地 `git` 和 `gh`。对于 DDL 这类 executor-only 阶段，`generate-executor-evidence-pr-draft` 可以把 `evidence/executor-ddl-run.json` 转成 PR body，`create-executor-evidence-pr` 可以默认 dry-run 地准备 evidence PR 的 bot branch、commit、push 和 GitHub PR 命令。

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

发布二进制时，推送形如 `v0.1.0` 的 tag 会触发 release workflow，为 Linux、macOS 和 Windows 构建归档、生成 checksums，并创建 GitHub Release。container workflow 也会发布 `ghcr.io/bornchanger/sqlserver2tidb:v0.1.0`，并更新 `ghcr.io/bornchanger/sqlserver2tidb:latest`。

本地构建容器镜像：

```bash
docker build \
  --build-arg VERSION=dev \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t sqlserver2tidb:dev .
```

挂载迁移 metadata 仓库并执行预检：

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  sqlserver2tidb:dev doctor --root /workspace
```

以容器进程方式运行 metadata-only worker agent：

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  sqlserver2tidb:dev worker-agent \
    --root /workspace \
    --source-cluster-id prod-sqlserver-a \
    --holder agent-a \
    --max-iterations 0 \
    --interval 5s \
    --poll \
    --idle-iterations 0 \
    --state-pr-draft
```

仓库和 release archive 都包含 `examples/worker-agent/`，里面有 Docker Compose、`.env.example` 和 systemd unit 模板，可作为本地/容器部署起点。

镜像内包含 `git`、`sqlserver2tidb` 和 `sqlserver2tidb-executor`，默认使用非 root 的 `sqlserver2tidb` 用户运行。镜像不内置 GitHub CLI；如果需要在容器内执行 `create-pr --execute`、`create-worker-state-pr --execute` 或 `create-executor-evidence-pr --execute`，请扩展镜像安装 `gh`，或者在宿主机执行这些 GitHub PR wrapper。

### 5.4 运行测试

```bash
make test
```

运行与 GitHub Actions 对齐的本地检查：

```bash
make ci
```

运行离线 quickstart 样例：

```bash
make example-check
```

该命令会在临时目录生成一个迁移 metadata 仓库，注入 `examples/quickstart/inventory.json`，生成 schema/data/CDC/validation 草稿，执行 `worker-reconcile --dry-run`，并运行 `validate-repo`。它不会连接 SQL Server、TiDB、GitHub 或对象存储。如果需要保留生成结果用于查看，指定一个空目录：

```bash
SQLSERVER2TIDB_QUICKSTART_ROOT=/tmp/sqlserver2tidb-quickstart make example-check
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

也可以执行本地预检：

```bash
bin/sqlserver2tidb doctor --root .
```

`doctor` 会复用 `validate-repo` 的结构校验，并检查本地 `git`、`gh`、`sqlserver2tidb-executor` 是否可用。默认工具缺失只作为 warning 输出；如果当前环境必须具备这些工具，使用：

```bash
bin/sqlserver2tidb doctor --root . --require-tools
```

CI/CD 或监控系统可以使用 JSON 输出：

```bash
bin/sqlserver2tidb doctor --root . --json
```

JSON 输出包含 `repository.valid`、`repository.checked_dirs`、`repository.checked_files`、`repository.errors` 和 `tools[]`。

如果缺少必需文件、必需目录、file schema policy 映射，或者 inventory JSON 无法解析或 status 不是 `pending` / `discovered`，或者 cluster/project 目录名与元数据 ID 不一致，或者 `source-profile.yaml`、cluster state、CDC checkpoint mode/phase/status/updated_at、worker lease phase、active worker lease 必需字段、project state phase/status/updated_at、export/import state phase/status/updated_at、validation status state/phase/updated_at、state payload hash、schema diff status/generated_at/summary counts、evidence JSON ownership/status/generated_at、executor evidence JSON / generated_at、`plan/migration-plan.yaml`、approval 文件中的 `project_id` / `source_cluster_id` / action / mode / status / payload hash 与项目元数据不一致，或者 approval 的 `approved_at` 非空但不是 RFC3339，或者已 approved 的 approval 缺少 reviewer / `payload_hash` / `approved_at`，或者 export/import/CDC/validation plan 缺少 status 或 status 不是 `draft` / `reviewed` / `approved`，或者 export/import/CDC plan 中已有 work item 但缺少执行必需字段，或者 SQL Server `source_object` 不是 `schema.table` / `database.schema.table`，或者 TiDB `target_object` 不是 `table` / `database.table`，或者 reviewed/approved CDC tracked table 缺少 `columns` 或 `key_columns`，或者 CDC key columns 不在 captured columns 中，或者 export plan 使用当前 executor 不支持的 `format`，或者 import plan 使用当前 executor 不支持的 `engine`，或者 export chunk / import job / CDC source / validation check 出现重复标识，或者 import job 引用了不存在的 export chunk，或者 import job 的 `source_uri` 与所依赖 export chunk 的 `output_uri` 不一致，或者 export chunk predicate 仍包含 `TODO`，或者 `plan/validation-plan.yaml` 中的 `row_count` / `row-count` 检查项缺少 `id`、`source_object`、`target_object`，或 predicate / target predicate 仍包含 `TODO`，或者 `checksum` / `sampled_hash` / `bucketed_count` / `business_sql` 检查项缺少 `id`、`source_sql`、`target_sql`，或 source_sql / target_sql 仍包含 `TODO`，命令会返回非零退出码，并列出问题。空的 draft plan 列表仍然是合法的初始化状态；`phase: idle` 的 worker lease 可以为空闲占位文件，但 `export`、`import`、`cdc`、`validation` 或 `cutover` 这类 active phase 必须包含非空 `holder`、`lease_id`、`project_id`、`expires_at` 和 `renewed_at`，`project_id` 必须引用同一源集群下已存在的项目目录，两个时间字段必须是 RFC3339，且 `expires_at` 不能早于 `renewed_at`。示例：

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
  --import-engine sql-insert \
  --compression gzip
```

输入：

- `clusters/<source_cluster_id>/inventory/inventory.json`
- `clusters/<source_cluster_id>/projects/<project_id>/project.yaml`
- `--object-uri-prefix`
- `--chunk-size-rows`
- `--export-format`
- `--import-engine`
- `--compression`

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/plan/export-plan.yaml`
- `clusters/<source_cluster_id>/projects/<project_id>/plan/import-plan.yaml`

当前实现特点：

- 只处理 `project.yaml` 中指定的 source database 和 source schemas。
- 根据 inventory 中的 `row_count` 和 `--chunk-size-rows` 估算 export chunk 数。
- 为每个 export chunk 生成对应 import job。
- 只生成当前内置 executor 支持的计划：`--export-format csv`；默认 `--import-engine sql-insert` 支持本地 `file://`、`http://`、`https://`、`s3://`、`gs://` 或 `azblob://` URI 前缀，HTTP(S) 前缀必须包含 host，S3/GCS 前缀必须包含 bucket，Azure Blob 前缀的 host 是 container；`--compression gzip` 会在 export/import plan 写入 `compression: gzip` 并生成 `.csv.gz` 对象名，worker-executor 会把它透传给 executor；显式 `--import-engine tidb-import-into` 的端到端可执行计划支持本地绝对 `file://`、`s3://` 或 `gs://` URI 前缀，S3/GCS 前缀必须包含 bucket，并会在每个 import job 写入从 inventory 列名生成的 `fields` 列表，但当前不支持与 `--compression gzip` 组合。Azure Blob 目前只用于默认 `sql-insert` 路径。
- 单 schema project 默认保留原表名；多 schema project 会用 `<schema>_<table>` 作为目标表名。
- 单 chunk 表会生成 `1 = 1` predicate；需要拆成多个 chunk 的表仍会先写成 `TODO` split predicate，必须由 DBA 或 operator 根据主键、唯一键、时间列或分桶策略 review；approval 后 `worker-export` 和 `worker-executor --stage export` 会拒绝仍包含 `TODO` 的 predicate。
- 不连接 SQL Server，不连接 TiDB，不读取业务数据，不写对象存储，也不执行 `IMPORT INTO`。

示例 `plan/export-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
format: csv
compression: gzip
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
        output_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv.gz
```

示例 `plan/import-plan.yaml`：

```yaml
status: draft
project_id: sales-db-to-tidb-prod-a
source_cluster_id: prod-sqlserver-a
engine: sql-insert
compression: gzip
mode: append
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv.gz
    depends_on_export_chunk: dbo.orders.000001
```

如果使用 `--import-engine tidb-import-into`，生成的 import job 会额外带 `fields`，用于把内部 null bitmap 尾列映射成 TiDB user variable。默认 `sql-insert` 计划依赖 CSV header，不允许在 import job 里带 `fields`；`validate-repo`、`worker-import` 和 `worker-executor` 都会拒绝这种组合。`tidb-import-into` 当前必须使用 `compression: none`；如果 plan 写了 `compression: gzip` 会被生成、校验和执行器准备阶段拒绝。`tidb-import-into` 的 `fields` 也必须非空、列名不重复、user variable 不重复，并且 user variable 只能使用简单 `@name` 字符集；S3/GCS source URI 可以由 executor 执行前 signed GET 读取 header，因此手工 import plan 可以省略 `fields`，生成计划仍会写入 reviewed `fields` 便于 PR 审查：

```yaml
engine: tidb-import-into
jobs:
  - id: import-dbo.orders.000001
    target_object: app.orders
    source_uri: s3://bucket/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full/dbo.orders.000001.csv
    depends_on_export_chunk: dbo.orders.000001
    fields:
      - "id"
      - "customer_name"
      - "@sqlserver2tidb_null_bitmap"
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

当前 `generate-cdc-plan` 只生成追踪表清单、captured columns、目标端 apply key columns 和 checkpoint 策略，不启用 SQL Server CDC，不创建 Debezium connector，不读取 LSN，不写 Kafka，也不向 TiDB 回放增量。captured columns 来自 inventory 中发现的非计算列；key columns 优先来自 inventory 中发现的 SQL Server primary key，其次来自非过滤 unique index；如果没有这类索引，会生成 `key_columns: []`，必须人工 review 后补齐才能执行 CDC worker 或 executor。执行 `worker-executor --stage cdc` 时还会重新读取当前 `clusters/<source_cluster_id>/inventory/inventory.json`，如果已 review 的 captured columns 与当前非计算列不一致，或 `key_columns` 已不再匹配当前 primary key / 非过滤 unique index，会在生成 executor 命令前失败；这是基于 GitHub 文件的 schema drift gate，不依赖 TiDB metadata 表。

生成或推进显式 CDC LSN range：

```bash
bin/sqlserver2tidb prepare-cdc-range \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --to-lsn 0x00000027000001f40003
```

`prepare-cdc-range` 不连接 SQL Server，不查询当前 max LSN。它读取源集群级 `state/cdc-checkpoint.yaml`，把每张表 checkpoint 里的 `to_lsn` 写成下一轮 plan 的 `from_lsn`，并使用 operator 提供的 `--to-lsn` 写入下一轮 `to_lsn`。如果是第一轮 CDC，还没有 checkpoint entry，则必须显式传 `--from-lsn`。需要读取 SQL Server CDC LSN 边界时，先用 `sqlserver2tidb-executor cdc-lsn --execute` 获取 min/max LSN，再把选定的 `to_lsn` 带入本命令。命令会把 `plan/cdc-plan.yaml` 顶层和 tracked table 的 status 重置为 `draft`，确保新的 range 必须重新通过 PR review 和 approval。

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
    columns:
      - id
      - customer_name
    key_columns:
      - id
    from_lsn: ""
    to_lsn: ""
    apply_batch_size: 1000
    status: draft
```

当前 MVP 也支持从 inventory 和 project metadata 生成项目级 validation plan 草稿：

```bash
bin/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --include-checksum \
  --include-sampled-hash \
  --sample-modulo 100 \
  --include-bucketed-count \
  --bucket-count 16
```

输出：

- `clusters/<source_cluster_id>/projects/<project_id>/plan/validation-plan.yaml`

当前 `generate-validation-plan` 会为 project 范围内每张表生成一个 `row_count` 检查项。加 `--include-checksum` 后，会对存在非 computed 精确数值列的表生成 `checksum` scalar-query 草稿；加 `--include-sampled-hash` 后，会对同时具备整数采样列的表生成 `sampled_hash` scalar-query 草稿，采样谓词由 `--sample-modulo` 控制；加 `--include-bucketed-count` 后，会对具备非 computed 整数分桶列的表按 `--bucket-count` 生成 `bucketed_count` scalar-query 草稿，`--bucket-count` 最大为 `1024`。它不连接 SQL Server 或 TiDB，不执行校验，只把源/目标对象映射和 reviewed SQL 草稿写成可 review 的 GitHub 文件。

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
  - id: dbo.orders.checksum
    type: checksum
    source_sql: "SELECT COALESCE(SUM(COALESCE(CAST([id] AS DECIMAL(38, 6)), 0)), 0) FROM [sales].[dbo].[orders]"
    target_sql: "SELECT COALESCE(SUM(COALESCE(CAST(`id` AS DECIMAL(38, 6)), 0)), 0) FROM `app`.`orders`"
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

当前 MVP 的 `worker-export` 只在 export approval 和 payload hash 匹配、且 `plan/export-plan.yaml` 的 status 已经是 `reviewed` 或 `approved` 后，把 `plan/export-plan.yaml` 写成 planned 状态和 evidence。它会拒绝 draft export plan，也会拒绝仍包含 `TODO` predicate 的 export chunk。`worker-import` 同样要求 `plan/import-plan.yaml` 的 status 已经是 `reviewed` 或 `approved`，并会拒绝 import job `fields` 与所选 import engine 不兼容的计划。它们不执行导出/导入、不连接 SQL Server 或 TiDB、不写对象存储。

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

它会拒绝没有 chunks、缺少必需字段、`output_uri` 不是当前 executor 支持的本地 `file://`、HTTP(S)、`s3://`、`gs://` 或 `azblob://`，或 predicate 仍包含 `TODO` 的 export plan。

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

当前 MVP 的 `worker-import` 只在 import approval 和 payload hash 匹配后，把 `plan/import-plan.yaml` 写成 planned 状态和 evidence；如果 `tidb-import-into` import job 带有 `fields`，会原样写入 `state/import-jobs.yaml`。它会拒绝没有 jobs、缺少必需字段、import job `source_uri` 与所选 import engine 不匹配、`sql-insert` 或 `tidb-lightning` import job 带有 `fields`，或 `tidb-import-into` import job 的 `fields` 为空、重复、包含不安全 user variable。S3/GCS 可由 executor 在执行前读取 header，因此 `tidb-import-into` 的手工 plan 可以省略 `fields`；Azure Blob 当前可用于 `sql-insert` 和 `tidb-lightning`，不用于 TiDB `IMPORT INTO`。`tidb-lightning` plan 还必须解析出一个与所有 job 源目录一致的 `data_source_uri`。metadata worker 本身不执行 TiDB Lightning 或 `IMPORT INTO`，也不连接 TiDB。

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

默认只打印 `sqlserver2tidb-executor import ...` 命令。只有显式加 `--execute`，才会调用外部执行器 binary，并向被调用的 executor 子命令传入 `--execute`。如果确认当前 `sql-insert` import job 运行前目标表必须为空，可以在 `worker-executor` 上加 `--require-empty-target`，它会被透传到生成的 `sqlserver2tidb-executor import` 命令；默认不启用该检查，因为一个目标表可能有多个 chunk import job，需要按顺序追加导入。

### 10.8 阶段 7：CDC 增量回放

CDC checkpoint 是源集群级：

```text
clusters/<source_cluster_id>/state/cdc-checkpoint.yaml
```

`validate-repo` 会校验 checkpoint 归属的 `source_cluster_id`，并要求初始化状态里的 `capture_mode` 或 worker 写回状态里的 `mode` 与 `cluster.yaml` 的 `cdc.mode` 一致。checkpoint 顶层 `phase` 可在初始化时省略，出现时必须是 `cdc`；`status` 只接受 `not_started`、`planned`、`running`、`caught_up` 或 `failed`；`updated_at` 必须非空并使用 RFC3339。表级 checkpoint entry 必须包含源/目标对象，且 SQL Server `source_object` 必须是 `schema.table` / `database.schema.table`，TiDB `target_object` 必须是 `table` / `database.table`；还必须包含 10-byte hex `from_lsn` / `to_lsn`、满足 `from_lsn <= to_lsn`、非负 `applied_changes` 和 RFC3339 `completed_at`。

原因：

- CDC 是 SQL Server 源集群能力。
- 多个 project 可能共享同一个源端 CDC 配置。
- LSN/retention 风险属于源集群级风险。

终极形态支持：

- SQL Server CDC direct reader。
- Debezium SQL Server Connector。
- Kafka-based replay。

LLM 不参与 LSN 判断，也不参与 offset 决策。

当前 MVP 的 `worker-cdc` 只在 `plan/cdc-plan.yaml` 已经是 `reviewed` 或 `approved`、cdc approval 通过且 payload hash 匹配后，把 `plan/cdc-plan.yaml` 写成 planned 状态和 evidence。它会拒绝 draft CDC plan、没有 tracked tables、缺少 `columns`、缺少 `key_columns`、key columns 不在 captured columns 中或缺少其他必需字段的 CDC plan。它不启用 SQL Server CDC，不启动 Debezium，不读取 LSN，不判断 catch-up，也不向 TiDB 回放增量。

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

当前 MVP 已支持 metadata-only validation worker。它不会连接 SQL Server 或 TiDB，只校验当前仓库里的迁移元数据是否满足进入后续执行的最低条件，包括 schema diff 可解析、DDL 已生成、manual review 已清空、conversion report 存在、validation plan 存在，row-count 检查项的 `id`、`source_object`、`target_object` 已填写，`source_object` / `target_object` 符合 executor 支持的对象名形态，并且 predicate / target predicate 不再包含 `TODO`；`checksum`、`sampled_hash`、`bucketed_count` 和 `business_sql` 检查项的 `id`、`source_sql`、`target_sql` 已填写，并且 source_sql / target_sql 不再包含 `TODO`。真实数据校验目前提供行数校验和 reviewed scalar-query 校验路径：先用 `generate-validation-plan` 生成 `plan/validation-plan.yaml`，可选打开 `--include-checksum` / `--include-sampled-hash` / `--include-bucketed-count`，再通过 PR review 补充或确认检查项，并把 validation plan 标成 `reviewed` 或 `approved`。validation approval/hash 通过后，`worker-executor --stage validation` 会先读取当前 inventory 做 schema drift gate：row-count 的 `source_object` 必须仍存在，query 类检查会对 `source_sql` 里可识别的 `FROM` / `JOIN` 三段式 SQL Server 源对象做同样校验；如果 `schema/schema-diff.json` 已 reviewed，还会比较 reviewed 源列基线和当前 inventory 列名/类型。通过后才会为 `type: row_count` 或 `type: row-count` 的检查项生成 `sqlserver2tidb-executor validate-count` 命令，也会为 `type: checksum`、`type: sampled_hash`、`type: bucketed_count` 或 `type: business_sql` 的检查项生成 `sqlserver2tidb-executor validate-query` 命令；执行模式会跑完整个 validation 命令清单，再把每条命令的输出和 exit code 汇总进 `executor-validation-run.json`，因此一个 mismatch 不会遮住后续检查结果。

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

如果 approval 未通过或 hash 不匹配，worker 直接退出，不写 `state/` 或 `evidence/`。如果 approval 通过，worker 写回 `state/validation-status.yaml` 和 `evidence/validation-report.md`。当 validation plan 结构合法时，`validation_plan_checks_valid` 消息会汇总支持的检查类型数量，例如 `1 row_count, 1 checksum, 1 sampled_hash, 16 bucketed_count, 1 business_sql`。如果 `evidence/executor-validation-run.json` 已经存在，worker 还会校验该 evidence 是否属于当前 project、stage 和 approval payload hash，并把 executor 命令数量、失败命令 ID、exit code 和输出摘要写入 validation status/report；只要存在失败的 validation executor 命令，worker 结果就是 `failed`。如果检查失败，worker 会写入 failed 状态，CLI 返回非零退出码。

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

如果行数不同，命令返回非零退出码并输出 mismatch。当前也支持通过 `checksum`、`sampled_hash`、`bucketed_count` 和 `business_sql` 检查项配置源端和目标端各一条返回单行单列的 SQL，并由 `sqlserver2tidb-executor validate-query --execute` 比较标量结果。`generate-validation-plan --include-checksum --include-sampled-hash --include-bucketed-count` 可以自动生成 exact-numeric 和整数分桶 count 的 scalar-query 草稿。执行 `worker-executor --stage validation --execute` 时，即使某个 validation command 已经发现 mismatch，也会继续执行后续 validation command；命令最终返回非零退出码，并把完整检查清单的输出、exit code 和失败 command ID 写入 `executor-validation-run.json`。之后可以再运行 `worker-validate`，把该 evidence 聚合进 `validation-report.md`。当前仍不提供通用行级 digest、生产级 bucketed sampled-hash 或大规模聚合校验引擎。

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

`worker-cutover` 是 metadata-only 的 cutover gate，不会修改 DNS、代理、应用配置或数据库连接。它只在以下条件都满足时写回 completed 状态：

- `approvals/cutover-approval.yaml` 已 approved，且 payload hash 匹配 `project.yaml` 和 `plan/cutover-runbook.md`。
- `plan/cutover-runbook.md` 不再是初始化占位内容，也不包含 `TODO`。
- `evidence/executor-export-run.json`、`evidence/executor-import-run.json` 和 `evidence/executor-validation-run.json` 均为 `succeeded`，且各自仍匹配当前已审批 payload hash。
- `state/validation-status.yaml` 的 status 为 `passed`。
- 非 `offline` 项目还要求 `evidence/executor-cdc-run.json` 为 `succeeded`，且 `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml` 对当前 project 的 status 为 `caught_up`。

通过后它会把 `state/migration-state.yaml` 写成 `phase: completed` / `status: completed`，并生成 `evidence/cutover-evidence.md` 和 `evidence/post-cutover-report.md`。真实业务流量切换仍由应用 owner / SRE 按 runbook 在外部执行，然后把 evidence 通过 PR 固化。

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

worker lease 是上游 SQL Server 集群级，避免多个 worker 同时操作同一个源集群。`phase: idle` 表示空闲占位；当 phase 是 `export`、`import`、`cdc`、`validation` 或 `cutover` 时，lease 必须带有非空 `holder`、`lease_id`、`project_id`、`expires_at` 和 `renewed_at`，`project_id` 必须引用同一源集群下已存在的项目目录，两个时间字段必须是 RFC3339，且 `expires_at` 不能早于 `renewed_at`，否则 `validate-repo` 会拒绝该仓库状态。

当前 MVP 实现了 `worker-export`、`worker-import`、`worker-cdc`、`worker-validate` 和 `worker-cutover`。它们不是完整 reconcile loop，也不会抢占 worker lease。它们只针对一个显式指定的 project 执行 approval gate、payload hash 校验和 metadata-only 状态写回，并要求对应 plan 或 cutover runbook / evidence gate 满足执行条件。

当前 MVP 也实现了只读扫描：

```bash
bin/sqlserver2tidb worker-reconcile --root . --dry-run
```

该命令默认扫描所有 `clusters/<source_cluster_id>/projects/<project_id>/`；生产 agent 建议加 `--source-cluster-id <cluster>`，只扫描一个上游 SQL Server 集群目录。它对 `ddl`、`export`、`import`、`cdc`、`validation` 和 `cutover` 分别执行 approval/hash gate，输出：

- `ready`：approval 已通过、`approved_by` 非空且 payload hash 匹配。
- `blocked`：approval 未通过、hash 不匹配、payload 文件缺失、plan 仍为 draft，或同一 approved payload hash 已有非空 stage state，并给出原因。
- ready metadata action 对应的单项目 worker 命令。
- ready DDL executor action 对应的 `worker-executor --stage ddl` 命令。

`worker-reconcile --dry-run` 不执行 worker，不获取 lease，不写 state/evidence，不创建分支或 PR。它是完整 reconcile loop 的只读预演；如果 export/import/CDC/validation/cutover 的同一 approved payload hash 已经写过 `planned`、`passed`、`completed` 或 `failed` 等非空状态，dry-run 会把该 stage 标成 blocked，避免 loop 对同一 payload 反复写状态。

加上 `--json` 后，dry-run 会输出机器可读 JSON，字段包括 `projects`、`ready_actions`、`blocked_actions` 和 `actions`，方便调度器、监控或外部 agent wrapper 读取。`--json` 只支持 dry-run，不支持 execute-next 或 loop。

当前 MVP 还支持单步执行：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --execute-next \
  --holder agent-a \
  --lease-ttl 15m \
  --state-pr-draft
```

该命令按 source cluster / project / stage 顺序选择第一个 ready metadata-only action，也就是 `export`、`import`、`cdc` 或 `validation`，先写入源集群级 `state/worker-lease.yaml`，再调用对应 metadata-only worker。`ddl` 是 executor-only action，会在 dry-run 中展示，但不会被 `--execute-next` 自动选择。当前同一 `--holder` 可以续租自己的未过期 lease；不同 holder 在 lease 过期前会被拒绝。加上 `--state-pr-draft` 后，它会在项目 `prs/` 目录下写入 `reconcile-<stage>-state-pr.md`，用于审阅本次 state/evidence/lease 写回。它仍然不连接 SQL Server/TiDB/Kafka/对象存储，也不创建 GitHub branch 或 PR。

当前 MVP 还支持 bounded loop：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --loop \
  --holder agent-a \
  --max-iterations 10 \
  --interval 5s
```

该命令使用同一个 holder 重复执行 `--execute-next` 的选择逻辑，直到没有 ready metadata-only action，或达到 `--max-iterations`。`--max-iterations 0` 表示一直运行到没有 ready metadata-only action。它仍然不会选择 DDL、不会运行外部 executor、不会连接 SQL Server/TiDB/Kafka/对象存储，也不会创建 GitHub branch 或 PR；同一 payload hash 已有 state 的 stage 会被 blocked，防止 loop 重复执行同一动作。

面向交付时，可以直接运行 worker agent：

```bash
bin/sqlserver2tidb worker-agent \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --holder agent-a \
  --max-iterations 0 \
  --interval 5s \
  --poll \
  --idle-iterations 0 \
  --state-pr-draft
```

`worker-agent` 是同一 deterministic reconcile loop 的稳定进程入口，适合本地进程管理器或容器运行时使用。它要求 `--holder`，使用源集群级 lease，建议用 `--source-cluster-id` 限定单个上游 SQL Server 集群目录，遵守同一 approval/hash/plan/status/state-dedupe gate；`--poll` 会在没有 ready metadata-only action 时继续按 `--interval` 轮询，`--idle-iterations 0` 表示不限制空转次数。`--state-pr-draft` 会为每次 metadata-only state/evidence/lease 写回生成对应 PR body，但不会创建 branch、commit、push 或 GitHub PR。

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

如果 checkpoint 早于 SQL Server CDC min LSN，则增量缺失。`cdc-orchestrator` 默认会对每张 tracked table 探测 `min_lsn`，并在写入下一段 `cdc-plan.yaml` 前 fail fast；只有外部调度平台已经做了等价检查时，才应使用 `--skip-retention-check`。

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

当前 MVP 可以只读连接 SQL Server catalog 生成 inventory，可以从 inventory 生成 TiDB DDL 草稿、全量导出/导入计划草稿、CDC 计划草稿和 validation plan 草稿，并执行 metadata-only export/import/CDC/validation/cutover worker。`worker-executor` 可以在 approval/hash gate 后生成外部执行器命令；当前 export 只接受 `format: csv`，import 接受 `engine: sql-insert`、`engine: tidb-import-into` 和 `engine: tidb-lightning`。`sqlserver2tidb-executor` 当前已经可以解析这些 work item 并 dry-run 输出上下文。`apply-ddl --execute` 支持把已 review 且不含 `TODO` 的 DDL 文件执行到 TiDB。`export --execute` 支持 SQL Server 到本地 `file://` CSV、HTTP(S) CSV、原生 `s3://` CSV、原生 `gs://` CSV 或原生 `azblob://` CSV 的真实导出路径；默认通过内部 null bitmap 尾列保留 SQL NULL，Lightning 计划则使用 `\N` NULL 编码且不写内部 bitmap。远端对象上传使用本地临时文件 close-time 上传和有界重试。`import --execute` 的默认 `sql-insert` 支持本地 `file://`、HTTP(S)、S3、GCS 或 Azure Blob CSV 到 TiDB 的流式逐行 insert 路径，会识别并排除内部 null bitmap 尾列，并用 `--import-batch-size` 分批提交事务；`tidb-import-into` 会执行 TiDB `IMPORT INTO ... FROM FILE`，支持绝对本地路径、本地 `file://`、`s3://`、`gs://`，S3/GCS URI 必须包含 bucket 和对象路径，其中本地/file/S3/GCS CSV 会读取 header、跳过内部 null bitmap 尾列，并预审计行数、字节数和 SHA256；`tidb-lightning` 会预审计 import plan 中所有 CSV source，生成 TiDB Lightning TOML，并调用外部 `tidb-lightning -config <toml>`，支持 local/file、S3、GCS 和 Azure Blob data-source URI。Azure Blob 当前可用于 `sql-insert` 和 `tidb-lightning`，不用于 TiDB `IMPORT INTO`。`worker-executor --stage validation` 可以在 validation approval/hash gate 后生成 `validate-count` 和 `validate-query` 命令，`validate-count --execute` 支持单对象 SQL Server/TiDB 行数对比，`validate-query --execute` 支持已 review 的 checksum、sampled-hash、bucketed-count 和 business-SQL scalar-query 结果对比。`generate-validation-plan` 可以显式生成 exact-numeric checksum/sample-hash 和整数 bucketed-count scalar-query 草稿。`cdc-lsn --execute` 支持显式读取 SQL Server CDC min/max LSN，`cdc --execute` 支持显式 LSN 范围的 SQL Server CDC 读取和 TiDB upsert/delete apply，`advance-cdc-checkpoint` 可以基于成功 CDC executor evidence 推进源集群 checkpoint snapshot，`prepare-cdc-range` 可以用已提交 checkpoint 和 operator 提供的 `--to-lsn` 生成下一段 CDC plan；`cdc-orchestrator` 可以长期探测 max LSN、生成下一段 CDC range PR 草稿，并可通过 `--apply-approved` 执行已经审批通过的当前 range 后自动推进 checkpoint；`worker-cutover` 可以在 reviewed runbook、成功 executor evidence、passed validation state 和 caught-up CDC checkpoint 都满足后写入 completed 状态和 cutover/post-cutover evidence。自动审批和 merge PR 仍是后续能力。通用行级 digest、生产级 bucketed sampled-hash 和大规模校验引擎仍是后续能力。

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

该命令检查必需目录、必需文件、`global/policies/file-schema-policy.yaml` 中的 plan schema 映射，以及已填写的 export/import/CDC/validation plan work item 的关键字段、唯一性、import/export 依赖关系、export output URI 与当前 executor 的兼容性、import source URI 与 export output URI 的一致性、import source URI 与所选 import engine 的兼容性、import job `fields` 与所选 import engine 的兼容性、`tidb-import-into` fields 内容合法性，以及未清理的 `TODO` predicate。它不会连接 SQL Server 或 TiDB，也不会执行数据迁移。

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
  --import-engine sql-insert \
  --compression gzip
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/export-plan.yaml` 和 `plan/import-plan.yaml`。它只生成当前内置 executor 支持的 CSV 草稿。默认 `--import-engine sql-insert` 时，`--object-uri-prefix` 可以使用本地 `file://`、`http://`、`https://`、`s3://`、`gs://` 或 `azblob://` 前缀，生成的 import job 不带 `fields`；`--compression gzip` 会生成 `.csv.gz` 对象名，并在 plan 中写入 `compression: gzip`。显式 `--import-engine tidb-import-into` 时，端到端可执行的 `--object-uri-prefix` 必须使用本地绝对 `file://`、`s3://` 或 `gs://` 前缀，并会在每个 import job 中写入 `fields`，内容是 inventory 列名加上 `@sqlserver2tidb_null_bitmap`；当前 `tidb-import-into` 不支持 `--compression gzip`。显式 `--import-engine tidb-lightning` 时，`--object-uri-prefix` 可以使用本地 `file://`、`s3://`、`gs://` 或 `azblob://` 前缀；生成的 export 文件名基于 TiDB 目标对象，export plan 写入 `null_encoding: backslash-n`，CSV 不再追加内部 null bitmap 列，SQL NULL 编码为 `\N`，import plan 写入顶层 `data_source_uri`，后续 `worker-executor --stage import` 会生成一个聚合 Lightning 命令。执行 `worker-executor --stage export` 或 `--stage import` 时会重新读取当前 inventory；如果 source table 已消失，reviewed `schema-diff.json` 的源列基线与当前 inventory 不一致，或 `tidb-import-into` reviewed `fields` 与当前 inventory 列不一致，会在生成 executor 命令前失败。该命令不连接 SQL Server 或 TiDB，不执行导出或导入。`--chunk-size-rows` 默认是 `1000000`，`--export-format` 默认是 `csv`，`--import-engine` 默认是 `sql-insert`，`--compression` 默认是 `none`。

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

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/cdc-plan.yaml`。它只生成草稿，不启用 SQL Server CDC，不启动 Debezium，不读取 LSN，不连接 TiDB，也不执行增量回放。生成的 tracked table 会包含 `columns` 和 `key_columns`；`columns` 来自非计算列，`key_columns` 优先使用 SQL Server primary key，其次使用非过滤 unique index；缺少可用 key 时会保留空列表等待人工 review。`--mode` 默认是 `sqlserver-cdc`，`--retention-hours` 默认是 `168`，`--apply-batch-size` 默认是 `1000`。

### 16.9.1 prepare-cdc-range

```bash
bin/sqlserver2tidb prepare-cdc-range \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --to-lsn 0x00000027000001f40003
```

该命令读取当前 `plan/cdc-plan.yaml` 和源集群级 `state/cdc-checkpoint.yaml`，把已有 checkpoint entry 的 `to_lsn` 写成下一段 plan 的 `from_lsn`，并把 operator 提供的 `--to-lsn` 写成下一段 plan 的 `to_lsn`。如果 tracked table 还没有 checkpoint entry，必须传 `--from-lsn` 作为第一段 CDC range 的起点。命令会校验 `from_lsn <= to_lsn`；不连接 SQL Server，不查询 max LSN，会把 CDC plan 顶层和 tracked table status 重置为 `draft`，让新的 range 重新经过 PR review 和 approval。

### 16.9.2 prepare-cdc-iteration

```bash
bin/sqlserver2tidb prepare-cdc-iteration \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --max-lsn 0x00000027000001f40004 \
  --pr-draft
```

该命令是连续 CDC 编排里的 GitHub 文件推进步骤。它不连接 SQL Server，而是接收外部探测得到的 `--max-lsn`，读取源集群级 `state/cdc-checkpoint.yaml`，把每张 tracked table 的 checkpoint `to_lsn` 与 `--max-lsn` 比较。如果还有增量需要执行，它会把下一段 range 写入 `plan/cdc-plan.yaml`，把 plan 状态重置为 `draft`，并在 `--pr-draft` 开启时生成 `prs/cdc-range-pr.md` 供 review。如果所有 tracked table 都已经到达 `--max-lsn`，命令输出 `status: caught_up`，不会修改 plan，也不会生成 PR 草稿。第一段 CDC 没有 checkpoint 时，仍需要通过 `--from-lsn` 提供起点。

### 16.9.3 cdc-orchestrator

```bash
bin/sqlserver2tidb cdc-orchestrator \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --apply-approved \
  --poll \
  --pr-draft
```

`cdc-orchestrator` 是连续 CDC 的 probe / approved-apply / plan 入口。它会调用 `sqlserver2tidb-executor cdc-lsn --execute` 探测 SQL Server 当前 `max_lsn`，并对每张 tracked table 追加 `--source-object` 探测 capture instance 的 `min_lsn`，再复用 `prepare-cdc-iteration` 生成下一段 `plan/cdc-plan.yaml` 和可选 `prs/cdc-range-pr.md`。如果下一段 `from_lsn` 已经早于 SQL Server 返回的 `min_lsn`，命令会在修改 plan 前失败，避免生成无法完整回放的 CDC range；只有外部 scheduler 已经完成等价 retention guard 时，才使用 `--skip-retention-check`。默认 executor binary 是 `sqlserver2tidb-executor`，可以用 `--executor-binary` 覆盖；SQL Server 连接串环境变量默认是 `SQLSERVER2TIDB_SOURCE_CONNECTION_STRING`，可以用 `--source-connection-string-env` 覆盖；TiDB 连接串环境变量默认是 `SQLSERVER2TIDB_TARGET_CONNECTION_STRING`，可以用 `--target-connection-string-env` 覆盖；`--from-lsn` 用于第一段还没有 checkpoint 的表。

在 `--apply-approved` 模式下，每轮探测前会先检查当前 `plan/cdc-plan.yaml` 和 `approvals/cdc-approval.yaml` 是否已经通过 approval/hash gate。如果已通过且 checkpoint 尚未覆盖该 range，它会调用 `worker-executor --stage cdc --execute`；该路径会先做 inventory schema drift gate，确认 reviewed CDC columns 和 key columns 仍匹配当前 inventory，再要求 executor 输出 `applied changes: N`，写入 `evidence/executor-cdc-run.json`，然后调用 checkpoint advance 逻辑更新源集群级 `state/cdc-checkpoint.yaml`。如果 checkpoint 已经覆盖当前 range，它会跳过 apply，避免长循环重复执行同一段 LSN。`--command-timeout`、`--command-retries`、`--retry-backoff` 和 `--resume` 会透传给内部 CDC executor 执行路径。

在 `--poll` 模式下，如果当前 checkpoint 已经追到 `max_lsn`，命令会按 `--interval` 继续探测；`--idle-iterations 0` 表示不限制连续 caught-up 次数，正整数适合 smoke test 或批处理退出；`--max-iterations 0` 表示不限制探测次数。只要发现新 range，它会写 plan/PR 草稿并停止，让 operator 走 GitHub review 和 approval。它不会自动 approve 或 merge PR。

如果上游 max LSN 由外部 scheduler 已经探测好，也可以用 `--max-lsn 0x...` 跳过 executor 探测；这主要用于集成测试或把 LSN 探测交给独立平台组件。

### 16.9.4 generate-validation-plan

```bash
bin/sqlserver2tidb generate-validation-plan \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --include-checksum \
  --include-sampled-hash \
  --sample-modulo 100 \
  --include-bucketed-count \
  --bucket-count 16
```

该命令读取源集群 inventory 和项目 metadata，写回项目目录下的 `plan/validation-plan.yaml`。它为 project 范围内每张表生成一个 `row_count` 检查项；`--include-checksum` 会为有精确数值列的表生成 `checksum` scalar-query 草稿；`--include-sampled-hash` 会为有整数采样列的表生成 `sampled_hash` scalar-query 草稿；`--sample-modulo` 默认是 `100`；`--include-bucketed-count` 会为有整数分桶列的表生成 `bucketed_count` scalar-query 草稿；`--bucket-count` 默认是 `16`，最大是 `1024`。命令只生成草稿，不连接 SQL Server 或 TiDB，也不执行校验。

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

该命令计算指定 stage 的 payload hash。当前支持 `ddl`、`export`、`import`、`cdc`、`validation` 和 `cutover` stage。这个 hash 应写入对应 approval 文件，防止审批后 payload 文件被修改。

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

该命令先检查 `approvals/import-approval.yaml`，只有 approval 通过且 payload hash 匹配时才读取 `plan/import-plan.yaml`，并写回 `state/import-jobs.yaml` 和 `evidence/import-summary.json`。如果 `tidb-import-into` import job 带有 `fields`，planned state 会保留该字段列表，便于后续 executor PR 审计；`sql-insert` import job 带 `fields`、`tidb-import-into` fields 为空/重复/包含不安全 user variable，或 `gs://` source URI 缺少 `fields` 会被拒绝。它只写 planned 状态，不执行真实导入。

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

该命令先检查 `approvals/validation-approval.yaml`，只有 approval 通过、payload hash 匹配，且 `plan/validation-plan.yaml` 已经是 `reviewed` 或 `approved` 时才执行 validation checks。执行后写回 `state/validation-status.yaml` 和 `evidence/validation-report.md`，并在 `validation_plan_checks_valid` 消息中列出支持的 validation check 类型数量。

### 16.17 worker-cutover

```bash
bin/sqlserver2tidb worker-cutover \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a
```

该命令先检查 `approvals/cutover-approval.yaml`，只有 approval 通过且 payload hash 匹配时才执行 cutover gate。它要求 cutover runbook 已经由占位内容改成 reviewed 内容，且不包含 `TODO`；要求 export/import/validation executor evidence 都是 `succeeded`；要求 validation worker 状态为 `passed`；非 offline 项目还要求 CDC executor evidence 为 `succeeded` 且源集群 `state/cdc-checkpoint.yaml` 为 `caught_up`。通过后写回：

```text
state/migration-state.yaml
evidence/cutover-evidence.md
evidence/post-cutover-report.md
```

它不会修改应用流量、DNS、代理或数据库连接，只把已审批 cutover gate 的结果固化成 GitHub metadata。

### 16.18 worker-executor

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

该命令支持 `ddl`、`export`、`import`、`cdc` 和 `validation`。它复用对应 stage 的 approval/hash gate，只有 approval 通过、payload hash 匹配，且 DDL 的 `schema/schema-diff.json` 已经是 `reviewed`，或 export/import/CDC/validation plan 已经是 `reviewed` 或 `approved` 时才生成执行器命令。生成 export/import/validation/CDC executor 命令前，它会读取当前 `clusters/<source_cluster_id>/inventory/inventory.json` 做 source schema drift gate：plan 引用的源表必须仍然存在；如果 `schema/schema-diff.json` 已 reviewed，则 reviewed 源列名/类型必须仍匹配当前 inventory；`tidb-import-into` reviewed `fields` 必须仍匹配当前 inventory 列；CDC captured columns 和 key columns 也必须仍匹配当前 inventory 与当前主键/非过滤唯一索引。这个 gate 只使用 GitHub 文件，不依赖 TiDB metadata 表。默认外部 binary 是 `sqlserver2tidb-executor`，可以通过 `--executor-binary` 覆盖。`--source-connection-string-env`、`--target-connection-string-env` 和 `--import-batch-size` 会被渲染进生成的 executor 命令，不写入 GitHub metadata；`--require-empty-target` 只会被渲染进 `sql-insert` import 命令；`tidb-import-into` import job 里的可选 `fields` 会被透传成 executor 的 `--fields` 参数；`tidb-lightning` import plan 会被渲染成一个 `--engine tidb-lightning --job-id tidb-lightning --source-uri <data_source_uri> --import-plan <path>` 聚合命令；CDC tracked table 的 `columns` / `key_columns` / `from_lsn` / `to_lsn` 会被透传成 executor 的 `--columns` / `--key-columns` / `--from-lsn` / `--to-lsn` 参数。默认 dry-run 只打印命令；只有加 `--execute` 才会调用外部 binary，并在 executor 子命令后自动注入 `--execute`。执行模式会写回 `evidence/executor-<stage>-run.json`，记录 payload hash、每条命令、输出、exit code、每条命令的开始/结束时间和耗时；超时、重试、resume、CDC `applied changes: N` 解析、validation 聚合失败语义与前述章节一致。`ddl`、`export`、`import` 和 `cdc` stage 仍然是 fail-fast。export plan 只有 `format: csv`、`null_encoding` 为 `bitmap` 或 `backslash-n`，且 export chunk `output_uri` 为当前 executor 支持的 `file://`、HTTP(S)、`s3://`、`gs://` 或 `azblob://` 时才会生成当前 executor 命令；import plan 只有 `engine: sql-insert`、`engine: tidb-import-into` 或 `engine: tidb-lightning`，import job `source_uri` 与 engine 匹配，且 import job `fields` 只用于 `tidb-import-into` 时才会生成当前 executor 命令。当前随仓库提供的 executor 可以执行 TiDB DDL、SQL Server CSV/gzip CSV export、`sql-insert`、TiDB `IMPORT INTO`、TiDB Lightning、validation query/count、CDC LSN 探测和显式 CDC range apply；当前还没有自动审批或 merge PR。

export/import executor 的数据量指标有固定输出约定：export 成功时输出 `exported rows: N`、`output bytes: N` 和 `output sha256: sha256:<digest>`；`sql-insert` import 成功时输出 `imported rows: N`、`input bytes: N` 和 `input sha256: sha256:<digest>`；本地路径、`file://`、S3 或 GCS 的 `tidb-import-into` import 会在执行 `IMPORT INTO` 前预读 CSV 做审计；`tidb-lightning` import 会在启动外部 Lightning 前预读 import plan 中所有 CSV source，拒绝仍带内部 null bitmap 列的文件，并输出聚合 `imported rows`、`input bytes` 和 `input sha256`。`worker-executor --execute` 会把完整且非负的指标对写入 command 级 `data_rows` 和 `data_bytes`，并把合法 SHA256 写入 `data_sha256`；如果只出现其中一个行数/字节数指标、值不是非负整数，或 SHA256 格式不合法，会先写 failed evidence 再返回非零退出码。

对 export、`sql-insert` import、本地路径、`file://`、S3 或 GCS 的 `tidb-import-into` import，以及 `tidb-lightning` import，`worker-executor --execute` 还会把“命令 exit 0 但缺少完整数据审计输出”视为失败；这样不会先写一个看似成功、随后又被 PR gate 拒绝的 evidence。

`validate-repo` 和 executor evidence PR 生成也会拒绝缺少完整数据审计的成功 export / `sql-insert` import / 本地、S3 或 GCS `tidb-import-into` / `tidb-lightning` evidence；也就是说这些成功 evidence 必须同时具备 `data_rows`、`data_bytes` 和合法 `data_sha256`。

本地 `file://` export 不会直接写最终文件名；executor 会在同目录写隐藏临时文件，只有 CSV/gzip 输出流成功关闭后才 rename 到目标路径。导出失败路径会 abort 并删除临时文件，避免下游 import 读到半截 CSV。

HTTP(S) export 在 CSV 写入失败时会中止请求体，而不是把流正常 close 成一次成功上传。这不能替代对象存储侧的版本控制/生命周期清理策略，但可以避免 executor 主动把失败路径提交为一个完整的部分对象。

HTTP(S) import 会显式请求 `Accept-Encoding: identity`，避免 Go HTTP 客户端或对象存储网关自动 gzip 解压改变 `input bytes` / `input sha256` 的口径。需要导入 gzip 对象时，仍应通过 reviewed plan 和 executor 参数使用 `--compression gzip`。

`worker-executor --resume` 复用 export 和 `sql-insert` import 成功 evidence 时，也要求该 command 已有完整 `data_rows`、`data_bytes` 和合法 `data_sha256`。因此从旧版本升级后，如果旧 evidence 没有这些审计字段，resume 会重新执行对应 export/import command 来补齐数据通道审计信息；`tidb-import-into` 当前不输出这些数据通道指标，不受该复用条件影响。

### 16.19 advance-cdc-checkpoint

```bash
bin/sqlserver2tidb advance-cdc-checkpoint \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --status running
```

该命令读取 `clusters/<source_cluster_id>/projects/<project_id>/evidence/executor-cdc-run.json`，复用 executor evidence 的 approval、payload hash、CDC plan reviewed、命令结构校验，要求 evidence status 是 `succeeded`，并要求每条 CDC 命令有结构化的 `cdc_applied_changes`。命令还会校验 evidence 里的 `--source-object`、`--target-object`、`--from-lsn`、`--to-lsn` 与当前 reviewed `plan/cdc-plan.yaml` 完全一致，然后重写源集群级 `clusters/<source_cluster_id>/state/cdc-checkpoint.yaml`。默认写 `status: running`；只有 operator 明确确认该 LSN range 已经追平时才使用 `--status caught_up`。该命令不会连接 SQL Server，不会查询当前 max LSN，不会启动长周期 CDC loop；checkpoint 文件仍应和 executor evidence 一起通过 PR review。下一段 LSN range 可用 `prepare-cdc-range --to-lsn ...` 基于已提交 checkpoint 生成。

### 16.20 sqlserver2tidb-executor

DDL dry-run：

```bash
bin/sqlserver2tidb-executor apply-ddl \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --ddl-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/schema/tidb-ddl/dbo.orders.sql
```

DDL dry-run 会读取 DDL 文件，拒绝仍包含 `TODO` 的文件或没有任何 SQL statement 的文件；它不会打开 TiDB 连接。

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

执行模式同样会读取 DDL 文件并拒绝仍包含 `TODO` 的文件，然后用目标连接串执行其中的 SQL 语句。

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

导出 dry-run 会校验 `--output-uri` 是否为当前 executor 支持的 `file://`、`http://`、`https://`、`s3://`、`gs://` 或 `azblob://` CSV 输出，并拒绝仍包含 `TODO` 的 predicate；它不会打开 SQL Server，也不会写 CSV。

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

也可以用 `--source-connection-string-env <ENV_NAME>` 指定其他环境变量。执行模式会拒绝仍包含 `TODO` 的 predicate，并且当前只接受 `file://`、`http://`、`https://`、`s3://`、`gs://` 和 `azblob://` 输出 URI。

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

也可以用 `--target-connection-string-env <ENV_NAME>` 指定其他环境变量。即使不加 `--execute`，executor import dry-run 也会校验所选 engine 与 `--source-uri` 是否兼容，并校验 `--compression` 和 `tidb-import-into` 字段列表；它不会打开 TiDB，也不会打开 CSV source。默认 `--engine sql-insert` 会读取 CSV header 作为目标列名；如果 header 最后一列是内部 `__sqlserver2tidb_null_bitmap`，import 会把该列从目标列中排除，并根据 bitmap 把对应字段恢复为 NULL。`sql-insert` 不需要 GitOps import job `fields`，相关 metadata gate 会拒绝 `sql-insert + fields`。如果 source 是 gzip CSV，使用 `--compression gzip` 后会边解压边流式读取，不会把完整文件载入内存。随后它流式读取 CSV 行，并按 `--import-batch-size` 分批事务提交 `INSERT`。如果为 `sql-insert` 增加 `--require-empty-target`，executor 会先连接 TiDB 并对目标表执行带引用保护的 `COUNT(*)`；如果目标表已有数据，会在打开 CSV source 前失败。默认不启用该检查，因为同一目标表的多 chunk 导入需要第二个及后续 job 在非空表上继续插入。

使用 TiDB 原生 `IMPORT INTO`：

```bash
bin/sqlserver2tidb-executor import \
  --execute \
  --engine tidb-import-into \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --job-id import-dbo.orders.000001 \
  --target-object app.orders \
  --source-uri file:///tmp/sqlserver2tidb/dbo.orders.000001.csv
```

`tidb-import-into` 会执行形如 `IMPORT INTO app.orders FROM '<fileLocation>' FORMAT 'csv' WITH skip_rows=1` 的 TiDB SQL。根据 TiDB 官方文档，目标表必须已经存在且为空，执行过程不支持事务回滚；详见 https://docs.pingcap.com/tidb/stable/sql-statement-import-into/ 。当前实现支持绝对本地路径、本地 `file://`、`s3://`、`gs://` file location，并拒绝相对本地路径、带远端 host 的 `file://`、缺 bucket 的 `s3://`/`gs://`，以及缺对象路径的 `s3://`/`gs://`。执行 `IMPORT INTO` 前，executor 会先对目标表执行带引用保护的 `COUNT(*)` 预检；如果目标表不存在、无查询权限或行数非 0，会在发起 `IMPORT INTO` 前失败。该预检不能替代导入期间禁止并发 DDL/DML 的操作要求。对于本地路径、`file://`、`s3://` 和 `gs://`，executor 会读取 CSV header，并把内部 `__sqlserver2tidb_null_bitmap` 尾列映射到 TiDB user variable 以跳过该字段；S3/GCS 读取使用 signed GET，同时会预审计行数、字节数和 SHA256。S3/GCS 端到端计划可由 `generate-data-plans --import-engine tidb-import-into` 生成；Azure Blob 当前只适用于 `sql-insert`。worker-executor 会把已 review 的字段透传为 `--fields id,name,@sqlserver2tidb_null_bitmap`；如果省略字段，executor 会从 CSV header 自动推导。字段列表会在 GitOps 和 executor dry-run 阶段校验，拒绝空字段、重复列名、重复 user variable，以及包含空格、分号、括号等不安全字符的 user variable。

使用 TiDB Lightning：

```bash
SQLSERVER2TIDB_LIGHTNING_PD_ADDR=pd.example.com:2379 \
bin/sqlserver2tidb-executor import \
  --execute \
  --engine tidb-lightning \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --job-id tidb-lightning \
  --source-uri s3://migration-bucket/sqlserver2tidb/full \
  --import-plan clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/plan/import-plan.yaml \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

`tidb-lightning` 不按单个 job 写入一个目标表，而是读取已 review 的 import plan，预审计所有 `source_uri` 对应 CSV，然后生成 TiDB Lightning TOML 并调用外部 `tidb-lightning -config <toml>`。目标 TiDB 连接串仍通过 `--target-connection-string-env` 提供，连接串必须是可解析的 TCP MySQL/TiDB DSN；PD 地址通过 `--lightning-pd-addr` 或 `SQLSERVER2TIDB_LIGHTNING_PD_ADDR` 提供；sorted KV 目录默认是 `/tmp/sqlserver2tidb-lightning-sorted-kv`，可用 `--lightning-sorted-kv-dir` 覆盖。默认使用临时 TOML 文件，也可以用 `--lightning-config-file` 固定输出路径便于审计。Lightning 计划必须由 `generate-data-plans --import-engine tidb-lightning` 或等价 reviewed metadata 生成，这样 export CSV 会使用 `null_encoding: backslash-n`，不包含内部 null bitmap 列，并在 TOML 中配置 `null = '\N'`。

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

也可以分别用 `--source-connection-string-env <ENV_NAME>` 和 `--target-connection-string-env <ENV_NAME>` 指定其他环境变量。dry-run 和执行模式都会拒绝仍包含 `TODO` 的 source predicate 或 target predicate；执行模式还会在源端和目标端的 `COUNT(*)` 不一致时返回非零退出码。

reviewed scalar-query 校验 dry-run：

```bash
bin/sqlserver2tidb-executor validate-query \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --check-id orders-total \
  --source-sql "SELECT SUM(total) FROM sales.dbo.orders" \
  --target-sql "SELECT SUM(total) FROM app.orders"
```

执行 reviewed scalar-query 校验：

```bash
bin/sqlserver2tidb-executor validate-query \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --check-id orders-total \
  --source-sql "SELECT SUM(total) FROM sales.dbo.orders" \
  --target-sql "SELECT SUM(total) FROM app.orders"
```

dry-run 和执行模式都会拒绝仍包含 `TODO` 的 source SQL 或 target SQL。执行模式要求源端和目标端 SQL 都只返回一行一列。命令会把两边标量结果归一化为字符串后比较；不一致时返回非零退出码。也可以分别用 `--source-connection-string-env <ENV_NAME>` 和 `--target-connection-string-env <ENV_NAME>` 指定其他环境变量。

读取 SQL Server CDC LSN 边界：

```bash
export SQLSERVER2TIDB_SOURCE_CONNECTION_STRING='server=sqlserver-a.internal;user id=readonly;password=REDACTED;database=sales;encrypt=true'

bin/sqlserver2tidb-executor cdc-lsn \
  --execute \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders
```

执行模式会读取 `sys.fn_cdc_get_max_lsn()`；如果提供 `--source-object`，还会按 `schema_table` 规则推导 capture instance，并读取 `sys.fn_cdc_get_min_lsn()`。输出的 `max_lsn` 可作为 `prepare-cdc-iteration --max-lsn` 的候选输入，也可以直接作为 `prepare-cdc-range --to-lsn` 的显式输入；如果是第一段 CDC range，输出的 `min_lsn` 可作为 `prepare-cdc-iteration --from-lsn` 或 `prepare-cdc-range --from-lsn` 的候选输入。该命令只读 SQL Server，不写 GitHub metadata，也不会直接修改 CDC plan。

CDC dry-run：

```bash
bin/sqlserver2tidb-executor cdc \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-object sales.dbo.orders \
  --target-object app.orders \
  --columns id,customer_name \
  --key-columns id \
  --from-lsn 0x00000027000001f40001 \
  --to-lsn 0x00000027000001f40002 \
  --apply-batch-size 1000
```

CDC dry-run 会校验 `columns`、`key-columns`、key column 是否属于 captured columns、`from-lsn` / `to-lsn` 是否为 10-byte hex 且 `from-lsn <= to-lsn`；它不会启动 CDC reader，也不会连接 TiDB 执行 apply。

当前 binary 默认只做参数解析、对象名格式预检、DDL 文件 review 状态检查、export output URI / import engine/source URI / compression / CDC LSN range 兼容性校验和 dry-run 输出。`export --execute` 会连接 SQL Server，并把 CSV 写到本地 `file://`、HTTP(S) URL、原生 `s3://`、`gs://` 或 `azblob://` 对象；默认会在 header 尾部增加内部 `__sqlserver2tidb_null_bitmap` 列，Lightning 计划则通过 `--null-encoding backslash-n` 生成无 bitmap、NULL 为 `\N` 的 CSV。加 `--compression gzip` 时会写 gzip CSV；HTTP(S)、S3、GCS 和 Azure Blob export 上传会设置 `Content-Encoding: gzip`。远端 export 会先把 CSV/gzip payload 写入本地临时文件，CSV writer 成功关闭后才执行 PUT 上传；S3 使用 AWS Signature V4，GCS 使用 HMAC V4 signing，Azure Blob 使用 Shared Key 签名。HTTP(S)/S3/GCS/Azure Blob CSV 下载和上传会对请求错误以及 408/429/5xx 响应做最多 3 次有界重试，上传重试会重放完整临时文件 payload。`import --execute --engine sql-insert` 会连接 TiDB，并从本地 `file://`、HTTP(S)、S3、GCS 或 Azure Blob CSV 流式逐行插入，按 batch size 分批提交；如果 CSV 带内部 null bitmap 尾列，会恢复 NULL 并不把该内部列写入目标表。`import --execute --engine tidb-import-into` 会连接 TiDB，先用 `COUNT(*)` 预检目标表为空，再执行 `IMPORT INTO ... FROM FILE`，支持绝对本地路径、本地 `file://`、`s3://`、`gs://`；当前该 engine 不接受 `--compression gzip`，也不接受 Azure Blob source URI。`import --execute --engine tidb-lightning` 会预审计 import plan 中所有 CSV source，生成 TiDB Lightning TOML，然后调用外部 `tidb-lightning -config <toml>`；该路径支持 local/file、S3、GCS 和 Azure Blob data-source URI，但仍依赖部署环境中的 TiDB Lightning 二进制、PD 地址和对象存储访问能力。当前不会生成 Parquet。`validate-count --execute`、`validate-query --execute`、`cdc-lsn --execute`、`cdc --execute`、`advance-cdc-checkpoint`、`prepare-cdc-range` 和 `cdc-orchestrator` 能力保持不变。当前仍不会自动审批或 merge PR。

executor execute 模式的数据通道输出可以直接用于审计：`export --execute` 成功输出导出行数、写入字节数和输出 SHA256，`import --execute --engine sql-insert`、`tidb-import-into` 的可审计来源，以及 `tidb-lightning` 成功输出导入行数、读取字节数和输入 SHA256。`worker-executor --execute` 会把这些输出提升为结构化 evidence 字段，避免 reviewer 只能从文本日志里人工查找。

### 16.21 worker-reconcile

只读扫描：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --dry-run
```

单步执行：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --execute-next \
  --holder agent-a \
  --lease-ttl 15m \
  --state-pr-draft
```

bounded loop：

```bash
bin/sqlserver2tidb worker-reconcile \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --loop \
  --holder agent-a \
  --max-iterations 10 \
  --interval 5s
```

`--dry-run` 默认扫描所有 source cluster 和 project，也可以通过 `--source-cluster-id` 限定到一个源集群；它对 `ddl`、`export`、`import`、`cdc`、`validation` 计算 ready/blocked 状态，并为 ready action 输出对应命令。`ddl` 的命令是 `worker-executor --stage ddl`；其他 metadata-only stage 会输出对应的单项目 worker 命令。它不执行 worker，不获取 worker lease，不写 state/evidence，也不创建 GitHub PR。

`--dry-run --json` 输出同一报告的 JSON 形式，适合调度器、监控和外部 agent wrapper 消费；JSON 模式不会输出文本 header。

`--execute-next` 只会选择第一个 ready metadata-only action，也就是 `export`、`import`、`cdc` 或 `validation`，获取或续租 source-cluster 级 worker lease，然后执行对应 metadata-only worker。`ddl` 是 executor-only action，需要显式通过 `worker-executor --stage ddl` 执行。`--holder` 必填，`--lease-ttl` 默认是 `15m`。该模式会写 `state/worker-lease.yaml` 和被选中 worker 对应的 state/evidence 文件；active lease 会包含 `holder`、`lease_id`、`phase`、`project_id`、`expires_at` 和 `renewed_at`，时间字段使用 RFC3339。

`--loop` 会用同一个 holder 重复执行 ready metadata-only action，直到没有 ready metadata-only action 或达到 `--max-iterations`。`--max-iterations 0` 表示不设置迭代上限，只在没有 ready metadata-only action 时退出。`--interval` 控制两次迭代之间的等待时间。loop 使用与 `--execute-next` 相同的 approval、payload hash、plan status、state dedupe 和 lease 规则；它不执行 DDL、不调用外部 executor、不创建 GitHub PR，也不连接数据库或对象存储。

`--state-pr-draft` 在 `--execute-next` 和 `--loop` 模式下生效。启用后，命令会额外生成项目级 PR body：

```text
clusters/<source_cluster_id>/projects/<project_id>/prs/reconcile-<stage>-state-pr.md
```

该 PR body 会列出 payload hash、lease id、建议 branch、项目 state/evidence 文件、源集群 `state/worker-lease.yaml`；如果 stage 是 `cdc`，还会列出源集群 `state/cdc-checkpoint.yaml`。它只是 PR 草稿，不会创建 branch、commit、push 或 GitHub PR。`worker-agent --state-pr-draft` 生成的是同一种 PR body。

### 16.22 worker-agent

```bash
bin/sqlserver2tidb worker-agent \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --holder agent-a \
  --max-iterations 0 \
  --interval 5s \
  --poll \
  --idle-iterations 0 \
  --state-pr-draft
```

`worker-agent` 是 `worker-reconcile --loop` 的交付入口，用于本地进程或容器中长期运行 metadata-only reconcile。它要求 `--holder`，建议用 `--source-cluster-id` 把 agent 限定到单个上游 SQL Server 集群目录；默认 `--max-iterations 0`，即一直运行到没有 ready metadata-only action；加上 `--poll` 后，没有 ready action 时不会退出，而是按 `--interval` 继续轮询；`--idle-iterations 0` 表示不限制连续 idle 次数，正整数可用于 smoke test 或批处理退出；`--lease-ttl` 默认 `15m`；`--state-pr-draft` 会为被执行的 action 生成 state/evidence/lease PR body。它不执行 DDL、不调用外部 executor、不创建 GitHub PR，也不连接数据库或对象存储。

### 16.23 create-worker-state-pr

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

该命令要求 `worker-reconcile --execute-next --state-pr-draft`、`worker-reconcile --loop --state-pr-draft` 或 `worker-agent --state-pr-draft` 已经生成对应的 `reconcile-<stage>-state-pr.md`，并要求被提交的 state/evidence/lease 文件已经存在。如果已存在 `evidence/executor-<stage>-run.json`，命令会先按 executor evidence PR 相同规则校验 approval、payload hash、已 review 的执行指令、status、command id、args、exit code、command error 和 timing 字段，再把该执行证据一并加入 `git add` 文件列表。默认 dry-run 只打印 `git switch`、`git add`、`git commit`、`git push` 和 `gh pr create` 命令，并提示 PR body 文件列表是否需要刷新；dry-run 不会修改 PR body。只有加 `--execute` 才会在提交前刷新 PR body、修改本地 git checkout、推送分支并调用 GitHub CLI。它不会 merge PR、approve PR、绕过 branch protection，也不会判断 worker 结果是否业务正确。

### 16.24 generate-executor-evidence-pr-draft / create-executor-evidence-pr

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

`generate-executor-evidence-pr-draft` 要求 `evidence/executor-<stage>-run.json` 已存在，并要求对应 stage approval 已通过、payload hash 与当前 metadata 匹配，且 DDL 的 `schema/schema-diff.json` 或对应 stage plan 仍处于已 review 状态。它会校验 evidence 内的 `source_cluster_id`、`project_id`、`stage` 和 `payload_hash`；evidence status 只接受 `succeeded` 或 `failed`，顶层 `generated_at` 如果存在必须是 RFC3339，command id 不能重复。它会拒绝没有 executor command 记录的 evidence；每条 command 记录必须包含 `id`、非空 `args`、与 args 匹配的 shell-quoted `shell_command`、`exit_code`、`started_at`、`completed_at` 和 `duration_ms`。命令时间字段必须是 RFC3339Nano，`completed_at` 不能早于 `started_at`，`duration_ms` 必须非负；可选的 command `error` 会在 PR body 中展示。当 evidence status 是 `succeeded` 时，所有 command 的 `exit_code` 都必须是 `0` 且 `error` 必须为空；当 status 是 `failed` 时，至少一条 command 的 `exit_code` 必须非 0，或至少一条 command 带有非空 `error`。校验通过后，它会写入 `prs/executor-<stage>-evidence-pr.md`，并在 PR body 里渲染命令 ID、exit code、command error、时间戳、耗时和压缩后的输出摘要表。`create-executor-evidence-pr` 默认 dry-run，会拒绝内容已经不匹配当前 evidence 的陈旧 PR body，并只打印 `git switch`、`git add`、`git commit`、`git push` 和 `gh pr create` 命令；只有加 `--execute` 才会修改本地 git checkout、推送分支并调用 GitHub CLI。它不会 merge PR、approve PR、绕过 branch protection，也不会判断 executor 输出是否业务正确。

executor evidence PR 校验会拒绝负数的 `cdc_applied_changes`、`data_rows` 和 `data_bytes`，拒绝不符合 `sha256:<64 hex chars>` 的 `data_sha256`，并要求 `data_sha256` 必须和 `data_rows` / `data_bytes` 一起出现。当 evidence 中存在 `data_rows` / `data_bytes` / `data_sha256` 时，PR body 的命令表会展示 `Data rows`、`Data bytes` 和 `Data SHA256` 列，便于 reviewer 对照 chunk/job 级别的数据量和内容摘要。

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
21. 如果需要连续处理多个已审批 metadata-only action，执行 `worker-reconcile --loop --holder <agent-id> --max-iterations <n>`；正式交付时也可以用 `worker-agent --holder <agent-id>` 作为本地/容器进程入口。loop 会在同一 payload 已有 state 后自动停止重复执行该 stage。
22. 执行 `create-worker-state-pr` dry-run 检查 git/gh 命令，再按需执行 `create-worker-state-pr --execute` 创建 state/evidence 写回 PR。
23. 对 ddl/export/import/cdc/validation 执行 `worker-executor` dry-run，检查外部执行器命令和当前 plan 是否一致。
24. 按需执行 `worker-executor --execute`，生成 `evidence/executor-<stage>-run.json`。
25. 对 executor-only evidence 执行 `generate-executor-evidence-pr-draft` 和 `create-executor-evidence-pr` dry-run，再按需执行 `create-executor-evidence-pr --execute` 创建 evidence PR。
26. approval 通过后执行 `worker-validate`，生成 validation state 和 evidence。
27. 将 `sqlserver2tidb-executor export/import` 从 CSV 路径继续扩展到更多生产导入引擎，把 validation 从 exact-numeric / bucketed-count scalar-query 草稿扩展到通用行级 digest 和生产级分桶 sampled-hash 策略。
28. 对 cutover 后的真实业务流量切换、监控观察和回滚自动化补充外部系统集成。
