package verdict

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// ClassifierMeta carries the rich classifier verdict attached to a Result when
// a ClassifierMatch matcher fires. Used by the runner (B5) to populate
// PerQuery twin-fields and ClassifierInputSnapshot.
type ClassifierMeta struct {
	Score     Score
	Reason    string
	ElapsedMs int64
	PromptRef string

	// Snapshot fields for bit-reproducible replay (OD-1 residual / B5).
	ContentHash          string // sha256 of the classifier's input (MUT content)
	PromptHash           string // sha256 of the prompt file at call time
	ClassifierParamsHash string // sha256 of canonical JSON {max_tokens,seed,temperature,top_p}
}

// classifierRawResult is the raw output from one classifier invocation before
// interpretation. Carries the snapshot hashes computed during the call.
type classifierRawResult struct {
	Answer    string // "YES", "NO", or garbled output
	ElapsedMs int64
	Err       error

	// Snapshot fields (populated even on error for audit purposes).
	PromptRef            string
	ContentHash          string
	PromptHash           string
	ClassifierParamsHash string
}

// classifierParams is the fixed set of sampling parameters passed to the
// classifier adapter on every call. Canonicalised JSON of this struct is
// sha256'd to form ClassifierParamsHash in the snapshot.
type classifierParams struct {
	MaxTokens   int     `json:"max_tokens"`
	Seed        *int    `json:"seed"`        // nil = not pinned
	Temperature float64 `json:"temperature"`
	TopP        *float64 `json:"top_p"`      // nil = not pinned
}

// fixedClassifierParams is the canonical parameter set for every classifier
// call. Temperature=0 enforced per spec Principle 3.
var fixedClassifierParams = classifierParams{
	MaxTokens:   16,
	Temperature: 0,
}

// hashClassifierParams returns the sha256 hex digest of the canonical JSON
// encoding of the fixed classifier params. Cached after first computation.
func hashClassifierParams() string {
	b, _ := json.Marshal(fixedClassifierParams)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sha256Hex returns the hex-encoded sha256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// callClassifier invokes the classifier adapter with the prompt template at
// promptPath, substituting content for the {{output}} placeholder.
//
// Applies classifierCallTimeout per call (§11.1 pre-mortem scenario 1). The
// parent ctx from classifierCtx may impose an outer deadline. All snapshot
// hashes are computed before the call so they are available even on error.
func callClassifier(cc *classifierCtx, promptPath, content string) classifierRawResult {
	contentHash := sha256Hex([]byte(content))
	paramsHash := hashClassifierParams()

	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return classifierRawResult{
			PromptRef:            promptPath,
			ContentHash:          contentHash,
			ClassifierParamsHash: paramsHash,
			Err:                  fmt.Errorf("load prompt %q: %w", promptPath, err),
		}
	}
	promptHash := sha256Hex(promptBytes)
	prompt := strings.ReplaceAll(string(promptBytes), "{{output}}", content)

	callCtx, cancel := context.WithTimeout(cc.ctx, classifierCallTimeout)
	defer cancel()

	resp, err := cc.cl.Chat(callCtx, adapter.ChatRequest{
		Model:       "qwen2.5:3b",
		Messages:    []adapter.Message{{Role: "user", Content: prompt}},
		Temperature: fixedClassifierParams.Temperature,
		MaxTokens:   fixedClassifierParams.MaxTokens,
		Timeout:     classifierCallTimeout,
	})
	if err != nil {
		return classifierRawResult{
			PromptRef:            promptPath,
			ContentHash:          contentHash,
			PromptHash:           promptHash,
			ClassifierParamsHash: paramsHash,
			Err:                  fmt.Errorf("classifier call: %w", err),
		}
	}
	answer := strings.ToUpper(strings.TrimSpace(resp.Content))
	// Observability trace (§11.2): one line per classifier call so operators
	// can confirm which model fired, verify elapsed ms, and audit the params
	// hash without reading the full scorecard JSON.
	fmt.Fprintf(os.Stderr,
		"classifier: model=%s elapsed=%dms paramsHash=%.8s answer=%s\n",
		"qwen2.5:3b", resp.ElapsedMs, paramsHash, answer,
	)
	return classifierRawResult{
		Answer:               answer,
		ElapsedMs:            resp.ElapsedMs,
		PromptRef:            promptPath,
		ContentHash:          contentHash,
		PromptHash:           promptHash,
		ClassifierParamsHash: paramsHash,
	}
}

// interpretClassifier converts a classifierRawResult into a verdict Result
// with attached ClassifierMeta for twin-field population (B5).
//
//   - YES → ScoreCorrect
//   - NO  → ScoreIncorrect
//   - call error → ScoreError with the error message as Reason
//   - any other answer → ScoreError with the raw answer in Reason (parse failure)
func interpretClassifier(cr classifierRawResult, cm *scenario.ClassifierMatchSpec) Result {
	meta := &ClassifierMeta{
		ElapsedMs:            cr.ElapsedMs,
		PromptRef:            cr.PromptRef,
		ContentHash:          cr.ContentHash,
		PromptHash:           cr.PromptHash,
		ClassifierParamsHash: cr.ClassifierParamsHash,
	}
	if cr.Err != nil {
		meta.Score = ScoreError
		meta.Reason = fmt.Sprintf("classifier error (%s): %v", cm.Claim, cr.Err)
		return Result{Score: ScoreError, Reason: meta.Reason, Classifier: meta}
	}
	switch cr.Answer {
	case "YES":
		meta.Score = ScoreCorrect
		meta.Reason = fmt.Sprintf("classifier YES: %s", cm.Claim)
		return Result{Score: ScoreCorrect, Reason: meta.Reason, Classifier: meta}
	case "NO":
		meta.Score = ScoreIncorrect
		meta.Reason = fmt.Sprintf("classifier NO: %s", cm.Claim)
		return Result{Score: ScoreIncorrect, Reason: meta.Reason, Classifier: meta}
	default:
		meta.Score = ScoreError
		meta.Reason = fmt.Sprintf("classifier parse error (%s): unexpected answer %q (want YES or NO)", cm.Claim, cr.Answer)
		return Result{Score: ScoreError, Reason: meta.Reason, Classifier: meta}
	}
}
