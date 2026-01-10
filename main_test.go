package main

import (
	"os"
	"strings"
	"testing"
)

// Helper to create a viewer from lines
func newTestViewer(lines []string) *Viewer {
	return &Viewer{
		lines:    lines,
		loading:  false,
		filename: "test.log",
		width:    80,
		height:   24,
	}
}

// ==================== Viewer Basic Tests ====================

func TestViewerLineCount(t *testing.T) {
	v := newTestViewer([]string{"line1", "line2", "line3"})
	if v.LineCount() != 3 {
		t.Errorf("Expected 3 lines, got %d", v.LineCount())
	}
}

func TestViewerGetLine(t *testing.T) {
	v := newTestViewer([]string{"first", "second", "third"})

	if v.GetLine(0) != "first" {
		t.Errorf("Expected 'first', got '%s'", v.GetLine(0))
	}
	if v.GetLine(1) != "second" {
		t.Errorf("Expected 'second', got '%s'", v.GetLine(1))
	}
	if v.GetLine(-1) != "" {
		t.Errorf("Expected empty string for negative index, got '%s'", v.GetLine(-1))
	}
	if v.GetLine(100) != "" {
		t.Errorf("Expected empty string for out of bounds, got '%s'", v.GetLine(100))
	}
}

func TestViewerGetLines(t *testing.T) {
	original := []string{"a", "b", "c"}
	v := newTestViewer(original)
	lines := v.GetLines()

	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}

	// Modify returned slice should not affect original
	lines[0] = "modified"
	if v.GetLine(0) != "a" {
		t.Error("GetLines should return a copy, not the original slice")
	}
}

// ==================== Navigation Tests ====================

func TestNavigateDown(t *testing.T) {
	v := newTestViewer([]string{"1", "2", "3", "4", "5"})
	v.topLine = 0

	v.navigateDown()
	if v.topLine != 1 {
		t.Errorf("Expected topLine 1, got %d", v.topLine)
	}

	// Navigate to end
	v.topLine = 4
	v.navigateDown()
	if v.topLine != 4 {
		t.Error("Should not navigate past last line")
	}
}

func TestNavigateUp(t *testing.T) {
	v := newTestViewer([]string{"1", "2", "3", "4", "5"})
	v.topLine = 2

	v.navigateUp()
	if v.topLine != 1 {
		t.Errorf("Expected topLine 1, got %d", v.topLine)
	}

	// Navigate to start
	v.topLine = 0
	v.navigateUp()
	if v.topLine != 0 {
		t.Error("Should not navigate before first line")
	}
}

func TestPageDown(t *testing.T) {
	v := newTestViewer(make([]string, 100))
	v.height = 20
	v.topLine = 0

	v.pageDown()
	if v.topLine != 20 {
		t.Errorf("Expected topLine 20, got %d", v.topLine)
	}
}

func TestPageUp(t *testing.T) {
	v := newTestViewer(make([]string, 100))
	v.height = 20
	v.topLine = 50

	v.pageUp()
	if v.topLine != 30 {
		t.Errorf("Expected topLine 30, got %d", v.topLine)
	}
}

func TestGoToStartEnd(t *testing.T) {
	v := newTestViewer(make([]string, 100))
	v.topLine = 50

	v.goToStart()
	if v.topLine != 0 {
		t.Errorf("Expected topLine 0, got %d", v.topLine)
	}

	v.goToEnd()
	if v.topLine != 99 {
		t.Errorf("Expected topLine 99, got %d", v.topLine)
	}
}

// ==================== Search Tests ====================

func TestSearchForward(t *testing.T) {
	lines := []string{"apple", "banana", "cherry", "apple pie", "date"}
	s := &SearchState{}

	idx := s.Search(lines, "apple", 0, false, false, false)
	if idx != 0 {
		t.Errorf("Expected first match at 0, got %d", idx)
	}
	if len(s.matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(s.matches))
	}
}

