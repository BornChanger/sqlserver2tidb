# SQL Server to TiDB Migration Agent 生产上线运维手册

本文档面向接收 `sqlserver2tidb` 的 DBA、SRE、平台工程和应用团队，描述编排用 migration agent 的上线准入、日常运维、告警处理、回滚和交接要求。

## 1. 运维边界

`sqlserver2tidb agent` 是 GitOps 编排入口。它负责读取迁移控制仓库、展示状态、生成 PR 草稿、触发已审批动作、关闭 PR、运行 CDC 运维检查和调用 LLM advisory。它不绕过以下边界：

- 关键迁移状态以 GitHub 文件为准，不依赖 TiDB 元数据表。
- 真实执行必须显式传入 `--execute`，并且必须通过 approval/hash/status gate。
- PR 创建、审批同步、merge 必须显式传入执行类开关或由受控 workflow 触发。
- LLM 输出只作为 advisory，不作为 approval、worker state、plan input、evidence 或 executor instruction。
- 密钥只来自运行环境变量、GitHub Secrets、Kubernetes Secret 或主机 secret store，不能提交到迁移控制仓库。

## 2. 角色和职责

| 角色 | 职责 |
| --- | --- |
| Migration Owner | 负责迁移窗口、业务确认、上线/回退决策。 |
| DBA | 负责 SQL Server CDC、TiDB 目标库、DDL/数据校验、性能风险。 |
| SRE/Platform | 负责 runner、容器、Kubernetes/systemd、告警、日志、GitHub token。 |
| App Owner | 负责业务停写、读写切换、应用验证和回退确认。 |
| Reviewer | 审核 schema/export/import/cdc/validation/cutover PR 和 evidence PR。 |

## 3. 上线准入 Checklist

上线前必须逐项确认：

- [ ] 已选择固定版本的 release archive 或 container image tag，禁止使用未确认的本地构建。
- [ ] `sqlserver2tidb version` 和 `sqlserver2tidb-executor version` 已记录到上线单。
- [ ] 迁移控制仓库已执行 `sqlserver2tidb init-repo --root .`。
- [ ] `sqlserver2tidb validate-repo --root .` 通过。
- [ ] `sqlserver2tidb doctor --root . --require-tools` 在运行环境通过。
- [ ] `global/policies/agent-policy.yaml` 已审阅，生产环境的执行开关符合变更策略。
- [ ] 每个上游 SQL Server 集群有独立 `clusters/<source_cluster_id>/` 目录。
- [ ] 每个迁移项目有明确 owner、目标库、source schemas 和 secret refs。
- [ ] GitHub branch protection、required reviews、required checks 已启用。
- [ ] GitHub token 权限最小化，至少覆盖实际需要的 contents / pull-requests / checks / statuses 权限。
- [ ] SQL Server 账号权限已验证：读取 schema、导出数据、CDC enable/LSN 查询/CDC 读取按迁移阶段授权。
- [ ] TiDB 账号权限已验证：建库建表、导入、校验查询、必要时执行 Lightning/IMPORT INTO。
- [ ] 对象存储 bucket/container 路径、生命周期、加密、跨区带宽和清理策略已确认。
- [ ] Feishu/Slack 告警 webhook 已配置在 secret store，且测试消息可达。
- [ ] `agent status --json` 输出已被日志系统或 workflow artifact 保存。
- [ ] `cdc-health --history-file` 或 `agent --mode cdc-ops --history-file` 已配置历史文件路径。
- [ ] 真实环境 integration test 或 CDC soak 已至少在预发环境跑通。
- [ ] 应用切流和回退步骤由 App Owner 单独确认；agent 不自动修改业务流量。

## 4. 推荐上线阶段

### T-7d 到 T-3d：环境准备

1. 固定 release 版本或 image tag。
2. 初始化或更新迁移控制仓库。
3. 创建 source cluster 和 migration project。
4. 配置 GitHub Secrets / Kubernetes Secret / systemd EnvironmentFile。
5. 配置 `examples/agent-runtime/` 中的 GitHub Actions、CronJob 或 systemd timer。
6. 执行：

```bash
sqlserver2tidb doctor --root . --require-tools
sqlserver2tidb validate-repo --root .
sqlserver2tidb agent --mode status --root . --source-cluster-id <cluster> --project-id <project>
```

### T-3d 到 T-1d：预演

1. 生成 schema/data/CDC/validation plan。
2. 创建并审核 plan PR。
3. 在预发或隔离环境执行 executor-backed DDL/export/import/validation。
4. 运行 CDC soak 或至少一轮 CDC range apply。
5. 确认 `agent status` 没有未知 blocked reason。
6. 记录最终 cutover runbook 和回退条件。

### T-0：迁移窗口

