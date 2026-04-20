package knowledge

import (
	"testing"
)

func TestParseDrillDown_ValidJSON(t *testing.T) {
	result := parseDrillDown(`{"drillDown": [0, 3, 7]}`)
	if len(result) != 3 {
		t.Fatalf("expected 3 indices, got %d", len(result))
	}
	if result[0] != 0 || result[1] != 3 || result[2] != 7 {
		t.Errorf("unexpected indices: %v", result)
	}
}

func TestParseDrillDown_EmbeddedJSON(t *testing.T) {
	response := `Looking at the context, I need more detail on these areas.
{"drillDown": [1, 4]}
Let me check those agents.`

	result := parseDrillDown(response)
	if len(result) != 2 {
		t.Fatalf("expected 2 indices, got %d", len(result))
	}
	if result[0] != 1 || result[1] != 4 {
		t.Errorf("unexpected indices: %v", result)
	}
}

func TestParseDrillDown_NotADrillDown(t *testing.T) {
	result := parseDrillDown("The main function is in cmd/server/main.go and handles HTTP setup.")
	if result != nil {
		t.Errorf("expected nil for plain answer, got %v", result)
	}
}

func TestParseDrillDown_EmptyArrayReturnsNil(t *testing.T) {
	result := parseDrillDown(`{"drillDown": []}`)
	if result != nil {
		t.Errorf("expected nil for empty drillDown, got %v", result)
	}
}

func TestParseAnswer_WithSources(t *testing.T) {
	response := `The handler processes HTTP requests using net/http.

Sources:
- pkg/server/handler.go
- pkg/server/routes.go`

	manifest := Manifest{}
	result := parseAnswer(response, manifest)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Answer != response {
		t.Error("expected full response as answer")
	}
	if len(result.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(result.Sources))
	}
}

func TestParseAnswer_NoSources(t *testing.T) {
	response := "The system uses PostgreSQL for storage."
	manifest := Manifest{}
	result := parseAnswer(response, manifest)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(result.Sources))
	}
}
