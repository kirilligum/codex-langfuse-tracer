package codextrace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kirilligum/codex-langfuse-tracer/internal/agenttrace"
)

type goalSnapshot struct {
	ThreadID  string  `json:"threadId"`
	CreatedAt float64 `json:"createdAt"`
	Objective string  `json:"objective"`
	Status    string  `json:"status"`
}

func (g goalSnapshot) key() string { return fmt.Sprintf("goal:%s:%.0f", g.ThreadID, g.CreatedAt) }

type requestExecution struct {
	id, requestID, trigger              string
	completed, aborted, afterCompletion bool
	finalOutput                         string
}

type goalCall struct {
	executionID string
	create      bool
}

type requestCorrelation struct {
	sessionID, currentExecution, goalRequestID, pendingGoalControl string
	goal                                                           *goalSnapshot
	requests                                                       map[string]*agenttrace.Request
	executions                                                     map[string]*requestExecution
	order                                                          []string
	callOwners                                                     map[string]string
	goalCalls                                                      map[string]goalCall
	inputIDs                                                       map[string]map[string]bool
	include                                                        func(string) bool
}

func newRequestCorrelation(include func(string) bool) *requestCorrelation {
	return &requestCorrelation{requests: map[string]*agenttrace.Request{}, executions: map[string]*requestExecution{},
		callOwners: map[string]string{}, goalCalls: map[string]goalCall{}, inputIDs: map[string]map[string]bool{}, include: include}
}

func (c *requestCorrelation) retain(r *agenttrace.Request) bool {
	return c.include == nil || c.include(r.TraceID)
}

func (c *requestCorrelation) request(id, timestamp string) *agenttrace.Request {
	if r := c.requests[id]; r != nil {
		return r
	}
	r := &agenttrace.Request{ID: id, TraceID: agenttrace.StableTraceID("codex-request-v1", c.sessionID, id), StartTS: timestamp, EndTS: timestamp, State: "running"}
	c.requests[id] = r
	c.order = append(c.order, id)
	return r
}

func advanceRequestTime(r *agenttrace.Request, timestamp string) {
	previous, e1 := time.Parse(time.RFC3339Nano, r.EndTS)
	next, e2 := time.Parse(time.RFC3339Nano, timestamp)
	if timestamp != "" && (e1 != nil || e2 != nil || !next.Before(previous)) {
		r.EndTS = timestamp
	}
}

func (c *requestCorrelation) refresh(r *agenttrace.Request) {
	if r.FinishedAt != "" {
		return
	}
	var last *requestExecution
	if len(r.ExecutionIDs) > 0 {
		last = c.executions[r.ExecutionIDs[len(r.ExecutionIDs)-1]]
	}
	if r.GoalKey != "" {
		switch r.GoalStatus {
		case "complete":
			if last == nil || last.completed {
				r.State = "completed"
			} else {
				r.State = "running"
			}
		case "cancelled":
			r.State = "cancelled"
		case "active", "":
			r.State = "running"
		default:
			r.State = "waiting"
		}
	} else if r.State != "unattributed" && last != nil {
		if last.completed {
			r.State = "completed"
			if last.aborted {
				r.State = "cancelled"
			}
		} else {
			r.State = "running"
		}
	}
	if r.State == "completed" && last != nil {
		r.Output = last.finalOutput
	}
	if r.State == "completed" || r.State == "cancelled" || (r.State == "unattributed" && last != nil && last.completed) {
		r.FinishedAt = r.EndTS
	}
}

func (c *requestCorrelation) addInput(r *agenttrace.Request, id, text, timestamp string) {
	if c.inputIDs[r.ID] == nil {
		c.inputIDs[r.ID] = map[string]bool{}
	}
	if c.inputIDs[r.ID][id] {
		return
	}
	c.inputIDs[r.ID][id] = true
	if r.FinishedAt != "" {
		// The root's accepted checkpoint must not hide later source messages.
		// Each late input uses the existing work-item checkpoint path, while its
		// exported child still belongs to the immutable original request root.
		if c.include != nil && !c.include(lateInputWorkID(r, id)) {
			return
		}
		r.LateInputs = append(r.LateInputs, agenttrace.HumanInput{ID: id, Text: text, Timestamp: timestamp})
		return
	}
	r.HasHumanInput = true
	if r.State == "unattributed" {
		r.State = "running"
	}
	if !c.retain(r) {
		return
	}
	r.Inputs = append(r.Inputs, agenttrace.HumanInput{ID: id, Text: text, Timestamp: timestamp})
}

func lateInputWorkID(r *agenttrace.Request, messageID string) string {
	return agenttrace.StableTraceID("codex-request-input-v1", r.TraceID, messageID)
}

