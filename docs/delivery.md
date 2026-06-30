# Delivery Guide

This document describes how to deliver `sqlserver2tidb` so another team can install it, configure it, and operate the GitOps migration agent without relying on this source checkout.

## Delivery Artifacts

Supported delivery artifacts are:

- Release archives from GitHub Releases, containing `sqlserver2tidb`, `sqlserver2tidb-executor`, docs, scripts, examples, and integration-test assets.
- Container images published to `ghcr.io/bornchanger/sqlserver2tidb:<version>` and `ghcr.io/bornchanger/sqlserver2tidb:latest`.
- A metadata repository initialized by `sqlserver2tidb init-repo`, where every source SQL Server cluster owns one directory under `clusters/<source_cluster_id>/`.
- Production readiness and operations material under `docs/production-operations.md`.
- Optional top-level migration agent runtime templates under `examples/agent-runtime/`.
- Optional worker-agent deployment examples under `examples/worker-agent/`.
- Optional LLM provider configuration examples under `examples/llm-providers.yaml`.

The release archive and container image intentionally do not embed credentials. SQL Server, TiDB, GitHub, Feishu, Slack, object storage, and LLM credentials must be supplied by the runtime environment.

## Install From Release Archive

Download the archive for the target platform from GitHub Releases, then verify `checksums.txt`:

```bash
shasum -a 256 -c checksums.txt
tar -xzf sqlserver2tidb_<version>_<goos>_<goarch>.tar.gz
```

Install both binaries somewhere on `PATH`:

```bash
install -m 0755 sqlserver2tidb_<version>_<goos>_<goarch>/sqlserver2tidb /usr/local/bin/sqlserver2tidb
install -m 0755 sqlserver2tidb_<version>_<goos>_<goarch>/sqlserver2tidb-executor /usr/local/bin/sqlserver2tidb-executor
```

Verify the installed version:

```bash
sqlserver2tidb version
sqlserver2tidb-executor version
```

## Bootstrap A Metadata Repository

Create or clone the migration metadata repository, then initialize it:

```bash
sqlserver2tidb init-repo --root .
sqlserver2tidb doctor --root .
```

Create one source-cluster directory per upstream SQL Server cluster:

```bash
sqlserver2tidb create-cluster \
  --root . \
  --cluster-id prod-sqlserver-a \
  --display-name "prod SQL Server A" \
  --listener sqlserver-a.internal \
  --secret-ref vault://migration/prod-sqlserver-a/readonly \
  --owner dba-team
```

Create one or more migration projects under that source cluster:

```bash
sqlserver2tidb create-project \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --source-database sales \
  --source-schema dbo \
  --target-name tidb-prod-a \
  --target-database app \
  --target-secret-ref vault://migration/tidb-prod-a/migrate-user \
  --owner dba-team,app-team
```

## Run With The Container Image

Run one-off commands against a mounted metadata repository:

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  ghcr.io/bornchanger/sqlserver2tidb:<version> doctor --root /workspace
```

Run the metadata-only worker agent with Docker Compose:

```bash
cd examples/worker-agent
cp .env.example .env
docker compose --env-file .env -f docker-compose.yaml up
```

The container image includes `git`, `gh`, `sqlserver2tidb`, and `sqlserver2tidb-executor`. With a mounted metadata repository plus `GH_TOKEN` or a mounted GitHub CLI auth config, the same image can run PR creation, approval sync, merge, worker state PR, and executor evidence PR automation.

## Run The Migration Agent

Runtime templates for the top-level orchestration agent are included under
`examples/agent-runtime/`:

```text
examples/agent-runtime/
  github-actions/migration-agent.yml
  kubernetes/migration-agent-cronjob.yaml
  systemd/sqlserver2tidb-agent-status.service
  systemd/sqlserver2tidb-agent-status.timer
  systemd/sqlserver2tidb-agent-cdc-ops.service
  systemd/sqlserver2tidb-agent-cdc-ops.timer
