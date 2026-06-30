package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BornChanger/sqlserver2tidb/internal/buildinfo"
	"github.com/BornChanger/sqlserver2tidb/internal/gitops"
	"github.com/BornChanger/sqlserver2tidb/internal/llm"
	"github.com/BornChanger/sqlserver2tidb/internal/redact"
	sqlservercatalog "github.com/BornChanger/sqlserver2tidb/internal/sqlserver"
)

var lookPath = exec.LookPath

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "init-repo":
		return runInitRepo(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "validate-repo":
		return runValidateRepo(args[1:], stdout, stderr)
	case "discover-sqlserver":
		return runDiscoverSQLServer(args[1:], stdout, stderr)
	case "analyze-compatibility":
		return runAnalyzeCompatibility(args[1:], stdout, stderr)
	case "llm-compatibility-advice":
		return runLLMCompatibilityAdvice(args[1:], stdout, stderr)
	case "llm-schema-advice":
		return runLLMSchemaAdvice(args[1:], stdout, stderr)
	case "llm-migration-strategy":
		return runLLMMigrationStrategy(args[1:], stdout, stderr)
	case "llm-validation-analysis":
		return runLLMProjectAdvice(args[1:], stdout, stderr, llmValidationAnalysisCommand())
	case "llm-cutover-risk":
		return runLLMProjectAdvice(args[1:], stdout, stderr, llmCutoverRiskCommand())
	case "llm-pr-summary":
		return runLLMProjectAdvice(args[1:], stdout, stderr, llmPRSummaryCommand())
	case "generate-schema-draft":
		return runGenerateSchemaDraft(args[1:], stdout, stderr)
	case "generate-data-plans":
		return runGenerateDataPlans(args[1:], stdout, stderr)
	case "repair-schema-drift":
		return runRepairSchemaDrift(args[1:], stdout, stderr)
	case "generate-cdc-plan":
		return runGenerateCDCPlan(args[1:], stdout, stderr)
	case "prepare-cdc-range":
		return runPrepareCDCRange(args[1:], stdout, stderr)
	case "prepare-cdc-iteration":
		return runPrepareCDCIteration(args[1:], stdout, stderr)
	case "cdc-orchestrator":
		return runCDCOrchestrator(args[1:], stdout, stderr)
	case "cdc-health":
		return runCDCHealth(args[1:], stdout, stderr)
	case "agent":
		return runAgent(args[1:], stdout, stderr)
	case "generate-validation-plan":
		return runGenerateValidationPlan(args[1:], stdout, stderr)
	case "generate-pr-draft":
		return runGeneratePRDraft(args[1:], stdout, stderr)
	case "create-pr":
		return runCreatePR(args[1:], stdout, stderr)
	case "sync-github-pr-approval":
		return runSyncGitHubPRApproval(args[1:], stdout, stderr)
	case "complete-github-pr":
		return runCompleteGitHubPR(args[1:], stdout, stderr)
	case "create-worker-state-pr":
		return runCreateWorkerStatePR(args[1:], stdout, stderr)
	case "generate-executor-evidence-pr-draft":
		return runGenerateExecutorEvidencePRDraft(args[1:], stdout, stderr)
	case "create-executor-evidence-pr":
		return runCreateExecutorEvidencePR(args[1:], stdout, stderr)
	case "compute-payload-hash":
		return runComputePayloadHash(args[1:], stdout, stderr)
	case "worker-export":
		return runWorkerExport(args[1:], stdout, stderr)
	case "worker-import":
		return runWorkerImport(args[1:], stdout, stderr)
	case "worker-cdc":
		return runWorkerCDC(args[1:], stdout, stderr)
	case "worker-validate":
		return runWorkerValidate(args[1:], stdout, stderr)
	case "worker-cutover":
		return runWorkerCutover(args[1:], stdout, stderr)
	case "worker-executor":
		return runWorkerExecutor(args[1:], stdout, stderr)
	case "advance-cdc-checkpoint":
		return runAdvanceCDCCheckpoint(args[1:], stdout, stderr)
	case "worker-reconcile":
		return runWorkerReconcile(args[1:], stdout, stderr)
	case "worker-agent":
		return runWorkerAgent(args[1:], stdout, stderr)
	case "create-cluster":
		return runCreateCluster(args[1:], stdout, stderr)
	case "create-project":
		return runCreateProject(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprint(stdout, buildinfo.Format("sqlserver2tidb"))
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runInitRepo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init-repo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := gitops.InitRepo(*root); err != nil {
		fmt.Fprintf(stderr, "init repo: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized migration repository at %s\n", *root)
	return 0
}

func runValidateRepo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate-repo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	report, err := gitops.ValidateRepo(*root)
	if err != nil {
		fmt.Fprintf(stderr, "validate repo: %v\n", err)
		return 1
	}
	if !report.Valid {
		fmt.Fprintf(stderr, "repository validation failed at %s:\n", *root)
		for _, message := range report.Errors {
			fmt.Fprintf(stderr, "- %s\n", message)
		}
		return 1
	}
	fmt.Fprintf(stdout, "repository is valid at %s (%d dirs, %d files checked)\n", *root, report.CheckedDirs, report.CheckedFiles)
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	requireTools := fs.Bool("require-tools", false, "return non-zero when local helper tools are missing")
	jsonOutput := fs.Bool("json", false, "write doctor report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := gitops.ValidateRepo(*root)
	if err != nil {
		fmt.Fprintf(stderr, "doctor: validate repo: %v\n", err)
		return 1
	}

	tools := []string{"git", "gh", "sqlserver2tidb-executor"}
	type doctorToolReport struct {
		Name  string `json:"name"`
		Found bool   `json:"found"`
		Path  string `json:"path,omitempty"`
	}
	doctorReport := struct {
		Repository struct {
			Valid        bool     `json:"valid"`
			CheckedDirs  int      `json:"checked_dirs"`
			CheckedFiles int      `json:"checked_files"`
			Errors       []string `json:"errors"`
		} `json:"repository"`
		Tools []doctorToolReport `json:"tools"`
	}{}
	doctorReport.Repository.Valid = report.Valid
	doctorReport.Repository.CheckedDirs = report.CheckedDirs
	doctorReport.Repository.CheckedFiles = report.CheckedFiles
	doctorReport.Repository.Errors = report.Errors

	missingTools := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolReport := doctorToolReport{Name: tool}
		path, err := lookPath(tool)
		if err != nil {
			missingTools = append(missingTools, tool)
			doctorReport.Tools = append(doctorReport.Tools, toolReport)
			continue
		}
		toolReport.Found = true
		toolReport.Path = path
		doctorReport.Tools = append(doctorReport.Tools, toolReport)
	}

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(doctorReport); err != nil {
			fmt.Fprintf(stderr, "doctor json: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintln(stdout, "doctor completed")
		if report.Valid {
			fmt.Fprintf(stdout, "repository: valid (%d dirs, %d files checked)\n", report.CheckedDirs, report.CheckedFiles)
		} else {
			fmt.Fprintf(stdout, "repository: invalid (%d errors)\n", len(report.Errors))
			for _, message := range report.Errors {
				fmt.Fprintf(stdout, "- %s\n", message)
			}
		}
		fmt.Fprintln(stdout, "tools:")
		for _, tool := range doctorReport.Tools {
			if !tool.Found {
				fmt.Fprintf(stdout, "- %s: missing\n", tool.Name)
				continue
			}
			fmt.Fprintf(stdout, "- %s: found (%s)\n", tool.Name, tool.Path)
		}
	}

	if !report.Valid {
		return 1
	}
	if *requireTools && len(missingTools) > 0 {
		fmt.Fprintf(stderr, "doctor: required tools missing: %s\n", strings.Join(missingTools, ", "))
		return 1
	}
	return 0
}

type agentStatusReport struct {
	Mode            string                       `json:"mode"`
	SourceClusterID string                       `json:"source_cluster_id,omitempty"`
	ProjectID       string                       `json:"project_id,omitempty"`
	Repository      agentRepositoryStatus        `json:"repository"`
	Reconcile       gitops.WorkerReconcileReport `json:"reconcile"`
	NextAction      gitops.WorkerReconcileAction `json:"next_action"`
}

type agentRepositoryStatus struct {
	Valid        bool     `json:"valid"`
	CheckedDirs  int      `json:"checked_dirs"`
	CheckedFiles int      `json:"checked_files"`
	Errors       []string `json:"errors,omitempty"`
}

type agentAutoDryRunReport struct {
	Mode            string                       `json:"mode"`
	SourceClusterID string                       `json:"source_cluster_id,omitempty"`
	ProjectID       string                       `json:"project_id,omitempty"`
	Repository      agentRepositoryStatus        `json:"repository"`
	Reconcile       gitops.WorkerReconcileReport `json:"reconcile"`
	NextAction      agentAutoAction              `json:"next_action"`
	StopReason      string                       `json:"stop_reason"`
}

type agentAutoAction struct {
	Name            string `json:"name,omitempty"`
	SourceClusterID string `json:"source_cluster_id,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	Stage           string `json:"stage,omitempty"`
	Status          string `json:"status,omitempty"`
	Command         string `json:"command,omitempty"`
}

type agentPlanAndPRReport struct {
	Mode            string `json:"mode"`
	SourceClusterID string `json:"source_cluster_id"`
	ProjectID       string `json:"project_id,omitempty"`
	Stage           string `json:"stage"`
	Title           string `json:"title"`
	BranchName      string `json:"branch_name"`
	BodyFile        string `json:"body_file"`
	FilesToReview   int    `json:"files_to_review"`
	Command         string `json:"command"`
	ExecutedPR      bool   `json:"executed_pr"`
	GitHubOutput    string `json:"github_output,omitempty"`
}

type agentExecuteApprovedReport struct {
	Mode            string                       `json:"mode"`
	SourceClusterID string                       `json:"source_cluster_id"`
	ProjectID       string                       `json:"project_id"`
	Stage           string                       `json:"stage"`
	Action          gitops.WorkerReconcileAction `json:"action"`
	Command         string                       `json:"command"`
	Executed        bool                         `json:"executed"`
	StopReason      string                       `json:"stop_reason,omitempty"`
}

type agentExecutorOptions struct {
	UseExecutor               bool
	ExecutorBinary            string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
	ImportBatchSize           int
	RequireEmptyTarget        bool
	CommandTimeout            time.Duration
	CommandRetries            int
	RetryBackoff              time.Duration
	Resume                    bool
}

type agentEvidencePROptions struct {
	Draft   bool
	Create  bool
	Execute bool
}

type agentPRCloseOptions struct {
	PRNumber           int
	Repo               string
	GHBinary           string
	GitBinary          string
	Base               string
	MergeMethod        string
	SkipApprove        bool
	DeleteBranch       bool
	AutomationActor    string
	AutomationWorkflow string
	AutomationRunID    string
	AutomationRunURL   string
	AutomationCommit   string
}

type agentCDCOpsOptions struct {
	MaxLSN                 string
	MinLSNs                cdcHealthMinLSNFlags
	MaxCheckpointAge       time.Duration
	Now                    string
	MetricsFile            string
	HistoryFile            string
	FailOnWarning          bool
	ProbeLSN               bool
	FeishuWebhookEnv       string
	FeishuSecretEnv        string
	FeishuAlertMinSeverity string
	SlackWebhookEnv        string
	SlackAlertMinSeverity  string
	FromLSN                string
	PRDraft                bool
	SkipRetentionCheck     bool
	ApplyApproved          bool
	CheckpointStatus       string
	Poll                   bool
	MaxIterations          int
	Interval               time.Duration
	IdleIterations         int
	MinAppliedChanges      int
}

type agentLLMOptions struct {
	ProviderConfigPath   string
	ProviderID           string
	ProviderType         string
	BaseURL              string
	Model                string
	AuthMode             string
	APIKeyEnv            string
	TokenEnv             string
	TokenURL             string
	ClientIDEnv          string
	ClientSecretEnv      string
	RefreshTokenEnv      string
	Scopes               string
	ExternalCommand      string
	AllowExternalCommand bool
	Timeout              time.Duration
	ExecuteLLM           bool
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "status", "agent mode: status, auto, plan-and-pr, execute-approved, pr-close, cdc-ops, or review-assist")
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "optional source cluster id to scope agent status")
	projectID := fs.String("project-id", "", "optional project id to scope agent status")
	stage := fs.String("stage", "", "migration stage for modes that operate on one stage")
	jsonOutput := fs.Bool("json", false, "write agent status as JSON")
	dryRun := fs.Bool("dry-run", false, "plan agent actions without writing files, calling GitHub, or executing database work")
	maxSteps := fs.Int("max-steps", 1, "maximum safe planning steps for agent auto; must be positive")
	execute := fs.Bool("execute", false, "execute approved agent action when the selected mode supports execution")
	executePR := fs.Bool("execute-pr", false, "call gh pr create after generating a PR draft")
	evidencePRDraft := fs.Bool("evidence-pr-draft", false, "after execute-approved executor-backed execution, generate an executor evidence PR draft")
	createEvidencePR := fs.Bool("create-evidence-pr", false, "after execute-approved executor-backed execution, generate and preview executor evidence PR creation; default does not change git or call GitHub")
	executeEvidencePR := fs.Bool("execute-evidence-pr", false, "after execute-approved executor-backed execution, generate and create the executor evidence PR through git and gh")
	useExecutor := fs.Bool("use-executor", false, "for execute-approved, route executor-supported stages through worker-executor instead of metadata-only workers")
	executorBinary := fs.String("executor-binary", "", "external executor binary for executor-backed agent actions; default is sqlserver2tidb-executor")
	prNumber := fs.Int("pr", 0, "GitHub pull request number for pr-close")
	repo := fs.String("repo", "", "optional GitHub repository in owner/name form for pr-close")
	ghBinary := fs.String("gh-binary", "gh", "GitHub CLI binary for pr-close")
	gitBinary := fs.String("git-binary", "git", "git binary for pr-close")
	base := fs.String("base", "main", "base branch for pr-close")
	mergeMethod := fs.String("merge-method", "squash", "GitHub PR merge method for pr-close: squash, merge, or rebase")
	skipApprove := fs.Bool("skip-approve", false, "skip automated gh pr review --approve in pr-close")
	deleteBranch := fs.Bool("delete-branch", true, "delete the PR branch after merge in pr-close")
	automationActor := fs.String("automation-actor", os.Getenv("GITHUB_ACTOR"), "optional actor recorded in approval automation audit metadata for pr-close")
	automationWorkflow := fs.String("automation-workflow", os.Getenv("GITHUB_WORKFLOW"), "optional workflow name recorded in approval automation audit metadata for pr-close")
	automationRunID := fs.String("automation-run-id", os.Getenv("GITHUB_RUN_ID"), "optional workflow run id recorded in approval automation audit metadata for pr-close")
	automationRunURL := fs.String("automation-run-url", defaultGitHubRunURL(), "optional workflow run URL recorded in approval automation audit metadata for pr-close")
	automationCommit := fs.String("automation-commit", os.Getenv("GITHUB_SHA"), "optional workflow commit SHA recorded in approval automation audit metadata for pr-close")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "", "environment variable containing the SQL Server connection string for executor-backed agent actions")
	targetConnectionStringEnv := fs.String("target-connection-string-env", "", "environment variable containing the TiDB/MySQL connection string for executor-backed agent actions")
	importBatchSize := fs.Int("import-batch-size", 0, "rows to commit per executor import transaction; default is executor-defined")
	requireEmptyTarget := fs.Bool("require-empty-target", false, "pass executor --require-empty-target to sql-insert import commands")
	commandTimeout := fs.Duration("command-timeout", 0, "maximum runtime per external executor command; 0 disables timeout")
	commandRetries := fs.Int("command-retries", 0, "number of retries for a failed external executor command")
	retryBackoff := fs.Duration("retry-backoff", time.Second, "fixed backoff between external executor command retries")
	resume := fs.Bool("resume", false, "skip matching successful executor commands from existing evidence")
	maxLSN := fs.String("max-lsn", "", "current SQL Server CDC max LSN for cdc-ops")
	maxCheckpointAge := fs.Duration("max-checkpoint-age", 0, "maximum allowed CDC checkpoint age for cdc-ops health checks; 0 disables age checking")
	nowRaw := fs.String("now", "", "current time override as RFC3339 for cdc-ops health checks")
	metricsFile := fs.String("metrics-file", "", "optional cdc-ops health metrics JSON file")
	historyFile := fs.String("history-file", "", "optional cdc-ops health JSONL history file")
	failOnWarning := fs.Bool("fail-on-warning", false, "return non-zero when cdc-ops health status is warning")
	probeLSN := fs.Bool("probe-lsn", false, "probe SQL Server CDC max/min LSNs through the executor before evaluating cdc-ops health")
	feishuWebhookEnv := fs.String("feishu-webhook-env", "SQLSERVER2TIDB_FEISHU_WEBHOOK", "environment variable containing the Feishu custom bot webhook URL for cdc-ops")
	feishuSecretEnv := fs.String("feishu-secret-env", "SQLSERVER2TIDB_FEISHU_SECRET", "environment variable containing the optional Feishu custom bot signing secret for cdc-ops")
	feishuAlertMinSeverity := fs.String("feishu-alert-min-severity", "critical", "minimum cdc-ops health status that sends Feishu alerts: ok, warning, critical, or none")
	slackWebhookEnv := fs.String("slack-webhook-env", "SQLSERVER2TIDB_SLACK_WEBHOOK", "environment variable containing the Slack incoming webhook URL for cdc-ops")
	slackAlertMinSeverity := fs.String("slack-alert-min-severity", "critical", "minimum cdc-ops health status that sends Slack alerts: ok, warning, critical, or none")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for cdc-ops tables without checkpoint state")
	prDraft := fs.Bool("pr-draft", false, "write a CDC range PR draft when cdc-ops prepares a new range")
	skipRetentionCheck := fs.Bool("skip-retention-check", false, "skip cdc-ops per-table SQL Server CDC min LSN retention checks")
	applyApproved := fs.Bool("apply-approved", false, "execute an already approved CDC range in cdc-ops before probing the next SQL Server max LSN")
	checkpointStatus := fs.String("checkpoint-status", "running", "checkpoint status to write after approved CDC apply in cdc-ops: running or caught_up")
	poll := fs.Bool("poll", false, "continue polling when cdc-ops is caught up")
	maxIterations := fs.Int("max-iterations", 0, "maximum cdc-ops probe iterations; 0 means unlimited")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between caught-up cdc-ops polling iterations")
	idleIterations := fs.Int("idle-iterations", 0, "maximum consecutive caught-up polls in cdc-ops --poll mode; 0 means unlimited")
	minAppliedChanges := fs.Int("min-applied-changes", 0, "minimum total CDC applied changes required before cdc-ops can exit successfully")
	var minLSNs cdcHealthMinLSNFlags
	fs.Var(&minLSNs, "min-lsn", "per-table SQL Server CDC min LSN for cdc-ops health as source.object=0x...")
	executeLLM := fs.Bool("execute-llm", false, "call the LLM provider for review-assist; default is dry-run")
	providerConfigPath := fs.String("provider-config", "", "optional LLM provider config file for review-assist; defaults to inline flags")
	providerID := fs.String("provider-id", "", "LLM provider id from provider config for review-assist; defaults to config default_provider")
	providerType := fs.String("provider-type", llm.ProviderTypeOpenAICompatible, "LLM provider type for review-assist")
	baseURL := fs.String("base-url", "", "OpenAI-compatible base URL for review-assist, for example https://api.openai.com/v1")
	model := fs.String("model", "", "LLM model name for review-assist")
	authMode := fs.String("auth-mode", llm.AuthModeAPIKey, "LLM auth mode for review-assist: api_key, oauth_client_credentials, oauth_refresh_token, oauth_token_env, external_command")
	apiKeyEnv := fs.String("api-key-env", "OPENAI_API_KEY", "API key environment variable for review-assist api_key auth")
	tokenEnv := fs.String("token-env", "", "access token environment variable for review-assist oauth_token_env auth")
	tokenURL := fs.String("token-url", "", "OAuth token URL for review-assist oauth_client_credentials or oauth_refresh_token auth")
	clientIDEnv := fs.String("client-id-env", "", "OAuth client id environment variable for review-assist")
	clientSecretEnv := fs.String("client-secret-env", "", "OAuth client secret environment variable for review-assist")
	refreshTokenEnv := fs.String("refresh-token-env", "", "OAuth refresh token environment variable for review-assist")
	scopes := fs.String("scopes", "", "comma-separated OAuth scopes for review-assist")
	externalCommand := fs.String("external-command", "", "external token command for review-assist; disabled unless --allow-external-command is set")
	allowExternalCommand := fs.Bool("allow-external-command", false, "allow review-assist external_command auth to run a local command")
	llmTimeout := fs.Duration("timeout", 2*time.Minute, "LLM provider request timeout for review-assist")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	executorOptions := agentExecutorOptions{
		UseExecutor:               *useExecutor,
		ExecutorBinary:            strings.TrimSpace(*executorBinary),
		SourceConnectionStringEnv: strings.TrimSpace(*sourceConnectionStringEnv),
		TargetConnectionStringEnv: strings.TrimSpace(*targetConnectionStringEnv),
		ImportBatchSize:           *importBatchSize,
		RequireEmptyTarget:        *requireEmptyTarget,
		CommandTimeout:            *commandTimeout,
		CommandRetries:            *commandRetries,
		RetryBackoff:              *retryBackoff,
		Resume:                    *resume,
	}
	evidencePROptions := agentEvidencePROptions{
		Draft:   *evidencePRDraft || *createEvidencePR || *executeEvidencePR,
		Create:  *createEvidencePR || *executeEvidencePR,
		Execute: *executeEvidencePR,
	}
	prCloseOptions := agentPRCloseOptions{
		PRNumber:           *prNumber,
		Repo:               strings.TrimSpace(*repo),
		GHBinary:           strings.TrimSpace(*ghBinary),
		GitBinary:          strings.TrimSpace(*gitBinary),
		Base:               strings.TrimSpace(*base),
		MergeMethod:        strings.TrimSpace(*mergeMethod),
		SkipApprove:        *skipApprove,
		DeleteBranch:       *deleteBranch,
		AutomationActor:    strings.TrimSpace(*automationActor),
		AutomationWorkflow: strings.TrimSpace(*automationWorkflow),
		AutomationRunID:    strings.TrimSpace(*automationRunID),
		AutomationRunURL:   strings.TrimSpace(*automationRunURL),
		AutomationCommit:   strings.TrimSpace(*automationCommit),
	}
	cdcOpsOptions := agentCDCOpsOptions{
		MaxLSN:                 strings.TrimSpace(*maxLSN),
		MinLSNs:                minLSNs,
		MaxCheckpointAge:       *maxCheckpointAge,
		Now:                    strings.TrimSpace(*nowRaw),
		MetricsFile:            strings.TrimSpace(*metricsFile),
		HistoryFile:            strings.TrimSpace(*historyFile),
		FailOnWarning:          *failOnWarning,
		ProbeLSN:               *probeLSN,
		FeishuWebhookEnv:       strings.TrimSpace(*feishuWebhookEnv),
		FeishuSecretEnv:        strings.TrimSpace(*feishuSecretEnv),
		FeishuAlertMinSeverity: strings.TrimSpace(*feishuAlertMinSeverity),
		SlackWebhookEnv:        strings.TrimSpace(*slackWebhookEnv),
		SlackAlertMinSeverity:  strings.TrimSpace(*slackAlertMinSeverity),
		FromLSN:                strings.TrimSpace(*fromLSN),
		PRDraft:                *prDraft,
		SkipRetentionCheck:     *skipRetentionCheck,
		ApplyApproved:          *applyApproved,
		CheckpointStatus:       strings.TrimSpace(*checkpointStatus),
		Poll:                   *poll,
		MaxIterations:          *maxIterations,
		Interval:               *interval,
		IdleIterations:         *idleIterations,
		MinAppliedChanges:      *minAppliedChanges,
	}
	llmOptions := agentLLMOptions{
		ProviderConfigPath:   strings.TrimSpace(*providerConfigPath),
		ProviderID:           strings.TrimSpace(*providerID),
		ProviderType:         strings.TrimSpace(*providerType),
		BaseURL:              strings.TrimSpace(*baseURL),
		Model:                strings.TrimSpace(*model),
		AuthMode:             strings.TrimSpace(*authMode),
		APIKeyEnv:            strings.TrimSpace(*apiKeyEnv),
		TokenEnv:             strings.TrimSpace(*tokenEnv),
		TokenURL:             strings.TrimSpace(*tokenURL),
		ClientIDEnv:          strings.TrimSpace(*clientIDEnv),
		ClientSecretEnv:      strings.TrimSpace(*clientSecretEnv),
		RefreshTokenEnv:      strings.TrimSpace(*refreshTokenEnv),
		Scopes:               strings.TrimSpace(*scopes),
		ExternalCommand:      strings.TrimSpace(*externalCommand),
		AllowExternalCommand: *allowExternalCommand,
		Timeout:              *llmTimeout,
		ExecuteLLM:           *executeLLM,
	}
	switch strings.TrimSpace(*mode) {
	case "status":
		return runAgentStatus(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), *jsonOutput, stdout, stderr)
	case "auto":
		if *dryRun || *jsonOutput {
			return runAgentAutoDryRun(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), *jsonOutput, stdout, stderr)
		}
		return runAgentAuto(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), *maxSteps, *executePR, stdout, stderr)
	case "plan-and-pr":
		return runAgentPlanAndPR(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), strings.TrimSpace(*stage), *executePR, *jsonOutput, stdout, stderr)
	case "execute-approved":
		return runAgentExecuteApproved(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), strings.TrimSpace(*stage), *execute, *jsonOutput, evidencePROptions, executorOptions, stdout, stderr)
	case "pr-close":
		return runAgentPRClose(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), strings.TrimSpace(*stage), *execute, prCloseOptions, stdout, stderr)
	case "cdc-ops":
		return runAgentCDCOps(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), executorOptions, cdcOpsOptions, stdout, stderr)
	case "review-assist":
		return runAgentReviewAssist(*root, strings.TrimSpace(*sourceClusterID), strings.TrimSpace(*projectID), strings.TrimSpace(*stage), llmOptions, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "agent: unsupported mode %q; supported modes: status, auto, plan-and-pr, execute-approved, pr-close, cdc-ops, review-assist\n", *mode)
		return 2
	}
}

func runAgentStatus(root, sourceClusterID, projectID string, jsonOutput bool, stdout, stderr io.Writer) int {
	report, err := buildAgentStatusReport(root, sourceClusterID, projectID)
	if err != nil {
		fmt.Fprintf(stderr, "agent status: %v\n", err)
		return 1
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "agent status json: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, "migration agent status")
	fmt.Fprintf(stdout, "mode: %s\n", report.Mode)
	if report.SourceClusterID != "" {
		fmt.Fprintf(stdout, "source cluster: %s\n", report.SourceClusterID)
	}
	if report.ProjectID != "" {
		fmt.Fprintf(stdout, "project: %s\n", report.ProjectID)
	}
	if report.Repository.Valid {
		fmt.Fprintf(stdout, "repository: valid (%d dirs, %d files checked)\n", report.Repository.CheckedDirs, report.Repository.CheckedFiles)
	} else {
		fmt.Fprintf(stdout, "repository: invalid (%d errors)\n", len(report.Repository.Errors))
	}
	fmt.Fprintf(stdout, "projects: %d\n", report.Reconcile.Projects)
	fmt.Fprintf(stdout, "ready actions: %d\n", report.Reconcile.ReadyActions)
	fmt.Fprintf(stdout, "blocked actions: %d\n", report.Reconcile.BlockedActions)
	if report.NextAction.Stage == "" {
		fmt.Fprintln(stdout, "next action: none")
		return 0
	}
	fmt.Fprintf(stdout, "next action: %s/%s %s\n", report.NextAction.SourceClusterID, report.NextAction.ProjectID, report.NextAction.Stage)
	fmt.Fprintf(stdout, "command: %s\n", redact.Text(report.NextAction.Command))
	return 0
}

func runAgentAutoDryRun(root, sourceClusterID, projectID string, jsonOutput bool, stdout, stderr io.Writer) int {
	report, err := buildAgentAutoDryRunReport(root, sourceClusterID, projectID)
	if err != nil {
		fmt.Fprintf(stderr, "agent auto dry-run: %v\n", err)
		return 1
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "agent auto dry-run json: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "migration agent auto dry run")
	fmt.Fprintf(stdout, "mode: %s\n", report.Mode)
	if report.SourceClusterID != "" {
		fmt.Fprintf(stdout, "source cluster: %s\n", report.SourceClusterID)
	}
	if report.ProjectID != "" {
		fmt.Fprintf(stdout, "project: %s\n", report.ProjectID)
	}
	if report.Repository.Valid {
		fmt.Fprintf(stdout, "repository: valid (%d dirs, %d files checked)\n", report.Repository.CheckedDirs, report.Repository.CheckedFiles)
	} else {
		fmt.Fprintf(stdout, "repository: invalid (%d errors)\n", len(report.Repository.Errors))
	}
	fmt.Fprintf(stdout, "projects: %d\n", report.Reconcile.Projects)
	fmt.Fprintf(stdout, "ready actions: %d\n", report.Reconcile.ReadyActions)
	fmt.Fprintf(stdout, "blocked actions: %d\n", report.Reconcile.BlockedActions)
	if report.NextAction.Name == "" {
		fmt.Fprintln(stdout, "next action: none")
		fmt.Fprintf(stdout, "stop reason: %s\n", report.StopReason)
		return 0
	}
	if report.NextAction.Name == "worker-action" {
		fmt.Fprintf(stdout, "next action: %s/%s %s\n", report.NextAction.SourceClusterID, report.NextAction.ProjectID, report.NextAction.Stage)
	} else {
		fmt.Fprintf(stdout, "next action: %s\n", report.NextAction.Name)
	}
	fmt.Fprintf(stdout, "command: %s\n", redact.Text(report.NextAction.Command))
	fmt.Fprintf(stdout, "stop reason: %s\n", report.StopReason)
	return 0
}

func runAgentAuto(root, sourceClusterID, projectID string, maxSteps int, executePR bool, stdout, stderr io.Writer) int {
	if maxSteps < 1 {
		fmt.Fprintln(stderr, "agent auto: --max-steps must be positive")
		return 2
	}
	fmt.Fprintln(stdout, "migration agent auto")
	for step := 1; step <= maxSteps; step++ {
		report, err := buildAgentAutoDryRunReport(root, sourceClusterID, projectID)
		if err != nil {
			fmt.Fprintf(stderr, "agent auto: %v\n", err)
			return 1
		}
		if report.NextAction.Name == "" {
			fmt.Fprintln(stdout, "next action: none")
			fmt.Fprintf(stdout, "stop reason: %s\n", report.StopReason)
			return 0
		}
		switch report.NextAction.Name {
		case "generate schema draft":
			code := runGenerateSchemaDraft([]string{
				"--root", root,
				"--source-cluster-id", sourceClusterID,
				"--project-id", projectID,
			}, stdout, stderr)
			if code != 0 {
				return code
			}
			if step == maxSteps {
				fmt.Fprintln(stdout, "stop reason: max steps reached")
				return 0
			}
		case "generate schema PR":
			return runAgentPlanAndPR(root, sourceClusterID, projectID, "schema", executePR, false, stdout, stderr)
		case "generate plan PR":
			return runAgentPlanAndPR(root, sourceClusterID, projectID, "plan", executePR, false, stdout, stderr)
		case "worker-action":
			fmt.Fprintf(stdout, "next action: %s/%s %s\n", report.NextAction.SourceClusterID, report.NextAction.ProjectID, report.NextAction.Stage)
			fmt.Fprintf(stdout, "command: %s\n", redact.Text(report.NextAction.Command))
			fmt.Fprintln(stdout, "stop reason: execution requires --mode execute-approved")
			return 0
		default:
			fmt.Fprintf(stderr, "agent auto: unsupported next action %q\n", report.NextAction.Name)
			return 1
		}
	}
	return 0
}

func runAgentPlanAndPR(root, sourceClusterID, projectID, stage string, executePR, jsonOutput bool, stdout, stderr io.Writer) int {
	report, err := buildAgentPlanAndPRReport(root, sourceClusterID, projectID, stage, executePR)
	if err != nil {
		fmt.Fprintf(stderr, "agent plan-and-pr: %v\n", err)
		return 1
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "agent plan-and-pr json: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "migration agent plan-and-pr")
	fmt.Fprintf(stdout, "stage: %s\n", report.Stage)
	fmt.Fprintf(stdout, "PR draft generated for %s\n", report.Stage)
	fmt.Fprintf(stdout, "title: %s\n", report.Title)
	fmt.Fprintf(stdout, "branch: %s\n", report.BranchName)
	fmt.Fprintf(stdout, "body file: %s\n", report.BodyFile)
	fmt.Fprintf(stdout, "files to review: %d\n", report.FilesToReview)
	if !executePR {
		fmt.Fprintln(stdout, "dry run: not calling GitHub")
	}
	fmt.Fprintf(stdout, "command: %s\n", redact.Text(report.Command))
	if executePR {
		if report.GitHubOutput != "" {
			fmt.Fprint(stdout, report.GitHubOutput)
			if !strings.HasSuffix(report.GitHubOutput, "\n") {
				fmt.Fprintln(stdout)
			}
		}
		fmt.Fprintln(stdout, "GitHub PR created")
	}
	return 0
}

func buildAgentPlanAndPRReport(root, sourceClusterID, projectID, stage string, executePR bool) (agentPlanAndPRReport, error) {
	if strings.TrimSpace(stage) == "" {
		return agentPlanAndPRReport{}, fmt.Errorf("agent plan-and-pr requires --stage")
	}
	draft, err := gitops.GeneratePRDraft(root, sourceClusterID, projectID, stage)
	if err != nil {
		return agentPlanAndPRReport{}, err
	}
	spec, err := gitops.PrepareGitHubPRCreate(root, sourceClusterID, projectID, stage)
	if err != nil {
		return agentPlanAndPRReport{}, err
	}
	var githubOutput string
	if executePR {
		cmd := exec.Command("gh", spec.Args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			if len(output) > 0 {
				return agentPlanAndPRReport{}, fmt.Errorf("gh pr create failed: %w: %s", err, redact.Text(string(output)))
			}
			return agentPlanAndPRReport{}, fmt.Errorf("gh pr create failed: %w", err)
		}
		githubOutput = redact.Text(string(output))
	}
	return agentPlanAndPRReport{
		Mode:            "plan-and-pr",
		SourceClusterID: draft.SourceClusterID,
		ProjectID:       draft.ProjectID,
		Stage:           draft.Stage,
		Title:           draft.Title,
		BranchName:      draft.BranchName,
		BodyFile:        draft.BodyFile,
		FilesToReview:   len(draft.Files),
		Command:         spec.ShellCommand,
		ExecutedPR:      executePR,
		GitHubOutput:    githubOutput,
	}, nil
}

func runAgentPRClose(root, sourceClusterID, projectID, stage string, execute bool, options agentPRCloseOptions, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "migration agent pr-close")
	return runCompleteGitHubPR(agentPRCloseArgs(root, sourceClusterID, projectID, stage, execute, options), stdout, stderr)
}

func agentPRCloseArgs(root, sourceClusterID, projectID, stage string, execute bool, options agentPRCloseOptions) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", sourceClusterID,
		"--project-id", projectID,
		"--stage", stage,
		"--pr", strconv.Itoa(options.PRNumber),
		"--base", options.Base,
		"--merge-method", options.MergeMethod,
	}
	if options.Repo != "" {
		args = append(args, "--repo", options.Repo)
	}
	if options.GHBinary != "" {
		args = append(args, "--gh-binary", options.GHBinary)
	}
	if options.GitBinary != "" {
		args = append(args, "--git-binary", options.GitBinary)
	}
	if options.SkipApprove {
		args = append(args, "--skip-approve")
	}
	if !options.DeleteBranch {
		args = append(args, "--delete-branch=false")
	}
	if options.AutomationActor != "" {
		args = append(args, "--automation-actor", options.AutomationActor)
	}
	if options.AutomationWorkflow != "" {
		args = append(args, "--automation-workflow", options.AutomationWorkflow)
	}
	if options.AutomationRunID != "" {
		args = append(args, "--automation-run-id", options.AutomationRunID)
	}
	if options.AutomationRunURL != "" {
		args = append(args, "--automation-run-url", options.AutomationRunURL)
	}
	if options.AutomationCommit != "" {
		args = append(args, "--automation-commit", options.AutomationCommit)
	}
	if execute {
		args = append(args, "--execute")
	}
	return args
}

func runAgentExecuteApproved(root, sourceClusterID, projectID, stage string, execute, jsonOutput bool, evidencePROptions agentEvidencePROptions, executorOptions agentExecutorOptions, stdout, stderr io.Writer) int {
	report, err := buildAgentExecuteApprovedReport(root, sourceClusterID, projectID, stage, execute)
	if err != nil {
		fmt.Fprintf(stderr, "agent execute-approved: %v\n", err)
		return 1
	}
	if hasAgentEvidencePRAction(evidencePROptions) && !execute {
		fmt.Fprintln(stderr, "agent execute-approved: evidence PR options require --execute")
		return 2
	}
	if hasAgentEvidencePRAction(evidencePROptions) && !shouldUseAgentWorkerExecutor(report.Stage, executorOptions) {
		fmt.Fprintf(stderr, "agent execute-approved: evidence PR options require an executor-backed stage; use --use-executor for %q\n", report.Stage)
		return 2
	}
	if jsonOutput && !execute {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "agent execute-approved json: %v\n", err)
			return 1
		}
		return 0
	}
	if !execute {
		fmt.Fprintln(stdout, "migration agent execute-approved dry run")
		if shouldUseAgentWorkerExecutor(report.Stage, executorOptions) {
			return runWorkerExecutor(agentWorkerExecutorArgs(root, report.Action, executorOptions, false), stdout, stderr)
		}
		fmt.Fprintf(stdout, "next action: %s/%s %s\n", report.SourceClusterID, report.ProjectID, report.Stage)
		fmt.Fprintf(stdout, "command: %s\n", redact.Text(report.Command))
		fmt.Fprintf(stdout, "stop reason: %s\n", report.StopReason)
		return 0
	}
	fmt.Fprintln(stdout, "migration agent execute-approved")
	if code := runAgentExecuteApprovedAction(root, report.Action, executorOptions, stdout, stderr); code != 0 {
		return code
	}
	if evidencePROptions.Draft {
		if code := runGenerateExecutorEvidencePRDraft(agentExecutorEvidencePRDraftArgs(root, report.Action), stdout, stderr); code != 0 {
			return code
		}
	}
	if evidencePROptions.Create {
		return runCreateExecutorEvidencePR(agentExecutorEvidencePRCreateArgs(root, report.Action, evidencePROptions.Execute), stdout, stderr)
	}
	return 0
}

func buildAgentExecuteApprovedReport(root, sourceClusterID, projectID, stage string, execute bool) (agentExecuteApprovedReport, error) {
	if strings.TrimSpace(stage) == "" {
		return agentExecuteApprovedReport{}, fmt.Errorf("agent execute-approved requires --stage")
	}
	status, err := buildAgentStatusReport(root, sourceClusterID, projectID)
	if err != nil {
		return agentExecuteApprovedReport{}, err
	}
	var selected gitops.WorkerReconcileAction
	for _, action := range status.Reconcile.Actions {
		if action.Stage == stage {
			selected = action
			break
		}
	}
	if selected.Stage == "" {
		return agentExecuteApprovedReport{}, fmt.Errorf("stage %q is not available in the selected project scope", stage)
	}
	if selected.Status != "ready" {
		if selected.Reason != "" {
			return agentExecuteApprovedReport{}, fmt.Errorf("stage %q is not ready: %s", stage, selected.Reason)
		}
		return agentExecuteApprovedReport{}, fmt.Errorf("stage %q is not ready", stage)
	}
	report := agentExecuteApprovedReport{
		Mode:            "execute-approved",
		SourceClusterID: selected.SourceClusterID,
		ProjectID:       selected.ProjectID,
		Stage:           selected.Stage,
		Action:          selected,
		Command:         selected.Command,
		Executed:        execute,
	}
	if !execute {
		report.StopReason = "requires --execute"
	}
	return report, nil
}

func runAgentExecuteApprovedAction(root string, action gitops.WorkerReconcileAction, executorOptions agentExecutorOptions, stdout, stderr io.Writer) int {
	args := []string{
		"--root", root,
		"--source-cluster-id", action.SourceClusterID,
		"--project-id", action.ProjectID,
	}
	if shouldUseAgentWorkerExecutor(action.Stage, executorOptions) {
		return runWorkerExecutor(agentWorkerExecutorArgs(root, action, executorOptions, true), stdout, stderr)
	}
	switch action.Stage {
	case "export":
		return runWorkerExport(args, stdout, stderr)
	case "import":
		return runWorkerImport(args, stdout, stderr)
	case "cdc":
		return runWorkerCDC(args, stdout, stderr)
	case "validation":
		return runWorkerValidate(args, stdout, stderr)
	case "cutover":
		return runWorkerCutover(args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "agent execute-approved: stage %q is not executable by the agent\n", action.Stage)
		return 1
	}
}

func shouldUseAgentWorkerExecutor(stage string, options agentExecutorOptions) bool {
	if isAgentExecutorOnlyStage(stage) {
		return true
	}
	return options.UseExecutor && isAgentExecutorSupportedStage(stage)
}

func isAgentExecutorOnlyStage(stage string) bool {
	return stage == "ddl" || stage == "cdc-enable"
}

func isAgentExecutorSupportedStage(stage string) bool {
	switch stage {
	case "ddl", "export", "import", "cdc-enable", "cdc", "validation":
		return true
	default:
		return false
	}
}

func hasAgentEvidencePRAction(options agentEvidencePROptions) bool {
	return options.Draft || options.Create || options.Execute
}

func agentWorkerExecutorArgs(root string, action gitops.WorkerReconcileAction, options agentExecutorOptions, execute bool) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", action.SourceClusterID,
		"--project-id", action.ProjectID,
		"--stage", action.Stage,
	}
	if options.ExecutorBinary != "" {
		args = append(args, "--executor-binary", options.ExecutorBinary)
	}
	if options.SourceConnectionStringEnv != "" {
		args = append(args, "--source-connection-string-env", options.SourceConnectionStringEnv)
	}
	if options.TargetConnectionStringEnv != "" {
		args = append(args, "--target-connection-string-env", options.TargetConnectionStringEnv)
	}
	if options.ImportBatchSize != 0 {
		args = append(args, "--import-batch-size", strconv.Itoa(options.ImportBatchSize))
	}
	if options.RequireEmptyTarget {
		args = append(args, "--require-empty-target")
	}
	if options.CommandTimeout != 0 {
		args = append(args, "--command-timeout", options.CommandTimeout.String())
	}
	if options.CommandRetries != 0 {
		args = append(args, "--command-retries", strconv.Itoa(options.CommandRetries))
	}
	if options.RetryBackoff != time.Second {
		args = append(args, "--retry-backoff", options.RetryBackoff.String())
	}
	if options.Resume {
		args = append(args, "--resume")
	}
	if execute {
		args = append(args, "--execute")
	}
	return args
}

func agentExecutorEvidencePRDraftArgs(root string, action gitops.WorkerReconcileAction) []string {
	return []string{
		"--root", root,
		"--source-cluster-id", action.SourceClusterID,
		"--project-id", action.ProjectID,
		"--stage", action.Stage,
	}
}

func agentExecutorEvidencePRCreateArgs(root string, action gitops.WorkerReconcileAction, execute bool) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", action.SourceClusterID,
		"--project-id", action.ProjectID,
		"--stage", action.Stage,
	}
	if execute {
		args = append(args, "--execute")
	}
	return args
}

func runAgentCDCOps(root, sourceClusterID, projectID string, executorOptions agentExecutorOptions, options agentCDCOpsOptions, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "migration agent cdc-ops")
	healthCode := runCDCHealth(agentCDCHealthArgs(root, sourceClusterID, projectID, executorOptions, options), stdout, stderr)
	if healthCode != 0 {
		return healthCode
	}
	return runCDCOrchestrator(agentCDCOrchestratorArgs(root, sourceClusterID, projectID, executorOptions, options), stdout, stderr)
}

func agentCDCHealthArgs(root, sourceClusterID, projectID string, executorOptions agentExecutorOptions, options agentCDCOpsOptions) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", sourceClusterID,
		"--project-id", projectID,
	}
	if options.MaxLSN != "" {
		args = append(args, "--max-lsn", options.MaxLSN)
	}
	for _, sourceObject := range sortedCDCHealthMinLSNKeys(options.MinLSNs) {
		args = append(args, "--min-lsn", sourceObject+"="+options.MinLSNs[sourceObject])
	}
	if options.MaxCheckpointAge != 0 {
		args = append(args, "--max-checkpoint-age", options.MaxCheckpointAge.String())
	}
	if options.Now != "" {
		args = append(args, "--now", options.Now)
	}
	if options.MetricsFile != "" {
		args = append(args, "--metrics-file", options.MetricsFile)
	}
	if options.HistoryFile != "" {
		args = append(args, "--history-file", options.HistoryFile)
	}
	if options.FailOnWarning {
		args = append(args, "--fail-on-warning")
	}
	if options.ProbeLSN {
		args = append(args, "--probe-lsn")
	}
	if executorOptions.ExecutorBinary != "" {
		args = append(args, "--executor-binary", executorOptions.ExecutorBinary)
	}
	if executorOptions.SourceConnectionStringEnv != "" {
		args = append(args, "--source-connection-string-env", executorOptions.SourceConnectionStringEnv)
	}
	if options.FeishuWebhookEnv != "" {
		args = append(args, "--feishu-webhook-env", options.FeishuWebhookEnv)
	}
	if options.FeishuSecretEnv != "" {
		args = append(args, "--feishu-secret-env", options.FeishuSecretEnv)
	}
	if options.FeishuAlertMinSeverity != "" {
		args = append(args, "--feishu-alert-min-severity", options.FeishuAlertMinSeverity)
	}
	if options.SlackWebhookEnv != "" {
		args = append(args, "--slack-webhook-env", options.SlackWebhookEnv)
	}
	if options.SlackAlertMinSeverity != "" {
		args = append(args, "--slack-alert-min-severity", options.SlackAlertMinSeverity)
	}
	return args
}

func agentCDCOrchestratorArgs(root, sourceClusterID, projectID string, executorOptions agentExecutorOptions, options agentCDCOpsOptions) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", sourceClusterID,
		"--project-id", projectID,
	}
	if executorOptions.ExecutorBinary != "" {
		args = append(args, "--executor-binary", executorOptions.ExecutorBinary)
	}
	if executorOptions.SourceConnectionStringEnv != "" {
		args = append(args, "--source-connection-string-env", executorOptions.SourceConnectionStringEnv)
	}
	if executorOptions.TargetConnectionStringEnv != "" {
		args = append(args, "--target-connection-string-env", executorOptions.TargetConnectionStringEnv)
	}
	if options.MaxLSN != "" {
		args = append(args, "--max-lsn", options.MaxLSN)
	}
	if options.FromLSN != "" {
		args = append(args, "--from-lsn", options.FromLSN)
	}
	if options.PRDraft {
		args = append(args, "--pr-draft")
	}
	if options.SkipRetentionCheck {
		args = append(args, "--skip-retention-check")
	}
	if options.ApplyApproved {
		args = append(args, "--apply-approved")
	}
	if options.CheckpointStatus != "" && options.CheckpointStatus != "running" {
		args = append(args, "--checkpoint-status", options.CheckpointStatus)
	}
	if executorOptions.CommandTimeout != 0 {
		args = append(args, "--command-timeout", executorOptions.CommandTimeout.String())
	}
	if executorOptions.CommandRetries != 0 {
		args = append(args, "--command-retries", strconv.Itoa(executorOptions.CommandRetries))
	}
	if executorOptions.RetryBackoff != time.Second {
		args = append(args, "--retry-backoff", executorOptions.RetryBackoff.String())
	}
	if executorOptions.Resume {
		args = append(args, "--resume")
	}
	if options.MinAppliedChanges != 0 {
		args = append(args, "--min-applied-changes", strconv.Itoa(options.MinAppliedChanges))
	}
	if options.Poll {
		args = append(args, "--poll")
	}
	if options.MaxIterations != 0 {
		args = append(args, "--max-iterations", strconv.Itoa(options.MaxIterations))
	}
	if options.Interval != 5*time.Second {
		args = append(args, "--interval", options.Interval.String())
	}
	if options.IdleIterations != 0 {
		args = append(args, "--idle-iterations", strconv.Itoa(options.IdleIterations))
	}
	return args
}

func sortedCDCHealthMinLSNKeys(values cdcHealthMinLSNFlags) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runAgentReviewAssist(root, sourceClusterID, projectID, stage string, options agentLLMOptions, stdout, stderr io.Writer) int {
	if strings.TrimSpace(stage) == "" {
		fmt.Fprintln(stderr, "agent review-assist requires --stage")
		return 2
	}
	fmt.Fprintln(stdout, "migration agent review-assist")
	args := agentLLMArgs(root, sourceClusterID, projectID, "", options)
	switch stage {
	case "compatibility":
		return runLLMCompatibilityAdvice(agentLLMArgs(root, sourceClusterID, "", "", options), stdout, stderr)
	case "schema":
		return runLLMSchemaAdvice(args, stdout, stderr)
	case "strategy", "migration-strategy", "plan":
		return runLLMMigrationStrategy(args, stdout, stderr)
	case "validation":
		return runLLMProjectAdvice(args, stdout, stderr, llmValidationAnalysisCommand())
	case "cutover":
		return runLLMProjectAdvice(args, stdout, stderr, llmCutoverRiskCommand())
	default:
		fmt.Fprintf(stderr, "agent review-assist: unsupported stage %q; supported stages: compatibility, schema, strategy, validation, cutover\n", stage)
		return 2
	}
}

func agentLLMArgs(root, sourceClusterID, projectID, stage string, options agentLLMOptions) []string {
	args := []string{
		"--root", root,
		"--source-cluster-id", sourceClusterID,
	}
	if projectID != "" {
		args = append(args, "--project-id", projectID)
	}
	if stage != "" {
		args = append(args, "--stage", stage)
	}
	if options.ProviderConfigPath != "" {
		args = append(args, "--provider-config", options.ProviderConfigPath)
	}
	if options.ProviderID != "" {
		args = append(args, "--provider-id", options.ProviderID)
	}
	if options.ProviderType != "" {
		args = append(args, "--provider-type", options.ProviderType)
	}
	if options.BaseURL != "" {
		args = append(args, "--base-url", options.BaseURL)
	}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.AuthMode != "" {
		args = append(args, "--auth-mode", options.AuthMode)
	}
	if options.APIKeyEnv != "" {
		args = append(args, "--api-key-env", options.APIKeyEnv)
	}
	if options.TokenEnv != "" {
		args = append(args, "--token-env", options.TokenEnv)
	}
	if options.TokenURL != "" {
		args = append(args, "--token-url", options.TokenURL)
	}
	if options.ClientIDEnv != "" {
		args = append(args, "--client-id-env", options.ClientIDEnv)
	}
	if options.ClientSecretEnv != "" {
		args = append(args, "--client-secret-env", options.ClientSecretEnv)
	}
	if options.RefreshTokenEnv != "" {
		args = append(args, "--refresh-token-env", options.RefreshTokenEnv)
	}
	if options.Scopes != "" {
		args = append(args, "--scopes", options.Scopes)
	}
	if options.ExternalCommand != "" {
		args = append(args, "--external-command", options.ExternalCommand)
	}
	if options.AllowExternalCommand {
		args = append(args, "--allow-external-command")
	}
	if options.Timeout != 2*time.Minute {
		args = append(args, "--timeout", options.Timeout.String())
	}
	if options.ExecuteLLM {
		args = append(args, "--execute")
	}
	return args
}

func buildAgentStatusReport(root, sourceClusterID, projectID string) (agentStatusReport, error) {
	repoReport, err := gitops.ValidateRepo(root)
	if err != nil {
		return agentStatusReport{}, fmt.Errorf("validate repo: %w", err)
	}
	status := agentStatusReport{
		Mode:            "status",
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Repository: agentRepositoryStatus{
			Valid:        repoReport.Valid,
			CheckedDirs:  repoReport.CheckedDirs,
			CheckedFiles: repoReport.CheckedFiles,
			Errors:       repoReport.Errors,
		},
	}
	if !repoReport.Valid {
		return status, fmt.Errorf("repository validation failed with %d errors", len(repoReport.Errors))
	}
	reconcileReport, err := gitops.PlanWorkerReconcileWithSpec(root, gitops.WorkerReconcilePlanSpec{SourceClusterID: sourceClusterID})
	if err != nil {
		return agentStatusReport{}, err
	}
	if projectID != "" {
		reconcileReport = filterWorkerReconcileReportByProject(reconcileReport, projectID)
		if reconcileReport.Projects == 0 {
			return agentStatusReport{}, fmt.Errorf("project %q does not exist in source cluster scope", projectID)
		}
	}
	status.Reconcile = reconcileReport
	for _, action := range reconcileReport.Actions {
		if action.Status == "ready" {
			status.NextAction = action
			break
		}
	}
	return status, nil
}

func buildAgentAutoDryRunReport(root, sourceClusterID, projectID string) (agentAutoDryRunReport, error) {
	status, err := buildAgentStatusReport(root, sourceClusterID, projectID)
	if err != nil {
		return agentAutoDryRunReport{}, err
	}
	report := agentAutoDryRunReport{
		Mode:            "auto",
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Repository:      status.Repository,
		Reconcile:       status.Reconcile,
		StopReason:      "completed",
	}
	if sourceClusterID != "" && projectID != "" {
		schemaStatus, exists, err := readAgentSchemaDiffStatus(root, sourceClusterID, projectID)
		if err != nil {
			return agentAutoDryRunReport{}, err
		}
		if exists && schemaStatus != "reviewed" {
			if schemaStatus == "pending" {
				report.NextAction = agentAutoAction{
					Name:            "generate schema draft",
					SourceClusterID: sourceClusterID,
					ProjectID:       projectID,
					Stage:           "schema",
					Status:          "ready",
					Command:         fmt.Sprintf("sqlserver2tidb generate-schema-draft --root %s --source-cluster-id %s --project-id %s", root, sourceClusterID, projectID),
				}
				report.StopReason = "dry-run"
				return report, nil
			}
			if agentProjectFileExists(root, sourceClusterID, projectID, "prs/schema-pr.md") {
				report.StopReason = "review required"
				return report, nil
			}
			report.NextAction = agentAutoAction{
				Name:            "generate schema PR",
				SourceClusterID: sourceClusterID,
				ProjectID:       projectID,
				Stage:           "schema",
				Status:          "ready",
				Command:         fmt.Sprintf("sqlserver2tidb generate-pr-draft --root %s --source-cluster-id %s --project-id %s --stage schema", root, sourceClusterID, projectID),
			}
			report.StopReason = "review required"
			return report, nil
		}
	}
	if status.NextAction.Stage != "" {
		report.NextAction = agentAutoAction{
			Name:            "worker-action",
			SourceClusterID: status.NextAction.SourceClusterID,
			ProjectID:       status.NextAction.ProjectID,
			Stage:           status.NextAction.Stage,
			Status:          status.NextAction.Status,
			Command:         status.NextAction.Command,
		}
		report.StopReason = "dry-run"
	}
	if report.NextAction.Name == "" && sourceClusterID != "" && projectID != "" && agentProjectFileExists(root, sourceClusterID, projectID, "plan/migration-plan.yaml") && !agentProjectFileExists(root, sourceClusterID, projectID, "prs/plan-pr.md") {
		report.NextAction = agentAutoAction{
			Name:            "generate plan PR",
			SourceClusterID: sourceClusterID,
			ProjectID:       projectID,
			Stage:           "plan",
			Status:          "ready",
			Command:         fmt.Sprintf("sqlserver2tidb generate-pr-draft --root %s --source-cluster-id %s --project-id %s --stage plan", root, sourceClusterID, projectID),
		}
		report.StopReason = "review required"
	}
	return report, nil
}

func agentProjectFileExists(root, sourceClusterID, projectID, rel string) bool {
	info, err := os.Stat(filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}

func readAgentSchemaDiffStatus(root, sourceClusterID, projectID string) (string, bool, error) {
	path := filepath.Join(root, "clusters", sourceClusterID, "projects", projectID, "schema", "schema-diff.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read schema-diff.json: %w", err)
	}
	var diff struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &diff); err != nil {
		return "", false, fmt.Errorf("parse schema-diff.json: %w", err)
	}
	return strings.TrimSpace(diff.Status), true, nil
}

func filterWorkerReconcileReportByProject(report gitops.WorkerReconcileReport, projectID string) gitops.WorkerReconcileReport {
	var filtered gitops.WorkerReconcileReport
	seenProjects := map[string]struct{}{}
	for _, action := range report.Actions {
		if action.ProjectID != projectID {
			continue
		}
		filtered.Actions = append(filtered.Actions, action)
		seenProjects[action.SourceClusterID+"/"+action.ProjectID] = struct{}{}
		if action.Status == "ready" {
			filtered.ReadyActions++
		} else {
			filtered.BlockedActions++
		}
	}
	filtered.Projects = len(seenProjects)
	return filtered
}

func runDiscoverSQLServer(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover-sqlserver", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	dryRun := fs.Bool("dry-run", false, "print discovery plan without connecting to SQL Server or writing files")
	connectionStringEnv := fs.String("connection-string-env", "", "environment variable containing the SQL Server connection string")
	timeout := fs.Duration("timeout", 30*time.Second, "SQL Server discovery timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dryRun {
		plan, err := gitops.BuildSQLServerDiscoveryDryRunPlan(*root, *sourceClusterID)
		if err != nil {
			fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "SQL Server discovery dry run for %s\n", plan.SourceClusterID)
		fmt.Fprintln(stdout, "No database connection will be opened.")
		fmt.Fprintf(stdout, "Writes files: %t\n", plan.WritesFiles)
		fmt.Fprintln(stdout, "\nTarget files:")
		for _, target := range plan.TargetFiles {
			fmt.Fprintf(stdout, "- %s\n", target)
		}
		fmt.Fprintln(stdout, "\nCatalog queries:")
		for _, query := range plan.CatalogQueries {
			fmt.Fprintf(stdout, "- %s\n", query)
		}
		return 0
	}
	if strings.TrimSpace(*connectionStringEnv) == "" {
		fmt.Fprintln(stderr, "discover-sqlserver: requires --connection-string-env unless --dry-run is set")
		return 2
	}
	connectionString := os.Getenv(*connectionStringEnv)
	if strings.TrimSpace(connectionString) == "" {
		fmt.Fprintf(stderr, "discover-sqlserver: environment variable %s is not set\n", *connectionStringEnv)
		return 1
	}
	reader, err := sqlservercatalog.NewCatalogReader(connectionString)
	if err != nil {
		fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
		return 1
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := gitops.ExecuteSQLServerDiscovery(ctx, *root, *sourceClusterID, reader)
	if err != nil {
		fmt.Fprintf(stderr, "discover sqlserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "SQL Server discovery completed for %s\n", result.SourceClusterID)
	fmt.Fprintf(stdout, "databases: %d, tables: %d, columns: %d, source DDL files: %d\n",
		result.Databases,
		result.Tables,
		result.Columns,
		result.SourceDDLs,
	)
	fmt.Fprintf(stdout, "wrote %s\n", result.InventoryFile)
	return 0
}

func runAnalyzeCompatibility(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze-compatibility", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	report, err := gitops.AnalyzeSQLServerCompatibility(*root, *sourceClusterID)
	if err != nil {
		fmt.Fprintf(stderr, "analyze compatibility: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "compatibility analysis completed for %s\n", report.SourceClusterID)
	fmt.Fprintf(stdout, "findings: %d, blockers: %d, warnings: %d, info: %d\n",
		report.Summary.TotalFindings,
		report.Summary.Blockers,
		report.Summary.Warnings,
		report.Summary.Info,
	)
	fmt.Fprintf(stdout, "wrote %s\n", "inventory/schema-issues.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "inventory/compatibility-report.md")
	return 0
}

func runLLMCompatibilityAdvice(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("llm-compatibility-advice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	providerConfigPath := fs.String("provider-config", "", "optional LLM provider config file; defaults to inline flags")
	providerID := fs.String("provider-id", "", "LLM provider id from provider config; defaults to config default_provider")
	providerType := fs.String("provider-type", llm.ProviderTypeOpenAICompatible, "LLM provider type")
	baseURL := fs.String("base-url", "", "OpenAI-compatible base URL, for example https://api.openai.com/v1")
	model := fs.String("model", "", "LLM model name")
	authMode := fs.String("auth-mode", llm.AuthModeAPIKey, "auth mode: api_key, oauth_client_credentials, oauth_refresh_token, oauth_token_env, external_command")
	apiKeyEnv := fs.String("api-key-env", "OPENAI_API_KEY", "API key environment variable for api_key auth")
	tokenEnv := fs.String("token-env", "", "access token environment variable for oauth_token_env auth")
	tokenURL := fs.String("token-url", "", "OAuth token URL for oauth_client_credentials or oauth_refresh_token auth")
	clientIDEnv := fs.String("client-id-env", "", "OAuth client id environment variable")
	clientSecretEnv := fs.String("client-secret-env", "", "OAuth client secret environment variable")
	refreshTokenEnv := fs.String("refresh-token-env", "", "OAuth refresh token environment variable")
	scopes := fs.String("scopes", "", "comma-separated OAuth scopes")
	externalCommand := fs.String("external-command", "", "external token command; disabled unless --allow-external-command is set")
	allowExternalCommand := fs.Bool("allow-external-command", false, "allow external_command auth to run a local command")
	execute := fs.Bool("execute", false, "call the LLM provider and write advice files; default is dry-run")
	timeout := fs.Duration("timeout", 2*time.Minute, "LLM provider request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	provider := llm.ProviderConfig{
		ID:      strings.TrimSpace(*providerID),
		Type:    strings.TrimSpace(*providerType),
		BaseURL: strings.TrimSpace(*baseURL),
		Model:   strings.TrimSpace(*model),
		Auth: llm.AuthConfig{
			Mode:                 strings.TrimSpace(*authMode),
			Env:                  strings.TrimSpace(*apiKeyEnv),
			TokenEnv:             strings.TrimSpace(*tokenEnv),
			TokenURL:             strings.TrimSpace(*tokenURL),
			ClientIDEnv:          strings.TrimSpace(*clientIDEnv),
			ClientSecretEnv:      strings.TrimSpace(*clientSecretEnv),
			RefreshTokenEnv:      strings.TrimSpace(*refreshTokenEnv),
			Scopes:               splitCommaList(*scopes),
			Command:              splitCommand(*externalCommand),
			AllowExternalCommand: *allowExternalCommand,
		},
	}
	var err error
	provider, err = resolveLLMProviderConfig(*root, *providerConfigPath, *providerID, provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm compatibility advice: %v\n", err)
		return 1
	}
	advice, err := buildLLMCompatibilityAdviceRequest(*root, *sourceClusterID)
	if err != nil {
		fmt.Fprintf(stderr, "llm compatibility advice: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintf(stdout, "LLM compatibility advice dry run for %s\n", advice.SourceClusterID)
		fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
		fmt.Fprintf(stdout, "provider type: %s\n", provider.Type)
		fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
		fmt.Fprintf(stdout, "model: %s\n", provider.Model)
		fmt.Fprintf(stdout, "input files: %d\n", len(advice.Inputs))
		fmt.Fprintf(stdout, "would write %s\n", advice.OutputFile)
		fmt.Fprintf(stdout, "would write %s\n", advice.AuditFile)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client, err := newLLMClientFromProvider(provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm compatibility advice: %v\n", err)
		return 1
	}
	response, err := client.Generate(ctx, advice.Request)
	if err != nil {
		fmt.Fprintf(stderr, "llm compatibility advice: %v\n", err)
		return 1
	}
	if err := writeLLMCompatibilityAdvice(*root, provider, advice, response); err != nil {
		fmt.Fprintf(stderr, "llm compatibility advice: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "LLM compatibility advice generated for %s\n", advice.SourceClusterID)
	fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
	fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
	fmt.Fprintf(stdout, "model: %s\n", provider.Model)
	fmt.Fprintf(stdout, "wrote %s\n", advice.OutputFile)
	fmt.Fprintf(stdout, "wrote %s\n", advice.AuditFile)
	return 0
}

func runLLMSchemaAdvice(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("llm-schema-advice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	providerConfigPath := fs.String("provider-config", "", "optional LLM provider config file; defaults to inline flags")
	providerID := fs.String("provider-id", "", "LLM provider id from provider config; defaults to config default_provider")
	providerType := fs.String("provider-type", llm.ProviderTypeOpenAICompatible, "LLM provider type")
	baseURL := fs.String("base-url", "", "OpenAI-compatible base URL, for example https://api.openai.com/v1")
	model := fs.String("model", "", "LLM model name")
	authMode := fs.String("auth-mode", llm.AuthModeAPIKey, "auth mode: api_key, oauth_client_credentials, oauth_refresh_token, oauth_token_env, external_command")
	apiKeyEnv := fs.String("api-key-env", "OPENAI_API_KEY", "API key environment variable for api_key auth")
	tokenEnv := fs.String("token-env", "", "access token environment variable for oauth_token_env auth")
	tokenURL := fs.String("token-url", "", "OAuth token URL for oauth_client_credentials or oauth_refresh_token auth")
	clientIDEnv := fs.String("client-id-env", "", "OAuth client id environment variable")
	clientSecretEnv := fs.String("client-secret-env", "", "OAuth client secret environment variable")
	refreshTokenEnv := fs.String("refresh-token-env", "", "OAuth refresh token environment variable")
	scopes := fs.String("scopes", "", "comma-separated OAuth scopes")
	externalCommand := fs.String("external-command", "", "external token command; disabled unless --allow-external-command is set")
	allowExternalCommand := fs.Bool("allow-external-command", false, "allow external_command auth to run a local command")
	execute := fs.Bool("execute", false, "call the LLM provider and write advice files; default is dry-run")
	timeout := fs.Duration("timeout", 2*time.Minute, "LLM provider request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	provider := llm.ProviderConfig{
		ID:      strings.TrimSpace(*providerID),
		Type:    strings.TrimSpace(*providerType),
		BaseURL: strings.TrimSpace(*baseURL),
		Model:   strings.TrimSpace(*model),
		Auth: llm.AuthConfig{
			Mode:                 strings.TrimSpace(*authMode),
			Env:                  strings.TrimSpace(*apiKeyEnv),
			TokenEnv:             strings.TrimSpace(*tokenEnv),
			TokenURL:             strings.TrimSpace(*tokenURL),
			ClientIDEnv:          strings.TrimSpace(*clientIDEnv),
			ClientSecretEnv:      strings.TrimSpace(*clientSecretEnv),
			RefreshTokenEnv:      strings.TrimSpace(*refreshTokenEnv),
			Scopes:               splitCommaList(*scopes),
			Command:              splitCommand(*externalCommand),
			AllowExternalCommand: *allowExternalCommand,
		},
	}
	var err error
	provider, err = resolveLLMProviderConfig(*root, *providerConfigPath, *providerID, provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm schema advice: %v\n", err)
		return 1
	}
	advice, err := buildLLMSchemaAdviceRequest(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "llm schema advice: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintf(stdout, "LLM schema advice dry run for %s\n", advice.ProjectID)
		fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
		fmt.Fprintf(stdout, "provider type: %s\n", provider.Type)
		fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
		fmt.Fprintf(stdout, "model: %s\n", provider.Model)
		fmt.Fprintf(stdout, "input files: %d\n", len(advice.Inputs))
		fmt.Fprintf(stdout, "would write %s\n", advice.OutputFile)
		fmt.Fprintf(stdout, "would write %s\n", advice.AuditFile)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client, err := newLLMClientFromProvider(provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm schema advice: %v\n", err)
		return 1
	}
	response, err := client.Generate(ctx, advice.Request)
	if err != nil {
		fmt.Fprintf(stderr, "llm schema advice: %v\n", err)
		return 1
	}
	if err := writeLLMSchemaAdvice(*root, provider, advice, response); err != nil {
		fmt.Fprintf(stderr, "llm schema advice: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "LLM schema advice generated for %s\n", advice.ProjectID)
	fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
	fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
	fmt.Fprintf(stdout, "model: %s\n", provider.Model)
	fmt.Fprintf(stdout, "wrote %s\n", advice.OutputFile)
	fmt.Fprintf(stdout, "wrote %s\n", advice.AuditFile)
	return 0
}

func runLLMMigrationStrategy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("llm-migration-strategy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	providerConfigPath := fs.String("provider-config", "", "optional LLM provider config file; defaults to inline flags")
	providerID := fs.String("provider-id", "", "LLM provider id from provider config; defaults to config default_provider")
	providerType := fs.String("provider-type", llm.ProviderTypeOpenAICompatible, "LLM provider type")
	baseURL := fs.String("base-url", "", "OpenAI-compatible base URL, for example https://api.openai.com/v1")
	model := fs.String("model", "", "LLM model name")
	authMode := fs.String("auth-mode", llm.AuthModeAPIKey, "auth mode: api_key, oauth_client_credentials, oauth_refresh_token, oauth_token_env, external_command")
	apiKeyEnv := fs.String("api-key-env", "OPENAI_API_KEY", "API key environment variable for api_key auth")
	tokenEnv := fs.String("token-env", "", "access token environment variable for oauth_token_env auth")
	tokenURL := fs.String("token-url", "", "OAuth token URL for oauth_client_credentials or oauth_refresh_token auth")
	clientIDEnv := fs.String("client-id-env", "", "OAuth client id environment variable")
	clientSecretEnv := fs.String("client-secret-env", "", "OAuth client secret environment variable")
	refreshTokenEnv := fs.String("refresh-token-env", "", "OAuth refresh token environment variable")
	scopes := fs.String("scopes", "", "comma-separated OAuth scopes")
	externalCommand := fs.String("external-command", "", "external token command; disabled unless --allow-external-command is set")
	allowExternalCommand := fs.Bool("allow-external-command", false, "allow external_command auth to run a local command")
	execute := fs.Bool("execute", false, "call the LLM provider and write advice files; default is dry-run")
	timeout := fs.Duration("timeout", 2*time.Minute, "LLM provider request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	provider := llm.ProviderConfig{
		ID:      strings.TrimSpace(*providerID),
		Type:    strings.TrimSpace(*providerType),
		BaseURL: strings.TrimSpace(*baseURL),
		Model:   strings.TrimSpace(*model),
		Auth: llm.AuthConfig{
			Mode:                 strings.TrimSpace(*authMode),
			Env:                  strings.TrimSpace(*apiKeyEnv),
			TokenEnv:             strings.TrimSpace(*tokenEnv),
			TokenURL:             strings.TrimSpace(*tokenURL),
			ClientIDEnv:          strings.TrimSpace(*clientIDEnv),
			ClientSecretEnv:      strings.TrimSpace(*clientSecretEnv),
			RefreshTokenEnv:      strings.TrimSpace(*refreshTokenEnv),
			Scopes:               splitCommaList(*scopes),
			Command:              splitCommand(*externalCommand),
			AllowExternalCommand: *allowExternalCommand,
		},
	}
	var err error
	provider, err = resolveLLMProviderConfig(*root, *providerConfigPath, *providerID, provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm migration strategy: %v\n", err)
		return 1
	}
	advice, err := buildLLMMigrationStrategyRequest(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "llm migration strategy: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintf(stdout, "LLM migration strategy dry run for %s\n", advice.ProjectID)
		fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
		fmt.Fprintf(stdout, "provider type: %s\n", provider.Type)
		fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
		fmt.Fprintf(stdout, "model: %s\n", provider.Model)
		fmt.Fprintf(stdout, "input files: %d\n", len(advice.Inputs))
		fmt.Fprintf(stdout, "would write %s\n", advice.OutputFile)
		fmt.Fprintf(stdout, "would write %s\n", advice.AuditFile)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client, err := newLLMClientFromProvider(provider)
	if err != nil {
		fmt.Fprintf(stderr, "llm migration strategy: %v\n", err)
		return 1
	}
	response, err := client.Generate(ctx, advice.Request)
	if err != nil {
		fmt.Fprintf(stderr, "llm migration strategy: %v\n", err)
		return 1
	}
	if err := writeLLMMigrationStrategyAdvice(*root, provider, advice, response); err != nil {
		fmt.Fprintf(stderr, "llm migration strategy: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "LLM migration strategy advice generated for %s\n", advice.ProjectID)
	fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
	fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
	fmt.Fprintf(stdout, "model: %s\n", provider.Model)
	fmt.Fprintf(stdout, "wrote %s\n", advice.OutputFile)
	fmt.Fprintf(stdout, "wrote %s\n", advice.AuditFile)
	return 0
}

type llmProjectAdviceCommand struct {
	CommandName     string
	DryRunLabel     string
	GeneratedLabel  string
	Task            string
	System          string
	OutputStyle     string
	OutputFileName  string
	RequiresStage   bool
	InputFileGroups func(clusterRel, projectRel, stage string) (required []string, optional []string)
}

func llmValidationAnalysisCommand() llmProjectAdviceCommand {
	return llmProjectAdviceCommand{
		CommandName:    "llm-validation-analysis",
		DryRunLabel:    "validation analysis",
		GeneratedLabel: "validation analysis",
		Task:           "validation_mismatch_analysis",
		System:         "You are a SQL Server to TiDB validation advisor. Produce advisory validation analysis only. Do not claim that any validation was rerun. Do not include secrets.",
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# Validation Mismatch Analysis'.",
			"Explain observed mismatches, likely schema/data/CDC causes, deterministic checks to rerun, and owner review focus.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
		OutputFileName: "validation-mismatch-analysis.md",
		InputFileGroups: func(clusterRel, projectRel, stage string) ([]string, []string) {
			return []string{
					filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml")),
				}, []string{
					filepath.ToSlash(filepath.Join(projectRel, "plan", "migration-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "schema", "schema-diff.json")),
					filepath.ToSlash(filepath.Join(projectRel, "schema", "conversion-report.md")),
					filepath.ToSlash(filepath.Join(projectRel, "state", "validation-status.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "validation-report.md")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "executor-validation-run.json")),
				}
		},
	}
}

func llmCutoverRiskCommand() llmProjectAdviceCommand {
	return llmProjectAdviceCommand{
		CommandName:    "llm-cutover-risk",
		DryRunLabel:    "cutover risk summary",
		GeneratedLabel: "cutover risk summary",
		Task:           "cutover_risk_summary",
		System:         "You are a SQL Server to TiDB cutover readiness advisor. Produce advisory risk text only. Do not claim that any cutover action was executed. Do not include secrets.",
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# Cutover Risk Summary'.",
			"Summarize cutover readiness, unresolved risks, rollback boundary checks, communication owner focus, and post-cutover verification.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
		OutputFileName: "cutover-risk-summary.md",
		InputFileGroups: func(clusterRel, projectRel, stage string) ([]string, []string) {
			return []string{
					filepath.ToSlash(filepath.Join(projectRel, "plan", "cutover-runbook.md")),
				}, []string{
					filepath.ToSlash(filepath.Join(projectRel, "plan", "migration-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "state", "migration-state.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "state", "validation-status.yaml")),
					filepath.ToSlash(filepath.Join(clusterRel, "state", "cdc-checkpoint.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "state", "cdc-health-history.jsonl")),
					filepath.ToSlash(filepath.Join(projectRel, "approvals", "cutover-approval.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "executor-export-run.json")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "executor-import-run.json")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "executor-cdc-run.json")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "executor-validation-run.json")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "validation-report.md")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "cutover-evidence.md")),
					filepath.ToSlash(filepath.Join(projectRel, "evidence", "post-cutover-report.md")),
				}
		},
	}
}

func llmPRSummaryCommand() llmProjectAdviceCommand {
	return llmProjectAdviceCommand{
		CommandName:    "llm-pr-summary",
		DryRunLabel:    "PR summary",
		GeneratedLabel: "PR summary",
		Task:           "pr_summary_advice",
		System:         "You are a SQL Server to TiDB GitOps PR reviewer assistant. Produce advisory PR summary text only. Do not approve the PR. Do not include secrets.",
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# PR Summary'.",
			"Summarize review intent, changed artifacts, risk areas, and reviewer focus using the PR draft and committed metadata.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
		OutputFileName: "pr-summary.md",
		RequiresStage:  true,
		InputFileGroups: func(clusterRel, projectRel, stage string) ([]string, []string) {
			return []string{
					filepath.ToSlash(filepath.Join(projectRel, "prs", stage+"-pr.md")),
				}, []string{
					filepath.ToSlash(filepath.Join(projectRel, "project.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "migration-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "schema", "schema-diff.json")),
					filepath.ToSlash(filepath.Join(projectRel, "schema", "conversion-report.md")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml")),
					filepath.ToSlash(filepath.Join(projectRel, "plan", "cutover-runbook.md")),
				}
		},
	}
}

func runLLMProjectAdvice(args []string, stdout, stderr io.Writer, command llmProjectAdviceCommand) int {
	fs := flag.NewFlagSet(command.CommandName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "PR stage for commands that summarize a generated PR draft")
	providerConfigPath := fs.String("provider-config", "", "optional LLM provider config file; defaults to inline flags")
	providerID := fs.String("provider-id", "", "LLM provider id from provider config; defaults to config default_provider")
	providerType := fs.String("provider-type", llm.ProviderTypeOpenAICompatible, "LLM provider type")
	baseURL := fs.String("base-url", "", "OpenAI-compatible base URL, for example https://api.openai.com/v1")
	model := fs.String("model", "", "LLM model name")
	authMode := fs.String("auth-mode", llm.AuthModeAPIKey, "auth mode: api_key, oauth_client_credentials, oauth_refresh_token, oauth_token_env, external_command")
	apiKeyEnv := fs.String("api-key-env", "OPENAI_API_KEY", "API key environment variable for api_key auth")
	tokenEnv := fs.String("token-env", "", "access token environment variable for oauth_token_env auth")
	tokenURL := fs.String("token-url", "", "OAuth token URL for oauth_client_credentials or oauth_refresh_token auth")
	clientIDEnv := fs.String("client-id-env", "", "OAuth client id environment variable")
	clientSecretEnv := fs.String("client-secret-env", "", "OAuth client secret environment variable")
	refreshTokenEnv := fs.String("refresh-token-env", "", "OAuth refresh token environment variable")
	scopes := fs.String("scopes", "", "comma-separated OAuth scopes")
	externalCommand := fs.String("external-command", "", "external token command; disabled unless --allow-external-command is set")
	allowExternalCommand := fs.Bool("allow-external-command", false, "allow external_command auth to run a local command")
	execute := fs.Bool("execute", false, "call the LLM provider and write advice files; default is dry-run")
	timeout := fs.Duration("timeout", 2*time.Minute, "LLM provider request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	provider := llm.ProviderConfig{
		ID:      strings.TrimSpace(*providerID),
		Type:    strings.TrimSpace(*providerType),
		BaseURL: strings.TrimSpace(*baseURL),
		Model:   strings.TrimSpace(*model),
		Auth: llm.AuthConfig{
			Mode:                 strings.TrimSpace(*authMode),
			Env:                  strings.TrimSpace(*apiKeyEnv),
			TokenEnv:             strings.TrimSpace(*tokenEnv),
			TokenURL:             strings.TrimSpace(*tokenURL),
			ClientIDEnv:          strings.TrimSpace(*clientIDEnv),
			ClientSecretEnv:      strings.TrimSpace(*clientSecretEnv),
			RefreshTokenEnv:      strings.TrimSpace(*refreshTokenEnv),
			Scopes:               splitCommaList(*scopes),
			Command:              splitCommand(*externalCommand),
			AllowExternalCommand: *allowExternalCommand,
		},
	}
	var err error
	provider, err = resolveLLMProviderConfig(*root, *providerConfigPath, *providerID, provider)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.ReplaceAll(command.CommandName, "-", " "), err)
		return 1
	}
	advice, err := buildLLMProjectAdviceRequest(*root, *sourceClusterID, *projectID, *stage, command)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.ReplaceAll(command.CommandName, "-", " "), err)
		return 1
	}
	if !*execute {
		fmt.Fprintf(stdout, "LLM %s dry run for %s\n", command.DryRunLabel, advice.ProjectID)
		fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
		fmt.Fprintf(stdout, "provider type: %s\n", provider.Type)
		fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
		fmt.Fprintf(stdout, "model: %s\n", provider.Model)
		fmt.Fprintf(stdout, "input files: %d\n", len(advice.Inputs))
		fmt.Fprintf(stdout, "would write %s\n", advice.OutputFile)
		fmt.Fprintf(stdout, "would write %s\n", advice.AuditFile)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client, err := newLLMClientFromProvider(provider)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.ReplaceAll(command.CommandName, "-", " "), err)
		return 1
	}
	response, err := client.Generate(ctx, advice.Request)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.ReplaceAll(command.CommandName, "-", " "), err)
		return 1
	}
	if err := writeLLMProjectAdvice(*root, provider, advice, response); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.ReplaceAll(command.CommandName, "-", " "), err)
		return 1
	}
	fmt.Fprintf(stdout, "LLM %s generated for %s\n", command.GeneratedLabel, advice.ProjectID)
	fmt.Fprintf(stdout, "provider id: %s\n", provider.ID)
	fmt.Fprintf(stdout, "auth mode: %s\n", provider.Auth.Mode)
	fmt.Fprintf(stdout, "model: %s\n", provider.Model)
	fmt.Fprintf(stdout, "wrote %s\n", advice.OutputFile)
	fmt.Fprintf(stdout, "wrote %s\n", advice.AuditFile)
	return 0
}

type llmCompatibilityAdviceRequest struct {
	SourceClusterID string
	Inputs          []llm.InputFile
	Request         llm.Request
	OutputFile      string
	AuditFile       string
	InputHashes     []llmAdviceInputHash
	PromptHash      string
}

type llmSchemaAdviceRequest struct {
	SourceClusterID string
	ProjectID       string
	Inputs          []llm.InputFile
	Request         llm.Request
	OutputFile      string
	AuditFile       string
	InputHashes     []llmAdviceInputHash
	PromptHash      string
}

type llmMigrationStrategyRequest struct {
	SourceClusterID string
	ProjectID       string
	Inputs          []llm.InputFile
	Request         llm.Request
	OutputFile      string
	AuditFile       string
	InputHashes     []llmAdviceInputHash
	PromptHash      string
}

type llmProjectAdviceRequest struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
	Inputs          []llm.InputFile
	Request         llm.Request
	OutputFile      string
	AuditFile       string
	InputHashes     []llmAdviceInputHash
	PromptHash      string
}

type llmAdviceInputHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func resolveLLMProviderConfig(root, configPath, providerID string, inline llm.ProviderConfig) (llm.ProviderConfig, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		defaultPath := filepath.Join(root, "global", "llm-providers.yaml")
		if _, err := os.Stat(defaultPath); err == nil {
			configPath = defaultPath
		}
	}
	if configPath == "" {
		if strings.TrimSpace(inline.ID) == "" {
			inline.ID = "inline"
		}
		if strings.TrimSpace(inline.Type) == "" {
			inline.Type = llm.ProviderTypeOpenAICompatible
		}
		if strings.TrimSpace(inline.Auth.Mode) == "" {
			inline.Auth.Mode = llm.AuthModeAPIKey
		}
		return inline, nil
	}
	data, err := os.Open(configPath)
	if err != nil {
		return llm.ProviderConfig{}, fmt.Errorf("open provider config: %w", err)
	}
	defer data.Close()
	loaded, err := llm.ParseProviderConfig(data)
	if err != nil {
		return llm.ProviderConfig{}, fmt.Errorf("parse provider config: %w", err)
	}
	provider, ok := loaded.Provider(providerID)
	if !ok {
		if strings.TrimSpace(providerID) == "" {
			return llm.ProviderConfig{}, fmt.Errorf("default provider %q not found in provider config", loaded.DefaultProvider)
		}
		return llm.ProviderConfig{}, fmt.Errorf("provider %q not found in provider config", providerID)
	}
	return provider, nil
}

func newLLMClientFromProvider(provider llm.ProviderConfig) (llm.Client, error) {
	if strings.TrimSpace(provider.Type) == "" {
		provider.Type = llm.ProviderTypeOpenAICompatible
	}
	tokenSource, err := llm.NewTokenSource(provider.Auth, llm.TokenSourceOptions{})
	if err != nil {
		return nil, err
	}
	switch provider.Type {
	case llm.ProviderTypeOpenAICompatible:
		return llm.NewOpenAICompatibleClient(llm.OpenAICompatibleConfig{
			BaseURL:     provider.BaseURL,
			Model:       provider.Model,
			TokenSource: tokenSource,
		})
	default:
		return nil, fmt.Errorf("unsupported LLM provider type %q", provider.Type)
	}
}

func buildLLMCompatibilityAdviceRequest(root, sourceClusterID string) (llmCompatibilityAdviceRequest, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	if sourceClusterID == "" {
		return llmCompatibilityAdviceRequest{}, fmt.Errorf("source cluster id is required")
	}
	baseRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "inventory"))
	inputRels := []string{
		filepath.ToSlash(filepath.Join(baseRel, "schema-issues.yaml")),
		filepath.ToSlash(filepath.Join(baseRel, "compatibility-report.md")),
	}
	inputs := make([]llm.InputFile, 0, len(inputRels))
	hashes := make([]llmAdviceInputHash, 0, len(inputRels))
	for _, rel := range inputRels {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return llmCompatibilityAdviceRequest{}, fmt.Errorf("read LLM input %s: %w", rel, err)
		}
		redacted := redact.Text(string(data))
		inputs = append(inputs, llm.InputFile{Path: rel, Content: redacted})
		hashes = append(hashes, llmAdviceInputHash{Path: rel, SHA256: sha256String(redacted)})
	}
	request := llm.Request{
		Task:   "compatibility_advice",
		System: "You are a SQL Server to TiDB migration advisor. Produce advisory text only. Do not claim that any database action was executed. Do not include secrets.",
		Inputs: inputs,
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# Compatibility Advice'.",
			"Explain blockers, warnings, owner review focus, and next deterministic GitOps steps.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
	}
	outputRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "ai", "compatibility-advice.md"))
	auditRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "ai", "compatibility-advice.audit.json"))
	return llmCompatibilityAdviceRequest{
		SourceClusterID: sourceClusterID,
		Inputs:          inputs,
		Request:         request,
		OutputFile:      outputRel,
		AuditFile:       auditRel,
		InputHashes:     hashes,
		PromptHash:      sha256String(renderLLMRequestForHash(request)),
	}, nil
}

func writeLLMCompatibilityAdvice(root string, provider llm.ProviderConfig, advice llmCompatibilityAdviceRequest, response llm.Response) error {
	outputPath := filepath.Join(root, filepath.FromSlash(advice.OutputFile))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create LLM advice directory: %w", err)
	}
	content := redact.Text(response.Text)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("LLM advice response is empty")
	}
	if err := os.WriteFile(outputPath, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write LLM advice: %w", err)
	}
	audit := struct {
		SourceClusterID string               `json:"source_cluster_id"`
		ProviderID      string               `json:"provider_id"`
		ProviderType    string               `json:"provider_type"`
		AuthMode        string               `json:"auth_mode"`
		Model           string               `json:"model"`
		PromptSHA256    string               `json:"prompt_sha256"`
		InputFiles      []llmAdviceInputHash `json:"input_files"`
		OutputFile      string               `json:"output_file"`
		Usage           llm.Usage            `json:"usage"`
		GeneratedAt     string               `json:"generated_at"`
	}{
		SourceClusterID: advice.SourceClusterID,
		ProviderID:      provider.ID,
		ProviderType:    provider.Type,
		AuthMode:        provider.Auth.Mode,
		Model:           response.Model,
		PromptSHA256:    advice.PromptHash,
		InputFiles:      advice.InputHashes,
		OutputFile:      advice.OutputFile,
		Usage:           response.Usage,
		GeneratedAt:     response.GeneratedAt,
	}
	if strings.TrimSpace(audit.ProviderID) == "" {
		audit.ProviderID = "inline"
	}
	if strings.TrimSpace(audit.ProviderType) == "" {
		audit.ProviderType = llm.ProviderTypeOpenAICompatible
	}
	if strings.TrimSpace(audit.AuthMode) == "" {
		audit.AuthMode = llm.AuthModeAPIKey
	}
	if strings.TrimSpace(audit.Model) == "" {
		audit.Model = provider.Model
	}
	if strings.TrimSpace(audit.GeneratedAt) == "" {
		audit.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LLM advice audit: %w", err)
	}
	data = append(data, '\n')
	auditPath := filepath.Join(root, filepath.FromSlash(advice.AuditFile))
	if err := os.WriteFile(auditPath, data, 0o644); err != nil {
		return fmt.Errorf("write LLM advice audit: %w", err)
	}
	return nil
}

func buildLLMSchemaAdviceRequest(root, sourceClusterID, projectID string) (llmSchemaAdviceRequest, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	if sourceClusterID == "" {
		return llmSchemaAdviceRequest{}, fmt.Errorf("source cluster id is required")
	}
	if projectID == "" {
		return llmSchemaAdviceRequest{}, fmt.Errorf("project id is required")
	}
	projectRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID, "projects", projectID))
	schemaRel := filepath.ToSlash(filepath.Join(projectRel, "schema"))
	inputRels := []string{
		filepath.ToSlash(filepath.Join(schemaRel, "schema-diff.json")),
		filepath.ToSlash(filepath.Join(schemaRel, "conversion-report.md")),
	}
	ddlDir := filepath.Join(root, filepath.FromSlash(schemaRel), "tidb-ddl")
	if entries, err := os.ReadDir(ddlDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			inputRels = append(inputRels, filepath.ToSlash(filepath.Join(schemaRel, "tidb-ddl", entry.Name())))
		}
	}
	inputs := make([]llm.InputFile, 0, len(inputRels))
	hashes := make([]llmAdviceInputHash, 0, len(inputRels))
	for _, rel := range inputRels {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return llmSchemaAdviceRequest{}, fmt.Errorf("read LLM input %s: %w", rel, err)
		}
		redacted := redact.Text(string(data))
		inputs = append(inputs, llm.InputFile{Path: rel, Content: redacted})
		hashes = append(hashes, llmAdviceInputHash{Path: rel, SHA256: sha256String(redacted)})
	}
	request := llm.Request{
		Task:   "schema_rewrite_candidates",
		System: "You are a SQL Server to TiDB schema migration advisor. Produce advisory candidate rewrites only. Do not claim that any DDL was executed. Do not include secrets.",
		Inputs: inputs,
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# Schema Rewrite Candidates'.",
			"Focus on manual review items, incompatible SQL Server types, generated TiDB DDL TODO comments, and owner review questions.",
			"Candidates must be explanatory and must not be presented as approved DDL.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
	}
	outputRel := filepath.ToSlash(filepath.Join(projectRel, "ai", "schema-rewrite-candidates.md"))
	auditRel := filepath.ToSlash(filepath.Join(projectRel, "ai", "schema-rewrite-candidates.audit.json"))
	return llmSchemaAdviceRequest{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Inputs:          inputs,
		Request:         request,
		OutputFile:      outputRel,
		AuditFile:       auditRel,
		InputHashes:     hashes,
		PromptHash:      sha256String(renderLLMRequestForHash(request)),
	}, nil
}

func writeLLMSchemaAdvice(root string, provider llm.ProviderConfig, advice llmSchemaAdviceRequest, response llm.Response) error {
	outputPath := filepath.Join(root, filepath.FromSlash(advice.OutputFile))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create LLM schema advice directory: %w", err)
	}
	content := redact.Text(response.Text)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("LLM schema advice response is empty")
	}
	if err := os.WriteFile(outputPath, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write LLM schema advice: %w", err)
	}
	audit := struct {
		SourceClusterID string               `json:"source_cluster_id"`
		ProjectID       string               `json:"project_id"`
		ProviderID      string               `json:"provider_id"`
		ProviderType    string               `json:"provider_type"`
		AuthMode        string               `json:"auth_mode"`
		Model           string               `json:"model"`
		PromptSHA256    string               `json:"prompt_sha256"`
		InputFiles      []llmAdviceInputHash `json:"input_files"`
		OutputFile      string               `json:"output_file"`
		Usage           llm.Usage            `json:"usage"`
		GeneratedAt     string               `json:"generated_at"`
	}{
		SourceClusterID: advice.SourceClusterID,
		ProjectID:       advice.ProjectID,
		ProviderID:      provider.ID,
		ProviderType:    provider.Type,
		AuthMode:        provider.Auth.Mode,
		Model:           response.Model,
		PromptSHA256:    advice.PromptHash,
		InputFiles:      advice.InputHashes,
		OutputFile:      advice.OutputFile,
		Usage:           response.Usage,
		GeneratedAt:     response.GeneratedAt,
	}
	if strings.TrimSpace(audit.ProviderID) == "" {
		audit.ProviderID = "inline"
	}
	if strings.TrimSpace(audit.ProviderType) == "" {
		audit.ProviderType = llm.ProviderTypeOpenAICompatible
	}
	if strings.TrimSpace(audit.AuthMode) == "" {
		audit.AuthMode = llm.AuthModeAPIKey
	}
	if strings.TrimSpace(audit.Model) == "" {
		audit.Model = provider.Model
	}
	if strings.TrimSpace(audit.GeneratedAt) == "" {
		audit.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LLM schema advice audit: %w", err)
	}
	data = append(data, '\n')
	auditPath := filepath.Join(root, filepath.FromSlash(advice.AuditFile))
	if err := os.WriteFile(auditPath, data, 0o644); err != nil {
		return fmt.Errorf("write LLM schema advice audit: %w", err)
	}
	return nil
}

func buildLLMMigrationStrategyRequest(root, sourceClusterID, projectID string) (llmMigrationStrategyRequest, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	if sourceClusterID == "" {
		return llmMigrationStrategyRequest{}, fmt.Errorf("source cluster id is required")
	}
	if projectID == "" {
		return llmMigrationStrategyRequest{}, fmt.Errorf("project id is required")
	}
	clusterRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID))
	projectRel := filepath.ToSlash(filepath.Join(clusterRel, "projects", projectID))
	requiredRels := []string{
		filepath.ToSlash(filepath.Join(clusterRel, "cluster.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "project.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "migration-plan.yaml")),
	}
	optionalRels := []string{
		filepath.ToSlash(filepath.Join(clusterRel, "inventory", "compatibility-report.md")),
		filepath.ToSlash(filepath.Join(projectRel, "schema", "schema-diff.json")),
		filepath.ToSlash(filepath.Join(projectRel, "schema", "conversion-report.md")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "export-plan.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "import-plan.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "cdc-plan.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "plan", "validation-plan.yaml")),
	}
	inputRels := append([]string{}, requiredRels...)
	for _, rel := range optionalRels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			inputRels = append(inputRels, rel)
		}
	}
	inputs := make([]llm.InputFile, 0, len(inputRels))
	hashes := make([]llmAdviceInputHash, 0, len(inputRels))
	for _, rel := range inputRels {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return llmMigrationStrategyRequest{}, fmt.Errorf("read LLM input %s: %w", rel, err)
		}
		redacted := redact.Text(string(data))
		inputs = append(inputs, llm.InputFile{Path: rel, Content: redacted})
		hashes = append(hashes, llmAdviceInputHash{Path: rel, SHA256: sha256String(redacted)})
	}
	request := llm.Request{
		Task:   "migration_strategy_advice",
		System: "You are a SQL Server to TiDB migration strategy advisor. Produce advisory strategy text only. Do not claim that any migration stage was executed. Do not include secrets.",
		Inputs: inputs,
		OutputStyle: strings.Join([]string{
			"Write Markdown.",
			"Start with '# Migration Strategy Advice'.",
			"Compare offline, short-downtime, and low-downtime fit using the committed metadata and plans.",
			"Call out review focus for schema, export/import, CDC, validation, and cutover readiness.",
			"Do not emit approval YAML, state YAML, executor commands, or credentials.",
		}, "\n"),
	}
	outputRel := filepath.ToSlash(filepath.Join(projectRel, "ai", "migration-strategy-advice.md"))
	auditRel := filepath.ToSlash(filepath.Join(projectRel, "ai", "migration-strategy-advice.audit.json"))
	return llmMigrationStrategyRequest{
		SourceClusterID: sourceClusterID,
		ProjectID:       projectID,
		Inputs:          inputs,
		Request:         request,
		OutputFile:      outputRel,
		AuditFile:       auditRel,
		InputHashes:     hashes,
		PromptHash:      sha256String(renderLLMRequestForHash(request)),
	}, nil
}

func writeLLMMigrationStrategyAdvice(root string, provider llm.ProviderConfig, advice llmMigrationStrategyRequest, response llm.Response) error {
	outputPath := filepath.Join(root, filepath.FromSlash(advice.OutputFile))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create LLM migration strategy directory: %w", err)
	}
	content := redact.Text(response.Text)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("LLM migration strategy response is empty")
	}
	if err := os.WriteFile(outputPath, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write LLM migration strategy advice: %w", err)
	}
	audit := struct {
		SourceClusterID string               `json:"source_cluster_id"`
		ProjectID       string               `json:"project_id"`
		ProviderID      string               `json:"provider_id"`
		ProviderType    string               `json:"provider_type"`
		AuthMode        string               `json:"auth_mode"`
		Model           string               `json:"model"`
		PromptSHA256    string               `json:"prompt_sha256"`
		InputFiles      []llmAdviceInputHash `json:"input_files"`
		OutputFile      string               `json:"output_file"`
		Usage           llm.Usage            `json:"usage"`
		GeneratedAt     string               `json:"generated_at"`
	}{
		SourceClusterID: advice.SourceClusterID,
		ProjectID:       advice.ProjectID,
		ProviderID:      provider.ID,
		ProviderType:    provider.Type,
		AuthMode:        provider.Auth.Mode,
		Model:           response.Model,
		PromptSHA256:    advice.PromptHash,
		InputFiles:      advice.InputHashes,
		OutputFile:      advice.OutputFile,
		Usage:           response.Usage,
		GeneratedAt:     response.GeneratedAt,
	}
	if strings.TrimSpace(audit.ProviderID) == "" {
		audit.ProviderID = "inline"
	}
	if strings.TrimSpace(audit.ProviderType) == "" {
		audit.ProviderType = llm.ProviderTypeOpenAICompatible
	}
	if strings.TrimSpace(audit.AuthMode) == "" {
		audit.AuthMode = llm.AuthModeAPIKey
	}
	if strings.TrimSpace(audit.Model) == "" {
		audit.Model = provider.Model
	}
	if strings.TrimSpace(audit.GeneratedAt) == "" {
		audit.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LLM migration strategy audit: %w", err)
	}
	data = append(data, '\n')
	auditPath := filepath.Join(root, filepath.FromSlash(advice.AuditFile))
	if err := os.WriteFile(auditPath, data, 0o644); err != nil {
		return fmt.Errorf("write LLM migration strategy audit: %w", err)
	}
	return nil
}

func buildLLMProjectAdviceRequest(root, sourceClusterID, projectID, stage string, command llmProjectAdviceCommand) (llmProjectAdviceRequest, error) {
	sourceClusterID = strings.TrimSpace(sourceClusterID)
	projectID = strings.TrimSpace(projectID)
	stage = strings.ToLower(strings.TrimSpace(stage))
	if sourceClusterID == "" {
		return llmProjectAdviceRequest{}, fmt.Errorf("source cluster id is required")
	}
	if projectID == "" {
		return llmProjectAdviceRequest{}, fmt.Errorf("project id is required")
	}
	if command.RequiresStage && stage == "" {
		return llmProjectAdviceRequest{}, fmt.Errorf("stage is required")
	}
	clusterRel := filepath.ToSlash(filepath.Join("clusters", sourceClusterID))
	projectRel := filepath.ToSlash(filepath.Join(clusterRel, "projects", projectID))
	requiredRels := []string{
		filepath.ToSlash(filepath.Join(clusterRel, "cluster.yaml")),
		filepath.ToSlash(filepath.Join(projectRel, "project.yaml")),
	}
	if command.InputFileGroups != nil {
		required, optional := command.InputFileGroups(clusterRel, projectRel, stage)
		requiredRels = append(requiredRels, required...)
		inputs, hashes, err := readLLMAdviceInputs(root, requiredRels, optional)
		if err != nil {
			return llmProjectAdviceRequest{}, err
		}
		request := llm.Request{
			Task:        command.Task,
			System:      command.System,
			Inputs:      inputs,
			OutputStyle: command.OutputStyle,
		}
		outputRel := filepath.ToSlash(filepath.Join(projectRel, "ai", command.OutputFileName))
		auditName := strings.TrimSuffix(command.OutputFileName, ".md") + ".audit.json"
		auditRel := filepath.ToSlash(filepath.Join(projectRel, "ai", auditName))
		return llmProjectAdviceRequest{
			SourceClusterID: sourceClusterID,
			ProjectID:       projectID,
			Stage:           stage,
			Inputs:          inputs,
			Request:         request,
			OutputFile:      outputRel,
			AuditFile:       auditRel,
			InputHashes:     hashes,
			PromptHash:      sha256String(renderLLMRequestForHash(request)),
		}, nil
	}
	return llmProjectAdviceRequest{}, fmt.Errorf("LLM command %s has no input files configured", command.CommandName)
}

func readLLMAdviceInputs(root string, requiredRels, optionalRels []string) ([]llm.InputFile, []llmAdviceInputHash, error) {
	inputRels := append([]string{}, requiredRels...)
	for _, rel := range optionalRels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			inputRels = append(inputRels, rel)
		}
	}
	seen := map[string]struct{}{}
	inputs := make([]llm.InputFile, 0, len(inputRels))
	hashes := make([]llmAdviceInputHash, 0, len(inputRels))
	for _, rel := range inputRels {
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, nil, fmt.Errorf("read LLM input %s: %w", rel, err)
		}
		redacted := redact.Text(string(data))
		inputs = append(inputs, llm.InputFile{Path: rel, Content: redacted})
		hashes = append(hashes, llmAdviceInputHash{Path: rel, SHA256: sha256String(redacted)})
	}
	return inputs, hashes, nil
}

func writeLLMProjectAdvice(root string, provider llm.ProviderConfig, advice llmProjectAdviceRequest, response llm.Response) error {
	outputPath := filepath.Join(root, filepath.FromSlash(advice.OutputFile))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create LLM project advice directory: %w", err)
	}
	content := redact.Text(response.Text)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("LLM project advice response is empty")
	}
	if err := os.WriteFile(outputPath, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write LLM project advice: %w", err)
	}
	audit := struct {
		SourceClusterID string               `json:"source_cluster_id"`
		ProjectID       string               `json:"project_id"`
		Stage           string               `json:"stage,omitempty"`
		ProviderID      string               `json:"provider_id"`
		ProviderType    string               `json:"provider_type"`
		AuthMode        string               `json:"auth_mode"`
		Model           string               `json:"model"`
		PromptSHA256    string               `json:"prompt_sha256"`
		InputFiles      []llmAdviceInputHash `json:"input_files"`
		OutputFile      string               `json:"output_file"`
		Usage           llm.Usage            `json:"usage"`
		GeneratedAt     string               `json:"generated_at"`
	}{
		SourceClusterID: advice.SourceClusterID,
		ProjectID:       advice.ProjectID,
		Stage:           advice.Stage,
		ProviderID:      provider.ID,
		ProviderType:    provider.Type,
		AuthMode:        provider.Auth.Mode,
		Model:           response.Model,
		PromptSHA256:    advice.PromptHash,
		InputFiles:      advice.InputHashes,
		OutputFile:      advice.OutputFile,
		Usage:           response.Usage,
		GeneratedAt:     response.GeneratedAt,
	}
	if strings.TrimSpace(audit.ProviderID) == "" {
		audit.ProviderID = "inline"
	}
	if strings.TrimSpace(audit.ProviderType) == "" {
		audit.ProviderType = llm.ProviderTypeOpenAICompatible
	}
	if strings.TrimSpace(audit.AuthMode) == "" {
		audit.AuthMode = llm.AuthModeAPIKey
	}
	if strings.TrimSpace(audit.Model) == "" {
		audit.Model = provider.Model
	}
	if strings.TrimSpace(audit.GeneratedAt) == "" {
		audit.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LLM project advice audit: %w", err)
	}
	data = append(data, '\n')
	auditPath := filepath.Join(root, filepath.FromSlash(advice.AuditFile))
	if err := os.WriteFile(auditPath, data, 0o644); err != nil {
		return fmt.Errorf("write LLM project advice audit: %w", err)
	}
	return nil
}

func renderLLMRequestForHash(request llm.Request) string {
	var b strings.Builder
	b.WriteString(request.Task)
	b.WriteByte('\n')
	b.WriteString(request.System)
	b.WriteByte('\n')
	b.WriteString(request.OutputStyle)
	b.WriteByte('\n')
	b.WriteString(request.JSONSchema)
	b.WriteByte('\n')
	for _, input := range request.Inputs {
		b.WriteString(input.Path)
		b.WriteByte('\n')
		b.WriteString(input.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func splitCommaList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	raw := strings.Split(value, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func splitCommand(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.Fields(value)
}

func runGenerateSchemaDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-schema-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateSchemaDraft(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "generate schema draft: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "schema draft generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "tables: %d, columns: %d, manual review items: %d\n",
		result.Tables,
		result.Columns,
		result.ManualReviewItems,
	)
	fmt.Fprintf(stdout, "wrote %s\n", "schema/tidb-ddl")
	fmt.Fprintf(stdout, "wrote %s\n", "schema/conversion-report.md")
	fmt.Fprintf(stdout, "wrote %s\n", "schema/schema-diff.json")
	return 0
}

func runGenerateDataPlans(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-data-plans", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	objectURIPrefix := fs.String("object-uri-prefix", "", "CSV output URI prefix for exported full-load files; sql-insert supports file/http(s)/s3/gs/azblob, tidb-import-into supports file/s3/gs, tidb-lightning supports file/s3/gs/azblob")
	chunkSizeRows := fs.Int64("chunk-size-rows", 1000000, "estimated rows per export chunk")
	exportFormat := fs.String("export-format", "csv", "export file format")
	importEngine := fs.String("import-engine", "sql-insert", "TiDB import engine: sql-insert, tidb-import-into, or tidb-lightning")
	compression := fs.String("compression", "none", "export/import compression: none or gzip")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateDataMovementPlans(*root, *sourceClusterID, *projectID, gitops.DataMovementPlanSpec{
		ObjectURIPrefix: *objectURIPrefix,
		ChunkSizeRows:   *chunkSizeRows,
		ExportFormat:    *exportFormat,
		ImportEngine:    *importEngine,
		Compression:     *compression,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate data plans: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "data movement plans generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "tables: %d\n", result.Tables)
	fmt.Fprintf(stdout, "export chunks: %d\n", result.ExportChunks)
	fmt.Fprintf(stdout, "import jobs: %d\n", result.ImportJobs)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/export-plan.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "plan/import-plan.yaml")
	return 0
}

func runRepairSchemaDrift(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("repair-schema-drift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	apply := fs.Bool("apply", false, "regenerate schema/data/CDC/validation draft files when drift is auto-repairable")
	prDraft := fs.Bool("pr-draft", false, "write a schema drift repair PR draft")
	objectURIPrefix := fs.String("object-uri-prefix", "", "CSV output URI prefix used when --apply regenerates export/import plans")
	chunkSizeRows := fs.Int64("chunk-size-rows", 1000000, "estimated rows per export chunk when --apply regenerates export/import plans")
	exportFormat := fs.String("export-format", "csv", "export file format")
	importEngine := fs.String("import-engine", "sql-insert", "TiDB import engine: sql-insert, tidb-import-into, or tidb-lightning")
	compression := fs.String("compression", "none", "export/import compression: none or gzip")
	cdcMode := fs.String("cdc-mode", "sqlserver-cdc", "CDC mode used when --apply regenerates cdc-plan.yaml")
	retentionHoursRequired := fs.Int("retention-hours-required", 168, "SQL Server CDC retention hours required when --apply regenerates cdc-plan.yaml")
	cdcApplyBatchSize := fs.Int("cdc-apply-batch-size", 1000, "CDC apply batch size when --apply regenerates cdc-plan.yaml")
	cdcRoleName := fs.String("cdc-role-name", "", "SQL Server CDC role_name when --apply regenerates cdc-plan.yaml")
	cdcSupportsNetChanges := fs.Bool("cdc-supports-net-changes", false, "set supports_net_changes when --apply regenerates cdc-plan.yaml")
	includeChecksum := fs.Bool("include-checksum", false, "include scalar-query checksum checks when --apply regenerates validation-plan.yaml")
	includeSampledHash := fs.Bool("include-sampled-hash", false, "include scalar-query sampled_hash checks when --apply regenerates validation-plan.yaml")
	includeBucketedCount := fs.Bool("include-bucketed-count", false, "include scalar-query bucketed_count checks when --apply regenerates validation-plan.yaml")
	sampleModulo := fs.Int("sample-modulo", 100, "modulo used by sampled_hash checks")
	bucketCount := fs.Int("bucket-count", 16, "bucket count used by bucketed_count checks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RepairSchemaDrift(*root, *sourceClusterID, *projectID, gitops.SchemaDriftRepairSpec{
		Apply:        *apply,
		WritePRDraft: *prDraft,
		DataPlan: gitops.DataMovementPlanSpec{
			ObjectURIPrefix: *objectURIPrefix,
			ChunkSizeRows:   *chunkSizeRows,
			ExportFormat:    *exportFormat,
			ImportEngine:    *importEngine,
			Compression:     *compression,
		},
		CDCPlan: gitops.CDCPlanSpec{
			Mode:                   *cdcMode,
			RetentionHoursRequired: *retentionHoursRequired,
			ApplyBatchSize:         *cdcApplyBatchSize,
			RoleName:               *cdcRoleName,
			SupportsNetChanges:     *cdcSupportsNetChanges,
		},
		ValidationPlan: gitops.ValidationPlanSpec{
			IncludeChecksum:      *includeChecksum,
			IncludeSampledHash:   *includeSampledHash,
			IncludeBucketedCount: *includeBucketedCount,
			SampleModulo:         *sampleModulo,
			BucketCount:          *bucketCount,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "repair schema drift: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "schema drift repair completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "drift issues: %d\n", len(result.Issues))
	fmt.Fprintf(stdout, "drift detected: %t\n", result.DriftDetected)
	fmt.Fprintf(stdout, "applied: %t\n", result.Applied)
	fmt.Fprintf(stdout, "report file: %s\n", result.ReportFile)
	if result.PRDraftFile != "" {
		fmt.Fprintf(stdout, "PR draft: %s\n", result.PRDraftFile)
	}
	if len(result.UpdatedFiles) > 0 {
		fmt.Fprintf(stdout, "updated files: %d\n", len(result.UpdatedFiles))
	}
	return 0
}

func runGenerateCDCPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-cdc-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	mode := fs.String("mode", "sqlserver-cdc", "CDC mode")
	retentionHours := fs.Int("retention-hours", 168, "required CDC retention hours")
	applyBatchSize := fs.Int("apply-batch-size", 1000, "planned TiDB CDC apply batch size")
	roleName := fs.String("role-name", "", "optional SQL Server CDC gating role name to render into tracked tables")
	supportsNetChanges := fs.Bool("supports-net-changes", false, "render SQL Server CDC net changes support for tracked tables")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateCDCPlan(*root, *sourceClusterID, *projectID, gitops.CDCPlanSpec{
		Mode:                   *mode,
		RetentionHoursRequired: *retentionHours,
		ApplyBatchSize:         *applyBatchSize,
		RoleName:               *roleName,
		SupportsNetChanges:     *supportsNetChanges,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate cdc plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc plan generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "mode: %s\n", result.Mode)
	fmt.Fprintf(stdout, "tracked tables: %d\n", result.Tables)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	return 0
}

func runPrepareCDCRange(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prepare-cdc-range", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	toLSN := fs.String("to-lsn", "", "CDC to LSN for the next reviewed range")
	var minLSNs cdcHealthMinLSNFlags
	fs.Var(&minLSNs, "min-lsn", "per-table SQL Server CDC min LSN as source.object=0x...")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.PrepareCDCPlanRange(*root, *sourceClusterID, *projectID, gitops.CDCPlanRangeSpec{
		FromLSN: *fromLSN,
		ToLSN:   *toLSN,
		MinLSNs: minLSNs.Map(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare cdc range: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc range prepared for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	return 0
}

func runPrepareCDCIteration(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prepare-cdc-iteration", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	maxLSN := fs.String("max-lsn", "", "latest SQL Server CDC max LSN for this iteration")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	prDraft := fs.Bool("pr-draft", false, "write a CDC range PR draft alongside the updated plan")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.PrepareCDCIteration(*root, *sourceClusterID, *projectID, gitops.CDCIterationSpec{
		MaxLSN:         *maxLSN,
		InitialFromLSN: *fromLSN,
		WritePRDraft:   *prDraft,
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare cdc iteration: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc iteration prepared for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "max_lsn: %s\n", result.MaxLSN)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	if result.Status == gitops.CDCIterationStatusRangePrepared {
		fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
	}
	if result.PRBodyFile != "" {
		fmt.Fprintf(stdout, "PR draft: %s\n", result.PRBodyFile)
	}
	return 0
}

func runCDCOrchestrator(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc-orchestrator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	executorBinary := fs.String("executor-binary", "sqlserver2tidb-executor", "executor binary used to probe SQL Server CDC max LSN")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING", "environment variable containing the SQL Server CDC connection string")
	targetConnectionStringEnv := fs.String("target-connection-string-env", "SQLSERVER2TIDB_TARGET_CONNECTION_STRING", "environment variable containing the TiDB/MySQL connection string for CDC apply")
	maxLSNOverride := fs.String("max-lsn", "", "skip executor probing and use this SQL Server CDC max LSN")
	fromLSN := fs.String("from-lsn", "", "initial CDC from LSN for tables without checkpoint state")
	prDraft := fs.Bool("pr-draft", false, "write a CDC range PR draft when a new range is prepared")
	skipRetentionCheck := fs.Bool("skip-retention-check", false, "skip per-table SQL Server CDC min LSN retention checks")
	applyApproved := fs.Bool("apply-approved", false, "execute an already approved CDC range before probing the next SQL Server max LSN")
	checkpointStatus := fs.String("checkpoint-status", "running", "checkpoint status to write after approved CDC apply: running or caught_up")
	commandTimeout := fs.Duration("command-timeout", 0, "maximum runtime per CDC apply executor command; 0 disables the timeout")
	commandRetries := fs.Int("command-retries", 0, "number of retries for a failed CDC apply executor command")
	retryBackoff := fs.Duration("retry-backoff", time.Second, "fixed backoff between CDC apply executor command retries")
	resume := fs.Bool("resume", false, "skip matching successful CDC apply commands from existing executor evidence")
	minAppliedChanges := fs.Int("min-applied-changes", 0, "minimum total CDC applied changes required before the orchestrator can exit successfully")
	poll := fs.Bool("poll", false, "continue polling when the project is caught up")
	maxIterations := fs.Int("max-iterations", 0, "maximum probe iterations; 0 means unlimited")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between caught-up polling iterations")
	idleIterations := fs.Int("idle-iterations", 0, "maximum consecutive caught-up polls in --poll mode; 0 means unlimited")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *maxIterations < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --max-iterations must be non-negative")
		return 2
	}
	if *interval < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --interval must be non-negative")
		return 2
	}
	if *idleIterations < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --idle-iterations must be non-negative")
		return 2
	}
	if *commandTimeout < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --command-timeout must be non-negative")
		return 2
	}
	if *commandRetries < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --command-retries must be non-negative")
		return 2
	}
	if *retryBackoff < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --retry-backoff must be non-negative")
		return 2
	}
	if *minAppliedChanges < 0 {
		fmt.Fprintln(stderr, "cdc-orchestrator --min-applied-changes must be non-negative")
		return 2
	}

	fmt.Fprintln(stdout, "cdc orchestrator")
	prepared := 0
	applied := 0
	appliedChanges := 0
	idle := 0
	finish := func() int {
		fmt.Fprintf(stdout, "prepared iterations: %d\n", prepared)
		fmt.Fprintf(stdout, "applied iterations: %d\n", applied)
		fmt.Fprintf(stdout, "applied changes: %d\n", appliedChanges)
		if appliedChanges < *minAppliedChanges {
			fmt.Fprintf(stderr, "cdc orchestrator: applied changes %d below required minimum %d\n", appliedChanges, *minAppliedChanges)
			return 1
		}
		return 0
	}
	for iteration := 1; ; iteration++ {
		if *maxIterations > 0 && iteration > *maxIterations {
			return finish()
		}
		if *applyApproved {
			status, err := runCDCOrchestratorApplyApproved(cdcOrchestratorApplySpec{
				Root:                      *root,
				SourceClusterID:           *sourceClusterID,
				ProjectID:                 *projectID,
				ExecutorBinary:            *executorBinary,
				SourceConnectionStringEnv: *sourceConnectionStringEnv,
				TargetConnectionStringEnv: *targetConnectionStringEnv,
				CheckpointStatus:          *checkpointStatus,
				CommandTimeout:            *commandTimeout,
				CommandRetries:            *commandRetries,
				RetryBackoff:              *retryBackoff,
				Resume:                    *resume,
				SkipRetentionCheck:        *skipRetentionCheck,
			}, stdout, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "cdc orchestrator: %s\n", redact.Text(err.Error()))
				return 1
			}
			if status.Applied {
				applied++
				appliedChanges += status.AppliedChanges
			}
		}
		bounds, err := cdcOrchestratorProbeLSNBounds(*root, *sourceClusterID, *projectID, *executorBinary, *sourceConnectionStringEnv, *maxLSNOverride, *skipRetentionCheck)
		if err != nil {
			fmt.Fprintf(stderr, "cdc orchestrator: %s\n", redact.Text(err.Error()))
			return 1
		}
		fmt.Fprintf(stdout, "iteration %d: max_lsn %s\n", iteration, bounds.MaxLSN)
		result, err := gitops.PrepareCDCIteration(*root, *sourceClusterID, *projectID, gitops.CDCIterationSpec{
			MaxLSN:         bounds.MaxLSN,
			InitialFromLSN: *fromLSN,
			WritePRDraft:   *prDraft,
			MinLSNs:        bounds.MinLSNs,
		})
		if err != nil {
			fmt.Fprintf(stderr, "cdc orchestrator: %s\n", redact.Text(err.Error()))
			return 1
		}
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		if result.Status == gitops.CDCIterationStatusRangePrepared {
			prepared++
			fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
			fmt.Fprintf(stdout, "wrote %s\n", "plan/cdc-plan.yaml")
			if result.PRBodyFile != "" {
				fmt.Fprintf(stdout, "PR draft: %s\n", result.PRBodyFile)
			}
			return finish()
		}
		if result.Status != gitops.CDCIterationStatusCaughtUp {
			fmt.Fprintf(stderr, "cdc orchestrator: unsupported cdc iteration status %q\n", result.Status)
			return 1
		}
		if !*poll {
			return finish()
		}
		idle++
		fmt.Fprintf(stdout, "idle iteration %d: caught_up\n", idle)
		if *idleIterations > 0 && idle >= *idleIterations {
			return finish()
		}
		time.Sleep(*interval)
	}
}

type cdcOrchestratorApplySpec struct {
	Root                      string
	SourceClusterID           string
	ProjectID                 string
	ExecutorBinary            string
	SourceConnectionStringEnv string
	TargetConnectionStringEnv string
	CheckpointStatus          string
	CommandTimeout            time.Duration
	CommandRetries            int
	RetryBackoff              time.Duration
	Resume                    bool
	SkipRetentionCheck        bool
}

type cdcOrchestratorApplyStatus struct {
	Applied        bool
	AppliedChanges int
}

func runCDCOrchestratorApplyApproved(spec cdcOrchestratorApplySpec, stdout, stderr io.Writer) (cdcOrchestratorApplyStatus, error) {
	_, err := gitops.PrepareWorkerExecutor(spec.Root, spec.SourceClusterID, spec.ProjectID, "cdc", gitops.WorkerExecutorPrepareSpec{
		Binary:                    spec.ExecutorBinary,
		SourceConnectionStringEnv: spec.SourceConnectionStringEnv,
		TargetConnectionStringEnv: spec.TargetConnectionStringEnv,
	})
	if err != nil {
		if isCDCOrchestratorApplyNotReadyError(err) {
			fmt.Fprintf(stdout, "approved cdc apply not ready: %v\n", err)
			return cdcOrchestratorApplyStatus{}, nil
		}
		return cdcOrchestratorApplyStatus{}, err
	}
	applyStatus, err := gitops.CheckCDCPlanApplyStatus(spec.Root, spec.SourceClusterID, spec.ProjectID)
	if err != nil {
		return cdcOrchestratorApplyStatus{}, err
	}
	if applyStatus.Needed && !spec.SkipRetentionCheck {
		minLSNs, err := cdcOrchestratorMinLSNs(spec.Root, spec.SourceClusterID, spec.ProjectID, spec.ExecutorBinary, spec.SourceConnectionStringEnv)
		if err != nil {
			return cdcOrchestratorApplyStatus{}, err
		}
		applyStatus, err = gitops.CheckCDCPlanApplyStatusWithSpec(spec.Root, spec.SourceClusterID, spec.ProjectID, gitops.CDCPlanApplyStatusSpec{
			MinLSNs: minLSNs,
		})
		if err != nil {
			return cdcOrchestratorApplyStatus{}, err
		}
	}
	if !applyStatus.Needed {
		fmt.Fprintf(stdout, "approved cdc apply skipped: %s\n", applyStatus.Reason)
		return cdcOrchestratorApplyStatus{}, nil
	}
	args := []string{
		"worker-executor",
		"--root", spec.Root,
		"--source-cluster-id", spec.SourceClusterID,
		"--project-id", spec.ProjectID,
		"--stage", "cdc",
		"--executor-binary", spec.ExecutorBinary,
		"--source-connection-string-env", spec.SourceConnectionStringEnv,
		"--target-connection-string-env", spec.TargetConnectionStringEnv,
		"--execute",
	}
	if spec.CommandTimeout > 0 {
		args = append(args, "--command-timeout", spec.CommandTimeout.String())
	}
	if spec.CommandRetries > 0 {
		args = append(args, "--command-retries", strconv.Itoa(spec.CommandRetries))
	}
	if spec.RetryBackoff != time.Second {
		args = append(args, "--retry-backoff", spec.RetryBackoff.String())
	}
	if spec.Resume {
		args = append(args, "--resume")
	}
	if code := runWorkerExecutor(args[1:], stdout, stderr); code != 0 {
		return cdcOrchestratorApplyStatus{}, fmt.Errorf("approved cdc apply failed")
	}
	result, err := gitops.AdvanceCDCCheckpointFromExecutorEvidence(spec.Root, spec.SourceClusterID, spec.ProjectID, gitops.CDCCheckpointAdvanceSpec{
		Status: spec.CheckpointStatus,
	})
	if err != nil {
		return cdcOrchestratorApplyStatus{}, err
	}
	fmt.Fprintln(stdout, "approved cdc apply completed")
	fmt.Fprintf(stdout, "checkpoint status: %s\n", result.Status)
	fmt.Fprintf(stdout, "checkpoint updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "checkpoint applied changes: %d\n", result.AppliedChanges)
	fmt.Fprintf(stdout, "wrote %s\n", result.CheckpointFile)
	return cdcOrchestratorApplyStatus{Applied: true, AppliedChanges: result.AppliedChanges}, nil
}

func isCDCOrchestratorApplyNotReadyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "cdc approval is not approved") ||
		strings.Contains(message, "cdc approval payload hash mismatch") ||
		strings.Contains(message, "cdc plan status is ") ||
		strings.Contains(message, "read approval:")
}

type cdcOrchestratorLSNBounds struct {
	MaxLSN  string
	MinLSNs map[string]string
}

func cdcOrchestratorProbeLSNBounds(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride string, skipRetentionCheck bool) (cdcOrchestratorLSNBounds, error) {
	maxLSN, err := cdcOrchestratorMaxLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride)
	if err != nil {
		return cdcOrchestratorLSNBounds{}, err
	}
	minLSNs := map[string]string{}
	if !skipRetentionCheck {
		minLSNs, err = cdcOrchestratorMinLSNs(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv)
		if err != nil {
			return cdcOrchestratorLSNBounds{}, err
		}
	}
	return cdcOrchestratorLSNBounds{
		MaxLSN:  maxLSN,
		MinLSNs: minLSNs,
	}, nil
}

func cdcOrchestratorMaxLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, maxLSNOverride string) (string, error) {
	if maxLSN := strings.TrimSpace(maxLSNOverride); maxLSN != "" {
		return maxLSN, nil
	}
	binary := strings.TrimSpace(executorBinary)
	if binary == "" {
		binary = "sqlserver2tidb-executor"
	}
	args := []string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", strings.TrimSpace(sourceClusterID),
		"--project-id", strings.TrimSpace(projectID),
	}
	if strings.TrimSpace(sourceConnectionStringEnv) != "" {
		args = append(args, "--source-connection-string-env", strings.TrimSpace(sourceConnectionStringEnv))
	}
	cmd := newWorkerExecutorCommand(context.Background(), binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cdc-lsn probe failed: %w: %s", err, redact.Text(strings.TrimSpace(string(output))))
	}
	maxLSN, err := parseCDCMaxLSNOutput(string(output))
	if err != nil {
		return "", err
	}
	return maxLSN, nil
}

func cdcOrchestratorMinLSNs(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv string) (map[string]string, error) {
	sourceObjects, err := gitops.ListCDCTrackedSourceObjects(root, sourceClusterID, projectID)
	if err != nil {
		return nil, err
	}
	minLSNs := make(map[string]string, len(sourceObjects))
	for _, sourceObject := range sourceObjects {
		minLSN, err := cdcOrchestratorMinLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, sourceObject)
		if err != nil {
			return nil, err
		}
		minLSNs[sourceObject] = minLSN
	}
	return minLSNs, nil
}

func cdcOrchestratorMinLSN(root, sourceClusterID, projectID, executorBinary, sourceConnectionStringEnv, sourceObject string) (string, error) {
	binary := strings.TrimSpace(executorBinary)
	if binary == "" {
		binary = "sqlserver2tidb-executor"
	}
	args := []string{
		"cdc-lsn",
		"--execute",
		"--root", ".",
		"--source-cluster-id", strings.TrimSpace(sourceClusterID),
		"--project-id", strings.TrimSpace(projectID),
		"--source-object", strings.TrimSpace(sourceObject),
	}
	if strings.TrimSpace(sourceConnectionStringEnv) != "" {
		args = append(args, "--source-connection-string-env", strings.TrimSpace(sourceConnectionStringEnv))
	}
	cmd := newWorkerExecutorCommand(context.Background(), binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cdc-lsn min probe failed for %s: %w: %s", sourceObject, err, redact.Text(strings.TrimSpace(string(output))))
	}
	minLSN, err := parseCDCMinLSNOutput(string(output))
	if err != nil {
		return "", fmt.Errorf("cdc-lsn min probe failed for %s: %w", sourceObject, err)
	}
	return minLSN, nil
}

func parseCDCMaxLSNOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "max_lsn:") {
			maxLSN := strings.TrimSpace(strings.TrimPrefix(line, "max_lsn:"))
			if maxLSN == "" {
				return "", fmt.Errorf("cdc-lsn output max_lsn is empty")
			}
			return maxLSN, nil
		}
	}
	return "", fmt.Errorf("cdc-lsn output did not include max_lsn")
}

func parseCDCMinLSNOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "min_lsn:") {
			minLSN := strings.TrimSpace(strings.TrimPrefix(line, "min_lsn:"))
			if minLSN == "" {
				return "", fmt.Errorf("cdc-lsn output min_lsn is empty")
			}
			return minLSN, nil
		}
	}
	return "", fmt.Errorf("cdc-lsn output did not include min_lsn")
}

func runCDCHealth(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cdc-health", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	maxLSN := fs.String("max-lsn", "", "current SQL Server CDC max LSN")
	maxCheckpointAge := fs.Duration("max-checkpoint-age", 0, "maximum allowed CDC checkpoint age; 0 disables age checking")
	nowRaw := fs.String("now", "", "current time override as RFC3339, for deterministic checks")
	jsonOutput := fs.Bool("json", false, "emit JSON health report")
	metricsFile := fs.String("metrics-file", "", "optional path to write JSON health metrics")
	historyFile := fs.String("history-file", "", "optional JSONL file to append CDC health reports")
	failOnWarning := fs.Bool("fail-on-warning", false, "return non-zero when health status is warning")
	probeLSN := fs.Bool("probe-lsn", false, "probe SQL Server CDC max/min LSNs through the executor before evaluating health")
	executorBinary := fs.String("executor-binary", "sqlserver2tidb-executor", "executor binary used when --probe-lsn is set")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "SQLSERVER2TIDB_SOURCE_CONNECTION_STRING", "environment variable containing the SQL Server CDC connection string")
	feishuWebhookEnv := fs.String("feishu-webhook-env", "SQLSERVER2TIDB_FEISHU_WEBHOOK", "environment variable containing the Feishu custom bot webhook URL; empty value disables Feishu alerts")
	feishuSecretEnv := fs.String("feishu-secret-env", "SQLSERVER2TIDB_FEISHU_SECRET", "environment variable containing the optional Feishu custom bot signing secret")
	feishuAlertMinSeverity := fs.String("feishu-alert-min-severity", "critical", "minimum CDC health status that sends Feishu alerts: ok, warning, critical, or none")
	slackWebhookEnv := fs.String("slack-webhook-env", "SQLSERVER2TIDB_SLACK_WEBHOOK", "environment variable containing the Slack incoming webhook URL; empty value disables Slack alerts")
	slackAlertMinSeverity := fs.String("slack-alert-min-severity", "critical", "minimum CDC health status that sends Slack alerts: ok, warning, critical, or none")
	var minLSNs cdcHealthMinLSNFlags
	fs.Var(&minLSNs, "min-lsn", "per-table SQL Server CDC min LSN as source.object=0x...")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *maxCheckpointAge < 0 {
		fmt.Fprintln(stderr, "cdc-health --max-checkpoint-age must be non-negative")
		return 2
	}
	now := time.Now().UTC()
	if strings.TrimSpace(*nowRaw) != "" {
		parsedNow, err := time.Parse(time.RFC3339, strings.TrimSpace(*nowRaw))
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health --now must be RFC3339: %v\n", err)
			return 2
		}
		now = parsedNow.UTC()
	}
	feishuAlertSeverity, err := parseCDCHealthAlertSeverity(*feishuAlertMinSeverity, "--feishu-alert-min-severity")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 2
	}
	slackAlertSeverity, err := parseCDCHealthAlertSeverity(*slackAlertMinSeverity, "--slack-alert-min-severity")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 2
	}
	minLSNMap := minLSNs.Map()
	if *probeLSN {
		bounds, err := cdcOrchestratorProbeLSNBounds(*root, *sourceClusterID, *projectID, *executorBinary, *sourceConnectionStringEnv, *maxLSN, false)
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health: %v\n", err)
			return 1
		}
		*maxLSN = bounds.MaxLSN
		for sourceObject, minLSN := range bounds.MinLSNs {
			minLSNMap[sourceObject] = minLSN
		}
	}
	report, err := gitops.EvaluateCDCHealth(*root, *sourceClusterID, *projectID, gitops.CDCHealthSpec{
		MaxLSN:           *maxLSN,
		MinLSNs:          minLSNMap,
		MaxCheckpointAge: *maxCheckpointAge,
		Now:              now,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: %v\n", err)
		return 1
	}
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "cdc-health: marshal report: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*metricsFile) != "" {
		metricsPath := strings.TrimSpace(*metricsFile)
		if !filepath.IsAbs(metricsPath) {
			metricsPath = filepath.Join(*root, filepath.FromSlash(metricsPath))
		}
		if err := os.MkdirAll(filepath.Dir(metricsPath), 0o755); err != nil {
			fmt.Fprintf(stderr, "cdc-health: create metrics directory: %v\n", err)
			return 1
		}
		if err := os.WriteFile(metricsPath, append(reportJSON, '\n'), 0o644); err != nil {
			fmt.Fprintf(stderr, "cdc-health: write metrics file: %v\n", err)
			return 1
		}
	}
	if strings.TrimSpace(*historyFile) != "" {
		historyPath, err := appendCDCHealthHistory(*root, *historyFile, report)
		if err != nil {
			fmt.Fprintf(stderr, "cdc-health: append history: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintf(stdout, "cdc health history appended: %s\n", historyPath)
		}
	}
	feishuWebhook := strings.TrimSpace(os.Getenv(strings.TrimSpace(*feishuWebhookEnv)))
	if feishuWebhook != "" && shouldSendCDCHealthAlert(report.Status, feishuAlertSeverity) {
		if err := sendFeishuCDCHealthAlert(feishuWebhook, os.Getenv(strings.TrimSpace(*feishuSecretEnv)), now, report); err != nil {
			fmt.Fprintf(stderr, "cdc-health: send Feishu alert: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintln(stdout, "Feishu CDC health alert sent")
		}
	}
	slackWebhook := strings.TrimSpace(os.Getenv(strings.TrimSpace(*slackWebhookEnv)))
	if slackWebhook != "" && shouldSendCDCHealthAlert(report.Status, slackAlertSeverity) {
		if err := sendSlackCDCHealthAlert(slackWebhook, report); err != nil {
			fmt.Fprintf(stderr, "cdc-health: send Slack alert: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			fmt.Fprintln(stdout, "Slack CDC health alert sent")
		}
	}
	if *jsonOutput {
		fmt.Fprintln(stdout, string(reportJSON))
	} else {
		renderCDCHealthText(stdout, report)
	}
	if report.Status == "critical" || (*failOnWarning && report.Status == "warning") {
		return 1
	}
	return 0
}

func appendCDCHealthHistory(root, path string, report gitops.CDCHealthReport) (string, error) {
	historyPath := strings.TrimSpace(path)
	if historyPath == "" {
		return "", fmt.Errorf("history file is required")
	}
	if !filepath.IsAbs(historyPath) {
		historyPath = filepath.Join(root, filepath.FromSlash(historyPath))
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("marshal history report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
		return "", fmt.Errorf("create history directory: %w", err)
	}
	file, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open history file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(reportJSON, '\n')); err != nil {
		return "", fmt.Errorf("write history entry: %w", err)
	}
	return filepath.ToSlash(historyPath), nil
}

type cdcHealthAlertSeverity int

const (
	cdcHealthAlertNone cdcHealthAlertSeverity = iota
	cdcHealthAlertOK
	cdcHealthAlertWarning
	cdcHealthAlertCritical
)

func parseCDCHealthAlertSeverity(value, flagName string) (cdcHealthAlertSeverity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "critical":
		return cdcHealthAlertCritical, nil
	case "warning":
		return cdcHealthAlertWarning, nil
	case "ok", "always":
		return cdcHealthAlertOK, nil
	case "none", "disabled":
		return cdcHealthAlertNone, nil
	default:
		return cdcHealthAlertNone, fmt.Errorf("invalid %s %q", flagName, value)
	}
}

func shouldSendCDCHealthAlert(status string, min cdcHealthAlertSeverity) bool {
	if min == cdcHealthAlertNone {
		return false
	}
	return cdcHealthStatusRank(status) >= int(min)
}

func cdcHealthStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "critical":
		return int(cdcHealthAlertCritical)
	case "warning":
		return int(cdcHealthAlertWarning)
	case "ok":
		return int(cdcHealthAlertOK)
	default:
		return 0
	}
}

func sendFeishuCDCHealthAlert(webhookURL, secret string, now time.Time, report gitops.CDCHealthReport) error {
	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": renderCDCHealthAlertText(report),
		},
	}
	if strings.TrimSpace(secret) != "" {
		timestamp := strconv.FormatInt(now.Unix(), 10)
		payload["timestamp"] = timestamp
		payload["sign"] = signFeishuCustomBotRequest(timestamp, strings.TrimSpace(secret))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(webhookURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := checkFeishuWebhookResponse(respBody); err != nil {
		return err
	}
	return nil
}

func sendSlackCDCHealthAlert(webhookURL string, report gitops.CDCHealthReport) error {
	payload := map[string]string{
		"text": renderCDCHealthAlertText(report),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(webhookURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func signFeishuCustomBotRequest(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func checkFeishuWebhookResponse(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var response struct {
		Code          *int   `json:"code"`
		Msg           string `json:"msg"`
		StatusCode    *int   `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	if response.Code != nil && *response.Code != 0 {
		return fmt.Errorf("webhook returned code %d: %s", *response.Code, response.Msg)
	}
	if response.StatusCode != nil && *response.StatusCode != 0 {
		return fmt.Errorf("webhook returned StatusCode %d: %s", *response.StatusCode, response.StatusMessage)
	}
	return nil
}

func renderCDCHealthAlertText(report gitops.CDCHealthReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sqlserver2tidb CDC health %s\n", report.Status)
	fmt.Fprintf(&b, "source_cluster_id: %s\n", report.SourceClusterID)
	fmt.Fprintf(&b, "project_id: %s\n", report.ProjectID)
	fmt.Fprintf(&b, "generated_at: %s\n", report.GeneratedAt)
	if report.MaxLSN != "" {
		fmt.Fprintf(&b, "max_lsn: %s\n", report.MaxLSN)
	}
	fmt.Fprintf(&b, "tracked_tables: %d, lagging_tables: %d, expired_tables: %d\n", report.TrackedTables, report.LaggingTables, report.ExpiredTables)
	if len(report.Alerts) == 0 {
		b.WriteString("alerts: none")
		return b.String()
	}
	b.WriteString("alerts:\n")
	for _, alert := range report.Alerts {
		if alert.SourceObject != "" {
			fmt.Fprintf(&b, "- %s %s %s: %s\n", alert.Severity, alert.Code, alert.SourceObject, alert.Message)
		} else {
			fmt.Fprintf(&b, "- %s %s: %s\n", alert.Severity, alert.Code, alert.Message)
		}
	}
	return strings.TrimSpace(b.String())
}

type cdcHealthMinLSNFlags map[string]string

func (flags *cdcHealthMinLSNFlags) String() string {
	if flags == nil || len(*flags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*flags))
	for sourceObject, minLSN := range *flags {
		parts = append(parts, sourceObject+"="+minLSN)
	}
	return strings.Join(parts, ",")
}

func (flags *cdcHealthMinLSNFlags) Set(value string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("min-lsn value is required")
	}
	index := strings.LastIndex(raw, "=")
	if index <= 0 || index == len(raw)-1 {
		return fmt.Errorf("min-lsn must use source.object=0x... format")
	}
	sourceObject := strings.TrimSpace(raw[:index])
	minLSN := strings.TrimSpace(raw[index+1:])
	if sourceObject == "" || minLSN == "" {
		return fmt.Errorf("min-lsn must use source.object=0x... format")
	}
	if *flags == nil {
		*flags = cdcHealthMinLSNFlags{}
	}
	(*flags)[sourceObject] = minLSN
	return nil
}

