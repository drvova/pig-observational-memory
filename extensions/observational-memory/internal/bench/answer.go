package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// ActorInstructions is the system prompt of Mastra's LongMemEval answering
// agent (explorations/longmemeval/src/commands/run.ts), verbatim.
const ActorInstructions = "You are a helpful assistant with access to extensive conversation history. \n" +
	"When answering questions, carefully review the conversation history to identify and use any relevant user preferences, interests, or specific details they have mentioned."

// TailHeading introduces the unobserved conversation tail after the summary.
const TailHeading = "Conversation since these memories were recorded (not yet condensed into observations):"

// UserPrompt is the question in LongMemEval's (and Mastra's DateInjector's)
// format: "Current Date: {date}\nQuestion: {question}".
func UserPrompt(q Question) string {
	return "Current Date: " + q.QuestionDate + "\nQuestion: " + q.Question
}

// recallParagraphs are the paragraphs of the summary's usage instructions, one
// per format, that point the agent at the memory tool.
var recallParagraphs = func() []string {
	paragraphs := []string{"\n\n" + ledger.TablesRecallParagraph}
	if i := strings.Index(ledger.ContextUsageInstructions, "When exact source context is needed"); i >= 0 {
		paragraphs = append(paragraphs, "\n\n"+ledger.ContextUsageInstructions[i:])
	}
	return paragraphs
}()

// MemoryMessage is the system message carrying the extension's memory: the
// rendered summary, then the raw tail Pi would still hold in context. Without
// the memory tool, the summary's paragraph about it is left out, since the
// actor could not follow it.
func MemoryMessage(m Memory, withRecall bool) string {
	parts := []string{}
	if summary := m.Summary; strings.TrimSpace(summary) != "" {
		if !withRecall {
			for _, paragraph := range recallParagraphs {
				summary = strings.Replace(summary, paragraph, "", 1)
			}
		}
		parts = append(parts, summary)
	}
	if strings.TrimSpace(m.Tail) != "" {
		parts = append(parts, TailHeading+"\n\n"+m.Tail)
	}
	return strings.Join(parts, "\n\n")
}

// RecallPrompt is what Pi adds to the system prompt for the memory tool: its
// snippet and guidelines.
func RecallPrompt() string {
	lines := []string{memquery.PromptSnippet}
	for _, guideline := range memquery.PromptGuidelines {
		lines = append(lines, "- "+guideline)
	}
	return strings.Join(lines, "\n")
}

// OMMessages are the actor's messages in observational-memory mode, shaped
// like Mastra's: the agent instructions, the memory system message (where
// Mastra's OM processor injects its observations), and the dated question.
// withRecall adds the memory tool's system prompt lines, as Pi does when the
// tool is registered.
func OMMessages(q Question, m Memory, withRecall bool) []ChatMessage {
	instructions := ActorInstructions
	if withRecall {
		instructions += "\n\n" + RecallPrompt()
	}
	messages := []ChatMessage{{Role: "system", Content: Text(instructions)}}
	if memory := MemoryMessage(m, withRecall); memory != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: Text(memory)})
	}
	return append(messages, ChatMessage{Role: "user", Content: Text(UserPrompt(q))})
}

// FullContextTemplate is LongMemEval's long-context reading prompt
// (src/generation/run_generation.py, no chain-of-thought).
const FullContextTemplate = "I will give you several history chats between you and a user. Please answer the question based on the relevant chat history.\n\n\nHistory Chats:\n\n%s\n\nCurrent Date: %s\nQuestion: %s\nAnswer:"

// FullContextPrompt renders the haystack in LongMemEval's JSON history format,
// keeping the newest sessions that fit maxTokens (estimated). It reports how
// many sessions were dropped from the front.
func FullContextPrompt(q Question, maxTokens int) (string, int, error) {
	sessions, err := q.ChronologicalSessions()
	if err != nil {
		return "", 0, err
	}
	blocks := make([]string, len(sessions))
	for i, s := range sessions {
		encoded, err := json.Marshal(s.Turns)
		if err != nil {
			return "", 0, err
		}
		blocks[i] = string(encoded)
	}
	render := func(from int) string {
		var b strings.Builder
		for i := from; i < len(sessions); i++ {
			fmt.Fprintf(&b, "\n### Session %d:\nSession Date: %s\nSession Content:\n\n%s\n", i-from+1, q.HaystackDates[sessions[i].Index], blocks[i])
		}
		return fmt.Sprintf(FullContextTemplate, b.String(), q.QuestionDate, q.Question)
	}
	from := 0
	prompt := render(from)
	for maxTokens > 0 && EstimateTokens(prompt) > maxTokens && from < len(sessions)-1 {
		from++
		prompt = render(from)
	}
	return prompt, from, nil
}

