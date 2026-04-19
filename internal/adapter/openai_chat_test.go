package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDecodeArgumentsStringOrObject exercises the spec §2 tolerance:
// `arguments` may be a JSON string or an object.
func TestDecodeArgumentsStringOrObject(t *testing.T) {
	cases := map[string]struct {
		raw   string
		want  map[string]any
		wantRaw string
	}{
		"object": {
			raw:  `{"node":"spark-01","command":"docker ps"}`,
			want: map[string]any{"node": "spark-01", "command": "docker ps"},
		},
		"string-wrapped object": {
			raw:  `"{\"node\":\"spark-01\",\"command\":\"docker ps\"}"`,
			want: map[string]any{"node": "spark-01", "command": "docker ps"},
		},
		"plain string (unparseable)": {
			raw:    `"not-json-at-all"`,
			want:   map[string]any{},
			wantRaw: "not-json-at-all",
		},
		"null": {
			raw:  `null`,
			want: map[string]any{},
		},
		"empty string": {
			raw:  `""`,
			want: map[string]any{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, raw, err := decodeArguments(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("arg[%s]: got %v want %v", k, got[k], v)
				}
			}
			if tc.wantRaw != "" && raw != tc.wantRaw {
				t.Errorf("raw: got %q want %q", raw, tc.wantRaw)
			}
		})
	}
}

// TestOpenAIChatEndToEnd spins up a fake server that returns a canned
// OpenAI-style response, verifies the adapter parses it correctly.
func TestOpenAIChatEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Spot-check that temperature + tool_choice made it through.
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["tool_choice"] != "auto" {
			t.Errorf("tool_choice: got %v", req["tool_choice"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":null,
			  "tool_calls":[{"id":"x","type":"function","function":{"name":"exec","arguments":"{\"node\":\"spark-01\",\"command\":\"docker ps\"}"}}]
			}}],
			"usage": {"prompt_tokens": 40, "completion_tokens": 12, "total_tokens": 52}
		}`))
	}))
	defer server.Close()

	ad := NewOpenAIChat(server.URL)
	resp, err := ad.Chat(context.Background(), ChatRequest{
		Model:       "test",
		Messages:    []Message{{Role: "user", Content: "test"}},
		Temperature: 0,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments["node"] != "spark-01" {
		t.Errorf("arg parse failed: %+v", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.PromptTokens != 40 {
		t.Errorf("usage not captured: %+v", resp.Usage)
	}
}

// TestMarshalMessagesToolCallArgsAsString confirms we double-encode
// tool_calls[*].function.arguments when echoing assistant turns back — spec
// requires a string, not an object.
func TestMarshalMessagesToolCallArgsAsString(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "1", Name: "exec", Arguments: map[string]any{"node": "spark-01", "command": "docker ps"}},
		}},
	}
	out := marshalMessages(msgs)
	if len(out) != 1 || len(out[0].ToolCalls) != 1 {
		t.Fatal("expected one message with one tool call")
	}
	raw := string(out[0].ToolCalls[0].Function.Arguments)
	// Must be JSON-encoded string (starts and ends with quote), NOT a raw object.
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		t.Fatalf("arguments should be JSON-encoded string; got %s", raw)
	}
	// The unquoted content should itself be valid JSON.
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("cannot unquote: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("inner JSON invalid: %v (inner: %s)", err, s)
	}
	if obj["node"] != "spark-01" {
		t.Errorf("inner object missing node: %+v", obj)
	}
}