func (flags cdcHealthMinLSNFlags) Map() map[string]string {
	result := make(map[string]string, len(flags))
	for sourceObject, minLSN := range flags {
		result[sourceObject] = minLSN
	}
	return result
}

func renderCDCHealthText(stdout io.Writer, report gitops.CDCHealthReport) {
	fmt.Fprintf(stdout, "cdc health: %s\n", report.Status)
	fmt.Fprintf(stdout, "source cluster: %s\n", report.SourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", report.ProjectID)
	fmt.Fprintf(stdout, "checkpoint status: %s\n", report.CheckpointStatus)
	if report.CheckpointUpdatedAt != "" {
		fmt.Fprintf(stdout, "checkpoint updated_at: %s\n", report.CheckpointUpdatedAt)
	}
	if report.CheckpointAgeSeconds != nil {
		fmt.Fprintf(stdout, "checkpoint age seconds: %d\n", *report.CheckpointAgeSeconds)
	}
	if report.MaxLSN != "" {
		fmt.Fprintf(stdout, "max_lsn: %s\n", report.MaxLSN)
	}
	fmt.Fprintf(stdout, "tracked tables: %d\n", report.TrackedTables)
	fmt.Fprintf(stdout, "lagging tables: %d\n", report.LaggingTables)
	fmt.Fprintf(stdout, "expired tables: %d\n", report.ExpiredTables)
	if len(report.Alerts) > 0 {
		fmt.Fprintln(stdout, "alerts:")
		for _, alert := range report.Alerts {
			if alert.SourceObject != "" {
				fmt.Fprintf(stdout, "- %s %s %s: %s\n", alert.Severity, alert.Code, alert.SourceObject, alert.Message)
			} else {
				fmt.Fprintf(stdout, "- %s %s: %s\n", alert.Severity, alert.Code, alert.Message)
			}
		}
	}
}

func runGenerateValidationPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-validation-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	includeChecksum := fs.Bool("include-checksum", false, "include reviewed scalar-query checksum checks for tables with exact numeric columns")
	includeSampledHash := fs.Bool("include-sampled-hash", false, "include reviewed scalar-query sampled_hash checks for tables with an integer sample column")
	includeBucketedCount := fs.Bool("include-bucketed-count", false, "include reviewed scalar-query bucketed_count checks for tables with an integer bucket column")
	sampleModulo := fs.Int("sample-modulo", 100, "modulo used by sampled_hash checks")
	bucketCount := fs.Int("bucket-count", 16, "bucket count used by bucketed_count checks (max 1024)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GenerateValidationPlanWithSpec(*root, *sourceClusterID, *projectID, gitops.ValidationPlanSpec{
		IncludeChecksum:      *includeChecksum,
		IncludeSampledHash:   *includeSampledHash,
		IncludeBucketedCount: *includeBucketedCount,
		SampleModulo:         *sampleModulo,
		BucketCount:          *bucketCount,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate validation plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "validation plan generated for %s under source cluster %s\n", result.ProjectID, result.SourceClusterID)
	fmt.Fprintf(stdout, "checks: %d\n", result.Checks)
	fmt.Fprintf(stdout, "wrote %s\n", "plan/validation-plan.yaml")
	return 0
}

func runGeneratePRDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-pr-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id; omit for cluster-level stages such as discovery")
	stage := fs.String("stage", "", "PR stage: discovery, schema, schema-drift, plan, export, import, cdc, validation, cutover")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.GeneratePRDraft(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "generate PR draft: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "PR draft generated for %s\n", result.Stage)
	fmt.Fprintf(stdout, "title: %s\n", result.Title)
	fmt.Fprintf(stdout, "branch: %s\n", result.BranchName)
	fmt.Fprintf(stdout, "body file: %s\n", result.BodyFile)
	fmt.Fprintf(stdout, "files to review: %d\n", len(result.Files))
	return 0
}

func runCreatePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id; omit for cluster-level stages such as discovery")
	stage := fs.String("stage", "", "PR stage: discovery, schema, schema-drift, plan, export, import, cdc, validation, cutover")
	execute := fs.Bool("execute", false, "call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareGitHubPRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not calling GitHub")
		fmt.Fprintf(stdout, "command: %s\n", redact.Text(spec.ShellCommand))
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		return 0
	}

	cmd := exec.Command("gh", spec.Args...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, redact.Text(string(output)))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runSyncGitHubPRApproval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync-github-pr-approval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "PR stage with an approval file: schema, export, import, cdc, validation, or cutover")
	prNumber := fs.Int("pr", 0, "GitHub pull request number")
	repo := fs.String("repo", "", "optional GitHub repository in owner/name form")
	ghBinary := fs.String("gh-binary", "gh", "GitHub CLI binary used to read PR status")
	base := fs.String("base", "main", "base branch recorded in optional automation audit metadata")
	automationActor := fs.String("automation-actor", os.Getenv("GITHUB_ACTOR"), "optional actor recorded in approval automation audit metadata")
	automationWorkflow := fs.String("automation-workflow", os.Getenv("GITHUB_WORKFLOW"), "optional workflow name recorded in approval automation audit metadata")
	automationRunID := fs.String("automation-run-id", os.Getenv("GITHUB_RUN_ID"), "optional workflow run id recorded in approval automation audit metadata")
	automationRunURL := fs.String("automation-run-url", defaultGitHubRunURL(), "optional workflow run URL recorded in approval automation audit metadata")
	automationCommit := fs.String("automation-commit", os.Getenv("GITHUB_SHA"), "optional workflow commit SHA recorded in approval automation audit metadata")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *prNumber <= 0 {
		fmt.Fprintln(stderr, "sync-github-pr-approval --pr must be positive")
		return 2
	}
	prStatus, err := readGitHubPRStatus(*root, *ghBinary, *repo, *prNumber)
	if err != nil {
		fmt.Fprintf(stderr, "sync GitHub PR approval: %v\n", err)
		return 1
	}
	inferred := inferGitHubPRMetadata(prStatus)
	effectiveSourceClusterID := firstNonEmpty(*sourceClusterID, inferred.SourceClusterID)
	effectiveProjectID := firstNonEmpty(*projectID, inferred.ProjectID)
	effectiveStage := firstNonEmpty(*stage, inferred.Stage)
	result, err := gitops.SyncGitHubPRApproval(*root, gitops.GitHubPRApprovalSyncSpec{
		SourceClusterID:    effectiveSourceClusterID,
		ProjectID:          effectiveProjectID,
		Stage:              effectiveStage,
		PRNumber:           *prNumber,
		PRState:            prStatus.State,
		ReviewDecision:     prStatus.ReviewDecision,
		ChecksStatus:       deriveGitHubPRChecksStatus(prStatus.StatusCheckRollup),
		MergedAt:           prStatus.MergedAt,
		ApprovedBy:         githubPRApprovers(prStatus.LatestReviews),
		ChangedFiles:       githubPRChangedFiles(prStatus.Files),
		AutomationActor:    strings.TrimSpace(*automationActor),
		AutomationWorkflow: strings.TrimSpace(*automationWorkflow),
		AutomationRunID:    strings.TrimSpace(*automationRunID),
		AutomationRunURL:   strings.TrimSpace(*automationRunURL),
		AutomationCommit:   strings.TrimSpace(*automationCommit),
		BaseBranch:         strings.TrimSpace(*base),
	})
	if err != nil {
		fmt.Fprintf(stderr, "sync GitHub PR approval: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "GitHub PR approval synced for %s\n", result.ApprovalStage)
	fmt.Fprintf(stdout, "PR: #%d\n", result.PRNumber)
	fmt.Fprintf(stdout, "approval file: %s\n", result.ApprovalFile)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "approved by: %s\n", strings.Join(result.ApprovedBy, ", "))
	return 0
}