func TestSearchBackward(t *testing.T) {
	lines := []string{"apple", "banana", "cherry", "apple pie", "date"}
	s := &SearchState{}

	idx := s.Search(lines, "apple", 4, true, false, false)
	if idx != 3 {
		t.Errorf("Expected match at 3, got %d", idx)
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	lines := []string{"Apple", "BANANA", "cherry"}
	s := &SearchState{}

	idx := s.Search(lines, "apple", 0, false, false, true)
	if idx != 0 {
		t.Errorf("Expected match at 0, got %d", idx)
	}

	idx = s.Search(lines, "banana", 0, false, false, true)
	if idx != 1 {
		t.Errorf("Expected match at 1, got %d", idx)
	}
}

func TestSearchRegex(t *testing.T) {
	lines := []string{"error: 123", "warning: 456", "error: 789"}
	s := &SearchState{}

	idx := s.Search(lines, "error: \\d+", 0, false, true, false)
	if idx != 0 {
		t.Errorf("Expected match at 0, got %d", idx)
	}
	if len(s.matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(s.matches))
	}
}

func TestSearchNoMatch(t *testing.T) {
	lines := []string{"apple", "banana", "cherry"}
	s := &SearchState{}

	idx := s.Search(lines, "orange", 0, false, false, false)
	if idx != -1 {
		t.Errorf("Expected -1 for no match, got %d", idx)
	}
}

func TestSearchNextPrev(t *testing.T) {
	lines := []string{"a", "b", "a", "c", "a"}
	s := &SearchState{}
	s.Search(lines, "a", 0, false, false, false)

	// Should be at first match (0)
	next := s.Next()
	if next != 2 {
		t.Errorf("Expected next match at 2, got %d", next)
	}

	next = s.Next()
	if next != 4 {
		t.Errorf("Expected next match at 4, got %d", next)
	}

	prev := s.Prev()
	if prev != 2 {
		t.Errorf("Expected prev match at 2, got %d", prev)
	}
}

// ==================== Filter Tests ====================

func TestFilterLinesKeep(t *testing.T) {
	lines := []string{"error: failed", "info: success", "error: timeout", "debug: trace"}

	filtered := filterLinesSlice(lines, "error", true)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered lines, got %d", len(filtered))
	}
	if filtered[0] != "error: failed" || filtered[1] != "error: timeout" {
		t.Error("Filtered lines don't match expected")
	}
}

func TestFilterLinesExclude(t *testing.T) {
	lines := []string{"error: failed", "info: success", "error: timeout", "debug: trace"}

	filtered := filterLinesSlice(lines, "error", false)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered lines, got %d", len(filtered))
	}
	if filtered[0] != "info: success" || filtered[1] != "debug: trace" {
		t.Error("Filtered lines don't match expected")
	}
}

func TestFilterLinesNoMatch(t *testing.T) {
	lines := []string{"apple", "banana", "cherry"}

	filtered := filterLinesSlice(lines, "orange", true)
	if len(filtered) != 0 {
		t.Errorf("Expected 0 filtered lines, got %d", len(filtered))
	}
}

// ==================== ViewerStack Tests ====================

func TestViewerStackPushPop(t *testing.T) {
	v1 := newTestViewer([]string{"original"})
	stack := NewViewerStack(v1)

	if stack.Current() != v1 {
		t.Error("Current should return initial viewer")
	}

	v2 := newTestViewer([]string{"filtered"})
	stack.Push(v2)

	if stack.Current() != v2 {
		t.Error("Current should return pushed viewer")
	}

	stack.Pop()
	if stack.Current() != v1 {
		t.Error("After pop, current should be original viewer")
	}

	// Pop on single viewer should fail
	if stack.Pop() {
		t.Error("Pop should return false when only one viewer remains")
	}
}

func TestViewerStackReset(t *testing.T) {
	v1 := newTestViewer([]string{"original"})
	stack := NewViewerStack(v1)

	stack.Push(newTestViewer([]string{"filter1"}))
	stack.Push(newTestViewer([]string{"filter2"}))
	stack.Push(newTestViewer([]string{"filter3"}))

	stack.Reset()
	if stack.Current() != v1 {
		t.Error("After reset, current should be original viewer")
	}
}

