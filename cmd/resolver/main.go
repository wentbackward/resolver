// Resolver is a test harness that benchmarks LLMs on agentic tool-use
// tasks. Tier 1 ports the 31-query resolver-validation spec; Tier 2 adds
// multi-turn scenarios and tool-count + context-size sweeps.
//
// CLI contract documented in README.md.
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/aggregate"
	"github.com/wentbackward/resolver/internal/gate"
	"github.com/wentbackward/resolver/internal/manifest"
	"github.com/wentbackward/resolver/internal/report"
	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/tokenizer"
	"github.com/wentbackward/resolver/internal/verdict"
)

//go:embed all:data
var embeddedData embed.FS

const (
	// defaultEndpoint points at a local OpenAI-compatible chat endpoint.
	// Override via --endpoint or $RESOLVER_ENDPOINT for other deployments
	// (e.g. a remote llm-proxy on your network or a hosted provider).
	defaultEndpoint = "http://localhost:4000/v1/chat/completions"
	defaultModel    = "gresh-general"
)

type flags struct {
	endpoint    string
	adapterName string
	model       string
	tier        string
	scenario    string
	sweep       string
	axis        string
	nSeeds      int
	gate        string
	parallel    bool
	dryRun      bool
	apiKey      string
	replay      string
	emitReplay  string
	runConfig   string
	thresholds  string
	dataDir     string
	out         string
	role        string
	noJudge bool
}

// selectAdapter returns the Adapter for the given flags. Defaults to
// openai-chat; pass --adapter=ollama (or RESOLVER_ADAPTER=ollama) to use the
// ollama-chat adapter (adds retry/backoff on 503, targets localhost:11434 when
// endpoint is the default).
func selectAdapter(f flags) adapter.Adapter {
	switch f.adapterName {
	case "ollama", "ollama-chat":
		return adapter.NewOllamaChat(f.endpoint)
	default:
		return adapter.NewOpenAIChat(f.endpoint)
	}
}

func main() {
	// Subcommand dispatch — minimal and flag-compatible. `aggregate` is the
	// only one today; adding more in v2.1 (e.g., `lint`, `migrate`) will
	// follow the same shape.
	if len(os.Args) > 1 && os.Args[1] == "aggregate" {
		os.Exit(runAggregate(os.Args[2:]))
	}
	os.Exit(runMain())
}

