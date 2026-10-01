package appnz

import (
	"context"
	"encoding/json"
	"fmt"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool is one capability the model may call. Parameters is a JSON Schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Run         func(ctx context.Context, args json.RawMessage) (any, error)
}

type ToolRun struct {
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type Agent struct {
	Client   *Client
	Model    string
	System   string
	Tools    []Tool
	MaxSteps int
}

type RunResult struct {
	Reply string    `json:"reply"`
	Tools []ToolRun `json:"tools"`
	Steps int       `json:"steps"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []toolSpec     `json:"tools,omitempty"`
	Extra    map[string]any `json:"-"`
}

type toolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// Run is the whole agent loop: call the model, run whatever tools it asks for,
// feed the results back, stop when it answers in plain text.
func (a *Agent) Run(ctx context.Context, token string, history []Message) (*RunResult, error) {
	steps := a.MaxSteps
	if steps <= 0 {
		steps = 6
	}
	msgs := append([]Message{{Role: "system", Content: a.System}}, history...)
	req := chatRequest{Model: a.Model}
	byName := map[string]Tool{}
	for _, t := range a.Tools {
		var spec toolSpec
		spec.Type = "function"
		spec.Function.Name, spec.Function.Description, spec.Function.Parameters = t.Name, t.Description, t.Parameters
		req.Tools = append(req.Tools, spec)
		byName[t.Name] = t
	}
	res := &RunResult{}
	for res.Steps < steps {
		res.Steps++
		req.Messages = msgs
		var out struct {
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
		}
		body, _ := json.Marshal(req)
		if err := a.Client.do(ctx, "POST", "/v1/chat/completions", token, body, &out); err != nil {
			return res, err
		}
		if len(out.Choices) == 0 {
			return res, fmt.Errorf("model returned no choices")
		}
		msg := out.Choices[0].Message
		msg.Role = "assistant"
		msgs = append(msgs, msg)
		if len(msg.ToolCalls) == 0 {
			res.Reply = msg.Content
			return res, nil
		}
		for _, call := range msg.ToolCalls {
			run := ToolRun{Name: call.Function.Name, Args: json.RawMessage(call.Function.Arguments)}
			content := ""
			if tool, ok := byName[call.Function.Name]; !ok {
				run.Error = "unknown tool"
			} else if result, err := tool.Run(ctx, run.Args); err != nil {
				run.Error = err.Error()
			} else {
				run.Result = result
			}
			if run.Error != "" {
				content = `{"error":` + quote(run.Error) + `}`
			} else {
				b, _ := json.Marshal(run.Result)
				content = string(b)
			}
			res.Tools = append(res.Tools, run)
			msgs = append(msgs, Message{Role: "tool", ToolCallID: call.ID, Content: content})
		}
	}
	return res, fmt.Errorf("agent did not finish in %d steps", steps)
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
