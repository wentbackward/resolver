// Resolver is a test harness that benchmarks LLMs on agentic tool-use
// tasks. Tier 1 ports the 31-query resolver-validation spec; Tier 2 adds
// multi-turn scenarios and tool-count + context-size sweeps.
//
// CLI contract documented in README.md.
package main

import (
	"context"
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
	defaultEndpoint = "http://spark-01:4000/v1/chat/completions"
	defaultModel    = "gresh-general"
)

type flags struct {
	endpoint string
	model    string
	tier     string
	scenario string
	sweep    string
	axis     string
	nSeeds   int
	gate     string
	parallel bool
	dryRun   bool
	apiKey   string
	replay   string
	dataDir  string
	out      string
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	f := parseFlags()
	ctx := context.Background()

	dataDir, err := resolveDataDir(f.dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	if f.sweep != "" {
		return runSweep(ctx, f, dataDir)
	}
	return runTier(ctx, f, dataDir)
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

	ad := adapter.NewOpenAIChat(f.endpoint)
	commonOpts := runner.ExecuteOpts{
		SystemPrompt: sysPrompt,
		Tools:        tools,
		Model:        f.model,
		APIKey:       f.apiKey,
		Timeout:      180 * time.Second,
	}
	if f.replay != "" {
		rp, err := loadReplay(f.replay)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		commonOpts.Replayer = rp
	}

	ts := time.Now().UTC()
	tok := tokenizer.Default()
	mb := manifest.NewBuilder(f.model, f.endpoint, ad.Name(), string(tok.Mode())).WithTier(f.tier)

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
				Tier: r.Tier, ID: r.ID, Query: firstUserText(&s),
				ExpectedTool: s.ExpectedTool, Score: r.Score, Reason: r.Reason,
				ElapsedMs: r.ElapsedMs, ToolCalls: r.ToolCalls,
				Content: nilIfEmpty(r.Content),
			})
		}
	} else {
		perQueries = runner.RunTier1(ctx, ad, scenarios, commonOpts)
	}

	meta := report.NewMeta(f.model, f.endpoint, ts, len(scenarios))
	sc := report.Build(meta, perQueries)

	outDir := f.out
	if outDir == "" {
		outDir = "reports/results"
	}
	path, err := report.Write(sc, outDir, ts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	mpath, err := mb.Write(outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: manifest write failed: %v\n", err)
	}

	printScorecard(os.Stdout, sc)
	fmt.Fprintf(os.Stderr, "scorecard: %s\n", path)
	if mpath != "" {
		fmt.Fprintf(os.Stderr, "manifest:  %s\n", mpath)
	}

	if sc.Summary.Overall == "PASS" {
		return 0
	}
	return 1
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

	ad := adapter.NewOpenAIChat(f.endpoint)
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
	flag.StringVar(&f.model, "model", envOr("RESOLVER_MODEL", defaultModel), "model identifier")
	flag.StringVar(&f.tier, "tier", "1", "tier to run: 1 or 2")
	flag.StringVar(&f.scenario, "scenario", "", "path to a single scenario YAML (overrides --tier)")
	flag.StringVar(&f.sweep, "sweep", "", "sweep name: tool-count|context-size")
	flag.StringVar(&f.axis, "axis", "", "comma-separated axis values (overrides sweep default)")
	flag.IntVar(&f.nSeeds, "n", 1, "seeds per sweep axis point")
	flag.StringVar(&f.gate, "gate", "", "gate policy YAML (sweep mode)")
	flag.BoolVar(&f.parallel, "parallel", false, "run sweep seeds in parallel")
	flag.BoolVar(&f.dryRun, "dry-run", false, "list scenarios without hitting the network")
	flag.StringVar(&f.apiKey, "api-key", os.Getenv("RESOLVER_API_KEY"), "bearer token (v1 stub for local llm-proxy)")
	flag.StringVar(&f.replay, "replay", "", "path to canned responses JSON for offline golden tests")
	flag.StringVar(&f.dataDir, "data-dir", "", "override embedded data with an external directory")
	flag.StringVar(&f.out, "out", "", "output directory (default reports/results or reports/sweeps)")
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
	tools, sys, err := ds.loadToolsAndPrompt()
	if err != nil {
		return nil, "", nil, err
	}
	var scenarios []scenario.Scenario
	if f.scenario != "" {
		scenarios, err = scenario.LoadFile(f.scenario)
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
	tools, err := ds.loadToolsFrom("shared/tools/resolver-tools.yaml")
	if err != nil {
		return nil, "", fmt.Errorf("load tools: %w", err)
	}
	sys, err := ds.readFile("tier1/system-prompt.md")
	if err != nil {
		return nil, "", fmt.Errorf("load system prompt: %w", err)
	}
	return tools, sys, nil
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

type replayEntry struct {
	ScenarioID string             `json:"scenarioId"`
	Content    string             `json:"content"`
	ToolCalls  []adapter.ToolCall `json:"toolCalls"`
}

type replayer struct {
	entries map[string]adapter.ChatResponse
}

func (r *replayer) Lookup(id string) (adapter.ChatResponse, bool) {
	got, ok := r.entries[id]
	return got, ok
}

func loadReplay(path string) (*replayer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	var entries []replayEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("replay parse: %w", err)
	}
	r := &replayer{entries: map[string]adapter.ChatResponse{}}
	for _, e := range entries {
		r.entries[e.ScenarioID] = adapter.ChatResponse{
			Content:   e.Content,
			ToolCalls: e.ToolCalls,
			ElapsedMs: 0,
		}
	}
	return r, nil
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
	for _, t := range scenario.AllTiers() {
		ts := sc.Summary.Tiers[t]
		fmt.Fprintf(w, "  %-4s  correct=%d partial=%d incorrect=%d errors=%d  total=%d  pct=%d%%\n",
			t, ts.Correct, ts.Partial, ts.Incorrect, ts.Errors, ts.Total, ts.Pct)
	}
	fmt.Fprintln(w)
	for _, th := range sc.Summary.Thresholds {
		status := "FAIL"
		if th.Pass {
			status = "PASS"
		}
		fmt.Fprintf(w, "  [%s]  %s  (pct=%d, threshold=%d)\n", status, th.Label, th.Pct, th.Threshold)
	}
	fmt.Fprintf(w, "\n  OVERALL: %s\n", sc.Summary.Overall)
	fmt.Fprintf(w, "  timing:  total=%dms avg=%dms p50=%dms p95=%dms max=%dms count=%d\n",
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
