package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)

type ClusterSpec struct {
	ClusterID              string
	DisplayName            string
	Listener               string
	Port                   int
	SecretRef              string
	CDCMode                string
	RetentionHoursRequired int
	Owners                 []string
}

type ProjectSpec struct {
	SourceClusterID string
	ProjectID       string
	DisplayName     string
	SourceDatabase  string
	SourceSchemas   []string
	TargetName      string
	TargetDatabase  string
	TargetSecretRef string
	Mode            string
	Owners          []string
}

func InitRepo(root string) error {
	if err := ensureDirs(root,
		"global/policies",
		"global/schemas",
		"global/templates",
		"clusters",
	); err != nil {
		return err
	}

	files := map[string]string{
		"global/policies/approval-policy.yaml": `version: 1
required_reviews:
  ddl: 2
  export: 1
  import: 1
  cdc: 2
  cutover: 2
`,
		"global/policies/execution-policy.yaml": `version: 1
worker:
  execute_only_merged_instructions: true
  require_approval_file: true
  require_idempotent_steps: true
`,
		"global/policies/file-schema-policy.yaml": `version: 1
schemas:
  cluster: global/schemas/cluster.schema.json
  project: global/schemas/project.schema.json
  migration_plan: global/schemas/migration-plan.schema.json
  export_plan: global/schemas/export-plan.schema.json
  import_plan: global/schemas/import-plan.schema.json
  cdc_plan: global/schemas/cdc-plan.schema.json
  validation_plan: global/schemas/validation-plan.schema.json
`,
		"global/templates/project.yaml": `project_id: example-project
source_cluster_id: example-sqlserver-cluster
mode: short-downtime
status: planning
`,
		"global/templates/migration-plan.yaml": `plan_version: 1
mode: short-downtime
approval_required:
  - ddl
  - export
  - import
  - cdc
  - cutover
`,
		"global/templates/cutover-runbook.md": `# Cutover Runbook

## Preconditions

- Full import completed.
- CDC checkpoint caught up.
- Validation passed.
- Cutover PR approved.
`,
		"global/schemas/cluster.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SQL Server source cluster",
  "type": "object",
  "required": ["cluster_id", "source", "cdc", "owners"],
  "properties": {
    "cluster_id": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$"},
    "display_name": {"type": "string"},
    "source": {
      "type": "object",
      "required": ["type", "listener", "port", "secret_ref"],
      "properties": {
        "type": {"const": "sqlserver"},
        "host_group": {"type": "string"},
        "listener": {"type": "string"},
        "port": {"type": "integer", "minimum": 1, "maximum": 65535},
        "secret_ref": {"type": "string"}
      }
    },
    "cdc": {
      "type": "object",
      "required": ["mode", "retention_hours_required"],
      "properties": {
        "mode": {"type": "string"},
        "retention_hours_required": {"type": "integer", "minimum": 1}
      }
    },
    "owners": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}}
  }
}
`,
		"global/schemas/project.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SQL Server to TiDB migration project",
  "type": "object",
  "required": ["project_id", "source_cluster_id", "source", "target", "mode", "owners"],
  "properties": {
    "project_id": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$"},
    "source_cluster_id": {"type": "string"},
    "source": {
      "type": "object",
      "required": ["type", "database", "schemas"],
      "properties": {
        "type": {"const": "sqlserver"},
        "database": {"type": "string"},
        "schemas": {"type": "array", "items": {"type": "string"}}
      }
    },
    "target": {
      "type": "object",
      "required": ["type", "name", "database", "secret_ref"],
      "properties": {
        "type": {"const": "tidb"},
        "name": {"type": "string"},
        "database": {"type": "string"},
        "secret_ref": {"type": "string"}
      }
    },
    "mode": {"enum": ["offline", "short-downtime", "low-downtime"]},
    "owners": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}}
  }
}
`,
		"global/schemas/migration-plan.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Migration plan",
  "type": "object",
  "required": ["plan_version", "project_id", "source_cluster_id", "mode", "approval_required"],
  "properties": {
    "plan_version": {"type": "integer", "minimum": 1},
    "project_id": {"type": "string"},
    "source_cluster_id": {"type": "string"},
    "mode": {"enum": ["offline", "short-downtime", "low-downtime"]},
    "approval_required": {
      "type": "array",
      "items": {"enum": ["ddl", "export", "import", "cdc", "cutover"]}
    }
  }
}
`,
		"global/schemas/export-plan.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Full export plan",
  "type": "object",
  "required": ["status"],
  "properties": {
    "status": {"enum": ["draft", "reviewed", "approved"]},
    "project_id": {"type": "string"},
    "source_cluster_id": {"type": "string"},
    "format": {"type": "string"},
    "null_encoding": {"enum": ["bitmap", "backslash-n"]},
    "compression": {"enum": ["none", "gzip"]},
    "object_uri_prefix": {"type": "string"},
    "chunk_size_rows": {"type": "integer", "minimum": 1},
    "tables": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["source_object", "target_object", "chunks"],
        "properties": {
          "source_object": {"type": "string", "minLength": 1},
          "target_object": {"type": "string", "minLength": 1},
          "row_count_estimate": {"type": "integer", "minimum": 0},
          "chunks": {
            "type": "array",
            "items": {
              "type": "object",
              "required": ["id", "output_uri"],
              "properties": {
                "id": {"type": "string", "minLength": 1},
                "estimated_rows": {"type": "integer", "minimum": 0},
                "predicate": {"type": "string"},
                "output_uri": {"type": "string", "minLength": 1}
              },
              "additionalProperties": true
            }
          }
        },
        "additionalProperties": true
      }
    },
    "chunks": {"type": "array"}
  },
  "additionalProperties": true
}
`,
		"global/schemas/import-plan.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Full import plan",
  "type": "object",
  "required": ["status"],
  "properties": {
    "status": {"enum": ["draft", "reviewed", "approved"]},
    "project_id": {"type": "string"},
    "source_cluster_id": {"type": "string"},
    "engine": {"enum": ["sql-insert", "tidb-import-into", "import-into", "tidb-lightning", "lightning"]},
    "data_source_uri": {"type": "string"},
    "compression": {"enum": ["none", "gzip"]},
    "mode": {"type": "string"},
    "jobs": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "target_object", "source_uri", "depends_on_export_chunk"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "target_object": {"type": "string", "minLength": 1},
          "source_uri": {"type": "string", "minLength": 1},
          "depends_on_export_chunk": {"type": "string", "minLength": 1},
          "fields": {
            "type": "array",
            "items": {"type": "string", "minLength": 1}
          }
        },
        "additionalProperties": true
      }
    }
  },
  "additionalProperties": true
}
`,
		"global/schemas/cdc-plan.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "CDC plan",
  "type": "object",
  "required": ["status", "mode"],
  "properties": {
    "status": {"enum": ["draft", "reviewed", "approved"]},
    "project_id": {"type": "string"},
    "source_cluster_id": {"type": "string"},
    "mode": {"type": "string"},
    "retention_hours_required": {"type": "integer", "minimum": 1},
    "source_database": {"type": "string"},
    "source_schemas": {"type": "array", "items": {"type": "string"}},
    "target_database": {"type": "string"},
    "checkpoint_scope": {"type": "string"},
    "checkpoint_file": {"type": "string"},
    "tracked_tables": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["source_object", "target_object", "apply_batch_size"],
        "properties": {
          "source_object": {"type": "string", "minLength": 1},
          "target_object": {"type": "string", "minLength": 1},
          "apply_batch_size": {"type": "integer", "minimum": 1},
          "status": {"type": "string"}
        },
        "additionalProperties": true
      }
    }
  },
  "additionalProperties": true
}
`,
		"global/schemas/validation-plan.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Validation plan",
  "type": "object",
  "required": ["status", "checks"],
  "properties": {
    "status": {"enum": ["draft", "reviewed", "approved"]},
    "checks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "type"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "type": {"enum": ["row_count", "row-count", "checksum", "sampled_hash", "bucketed_count", "business_sql"]},
          "source_object": {"type": "string", "minLength": 1},
          "target_object": {"type": "string", "minLength": 1},
          "source_sql": {"type": "string", "minLength": 1},
          "target_sql": {"type": "string", "minLength": 1},
          "predicate": {"type": "string"},
          "target_predicate": {"type": "string"},
          "description": {"type": "string"}
        },
        "allOf": [
          {
            "if": {"properties": {"type": {"enum": ["row_count", "row-count"]}}},
            "then": {"required": ["source_object", "target_object"]}
          },
          {
            "if": {"properties": {"type": {"enum": ["checksum", "sampled_hash", "bucketed_count", "business_sql"]}}},
            "then": {"required": ["source_sql", "target_sql"]}
          }
        ],
        "additionalProperties": true
      }
    }
  },
  "additionalProperties": true
}
`,
		"clusters/.gitkeep": "",
	}

	for rel, content := range files {
		if err := writeFile(root, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func CreateCluster(root string, spec ClusterSpec) error {
	if err := validateCluster(spec); err != nil {
		return err
	}

	base := filepath.Join("clusters", spec.ClusterID)
	if _, err := os.Stat(filepath.Join(root, base)); err == nil {
		return fmt.Errorf("source cluster %q already exists", spec.ClusterID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check source cluster %q: %w", spec.ClusterID, err)
	}
	if err := ensureDirs(root,
		base,
		filepath.Join(base, "inventory", "source-ddl"),
		filepath.Join(base, "state"),
		filepath.Join(base, "projects"),
		filepath.Join(base, "prs"),
	); err != nil {
		return err
	}

	now := nowUTC()
	files := map[string]string{
		filepath.Join(base, "cluster.yaml"): fmt.Sprintf(`cluster_id: %s
