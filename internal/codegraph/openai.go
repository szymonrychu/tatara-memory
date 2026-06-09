package codegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// CommunityLabeler names a community from its member entity names.
type CommunityLabeler interface {
	Label(ctx context.Context, memberNames []string) (string, error)
}

// OpenAILabeler is a minimal chat/completions client used to name communities.
// Gated on OPENAI_API_KEY; NewOpenAILabelerFromEnv returns nil when unset, in
// which case callers fall back to the top-degree member name.
type OpenAILabeler struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewOpenAILabelerFromEnv builds a labeler from OPENAI_API_KEY / SEMANTIC_MODEL
// / OPENAI_BASE_URL. Returns nil (no error) when OPENAI_API_KEY is unset.
func NewOpenAILabelerFromEnv() *OpenAILabeler {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil
	}
	model := os.Getenv("SEMANTIC_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAILabeler{
		apiKey:  key,
		model:   model,
		baseURL: base,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

type chatReq struct {
	Model    string    `json:"model"`
	Messages []chatMsg `json:"messages"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
}

// Label returns a short community label from member names.
func (l *OpenAILabeler) Label(ctx context.Context, memberNames []string) (string, error) {
	prompt := fmt.Sprintf("Name this code module in 2-4 words from these symbol names: %v. Reply with only the label.", memberNames)
	body, err := json.Marshal(chatReq{
		Model:    l.model,
		Messages: []chatMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	resp, err := l.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai label: status %d", resp.StatusCode)
	}
	var cr chatResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("openai label: empty choices")
	}
	return cr.Choices[0].Message.Content, nil
}