1. 冻结迁移控制仓库中非本次窗口 PR。
2. 确认 source/target DSN、object storage、GitHub token、alert webhook 可用。
3. 执行 `agent status`，确认 next action 与上线单一致。
4. 按 stage 推进：

```bash
sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id <cluster> --project-id <project> \
  --stage ddl --execute --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING

sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id <cluster> --project-id <project> \
  --stage export --execute --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING

sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id <cluster> --project-id <project> \
  --stage import --execute --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING

sqlserver2tidb agent --mode cdc-ops --root . --source-cluster-id <cluster> --project-id <project> \
  --history-file clusters/<cluster>/projects/<project>/state/cdc-health-history.jsonl \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING

sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id <cluster> --project-id <project> \
  --stage validation --execute --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

5. 每个执行 stage 完成后生成 evidence PR，等待 reviewer 审核。
6. cutover 前必须确认 CDC caught up、validation passed、应用停写/切流窗口已批准。

### T+1 到 T+7：稳定期

1. 保留 `agent status` 和 `cdc-health` 周期运行。
2. 每日检查 PR 队列、blocked action、CDC lag、validation evidence。
3. 清理过期临时导出文件和不再需要的 object storage staging 路径。
4. 输出迁移总结：版本、提交、执行窗口、数据量、evidence PR、异常和处理结论。

## 5. 日常运维命令

查看整体状态：

```bash
sqlserver2tidb agent --mode status --root . --source-cluster-id <cluster> --project-id <project>
sqlserver2tidb agent --mode status --root . --source-cluster-id <cluster> --project-id <project> --json
```

查看下一步安全动作：

```bash
sqlserver2tidb agent --mode auto --dry-run --root . --source-cluster-id <cluster> --project-id <project>
```

生成 PR 草稿：

```bash
sqlserver2tidb agent --mode plan-and-pr --root . --source-cluster-id <cluster> --project-id <project> --stage export
```

执行已审批动作：

```bash
sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id <cluster> --project-id <project> --stage export --execute --use-executor \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

运行 CDC 运维：

```bash
sqlserver2tidb agent --mode cdc-ops --root . --source-cluster-id <cluster> --project-id <project> \
  --history-file clusters/<cluster>/projects/<project>/state/cdc-health-history.jsonl \
  --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING \
  --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
```

触发 LLM advisory：

```bash
sqlserver2tidb agent --mode review-assist --root . --source-cluster-id <cluster> --project-id <project> \
  --stage validation --provider-config global/llm-providers.yaml
```

## 6. 告警和监控

最低要求：

- `agent status` 每 5 分钟运行一次，输出保存到 workflow log、systemd journal 或日志平台。
- `cdc-health` 或 `agent cdc-ops` 每 1 到 5 分钟运行一次，写入 `state/cdc-health-history.jsonl`。
- Feishu/Slack 对 `critical` 级别 CDC health 告警即时通知。
- warning 是否通知取决于迁移窗口阶段：cutover 前建议 warning 也通知。
- GitHub workflow 失败、PR merge 失败、validation failed 都必须进入值班通道。

建议阈值：

| 指标 | warning | critical |
| --- | --- | --- |
| CDC checkpoint age | 大于 15 分钟 | 大于 SQL Server CDC retention 安全窗口 |
| CDC lagging tables | 大于 0 | 大于 0 且接近 cutover |
| validation failed commands | 大于 0 | 大于 0 且影响 cutover 判断 |
| blocked action age | 大于 1 天 | 大于迁移窗口开始时间 |
| GitHub PR pending review | 大于 1 天 | 阻塞 T-0 执行 |

## 7. 常见故障 Runbook

### validate-repo 失败

1. 运行 `sqlserver2tidb validate-repo --root .` 获取完整错误。
2. 确认是否为缺失全局文件、状态文件 owner 不一致、payload hash 不一致、plan status 非法。
3. 不要手工跳过校验；通过 PR 修复 metadata 文件。
4. 修复后重新运行 `agent status`。

### agent status 显示 blocked

1. 查看 blocked action details 和 stage matrix。
2. 检查对应 approval、plan、state、evidence、PR 路径。
3. 如果是审批缺失，创建或合并对应 PR。
4. 如果是 payload hash 不匹配，重新计算并走 review，不要直接改 approval 文件。

### GitHub PR 自动化失败

1. 检查 `GH_TOKEN` 或 `SQLSERVER2TIDB_GITHUB_APP_TOKEN` 是否存在。
2. 检查 workflow permissions 是否包含 contents write、pull-requests write、checks read。
3. 检查 branch protection 是否禁止当前 token push/approve/merge。
4. 使用 dry-run 命令复现：

```bash
sqlserver2tidb agent --mode plan-and-pr --root . --source-cluster-id <cluster> --project-id <project> --stage <stage>
```