func runCompleteGitHubPR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("complete-github-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "PR stage with an approval file: schema, export, import, cdc, validation, or cutover")
	prNumber := fs.Int("pr", 0, "GitHub pull request number")
	repo := fs.String("repo", "", "optional GitHub repository in owner/name form")
	ghBinary := fs.String("gh-binary", "gh", "GitHub CLI binary used to read, approve, and merge PRs")
	gitBinary := fs.String("git-binary", "git", "git binary used to sync and push approval files")
	base := fs.String("base", "main", "base branch to pull and push approval commits")
	mergeMethod := fs.String("merge-method", "squash", "GitHub PR merge method: squash, merge, or rebase")
	skipApprove := fs.Bool("skip-approve", false, "skip automated gh pr review --approve before merge")
	deleteBranch := fs.Bool("delete-branch", true, "delete the PR branch after merge")
	execute := fs.Bool("execute", false, "approve, merge, sync approval, commit, and push; default is dry-run")
	automationActor := fs.String("automation-actor", os.Getenv("GITHUB_ACTOR"), "optional actor recorded in approval automation audit metadata")
	automationWorkflow := fs.String("automation-workflow", os.Getenv("GITHUB_WORKFLOW"), "optional workflow name recorded in approval automation audit metadata")
	automationRunID := fs.String("automation-run-id", os.Getenv("GITHUB_RUN_ID"), "optional workflow run id recorded in approval automation audit metadata")
	automationRunURL := fs.String("automation-run-url", defaultGitHubRunURL(), "optional workflow run URL recorded in approval automation audit metadata")
	automationCommit := fs.String("automation-commit", os.Getenv("GITHUB_SHA"), "optional workflow commit SHA recorded in approval automation audit metadata")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec := githubPRCompletionSpec{
		Root:               *root,
		SourceClusterID:    strings.TrimSpace(*sourceClusterID),
		ProjectID:          strings.TrimSpace(*projectID),
		Stage:              strings.ToLower(strings.TrimSpace(*stage)),
		PRNumber:           *prNumber,
		Repo:               strings.TrimSpace(*repo),
		GHBinary:           commandBinary(*ghBinary, "gh"),
		GitBinary:          commandBinary(*gitBinary, "git"),
		Base:               strings.TrimSpace(*base),
		MergeMethod:        strings.ToLower(strings.TrimSpace(*mergeMethod)),
		SkipApprove:        *skipApprove,
		DeleteBranch:       *deleteBranch,
		Execute:            *execute,
		AutomationActor:    strings.TrimSpace(*automationActor),
		AutomationWorkflow: strings.TrimSpace(*automationWorkflow),
		AutomationRunID:    strings.TrimSpace(*automationRunID),
		AutomationRunURL:   strings.TrimSpace(*automationRunURL),
		AutomationCommit:   strings.TrimSpace(*automationCommit),
	}
	if err := validateGitHubPRCompletionSpec(spec); err != nil {
		fmt.Fprintf(stderr, "complete GitHub PR: %v\n", err)
		return 2
	}
	if !spec.Execute {
		printCompleteGitHubPRDryRun(stdout, spec)
		return 0
	}
	result, err := completeGitHubPR(spec, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "complete GitHub PR: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "GitHub PR completed for %s\n", result.ApprovalStage)
	fmt.Fprintf(stdout, "PR: #%d\n", result.PRNumber)
	fmt.Fprintf(stdout, "approval file: %s\n", result.ApprovalFile)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "approved by: %s\n", strings.Join(result.ApprovedBy, ", "))
	return 0
}