```

Use the GitHub Actions template for manually triggered repository operations
such as `agent status`, bounded `agent auto`, PR draft/creation, approved
execution, CDC operations, and LLM review assist. Copy it into
`.github/workflows/migration-agent.yml` in the metadata repository and adjust
the workflow inputs and environment protection rules before enabling execution.

Use the Kubernetes CronJob template when a platform team owns a mounted
metadata-repository volume and wants periodic status or CDC health checks from
the published container image. Use the systemd timers when the metadata
repository lives on a long-running host.

All templates default to safe read-only/status behavior. PR creation, database
execution, and LLM provider calls require explicit inputs or environment
variables and remain subject to `global/policies/agent-policy.yaml` plus the
normal approval/hash gates.

## GitHub Permissions

For PR creation and closure commands, provide a GitHub identity either by authenticating GitHub CLI on the operator host or by passing `GH_TOKEN` into the container:

```bash
gh auth login
sqlserver2tidb doctor --root . --require-tools

docker run --rm \
  -e GH_TOKEN \
  -v "$PWD:/workspace" \
  ghcr.io/bornchanger/sqlserver2tidb:<version> doctor --root /workspace --require-tools
```

For GitHub Actions PR closure and CDC health history commits, configure `SQLSERVER2TIDB_GITHUB_APP_TOKEN` when repository branch protection prevents the default `GITHUB_TOKEN` from pushing or approving. The token should have only the repository permissions required by the workflow: contents read/write, pull requests read/write, checks read, and metadata read.

## LLM Provider Configuration

Copy the provider template into the metadata repository:

```bash
mkdir -p global
cp examples/llm-providers.yaml global/llm-providers.yaml
```

Choose one provider as `default_provider` and set only environment variable names in the file. Put real API keys, OAuth client secrets, refresh tokens, or access tokens in the runtime environment, not in Git.

LLM commands are dry-run by default. Use `--execute` only when the provider configuration and redaction boundary have been reviewed:

```bash
sqlserver2tidb llm-compatibility-advice \
  --root . \
  --source-cluster-id prod-sqlserver-a \
  --provider-config global/llm-providers.yaml \
  --execute
```

LLM output is advisory only. It is written under `ai/` and is not read as approval, worker state, plan input, evidence, or executor instruction.

## Runtime Secrets

Recommended environment variable pattern:

```text
SQLSERVER2TIDB_SOURCE_CONNECTION_STRING=...
SQLSERVER2TIDB_TARGET_CONNECTION_STRING=...
SQLSERVER2TIDB_FEISHU_WEBHOOK=...
SQLSERVER2TIDB_FEISHU_SECRET=...
SQLSERVER2TIDB_SLACK_WEBHOOK=...
OPENAI_API_KEY=...
```

Keep plaintext connection strings and tokens out of the metadata repository. The CLI and executor redact common secret patterns before writing logs, evidence, and LLM advisory files, but runtime secret hygiene still belongs to the deployment environment.

## Delivery Verification

Before handing the package to another team, run:

```bash
make ci
make dist-check
```

For a real environment, run the optional integration check after SQL Server and TiDB DSNs are available:

```bash
SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN=... \
SQLSERVER2TIDB_INTEGRATION_TARGET_DSN=... \
scripts/run-integration-tests.sh
```

For CDC production validation, configure repository or environment secrets and run `.github/workflows/cdc-soak.yml` manually.

Before production rollout, complete the checklist in `docs/production-operations.md`, including GitHub permission review, agent policy review, alert validation, CDC history configuration, rollback plan, and Go/No-Go sign-off.

## Upgrade And Rollback

Upgrade by replacing both binaries or changing the container image tag. Keep the metadata repository in Git so rollback is a normal Git operation:

```bash
git log --oneline
git revert <bad_metadata_commit>
```

Do not downgrade binaries across metadata format changes without running `sqlserver2tidb validate-repo --root .` and reviewing release notes.
