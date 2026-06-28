# Worker Agent Deployment Example

This directory contains deployment-oriented examples for running the metadata-only `sqlserver2tidb worker-agent` entrypoint.

The agent reads and writes a mounted migration metadata repository. It does not execute DDL, call the external executor, create GitHub PRs, or connect to SQL Server/TiDB/object storage. Use `create-worker-state-pr` outside the agent process to turn generated state PR drafts into GitHub PRs.

## Docker Compose

Copy `.env.example` to `.env`, adjust `SQLSERVER2TIDB_REPO` and `SQLSERVER2TIDB_HOLDER`, then run:

```bash
docker compose --env-file .env -f docker-compose.yaml up
```

`SQLSERVER2TIDB_REPO` must point at the checked-out migration metadata repository on the host. The container mounts it at `/workspace`.

## systemd

Copy `systemd/sqlserver2tidb-worker-agent.service` to `/etc/systemd/system/`, edit the `Environment=` values for the local metadata repository and holder, then run:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sqlserver2tidb-worker-agent
```

The service assumes `sqlserver2tidb` is installed at `/usr/local/bin/sqlserver2tidb` and the service user can read and write the metadata repository.