func runAggregate(args []string) int {
	fs := flag.NewFlagSet("aggregate", flag.ExitOnError)
	reports := fs.String("reports", "", "comma-separated report roots (default: reports,research/captures)")
	db := fs.String("db", "reports/resolver.duckdb", "DuckDB file path")
	community := fs.String("community-benchmarks", "reports/community-benchmarks.yaml", "community benchmarks YAML (skipped if missing)")
	dry := fs.Bool("dry-run", false, "list runs that would be ingested without writing")
	_ = fs.Parse(args)

	// Skip community YAML silently if the canonical file doesn't exist yet —
	// it's scheduled for v2 plan Phase 6. Present its absence as neutral.
	cbPath := *community
	if cbPath != "" {
		if _, err := os.Stat(cbPath); err != nil {
			cbPath = ""
		}
	}

	err := aggregate.Run(aggregate.Options{
		ReportsDir:          *reports,
		DBPath:              *db,
		CommunityBenchmarks: cbPath,
		DryRun:              *dry,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	return 0
}

func runMain() int {
	f := parseFlags()
	ctx := context.Background()

	dataDir, err := resolveDataDir(f.dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	// Load the embedded gate-thresholds YAML as the canonical source; any
	// --thresholds override replaces it wholesale. If either fails, stderr
	// logs and falls back to the hardcoded defaults inside GatedTiers().
	if err := loadThresholds(f.thresholds, dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "warn: gate-thresholds load failed, using hardcoded defaults:", err)
	}

	if f.sweep != "" {
		return runSweep(ctx, f, dataDir)
	}
	return runTier(ctx, f, dataDir)
}

// loadThresholds sets the package-level GatedTiers() override. Priority:
//   1. `--thresholds PATH` on disk  (if supplied).
//   2. Embedded `shared/gate-thresholds.yaml` (v2.1 role-keyed defaults).
func loadThresholds(override string, dataDir dataSource) error {
	if override != "" {
		checks, err := scenario.LoadGateThresholds(override)
		if err != nil {
			return err
		}
		scenario.SetGatedTiers(checks)
		return nil
	}
	raw, err := dataDir.readFile("gate-thresholds.yaml")
	if err != nil {
		return err
	}
	checks, err := scenario.ParseGateThresholdsBytes([]byte(raw))
	if err != nil {
		return err
	}
	scenario.SetGatedTiers(checks)
	return nil
}

func runTier(ctx context.Context, f flags, dataDir dataSource) int {
	tools, sysPrompt, scenarios, err := loadTier(f, dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	if f.dryRun {
		return doDryRun(scenarios)
	}

	n := f.nSeeds
	if n < 1 {
		n = 1
	}

	// Judge setup: create the ollama adapter and run preflight (ping +
	// digest check + gold-set calibration) once before the repeat loop.
	// --no-judge short-circuits everything: judgeAd stays nil and
	// every Judge arm in verdict.matchOne is skipped silently.
	var (
		judgeAd           adapter.Adapter
		judgeDataDir      string
		judgeWeightDigest string
		judgePromptHash   string
	)
	if !f.noJudge {
		judgeAd = adapter.NewOllamaChat("")
		cdd, cleanup, err := setupJudgeDataDir(f, dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: judge data dir:", err)
			return 2
		}
		if cleanup != nil {
			defer cleanup()
		}
		judgeDataDir = cdd

		pinsPath := filepath.Join(judgeDataDir, "gold-sets", "judge-pins.yaml")
		goldPath := filepath.Join(judgeDataDir, "gold-sets", "safety-refusal.yaml")
		promptPath := filepath.Join(judgeDataDir, "judge-prompts", "safety-refusal.txt")
		pf := runner.PreflightConfig{
			JudgeBaseURL: "http://localhost:11434",
			PinsFile:          pinsPath,
			GoldSetFile:       goldPath,
			PromptPath:        promptPath,
			Judge:             judgeAd,
		}
		pfResult, pfErr := runner.RunPreflight(ctx, pf)
		if pfErr != nil {
			fmt.Fprintln(os.Stderr, "error:", pfErr)
			return 2
		}
		// Compute prompt hash for manifest provenance (B6).
		if promptBytes, readErr := os.ReadFile(promptPath); readErr == nil {
			h := sha256.Sum256(promptBytes)
			judgePromptHash = fmt.Sprintf("%x", h[:])
		}
		judgeWeightDigest = pfResult.ModelDigest
	}

	// Reproducibility-repeat loop: `-n N` runs Tier 1 N times in sequence.
	// k=0 keeps the single-run filename (backwards compatible with scripts
	// that glob `reports/results/*.json`); k>0 suffixes `-rep{k}` to avoid
	// clobbering. Every manifest in the batch carries `repeat_group` so
	// the aggregator can group them.
	var firstExitCode int
	var repeatGroup string
	for k := 0; k < n; k++ {
		suffix := ""
		if k > 0 {
			suffix = fmt.Sprintf("-rep%d", k)
		}
		code, rg := runTierOnce(ctx, f, dataDir, tools, sysPrompt, scenarios, suffix, repeatGroup,
			judgeAd, judgeDataDir, judgeWeightDigest, judgePromptHash)
		if k == 0 {
			firstExitCode = code
			repeatGroup = rg
		}
	}
	return firstExitCode
}

// setupJudgeDataDir resolves the filesystem directory from which
// judge data files (pins, gold-sets, judge-prompts) are read.
//
// When --data-dir is set the external directory is used directly.
// Otherwise, the files are extracted from the embedded FS to a temp dir so
// that os.ReadFile calls in preflight and verdict can reach them. The returned
// cleanup func removes the temp dir; it is nil when --data-dir is used.
func setupJudgeDataDir(f flags, ds dataSource) (string, func(), error) {
	if f.dataDir != "" {
		return f.dataDir, nil, nil
	}

	// Extract the judge data files from the embedded FS.
	needed := []string{
		"gold-sets/judge-pins.yaml",
		"gold-sets/safety-refusal.yaml",
		"judge-prompts/safety-refusal.txt",
	}

	tmpDir, err := os.MkdirTemp("", "resolver-judge-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	for _, rel := range needed {
		content, err := ds.readFile(rel)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("extract %s from embedded data: %w", rel, err)
		}
		dest := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return tmpDir, cleanup, nil
}

// runTierOnce is one iteration of the Tier 1 run. Lifted out of runTier
// so the reproducibility repeat loop can call it N times. `suffix` lands
// on the scorecard filename (e.g. `-rep2`); `repeatGroup` lands on the
// manifest runConfig so rows can be joined downstream.
//
// judgeAd is the pre-initialised judge adapter (nil on --no-judge).
// judgeDataDir is the resolved data directory for prompt_ref paths.
func runTierOnce(ctx context.Context, f flags, dataDir dataSource,
	tools []scenario.ToolDef, sysPrompt string, scenarios []scenario.Scenario,
	suffix, repeatGroup string,
	judgeAd adapter.Adapter, judgeDataDir string,
	judgeWeightDigest, judgePromptHash string) (int, string) {

	ad := selectAdapter(f)
	commonOpts := runner.ExecuteOpts{
		SystemPrompt: sysPrompt,
		Tools:        tools,
		Model:        f.model,
		APIKey:       f.apiKey,
		Timeout:      180 * time.Second,
		Judge:   judgeAd,
		DataDir:      judgeDataDir,
	}

	// Replay mode: override scorecard meta so the golden diff is a pure
	// function of the replay file + verdict code (timestamp/model/endpoint
	// come from the capture, not from the current run).
	var capturedMeta *report.Meta
	if f.replay != "" {
		rp, cm, err := loadReplay(f.replay)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2, repeatGroup
		}
		commonOpts.Replayer = rp
		capturedMeta = cm
	}

	ts := time.Now().UTC()
	tok := tokenizer.Default()
	resolvedRole := resolveScenarioRole(scenarios)
	mb := manifest.NewBuilder(f.model, f.endpoint, ad.Name(), string(tok.Mode())).WithRole(resolvedRole)
	if f.noJudge {
		mb = mb.WithJudgeDisabled()
	} else if judgeWeightDigest != "" {
		mb = mb.WithJudge("qwen2.5:3b", judgeWeightDigest, "http://localhost:11434",
			"judge-prompts/safety-refusal.txt", judgePromptHash)
	}

	// Optional --run-config sidecar: capture proxy + vLLM recipe metadata into
	// the manifest alongside the scorecard. Unknown values stay unset.
	var rc *manifest.RunConfig
	if f.runConfig != "" {
		loaded, err := manifest.LoadSidecar(f.runConfig)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2, repeatGroup
		}
		rc = loaded
	}
	// -n N batch: stamp repeat_group on every manifest in the batch so the
	// aggregator can join them. k=0 self-seeds (its own RunID is the group);
	// k>0 inherits the group the caller threaded down. Setting on every
	// iteration — including k=0 of a single-shot run — means every manifest
	// always carries the join key; downstream consumers see a repeat_group
	// of cardinality 1 for single runs and N for -n N batches.
	if rc == nil {
		rc = &manifest.RunConfig{}
	}
	if repeatGroup != "" {
		rc.RepeatGroup = repeatGroup
	} else {
		rc.RepeatGroup = mb.Current().RunID
	}
	mb = mb.WithRunConfig(rc)

	// /v1/models probe (skip under --replay; the scorecard's `meta.model` is
	// the *virtual* model name, not the real one, so carrying it forward
	// would mislead downstream diffs. In replay mode users rely on the
	// --run-config sidecar's real_model field instead.)
	//
	// Not all adapters implement the probe (e.g. ollama-chat targets a local
	// endpoint where the "virtual model" concept doesn't apply). Use a local
	// interface so adapters that provide it opt in without changing Adapter.
	if capturedMeta == nil {
		type modelResolver interface {
			ResolveRealModel(ctx context.Context, forModel string) string
		}
		if mr, ok := ad.(modelResolver); ok {
			mb = mb.WithResolvedRealModel(mr.ResolveRealModel(ctx, f.model))
		} else {
			mb = mb.WithResolvedRealModel("unknown")
		}
	}

	var perQueries []runner.PerQuery
	if f.tier == "2" || isMultiTurn(scenarios) {
		mt := runner.MultiTurnOpts{
			ExecuteOpts: commonOpts,
			MaxTurns:    8,
			Tokenizer:   tok,
			Fixtures:    dataDir.mockFixtures(),
			MockTools:   runner.BuildMockTools(dataDir.mockFixtures(), tok),
		}
		for _, s := range scenarios {
			r := runner.RunMultiTurn(ctx, ad, &s, mt)
			perQueries = append(perQueries, runner.PerQuery{
				Tier: r.Tier, Role: s.Role, ID: r.ID, Query: firstUserText(&s),
				ExpectedTool: s.ExpectedTool, Score: r.Score, Reason: r.Reason,
				ElapsedMs: r.ElapsedMs, ToolCalls: r.ToolCalls,
				Content: nilIfEmpty(r.Content),
			})
		}
	} else {
		perQueries = runner.RunTier1(ctx, ad, scenarios, commonOpts)
	}

	var meta report.Meta
	metaTs := ts
	if capturedMeta != nil {
		meta = *capturedMeta
		meta.QueryCount = len(scenarios)
	} else {
		meta = report.NewMeta(f.model, f.endpoint, ts, len(scenarios))
	}
	sc := report.Build(meta, perQueries)

	outDir := f.out
	if outDir == "" {
		outDir = "reports/results"
	}
	path, err := report.WriteWithSuffix(sc, outDir, metaTs, suffix)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2, repeatGroup
	}
	mpath, err := mb.Write(outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: manifest write failed: %v\n", err)
	}

	// Emit-replay: capture the live run's per-query responses to a file that
	// can be fed back via --replay for deterministic golden tests.
	if f.emitReplay != "" && capturedMeta == nil {
		if err := writeReplay(f.emitReplay, meta, perQueries); err != nil {
			fmt.Fprintf(os.Stderr, "warn: emit-replay failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "replay:    %s\n", f.emitReplay)
		}
	}

	printScorecard(os.Stdout, sc)
	fmt.Fprintf(os.Stderr, "scorecard: %s\n", path)
	if mpath != "" {
		fmt.Fprintf(os.Stderr, "manifest:  %s\n", mpath)
	}

	// First iteration of an -n N batch: seed repeat_group with this run's
	// ID so subsequent iterations carry the join key.
	outGroup := repeatGroup
	if outGroup == "" {
		outGroup = mb.Current().RunID
	}

	// v2.1 exit code: any gated role with Verdict == "FAIL" fails the run.
	// INFO-verdict roles (ungated) never block the exit code. An empty
	// Roles map (no scenarios) is treated as a harness error (code 2),
	// but that's caught earlier by loadTier.
	exitCode := 0
	for _, rs := range sc.Summary.Roles {
		if rs.Verdict == "FAIL" {
			exitCode = 1
			break
		}
	}
	return exitCode, outGroup
}

func runSweep(ctx context.Context, f flags, dataDir dataSource) int {
	tools, sysPrompt, err := dataDir.loadToolsAndPrompt()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	scPath := fmt.Sprintf("tier2-sweeps/%s.yaml", f.sweep)
	scenarios, err := dataDir.loadScenarioFile(scPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if len(scenarios) == 0 {
		fmt.Fprintln(os.Stderr, "error: no scenarios in", scPath)
		return 2
	}
	sc := scenarios[0]

	axis := parseAxis(f.axis, f.sweep)
	if f.dryRun {
		fmt.Printf("sweep=%s\tscenario=%s\taxis=%v\tseeds=%d\tparallel=%v\n", f.sweep, sc.ID, axis, f.nSeeds, f.parallel)
		return 0
	}

	ad := selectAdapter(f)
	tok := tokenizer.Default()
	mt := runner.MultiTurnOpts{
		ExecuteOpts: runner.ExecuteOpts{
			SystemPrompt: sysPrompt,
			Tools:        tools,
			Model:        f.model,
			APIKey:       f.apiKey,
			Timeout:      180 * time.Second,
		},
		MaxTurns:  4,
		Tokenizer: tok,
		Fixtures:  dataDir.mockFixtures(),
	}

	rows := runner.RunSweep(ctx, ad, runner.SweepConfig{
		Kind:      runner.SweepKind(f.sweep),
		Scenario:  sc,
		Axis:      axis,
		Seeds:     max1(f.nSeeds),
		Parallel:  f.parallel,
		Tokenizer: tok,
	}, mt)

	ts := time.Now().UTC()
	outDir := f.out
	if outDir == "" {
		outDir = "reports/sweeps"
	}
	header, rowsCSV := formatSweepCSV(runner.SweepKind(f.sweep), rows)
	w, csvPath, err := report.NewCSVWriter(outDir, f.model, f.sweep, ts, header)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	for _, r := range rowsCSV {
		_ = w.Write(r)
	}
	if err := w.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	seeds := make([]int, f.nSeeds)
	for i := range seeds {
		seeds[i] = i
	}
	mb := manifest.NewBuilder(f.model, f.endpoint, ad.Name(), string(tok.Mode())).
		WithSweep(f.sweep, seeds, f.parallel)
	if _, err := mb.Write(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "warn: manifest write failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "sweep csv: %s\n", csvPath)
	printSweepSummary(os.Stdout, f.sweep, rows)

	if f.gate != "" {
		pol, err := gate.Load(f.gate)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		gateRows := gateRowsFromSweep(runner.SweepKind(f.sweep), rows)
		result := gate.Evaluate(pol, gateRows)
		printGate(os.Stdout, result)
		if !result.OverallPass {
			return 1
		}
	}
	return 0
}

// ---- flag parsing & env ----

func parseFlags() flags {
	f := flags{}
	flag.StringVar(&f.endpoint, "endpoint", envOr("RESOLVER_ENDPOINT", defaultEndpoint), "chat completions endpoint")
	flag.StringVar(&f.adapterName, "adapter", envOr("RESOLVER_ADAPTER", "openai-chat"), "adapter to use: openai-chat (default) or ollama-chat")
	flag.StringVar(&f.model, "model", envOr("RESOLVER_MODEL", defaultModel), "model identifier")
	flag.StringVar(&f.tier, "tier", "1", "tier to run: 1 or 2")
	flag.StringVar(&f.role, "role", "", "role to run (e.g. agentic-toolcall); loads roles/<role>/*.yaml with its own system prompt")
	flag.StringVar(&f.scenario, "scenario", "", "path to a single scenario YAML (overrides --tier)")
	flag.StringVar(&f.sweep, "sweep", "", "sweep name: tool-count|context-size")
	flag.StringVar(&f.axis, "axis", "", "comma-separated axis values (overrides sweep default)")
	flag.IntVar(&f.nSeeds, "n", 3, "seeds per sweep axis point")
	flag.StringVar(&f.gate, "gate", "", "gate policy YAML (sweep mode)")
	flag.BoolVar(&f.parallel, "parallel", false, "run sweep seeds in parallel")
	flag.BoolVar(&f.dryRun, "dry-run", false, "list scenarios without hitting the network")
	flag.StringVar(&f.apiKey, "api-key", os.Getenv("RESOLVER_API_KEY"), "bearer token (v1 stub for local llm-proxy)")
	flag.StringVar(&f.replay, "replay", "", "path to canned responses JSON for offline golden tests")
	flag.StringVar(&f.emitReplay, "emit-replay", "", "capture this live run to a replay JSON (alongside scorecard)")
	flag.StringVar(&f.runConfig, "run-config", "", "path to a run-config YAML sidecar (proxy route + vLLM recipe metadata captured into the manifest)")
	flag.StringVar(&f.thresholds, "thresholds", "", "path to a gate-thresholds YAML overriding the embedded defaults")
	flag.StringVar(&f.dataDir, "data-dir", "", "override embedded data with an external directory")
	flag.StringVar(&f.out, "out", "", "output directory (default reports/results or reports/sweeps)")
	flag.BoolVar(&f.noJudge, "no-judge", envOr("RESOLVER_NO_JUDGE", "") != "", "disable the LLM-as-judge matcher (skips preflight and all Judge arms)")
	flag.Parse()
	return f
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// ---- scenario loading helpers ----

func loadTier(f flags, ds dataSource) ([]scenario.ToolDef, string, []scenario.Scenario, error) {
	tools, sys, err := ds.loadToolsAndPromptFor(f.role)
	if err != nil {
		return nil, "", nil, err
	}
	var scenarios []scenario.Scenario
	if f.scenario != "" {
		scenarios, err = scenario.LoadFile(f.scenario)
		if err != nil {
			return nil, "", nil, err
		}
	} else if f.role != "" {
		scenarios, err = ds.walkScenarios("roles/" + f.role)
		if err != nil {
			return nil, "", nil, err
		}
	} else {
		scenarios, err = ds.loadTierScenarios(f.tier)
		if err != nil {
			return nil, "", nil, err
		}
	}
	if len(scenarios) == 0 {
		return nil, "", nil, fmt.Errorf("no scenarios loaded")
	}
	return tools, sys, scenarios, nil
}

func doDryRun(scenarios []scenario.Scenario) int {
	for _, s := range scenarios {
		fmt.Printf("%s\t%s\t%s\n", s.Tier, s.ID, s.ExpectedTool)
		if s.Query != "" {
			fmt.Printf("\tquery: %s\n", oneLine(s.Query))
		} else {
			fmt.Printf("\tturns: %d\n", len(s.Turns))
		}
		if len(s.Rule.CorrectIf) > 0 {
			fmt.Printf("\tcorrect_if: %d matcher(s)\n", len(s.Rule.CorrectIf))
		}
		if len(s.Rule.PartialIf) > 0 {
			fmt.Printf("\tpartial_if: %d matcher(s)\n", len(s.Rule.PartialIf))
		}
	}
	return 0
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 140 {
		return s[:140] + "…"
	}
	return s
}

// resolveScenarioRole picks the common Role across a scenario batch so
// the manifest can stamp a single role bucket per run. Loader folds
// `shared.role` into each Scenario.Role, so the per-scenario field is
// the single source of truth. Returns "" when the batch predates the
// v2.1 migration (legacy tier-only scenarios) — callers accept that
// since WithRole omits empty values in JSON.
func resolveScenarioRole(scenarios []scenario.Scenario) string {
	for _, s := range scenarios {
		if s.Role != "" {
			return string(s.Role)
		}
	}
	return ""
}

func isMultiTurn(scenarios []scenario.Scenario) bool {
	for _, s := range scenarios {
		if len(s.Turns) > 0 {
			return true
		}
	}
	return false
}

func firstUserText(s *scenario.Scenario) string {
	if s.Query != "" {
		return s.Query
	}
	for _, t := range s.Turns {
		if t.Role == "user" {
			return t.Content
		}
	}
	return ""
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- data source ----

type dataSource struct {
	external string
}

func resolveDataDir(override string) (dataSource, error) {
	if override != "" {
		info, err := os.Stat(override)
		if err != nil {
			return dataSource{}, fmt.Errorf("data-dir %s: %w", override, err)
		}
		if !info.IsDir() {
			return dataSource{}, fmt.Errorf("data-dir %s: not a directory", override)
		}
		return dataSource{external: override}, nil
	}
	return dataSource{}, nil
}

func (ds dataSource) mockFixtures() runner.MockFixturesFS {
	if ds.external != "" {
		return runner.DirFixtures{Root: filepath.Join(ds.external, "fixtures")}
	}
	sub, err := fs.Sub(embeddedData, "data/fixtures")
	if err != nil {
		return nil
	}
	return runner.FSFixtures{FS: sub}
}

func (ds dataSource) loadToolsAndPrompt() ([]scenario.ToolDef, string, error) {
	return ds.loadToolsAndPromptFor("")
}

func (ds dataSource) loadToolsAndPromptFor(role string) ([]scenario.ToolDef, string, error) {
	tools, err := ds.loadToolsFrom("shared/tools/resolver-tools.yaml")
	if err != nil {
		return nil, "", fmt.Errorf("load tools: %w", err)
	}

	// Always load the top-level sysadm frame.
	top, err := ds.readFile("system-prompt.md")
	if err != nil {
		return nil, "", fmt.Errorf("load top-level system prompt: %w", err)
	}
	if role == "" {
		return tools, top, nil
	}

	// Role override: `system-prompt.override.md` fully replaces the top-level
	// prompt. Use for roles that aren't sysadm probes (classifier, reducer-*).
	if override, err := ds.readFile("roles/" + role + "/system-prompt.override.md"); err == nil {
		return tools, override, nil
	}

	// Role extension: `system-prompt.md` is appended to the top-level prompt
	// with a blank-line separator. Use for probe-specific instructions that
	// layer on top of the sysadm frame (e.g. agentic-toolcall's tool-call-only
	// rule, long-context's "context facts supersede defaults").
	if ext, err := ds.readFile("roles/" + role + "/system-prompt.md"); err == nil {
		return tools, top + "\n\n" + ext, nil
	}

	// Role has no custom prompt — use the top-level unchanged.
	return tools, top, nil
}

func (ds dataSource) loadTierScenarios(tier string) ([]scenario.Scenario, error) {
	prefix := ""
	switch tier {
	case "1":
		prefix = "tier1"
	case "2":
		prefix = "tier2-multiturn"
	default:
		return nil, fmt.Errorf("unknown tier %s", tier)
	}
	return ds.walkScenarios(prefix)
}

func (ds dataSource) walkScenarios(prefix string) ([]scenario.Scenario, error) {
	if ds.external != "" {
		return scenario.LoadTree(filepath.Join(ds.external, prefix))
	}
	sub, err := fs.Sub(embeddedData, "data")
	if err != nil {
		return nil, err
	}
	var all []scenario.Scenario
	err = fs.WalkDir(sub, prefix, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !(strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), "_") {
			return nil
		}
		data, rerr := fs.ReadFile(sub, p)
		if rerr != nil {
			return rerr
		}
		tmp, werr := writeTempYAML(data)
		if werr != nil {
			return werr
		}
		defer os.Remove(tmp)
		loaded, lerr := scenario.LoadFile(tmp)
		if lerr != nil {
			return lerr
		}
		all = append(all, loaded...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func (ds dataSource) loadScenarioFile(rel string) ([]scenario.Scenario, error) {
	if ds.external != "" {
		return scenario.LoadFile(filepath.Join(ds.external, rel))
	}
	raw, err := fs.ReadFile(embeddedData, filepath.Join("data", rel))
	if err != nil {
		return nil, err
	}
	tmp, err := writeTempYAML(raw)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	return scenario.LoadFile(tmp)
}

func writeTempYAML(data []byte) (string, error) {
	f, err := os.CreateTemp("", "resolver-*.yaml")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

func (ds dataSource) loadToolsFrom(rel string) ([]scenario.ToolDef, error) {
	if ds.external != "" {
		return scenario.LoadSharedTools(filepath.Join(ds.external, rel))
	}
	raw, err := fs.ReadFile(embeddedData, filepath.Join("data", rel))
	if err != nil {
		return nil, err
	}
	tmp, err := writeTempYAML(raw)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	return scenario.LoadSharedTools(tmp)
}

func (ds dataSource) readFile(rel string) (string, error) {
	if ds.external != "" {
		data, err := os.ReadFile(filepath.Join(ds.external, rel))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := fs.ReadFile(embeddedData, filepath.Join("data", rel))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ---- replay ----

// replayEntry is one scenario's captured response. ElapsedMs is preserved
// so scorecard timings are fully deterministic under --replay.
type replayEntry struct {
	ScenarioID string             `json:"scenarioId"`
	Content    string             `json:"content"`
	ToolCalls  []adapter.ToolCall `json:"toolCalls"`
	ElapsedMs  int64              `json:"elapsedMs,omitempty"`
}

// replayFile is the on-disk envelope. Meta captures the scorecard.meta
// values at the time of live-capture so replay can reproduce them
// byte-for-byte.
type replayFile struct {
	Meta    report.Meta   `json:"meta"`
	Entries []replayEntry `json:"entries"`
}

type replayer struct {
	entries map[string]adapter.ChatResponse
}

func (r *replayer) Lookup(id string) (adapter.ChatResponse, bool) {
	got, ok := r.entries[id]
	return got, ok
}

// loadReplay accepts either the new envelope shape (preferred) or a bare
// array of replayEntry (v0 shape). The bare-array case falls back to zero
// meta so the caller can proceed with live-run meta.
func loadReplay(path string) (*replayer, *report.Meta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("replay: %w", err)
	}
	var (
		env     replayFile
		bare    []replayEntry
		entries []replayEntry
		meta    *report.Meta
	)
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Entries) > 0 {
		entries = env.Entries
		m := env.Meta
		meta = &m
	} else if err := json.Unmarshal(raw, &bare); err == nil {
		entries = bare
	} else {
		return nil, nil, fmt.Errorf("replay parse: unrecognised shape in %s", path)
	}

	r := &replayer{entries: map[string]adapter.ChatResponse{}}
	for _, e := range entries {
		r.entries[e.ScenarioID] = adapter.ChatResponse{
			Content:   e.Content,
			ToolCalls: e.ToolCalls,
			ElapsedMs: e.ElapsedMs,
		}
	}
	return r, meta, nil
}

// writeReplay emits the envelope from a live run's per-query results.
func writeReplay(path string, meta report.Meta, results []runner.PerQuery) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "" {
		return err
	}
	env := replayFile{Meta: meta}
	for _, r := range results {
		content := ""
		if s, ok := r.Content.(string); ok {
			content = s
		}
		env.Entries = append(env.Entries, replayEntry{
			ScenarioID: r.ID,
			Content:    content,
			ToolCalls:  r.ToolCalls,
			ElapsedMs:  r.ElapsedMs,
		})
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ---- axis / sweep helpers ----

func parseAxis(s, kind string) []int {
	if s != "" {
		return parseIntList(s)
	}
	switch runner.SweepKind(kind) {
	case runner.SweepToolCount:
		return []int{5, 20, 50, 100, 300}
	case runner.SweepContextSize:
		return []int{5000, 40000, 80000, 120000, 200000}
	}
	return nil
}

func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v, err := strconv.Atoi(p); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func formatSweepCSV(kind runner.SweepKind, rows []runner.SweepRow) ([]string, [][]string) {
	switch kind {
	case runner.SweepToolCount:
		header := []string{"tool_count", "seed", "score", "tools_called", "wrong_tool_count", "hallucinated_tool_count", "completed", "elapsed_ms"}
		out := make([][]string, len(rows))
		for i, r := range rows {
			out[i] = []string{
				strconv.Itoa(r.AxisValue), strconv.Itoa(r.Seed), string(r.Score),
				strconv.Itoa(r.ToolsCalled), strconv.Itoa(r.WrongToolCount),
				strconv.Itoa(r.HallucinatedToolCount), boolS(r.Completed), strconv.FormatInt(r.ElapsedMs, 10),
			}
		}
		return header, out
	case runner.SweepContextSize:
		header := []string{"context_tokens", "seed", "needle_found", "accuracy", "elapsed_ms", "score"}
		out := make([][]string, len(rows))
		for i, r := range rows {
			out[i] = []string{
				strconv.Itoa(r.AxisValue), strconv.Itoa(r.Seed), boolS(r.NeedleFound),
				strconv.FormatFloat(r.Accuracy, 'f', -1, 64), strconv.FormatInt(r.ElapsedMs, 10),
				string(r.Score),
			}
		}
		return header, out
	}
	return nil, nil
}

func boolS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func gateRowsFromSweep(kind runner.SweepKind, rows []runner.SweepRow) []gate.Row {
	out := make([]gate.Row, len(rows))
	for i, r := range rows {
		row := gate.Row{
			"seed":        float64(r.Seed),
			"elapsed_ms":  float64(r.ElapsedMs),
			"completed":   boolF(r.Completed),
		}
		switch kind {
		case runner.SweepToolCount:
			row["tool_count"] = float64(r.AxisValue)
			row["tools_called"] = float64(r.ToolsCalled)
			row["wrong_tool_count"] = float64(r.WrongToolCount)
			row["hallucinated_tool_count"] = float64(r.HallucinatedToolCount)
			row["accuracy"] = verdictToAcc(r.Score)
		case runner.SweepContextSize:
			row["context_tokens"] = float64(r.AxisValue)
			row["needle_found"] = boolF(r.NeedleFound)
			row["accuracy"] = r.Accuracy
		}
		out[i] = row
	}
	return out
}

func boolF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// verdictToAcc maps a verdict score to the 0.0–1.0 accuracy metric used by
// gate rules. correct=1.0, partial=0.5, anything else=0.
func verdictToAcc(s verdict.Score) float64 {
	switch s {
	case verdict.ScoreCorrect:
		return 1.0
	case verdict.ScorePartial:
		return 0.5
	}
	return 0
}

// ---- pretty printers ----

func printScorecard(w *os.File, sc report.Scorecard) {
	fmt.Fprintf(w, "\nResolver scorecard — %s @ %s (%s)\n", sc.Meta.Model, sc.Meta.Endpoint, sc.Meta.Timestamp)
	fmt.Fprintln(w, strings.Repeat("-", 72))
	for _, r := range scenario.AllRoles() {
		rs, ok := sc.Summary.Roles[r]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "  %-22s  %-4s  correct=%d partial=%d incorrect=%d errors=%d  total=%d  pct=%d%%\n",
			r, rs.Verdict, rs.Correct, rs.Partial, rs.Incorrect, rs.Errors, rs.Total, rs.Pct)
	}
	fmt.Fprintln(w)
	for _, th := range sc.Summary.Thresholds {
		status := "FAIL"
		if th.Pass {
			status = "PASS"
		}
		fmt.Fprintf(w, "  [%s]  %s  (pct=%d, threshold=%g)\n", status, th.Label, th.Pct, th.Threshold)
	}
	fmt.Fprintf(w, "\n  timing:  total=%dms avg=%dms p50=%dms p95=%dms max=%dms count=%d\n",
		sc.Summary.Timing.TotalMs, sc.Summary.Timing.AvgMs, sc.Summary.Timing.P50Ms,
		sc.Summary.Timing.P95Ms, sc.Summary.Timing.MaxMs, sc.Summary.Timing.Count)
}

func printSweepSummary(w *os.File, sweep string, rows []runner.SweepRow) {
	fmt.Fprintf(w, "\nSweep %s — %d rows\n", sweep, len(rows))
	fmt.Fprintln(w, strings.Repeat("-", 72))
	byAxis := map[int][]runner.SweepRow{}
	order := []int{}
	for _, r := range rows {
		if _, ok := byAxis[r.AxisValue]; !ok {
			order = append(order, r.AxisValue)
		}
		byAxis[r.AxisValue] = append(byAxis[r.AxisValue], r)
	}
	for _, ax := range order {
		grp := byAxis[ax]
		correct, needles, comp := 0, 0, 0
		var elapsed int64
		for _, r := range grp {
			if r.Score == "correct" {
				correct++
			}
			if r.NeedleFound {
				needles++
			}
			if r.Completed {
				comp++
			}
			elapsed += r.ElapsedMs
		}
		fmt.Fprintf(w, "  axis=%d  n=%d  correct=%d  needle_found=%d  completed=%d  avgMs=%d\n",
			ax, len(grp), correct, needles, comp, elapsed/int64(len(grp)))
	}
}

func printGate(w *os.File, r gate.Result) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("-", 72))
	for _, v := range r.Verdicts {
		status := "FAIL"
		if v.Pass {
			status = "PASS"
		}
		label := v.Rule.Label
		if label == "" {
			label = fmt.Sprintf("%s %s %g", v.Rule.Metric, v.Rule.Operator, v.Rule.Threshold)
		}
		fmt.Fprintf(w, "  [%s]  %s  (observed=%.4f, threshold=%g)\n", status, label, v.Observed, v.Rule.Threshold)
		if v.Note != "" {
			fmt.Fprintf(w, "         note: %s\n", v.Note)
		}
	}
	overall := "FAIL"
	if r.OverallPass {
		overall = "PASS"
	}
	fmt.Fprintf(w, "\n  GATE OVERALL: %s\n", overall)
}