// Answer is the actor's reply.
type Answer struct {
	Hypothesis   string `json:"hypothesis"`
	FinishReason string `json:"finishReason"`
	PromptTokens int    `json:"promptTokens"`
	// DroppedSessions counts sessions the full-context prompt left out.
	DroppedSessions int `json:"droppedSessions,omitempty"`
	// Recalls counts memory tool calls.
	Recalls int `json:"recalls,omitempty"`
}

// AnswerOptions configure the actor call.
type AnswerOptions struct {
	Model     string
	MaxTokens int
	// ReasoningEffort is sent as reasoning_effort; "" leaves the provider default.
	ReasoningEffort string
}

var zero = 0.0

// EmptyReplyAttempts bounds the calls made for one reply when a reasoning
// model ends with reasoning only and no content.
const EmptyReplyAttempts = 3

// chatWithContent repeats a call whose reply has no content.
func chatWithContent(ctx context.Context, client *Client, role string, req ChatRequest) (ChatResponse, error) {
	var resp ChatResponse
	var err error
	for range EmptyReplyAttempts {
		resp, err = client.Chat(ctx, role, req)
		if err != nil || strings.TrimSpace(resp.Choices[0].Message.Text()) != "" {
			return resp, err
		}
	}
	return resp, nil
}

// RecallTool is the extension's memory tool as a chat-completions function.
func RecallTool() ToolSpec {
	return ToolSpec{Type: "function", Function: FunctionSpec{Name: memquery.ToolName, Description: memquery.Description(nil), Parameters: memquery.Parameters}}
}

// MaxRecallTurns bounds the actor's memory tool loop.
const MaxRecallTurns = 8

// AskWithRecall is Ask with the extension's memory tool, run against the
// ingested branch as the extension's tool runs against the session, without a
// curated store (saved and writes report curated memory off). After
// MaxRecallTurns tool turns the last call is made without tools.
func AskWithRecall(ctx context.Context, client *Client, messages []ChatMessage, entries []session.Entry, opts AnswerOptions) (Answer, int, error) {
	messages = append([]ChatMessage{}, messages...)
	recalls := 0
	for turn := 0; ; turn++ {
		req := ChatRequest{Model: opts.Model, Messages: messages, MaxTokens: opts.MaxTokens, Temperature: &zero, ReasoningEffort: opts.ReasoningEffort}
		if turn < MaxRecallTurns {
			req.Tools = []ToolSpec{RecallTool()}
		}
		resp, err := client.Chat(ctx, RoleActor, req)
		if err != nil {
			return Answer{}, recalls, err
		}
		choice := resp.Choices[0]
		if len(choice.Message.ToolCalls) == 0 || req.Tools == nil {
			if strings.TrimSpace(choice.Message.Text()) == "" && turn < MaxRecallTurns+EmptyReplyAttempts {
				continue
			}
			return Answer{Hypothesis: strings.TrimSpace(choice.Message.Text()), FinishReason: choice.FinishReason, PromptTokens: resp.Usage.PromptTokens}, recalls, nil
		}
		messages = append(messages, choice.Message)
		for _, call := range choice.Message.ToolCalls {
			var args struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
			text := "Tool " + call.Function.Name + " not found"
			if call.Function.Name == memquery.ToolName {
				text = memquery.Run(memquery.Env{Entries: entries}, args.Query).Text
				recalls++
			}
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: call.ID, Content: Text(text)})
		}
	}
}

// Ask sends the actor messages at temperature 0, as Mastra does.
func Ask(ctx context.Context, client *Client, messages []ChatMessage, opts AnswerOptions) (Answer, error) {
	resp, err := chatWithContent(ctx, client, RoleActor, ChatRequest{Model: opts.Model, Messages: messages, MaxTokens: opts.MaxTokens, Temperature: &zero, ReasoningEffort: opts.ReasoningEffort})
	if err != nil {
		return Answer{}, err
	}
	choice := resp.Choices[0]
	return Answer{Hypothesis: strings.TrimSpace(choice.Message.Text()), FinishReason: choice.FinishReason, PromptTokens: resp.Usage.PromptTokens}, nil
}
