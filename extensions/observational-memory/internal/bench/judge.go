package bench

import (
	"context"
	"fmt"
	"strings"
)

// The judge prompts of LongMemEval's src/evaluation/evaluate_qa.py
// (get_anscheck_prompt), verbatim, including their spacing.
const (
	judgeDefault         = "I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. \n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only."
	judgeTemporal        = "I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. In addition, do not penalize off-by-one errors for the number of days. If the question asks for the number of days/weeks/months, etc., and the model makes off-by-one errors (e.g., predicting 19 days when the answer is 18), the model's response is still correct. \n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only."
	judgeKnowledgeUpdate = "I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response contains some previous information along with an updated answer, the response should be considered as correct as long as the updated answer is the required answer.\n\nQuestion: %s\n\nCorrect Answer: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only."
	judgePreference      = "I will give you a question, a rubric for desired personalized response, and a response from a model. Please answer yes if the response satisfies the desired response. Otherwise, answer no. The model does not need to reflect all the points in the rubric. The response is correct as long as it recalls and utilizes the user's personal information correctly.\n\nQuestion: %s\n\nRubric: %s\n\nModel Response: %s\n\nIs the model response correct? Answer yes or no only."
	judgeAbstention      = "I will give you an unanswerable question, an explanation, and a response from a model. Please answer yes if the model correctly identifies the question as unanswerable. The model could say that the information is incomplete, or some other information is given but the asked information is not.\n\nQuestion: %s\n\nExplanation: %s\n\nModel Response: %s\n\nDoes the model correctly identify the question as unanswerable? Answer yes or no only."
)

// JudgePrompt is the LongMemEval grading prompt for a question and response.
// Abstention questions use the abstention prompt whatever their type.
func JudgePrompt(q Question, response string) (string, error) {
	template := ""
	switch {
	case q.Abstention():
		template = judgeAbstention
	case q.Type == "single-session-user", q.Type == "single-session-assistant", q.Type == "multi-session":
		template = judgeDefault
	case q.Type == "temporal-reasoning":
		template = judgeTemporal
	case q.Type == "knowledge-update":
		template = judgeKnowledgeUpdate
	case q.Type == "single-session-preference":
		template = judgePreference
	default:
		return "", fmt.Errorf("unknown question type %q", q.Type)
	}
	return fmt.Sprintf(template, q.Question, q.Answer(), response), nil
}

// ParseVerdict applies evaluate_qa.py's rule: the label is true when the
// judge's reply contains "yes", case-insensitively.
func ParseVerdict(reply string) bool {
	return strings.Contains(strings.ToLower(reply), "yes")
}

// StrictVerdict is Mastra's stricter rule: the trimmed reply is "yes" or
// starts with "yes.". It is reported alongside, not used for scoring.
func StrictVerdict(reply string) bool {
	r := strings.ToLower(strings.TrimSpace(reply))
	return r == "yes" || strings.HasPrefix(r, "yes.")
}

// Verdict is the judge's grade.
type Verdict struct {
	Correct bool   `json:"correct"`
	Strict  bool   `json:"strictCorrect"`
	Reply   string `json:"reply"`
}

// Judge grades a response with a single user message at temperature 0 and
// no system prompt, as evaluate_qa.py does. maxTokens is far above its 10
// because a reasoning model spends tokens before the verdict. effort is sent as
// reasoning_effort; "" leaves the provider default.
func Judge(ctx context.Context, client *Client, model string, maxTokens int, effort string, q Question, response string) (Verdict, error) {
	prompt, err := JudgePrompt(q, response)
	if err != nil {
		return Verdict{}, err
	}
	resp, err := chatWithContent(ctx, client, RoleJudge, ChatRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: Text(prompt)}}, MaxTokens: maxTokens, Temperature: &zero, ReasoningEffort: effort})
	if err != nil {
		return Verdict{}, err
	}
	reply := strings.TrimSpace(resp.Choices[0].Message.Text())
	if reply == "" {
		return Verdict{}, fmt.Errorf("judge returned no content (finish_reason %q)", resp.Choices[0].FinishReason)
	}
	return Verdict{Correct: ParseVerdict(reply), Strict: StrictVerdict(reply), Reply: reply}, nil
}