### execute-approved 失败

1. 保留 stdout/stderr 和 `evidence/executor-<stage>-run.json`。
2. 确认失败前是否已经产生部分外部副作用。
3. 对支持 resume 的 executor-backed stage，优先使用 `--resume`。
4. 失败 evidence 必须通过 PR 审核，不要覆盖已有 evidence。
5. 如果需要回退 metadata，使用 Git revert；如果需要回退数据库，按 stage-specific rollback 操作执行。

### CDC health warning/critical

1. 查看最新 `state/cdc-health-history.jsonl`。
2. 确认 checkpoint status、max_lsn、lagging_tables、expired_tables。
3. 如果 retention 风险接近 critical，暂停 cutover，增加 CDC retention 或加速 apply。
4. 如果 SQL Server CDC disabled 或 capture instance 缺失，先恢复 SQL Server CDC，再重新运行 cdc-ops。
5. 如果 target apply 失败，检查 executor evidence 和 TiDB 错误，修复后用 approved range 重试。

### validation mismatch

1. 保留 validation evidence。
2. 区分 row_count、checksum、sampled_hash、bucketed_count、business_sql 类型。
3. 先排除时间窗口、未停止写入、CDC 未追平、过滤条件不一致。
4. 对 bucketed_count mismatch，定位 bucket 后追加更细粒度 validation plan。
5. mismatch 未解释前不得进入 cutover。

### LLM provider 失败

1. 确认失败不影响 deterministic gate。
2. 检查 `global/llm-providers.yaml` 只包含环境变量名。
3. 检查 API key/OAuth token/external command 所在运行环境。
4. 需要时跳过 LLM advisory，继续走人工 review。

## 8. 回滚和恢复

Metadata 回滚：

```bash
git log --oneline
git revert <bad_metadata_commit>
sqlserver2tidb validate-repo --root .
```

执行失败恢复：

- DDL：按 schema PR 和 TiDB DDL 状态人工确认是否需要反向 DDL。
- Export：删除未发布或失败的 staging object，重新执行 export。
- Import：如果目标表要求空表，失败后清理目标表再重试；如果支持幂等导入，按 import evidence 判定已完成 job。
- CDC：保留 checkpoint 和 failed evidence；修复后从已审批 LSN range 或 checkpoint 重试。
- Validation：保留 failed report；补充 validation plan 或修复数据后重跑。
- Cutover：agent 不改业务流量。应用侧回切必须按应用团队 runbook 执行。

## 9. 变更管理

任何以下变更必须走 PR review：

- `global/policies/agent-policy.yaml`
- `global/policies/approval-policy.yaml`
- cluster/project metadata
- schema/data/CDC/validation/cutover plan
- approval 文件
- state/evidence 文件
- agent runtime workflow、CronJob、systemd timer

生产环境建议默认 policy：

```yaml
version: 1
allow_execute: false
allow_execute_pr: true
allow_execute_evidence_pr: true
allow_execute_llm: false
max_auto_steps: 1
```

需要在迁移窗口执行时，通过受控 PR 临时打开必要开关，窗口结束后恢复。

## 10. 上线交接材料

每次迁移上线完成后，交接包至少包含：

- release version、commit、image digest。
- 迁移控制仓库 commit 范围。
- source cluster id、project id、owner。
- 所有 schema/export/import/cdc/validation/cutover PR 链接。
- 所有 executor evidence PR 链接。
- `agent status --json` 最终输出。
- `cdc-health-history.jsonl` 稳定期摘要。
- validation report 和 cutover evidence。
- 未关闭风险、遗留任务和回滚保留期限。

## 11. Go/No-Go 决策

Go 条件：

- [ ] `validate-repo` 通过。
- [ ] `agent status` 的 next action 与上线计划一致。
- [ ] 必要 approvals 已 approved，payload hash 匹配。
- [ ] CDC caught up 或满足业务定义的延迟窗口。
- [ ] validation 通过或 mismatch 已被业务书面接受。
- [ ] 告警通道在线。
- [ ] 应用回退方案已确认。

No-Go 条件：

- [ ] CDC retention 已接近或进入不可恢复窗口。
- [ ] validation mismatch 未解释。
- [ ] GitHub approval/hash gate 不一致。
- [ ] 目标库权限或容量风险未消除。
- [ ] 业务方未确认停写/切流/回退窗口。

## 12. 生产试点建议

第一批生产试点建议选择：

- 单 source cluster、单 project。
- 表数量和数据量可控。
- 有明确只读窗口或短暂停写窗口。
- 可以接受人工 cutover。
- 业务方能提供确定的 validation SQL。
- CDC retention 能覆盖至少两倍预估迁移窗口。

试点完成后，再扩大到多 project、多 source cluster 或更长 CDC 周期。