display_name: "%s"
source:
  type: sqlserver
  host_group: %s
  listener: %s
  port: %d
  secret_ref: %s
cdc:
  mode: %s
  retention_hours_required: %d
owners:
%screated_at: "%s"
updated_at: "%s"
`,
			spec.ClusterID,
			escapeDoubleQuoted(spec.DisplayName),
			spec.ClusterID,
			spec.Listener,
			spec.Port,
			spec.SecretRef,
			spec.CDCMode,
			spec.RetentionHoursRequired,
			yamlList(spec.Owners, "  "),
			now,
			now,
		),
		filepath.Join(base, "source-profile.yaml"): fmt.Sprintf(`cluster_id: %s
connection:
  listener: %s
  port: %d
  secret_ref: %s
`, spec.ClusterID, spec.Listener, spec.Port, spec.SecretRef),
		filepath.Join(base, "inventory", "inventory.json"): `{
  "status": "pending",
  "databases": []
}
`,
		filepath.Join(base, "inventory", "compatibility-report.md"): "# Compatibility Report\n\nPending discovery.\n",
		filepath.Join(base, "inventory", "schema-issues.yaml"):      "issues: []\n",
		filepath.Join(base, "state", "cdc-checkpoint.yaml"): fmt.Sprintf(`source_cluster_id: %s