type githubPRCompletionSpec struct {
	Root               string
	SourceClusterID    string
	ProjectID          string
	Stage              string
	PRNumber           int
	Repo               string
	GHBinary           string
	GitBinary          string
	Base               string
	MergeMethod        string
	SkipApprove        bool
	DeleteBranch       bool
	Execute            bool
	AutomationActor    string
	AutomationWorkflow string
	AutomationRunID    string
	AutomationRunURL   string
	AutomationCommit   string
}

func validateGitHubPRCompletionSpec(spec githubPRCompletionSpec) error {
	if spec.PRNumber <= 0 {
		return fmt.Errorf("--pr must be positive")
	}
	if !spec.Execute && strings.TrimSpace(spec.SourceClusterID) == "" {
		return fmt.Errorf("--source-cluster-id is required")
	}
	if !spec.Execute && strings.TrimSpace(spec.ProjectID) == "" {
		return fmt.Errorf("--project-id is required")
	}
	if !spec.Execute && strings.TrimSpace(spec.Stage) == "" {
		return fmt.Errorf("--stage is required")
	}
	if strings.TrimSpace(spec.Base) == "" {
		return fmt.Errorf("--base is required")
	}
	switch spec.MergeMethod {
	case "merge", "squash", "rebase":
		return nil
	default:
		return fmt.Errorf("--merge-method must be one of merge, squash, or rebase")
	}
}

