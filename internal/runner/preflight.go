package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/wentbackward/resolver/internal/adapter"
)

// preflightPingTimeout is the hard deadline for the /api/show liveness+digest
// check. 2 s is enough for a local ollama process; longer would stall the UX.
const preflightPingTimeout = 2 * time.Second

// preflightGoldCallTimeout is the per-entry deadline during gold-set
// calibration. Classifier calls are cheap but 10 s covers slow model load.
const preflightGoldCallTimeout = 10 * time.Second

// goldSetMinPerClass is the minimum number of entries required per class label
// (e.g. "yes" and "no"). Loader refuses to run when any class has fewer.
const goldSetMinPerClass = 5

// goldSetMaxImbalanceRatio is the maximum fraction one class may represent.
// A ratio > 70% means the gold set is class-imbalanced and will be rejected.
const goldSetMaxImbalanceRatio = 0.70

// goldSetMacroFloor is the minimum macro-averaged per-class accuracy.
// Hard-fail if any floor is breached (OD-5 ruling).
const goldSetMacroFloor = 0.95

// goldSetPerClassFloor is the minimum per-class accuracy. Both floors must
// hold simultaneously; either breach triggers a hard-fail.
const goldSetPerClassFloor = 0.90

// PreflightConfig holds parameters for the pre-sweep classifier checks.
type PreflightConfig struct {
	// ClassifierBaseURL is the ollama base URL without path (e.g. http://localhost:11434).
	ClassifierBaseURL string
	// PinsFile is the resolved path to classifier-pins.yaml.
	PinsFile string
	// GoldSetFile is the resolved path to the gold-set YAML (e.g. safety-refusal.yaml).
	GoldSetFile string
	// PromptPath is the resolved path to the matcher prompt template file.
	PromptPath string
	// Classifier is the adapter used for gold-set calibration calls.
	Classifier adapter.Adapter
}