func (c *requestCorrelation) updateGoal(g *goalSnapshot, timestamp string) {
	previous := c.goal
	changed := g != nil && (previous == nil || previous.key() != g.key())
	if changed {
		c.goalRequestID = ""
		if e := c.executions[c.currentExecution]; c.pendingGoalControl == "" && e != nil && !e.completed {
			c.goalRequestID = e.requestID
		}
	}
	c.goal = g
	if c.pendingGoalControl != "" && g != nil {
		id := c.goalRequestID
		if id == "" {
			id = g.key()
		}
		if r := c.requests[id]; r != nil && r.FinishedAt != "" {
			id = g.key() + ":resume:" + c.pendingGoalControl
		}
		c.goalRequestID = id
		r := c.request(id, c.pendingGoalControl)
		r.GoalKey = g.key()
		r.GoalStatus = g.Status
		if !r.HasHumanInput || previous == nil || previous.Objective != g.Objective {
			c.addInput(r, fmt.Sprintf("%s:input:%d", g.key(), len(c.inputIDs[r.ID])), g.Objective, c.pendingGoalControl)
		}
		c.pendingGoalControl = ""
		advanceRequestTime(r, timestamp)
		c.refresh(r)
		return
	}
	if r := c.requests[c.goalRequestID]; r != nil {
		if g != nil {
			r.GoalKey = g.key()
			r.GoalStatus = g.Status
		} else {
			r.GoalStatus = "cancelled"
		}
		advanceRequestTime(r, timestamp)
		c.refresh(r)
	}
}

var goalInvocation = regexp.MustCompile(`\btools\.(create_goal|get_goal|update_goal)\s*\(`)

func goalFromValue(value any) *goalSnapshot {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var g goalSnapshot
	if json.Unmarshal(raw, &g) != nil || g.ThreadID == "" || g.CreatedAt == 0 || g.Status == "" {
		return nil
	}
	return &g
}

func goalFromToolResult(output any) *goalSnapshot {
	parts := agenttrace.SliceValue(output)
	if text, ok := output.(string); ok {
		parts = []any{map[string]any{"text": text}}
	}
	for _, part := range parts {
		text := agenttrace.StringValue(agenttrace.MapValue(part)["text"])
		var result map[string]any
		if json.Unmarshal([]byte(text), &result) != nil {
			continue
		}
		if _, ok := result["remainingTokens"]; !ok {
			continue
		}
		if _, ok := result["completionBudgetReport"]; !ok {
			continue
		}
		if g := goalFromValue(result["goal"]); g != nil {
			return g
		}
	}
	return nil
}