capture_mode: %s
status: not_started
checkpoints: []
updated_at: "%s"
`, spec.ClusterID, spec.CDCMode, now),
		filepath.Join(base, "state", "worker-lease.yaml"): fmt.Sprintf(`source_cluster_id: %s
holder: ""
lease_id: ""
phase: idle
expires_at: ""
renewed_at: "%s"
`, spec.ClusterID, now),
	}

	for rel, content := range files {
		if err := writeFile(root, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func CreateProject(root string, spec ProjectSpec) error {
	if err := validateProject(spec); err != nil {
		return err
	}

	clusterDir := filepath.Join(root, "clusters", spec.SourceClusterID)
	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return fmt.Errorf("source cluster %q does not exist", spec.SourceClusterID)
	}

	base := filepath.Join("clusters", spec.SourceClusterID, "projects", spec.ProjectID)
	if _, err := os.Stat(filepath.Join(root, base)); err == nil {
		return fmt.Errorf("migration project %q already exists", spec.ProjectID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check migration project %q: %w", spec.ProjectID, err)
	}
	if err := ensureDirs(root,
		base,
		filepath.Join(base, "schema", "tidb-ddl"),
		filepath.Join(base, "plan"),
		filepath.Join(base, "state"),
		filepath.Join(base, "evidence"),
		filepath.Join(base, "approvals"),
		filepath.Join(base, "prs"),
	); err != nil {
		return err
	}

	now := nowUTC()
	files := map[string]string{
		filepath.Join(base, "project.yaml"): fmt.Sprintf(`project_id: %s
