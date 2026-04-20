package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// createTestFile creates a file at root/path with the given content, creating directories as needed.
func createTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mockClient implements CompletionClient for testing.
type mockClient struct {
	responses []string
	callIndex int
	calls     []mockCall
}

type mockCall struct {
	Model        string
	SystemPrompt string
	UserPrompt   string
}

func (m *mockClient) Generate(_ context.Context, model, systemPrompt, userPrompt string) (string, error) {
	m.calls = append(m.calls, mockCall{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	})
	if m.callIndex >= len(m.responses) {
		return "mock summary", nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

type errorClient struct{ err error }

func (e *errorClient) Generate(context.Context, string, string, string) (string, error) {
	return "", e.err
}
