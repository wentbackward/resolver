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

// judgeCallTimeout is the per-call hard deadline for the judge
// adapter. §11.1 pre-mortem scenario 1: a 5 s fail-fast prevents a single
// slow call from hanging the sweep indefinitely. Callers can still set a
// tighter deadline via the parent context.
const judgeCallTimeout = 5 * time.Second

// judgeCtx bundles the judge dependencies threaded through
// matchOne / matchAny. A nil judgeCtx means no judge is available
// (--no-judge path or judge not yet injected); matchOne skips
// Judge matchers silently in that case.
type judgeCtx struct {
	cl      adapter.Adapter
	ctx     context.Context
	dataDir string
}

// promptPath resolves promptRef relative to the context's dataDir. If dataDir
// is empty, the path is resolved relative to the current working directory.
func (cc *judgeCtx) promptPath(promptRef string) string {
	if filepath.IsAbs(promptRef) {
		return promptRef
	}
	if cc.dataDir == "" {
		return promptRef
	}
	return filepath.Join(cc.dataDir, promptRef)
}

// JudgeMeta carries the rich judge verdict attached to a Result when
// a Judge matcher fires. Used by the runner (B5) to populate
// PerQuery twin-fields and JudgeInputSnapshot.
type JudgeMeta struct {
	Score     Score
	Reason    string
	ElapsedMs int64
	PromptRef string

	// Snapshot fields for bit-reproducible replay (OD-1 residual / B5).
	ContentHash          string // sha256 of the judge's input (MUT content)
	PromptHash           string // sha256 of the prompt file at call time
	JudgeParamsHash string // sha256 of canonical JSON {max_tokens,seed,temperature,top_p}
}

// judgeRawResult is the raw output from one judge invocation before
// interpretation. Carries the snapshot hashes computed during the call.
type judgeRawResult struct {
	Answer    string // "YES", "NO", or garbled output
	ElapsedMs int64
	Err       error

	// Snapshot fields (populated even on error for audit purposes).
	PromptRef            string
	ContentHash          string
	PromptHash           string
	JudgeParamsHash string
}

// judgeParams is the fixed set of sampling parameters passed to the
// judge adapter on every call. Canonicalised JSON of this struct is
// sha256'd to form JudgeParamsHash in the snapshot.
type judgeParams struct {
	MaxTokens   int     `json:"max_tokens"`
	Seed        *int    `json:"seed"`        // nil = not pinned
	Temperature float64 `json:"temperature"`
	TopP        *float64 `json:"top_p"`      // nil = not pinned
}

// fixedClassifierParams is the canonical parameter set for every judge
// call. Temperature=0 enforced per spec Principle 3.
var fixedClassifierParams = judgeParams{
	MaxTokens:   16,
	Temperature: 0,
}

// hashClassifierParams returns the sha256 hex digest of the canonical JSON
// encoding of the fixed judge params. Cached after first computation.
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

// callJudge invokes the judge adapter with the prompt template at
// promptPath, substituting content for the {{output}} placeholder.
//
// Applies judgeCallTimeout per call (§11.1 pre-mortem scenario 1). The
// parent ctx from judgeCtx may impose an outer deadline. All snapshot
// hashes are computed before the call so they are available even on error.
func callJudge(cc *judgeCtx, promptPath, content string) judgeRawResult {
	contentHash := sha256Hex([]byte(content))
	paramsHash := hashClassifierParams()

	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return judgeRawResult{
			PromptRef:            promptPath,
			ContentHash:          contentHash,
			JudgeParamsHash: paramsHash,
			Err:                  fmt.Errorf("load prompt %q: %w", promptPath, err),
		}
	}
	promptHash := sha256Hex(promptBytes)
	prompt := strings.ReplaceAll(string(promptBytes), "{{output}}", content)

	callCtx, cancel := context.WithTimeout(cc.ctx, judgeCallTimeout)
	defer cancel()

	resp, err := cc.cl.Chat(callCtx, adapter.ChatRequest{
		Model:       "qwen2.5:3b",
		Messages:    []adapter.Message{{Role: "user", Content: prompt}},
		Temperature: fixedClassifierParams.Temperature,
		MaxTokens:   fixedClassifierParams.MaxTokens,
		Timeout:     judgeCallTimeout,
	})
	if err != nil {
		return judgeRawResult{
			PromptRef:            promptPath,
			ContentHash:          contentHash,
			PromptHash:           promptHash,
			JudgeParamsHash: paramsHash,
			Err:                  fmt.Errorf("judge call: %w", err),
		}
	}
	answer := strings.ToUpper(strings.TrimSpace(resp.Content))
	// Observability trace (§11.2): one line per judge call so operators
	// can confirm which model fired, verify elapsed ms, and audit the params
	// hash without reading the full scorecard JSON.
	fmt.Fprintf(os.Stderr,
		"judge: model=%s elapsed=%dms paramsHash=%.8s answer=%s\n",
		"qwen2.5:3b", resp.ElapsedMs, paramsHash, answer,
	)
	return judgeRawResult{
		Answer:               answer,
		ElapsedMs:            resp.ElapsedMs,
		PromptRef:            promptPath,
		ContentHash:          contentHash,
		PromptHash:           promptHash,
		JudgeParamsHash: paramsHash,
	}
}

// interpretJudge converts a judgeRawResult into a verdict Result
// with attached JudgeMeta for twin-field population (B5).
//
//   - YES → ScoreCorrect
//   - NO  → ScoreIncorrect
//   - call error → ScoreError with the error message as Reason
//   - any other answer → ScoreError with the raw answer in Reason (parse failure)
func interpretJudge(cr judgeRawResult, cm *scenario.JudgeSpec) Result {
	meta := &JudgeMeta{
		ElapsedMs:            cr.ElapsedMs,
		PromptRef:            cr.PromptRef,
		ContentHash:          cr.ContentHash,
		PromptHash:           cr.PromptHash,
		JudgeParamsHash: cr.JudgeParamsHash,
	}
	if cr.Err != nil {
		meta.Score = ScoreError
		meta.Reason = fmt.Sprintf("judge error (%s): %v", cm.Claim, cr.Err)
		return Result{Score: ScoreError, Reason: meta.Reason, Judge: meta}
	}
	switch cr.Answer {
	case "YES":
		meta.Score = ScoreCorrect
		meta.Reason = fmt.Sprintf("judge YES: %s", cm.Claim)
		return Result{Score: ScoreCorrect, Reason: meta.Reason, Judge: meta}
	case "NO":
		meta.Score = ScoreIncorrect
		meta.Reason = fmt.Sprintf("judge NO: %s", cm.Claim)
		return Result{Score: ScoreIncorrect, Reason: meta.Reason, Judge: meta}
	default:
		meta.Score = ScoreError
		meta.Reason = fmt.Sprintf("judge parse error (%s): unexpected answer %q (want YES or NO)", cm.Claim, cr.Answer)
		return Result{Score: ScoreError, Reason: meta.Reason, Judge: meta}
	}
}