display_name: "%s"
source_cluster_id: %s
source:
  type: sqlserver
  database: %s
  schemas:
%starget:
  type: tidb
  name: %s
  database: %s
  secret_ref: %s
mode: %s
status: planning
owners:
%screated_at: "%s"
updated_at: "%s"
`,
			spec.ProjectID,
			escapeDoubleQuoted(spec.DisplayName),
			spec.SourceClusterID,
			spec.SourceDatabase,
			yamlList(spec.SourceSchemas, "    "),
			spec.TargetName,
			spec.TargetDatabase,
			spec.TargetSecretRef,
			spec.Mode,
			yamlList(spec.Owners, "  "),
			now,
			now,
		),
		filepath.Join(base, "schema", "conversion-report.md"): "# Schema Conversion Report\n\nPending conversion.\n",
		filepath.Join(base, "schema", "schema-diff.json"):     "{\n  \"status\": \"pending\",\n  \"diffs\": []\n}\n",
		filepath.Join(base, "plan", "migration-plan.yaml"): fmt.Sprintf(`plan_version: 1
project_id: %s
source_cluster_id: %s
mode: %s
source_snapshot:
  inventory_file: ../../../inventory/inventory.json
  compatibility_report: ../../../inventory/compatibility-report.md
schema:
  ddl_dir: schema/tidb-ddl
  conversion_report: schema/conversion-report.md
full_export:
  plan_file: plan/export-plan.yaml
  format: csv
full_import:
  plan_file: plan/import-plan.yaml
  engine: sql-insert
incremental:
  plan_file: plan/cdc-plan.yaml
  mode: sqlserver-cdc
validation:
  plan_file: plan/validation-plan.yaml
cutover:
  runbook: plan/cutover-runbook.md
approval_required:
  - ddl
  - export
  - import
  - cdc
  - cutover
`, spec.ProjectID, spec.SourceClusterID, spec.Mode),
		filepath.Join(base, "plan", "export-plan.yaml"):     "status: draft\nchunks: []\n",
		filepath.Join(base, "plan", "import-plan.yaml"):     "status: draft\njobs: []\n",
		filepath.Join(base, "plan", "cdc-plan.yaml"):        "status: draft\nmode: sqlserver-cdc\n",
		filepath.Join(base, "plan", "validation-plan.yaml"): "status: draft\nchecks: []\n",
		filepath.Join(base, "plan", "cutover-runbook.md"):   "# Cutover Runbook\n\nPending plan review.\n",
		filepath.Join(base, "state", "migration-state.yaml"): fmt.Sprintf(`project_id: %s
source_cluster_id: %s
phase: planning
status: not_started
updated_at: "%s"
`, spec.ProjectID, spec.SourceClusterID, now),
		filepath.Join(base, "state", "export-chunks.yaml"): fmt.Sprintf("project_id: %s\nsource_cluster_id: %s\nchunks: []\n", spec.ProjectID, spec.SourceClusterID),
		filepath.Join(base, "state", "import-jobs.yaml"):   fmt.Sprintf("project_id: %s\nsource_cluster_id: %s\njobs: []\n", spec.ProjectID, spec.SourceClusterID),
		filepath.Join(base, "state", "validation-status.yaml"): fmt.Sprintf(`project_id: %s
