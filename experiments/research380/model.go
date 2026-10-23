package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"time"

	tailrisk "github.com/EpicBlackWolfZ/microfat/experiments/research380/tailrisk"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	schemaVersion     = 1
	maximumApps       = 4
	maximumScenarios  = 4
	maximumEpisodes   = 1000
	maximumLaunches   = 12
	maximumArguments  = 32
	maximumTimeout    = time.Minute
	maximumCampaign   = 20 * time.Minute
	maximumHorizon    = 24 * time.Hour
	maximumInput      = 64 << 20
	maximumProcesses  = 12000
	maximumOutput     = 64 << 10
	maximumExecutable = 1 << 30
	armCount          = 2
	phasePilot        = "pilot"
	phaseConfirmation = "confirmation"
	armBaseline       = "never-prewarm"
	armPrewarm        = "deployment-selected-prewarm"
	measurementKind   = "scheduled-short-lived-command-completion"
	privateFileMode   = 0o600
	privateDirMode    = 0o700
)

type request struct {
	SchemaVersion     int           `json:"schema_version"`
	Phase             string        `json:"phase"`
	ProtocolID        string        `json:"protocol_id"`
	SourceRevision    string        `json:"source_revision"`
	Host              string        `json:"host"`
	CorpusSHA256      string        `json:"corpus_sha256"`
	PilotReportSHA256 string        `json:"pilot_report_sha256,omitempty"`
	Tool              string        `json:"microfat"`
	ToolSHA256        string        `json:"microfat_sha256"`
	SamplesPerArm     int           `json:"samples_per_arm"`
	TimeoutNS         int64         `json:"command_timeout_ns"`
	CampaignBudgetNS  int64         `json:"campaign_budget_ns"`
	GOMAXPROCS        int           `json:"gomaxprocs_per_process"`
	SLONS             int64         `json:"episode_slo_ns"`
	Margin            float64       `json:"practical_risk_margin"`
	Alpha             float64       `json:"family_alpha"`
	FamilySize        int           `json:"comparison_family_size"`
	IID               bool          `json:"iid_assumption"`
	Apps              []application `json:"applications"`
	Scenarios         []scenario    `json:"scenarios"`
}