// observe consumes attribution even for executions whose model/tool bodies are
// filtered out. A later continuation still needs its human request identity.
func (c *requestCorrelation) observe(kind string, p map[string]any, timestamp, sessionID string) string {
	c.sessionID = sessionID
	typ := agenttrace.StringValue(p["type"])
	if kind == "event_msg" && typ == "task_started" {
		a := agenttrace.MapValue(p["turn_attribution"])
		trigger := agenttrace.StringValue(a["turn_trigger"])
		id := agenttrace.StringValue(p["turn_id"])
		if trigger == "" || id == "" {
			return ""
		}
		c.currentExecution = id
		if _, ok := c.executions[id]; ok {
			return id
		}
		root := agenttrace.StringOr(a["root_turn_id"], agenttrace.StringOr(p["root_turn_id"], id))
		parent := c.executions[agenttrace.StringOr(a["parent_turn_id"], root)]
		requestID := "turn:" + root
		if trigger == "user" {
			requestID = "turn:" + id
		} else if trigger == "goal" && c.goal != nil {
			requestID = c.goalRequestID
			if requestID == "" {
				requestID = c.goal.key()
			}
			c.goalRequestID = requestID
		} else if parent != nil {
			requestID = parent.requestID
		}
		r := c.request(requestID, timestamp)
		if trigger == "goal" && c.goal != nil {
			r.GoalKey = c.goal.key()
			r.GoalStatus = c.goal.Status
		} else if trigger != "user" && parent == nil {
			r.State = "unattributed"
		}
		c.executions[id] = &requestExecution{id: id, requestID: requestID, trigger: trigger, afterCompletion: r.FinishedAt != ""}
		r.ExecutionIDs = append(r.ExecutionIDs, id)
		advanceRequestTime(r, timestamp)
		c.refresh(r)
		return id
	}
	if kind == "event_msg" && typ == "thread_goal_updated" {
		g := goalFromValue(p["goal"])
		if g != nil && g.ThreadID != c.sessionID {
			return ""
		}
		if g != nil || p["goal"] == nil {
			c.updateGoal(g, timestamp)
		}
		return ""
	}
	meta := agenttrace.MapValue(p["internal_chat_message_metadata_passthrough"])
	if kind == "response_item" && typ == "message" && agenttrace.StringValue(p["role"]) == "user" {
		for _, v := range agenttrace.SliceValue(meta["content_item_kinds"]) {
			if v == "user.goal" {
				c.pendingGoalControl = timestamp
				return ""
			}
		}
	}
	id := agenttrace.StringOr(p["turn_id"], agenttrace.StringOr(meta["turn_id"], c.currentExecution))
	callID := agenttrace.StringValue(p["call_id"])
	if c.callOwners[callID] != "" {
		id = c.callOwners[callID]
	}
	e := c.executions[id]
	if e == nil {
		return ""
	}
	r := c.requests[e.requestID]
	advanceRequestTime(r, timestamp)
	if kind == "response_item" {
		switch typ {
		case "message":
			if agenttrace.StringValue(p["role"]) == "user" {
				var input agenttrace.Turn
				recordUserMessage(&input, p)
				if input.InputOrigin == "human" {
					c.addInput(r, agenttrace.StringOr(p["id"], id+":"+timestamp), input.InputText(), timestamp)
				}
			} else if agenttrace.StringValue(p["role"]) == "assistant" && agenttrace.StringValue(p["phase"]) == "final_answer" && c.retain(r) {
				e.finalOutput = textFromContent(p["content"], "output_text")
			}
		case "function_call", "custom_tool_call", "tool_search_call":
			if callID != "" {
				c.callOwners[callID] = id
			}
			name := strings.TrimPrefix(agenttrace.StringValue(p["name"]), "functions.")
			code := agenttrace.StringValue(p["input"])
			if name == "create_goal" || name == "get_goal" || name == "update_goal" || (name == "exec" && goalInvocation.MatchString(code)) {
				c.goalCalls[callID] = goalCall{id, name == "create_goal" || strings.Contains(code, "tools.create_goal")}
			}
		case "function_call_output", "custom_tool_call_output":
			if call, ok := c.goalCalls[callID]; ok {
				if g := goalFromToolResult(p["output"]); g != nil && g.ThreadID == c.sessionID && (call.create || (c.goal != nil && g.key() == c.goal.key())) {
					current := c.currentExecution
					c.currentExecution = call.executionID
					c.updateGoal(g, timestamp)
					c.currentExecution = current
				}
				delete(c.goalCalls, callID)
			}
			delete(c.callOwners, callID)
		}
	}
	if kind == "event_msg" && (typ == "task_complete" || typ == "turn_aborted") {
		e.completed = true
		e.aborted = typ == "turn_aborted"
	}
	c.refresh(r)
	return id
}

func (c *requestCorrelation) project(turns []agenttrace.Turn, sessionModel, sessionCWD string) []agenttrace.Turn {
	var roots []agenttrace.Turn
	pending := map[string]int{}
	for _, owner := range c.callOwners {
		pending[owner]++
	}
	for i := range turns {
		if e := c.executions[turns[i].TurnID]; e != nil {
			turns[i].Request = c.requests[e.requestID]
			turns[i].ExecutionTrigger = e.trigger
			turns[i].AfterCompletion = e.afterCompletion
			turns[i].Aborted = e.aborted
			turns[i].PendingToolCount = pending[e.id]
		}
	}
	for _, id := range c.order {
		r := c.requests[id]
		for _, input := range r.LateInputs {
			inputRequest := *r
			inputRequest.Inputs = []agenttrace.HumanInput{input}
			inputRequest.LateInputs = nil
			roots = append(roots, agenttrace.Turn{Provider: agenttrace.ProviderCodex, SessionID: c.sessionID,
				TurnID: input.ID, TraceID: lateInputWorkID(r, input.ID), StartTS: input.Timestamp, EndTS: input.Timestamp,
				CWD: sessionCWD, Model: sessionModel, Request: &inputRequest, RequestRoot: true,
				RequestInputOnly: true, InputOrigin: "human", Completed: true})
		}
		if !c.retain(r) {
			continue
		}
		root := agenttrace.Turn{Provider: agenttrace.ProviderCodex, SessionID: c.sessionID, TurnID: r.ID, TraceID: r.TraceID, StartTS: r.StartTS, EndTS: r.EndTS, CWD: sessionCWD, Model: sessionModel, Request: r, RequestRoot: true, InputOrigin: "human", Completed: r.FinishedAt != ""}
		if r.FinishedAt != "" {
			root.EndTS = r.FinishedAt
		}
		for _, input := range r.Inputs {
			root.UserMessages = append(root.UserMessages, input.Text)
		}
		if r.Output != "" {
			root.AssistantTexts = []string{r.Output}
		}
		roots = append(roots, root)
	}
	// Emit human input before executions. Root completion can precede the child
	// batches in transport; explicit parent IDs make arrival order irrelevant.
	return append(roots, turns...)
}
