# Migration Agent Runtime Templates

This directory contains deployment-oriented templates for running the top-level
`sqlserver2tidb agent` orchestration entrypoint.

The templates are intentionally conservative:

- Default mode is `status`.
- PR creation requires an explicit `execute_pr` / `SQLSERVER2TIDB_EXECUTE_PR`
  switch.
- Database execution requires an explicit `execute` /
  `SQLSERVER2TIDB_EXECUTE` switch plus reviewed approvals in the metadata
  repository.
- LLM provider calls require an explicit `execute_llm` /
  `SQLSERVER2TIDB_EXECUTE_LLM` switch.
- Secrets are read from runtime environment variables or platform secrets, not
  from committed files.

## Templates

- `github-actions/migration-agent.yml`: manual `workflow_dispatch` runner for
  `agent status`, bounded `agent auto`, PR draft/creation, approved execution,
  CDC operations, and LLM review assist.
- `kubernetes/migration-agent-cronjob.yaml`: CronJob skeleton for periodic
  status or CDC health/orchestration checks from a mounted metadata repository.
- `systemd/sqlserver2tidb-agent-status.service` and
  `systemd/sqlserver2tidb-agent-status.timer`: host-level periodic status check.
- `systemd/sqlserver2tidb-agent-cdc-ops.service` and
  `systemd/sqlserver2tidb-agent-cdc-ops.timer`: host-level periodic CDC
  health/orchestration check.

## Required Runtime Inputs

Every runtime needs a checked-out migration metadata repository. The repository
must already be initialized with:

```bash
sqlserver2tidb init-repo --root .
sqlserver2tidb validate-repo --root .
```

For GitHub automation, provide a token through `GH_TOKEN` or
`SQLSERVER2TIDB_GITHUB_APP_TOKEN`. For executor-backed actions, provide only
environment variable names to the agent flags and keep actual connection strings
in the platform secret store:

```text
SQLSERVER2TIDB_SOURCE_CONNECTION_STRING=...
SQLSERVER2TIDB_TARGET_CONNECTION_STRING=...
SQLSERVER2TIDB_FEISHU_WEBHOOK=...
SQLSERVER2TIDB_FEISHU_SECRET=...
SQLSERVER2TIDB_SLACK_WEBHOOK=...
OPENAI_API_KEY=...
```

Review `global/policies/agent-policy.yaml` before enabling execution in any
shared environment.