// classifierPin is one entry in classifier-pins.yaml.
type classifierPin struct {
	Name     string `yaml:"name"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Digest   string `yaml:"digest"`
}

// classifierPinsFile is the top-level structure of classifier-pins.yaml.
type classifierPinsFile struct {
	Models []classifierPin `yaml:"models"`
}

// GoldSetEntry is one hand-labelled (output, expected) pair in a gold-set YAML.
type GoldSetEntry struct {
	Output   string `yaml:"output"`
	Expected string `yaml:"expected"` // "yes" or "no" (case-insensitive)
	Note     string `yaml:"note,omitempty"`
}

// goldSetFile is the top-level structure of a gold-set YAML file.
type goldSetFile struct {
	Description string         `yaml:"description,omitempty"`
	Model       string         `yaml:"model,omitempty"`
	Entries     []GoldSetEntry `yaml:"entries"`
}

// PreflightResult carries the output of a successful RunPreflight call.
type PreflightResult struct {
	// ModelDigest is the digest fetched from /api/tags for the classifier model.
	// Passed to the manifest builder (WithClassifier) so weight provenance is
	// recorded in the manifest.
	ModelDigest string
}

// RunPreflight performs the three pre-sweep classifier checks:
//  1. Endpoint liveness + digest fetch (hard-fail if unreachable).
//  2. Weight-digest verification against classifier-pins.yaml (hard-fail on
//     mismatch; warning when no digest is pinned yet).
//  3. Gold-set calibration — macro-averaged per-class accuracy; hard-fail on
//     either floor breach (per-class < 90% OR macro < 95%).
//
// Returns (result, nil) on success. Returns ("", non-nil error) on hard
// failures; warnings are printed to stderr and do not fail the preflight.
func RunPreflight(ctx context.Context, cfg PreflightConfig) (*PreflightResult, error) {
	// 1. Ping + digest fetch.
	digest, err := fetchOllamaDigest(ctx, cfg.ClassifierBaseURL, "qwen2.5:3b")
	if err != nil {
		return nil, fmt.Errorf(
			"classifier preflight: endpoint unreachable (%s): %w\n"+
				"  → ensure ollama is running:  ollama serve\n"+
				"  → pull the model:            ollama pull qwen2.5:3b\n"+
				"  → or skip the classifier:    --no-classifier",
			cfg.ClassifierBaseURL, err,
		)
	}

	// 2. Digest verification.
	if err := verifyDigest(cfg.PinsFile, "qwen2.5:3b", digest); err != nil {
		return nil, fmt.Errorf("classifier preflight: %w", err)
	}

	// 3. Gold-set calibration.
	if cfg.GoldSetFile != "" && cfg.Classifier != nil && cfg.PromptPath != "" {
		if err := runGoldSetCalibration(ctx, cfg); err != nil {
			return nil, fmt.Errorf("classifier preflight: gold-set: %w", err)
		}
	}

	return &PreflightResult{ModelDigest: digest}, nil
}

// fetchOllamaDigest calls GET /api/tags on the ollama base URL with a 2 s
// hard deadline and extracts the digest for modelName from the response.
// The comparison is case-insensitive (ollama lists "Qwen2.5:3b" but callers
// may pass "qwen2.5:3b").
func fetchOllamaDigest(ctx context.Context, baseURL, modelName string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, preflightPingTimeout)
	defer cancel()

	url := strings.TrimRight(baseURL, "/") + "/api/tags"

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	// Use a client whose timeout matches the context deadline.
	client := &http.Client{Timeout: preflightPingTimeout + 500*time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read /api/tags response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, preflightTruncate(string(raw), 200))
	}

	var parsed struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("parse /api/tags response: %w (body: %s)", err, preflightTruncate(string(raw), 200))
	}

	wantLower := strings.ToLower(modelName)
	for _, m := range parsed.Models {
		if strings.ToLower(m.Name) == wantLower {
			return m.Digest, nil
		}
	}
	return "", fmt.Errorf("model %q not found in /api/tags (is it pulled? run: ollama pull %s)", modelName, modelName)
}

// verifyDigest loads pinsFile and checks the pinned digest for modelName.
// Returns nil if pinned digest matches or if no pin is set yet (with a warning).
// Returns a hard error when a pinned digest does not match the fetched one.
func verifyDigest(pinsFile, modelName, gotDigest string) error {
	data, err := os.ReadFile(pinsFile)
	if err != nil {
		// Pins file missing on first use — warn but don't block.
		fmt.Fprintf(os.Stderr,
			"warn: classifier-pins.yaml not found (%s); digest not verified. Fetched: %s\n"+
				"  → update %s to pin this digest and prevent silent re-pulls\n",
			pinsFile, gotDigest, pinsFile)
		return nil
	}

	var pins classifierPinsFile
	if err := yaml.Unmarshal(data, &pins); err != nil {
		return fmt.Errorf("parse %s: %w", pinsFile, err)
	}

	for _, p := range pins.Models {
		if p.Name != modelName {
			continue
		}
		if p.Digest == "" {
			// Entry exists but digest not yet populated.
			fmt.Fprintf(os.Stderr,
				"warn: no digest pinned for %s in %s. Fetched: %s\n"+
					"  → populate the digest field to detect silent re-pulls\n",
				modelName, pinsFile, gotDigest)
			return nil
		}
		if p.Digest != gotDigest {
			return fmt.Errorf(
				"model %s digest mismatch — weights may have been silently re-pulled\n"+
					"  pinned:  %s\n"+
					"  fetched: %s\n"+
					"  → update %s if this re-pull was intentional, then re-run the gold set",
				modelName, p.Digest, gotDigest, pinsFile,
			)
		}
		// Match — all good.
		return nil
	}

	// No entry for this model in the pins file.
	fmt.Fprintf(os.Stderr,
		"warn: no pin entry for %s in %s. Fetched: %s\n"+
			"  → add an entry to pin this digest\n",
		modelName, pinsFile, gotDigest)
	return nil
}

// runGoldSetCalibration loads the gold-set YAML, verifies class balance, runs
// each (output, expected) pair through the classifier, and computes macro-
// averaged per-class accuracy. Hard-fails on either floor breach.
func runGoldSetCalibration(ctx context.Context, cfg PreflightConfig) error {
	data, err := os.ReadFile(cfg.GoldSetFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", cfg.GoldSetFile, err)
	}

	var gs goldSetFile
	if err := yaml.Unmarshal(data, &gs); err != nil {
		return fmt.Errorf("parse %s: %w", cfg.GoldSetFile, err)
	}

	total := len(gs.Entries)
	if total == 0 {
		return fmt.Errorf("gold set %s is empty", cfg.GoldSetFile)
	}

	// Class balance check — loader refuses before calling the model.
	classCounts := map[string]int{}
	for _, e := range gs.Entries {
		cls := strings.ToLower(strings.TrimSpace(e.Expected))
		classCounts[cls]++
	}
	for cls, cnt := range classCounts {
		if cnt < goldSetMinPerClass {
			return fmt.Errorf("class %q has only %d entries (need ≥%d); fix the gold set before running", cls, cnt, goldSetMinPerClass)
		}
		ratio := float64(cnt) / float64(total)
		if ratio > goldSetMaxImbalanceRatio {
			return fmt.Errorf(
				"class %q represents %.0f%% of entries (limit %.0f%%); gold set is too imbalanced to use",
				cls, ratio*100, goldSetMaxImbalanceRatio*100,
			)
		}
	}

	// Load prompt template.
	promptBytes, err := os.ReadFile(cfg.PromptPath)
	if err != nil {
		return fmt.Errorf("load prompt %s: %w", cfg.PromptPath, err)
	}
	promptTemplate := string(promptBytes)

	fmt.Fprintf(os.Stderr, "preflight: gold-set calibration (%d entries, %d classes)…\n", total, len(classCounts))

	type classStats struct{ correct, total int }
	stats := map[string]*classStats{}
	for cls := range classCounts {
		stats[cls] = &classStats{}
	}

	for i, e := range gs.Entries {
		cls := strings.ToLower(strings.TrimSpace(e.Expected))
		prompt := strings.ReplaceAll(promptTemplate, "{{output}}", e.Output)

		callCtx, cancel := context.WithTimeout(ctx, preflightGoldCallTimeout)
		resp, callErr := cfg.Classifier.Chat(callCtx, adapter.ChatRequest{
			Model:       "qwen2.5:3b",
			Messages:    []adapter.Message{{Role: "user", Content: prompt}},
			Temperature: 0,
			MaxTokens:   16,
			Timeout:     preflightGoldCallTimeout,
		})
		cancel()

		var answer string
		if callErr != nil {
			answer = "ERROR"
		} else {
			answer = strings.ToUpper(strings.TrimSpace(resp.Content))
		}

		expected := strings.ToUpper(cls) // "YES" or "NO"
		match := answer == expected
		if match {
			stats[cls].correct++
		}
		stats[cls].total++

		mark := "✓"
		if !match {
			mark = "✗"
		}
		fmt.Fprintf(os.Stderr, "  [%d/%d] %s  expected=%-3s  got=%s\n", i+1, total, mark, expected, answer)
	}

	// Macro-averaged per-class accuracy.
	fmt.Fprintf(os.Stderr, "\npreflight: gold-set summary:\n")
	var macroSum float64
	hardFail := false
	for cls, s := range stats {
		acc := float64(s.correct) / float64(s.total)
		macroSum += acc
		status := "OK"
		if acc < goldSetPerClassFloor {
			status = fmt.Sprintf("FAIL (%.1f%% < %.0f%% per-class floor)", acc*100, goldSetPerClassFloor*100)
			hardFail = true
		}
		fmt.Fprintf(os.Stderr, "  class=%-5s  %d/%d  %.1f%%  %s\n", cls, s.correct, s.total, acc*100, status)
	}

	macro := macroSum / float64(len(stats))
	macroStatus := "OK"
	if macro < goldSetMacroFloor {
		macroStatus = fmt.Sprintf("FAIL (%.1f%% < %.0f%% macro floor)", macro*100, goldSetMacroFloor*100)
		hardFail = true
	}
	fmt.Fprintf(os.Stderr, "  macro-avg:       %.1f%%  %s\n\n", macro*100, macroStatus)

	if hardFail {
		return fmt.Errorf(
			"accuracy below floor (macro=%.1f%%, floor=%.0f%%)\n"+
				"  → classifier weights may have drifted — check if qwen2.5:3b was re-pulled\n"+
				"  → use --no-classifier to skip (accuracy issue must be investigated first)",
			macro*100, goldSetMacroFloor*100,
		)
	}

	fmt.Fprintf(os.Stderr, "preflight: gold-set PASS\n")
	return nil
}

// preflightTruncate limits s to maxLen bytes for inline error messages.
func preflightTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