func printCompleteGitHubPRDryRun(stdout io.Writer, spec githubPRCompletionSpec) {
	approvalFile := githubPRCompletionApprovalFile(spec.SourceClusterID, spec.ProjectID, spec.Stage)
	fmt.Fprintln(stdout, "dry run: not changing GitHub or git")
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GHBinary}, githubPRViewArgs(spec.PRNumber, spec.Repo)...))))
	if !spec.SkipApprove {
		fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GHBinary}, githubPRReviewArgs(spec.PRNumber, spec.Repo)...))))
	}
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GHBinary}, githubPRMergeArgs(spec.PRNumber, spec.Repo, spec.MergeMethod, spec.DeleteBranch)...))))
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GitBinary}, gitPullArgs(spec.Base)...))))
	fmt.Fprintf(stdout, "sync approval: %s\n", approvalFile)
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GitBinary}, gitAddArgs(approvalFile)...))))
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GitBinary}, gitCommitArgs(spec.Stage, spec.ProjectID, spec.PRNumber)...))))
	fmt.Fprintf(stdout, "%s\n", redact.Text(renderArgsForGitHubPRCompletion(append([]string{spec.GitBinary}, gitPushArgs(spec.Base)...))))
}

func completeGitHubPR(spec githubPRCompletionSpec, stdout io.Writer) (gitops.GitHubPRApprovalSyncResult, error) {
	initialStatus, err := readGitHubPRStatus(spec.Root, spec.GHBinary, spec.Repo, spec.PRNumber)
	if err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	if checksStatus := deriveGitHubPRChecksStatus(initialStatus.StatusCheckRollup); checksStatus != "PASSED" {
		return gitops.GitHubPRApprovalSyncResult{}, fmt.Errorf("PR checks status is %s, want PASSED", checksStatus)
	}

	state := strings.ToUpper(strings.TrimSpace(initialStatus.State))
	switch state {
	case "MERGED":
	case "OPEN":
		if !spec.SkipApprove && strings.ToUpper(strings.TrimSpace(initialStatus.ReviewDecision)) != "APPROVED" {
			if err := runRepositoryCommand(spec.Root, spec.GHBinary, githubPRReviewArgs(spec.PRNumber, spec.Repo), stdout, "gh pr review"); err != nil {
				return gitops.GitHubPRApprovalSyncResult{}, err
			}
		}
		if err := runRepositoryCommand(spec.Root, spec.GHBinary, githubPRMergeArgs(spec.PRNumber, spec.Repo, spec.MergeMethod, spec.DeleteBranch), stdout, "gh pr merge"); err != nil {
			return gitops.GitHubPRApprovalSyncResult{}, err
		}
	default:
		return gitops.GitHubPRApprovalSyncResult{}, fmt.Errorf("PR state is %s, want OPEN or MERGED", state)
	}

	if err := runRepositoryCommand(spec.Root, spec.GitBinary, gitPullArgs(spec.Base), stdout, "git pull"); err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	finalStatus, err := readGitHubPRStatus(spec.Root, spec.GHBinary, spec.Repo, spec.PRNumber)
	if err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	inferred := inferGitHubPRMetadata(finalStatus)
	effectiveSourceClusterID := firstNonEmpty(spec.SourceClusterID, inferred.SourceClusterID)
	effectiveProjectID := firstNonEmpty(spec.ProjectID, inferred.ProjectID)
	effectiveStage := firstNonEmpty(spec.Stage, inferred.Stage)
	approvalFile := githubPRCompletionApprovalFile(effectiveSourceClusterID, effectiveProjectID, effectiveStage)
	beforeApproval, beforeApprovalExists, err := readOptionalFile(filepath.Join(spec.Root, filepath.FromSlash(approvalFile)))
	if err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	result, err := gitops.SyncGitHubPRApproval(spec.Root, gitops.GitHubPRApprovalSyncSpec{
		SourceClusterID:    effectiveSourceClusterID,
		ProjectID:          effectiveProjectID,
		Stage:              effectiveStage,
		PRNumber:           spec.PRNumber,
		PRState:            finalStatus.State,
		ReviewDecision:     finalStatus.ReviewDecision,
		ChecksStatus:       deriveGitHubPRChecksStatus(finalStatus.StatusCheckRollup),
		MergedAt:           finalStatus.MergedAt,
		ApprovedBy:         githubPRApprovers(finalStatus.LatestReviews),
		ChangedFiles:       githubPRChangedFiles(finalStatus.Files),
		AutomationActor:    spec.AutomationActor,
		AutomationWorkflow: spec.AutomationWorkflow,
		AutomationRunID:    spec.AutomationRunID,
		AutomationRunURL:   spec.AutomationRunURL,
		AutomationCommit:   spec.AutomationCommit,
		MergeMethod:        spec.MergeMethod,
		BaseBranch:         spec.Base,
	})
	if err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	afterApproval, afterApprovalExists, err := readOptionalFile(filepath.Join(spec.Root, filepath.FromSlash(result.ApprovalFile)))
	if err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	if beforeApprovalExists && afterApprovalExists && string(beforeApproval) == string(afterApproval) {
		fmt.Fprintln(stdout, "approval already current; no git commit needed")
		return result, nil
	}
	if err := runRepositoryCommand(spec.Root, spec.GitBinary, gitAddArgs(result.ApprovalFile), stdout, "git add"); err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	if err := runRepositoryCommand(spec.Root, spec.GitBinary, gitCommitArgs(result.Stage, result.ProjectID, result.PRNumber), stdout, "git commit"); err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	if err := runRepositoryCommand(spec.Root, spec.GitBinary, gitPushArgs(spec.Base), stdout, "git push"); err != nil {
		return gitops.GitHubPRApprovalSyncResult{}, err
	}
	return result, nil
}