type application struct {
	Family         string   `json:"family"`
	Artifact       string   `json:"artifact"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Level          string   `json:"selected_level"`
	Args           []string `json:"readiness_args"`
	StdoutSHA256   string   `json:"expected_stdout_sha256"`
	PayloadSHA256  string   `json:"selected_payload_sha256"`
	PayloadBytes   int64    `json:"selected_payload_bytes"`
}

type scenario struct {
	Name        string  `json:"name"`
	OffsetsNS   []int64 `json:"launch_offsets_ns"`
	RetentionNS int64   `json:"logical_retention_horizon_ns"`
}

type processStats struct {
	WallNS       int64  `json:"wall_ns"`
	CPUNS        int64  `json:"cpu_ns"`
	MaxRSSKiB    int64  `json:"max_rss_kib"`
	InputBlocks  int64  `json:"input_blocks"`
	OutputBlocks int64  `json:"output_blocks"`
	MinorFaults  int64  `json:"minor_faults"`
	MajorFaults  int64  `json:"major_faults"`
	ExitCode     int    `json:"exit_code"`
	TimedOut     bool   `json:"timed_out"`
	Error        string `json:"error,omitempty"`
	StdoutSHA256 string `json:"stdout_sha256"`
	StderrSHA256 string `json:"stderr_sha256"`
	StdoutBytes  int64  `json:"stdout_bytes"`
	StderrBytes  int64  `json:"stderr_bytes"`
}

type launch struct {
	Index              int                       `json:"index"`
	ScheduledNS        int64                     `json:"scheduled_ns"`
	StartedNS          int64                     `json:"started_ns"`
	ReadyNS            int64                     `json:"ready_ns"`
	ScheduledToReadyNS int64                     `json:"scheduled_to_ready_ns"`
	Process            processStats              `json:"process"`
	Failed             bool                      `json:"failed"`
	Dispatch           *format.DispatchTelemetry `json:"dispatch,omitempty"`
	DispatchError      string                    `json:"dispatch_error,omitempty"`
}

type episode struct {
	ID                     string        `json:"id"`
	Family                 string        `json:"family"`
	Scenario               string        `json:"scenario"`
	Arm                    string        `json:"arm"`
	Block                  int           `json:"block"`
	Preparation            *processStats `json:"preparation,omitempty"`
	Verification           *processStats `json:"postmeasurement_cache_verification,omitempty"`
	InitialDelayNS         int64         `json:"initial_deployment_delay_ns"`
	Launches               []launch      `json:"launches"`
	MaxReadyNS             int64         `json:"maximum_scheduled_to_ready_ns"`
	ForegroundCPUNS        int64         `json:"foreground_cpu_ns"`
	CacheBytes             int64         `json:"cache_bytes"`
	CacheFiles             int           `json:"cache_files"`
	ByteTime               float64       `json:"logical_retained_byte_seconds"`
	Failed                 bool          `json:"failed"`
	TimedOut               bool          `json:"timed_out"`
	Error                  string        `json:"error,omitempty"`
	Environment            []string      `json:"environment"`
	ForegroundSpanNS       int64         `json:"foreground_span_ns"`
	DeploymentFirstReadyNS *int64        `json:"deployment_to_first_ready_ns"`
	DeploymentSpanNS       int64         `json:"deployment_plus_foreground_span_ns"`
	TotalCPUNS             int64         `json:"preparation_plus_foreground_cpu_ns"`
}

type report struct {
	SchemaVersion    int       `json:"schema_version"`
	Request          request   `json:"request"`
	RequestSHA256    string    `json:"request_sha256"`
	ProducerRevision string    `json:"producer_revision"`
	GoVersion        string    `json:"go_version"`
	MeasurementKind  string    `json:"measurement_kind"`
	StartedUTC       string    `json:"started_utc"`
	Complete         bool      `json:"complete"`
	StopReason       string    `json:"stop_reason,omitempty"`
	WallNS           int64     `json:"wall_ns"`
	Episodes         []episode `json:"episodes"`
}

type comparison struct {
	Family             string           `json:"family"`
	Scenario           string           `json:"scenario"`
	BaselineAttempts   int              `json:"baseline_attempts"`
	PrewarmAttempts    int              `json:"prewarm_attempts"`
	BaselineFailures   int              `json:"baseline_failures"`
	PrewarmFailures    int              `json:"prewarm_failures"`
	BaselineViolations int              `json:"baseline_slo_violations"`
	PrewarmViolations  int              `json:"prewarm_slo_violations"`
	Inference          *tailrisk.Report `json:"inference,omitempty"`
	InferenceStatus    string           `json:"inference_status"`
	BaselineCosts      *costSummary     `json:"successful_baseline_costs,omitempty"`
	PrewarmCosts       *costSummary     `json:"successful_prewarm_costs,omitempty"`
}

type costSummary struct {
	Samples         int      `json:"samples"`
	PreparationWall float64  `json:"median_preparation_wall_ns"`
	ForegroundMax   float64  `json:"median_episode_maximum_scheduled_to_ready_ns"`
	DeploymentFirst *float64 `json:"median_deployment_to_first_ready_ns"`
	DeploymentSpan  float64  `json:"median_deployment_plus_foreground_span_ns"`
	CPU             float64  `json:"median_preparation_plus_foreground_cpu_ns"`
	ByteTime        float64  `json:"median_logical_retained_byte_seconds"`
	InputBlocks     int64    `json:"total_preparation_plus_foreground_input_blocks_512_bytes"`
	OutputBlocks    int64    `json:"total_preparation_plus_foreground_output_blocks_512_bytes"`
}

func validSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validateRequest(in request) error {
	if in.SchemaVersion != schemaVersion || in.ProtocolID == "" || in.SourceRevision == "" || in.Host == "" ||
		!validSHA(in.CorpusSHA256) || !validSHA(in.ToolSHA256) || !filepath.IsAbs(in.Tool) {
		return errors.New("declare schema, protocol, source, host, corpus and absolute tool identity")
	}
	if in.Phase != phasePilot && in.Phase != phaseConfirmation {
		return errors.New("phase must be pilot or confirmation")
	}
	if in.Phase == phaseConfirmation && !validSHA(in.PilotReportSHA256) {
		return errors.New("confirmation requires the completed pilot report digest")
	}
	if err := validatePlanBounds(in); err != nil {
		return err
	}
	if err := validateApplications(in.Apps); err != nil {
		return err
	}
	return validateScenarios(in)
}

func validatePlanBounds(in request) error {
	if in.SamplesPerArm < 1 || in.SamplesPerArm > maximumEpisodes || in.TimeoutNS < 1 ||
		in.TimeoutNS > int64(maximumTimeout) || in.CampaignBudgetNS < 1 || in.CampaignBudgetNS > int64(maximumCampaign) ||
		in.GOMAXPROCS < 1 || in.GOMAXPROCS > maximumArguments || in.SLONS < 1 {
		return errors.New("sample, timeout, campaign, process or SLO bounds exceeded")
	}
	if len(in.Apps) < 1 || len(in.Apps) > maximumApps || len(in.Scenarios) < 1 || len(in.Scenarios) > maximumScenarios ||
		in.FamilySize < len(in.Apps)*len(in.Scenarios) || in.FamilySize > tailrisk.MaximumFamily ||
		math.IsNaN(in.Alpha) || in.Alpha < 1e-6 || in.Alpha >= 1 || math.IsNaN(in.Margin) || in.Margin <= 0 || in.Margin >= 1 {
		return errors.New("invalid comparison family, error budget or practical margin")
	}
	return nil
}

func validateApplications(apps []application) error {
	seen := make(map[string]bool)
	for _, app := range apps {
		if app.Family == "" || seen[app.Family] || !filepath.IsAbs(app.Artifact) || !validSHA(app.ArtifactSHA256) ||
			app.Level == "" || !validSHA(app.StdoutSHA256) || len(app.Args) > maximumArguments ||
			!validSHA(app.PayloadSHA256) || app.PayloadBytes < 1 || app.PayloadBytes > maximumExecutable {
			return errors.New("applications need unique families, absolute artifacts, digests, levels and bounded arguments")
		}
		seen[app.Family] = true
	}
	return nil
}

func validateScenarios(in request) error {
	seen := make(map[string]bool)
	processes := 0
	for _, sc := range in.Scenarios {
		processes += armCount*len(sc.OffsetsNS) + armCount
		if sc.Name == "" || seen[sc.Name] || len(sc.OffsetsNS) < 1 || len(sc.OffsetsNS) > maximumLaunches ||
			sc.RetentionNS < 1 || sc.RetentionNS > int64(maximumHorizon) {
			return errors.New("scenarios need unique names and bounded launch schedules/retention horizons")
		}
		previous := int64(0)
		for _, offset := range sc.OffsetsNS {
			if offset < previous || offset > in.CampaignBudgetNS || offset > sc.RetentionNS {
				return errors.New("launch offsets must be ordered, nonnegative and inside campaign/retention bounds")
			}
			previous = offset
		}
		seen[sc.Name] = true
	}
	if processes*in.SamplesPerArm*len(in.Apps) > maximumProcesses {
		return errors.New("campaign exceeds process-attempt cap")
	}
	return nil
}

func analyzeReport(in report) ([]comparison, error) {
	if err := validateRequest(in.Request); err != nil {
		return nil, err
	}
	if in.SchemaVersion != schemaVersion || !validSHA(in.RequestSHA256) || in.ProducerRevision == "" || in.GoVersion == "" {
		return nil, errors.New("missing report identity")
	}
	seen := make(map[string]bool)
	for _, ep := range in.Episodes {
		key := fmt.Sprintf("%s/%s/%s/%d", ep.Family, ep.Scenario, ep.Arm, ep.Block)
		if ep.ID == "" || seen[key] || ep.Block < 0 || ep.Block >= in.Request.SamplesPerArm || ep.MaxReadyNS < 0 ||
			(ep.Arm != armBaseline && ep.Arm != armPrewarm) || !knownGroup(in.Request, ep.Family, ep.Scenario) {
			return nil, errors.New("invalid or repeated episode identity, group, block or duration")
		}
		seen[key] = true
		if err := validateEpisode(ep, in.Request); err != nil {
			return nil, err
		}
	}
	result := make([]comparison, 0, len(in.Request.Apps)*len(in.Request.Scenarios))
	for _, app := range in.Request.Apps {
		for _, sc := range in.Request.Scenarios {
			item, err := compareGroup(in, app.Family, sc.Name)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func validateEpisode(ep episode, in request) error {
	var schedule []int64
	for _, sc := range in.Scenarios {
		if sc.Name == ep.Scenario {
			schedule = sc.OffsetsNS
		}
	}
	if len(ep.Launches) != len(schedule) && !ep.Failed {
		return errors.New("successful episode has missing launch observations")
	}
	if math.IsNaN(ep.ByteTime) || math.IsInf(ep.ByteTime, 0) || ep.ByteTime < 0 {
		return errors.New("invalid retained byte-time")
	}
	if ep.Arm == armPrewarm && ep.Preparation == nil && !ep.Failed {
		return errors.New("successful prewarm episode lacks preparation")
	}
	seen := make(map[int]bool)
	maximum := int64(0)
	for _, launch := range ep.Launches {
		if launch.Index < 0 || launch.Index >= len(schedule) || seen[launch.Index] || launch.ScheduledNS != schedule[launch.Index] ||
			launch.StartedNS < 0 || launch.ReadyNS < launch.StartedNS ||
			launch.ScheduledToReadyNS != max(int64(0), launch.ReadyNS-launch.ScheduledNS) {
			return errors.New("invalid, repeated or inconsistent scheduled launch observation")
		}
		seen[launch.Index] = true
		maximum = max(maximum, launch.ScheduledToReadyNS)
		if (launch.Failed || launch.Process.TimedOut) && !ep.Failed {
			return errors.New("failed launch omitted from episode failure")
		}
	}
	if maximum != ep.MaxReadyNS {
		return errors.New("episode maximum does not match launch observations")
	}
	return nil
}

func knownGroup(in request, family, name string) bool {
	foundApp, foundScenario := false, false
	for _, app := range in.Apps {
		foundApp = foundApp || app.Family == family
	}
	for _, sc := range in.Scenarios {
		foundScenario = foundScenario || sc.Name == name
	}
	return foundApp && foundScenario
}

func compareGroup(in report, family, name string) (comparison, error) {
	item := comparison{Family: family, Scenario: name, InferenceStatus: "descriptive: pilot, incomplete or IID assumption not asserted"}
	baseline, prewarm := []tailrisk.Observation{}, []tailrisk.Observation{}
	baselineEpisodes, prewarmEpisodes := []episode{}, []episode{}
	for _, ep := range in.Episodes {
		if ep.Family != family || ep.Scenario != name {
			continue
		}
		obs := tailrisk.Observation{UnitID: ep.ID, Duration: ep.MaxReadyNS, Failed: ep.Failed, TimedOut: ep.TimedOut}
		violation := ep.Failed || ep.TimedOut || ep.MaxReadyNS > in.Request.SLONS
		if ep.Arm == armBaseline {
			baselineEpisodes = append(baselineEpisodes, ep)
			baseline = append(baseline, obs)
			item.BaselineAttempts++
			if ep.Failed {
				item.BaselineFailures++
			}
			if violation {
				item.BaselineViolations++
			}
		} else {
			prewarmEpisodes = append(prewarmEpisodes, ep)
			prewarm = append(prewarm, obs)
			item.PrewarmAttempts++
			if ep.Failed {
				item.PrewarmFailures++
			}
			if violation {
				item.PrewarmViolations++
			}
		}
	}
	item.BaselineCosts = summarizeCosts(baselineEpisodes)
	item.PrewarmCosts = summarizeCosts(prewarmEpisodes)
	if in.Complete && (len(baseline) != in.Request.SamplesPerArm || len(prewarm) != in.Request.SamplesPerArm) {
		return item, errors.New("complete report does not contain the fixed sample count in each arm")
	}
	if !in.Complete || in.Request.Phase != phaseConfirmation || !in.Request.IID {
		return item, nil
	}
	plan := tailrisk.Plan{Estimand: "probability any scheduled launch in an episode misses the completion SLO or fails",
		IndependentUnit: "complete independent deployment/launch episode", Cohort: family + "/" + name,
		Confirmation: in.Request.PilotReportSHA256, IID: true, FixedSample: true,
		BaselineSamples: in.Request.SamplesPerArm, CandidateSamples: in.Request.SamplesPerArm,
		SLONanoseconds: in.Request.SLONS, PracticalMargin: in.Request.Margin,
		Alpha: in.Request.Alpha, FamilySize: in.Request.FamilySize}
	formal, err := tailrisk.Analyze(tailrisk.Input{Plan: plan, Baseline: baseline, Candidate: prewarm,
		Provenance: tailrisk.Provenance{SourceRevision: in.ProducerRevision, GoVersion: in.GoVersion, Host: in.Request.Host,
			MeasurementKind: measurementKind, DatasetSHA256: in.RequestSHA256}})
	if err != nil {
		return item, err
	}
	item.Inference = &formal
	item.InferenceStatus = "conditional on declared IID and prospectively fixed protocol; neither is established by this report"
	return item, nil
}

func summarizeCosts(episodes []episode) *costSummary {
	result := costSummary{}
	preparation, foreground, first, span, cpu, byteTime := []float64{}, []float64{}, []float64{}, []float64{}, []float64{}, []float64{}
	for _, ep := range episodes {
		if ep.Failed || ep.TimedOut {
			continue
		}
		result.Samples++
		prep := int64(0)
		if ep.Preparation != nil {
			prep = ep.Preparation.WallNS
			result.InputBlocks += ep.Preparation.InputBlocks
			result.OutputBlocks += ep.Preparation.OutputBlocks
		}
		for _, launch := range ep.Launches {
			result.InputBlocks += launch.Process.InputBlocks
			result.OutputBlocks += launch.Process.OutputBlocks
		}
		preparation = append(preparation, float64(prep))
		foreground = append(foreground, float64(ep.MaxReadyNS))
		if ep.DeploymentFirstReadyNS != nil {
			first = append(first, float64(*ep.DeploymentFirstReadyNS))
		}
		span = append(span, float64(ep.DeploymentSpanNS))
		cpu = append(cpu, float64(ep.TotalCPUNS))
		byteTime = append(byteTime, ep.ByteTime)
	}
	if result.Samples == 0 {
		return nil
	}
	result.PreparationWall, result.ForegroundMax = median(preparation), median(foreground)
	if len(first) > 0 {
		value := median(first)
		result.DeploymentFirst = &value
	}
	result.DeploymentSpan = median(span)
	result.CPU, result.ByteTime = median(cpu), median(byteTime)
	return &result
}

func median(values []float64) float64 {
	slices.Sort(values)
	middle := len(values) / armCount
	if len(values)%armCount == 0 {
		return (values[middle-1] + values[middle]) / armCount
	}
	return values[middle]
}
