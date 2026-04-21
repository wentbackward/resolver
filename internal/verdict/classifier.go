package verdict

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/scenario"
)

// classifierCallTimeout is the per-call hard deadline for the classifier
// adapter. §11.1 pre-mortem scenario 1: a 5 s fail-fast prevents a single
// slow call from hanging the sweep indefinitely. Callers can still set a
// tighter deadline via the parent context.
const classifierCallTimeout = 5 * time.Second

// classifierCtx bundles the classifier dependencies threaded through
// matchOne / matchAny. A nil classifierCtx means no classifier is available
// (--no-classifier path or classifier not yet injected); matchOne skips
// ClassifierMatch matchers silently in that case.
type classifierCtx struct {
	cl      adapter.Adapter
	ctx     context.Context
	dataDir string
}

// promptPath resolves promptRef relative to the context's dataDir. If dataDir
// is empty, the path is resolved relative to the current working directory.
func (cc *classifierCtx) promptPath(promptRef string) string {
	if filepath.IsAbs(promptRef) {
		return promptRef
	}
	if cc.dataDir == "" {
		return promptRef
	}
	return filepath.Join(cc.dataDir, promptRef)
}

// classifierRawResult is the raw output from one classifier invocation before
// interpretation. Kept separate from Result so B5 can capture ElapsedMs.
type classifierRawResult struct {
	Answer    string // "YES", "NO", or garbled output
	ElapsedMs int64
	Err       error
}

// callClassifier invokes the classifier adapter with the prompt template at
// promptPath, substituting content for the {{output}} placeholder.
//
// Applies classifierCallTimeout per call (§11.1 pre-mortem scenario 1). The
// parent ctx from classifierCtx may impose an outer deadline.
func callClassifier(cc *classifierCtx, promptPath, content string) classifierRawResult {
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return classifierRawResult{Err: fmt.Errorf("load prompt %q: %w", promptPath, err)}
	}
	prompt := strings.ReplaceAll(string(promptBytes), "{{output}}", content)

	callCtx, cancel := context.WithTimeout(cc.ctx, classifierCallTimeout)
	defer cancel()

	resp, err := cc.cl.Chat(callCtx, adapter.ChatRequest{
		Model:       "qwen2.5:3b",
		Messages:    []adapter.Message{{Role: "user", Content: prompt}},
		Temperature: 0,
		MaxTokens:   16,
		Timeout:     classifierCallTimeout,
	})
	if err != nil {
		return classifierRawResult{Err: fmt.Errorf("classifier call: %w", err)}
	}
	answer := strings.ToUpper(strings.TrimSpace(resp.Content))
	return classifierRawResult{Answer: answer, ElapsedMs: resp.ElapsedMs}
}

// interpretClassifier converts a classifierRawResult into a verdict Result.
//
//   - YES → ScoreCorrect
//   - NO  → ScoreIncorrect
//   - call error → ScoreError with the error message as Reason
//   - any other answer → ScoreError with the raw answer in Reason (parse failure)
func interpretClassifier(cr classifierRawResult, cm *scenario.ClassifierMatchSpec) Result {
	if cr.Err != nil {
		return Result{
			Score:  ScoreError,
			Reason: fmt.Sprintf("classifier error (%s): %v", cm.Claim, cr.Err),
		}
	}
	switch cr.Answer {
	case "YES":
		return Result{
			Score:  ScoreCorrect,
			Reason: fmt.Sprintf("classifier YES: %s", cm.Claim),
		}
	case "NO":
		return Result{
			Score:  ScoreIncorrect,
			Reason: fmt.Sprintf("classifier NO: %s", cm.Claim),
		}
	default:
		return Result{
			Score:  ScoreError,
			Reason: fmt.Sprintf("classifier parse error (%s): unexpected answer %q (want YES or NO)", cm.Claim, cr.Answer),
		}
	}
}