source_cluster_id: %s
status: pending
checks: []
`, spec.ProjectID, spec.SourceClusterID),
		filepath.Join(base, "evidence", "precheck.json"):             "{\n  \"status\": \"pending\"\n}\n",
		filepath.Join(base, "evidence", "import-summary.json"):       "{\n  \"status\": \"pending\"\n}\n",
		filepath.Join(base, "evidence", "cdc-catchup.json"):          "{\n  \"status\": \"pending\"\n}\n",
		filepath.Join(base, "evidence", "validation-report.md"):      "# Validation Report\n\nPending validation.\n",
		filepath.Join(base, "evidence", "cutover-evidence.md"):       "# Cutover Evidence\n\nPending cutover.\n",
		filepath.Join(base, "evidence", "post-cutover-report.md"):    "# Post-Cutover Report\n\nPending stabilization.\n",
		filepath.Join(base, "approvals", "ddl-approval.yaml"):        approvalSkeleton(spec, "ddl"),
		filepath.Join(base, "approvals", "export-approval.yaml"):     approvalSkeleton(spec, "export"),
		filepath.Join(base, "approvals", "import-approval.yaml"):     approvalSkeleton(spec, "import"),
		filepath.Join(base, "approvals", "cdc-approval.yaml"):        approvalSkeleton(spec, "cdc"),
		filepath.Join(base, "approvals", "validation-approval.yaml"): approvalSkeleton(spec, "validation"),
		filepath.Join(base, "approvals", "cutover-approval.yaml"):    approvalSkeleton(spec, "cutover"),
	}

	for rel, content := range files {
		if err := writeFile(root, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func validateCluster(spec ClusterSpec) error {
	if !idPattern.MatchString(spec.ClusterID) {
		return fmt.Errorf("invalid cluster id %q", spec.ClusterID)
	}
	if spec.DisplayName == "" || spec.Listener == "" || spec.SecretRef == "" {
		return errors.New("cluster display name, listener, and secret ref are required")
	}
	if spec.Port <= 0 || spec.Port > 65535 {
		return fmt.Errorf("invalid SQL Server port %d", spec.Port)
	}
	if spec.CDCMode == "" {
		return errors.New("cdc mode is required")
	}
	if spec.RetentionHoursRequired <= 0 {
		return errors.New("cdc retention hours must be positive")
	}
	if err := validateOwners("cluster", spec.Owners); err != nil {
		return err
	}
	return nil
}

func validateProject(spec ProjectSpec) error {
	if !idPattern.MatchString(spec.SourceClusterID) {
		return fmt.Errorf("invalid source cluster id %q", spec.SourceClusterID)
	}
	if !idPattern.MatchString(spec.ProjectID) {
		return fmt.Errorf("invalid project id %q", spec.ProjectID)
	}
	if spec.DisplayName == "" || spec.SourceDatabase == "" || spec.TargetName == "" || spec.TargetDatabase == "" || spec.TargetSecretRef == "" {
		return errors.New("project display name, source database, target name, target database, and target secret ref are required")
	}
	if len(spec.SourceSchemas) == 0 {
		return errors.New("at least one source schema is required")
	}
	if spec.Mode == "" {
		return errors.New("migration mode is required")
	}
	if !isSupportedMigrationMode(spec.Mode) {
		return fmt.Errorf("unsupported migration mode %q; supported modes: offline, short-downtime, low-downtime", spec.Mode)
	}
	if err := validateOwners("project", spec.Owners); err != nil {
		return err
	}
	return nil
}

func isSupportedMigrationMode(mode string) bool {
	switch mode {
	case "offline", "short-downtime", "low-downtime":
		return true
	default:
		return false
	}
}

func validateOwners(kind string, owners []string) error {
	if len(owners) == 0 {
		return fmt.Errorf("at least one %s owner is required", kind)
	}
	for _, owner := range owners {
		if strings.TrimSpace(owner) == "" {
			return fmt.Errorf("%s owner must not be empty", kind)
		}
	}
	return nil
}

func ensureDirs(root string, rels ...string) error {
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(root, rel, content string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func yamlList(values []string, indent string) string {
	if len(values) == 0 {
		return indent + "[]\n"
	}
	var b strings.Builder
	for _, value := range values {
		b.WriteString(indent)
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return b.String()
}

func approvalSkeleton(spec ProjectSpec, action string) string {
	return fmt.Sprintf(`approval_id: ""
project_id: %s
source_cluster_id: %s
action: %s
payload_hash: ""
required_reviewers: []
approved_by: []
status: pending
approved_at: ""
`, spec.ProjectID, spec.SourceClusterID, action)
}

func escapeDoubleQuoted(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