func commandBinary(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func defaultGitHubRunURL() string {
	if value := strings.TrimSpace(os.Getenv("GITHUB_RUN_URL")); value != "" {
		return value
	}
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL")), "/")
	repository := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID"))
	if serverURL == "" || repository == "" || runID == "" {
		return ""
	}
	return serverURL + "/" + repository + "/actions/runs/" + runID
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read %s: %w", path, err)
}

const githubPRViewJSONFields = "title,body,state,reviewDecision,mergedAt,latestReviews,statusCheckRollup,files"

func renderArgsForGitHubPRCompletion(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == githubPRViewJSONFields {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, shellQuoteForEvidence(arg))
	}
	return strings.Join(quoted, " ")
}

func githubPRViewArgs(prNumber int, repo string) []string {
	args := []string{
		"pr", "view", strconv.Itoa(prNumber),
		"--json", githubPRViewJSONFields,
	}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", strings.TrimSpace(repo))
	}
	return args
}

func githubPRReviewArgs(prNumber int, repo string) []string {
	args := []string{
		"pr", "review", strconv.Itoa(prNumber),
		"--approve",
		"--body", "sqlserver2tidb automated approval",
	}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", strings.TrimSpace(repo))
	}
	return args
}

func githubPRMergeArgs(prNumber int, repo, method string, deleteBranch bool) []string {
	args := []string{"pr", "merge", strconv.Itoa(prNumber), "--" + strings.ToLower(strings.TrimSpace(method))}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", strings.TrimSpace(repo))
	}
	return args
}

func gitPullArgs(base string) []string {
	return []string{"pull", "--ff-only", "origin", strings.TrimSpace(base)}
}

func gitAddArgs(file string) []string {
	return []string{"add", filepath.ToSlash(file)}
}

func gitCommitArgs(stage, projectID string, prNumber int) []string {
	return []string{"commit", "-m", fmt.Sprintf("[approval:%s] %s from PR #%d", githubPRCompletionApprovalStage(stage), strings.TrimSpace(projectID), prNumber)}
}

func gitPushArgs(base string) []string {
	return []string{"push", "origin", "HEAD:" + strings.TrimSpace(base)}
}

func githubPRCompletionApprovalFile(sourceClusterID, projectID, stage string) string {
	return filepath.ToSlash(filepath.Join("clusters", strings.TrimSpace(sourceClusterID), "projects", strings.TrimSpace(projectID), "approvals", githubPRCompletionApprovalStage(stage)+"-approval.yaml"))
}

func githubPRCompletionApprovalStage(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "schema" {
		return "ddl"
	}
	return stage
}

func runRepositoryCommand(root, binary string, args []string, stdout io.Writer, name string) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, redact.Text(string(output)))
	}
	if err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

type githubPRViewStatus struct {
	Title             string                `json:"title"`
	Body              string                `json:"body"`
	State             string                `json:"state"`
	ReviewDecision    string                `json:"reviewDecision"`
	MergedAt          string                `json:"mergedAt"`
	LatestReviews     []githubPRReview      `json:"latestReviews"`
	StatusCheckRollup []githubPRStatusCheck `json:"statusCheckRollup"`
	Files             []githubPRFile        `json:"files"`
}

type githubPRReview struct {
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubPRStatusCheck struct {
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

type githubPRFile struct {
	Path string `json:"path"`
}

func readGitHubPRStatus(root, ghBinary, repo string, prNumber int) (githubPRViewStatus, error) {
	binary := strings.TrimSpace(ghBinary)
	if binary == "" {
		binary = "gh"
	}
	args := githubPRViewArgs(prNumber, repo)
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return githubPRViewStatus{}, fmt.Errorf("gh pr view failed: %w: %s", err, redact.Text(strings.TrimSpace(string(output))))
	}
	var status githubPRViewStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return githubPRViewStatus{}, fmt.Errorf("parse gh pr view JSON: %w", err)
	}
	return status, nil
}

type inferredGitHubPRMetadata struct {
	SourceClusterID string
	ProjectID       string
	Stage           string
}

func inferGitHubPRMetadata(status githubPRViewStatus) inferredGitHubPRMetadata {
	inferred := inferredGitHubPRMetadata{}
	if stage, projectID, ok := parseGitHubPRTitle(status.Title); ok {
		inferred.Stage = stage
		inferred.ProjectID = projectID
	}
	for _, line := range strings.Split(status.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- Stage:"):
			inferred.Stage = firstNonEmpty(extractBacktickValue(trimmed), inferred.Stage)
		case strings.HasPrefix(trimmed, "- Source cluster:"):
			inferred.SourceClusterID = firstNonEmpty(extractBacktickValue(trimmed), inferred.SourceClusterID)
		case strings.HasPrefix(trimmed, "- Project:"):
			value := extractBacktickValue(trimmed)
			if value != "" && value != "cluster-level" {
				inferred.ProjectID = firstNonEmpty(value, inferred.ProjectID)
			}
		}
	}
	return inferred
}

func parseGitHubPRTitle(title string) (string, string, bool) {
	trimmed := strings.TrimSpace(title)
	if !strings.HasPrefix(trimmed, "[") {
		return "", "", false
	}
	end := strings.Index(trimmed, "]")
	if end <= 1 || end+1 >= len(trimmed) {
		return "", "", false
	}
	stage := strings.TrimSpace(trimmed[1:end])
	projectID := strings.TrimSpace(trimmed[end+1:])
	if stage == "" || projectID == "" {
		return "", "", false
	}
	return stage, projectID, true
}

func extractBacktickValue(line string) string {
	start := strings.Index(line, "`")
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(line[start+1 : start+1+end])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func deriveGitHubPRChecksStatus(checks []githubPRStatusCheck) string {
	for _, check := range checks {
		conclusion := strings.ToUpper(strings.TrimSpace(check.Conclusion))
		switch conclusion {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			continue
		case "FAILURE", "FAILED", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED":
			return "FAILED"
		}
		state := strings.ToUpper(strings.TrimSpace(check.State))
		switch state {
		case "", "SUCCESS":
			continue
		case "FAILURE", "FAILED", "ERROR":
			return "FAILED"
		default:
			return "PENDING"
		}
	}
	return "PASSED"
}

func githubPRApprovers(reviews []githubPRReview) []string {
	seen := map[string]bool{}
	approvers := make([]string, 0, len(reviews))
	for _, review := range reviews {
		if strings.ToUpper(strings.TrimSpace(review.State)) != "APPROVED" {
			continue
		}
		login := strings.TrimSpace(review.Author.Login)
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		approvers = append(approvers, login)
	}
	return approvers
}

func githubPRChangedFiles(files []githubPRFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

func runCreateWorkerStatePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-worker-state-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "worker state PR stage: export, import, cdc, validation")
	execute := fs.Bool("execute", false, "create git branch, commit state files, and call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareWorkerStatePRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create worker state PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not changing git or calling GitHub")
		for _, command := range spec.ShellCommands {
			fmt.Fprintf(stdout, "command: %s\n", redact.Text(command))
		}
		if spec.BodyFileNeedsUpdate {
			fmt.Fprintln(stdout, "body file update: needed; execute mode refreshes it before commit")
		} else {
			fmt.Fprintln(stdout, "body file update: not needed")
		}
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "branch: %s\n", spec.BranchName)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		fmt.Fprintf(stdout, "files to commit: %d\n", len(spec.Files))
		return 0
	}

	if err := gitops.RefreshWorkerStatePRBody(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create worker state PR: %v\n", err)
		return 1
	}
	if spec.BodyFileNeedsUpdate {
		fmt.Fprintf(stdout, "updated %s\n", spec.BodyFile)
	}

	for _, gitArgs := range spec.GitArgs {
		cmd := exec.Command("git", gitArgs...)
		cmd.Dir = *root
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			fmt.Fprint(stdout, redact.Text(string(output)))
		}
		if err != nil {
			fmt.Fprintf(stderr, "create worker state PR: git %s failed: %v\n", strings.Join(gitArgs, " "), err)
			return 1
		}
	}
	cmd := exec.Command("gh", spec.GitHubArgs...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, redact.Text(string(output)))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create worker state PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runGenerateExecutorEvidencePRDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate-executor-evidence-pr-draft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc-enable, cdc, validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	draft, err := gitops.GenerateExecutorEvidencePRDraft(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "generate executor evidence PR draft: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "executor evidence PR draft generated")
	fmt.Fprintf(stdout, "title: %s\n", draft.Title)
	fmt.Fprintf(stdout, "branch: %s\n", draft.BranchName)
	fmt.Fprintf(stdout, "body file: %s\n", draft.BodyFile)
	fmt.Fprintf(stdout, "files to review: %d\n", len(draft.Files))
	return 0
}

func runCreateExecutorEvidencePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-executor-evidence-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor evidence PR stage: ddl, export, import, cdc-enable, cdc, validation")
	execute := fs.Bool("execute", false, "create git branch, commit executor evidence, and call gh pr create; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := gitops.PrepareExecutorEvidencePRCreate(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "create executor evidence PR: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "dry run: not changing git or calling GitHub")
		for _, command := range spec.ShellCommands {
			fmt.Fprintf(stdout, "command: %s\n", redact.Text(command))
		}
		fmt.Fprintf(stdout, "title: %s\n", spec.Title)
		fmt.Fprintf(stdout, "branch: %s\n", spec.BranchName)
		fmt.Fprintf(stdout, "body file: %s\n", spec.BodyFile)
		fmt.Fprintf(stdout, "files to commit: %d\n", len(spec.Files))
		return 0
	}

	for _, gitArgs := range spec.GitArgs {
		cmd := exec.Command("git", gitArgs...)
		cmd.Dir = *root
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			fmt.Fprint(stdout, redact.Text(string(output)))
		}
		if err != nil {
			fmt.Fprintf(stderr, "create executor evidence PR: git %s failed: %v\n", strings.Join(gitArgs, " "), err)
			return 1
		}
	}
	cmd := exec.Command("gh", spec.GitHubArgs...)
	cmd.Dir = *root
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(stdout, redact.Text(string(output)))
	}
	if err != nil {
		fmt.Fprintf(stderr, "create executor evidence PR: gh pr create failed: %v\n", err)
		return 1
	}
	return 0
}

func runComputePayloadHash(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compute-payload-hash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "stage to hash: export, import, cdc, or validation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	hash, err := gitops.ComputePayloadHashForStage(*root, *sourceClusterID, *projectID, *stage)
	if err != nil {
		fmt.Fprintf(stderr, "compute payload hash: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "stage: %s\n", *stage)
	fmt.Fprintf(stdout, "source cluster: %s\n", *sourceClusterID)
	fmt.Fprintf(stdout, "project: %s\n", *projectID)
	fmt.Fprintf(stdout, "payload hash: %s\n", hash)
	return 0
}

func runWorkerValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunValidationWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker validate: %v\n", err)
		return 1
	}
	status := "passed"
	exitCode := 0
	if !result.Passed {
		status = "failed"
		exitCode = 1
	}
	fmt.Fprintf(stdout, "validation worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", status)
	fmt.Fprintf(stdout, "checks: %d\n", len(result.Checks))
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", "state/validation-status.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", "evidence/validation-report.md")
	return exitCode
}

func runWorkerExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunExportWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker export: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "export worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "chunks: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunImportWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker import: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "import worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "jobs: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerCDC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-cdc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunCDCWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker cdc: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "tracked tables: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", "state/cdc-checkpoint.yaml")
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	return 0
}

func runWorkerCutover(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-cutover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.RunCutoverWorker(*root, *sourceClusterID, *projectID)
	if err != nil {
		fmt.Fprintf(stderr, "worker cutover: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cutover worker completed for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "gates: %d\n", result.Items)
	fmt.Fprintf(stdout, "payload hash: %s\n", result.PayloadHash)
	fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
	fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
	fmt.Fprintf(stdout, "wrote %s\n", "evidence/post-cutover-report.md")
	return 0
}

func runWorkerExecutor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-executor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	stage := fs.String("stage", "", "executor stage: ddl, export, import, cdc-enable, cdc, or validation")
	executorBinary := fs.String("executor-binary", "", "external executor binary; default is sqlserver2tidb-executor")
	sourceConnectionStringEnv := fs.String("source-connection-string-env", "", "environment variable containing the SQL Server connection string for export execution")
	targetConnectionStringEnv := fs.String("target-connection-string-env", "", "environment variable containing the TiDB/MySQL connection string for import execution")
	importBatchSize := fs.Int("import-batch-size", 0, "rows to commit per import transaction; default is executor-defined")
	requireEmptyTarget := fs.Bool("require-empty-target", false, "pass executor --require-empty-target to sql-insert import commands")
	commandTimeout := fs.Duration("command-timeout", 0, "maximum runtime per external executor command; 0 disables timeout")
	commandRetries := fs.Int("command-retries", 0, "number of retries for a failed external executor command")
	retryBackoff := fs.Duration("retry-backoff", time.Second, "fixed backoff between external executor command retries")
	resume := fs.Bool("resume", false, "skip matching successful commands from existing executor evidence for the current payload hash")
	execute := fs.Bool("execute", false, "run external executor commands with executor --execute; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *commandTimeout < 0 {
		fmt.Fprintln(stderr, "worker executor: --command-timeout must be non-negative")
		return 2
	}
	if *commandRetries < 0 {
		fmt.Fprintln(stderr, "worker executor: --command-retries must be non-negative")
		return 2
	}
	if *retryBackoff < 0 {
		fmt.Fprintln(stderr, "worker executor: --retry-backoff must be non-negative")
		return 2
	}
	spec, err := gitops.PrepareWorkerExecutor(*root, *sourceClusterID, *projectID, *stage, gitops.WorkerExecutorPrepareSpec{
		Binary:                    *executorBinary,
		SourceConnectionStringEnv: *sourceConnectionStringEnv,
		TargetConnectionStringEnv: *targetConnectionStringEnv,
		ImportBatchSize:           *importBatchSize,
		RequireEmptyTarget:        *requireEmptyTarget,
	})
	if err != nil {
		fmt.Fprintf(stderr, "worker executor: %v\n", err)
		return 1
	}
	if !*execute {
		fmt.Fprintln(stdout, "worker executor dry run")
		fmt.Fprintf(stdout, "stage: %s\n", spec.Stage)
		fmt.Fprintf(stdout, "payload hash: %s\n", spec.PayloadHash)
		fmt.Fprintf(stdout, "commands: %d\n", len(spec.Commands))
		for _, command := range spec.Commands {
			fmt.Fprintf(stdout, "command: %s\n", redact.Text(command.ShellCommand))
		}
		return 0
	}

	results := make([]workerExecutorRunCommandEvidence, 0, len(spec.Commands))
	resumeCommands := map[string]workerExecutorRunCommandEvidence{}
	if *resume {
		resumeCommands, err = loadWorkerExecutorResumeCommands(*root, spec)
		if err != nil {
			fmt.Fprintf(stderr, "worker executor: %v\n", err)
			return 1
		}
	}
	failedCommands := 0
	for _, command := range spec.Commands {
		if len(command.Args) == 0 {
			fmt.Fprintf(stderr, "worker executor: empty command for %s\n", command.ID)
			return 1
		}
		args := withExternalExecutorExecuteFlag(command.Args)
		if previous, ok := resumeCommands[command.ID]; ok && isReusableWorkerExecutorCommandEvidence(spec.Stage, previous, args) {
			previous = normalizeWorkerExecutorCommandEvidence(previous)
			results = append(results, previous)
			fmt.Fprintf(stdout, "resumed command: %s\n", command.ID)
			continue
		}
		maxAttempts := *commandRetries + 1
		attempts := make([]workerExecutorRunCommandAttemptEvidence, 0, maxAttempts)
		var commandErr error
		var parseErr error
		var timedOut bool
		var commandEvidence workerExecutorRunCommandEvidence
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			commandContext := context.Background()
			var cancel context.CancelFunc
			if *commandTimeout > 0 {
				commandContext, cancel = context.WithTimeout(commandContext, *commandTimeout)
			}
			cmd := newWorkerExecutorCommand(commandContext, args[0], args[1:]...)
			cmd.Dir = *root
			startedAt := time.Now().UTC()
			output, err := cmd.CombinedOutput()
			completedAt := time.Now().UTC()
			timedOut = commandContext.Err() == context.DeadlineExceeded
			if cancel != nil {
				cancel()
			}
			rawOutput := string(output)
			redactedOutput := redact.Text(rawOutput)
			if len(output) > 0 {
				fmt.Fprint(stdout, redactedOutput)
			}
			commandEvidenceError := ""
			if timedOut {
				commandEvidenceError = fmt.Sprintf("command timed out after %s", commandTimeout.String())
			}
			attemptEvidence := workerExecutorRunCommandAttemptEvidence{
				Attempt:     attempt,
				ExitCode:    exitCodeForCommandError(err),
				Output:      redactedOutput,
				Error:       redact.Text(commandEvidenceError),
				StartedAt:   startedAt.Format(time.RFC3339Nano),
				CompletedAt: completedAt.Format(time.RFC3339Nano),
				DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
			}
			attempts = append(attempts, attemptEvidence)
			cdcAppliedChanges, errParse := workerExecutorCDCAppliedChanges(spec.Stage, rawOutput)
			dataRows, dataBytes, errDataMetrics := workerExecutorDataMetrics(spec.Stage, rawOutput)
			if errParse == nil {
				errParse = errDataMetrics
			}
			dataSHA256, errDataSHA256 := workerExecutorDataSHA256(spec.Stage, rawOutput)
			if errParse == nil {
				errParse = errDataSHA256
			}
			if errParse == nil {
				errParse = workerExecutorRequiredDataAuditError(spec.Stage, args, dataRows, dataBytes, dataSHA256)
			}
			redactedArgs := redact.Args(args)
			commandEvidence = workerExecutorRunCommandEvidence{
				ID:                command.ID,
				Args:              redactedArgs,
				ShellCommand:      renderArgsForEvidence(redactedArgs),
				ExitCode:          attemptEvidence.ExitCode,
				Output:            attemptEvidence.Output,
				Error:             attemptEvidence.Error,
				AttemptCount:      len(attempts),
				StartedAt:         attemptEvidence.StartedAt,
				CompletedAt:       attemptEvidence.CompletedAt,
				DurationMs:        attemptEvidence.DurationMs,
				CDCAppliedChanges: cdcAppliedChanges,
				DataRows:          dataRows,
				DataBytes:         dataBytes,
				DataSHA256:        dataSHA256,
			}
			if len(attempts) > 1 {
				commandEvidence.Attempts = attempts
			}
			commandErr = err
			parseErr = errParse
			if commandErr == nil {
				break
			}
			if attempt == maxAttempts {
				break
			}
			if *retryBackoff > 0 {
				fmt.Fprintf(stderr, "worker executor: command %s attempt %d/%d failed: %v; retrying after %s\n", command.ID, attempt, maxAttempts, commandErr, retryBackoff.String())
				time.Sleep(*retryBackoff)
			} else {
				fmt.Fprintf(stderr, "worker executor: command %s attempt %d/%d failed: %v; retrying\n", command.ID, attempt, maxAttempts, commandErr)
			}
		}
		results = append(results, commandEvidence)
		if commandErr != nil {
			if spec.Stage == "validation" {
				failedCommands++
				continue
			}
			if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
				fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
			}
			if timedOut {
				fmt.Fprintf(stderr, "worker executor: command %s timed out after %s\n", command.ID, commandTimeout.String())
				return 1
			}
			fmt.Fprintf(stderr, "worker executor: command %s failed: %v\n", command.ID, commandErr)
			return 1
		}
		if parseErr != nil {
			results[len(results)-1].Error = redact.Text(parseErr.Error())
			if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
				fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
			}
			fmt.Fprintf(stderr, "worker executor: command %s: %s\n", command.ID, redact.Text(parseErr.Error()))
			return 1
		}
	}
	if failedCommands > 0 {
		if _, evidenceErr := writeWorkerExecutorRunEvidence(*root, spec, "failed", results); evidenceErr != nil {
			fmt.Fprintf(stderr, "worker executor: %v\n", evidenceErr)
		}
		fmt.Fprintf(stderr, "worker executor: validation completed with %d failed command(s)\n", failedCommands)
		return 1
	}
	evidenceFile, err := writeWorkerExecutorRunEvidence(*root, spec, "succeeded", results)
	if err != nil {
		fmt.Fprintf(stderr, "worker executor: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "worker executor completed for %s\n", spec.ProjectID)
	fmt.Fprintf(stdout, "stage: %s\n", spec.Stage)
	fmt.Fprintf(stdout, "commands: %d\n", len(spec.Commands))
	fmt.Fprintf(stdout, "wrote %s\n", evidenceFile)
	return 0
}

func loadWorkerExecutorResumeCommands(root string, spec gitops.WorkerExecutorSpec) (map[string]workerExecutorRunCommandEvidence, error) {
	path := filepath.Join(root, filepath.FromSlash(workerExecutorRunEvidenceRel(spec)))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]workerExecutorRunCommandEvidence{}, nil
		}
		return nil, fmt.Errorf("read resume executor evidence: %w", err)
	}
	var evidence workerExecutorRunEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return nil, fmt.Errorf("parse resume executor evidence: %w", err)
	}
	if evidence.Stage != spec.Stage {
		return nil, fmt.Errorf("resume executor evidence stage %q does not match current stage %q", evidence.Stage, spec.Stage)
	}
	if evidence.SourceClusterID != spec.SourceClusterID {
		return nil, fmt.Errorf("resume executor evidence source_cluster_id %q does not match current source cluster %q", evidence.SourceClusterID, spec.SourceClusterID)
	}
	if evidence.ProjectID != spec.ProjectID {
		return nil, fmt.Errorf("resume executor evidence project_id %q does not match current project %q", evidence.ProjectID, spec.ProjectID)
	}
	if evidence.PayloadHash != spec.PayloadHash {
		return map[string]workerExecutorRunCommandEvidence{}, nil
	}
	commands := make(map[string]workerExecutorRunCommandEvidence, len(evidence.Commands))
	for _, command := range evidence.Commands {
		commands[command.ID] = command
	}
	return commands, nil
}

func isReusableWorkerExecutorCommandEvidence(stage string, command workerExecutorRunCommandEvidence, expectedArgs []string) bool {
	if command.ExitCode != 0 || strings.TrimSpace(command.Error) != "" {
		return false
	}
	expectedRedactedArgs := redact.Args(expectedArgs)
	if !stringSlicesEqual(command.Args, expectedArgs) && !stringSlicesEqual(command.Args, expectedRedactedArgs) {
		return false
	}
	if command.ShellCommand != renderArgsForEvidence(expectedArgs) && command.ShellCommand != renderArgsForEvidence(expectedRedactedArgs) {
		return false
	}
	if stage == "cdc" && command.CDCAppliedChanges == nil {
		return false
	}
	if workerExecutorCommandRequiresDataAudit(stage, expectedArgs) && !workerExecutorCommandHasDataAudit(command) {
		return false
	}
	return true
}