// ==================== JSON Tests ====================

func TestIsJSON(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{`{"key": "value"}`, true},
		{`[1, 2, 3]`, true},
		{`prefix: {"key": "value"}`, true},
		{`no json here`, false},
		{``, false},
		{`{`, false}, // Too short (len < 2)
		{`{}`, true},
	}

	for _, tt := range tests {
		result := isJSON(tt.line)
		if result != tt.expected {
			t.Errorf("isJSON(%q) = %v, expected %v", tt.line, result, tt.expected)
		}
	}
}

func TestFindJSONStart(t *testing.T) {
	tests := []struct {
		line     string
		expected int
	}{
		{`{"key": "value"}`, 0},
		{`prefix: {"key": "value"}`, 8},
		{`[1, 2, 3]`, 0},
		{`no json`, -1},
	}

	for _, tt := range tests {
		result := findJSONStart(tt.line)
		if result != tt.expected {
			t.Errorf("findJSONStart(%q) = %d, expected %d", tt.line, result, tt.expected)
		}
	}
}

func TestFormatJSON(t *testing.T) {
	// Valid JSON
	lines := formatJSON(`{"a": 1, "b": 2}`)
	if len(lines) < 2 {
		t.Error("Expected formatted JSON to have multiple lines")
	}

	// With prefix
	lines = formatJSON(`log: {"a": 1}`)
	if len(lines) < 2 {
		t.Error("Expected formatted JSON with prefix to have multiple lines")
	}
	if lines[0] != "log: " {
		t.Errorf("Expected prefix 'log: ', got '%s'", lines[0])
	}

	// Invalid JSON - should return original
	lines = formatJSON(`not json`)
	if len(lines) != 1 || lines[0] != "not json" {
		t.Error("Invalid JSON should return original line")
	}
}

func TestPythonToJSON(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`{'key': 'value'}`, `{"key": "value"}`},
		{`{'enabled': True}`, `{"enabled": true}`},
		{`{'enabled': False}`, `{"enabled": false}`},
		{`{'value': None}`, `{"value": null}`},
	}

	for _, tt := range tests {
		result := pythonToJSON(tt.input)
		// Remove any whitespace differences
		result = strings.ReplaceAll(result, " ", "")
		expected := strings.ReplaceAll(tt.expected, " ", "")
		if result != expected {
			t.Errorf("pythonToJSON(%q) = %q, expected %q", tt.input, result, expected)
		}
	}
}

// ==================== ANSI Tests ====================

func TestParseANSI(t *testing.T) {
	// Plain text
	cells := parseANSI("hello")
	if len(cells) != 5 {
		t.Errorf("Expected 5 cells, got %d", len(cells))
	}

	// With ANSI codes
	cells = parseANSI("\x1b[31mred\x1b[0m")
	if len(cells) != 3 {
		t.Errorf("Expected 3 cells (red), got %d", len(cells))
	}
}

func TestStripANSIForJSON(t *testing.T) {
	input := "prefix \x1b[31mred\x1b[0m text"
	result := stripANSIForJSON(input)
	expected := "prefix red text"
	if result != expected {
		t.Errorf("stripANSIForJSON failed: got %q, expected %q", result, expected)
	}
}

// ==================== History Tests ====================

func TestHistory(t *testing.T) {
	h := &History{
		entries:  []string{},
		index:    -1,
		filename: "/tmp/test_history",
	}

	// Add entries
	h.Add("first")
	h.Add("second")
	h.Add("third")

	if len(h.entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(h.entries))
	}

	// Navigate up
	h.Reset()
	result := h.Up("")
	if result != "third" {
		t.Errorf("Expected 'third', got '%s'", result)
	}

	result = h.Up("")
	if result != "second" {
		t.Errorf("Expected 'second', got '%s'", result)
	}

	// Navigate down
	result = h.Down("")
	if result != "third" {
		t.Errorf("Expected 'third', got '%s'", result)
	}

	// Don't add duplicates in a row
	h.Add("third")
	if len(h.entries) != 3 {
		t.Errorf("Should not add duplicate, got %d entries", len(h.entries))
	}
}

