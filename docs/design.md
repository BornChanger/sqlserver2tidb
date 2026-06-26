# Design Notes

## Control Plane

The migration control plane is GitHub-based. This repository stores low-frequency durable state:

- source cluster profile
- inventory snapshots
- schema conversion output
- migration plans
- export/import state
- CDC checkpoint snapshots
- validation evidence
- approval files

GitHub PRs are used for review and approval.

## Metadata Boundary

Metadata is organized by upstream SQL Server cluster:

```text
clusters/<source_cluster_id>/
```

This is the right boundary for source inventory, SQL Server CDC, source permissions, and worker leases.

Migration projects live below the source cluster:

```text
clusters/<source_cluster_id>/projects/<project_id>/
```

A project represents one independently planned, approved, validated, and cut-over migration unit.

## LLM Boundary

LLMs may generate:

- compatibility explanations
- schema rewrite candidates
- migration plan drafts
- PR descriptions
- validation report narratives
- incident diagnosis suggestions

LLMs must not decide:

- whether DDL can run
- whether import can start
- whether CDC has caught up
- whether validation passed
- whether cutover can proceed
- whether destructive cleanup can run

Those gates are handled by deterministic rules, observed runtime state, and GitHub approvals.