func workerExecutorCommandRequiresDataAudit(stage string, args []string) bool {
	switch stage {
	case "export":
		return true
	case "import":
		switch workerExecutorImportEngine(args) {
		case "sql-insert":
			return true
		case "tidb-import-into":
			return workerExecutorImportSourceNeedsLocalAudit(workerExecutorArgValue(args, "--source-uri"))
		case "tidb-lightning":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func workerExecutorImportEngine(args []string) string {
	engine := "sql-insert"
	if value := workerExecutorArgValue(args, "--engine"); value != "" {
		engine = value
	}
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "sql-insert":
		return "sql-insert"
	case "tidb-import-into", "import-into":
		return "tidb-import-into"
	case "tidb-lightning", "lightning":
		return "tidb-lightning"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

func workerExecutorArgValue(args []string, flagName string) string {
	for i, arg := range args {
		if arg == flagName && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(arg, flagName+"=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, flagName+"="))
		}
	}
	return ""
}

func workerExecutorImportSourceNeedsLocalAudit(sourceURI string) bool {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return true
	}
	parsed, err := url.Parse(sourceURI)
	if err != nil {
		return true
	}
	switch parsed.Scheme {
	case "", "file", "s3", "gs":
		return true
	default:
		return true
	}
}

func workerExecutorCommandHasDataAudit(command workerExecutorRunCommandEvidence) bool {
	if command.DataRows == nil || *command.DataRows < 0 {
		return false
	}
	if command.DataBytes == nil || *command.DataBytes < 0 {
		return false
	}
	return isWorkerExecutorSHA256(strings.TrimSpace(command.DataSHA256))
}

func workerExecutorRequiredDataAuditError(stage string, args []string, dataRows, dataBytes *int64, dataSHA256 string) error {
	if !workerExecutorCommandRequiresDataAudit(stage, args) {
		return nil
	}
	if dataRows != nil && dataBytes != nil && isWorkerExecutorSHA256(strings.TrimSpace(dataSHA256)) {
		return nil
	}
	switch stage {
	case "export":
		return fmt.Errorf("export executor output must include exported rows: N, output bytes: N, and output sha256: sha256:<digest>")
	case "import":
		return fmt.Errorf("import executor output must include imported rows: N, input bytes: N, and input sha256: sha256:<digest>")
	default:
		return fmt.Errorf("%s executor output must include data_rows, data_bytes, and data_sha256", stage)
	}
}

func normalizeWorkerExecutorCommandEvidence(command workerExecutorRunCommandEvidence) workerExecutorRunCommandEvidence {
	if command.AttemptCount == 0 {
		if len(command.Attempts) > 0 {
			command.AttemptCount = len(command.Attempts)
		} else {
			command.AttemptCount = 1
		}
	}
	return redactWorkerExecutorCommandEvidence(command)
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type workerExecutorRunCommandEvidence struct {
	ID                string                                    `json:"id"`
	Args              []string                                  `json:"args"`
	ShellCommand      string                                    `json:"shell_command"`
	ExitCode          int                                       `json:"exit_code"`
	Output            string                                    `json:"output"`
	Error             string                                    `json:"error,omitempty"`
	AttemptCount      int                                       `json:"attempt_count"`
	Attempts          []workerExecutorRunCommandAttemptEvidence `json:"attempts,omitempty"`
	StartedAt         string                                    `json:"started_at"`
	CompletedAt       string                                    `json:"completed_at"`
	DurationMs        int64                                     `json:"duration_ms"`
	CDCAppliedChanges *int                                      `json:"cdc_applied_changes,omitempty"`
	DataRows          *int64                                    `json:"data_rows,omitempty"`
	DataBytes         *int64                                    `json:"data_bytes,omitempty"`
	DataSHA256        string                                    `json:"data_sha256,omitempty"`
}

type workerExecutorRunCommandAttemptEvidence struct {
	Attempt     int    `json:"attempt"`
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DurationMs  int64  `json:"duration_ms"`
}

type workerExecutorRunEvidence struct {
	Stage           string                             `json:"stage"`
	Status          string                             `json:"status"`
	ProjectID       string                             `json:"project_id"`
	SourceClusterID string                             `json:"source_cluster_id"`
	PayloadHash     string                             `json:"payload_hash"`
	Commands        []workerExecutorRunCommandEvidence `json:"commands"`
	GeneratedAt     string                             `json:"generated_at"`
}

func writeWorkerExecutorRunEvidence(root string, spec gitops.WorkerExecutorSpec, status string, commands []workerExecutorRunCommandEvidence) (string, error) {
	rel := workerExecutorRunEvidenceRel(spec)
	redactedCommands := make([]workerExecutorRunCommandEvidence, 0, len(commands))
	for _, command := range commands {
		redactedCommands = append(redactedCommands, redactWorkerExecutorCommandEvidence(command))
	}
	evidence := workerExecutorRunEvidence{
		Stage:           spec.Stage,
		Status:          status,
		ProjectID:       spec.ProjectID,
		SourceClusterID: spec.SourceClusterID,
		PayloadHash:     spec.PayloadHash,
		Commands:        redactedCommands,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal executor evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil {
		return "", fmt.Errorf("write executor evidence: %w", err)
	}
	return filepath.ToSlash(filepath.Join("evidence", "executor-"+spec.Stage+"-run.json")), nil
}

func redactWorkerExecutorCommandEvidence(command workerExecutorRunCommandEvidence) workerExecutorRunCommandEvidence {
	command.Args = redact.Args(command.Args)
	if len(command.Args) > 0 {
		command.ShellCommand = renderArgsForEvidence(command.Args)
	} else {
		command.ShellCommand = redact.Text(command.ShellCommand)
	}
	command.Output = redact.Text(command.Output)
	command.Error = redact.Text(command.Error)
	for i := range command.Attempts {
		command.Attempts[i].Output = redact.Text(command.Attempts[i].Output)
		command.Attempts[i].Error = redact.Text(command.Attempts[i].Error)
	}
	return command
}

func workerExecutorRunEvidenceRel(spec gitops.WorkerExecutorSpec) string {
	return filepath.ToSlash(filepath.Join("clusters", spec.SourceClusterID, "projects", spec.ProjectID, "evidence", "executor-"+spec.Stage+"-run.json"))
}

func exitCodeForCommandError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func renderArgsForEvidence(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteForEvidence(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteForEvidence(arg string) string {
	if arg == "" {
		return "''"
	}
	for _, r := range arg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '=':
			continue
		default:
			return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return arg
}

func workerExecutorCDCAppliedChanges(stage, output string) (*int, error) {
	if stage != "cdc" {
		return nil, nil
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "applied changes:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "applied changes:"))
		appliedChanges, err := strconv.Atoi(value)
		if err != nil || appliedChanges < 0 {
			return nil, fmt.Errorf("CDC applied changes output %q must contain a non-negative integer", line)
		}
		return &appliedChanges, nil
	}
	return nil, fmt.Errorf("CDC executor output must include applied changes: N")
}

func workerExecutorDataMetrics(stage, output string) (*int64, *int64, error) {
	switch stage {
	case "export":
		return parseWorkerExecutorDataMetrics(stage, output, "exported rows:", "output bytes:")
	case "import":
		return parseWorkerExecutorDataMetrics(stage, output, "imported rows:", "input bytes:")
	default:
		return nil, nil, nil
	}
}

func parseWorkerExecutorDataMetrics(stage, output, rowsPrefix, bytesPrefix string) (*int64, *int64, error) {
	var dataRows *int64
	var dataBytes *int64
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, rowsPrefix) {
			value, err := parseWorkerExecutorNonNegativeInt64Metric(line, rowsPrefix)
			if err != nil {
				return nil, nil, fmt.Errorf("%s executor output metric %q must contain a non-negative integer", stage, rowsPrefix)
			}
			dataRows = &value
			continue
		}
		if strings.HasPrefix(line, bytesPrefix) {
			value, err := parseWorkerExecutorNonNegativeInt64Metric(line, bytesPrefix)
			if err != nil {
				return nil, nil, fmt.Errorf("%s executor output metric %q must contain a non-negative integer", stage, bytesPrefix)
			}
			dataBytes = &value
		}
	}
	if (dataRows == nil) != (dataBytes == nil) {
		return nil, nil, fmt.Errorf("%s executor output must include both %s N and %s N when data metrics are present", stage, rowsPrefix, bytesPrefix)
	}
	return dataRows, dataBytes, nil
}

func parseWorkerExecutorNonNegativeInt64Metric(line, prefix string) (int64, error) {
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid non-negative integer")
	}
	return parsed, nil
}

func workerExecutorDataSHA256(stage, output string) (string, error) {
	switch stage {
	case "export":
		return parseWorkerExecutorDataSHA256(stage, output, "output sha256:")
	case "import":
		return parseWorkerExecutorDataSHA256(stage, output, "input sha256:")
	default:
		return "", nil
	}
}

func parseWorkerExecutorDataSHA256(stage, output, prefix string) (string, error) {
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if !isWorkerExecutorSHA256(value) {
			return "", fmt.Errorf("%s executor output metric %q must contain sha256:<64 hex chars>", stage, prefix)
		}
		return value, nil
	}
	return "", nil
}

func isWorkerExecutorSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func withExternalExecutorExecuteFlag(args []string) []string {
	out := append([]string(nil), args...)
	for _, arg := range out[1:] {
		if arg == "--execute" {
			return out
		}
	}
	if len(out) >= 2 {
		return append(append(out[:2:2], "--execute"), out[2:]...)
	}
	return append(out, "--execute")
}

func runAdvanceCDCCheckpoint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advance-cdc-checkpoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	status := fs.String("status", "running", "checkpoint status to write: running or caught_up")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := gitops.AdvanceCDCCheckpointFromExecutorEvidence(*root, *sourceClusterID, *projectID, gitops.CDCCheckpointAdvanceSpec{
		Status: *status,
	})
	if err != nil {
		fmt.Fprintf(stderr, "advance cdc checkpoint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cdc checkpoint advanced for %s\n", result.ProjectID)
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	fmt.Fprintf(stdout, "updated tables: %d\n", result.UpdatedTables)
	fmt.Fprintf(stdout, "applied changes: %d\n", result.AppliedChanges)
	fmt.Fprintf(stdout, "wrote %s\n", result.CheckpointFile)
	return 0
}

func runWorkerReconcile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "optional source cluster id to scope reconcile actions")
	dryRun := fs.Bool("dry-run", false, "plan worker actions without executing them")
	executeNext := fs.Bool("execute-next", false, "execute the first ready metadata-only worker action")
	loop := fs.Bool("loop", false, "execute ready metadata-only worker actions until none remain or max iterations is reached")
	jsonOutput := fs.Bool("json", false, "write dry-run report as JSON")
	maxIterations := fs.Int("max-iterations", 0, "maximum loop iterations; 0 means continue until no ready metadata-only actions remain")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between loop iterations")
	holder := fs.String("holder", "", "worker lease holder id for --execute-next or --loop")
	leaseTTL := fs.Duration("lease-ttl", 15*time.Minute, "worker lease ttl for --execute-next or --loop")
	statePRDraft := fs.Bool("state-pr-draft", false, "write PR drafts for worker state and evidence changes after --execute-next or --loop")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	selectedModes := 0
	for _, enabled := range []bool{*dryRun, *executeNext, *loop} {
		if enabled {
			selectedModes++
		}
	}
	if selectedModes != 1 {
		fmt.Fprintln(stderr, "worker-reconcile requires exactly one of --dry-run, --execute-next, or --loop")
		return 2
	}
	if *jsonOutput && !*dryRun {
		fmt.Fprintln(stderr, "worker-reconcile --json is only supported with --dry-run")
		return 2
	}
	if *loop {
		if *maxIterations < 0 {
			fmt.Fprintln(stderr, "worker-reconcile --max-iterations must be non-negative")
			return 2
		}
		if *interval < 0 {
			fmt.Fprintln(stderr, "worker-reconcile --interval must be non-negative")
			return 2
		}
		return runWorkerReconcileLoop(*root, gitops.WorkerReconcileExecuteSpec{
			Holder:          *holder,
			LeaseTTL:        *leaseTTL,
			CreatePRDraft:   *statePRDraft,
			SourceClusterID: *sourceClusterID,
		}, *maxIterations, *interval, stdout, stderr)
	}
	if *executeNext {
		result, err := gitops.ExecuteNextWorkerReconcile(*root, gitops.WorkerReconcileExecuteSpec{
			Holder:          *holder,
			LeaseTTL:        *leaseTTL,
			CreatePRDraft:   *statePRDraft,
			SourceClusterID: *sourceClusterID,
		})
		if err != nil {
			fmt.Fprintf(stderr, "worker reconcile: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "worker reconcile execute next")
		fmt.Fprintf(stdout, "selected: %s/%s %s\n", result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		fmt.Fprintf(stdout, "payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "branch: %s\n", result.BranchName)
		}
		return 0
	}
	report, err := gitops.PlanWorkerReconcileWithSpec(*root, gitops.WorkerReconcilePlanSpec{SourceClusterID: *sourceClusterID})
	if err != nil {
		fmt.Fprintf(stderr, "worker reconcile: %v\n", err)
		return 1
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "worker reconcile json: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "worker reconcile dry run")
	fmt.Fprintf(stdout, "projects: %d\n", report.Projects)
	fmt.Fprintf(stdout, "ready actions: %d\n", report.ReadyActions)
	fmt.Fprintf(stdout, "blocked actions: %d\n", report.BlockedActions)
	for _, action := range report.Actions {
		fmt.Fprintf(stdout, "- [%s] %s/%s %s\n", action.Status, action.SourceClusterID, action.ProjectID, action.Stage)
		if action.PayloadHash != "" {
			fmt.Fprintf(stdout, "  payload hash: %s\n", action.PayloadHash)
		}
		if action.Reason != "" {
			fmt.Fprintf(stdout, "  reason: %s\n", action.Reason)
		}
		if action.Status == "ready" {
			fmt.Fprintf(stdout, "  command: %s\n", redact.Text(action.Command))
		}
	}
	return 0
}

func runWorkerReconcileLoop(root string, spec gitops.WorkerReconcileExecuteSpec, maxIterations int, interval time.Duration, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "worker reconcile loop")
	executed := 0
	for iteration := 1; maxIterations == 0 || iteration <= maxIterations; iteration++ {
		result, err := gitops.ExecuteNextWorkerReconcile(root, spec)
		if err != nil {
			message := err.Error()
			if isNoReadyWorkerActionsError(message) {
				fmt.Fprintf(stdout, "iteration %d: %s\n", iteration, message)
				fmt.Fprintf(stdout, "executed actions: %d\n", executed)
				return 0
			}
			fmt.Fprintf(stderr, "worker reconcile loop: %v\n", err)
			return 1
		}
		executed++
		fmt.Fprintf(stdout, "iteration %d: selected %s/%s %s\n", iteration, result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "  status: %s\n", result.Status)
		fmt.Fprintf(stdout, "  payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "  lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "  wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "  state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "  branch: %s\n", result.BranchName)
		}
		if maxIterations == 0 || iteration < maxIterations {
			time.Sleep(interval)
		}
	}
	fmt.Fprintf(stdout, "executed actions: %d\n", executed)
	return 0
}

func isNoReadyWorkerActionsError(message string) bool {
	return strings.Contains(message, "no ready worker actions") || strings.Contains(message, "no ready metadata worker actions")
}

func runWorkerAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "optional source cluster id to scope reconcile actions")
	holder := fs.String("holder", "", "worker lease holder id")
	leaseTTL := fs.Duration("lease-ttl", 15*time.Minute, "worker lease ttl")
	maxIterations := fs.Int("max-iterations", 0, "maximum loop iterations; 0 means continue until no ready metadata-only actions remain")
	interval := fs.Duration("interval", 5*time.Second, "sleep interval between loop iterations")
	poll := fs.Bool("poll", false, "keep polling after idle no-ready scans")
	idleIterations := fs.Int("idle-iterations", 0, "maximum consecutive idle polls in --poll mode; 0 means unlimited")
	statePRDraft := fs.Bool("state-pr-draft", false, "write PR drafts for worker state and evidence changes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*holder) == "" {
		fmt.Fprintln(stderr, "worker-agent requires --holder")
		return 2
	}
	if *maxIterations < 0 {
		fmt.Fprintln(stderr, "worker-agent --max-iterations must be non-negative")
		return 2
	}
	if *interval < 0 {
		fmt.Fprintln(stderr, "worker-agent --interval must be non-negative")
		return 2
	}
	if *idleIterations < 0 {
		fmt.Fprintln(stderr, "worker-agent --idle-iterations must be non-negative")
		return 2
	}
	fmt.Fprintln(stdout, "worker agent")
	fmt.Fprintf(stdout, "holder: %s\n", *holder)
	spec := gitops.WorkerReconcileExecuteSpec{
		Holder:          *holder,
		LeaseTTL:        *leaseTTL,
		CreatePRDraft:   *statePRDraft,
		SourceClusterID: *sourceClusterID,
	}
	if *poll {
		return runWorkerAgentPoll(*root, spec, *maxIterations, *interval, *idleIterations, stdout, stderr)
	}
	return runWorkerReconcileLoop(*root, spec, *maxIterations, *interval, stdout, stderr)
}

func runWorkerAgentPoll(root string, spec gitops.WorkerReconcileExecuteSpec, maxIterations int, interval time.Duration, idleIterations int, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "worker agent poll")
	executed := 0
	idle := 0
	for {
		result, err := gitops.ExecuteNextWorkerReconcile(root, spec)
		if err != nil {
			message := err.Error()
			if isNoReadyWorkerActionsError(message) {
				idle++
				fmt.Fprintf(stdout, "idle iteration %d: %s\n", idle, message)
				if idleIterations > 0 && idle >= idleIterations {
					fmt.Fprintf(stdout, "executed actions: %d\n", executed)
					return 0
				}
				time.Sleep(interval)
				continue
			}
			fmt.Fprintf(stderr, "worker agent poll: %v\n", err)
			return 1
		}
		idle = 0
		executed++
		fmt.Fprintf(stdout, "iteration %d: selected %s/%s %s\n", executed, result.Action.SourceClusterID, result.Action.ProjectID, result.Action.Stage)
		fmt.Fprintf(stdout, "  status: %s\n", result.Status)
		fmt.Fprintf(stdout, "  payload hash: %s\n", result.Action.PayloadHash)
		fmt.Fprintf(stdout, "  lease id: %s\n", result.LeaseID)
		fmt.Fprintf(stdout, "  wrote %s\n", result.StateFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.EvidenceFile)
		fmt.Fprintf(stdout, "  wrote %s\n", result.LeaseFile)
		if result.PRBodyFile != "" {
			fmt.Fprintf(stdout, "  state PR draft: %s\n", result.PRBodyFile)
			fmt.Fprintf(stdout, "  branch: %s\n", result.BranchName)
		}
		if maxIterations > 0 && executed >= maxIterations {
			fmt.Fprintf(stdout, "executed actions: %d\n", executed)
			return 0
		}
		time.Sleep(interval)
	}
}

func runCreateCluster(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-cluster", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	clusterID := fs.String("cluster-id", "", "upstream SQL Server cluster id")
	displayName := fs.String("display-name", "", "display name")
	listener := fs.String("listener", "", "SQL Server listener or hostname")
	port := fs.String("port", "1433", "SQL Server port")
	secretRef := fs.String("secret-ref", "", "secret reference, not a plaintext secret")
	cdcMode := fs.String("cdc-mode", "sqlserver-cdc", "CDC mode")
	retentionHours := fs.Int("retention-hours", 168, "required CDC retention hours")
	owners := fs.String("owner", "", "comma-separated owners")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	parsedPort, err := strconv.Atoi(*port)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --port %q: %v\n", *port, err)
		return 2
	}
	spec := gitops.ClusterSpec{
		ClusterID:              *clusterID,
		DisplayName:            *displayName,
		Listener:               *listener,
		Port:                   parsedPort,
		SecretRef:              *secretRef,
		CDCMode:                *cdcMode,
		RetentionHoursRequired: *retentionHours,
		Owners:                 splitCSV(*owners),
	}
	if err := gitops.CreateCluster(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create cluster: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created cluster %s\n", *clusterID)
	return 0
}

func runCreateProject(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-project", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	sourceClusterID := fs.String("source-cluster-id", "", "upstream SQL Server cluster id")
	projectID := fs.String("project-id", "", "migration project id")
	displayName := fs.String("display-name", "", "display name")
	sourceDatabase := fs.String("source-database", "", "source SQL Server database")
	sourceSchemas := fs.String("source-schema", "", "comma-separated source schemas")
	targetName := fs.String("target-name", "", "target TiDB cluster name")
	targetDatabase := fs.String("target-database", "", "target TiDB database")
	targetSecretRef := fs.String("target-secret-ref", "", "target TiDB secret reference")
	mode := fs.String("mode", "short-downtime", "migration mode")
	owners := fs.String("owner", "", "comma-separated owners")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec := gitops.ProjectSpec{
		SourceClusterID: *sourceClusterID,
		ProjectID:       *projectID,
		DisplayName:     *displayName,
		SourceDatabase:  *sourceDatabase,
		SourceSchemas:   splitCSV(*sourceSchemas),
		TargetName:      *targetName,
		TargetDatabase:  *targetDatabase,
		TargetSecretRef: *targetSecretRef,
		Mode:            *mode,
		Owners:          splitCSV(*owners),
	}
	if err := gitops.CreateProject(*root, spec); err != nil {
		fmt.Fprintf(stderr, "create project: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created project %s under source cluster %s\n", *projectID, *sourceClusterID)
	return 0
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `sqlserver2tidb manages GitOps metadata for SQL Server to TiDB migrations.

Usage:
  sqlserver2tidb version
  sqlserver2tidb init-repo --root .
  sqlserver2tidb doctor --root .
  sqlserver2tidb validate-repo --root .
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --dry-run
  sqlserver2tidb discover-sqlserver --root . --source-cluster-id prod-sqlserver-a --connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN
  sqlserver2tidb analyze-compatibility --root . --source-cluster-id prod-sqlserver-a
  sqlserver2tidb llm-compatibility-advice --root . --source-cluster-id prod-sqlserver-a --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb llm-schema-advice --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb llm-migration-strategy --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb llm-validation-analysis --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb llm-cutover-risk --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb llm-pr-summary --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema --provider-config global/llm-providers.yaml --execute
  sqlserver2tidb generate-schema-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-data-plans --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full --compression gzip
  sqlserver2tidb repair-schema-drift --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --object-uri-prefix https://object-store.example/migration/prod-sqlserver-a/sales-db-to-tidb-prod-a/full --apply --pr-draft
  sqlserver2tidb generate-cdc-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb prepare-cdc-range --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --to-lsn 0x00000027000001f40003
  sqlserver2tidb prepare-cdc-iteration --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --max-lsn 0x00000027000001f40004 --pr-draft
  sqlserver2tidb cdc-orchestrator --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --apply-approved --poll --pr-draft
  sqlserver2tidb cdc-health --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --probe-lsn --max-checkpoint-age 15m --metrics-file artifacts/cdc-health.json --history-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/cdc-health-history.jsonl
  sqlserver2tidb agent --mode status --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb agent --mode auto --dry-run --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb agent --mode auto --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --max-steps 2
  sqlserver2tidb agent --mode plan-and-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export --use-executor --source-connection-string-env SQLSERVER2TIDB_SQLSERVER_DSN --execute --evidence-pr-draft
  sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
  sqlserver2tidb agent --mode execute-approved --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage cdc-enable --source-connection-string-env SQLSERVER2TIDB_CDC_ADMIN_CONNECTION_STRING --command-timeout 5m --command-retries 2 --retry-backoff 10s --resume --execute --create-evidence-pr
  sqlserver2tidb agent --mode pr-close --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export --pr 42 --repo BornChanger/sqlserver2tidb
  sqlserver2tidb agent --mode cdc-ops --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --probe-lsn --history-file clusters/prod-sqlserver-a/projects/sales-db-to-tidb-prod-a/state/cdc-health-history.jsonl --metrics-file artifacts/cdc-health.json --pr-draft
  sqlserver2tidb agent --mode review-assist --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage validation --provider-config global/llm-providers.yaml --execute-llm
  sqlserver2tidb generate-validation-plan --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb generate-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb create-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage schema
  sqlserver2tidb sync-github-pr-approval --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export --pr 42 --repo BornChanger/sqlserver2tidb
  sqlserver2tidb complete-github-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export --pr 42 --repo BornChanger/sqlserver2tidb --execute
  sqlserver2tidb create-worker-state-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb generate-executor-evidence-pr-draft --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb create-executor-evidence-pr --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage ddl
  sqlserver2tidb compute-payload-hash --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-export --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-import --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-cdc --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-validate --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-cutover --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage export
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage import --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING --import-batch-size 1000
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage cdc-enable --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING
  sqlserver2tidb worker-executor --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --stage validation --source-connection-string-env SQLSERVER2TIDB_SOURCE_CONNECTION_STRING --target-connection-string-env SQLSERVER2TIDB_TARGET_CONNECTION_STRING
  sqlserver2tidb advance-cdc-checkpoint --root . --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --status running
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --dry-run
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --execute-next --holder agent-a --state-pr-draft
  sqlserver2tidb worker-reconcile --root . --source-cluster-id prod-sqlserver-a --loop --holder agent-a --max-iterations 10 --interval 5s
  sqlserver2tidb worker-agent --root . --source-cluster-id prod-sqlserver-a --holder agent-a --max-iterations 0 --interval 5s --poll --idle-iterations 0 --state-pr-draft
  sqlserver2tidb create-cluster --cluster-id prod-sqlserver-a --display-name "prod SQL Server A" --listener sqlserver-a.internal --secret-ref vault://...
  sqlserver2tidb create-project --source-cluster-id prod-sqlserver-a --project-id sales-db-to-tidb-prod-a --source-database sales --source-schema dbo --target-name tidb-prod-a --target-database app --target-secret-ref vault://...
`)
}