// ==================== Word Wrap Tests ====================

func TestGetExpandedLineCountNoWrap(t *testing.T) {
	v := newTestViewer([]string{"short line", "another line"})
	v.wordWrap = false
	v.jsonPretty = false

	count := v.getExpandedLineCount(0)
	if count != 1 {
		t.Errorf("Without wrap, expected 1, got %d", count)
	}
}

func TestGetExpandedLineCountWithWrap(t *testing.T) {
	v := newTestViewer([]string{strings.Repeat("x", 100)})
	v.width = 20
	v.wordWrap = true
	v.jsonPretty = false

	count := v.getExpandedLineCount(0)
	if count != 5 {
		t.Errorf("With wrap, expected 5 rows (100/20), got %d", count)
	}
}

func TestGetExpandedLineCountWithJSON(t *testing.T) {
	v := newTestViewer([]string{`{"a": 1, "b": 2, "c": 3}`})
	v.width = 80
	v.wordWrap = false
	v.jsonPretty = true

	count := v.getExpandedLineCount(0)
	if count <= 1 {
		t.Errorf("With JSON, expected multiple rows, got %d", count)
	}
}

// ==================== Export Tests ====================

func TestExport(t *testing.T) {
	tmpFile := "/tmp/test_export.txt"
	defer os.Remove(tmpFile)

	lines := []string{"line1", "line2", "line3"}
	content := strings.Join(lines, "\n")

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Read back
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	if string(data) != content {
		t.Error("Exported content doesn't match")
	}
}

// ==================== Navigation with Offset Tests ====================

func TestNavigateWithOffset(t *testing.T) {
	v := newTestViewer([]string{strings.Repeat("x", 100), "short"})
	v.width = 20
	v.wordWrap = true
	v.topLine = 0
	v.topLineOffset = 0

	// First line should expand to 5 rows (100 chars / 20 width)
	expandedCount := v.getExpandedLineCount(0)
	if expandedCount != 5 {
		t.Errorf("Expected 5 expanded rows, got %d", expandedCount)
	}

	// Navigate down should increment offset
	v.navigateDown()
	if v.topLineOffset != 1 {
		t.Errorf("Expected offset 1, got %d", v.topLineOffset)
	}

	// Navigate through all rows of first line
	for v.topLine == 0 && v.topLineOffset < expandedCount-1 {
		v.navigateDown()
	}

	// One more should go to next line
	v.navigateDown()
	if v.topLine != 1 {
		t.Errorf("Expected to move to line 1, got line %d", v.topLine)
	}
	if v.topLineOffset != 0 {
		t.Errorf("Expected offset 0 on new line, got %d", v.topLineOffset)
	}
}

// ==================== Benchmark Tests ====================

func BenchmarkSearchLiteral(b *testing.B) {
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = "This is a test line with some content"
	}
	s := &SearchState{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Search(lines, "test", 0, false, false, false)
	}
}

func BenchmarkSearchRegex(b *testing.B) {
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = "This is a test line with some content"
	}
	s := &SearchState{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Search(lines, "test.*content", 0, false, true, false)
	}
}

func BenchmarkFilterLines(b *testing.B) {
	lines := make([]string, 10000)
	for i := range lines {
		if i%10 == 0 {
			lines[i] = "error: something went wrong"
		} else {
			lines[i] = "info: all good"
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filterLinesSlice(lines, "error", true)
	}
}

func BenchmarkParseANSI(b *testing.B) {
	line := "Normal text \x1b[31mred text\x1b[0m more normal \x1b[32mgreen\x1b[0m end"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseANSI(line)
	}
}

func BenchmarkFormatJSON(b *testing.B) {
	line := `{"key1": "value1", "key2": 123, "key3": true, "nested": {"a": 1, "b": 2}}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatJSON(line)
	}
}
