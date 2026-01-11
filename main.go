package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nsf/termbox-go"
)

// ansiCell represents a character with its color attributes
type ansiCell struct {
	char rune
	fg   termbox.Attribute
	bg   termbox.Attribute
}

// parseANSI parses a line with ANSI escape codes and returns cells with colors
func parseANSI(line string) []ansiCell {
	// Fast path: check for escape character using byte scan (faster than strings.Contains)
	hasEscape := false
	for i := 0; i < len(line); i++ {
		if line[i] == 0x1b {
			hasEscape = true
			break
		}
	}

	if !hasEscape {
		runes := []rune(line)
		cells := make([]ansiCell, len(runes))
		for i, r := range runes {
			cells[i] = ansiCell{r, termbox.ColorDefault, termbox.ColorDefault}
		}
		return cells
	}

	// Slow path: parse ANSI escape sequences
	var cells []ansiCell
	fg := termbox.ColorDefault
	bg := termbox.ColorDefault

	i := 0
	runes := []rune(line)
	for i < len(runes) {
		// Check for escape sequence
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			// Find end of escape sequence
			end := i + 2
			for end < len(runes) && runes[end] != 'm' {
				end++
			}
			if end < len(runes) {
				// Parse the escape sequence
				seq := string(runes[i+2 : end])
				fg, bg = applyANSICodes(seq, fg, bg)
				i = end + 1
				continue
			}
		}
		cells = append(cells, ansiCell{runes[i], fg, bg})
		i++
	}
	return cells
}

// applyANSICodes applies ANSI codes and returns updated colors
func applyANSICodes(seq string, fg, bg termbox.Attribute) (termbox.Attribute, termbox.Attribute) {
	if seq == "" || seq == "0" {
		return termbox.ColorDefault, termbox.ColorDefault
	}

	parts := strings.Split(seq, ";")
	i := 0
	for i < len(parts) {
		code, err := strconv.Atoi(parts[i])
		if err != nil {
			i++
			continue
		}

		switch {
		case code == 0:
			fg, bg = termbox.ColorDefault, termbox.ColorDefault
		case code == 1:
			fg |= termbox.AttrBold
		case code == 4:
			fg |= termbox.AttrUnderline
		case code == 7:
			fg |= termbox.AttrReverse
		case code >= 30 && code <= 37:
			fg = termbox.Attribute(code-30+1) | (fg & 0xFF00)
		case code == 39:
			fg = termbox.ColorDefault | (fg & 0xFF00)
		case code >= 40 && code <= 47:
			bg = termbox.Attribute(code - 40 + 1)
		case code == 49:
			bg = termbox.ColorDefault
		case code >= 90 && code <= 97:
			fg = termbox.Attribute(code-90+9) | (fg & 0xFF00)
		case code >= 100 && code <= 107:
			bg = termbox.Attribute(code - 100 + 9)
		case code == 38 && i+2 < len(parts):
			// 256 color foreground: 38;5;N
			if parts[i+1] == "5" {
				if n, err := strconv.Atoi(parts[i+2]); err == nil {
					fg = termbox.Attribute(n+1) | (fg & 0xFF00)
				}
				i += 2
			}
		case code == 48 && i+2 < len(parts):
			// 256 color background: 48;5;N
			if parts[i+1] == "5" {
				if n, err := strconv.Atoi(parts[i+2]); err == nil {
					bg = termbox.Attribute(n + 1)
				}
				i += 2
			}
		}
		i++
	}
	return fg, bg
}

// findJSONStart finds the start index of embedded JSON in a line
// Returns -1 if no JSON found
func findJSONStart(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] == '{' || line[i] == '[' {
			return i
		}
	}
	return -1
}

// findJSONEnd finds the matching closing brace/bracket starting from jsonStart
// Returns the index of the closing character, or -1 if not found
func findJSONEnd(line string, jsonStart int) int {
	if jsonStart < 0 || jsonStart >= len(line) {
		return -1
	}

	openChar := line[jsonStart]
	var closeChar byte
	if openChar == '{' {
		closeChar = '}'
	} else if openChar == '[' {
		closeChar = ']'
	} else {
		return -1
	}

	depth := 0
	inString := false
	escaped := false

	for i := jsonStart; i < len(line); i++ {
		c := line[i]

		if escaped {
			escaped = false
			continue
		}

		if c == '\\' && inString {
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if c == openChar {
			depth++
		} else if c == closeChar {
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// lineHasANSI quickly checks if a line contains ANSI escape codes
func lineHasANSI(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return true
		}
	}
	return false
}

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we find the end of the escape sequence
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // Skip the final letter
			}
			i = j
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// stripANSIForJSON removes ANSI escape codes from a string for JSON parsing
func stripANSIForJSON(s string) string {
	return stripANSI(s)
}

// pythonToJSON converts Python dict syntax to JSON
func pythonToJSON(s string) string {
	// First strip ANSI escape codes
	result := stripANSIForJSON(s)
	
	// Replace Python booleans and None
	// Replace True/False/None that are not part of larger words
	// This is a simple heuristic - replace when followed by comma, }, ], or whitespace
	replacements := []struct{ old, new string }{
		{"True,", "true,"},
		{"True}", "true}"},
		{"True]", "true]"},
		{"True ", "true "},
		{"False,", "false,"},
		{"False}", "false}"},
		{"False]", "false]"},
		{"False ", "false "},
		{"None,", "null,"},
		{"None}", "null}"},
		{"None]", "null]"},
		{"None ", "null "},
	}
	for _, r := range replacements {
		result = strings.ReplaceAll(result, r.old, r.new)
	}
	// Replace single quotes with double quotes (simple approach)
	result = strings.ReplaceAll(result, "'", "\"")
	return result
}

// formatJSON attempts to pretty-print JSON/Python dict in a line
func formatJSON(line string) []string {
	jsonStart := findJSONStart(line)
	if jsonStart == -1 {
		return []string{line}
	}

	jsonEnd := findJSONEnd(line, jsonStart)
	if jsonEnd == -1 {
		// No matching close, try the whole rest of line
		jsonEnd = len(line) - 1
	}

	prefix := line[:jsonStart]
	jsonPart := line[jsonStart : jsonEnd+1]
	suffix := ""
	if jsonEnd+1 < len(line) {
		suffix = line[jsonEnd+1:]
	}

	// Try as-is first (valid JSON)
	var out bytes.Buffer
	err := json.Indent(&out, []byte(jsonPart), "", "  ")
	if err != nil {
		// Try converting from Python dict syntax
		converted := pythonToJSON(jsonPart)
		out.Reset()
		err = json.Indent(&out, []byte(converted), "", "  ")
		if err != nil {
			// Still not valid, return original
			return []string{line}
		}
	}

	// Build result: prefix on first line, then indented JSON, then suffix
	formatted := out.String()
	jsonLines := strings.Split(formatted, "\n")

	result := make([]string, 0, len(jsonLines)+1)
	if prefix != "" {
		result = append(result, prefix)
	}
	for i, jl := range jsonLines {
		if i == len(jsonLines)-1 && suffix != "" {
			result = append(result, jl+suffix)
		} else {
			result = append(result, jl)
		}
	}

	return result
}

// isJSON checks if a line contains JSON
func isJSON(line string) bool {
	jsonStart := findJSONStart(line)
	if jsonStart == -1 {
		return false
	}
	// Check if we can find a matching closing bracket
	jsonEnd := findJSONEnd(line, jsonStart)
	return jsonEnd != -1
}

type Viewer struct {
	lines            []string     // All lines from the file
	hasANSI          []bool       // True if corresponding line has ANSI escape codes
	originIndices    []int        // Maps each line to its index in parent viewer (for filtered views)
	mu               sync.RWMutex // Protects lines during background loading
	loading          bool         // True while file is still loading
	filename         string       // Original filename (empty for filtered views)
	wordWrap         bool         // Word wrap mode
	jsonPretty       bool         // JSON pretty-print mode
	stickyLeft       int          // Number of chars to keep visible on left when scrolling (0 = disabled)
	topLine          int          // Index of the line at the top of the screen
	topLineOffset    int          // Offset within expanded line (for wrap/JSON mode)
	leftCol          int          // Horizontal scroll offset
	width            int          // Terminal width
	height           int          // Terminal height
	expandedCache    map[int]int  // Cache of expanded line counts (lineIdx -> rowCount)
	expandedCacheKey string       // Key to invalidate cache (mode+width)
	follow           bool         // Follow mode (like tail -f)
}

// ViewerStack manages a stack of viewers for filtering navigation
type ViewerStack struct {
	viewers []*Viewer
}

// App holds the application state
type App struct {
	stack           *ViewerStack
	search          *SearchState
	history         *History // Shared history for filters and searches
	statusMessage   string
	messageExpiry   time.Time
	visualMode      bool   // True when in visual selection mode
	visualStart     int    // Starting line of visual selection
	visualCursor    int    // Current cursor line in visual mode
	timestampFormat string // Python-style datetime format for timestamp search
}

// History manages persistent command history (for filters and searches)
type History struct {
	entries   []string
	index     int    // Current position when navigating (-1 = new input)
	tempInput string // Store user input when navigating history
	filename  string // File to persist history
}

const maxHistoryEntries = 100

func NewHistory(filename string) *History {
	h := &History{
		entries:  []string{},
		index:    -1,
		filename: filename,
	}
	h.load()
	return h
}

func (h *History) load() {
	data, err := os.ReadFile(h.filename)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line != "" {
			h.entries = append(h.entries, line)
		}
	}
}

func (h *History) save() {
	// Keep only the last maxHistoryEntries
	start := 0
	if len(h.entries) > maxHistoryEntries {
		start = len(h.entries) - maxHistoryEntries
	}
	data := strings.Join(h.entries[start:], "\n")
	os.WriteFile(h.filename, []byte(data), 0644)
}

// encodeHistoryEntry encodes query with modifiers as "RI|query" where R=r/-, I=i/-
func encodeHistoryEntry(query string, isRegex, ignoreCase bool) string {
	r := "-"
	if isRegex {
		r = "r"
	}
	i := "-"
	if ignoreCase {
		i = "i"
	}
	return r + i + "|" + query
}

// decodeHistoryEntry decodes "RI|query" into query, isRegex, ignoreCase
func decodeHistoryEntry(entry string) (string, bool, bool) {
	if len(entry) >= 3 && entry[2] == '|' {
		isRegex := entry[0] == 'r'
		ignoreCase := entry[1] == 'i'
		return entry[3:], isRegex, ignoreCase
	}
	// Legacy entry without modifiers
	return entry, false, false
}

func (h *History) Add(entry string) {
	if entry == "" {
		return
	}
	// Don't add duplicates in a row
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		return
	}
	h.entries = append(h.entries, entry)
	h.save()
}

// AddWithModifiers adds entry with regex and ignoreCase flags encoded
func (h *History) AddWithModifiers(query string, isRegex, ignoreCase bool) {
	if query == "" {
		return
	}
	entry := encodeHistoryEntry(query, isRegex, ignoreCase)
	// Don't add duplicates in a row
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		return
	}
	h.entries = append(h.entries, entry)
	h.save()
}

func (h *History) Reset() {
	h.index = -1
	h.tempInput = ""
}

func (h *History) Up(currentInput string) string {
	if len(h.entries) == 0 {
		return currentInput
	}
	if h.index == -1 {
		h.tempInput = currentInput
		h.index = len(h.entries) - 1
	} else if h.index > 0 {
		h.index--
	}
	return h.entries[h.index]
}

// UpWithModifiers returns query, isRegex, ignoreCase from history
func (h *History) UpWithModifiers(currentInput string, currentRegex, currentIgnoreCase bool) (string, bool, bool) {
	if len(h.entries) == 0 {
		return currentInput, currentRegex, currentIgnoreCase
	}
	if h.index == -1 {
		h.tempInput = encodeHistoryEntry(currentInput, currentRegex, currentIgnoreCase)
		h.index = len(h.entries) - 1
	} else if h.index > 0 {
		h.index--
	}
	return decodeHistoryEntry(h.entries[h.index])
}

func (h *History) Down(currentInput string) string {
	if h.index == -1 {
		return currentInput
	}
	h.index++
	if h.index >= len(h.entries) {
		h.index = -1
		return h.tempInput
	}
	return h.entries[h.index]
}

// DownWithModifiers returns query, isRegex, ignoreCase from history
func (h *History) DownWithModifiers(currentInput string, currentRegex, currentIgnoreCase bool) (string, bool, bool) {
	if h.index == -1 {
		return currentInput, currentRegex, currentIgnoreCase
	}
	h.index++
	if h.index >= len(h.entries) {
		h.index = -1
		return decodeHistoryEntry(h.tempInput)
	}
	return decodeHistoryEntry(h.entries[h.index])
}

// SearchState holds the current search results
type SearchState struct {
	query      string         // Current search query
	regex      *regexp.Regexp // Compiled regex pattern
	isRegex    bool           // True if regex mode is enabled
	ignoreCase bool           // True if case-insensitive search
	matches    []int          // Line indices that match
	current    int            // Current match index (-1 if none)
	backward   bool           // True if last search was backward (?)
}

// Clear resets the search state
func (s *SearchState) Clear() {
	s.query = ""
	s.regex = nil
	s.isRegex = false
	s.ignoreCase = false
	s.matches = nil
	s.current = -1
	s.backward = false
}

// HasResults returns true if there are search results
func (s *SearchState) HasResults() bool {
	return len(s.matches) > 0
}

// AtEnd returns true if at the last match
func (s *SearchState) AtEnd() bool {
	return s.current >= len(s.matches)-1
}

// AtStart returns true if at the first match
func (s *SearchState) AtStart() bool {
	return s.current <= 0
}

// Next moves to the next match, returns the line index or -1 if at end
func (s *SearchState) Next() int {
	if !s.HasResults() {
		return -1
	}
	if s.current >= len(s.matches)-1 {
		return -1 // At end, don't wrap
	}
	s.current++
	return s.matches[s.current]
}

// Prev moves to the previous match, returns the line index or -1 if at start
func (s *SearchState) Prev() int {
	if !s.HasResults() {
		return -1
	}
	if s.current <= 0 {
		return -1 // At start, don't wrap
	}
	s.current--
	return s.matches[s.current]
}

// Search performs a search starting from startLine, returns the first match line index or -1
// If backward is true, searches upward; otherwise searches downward
// hasANSI is optional cache of which lines have ANSI codes (pass nil to always strip)
func (s *SearchState) Search(lines []string, hasANSI []bool, query string, startLine int, backward bool, isRegex bool, ignoreCase bool) int {
	s.query = query
	s.isRegex = isRegex
	s.ignoreCase = ignoreCase
	s.matches = nil
	s.current = -1
	s.backward = backward
	s.regex = nil

	// Helper to get plain text (only strip if has ANSI codes)
	getPlain := func(i int, line string) string {
		if hasANSI != nil && i < len(hasANSI) && !hasANSI[i] {
			return line // No ANSI codes, use as-is
		}
		return stripANSI(line)
	}

	// Fast path: literal case-sensitive search using strings.Contains
	if !isRegex && !ignoreCase {
		for i, line := range lines {
			plainLine := getPlain(i, line)
			if strings.Contains(plainLine, query) {
				s.matches = append(s.matches, i)
			}
		}
	} else if !isRegex && ignoreCase {
		// Case-insensitive literal search
		lowerQuery := strings.ToLower(query)
		for i, line := range lines {
			plainLine := getPlain(i, line)
			if strings.Contains(strings.ToLower(plainLine), lowerQuery) {
				s.matches = append(s.matches, i)
			}
		}
	} else {
		// Regex search
		pattern := query
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			// Invalid regex, treat as literal
			re = regexp.MustCompile(regexp.QuoteMeta(query))
		}
		s.regex = re

		for i, line := range lines {
			plainLine := getPlain(i, line)
			if s.regex.MatchString(plainLine) {
				s.matches = append(s.matches, i)
			}
		}
	}

	if len(s.matches) == 0 {
		return -1
	}

	if backward {
		// Find the last match at or before startLine
		for i := len(s.matches) - 1; i >= 0; i-- {
			if s.matches[i] <= startLine {
				s.current = i
				return s.matches[i]
			}
		}
		s.current = 0
	} else {
		// Find the first match at or after startLine
		for i, lineIdx := range s.matches {
			if lineIdx >= startLine {
				s.current = i
				return s.matches[i]
			}
		}
		s.current = len(s.matches) - 1
	}

	return -1
}

func NewViewer(filename string) (*Viewer, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	v := &Viewer{
		lines:    nil,
		loading:  true,
		filename: filename,
		topLine:  0,
		leftCol:  0,
	}

	// Load file in background with batched updates for performance
	go func() {
		defer file.Close()
		loadFromReader(v, file)

		// If follow mode is enabled, keep watching for new content
		if v.follow {
			go v.followFile(filename)
		}
	}()

	return v, nil
}

// followFile watches a file for new content and appends it
func (v *Viewer) followFile(filename string) {
	for v.follow {
		time.Sleep(100 * time.Millisecond)

		file, err := os.Open(filename)
		if err != nil {
			continue
		}

		// Get current line count
		v.mu.RLock()
		currentLines := len(v.lines)
		v.mu.RUnlock()

		// Skip to where we left off
		scanner := bufio.NewScanner(file)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)

		lineNum := 0
		var newLines []string
		var newHasANSI []bool
		for scanner.Scan() {
			lineNum++
			if lineNum > currentLines {
				line := scanner.Text()
				newLines = append(newLines, line)
				newHasANSI = append(newHasANSI, lineHasANSI(line))
			}
		}
		file.Close()

		if len(newLines) > 0 {
			// Check if we're at the bottom before adding lines
			v.mu.RLock()
			atBottom := v.topLine >= len(v.lines)-v.height
			v.mu.RUnlock()

			v.mu.Lock()
			v.lines = append(v.lines, newLines...)
			v.hasANSI = append(v.hasANSI, newHasANSI...)
			if atBottom {
				// Auto-scroll to bottom
				v.topLine = len(v.lines) - v.height
				if v.topLine < 0 {
					v.topLine = 0
				}
			}
			v.mu.Unlock()

			termbox.Interrupt()
		}
	}
}

// NewViewerFromStdin creates a Viewer that reads from stdin
func NewViewerFromStdin() *Viewer {
	v := &Viewer{
		lines:    nil,
		loading:  true,
		filename: "<stdin>",
		topLine:  0,
		leftCol:  0,
	}

	// Load stdin in background
	go func() {
		loadFromReader(v, os.Stdin)
	}()

	return v
}

// loadFromReader loads lines from an io.Reader into a Viewer
func loadFromReader(v *Viewer, r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	const batchSize = 10000
	batch := make([]string, 0, batchSize)
	batchHasANSI := make([]bool, 0, batchSize)
	totalLines := 0

	for scanner.Scan() {
		line := scanner.Text()
		batch = append(batch, line)
		batchHasANSI = append(batchHasANSI, lineHasANSI(line))

		if len(batch) >= batchSize {
			v.mu.Lock()
			v.lines = append(v.lines, batch...)
			v.hasANSI = append(v.hasANSI, batchHasANSI...)
			v.mu.Unlock()
			totalLines += len(batch)
			batch = batch[:0]
			batchHasANSI = batchHasANSI[:0]

			// Only interrupt for first batch (to show content quickly) and then sparingly
			if totalLines == batchSize || totalLines%100000 == 0 {
				termbox.Interrupt()
			}
		}
	}

	// Append remaining lines
	if len(batch) > 0 {
		v.mu.Lock()
		v.lines = append(v.lines, batch...)
		v.hasANSI = append(v.hasANSI, batchHasANSI...)
		v.mu.Unlock()
	}

	v.mu.Lock()
	v.loading = false
	v.mu.Unlock()
	termbox.Interrupt()
}

// NewViewerFromLines creates a Viewer from an existing slice of lines
func NewViewerFromLines(lines []string) *Viewer {
	hasANSI := make([]bool, len(lines))
	for i, line := range lines {
		hasANSI[i] = lineHasANSI(line)
	}
	return &Viewer{
		lines:    lines,
		hasANSI:  hasANSI,
		loading:  false,
		filename: "", // empty for test viewers
		topLine:  0,
		leftCol:  0,
	}
}

// LineCount returns the number of lines (thread-safe)
func (v *Viewer) LineCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.lines)
}

// GetLine returns a line at index (thread-safe), or empty string if out of bounds
func (v *Viewer) GetLine(idx int) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if idx < 0 || idx >= len(v.lines) {
		return ""
	}
	return v.lines[idx]
}

// GetLines returns a copy of lines slice (thread-safe)
func (v *Viewer) GetLines() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make([]string, len(v.lines))
	copy(result, v.lines)
	return result
}

// GetHasANSI returns a copy of hasANSI slice (thread-safe)
func (v *Viewer) GetHasANSI() []bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make([]bool, len(v.hasANSI))
	copy(result, v.hasANSI)
	return result
}

// IsLoading returns true if still loading (thread-safe)
func (v *Viewer) IsLoading() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.loading
}

func (v *Viewer) draw() {
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	// Draw visible lines
	lineCount := v.LineCount()
	for screenY := 0; screenY < v.height; screenY++ {
		lineIndex := v.topLine + screenY

		// Check if we've run out of lines
		if lineIndex >= lineCount {
			break
		}

		line := v.GetLine(lineIndex)
		runes := []rune(line)

		// Draw each character in the line
		screenX := 0
		for i, char := range runes {
			// Skip characters before the horizontal scroll offset
			if i < v.leftCol {
				continue
			}

			// Stop if we've reached the edge of the screen
			if screenX >= v.width {
				break
			}

			termbox.SetCell(screenX, screenY, char, termbox.ColorDefault, termbox.ColorDefault)
			screenX++
		}
	}

	// Draw status bar at the bottom
	v.drawStatusBar()

	termbox.Flush()
}

func (v *Viewer) drawStatusBar() {
	v.drawStatusBarWithDepth(1, v.topLine, v.LineCount())
}

func (v *Viewer) drawStatusBarWithDepth(depth int, origLine int, origTotal int) {
	statusY := v.height
	lineCount := v.LineCount()
	loadingStr := ""
	if v.IsLoading() {
		loadingStr = " [loading...]"
	}
	modeStr := ""
	if v.follow {
		modeStr += " [follow]"
	}
	if v.wordWrap {
		modeStr += " [wrap]"
	}
	if v.jsonPretty {
		modeStr += " [json]"
	}
	if v.stickyLeft > 0 {
		modeStr += fmt.Sprintf(" [K:%d]", v.stickyLeft)
	}

	var status string
	if depth > 1 {
		// Show both current line and original line number
		status = fmt.Sprintf(" Line %d/%d | Original %d/%d | Col %d%s%s | Depth %d%s%s | q:quit ",
			v.topLine+1, lineCount, origLine+1, origTotal, v.leftCol, modeStr, loadingStr, depth, modeStr, loadingStr)
	} else {
		status = fmt.Sprintf(" Line %d/%d | Col %d%s%s | Depth %d%s%s | q:quit ",
			v.topLine+1, lineCount, v.leftCol, modeStr, loadingStr, depth, modeStr, loadingStr)
	}

	// Clear the status line first
	for i := 0; i < v.width; i++ {
		termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
	}

	// Draw left-aligned status
	for i, char := range status {
		if i >= v.width {
			break
		}
		termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
	}

	// Draw right-aligned filename
	if v.filename != "" {
		filenameDisplay := " " + v.filename + " "
		startX := v.width - len([]rune(filenameDisplay))
		if startX > len(status) { // Only if there's room
			for i, char := range filenameDisplay {
				termbox.SetCell(startX+i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
			}
		}
	}
}

// showMessage displays a message on the status bar
func (v *Viewer) showMessage(msg string) {
	statusY := v.height

	// Clear the status line first
	for i := 0; i < v.width; i++ {
		termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
	}

	for i, char := range msg {
		if i >= v.width {
			break
		}
		termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
	}
	termbox.Flush()
}

// drawVisualStatusBar draws the status bar in visual mode
func (a *App) drawVisualStatusBar(v *Viewer, status string) {
	statusY := v.height

	// Clear the status line
	for i := 0; i < v.width; i++ {
		termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
	}

	for i, char := range status {
		if i >= v.width {
			break
		}
		termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
	}
}

// getExpandedLineCount returns how many screen rows a line expands to
func (v *Viewer) getExpandedLineCount(lineIdx int) int {
	if lineIdx < 0 || lineIdx >= v.LineCount() {
		return 1
	}
	if v.width <= 0 {
		return 1 // Safety: avoid division by zero
	}

	// Build cache key based on current mode and width
	cacheKey := fmt.Sprintf("%v:%v:%d", v.wordWrap, v.jsonPretty, v.width)
	if v.expandedCacheKey != cacheKey {
		// Mode or width changed, invalidate cache
		v.expandedCache = make(map[int]int)
		v.expandedCacheKey = cacheKey
	}

	// Check cache
	if v.expandedCache != nil {
		if count, ok := v.expandedCache[lineIdx]; ok {
			return count
		}
	} else {
		v.expandedCache = make(map[int]int)
	}

	// Calculate expanded count
	line := v.GetLine(lineIdx)

	// Get expanded lines (JSON or original)
	var lines []string
	if v.jsonPretty && isJSON(line) {
		lines = formatJSON(line)
	} else {
		lines = []string{line}
	}

	var totalRows int
	if !v.wordWrap {
		totalRows = len(lines)
	} else {
		// Count wrapped rows for each line
		for _, l := range lines {
			cells := parseANSI(l)
			if len(cells) == 0 {
				totalRows++
			} else {
				totalRows += (len(cells) + v.width - 1) / v.width
			}
		}
	}

	if totalRows == 0 {
		totalRows = 1
	}

	// Store in cache
	v.expandedCache[lineIdx] = totalRows
	return totalRows
}

func (v *Viewer) navigateUp() {
	if v.wordWrap || v.jsonPretty {
		if v.topLineOffset > 0 {
			v.topLineOffset--
		} else if v.topLine > 0 {
			v.topLine--
			v.topLineOffset = v.getExpandedLineCount(v.topLine) - 1
		}
	} else {
		if v.topLine > 0 {
			v.topLine--
		}
	}
}

func (v *Viewer) navigateDown() {
	maxTop := v.LineCount() - 1
	if maxTop < 0 {
		maxTop = 0
	}

	if v.wordWrap || v.jsonPretty {
		expandedCount := v.getExpandedLineCount(v.topLine)
		if v.topLineOffset < expandedCount-1 {
			v.topLineOffset++
		} else if v.topLine < maxTop {
			v.topLine++
			v.topLineOffset = 0
		}
	} else {
		if v.topLine < maxTop {
			v.topLine++
		}
	}
}

func (v *Viewer) navigateLeft(amount int) {
	newValue := v.leftCol - amount
	if newValue < 0 {
		newValue = 0
	}
	v.leftCol = newValue
}

func (v *Viewer) navigateRight(amount int) {
	v.leftCol += amount
}

func (v *Viewer) pageDown() {
	if v.wordWrap || v.jsonPretty {
		// Move by screen height rows
		for i := 0; i < v.height; i++ {
			v.navigateDown()
		}
	} else {
		v.topLine += v.height
		// Allow scrolling until last line is at top
		maxTop := v.LineCount() - 1
		if maxTop < 0 {
			maxTop = 0
		}
		if v.topLine > maxTop {
			v.topLine = maxTop
		}
	}
}

func (v *Viewer) pageUp() {
	if v.wordWrap || v.jsonPretty {
		// Move by screen height rows
		for i := 0; i < v.height; i++ {
			v.navigateUp()
		}
	} else {
		v.topLine -= v.height
		if v.topLine < 0 {
			v.topLine = 0
		}
	}
}

func (v *Viewer) goToStart() {
	v.topLine = 0
	v.topLineOffset = 0
}

func (v *Viewer) goToEnd() {
	v.topLineOffset = 0
	// Go to last line at top
	v.topLine = v.LineCount() - 1
	if v.topLine < 0 {
		v.topLine = 0
	}
}

func (v *Viewer) resize(width, height int) {
	v.width = width
	v.height = height - 1 // Reserve one line for status bar
}

// promptForInput shows a prompt at the bottom line and collects user input
func (v *Viewer) promptForInput(prompt string) (string, bool) {
	input := ""

	for {
		statusY := v.height
		line := prompt + input

		for i := 0; i < v.width; i++ {
			termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
		}
		for i, char := range line {
			if i >= v.width {
				break
			}
			termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
		}
		cursorPos := len([]rune(line))
		if cursorPos < v.width {
			termbox.SetCursor(cursorPos, statusY)
		}
		termbox.Flush()

		ev := termbox.PollEvent()
		switch ev.Type {
		case termbox.EventKey:
			if ev.Key == termbox.KeyEnter {
				termbox.HideCursor()
				return input, true
			} else if ev.Key == termbox.KeyEsc {
				termbox.HideCursor()
				return "", false
			} else if ev.Key == termbox.KeyBackspace || ev.Key == termbox.KeyBackspace2 {
				if len(input) > 0 {
					runes := []rune(input)
					input = string(runes[:len(runes)-1])
				}
			} else if ev.Ch != 0 {
				input += string(ev.Ch)
			} else if ev.Key == termbox.KeySpace {
				input += " "
			}
		case termbox.EventResize:
			termbox.Sync()
			v.resize(ev.Width, ev.Height)
			v.draw()
		}
	}
}

// promptForSearch prompts for search input with regex (Ctrl+R), case (Ctrl+I) toggles, and history
// Returns: input string, isRegex flag, ignoreCase flag, ok
func (a *App) promptForSearch(prompt string) (string, bool, bool, bool) {
	v := a.stack.Current()
	a.history.Reset()
	input := ""
	isRegex := false
	ignoreCase := false

	for {
		// Draw the prompt line at the bottom
		statusY := v.height
		indicators := ""
		if isRegex {
			indicators += "[regex]"
		}
		if ignoreCase {
			if indicators != "" {
				indicators += " "
			}
			indicators += "[nocase]"
		}
		if indicators != "" {
			indicators += " "
		}
		line := prompt + indicators + input

		// Clear the status line first
		for i := 0; i < v.width; i++ {
			termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
		}

		// Draw the prompt and input
		for i, char := range line {
			if i >= v.width {
				break
			}
			termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
		}

		// Position cursor after input
		cursorPos := len([]rune(line))
		if cursorPos < v.width {
			termbox.SetCursor(cursorPos, statusY)
		}

		termbox.Flush()

		ev := termbox.PollEvent()
		switch ev.Type {
		case termbox.EventKey:
			if ev.Key == termbox.KeyEnter {
				termbox.HideCursor()
				if input != "" {
					a.history.AddWithModifiers(input, isRegex, ignoreCase)
				}
				return input, isRegex, ignoreCase, true
			} else if ev.Key == termbox.KeyEsc {
				termbox.HideCursor()
				return "", false, false, false
			} else if ev.Key == termbox.KeyBackspace || ev.Key == termbox.KeyBackspace2 {
				if len(input) > 0 {
					runes := []rune(input)
					input = string(runes[:len(runes)-1])
				}
			} else if ev.Key == termbox.KeyArrowUp {
				input, isRegex, ignoreCase = a.history.UpWithModifiers(input, isRegex, ignoreCase)
			} else if ev.Key == termbox.KeyArrowDown {
				input, isRegex, ignoreCase = a.history.DownWithModifiers(input, isRegex, ignoreCase)
			} else if ev.Key == termbox.KeyCtrlR {
				isRegex = !isRegex
			} else if ev.Key == termbox.KeyCtrlI {
				ignoreCase = !ignoreCase
			} else if ev.Ch != 0 {
				input += string(ev.Ch)
			} else if ev.Key == termbox.KeySpace {
				input += " "
			}
		case termbox.EventResize:
			termbox.Sync()
			v.resize(ev.Width, ev.Height)
			v.draw()
		}
	}
}

// filterLines returns lines based on query match
// If keep is true, returns lines containing query; if false, returns lines NOT containing query
// filterLinesSlice filters a slice of lines based on query match
func filterLinesSlice(lines []string, query string, keep bool) []string {
	var filtered []string
	for _, line := range lines {
		matches := strings.Contains(line, query)
		if matches == keep {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

// NewViewerStack creates a new ViewerStack with the initial viewer
func NewViewerStack(initial *Viewer) *ViewerStack {
	return &ViewerStack{
		viewers: []*Viewer{initial},
	}
}

// Current returns the current (top) viewer
func (s *ViewerStack) Current() *Viewer {
	return s.viewers[len(s.viewers)-1]
}

// Push adds a new viewer to the stack
func (s *ViewerStack) Push(v *Viewer) {
	s.viewers = append(s.viewers, v)
}

// Pop removes and returns the top viewer, returns false if only one viewer remains
func (s *ViewerStack) Pop() bool {
	if len(s.viewers) <= 1 {
		return false
	}
	s.viewers = s.viewers[:len(s.viewers)-1]
	return true
}

// Reset removes all viewers except the first one, returns false if already at first
func (s *ViewerStack) Reset() bool {
	if len(s.viewers) <= 1 {
		return false
	}
	s.viewers = s.viewers[:1]
	return true
}

// NewApp creates a new App with the given viewer
func NewApp(viewer *Viewer) *App {
	return &App{
		stack:   NewViewerStack(viewer),
		search:  &SearchState{},
		history: NewHistory("/tmp/sieve_history"),
	}
}

// promptForFilter prompts for filter input with regex (Ctrl+R), case (Ctrl+I) toggles, and history
// Returns: input string, isRegex flag, ignoreCase flag, ok
func (a *App) promptForFilter(prompt string) (string, bool, bool, bool) {
	v := a.stack.Current()
	a.history.Reset()
	input := ""
	isRegex := false
	ignoreCase := false

	for {
		statusY := v.height
		indicators := ""
		if isRegex {
			indicators += "[regex]"
		}
		if ignoreCase {
			if indicators != "" {
				indicators += " "
			}
			indicators += "[nocase]"
		}
		if indicators != "" {
			indicators += " "
		}
		line := prompt + indicators + input

		for i := 0; i < v.width; i++ {
			termbox.SetCell(i, statusY, ' ', termbox.ColorBlack, termbox.ColorWhite)
		}
		for i, char := range line {
			if i >= v.width {
				break
			}
			termbox.SetCell(i, statusY, char, termbox.ColorBlack, termbox.ColorWhite)
		}
		cursorPos := len([]rune(line))
		if cursorPos < v.width {
			termbox.SetCursor(cursorPos, statusY)
		}
		termbox.Flush()

		ev := termbox.PollEvent()
		switch ev.Type {
		case termbox.EventKey:
			if ev.Key == termbox.KeyEnter {
				termbox.HideCursor()
				if input != "" {
					a.history.AddWithModifiers(input, isRegex, ignoreCase)
				}
				return input, isRegex, ignoreCase, true
			} else if ev.Key == termbox.KeyEsc {
				termbox.HideCursor()
				return "", false, false, false
			} else if ev.Key == termbox.KeyBackspace || ev.Key == termbox.KeyBackspace2 {
				if len(input) > 0 {
					runes := []rune(input)
					input = string(runes[:len(runes)-1])
				}
			} else if ev.Key == termbox.KeyArrowUp {
				input, isRegex, ignoreCase = a.history.UpWithModifiers(input, isRegex, ignoreCase)
			} else if ev.Key == termbox.KeyArrowDown {
				input, isRegex, ignoreCase = a.history.DownWithModifiers(input, isRegex, ignoreCase)
			} else if ev.Key == termbox.KeyCtrlR {
				isRegex = !isRegex
			} else if ev.Key == termbox.KeyCtrlI {
				ignoreCase = !ignoreCase
			} else if ev.Ch != 0 {
				input += string(ev.Ch)
			} else if ev.Key == termbox.KeySpace {
				input += " "
			}
		case termbox.EventResize:
			termbox.Sync()
			v.resize(ev.Width, ev.Height)
			v.draw()
		}
	}
}

// ShowTempMessage displays a message for 3 seconds
func (a *App) ShowTempMessage(msg string) {
	a.statusMessage = msg
	a.messageExpiry = time.Now().Add(3 * time.Second)
	go func() {
		time.Sleep(3 * time.Second)
		termbox.Interrupt()
	}()
}

// copyToClipboard copies text to system clipboard
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// EnterVisualMode starts visual line selection
func (a *App) EnterVisualMode() {
	current := a.stack.Current()
	a.visualMode = true
	a.visualStart = current.topLine
	a.visualCursor = current.topLine
}

// ExitVisualMode exits visual mode without action
func (a *App) ExitVisualMode() {
	a.visualMode = false
	a.visualStart = 0
	a.visualCursor = 0
}

// VisualCursorDown moves cursor down in visual mode, scrolling if needed
func (a *App) VisualCursorDown() {
	current := a.stack.Current()
	lineCount := current.LineCount()
	
	if a.visualCursor < lineCount-1 {
		a.visualCursor++
		// Scroll if cursor goes below visible area
		if a.visualCursor >= current.topLine+current.height {
			current.topLine++
		}
	}
}

// VisualCursorUp moves cursor up in visual mode, scrolling if needed
func (a *App) VisualCursorUp() {
	current := a.stack.Current()
	
	if a.visualCursor > 0 {
		a.visualCursor--
		// Scroll if cursor goes above visible area
		if a.visualCursor < current.topLine {
			current.topLine--
		}
	}
}

// VisualPageDown moves cursor down by a page in visual mode
func (a *App) VisualPageDown() {
	current := a.stack.Current()
	lineCount := current.LineCount()
	
	a.visualCursor += current.height
	if a.visualCursor >= lineCount {
		a.visualCursor = lineCount - 1
	}
	// Scroll to keep cursor visible
	if a.visualCursor >= current.topLine+current.height {
		current.topLine = a.visualCursor - current.height + 1
		if current.topLine < 0 {
			current.topLine = 0
		}
	}
}

// VisualPageUp moves cursor up by a page in visual mode
func (a *App) VisualPageUp() {
	current := a.stack.Current()
	
	a.visualCursor -= current.height
	if a.visualCursor < 0 {
		a.visualCursor = 0
	}
	// Scroll to keep cursor visible
	if a.visualCursor < current.topLine {
		current.topLine = a.visualCursor
	}
}

// VisualGoToStart moves cursor to start of file in visual mode
func (a *App) VisualGoToStart() {
	current := a.stack.Current()
	a.visualCursor = 0
	current.topLine = 0
}

// VisualGoToEnd moves cursor to end of file in visual mode
func (a *App) VisualGoToEnd() {
	current := a.stack.Current()
	lineCount := current.LineCount()
	a.visualCursor = lineCount - 1
	// Scroll to show cursor
	if a.visualCursor >= current.topLine+current.height {
		current.topLine = a.visualCursor - current.height + 1
		if current.topLine < 0 {
			current.topLine = 0
		}
	}
}

// YankVisualSelection copies selected lines to clipboard
func (a *App) YankVisualSelection() {
	if !a.visualMode {
		return
	}

	current := a.stack.Current()
	startLine := a.visualStart
	endLine := a.visualCursor

	// Ensure start <= end
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}

	// Collect lines (strip ANSI codes for clean copy)
	var lines []string
	for i := startLine; i <= endLine; i++ {
		lines = append(lines, stripANSI(current.GetLine(i)))
	}

	text := strings.Join(lines, "\n")
	err := copyToClipboard(text)

	a.visualMode = false
	a.visualStart = 0
	a.visualCursor = 0

	if err != nil {
		a.ShowTempMessage("Clipboard error: " + err.Error())
	} else {
		count := endLine - startLine + 1
		a.ShowTempMessage(fmt.Sprintf("Yanked %d line(s)", count))
	}
}

// pythonToGoFormat converts Python datetime format to Go time format
func pythonToGoFormat(pyFormat string) string {
	replacements := []struct{ py, go_ string }{
		{"%Y", "2006"},
		{"%y", "06"},
		{"%m", "01"},
		{"%-d", "2"},  // day without zero padding
		{"%d", "02"},
		{"%H", "15"},
		{"%I", "03"},
		{"%M", "04"},
		{"%S", "05"},
		{"%f", "000000"},
		{"%p", "PM"},
		{"%z", "-0700"},
		{"%Z", "MST"},
		{"%j", "002"},
		{"%a", "Mon"},
		{"%A", "Monday"},
		{"%b", "Jan"},
		{"%B", "January"},
		{"%_d", "_2"}, // space-padded day (for syslog)
	}
	result := pyFormat
	for _, r := range replacements {
		result = strings.ReplaceAll(result, r.py, r.go_)
	}
	return result
}

// Common timestamp formats to try for auto-detection
var commonTimestampFormats = []string{
	// More specific formats first (with microseconds/milliseconds)
	"%Y-%m-%d %H:%M:%S.%f", // 2026-01-06 15:48:10.192158
	"%Y-%m-%dT%H:%M:%S.%f", // 2026-01-06T15:48:10.192158
	// Standard formats
	"%Y-%m-%d %H:%M:%S",
	"%Y-%m-%dT%H:%M:%S",
	"%Y/%m/%d %H:%M:%S",
	"%d/%m/%Y %H:%M:%S",
	"%m/%d/%Y %H:%M:%S",
	"%H:%M:%S",
	"%Y%m%d%H%M%S",
	"[%Y-%m-%d %H:%M:%S]",
	"%d-%b-%Y %H:%M:%S",
	"%b %_d %H:%M:%S", // syslog: Jan  4 00:00:01 (space-padded day)
	"%b %d %H:%M:%S",  // syslog variant with zero-padded day
}

// detectTimestampFormat tries to detect timestamp format from a line
func detectTimestampFormat(line string) string {
	for _, pyFmt := range commonTimestampFormats {
		goFmt := pythonToGoFormat(pyFmt)
		// Try to find a matching timestamp in the line
		// We'll try parsing substrings of appropriate length
		fmtLen := len(goFmt)
		for i := 0; i <= len(line)-fmtLen && i < 50; i++ {
			substr := line[i : i+fmtLen]
			_, err := time.Parse(goFmt, substr)
			if err == nil {
				return pyFmt
			}
		}
	}
	return ""
}

// extractTimestamp extracts and parses timestamp from a line using the given format
func extractTimestamp(line, pyFormat string) (time.Time, bool) {
	goFmt := pythonToGoFormat(pyFormat)
	fmtLen := len(goFmt)
	
	for i := 0; i <= len(line)-fmtLen && i < 100; i++ {
		substr := line[i : i+fmtLen]
		t, err := time.Parse(goFmt, substr)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// HandleSetTimestampFormat prompts for Python datetime format string
func (a *App) HandleSetTimestampFormat() {
	current := a.stack.Current()
	input, ok := current.promptForInput("t (timestamp format): ")
	if !ok {
		return
	}
	if input == "" {
		a.timestampFormat = ""
		a.ShowTempMessage("Timestamp format cleared")
		return
	}
	a.timestampFormat = input
	a.ShowTempMessage(fmt.Sprintf("Format set: %s", input))
}

// HandleTimestampSearch searches for a timestamp
func (a *App) HandleTimestampSearch() {
	current := a.stack.Current()
	
	// Get input: 6 digits (hhmmss) or 12 digits (yymmddhhmmss)
	input, ok := current.promptForInput("b (timestamp [yymmdd]hhmmss): ")
	if !ok || input == "" {
		return
	}
	
	// Validate input
	if len(input) != 6 && len(input) != 12 {
		a.ShowTempMessage("Enter 6 (hhmmss) or 12 (yymmddhhmmss) digits")
		return
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			a.ShowTempMessage("Enter digits only")
			return
		}
	}
	
	// Parse target time
	var targetTime time.Time
	now := time.Now()
	if len(input) == 6 {
		// hhmmss - use today's date
		h, _ := strconv.Atoi(input[0:2])
		m, _ := strconv.Atoi(input[2:4])
		s, _ := strconv.Atoi(input[4:6])
		targetTime = time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, time.Local)
	} else {
		// yymmddhhmmss
		y, _ := strconv.Atoi(input[0:2])
		mo, _ := strconv.Atoi(input[2:4])
		d, _ := strconv.Atoi(input[4:6])
		h, _ := strconv.Atoi(input[6:8])
		mi, _ := strconv.Atoi(input[8:10])
		s, _ := strconv.Atoi(input[10:12])
		year := 2000 + y
		if y > 50 {
			year = 1900 + y
		}
		targetTime = time.Date(year, time.Month(mo), d, h, mi, s, 0, time.Local)
	}
	
	// Detect or use set format
	format := a.timestampFormat
	if format == "" {
		// Try to detect from current line
		line := current.GetLine(current.topLine)
		format = detectTimestampFormat(line)
		if format == "" {
			a.ShowTempMessage("Couldn't detect timestamp format. Use 't' to set.")
			return
		}
	}
	
	// Search from current line to end
	lines := current.GetLines()
	for i := current.topLine; i < len(lines); i++ {
		ts, ok := extractTimestamp(lines[i], format)
		if ok {
			// For time-only searches, adjust the date to match
			if len(input) == 6 {
				ts = time.Date(now.Year(), now.Month(), now.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, time.Local)
			}
			if ts.Equal(targetTime) || ts.After(targetTime) {
				current.topLine = i
				a.ShowTempMessage(fmt.Sprintf("Found at line %d", i+1))
				return
			}
		}
	}
	a.ShowTempMessage("No matching timestamp found")
}

// ShowHelp displays the help screen
func (a *App) ShowHelp() {
	type helpEntry struct {
		key  string
		desc string
	}

	sections := []struct {
		title   string
		entries []helpEntry
	}{
		{"Navigation", []helpEntry{
			{"j / ↓", "Move down one line"},
			{"k / ↑", "Move up one line"},
			{"h / ←", "Scroll left"},
			{"l / →", "Scroll right"},
			{"< / >", "Scroll left/right by 1 char"},
			{"g / Home", "Go to first line"},
			{"G / End", "Go to last line"},
			{"Ctrl+D/Space/PgDn", "Page down"},
			{"Ctrl+U/PgUp", "Page up"},
			{":<number>", "Go to specific line number"},
		}},
		{"Search", []helpEntry{
			{"/", "Search forward"},
			{"?", "Search backward"},
			{"n", "Next match"},
			{"N", "Previous match"},
			{"Ctrl+R", "Toggle regex mode (in prompt)"},
			{"Ctrl+I", "Toggle case-insensitive (in prompt)"},
		}},
		{"Timestamp", []helpEntry{
			{"t", "Set timestamp format (Python style)"},
			{"b", "Jump to timestamp ([yymmdd]hhmmss)"},
		}},
		{"Filters", []helpEntry{
			{"&", "Keep lines matching pattern"},
			{"-", "Exclude lines matching pattern"},
			{"+", "Add matching from original file"},
			{"=", "Reset to original file"},
			{"U", "Pop last filter (go back one level)"},
		}},
		{"Display", []helpEntry{
			{"w", "Toggle word wrap"},
			{"f", "Toggle JSON pretty-print"},
			{"F", "Toggle follow mode (tail -f)"},
			{"K", "Set sticky left columns"},
		}},
		{"Selection & Export", []helpEntry{
			{"v", "Enter visual selection mode"},
			{"y", "Yank (copy) selected lines"},
			{";", "Export filtered view to file"},
			{"Esc", "Exit visual mode"},
		}},
		{"Help", []helpEntry{
			{"H / F1", "Show this help screen"},
			{"q", "Quit"},
		}},
	}

	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
	width, height := termbox.Size()

	// Use nearly full screen with some margin
	margin := 2
	boxWidth := width - margin*2
	boxHeight := height - margin*2
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxHeight < 20 {
		boxHeight = 20
	}
	if boxWidth > width {
		boxWidth = width
	}
	if boxHeight > height {
		boxHeight = height
	}
	startX := (width - boxWidth) / 2
	startY := (height - boxHeight) / 2

	// Colors
	borderFg := termbox.ColorCyan
	titleFg := termbox.ColorYellow | termbox.AttrBold
	sectionFg := termbox.ColorGreen | termbox.AttrBold
	keyFg := termbox.ColorWhite | termbox.AttrBold
	descFg := termbox.ColorDefault
	bgColor := termbox.ColorDefault

	// Draw border
	drawBox := func(x, y, w, h int) {
		// Corners
		termbox.SetCell(x, y, '╭', borderFg, bgColor)
		termbox.SetCell(x+w-1, y, '╮', borderFg, bgColor)
		termbox.SetCell(x, y+h-1, '╰', borderFg, bgColor)
		termbox.SetCell(x+w-1, y+h-1, '╯', borderFg, bgColor)
		// Top and bottom
		for i := 1; i < w-1; i++ {
			termbox.SetCell(x+i, y, '─', borderFg, bgColor)
			termbox.SetCell(x+i, y+h-1, '─', borderFg, bgColor)
		}
		// Left and right
		for i := 1; i < h-1; i++ {
			termbox.SetCell(x, y+i, '│', borderFg, bgColor)
			termbox.SetCell(x+w-1, y+i, '│', borderFg, bgColor)
		}
		// Fill inside with background
		for row := 1; row < h-1; row++ {
			for col := 1; col < w-1; col++ {
				termbox.SetCell(x+col, y+row, ' ', descFg, bgColor)
			}
		}
	}

	drawText := func(x, y int, text string, fg termbox.Attribute) {
		for i, ch := range text {
			if x+i < startX+boxWidth-1 {
				termbox.SetCell(x+i, y, ch, fg, bgColor)
			}
		}
	}

	drawBox(startX, startY, boxWidth, boxHeight)

	// Title
	title := fmt.Sprintf(" CUT v%s - Keybindings ", version)
	titleX := startX + (boxWidth-len(title))/2
	drawText(titleX, startY, title, titleFg)

	// Calculate columns
	colWidth := (boxWidth - 4) / 3
	if colWidth < 25 {
		colWidth = (boxWidth - 4) / 2
	}

	// Draw sections across columns
	col := 0
	y := startY + 2
	maxY := startY + boxHeight - 3

	for _, section := range sections {
		colX := startX + 2 + col*colWidth

		// Check if section fits in current column
		neededRows := 1 + len(section.entries) + 1
		if y+neededRows > maxY && col < 2 {
			// Move to next column
			col++
			colX = startX + 2 + col*colWidth
			y = startY + 2
		}

		if y >= maxY {
			break // No more room
		}

		drawText(colX, y, section.title, sectionFg)
		y++

		for _, entry := range section.entries {
			if y >= maxY {
				break
			}
			drawText(colX, y, fmt.Sprintf("%-12s", entry.key), keyFg)
			drawText(colX+13, y, entry.desc, descFg)
			y++
		}
		y++ // Space between sections
	}

	// Footer
	footer := "Press any key to close"
	footerX := startX + (boxWidth-len(footer))/2
	drawText(footerX, startY+boxHeight-2, footer, termbox.ColorDefault|termbox.AttrDim)

	termbox.Flush()

	// Wait for any key
	for {
		ev := termbox.PollEvent()
		if ev.Type == termbox.EventKey {
			break
		}
	}
}

// ClearMessage clears the status message
func (a *App) ClearMessage() {
	a.statusMessage = ""
}

// filterChunkResult holds the result of filtering a chunk
type filterChunkResult struct {
	chunkIdx int
	lines    []string
	hasANSI  []bool // Whether each line has ANSI codes
	indices  []int  // Original line indices
}

// HandleFilter filters lines based on query
// If keep is true (&), keeps matching lines; if false (-), excludes matching lines
func (a *App) HandleFilter(keep bool) {
	current := a.stack.Current()
	currentTopLine := current.topLine

	prompt := "&"
	if !keep {
		prompt = "-"
	}

	query, isRegex, ignoreCase, ok := a.promptForFilter(prompt)
	if ok && query != "" {
		lines := current.GetLines()       // Get snapshot for thread-safety
		hasANSICache := current.GetHasANSI() // Get ANSI cache

		// Compile matcher based on options (uses index to check hasANSI cache)
		var matcher func(line string, hasANSI bool) bool
		if isRegex {
			pattern := query
			if ignoreCase {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				a.ShowTempMessage("Invalid regex: " + err.Error())
				return
			}
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return re.MatchString(stripANSI(line))
				}
				return re.MatchString(line)
			}
		} else if ignoreCase {
			queryLower := strings.ToLower(query)
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return strings.Contains(strings.ToLower(stripANSI(line)), queryLower)
				}
				return strings.Contains(strings.ToLower(line), queryLower)
			}
		} else {
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return strings.Contains(stripANSI(line), query)
				}
				return strings.Contains(line, query)
			}
		}

		// Create new viewer immediately with loading state
		newViewer := &Viewer{
			lines:    nil,
			loading:  true,
			filename: current.filename,
			topLine:  0,
			leftCol:  0,
		}
		a.stack.Push(newViewer)
		a.search.Clear()

		// Filter in parallel
		go func() {
			numWorkers := 8
			totalLines := len(lines)
			if totalLines < numWorkers {
				numWorkers = 1
			}
			chunkSize := (totalLines + numWorkers - 1) / numWorkers

			resultChan := make(chan filterChunkResult, numWorkers)

			// Start workers
			for w := 0; w < numWorkers; w++ {
				start := w * chunkSize
				end := start + chunkSize
				if end > totalLines {
					end = totalLines
				}
				if start >= totalLines {
					break
				}

				go func(chunkIdx, start, end int) {
					var chunkLines []string
					var chunkHasANSI []bool
					var chunkIndices []int
					for i := start; i < end; i++ {
						has := i < len(hasANSICache) && hasANSICache[i]
						matches := matcher(lines[i], has)
						if matches == keep {
							chunkLines = append(chunkLines, lines[i])
							chunkHasANSI = append(chunkHasANSI, has)
							chunkIndices = append(chunkIndices, i)
						}
					}
					resultChan <- filterChunkResult{chunkIdx, chunkLines, chunkHasANSI, chunkIndices}
				}(w, start, end)
			}

			// Collect results in order
			results := make([]filterChunkResult, numWorkers)
			received := 0
			expectedWorkers := numWorkers
			if totalLines < numWorkers {
				expectedWorkers = 1
			}
			for i := 0; i < expectedWorkers && received < numWorkers; i++ {
				result := <-resultChan
				results[result.chunkIdx] = result
				received++
				if result.chunkIdx >= expectedWorkers {
					break
				}
			}
			close(resultChan)

			// Drain any remaining
			for range resultChan {
			}

			// Merge results in order and stream to viewer
			foundMatch := false
			matchesBefore := 0
			lineCount := 0
			var allIndices []int
			var allHasANSI []bool

			for chunkIdx := 0; chunkIdx < numWorkers; chunkIdx++ {
				chunk := results[chunkIdx]
				for j, line := range chunk.lines {
					newViewer.mu.Lock()
					newViewer.lines = append(newViewer.lines, line)
					newViewer.hasANSI = append(newViewer.hasANSI, chunk.hasANSI[j])
					newViewer.mu.Unlock()

					origIdx := chunk.indices[j]
					allIndices = append(allIndices, origIdx)
					allHasANSI = append(allHasANSI, chunk.hasANSI[j])

					if origIdx >= currentTopLine && !foundMatch {
						foundMatch = true
						newViewer.topLine = matchesBefore
					}
					if !foundMatch {
						matchesBefore++
					}

					lineCount++
					if lineCount <= 100 || lineCount%1000 == 0 {
						termbox.Interrupt()
					}
				}
			}

			newViewer.mu.Lock()
			newViewer.originIndices = allIndices
			newViewer.loading = false
			newViewer.mu.Unlock()
			termbox.Interrupt()
		}()
	}
}

// HandleFilterAppend appends matching lines from original
func (a *App) HandleFilterAppend() {
	current := a.stack.Current()
	currentLine := current.GetLine(current.topLine)

	query, isRegex, ignoreCase, ok := a.promptForFilter("+")
	if ok && query != "" {
		original := a.stack.viewers[0]
		currentLines := current.GetLines()
		originalLines := original.GetLines()
		originalHasANSI := original.GetHasANSI()

		// Compile matcher based on options (uses hasANSI flag)
		var matcher func(line string, hasANSI bool) bool
		if isRegex {
			pattern := query
			if ignoreCase {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				a.ShowTempMessage("Invalid regex: " + err.Error())
				return
			}
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return re.MatchString(stripANSI(line))
				}
				return re.MatchString(line)
			}
		} else if ignoreCase {
			queryLower := strings.ToLower(query)
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return strings.Contains(strings.ToLower(stripANSI(line)), queryLower)
				}
				return strings.Contains(strings.ToLower(line), queryLower)
			}
		} else {
			matcher = func(line string, hasANSI bool) bool {
				if hasANSI {
					return strings.Contains(stripANSI(line), query)
				}
				return strings.Contains(line, query)
			}
		}

		// Create new viewer immediately with loading state
		newViewer := &Viewer{
			lines:    nil,
			loading:  true,
			filename: current.filename,
			topLine:  0,
			leftCol:  0,
		}
		a.stack.Push(newViewer)
		a.search.Clear()

		// Process in parallel
		go func() {
			// Build current counts map (sequential - usually small)
			currentCounts := make(map[string]int)
			for _, line := range currentLines {
				currentCounts[line]++
			}

			// Parallel filtering of original lines
			numWorkers := 8
			totalLines := len(originalLines)
			if totalLines < numWorkers {
				numWorkers = 1
			}
			chunkSize := (totalLines + numWorkers - 1) / numWorkers

			// For append, we need to track which current lines are used per chunk
			// Each worker gets its own copy of counts for the lines in its chunk
			type appendChunkResult struct {
				chunkIdx int
				lines    []string
				hasANSI  []bool
				indices  []int
			}
			resultChan := make(chan appendChunkResult, numWorkers)

			// Pre-calculate which original lines match current lines (need order)
			// First, mark lines that are in current
			inCurrent := make([]bool, totalLines)
			tempCounts := make(map[string]int)
			for k, v := range currentCounts {
				tempCounts[k] = v
			}
			for i, line := range originalLines {
				if tempCounts[line] > 0 {
					inCurrent[i] = true
					tempCounts[line]--
				}
			}

			// Start workers - each checks if line is in current OR matches query
			for w := 0; w < numWorkers; w++ {
				start := w * chunkSize
				end := start + chunkSize
				if end > totalLines {
					end = totalLines
				}
				if start >= totalLines {
					break
				}

				go func(chunkIdx, start, end int) {
					var chunkLines []string
					var chunkHasANSI []bool
					var chunkIndices []int
					for i := start; i < end; i++ {
						has := i < len(originalHasANSI) && originalHasANSI[i]
						if inCurrent[i] || matcher(originalLines[i], has) {
							chunkLines = append(chunkLines, originalLines[i])
							chunkHasANSI = append(chunkHasANSI, has)
							chunkIndices = append(chunkIndices, i)
						}
					}
					resultChan <- appendChunkResult{chunkIdx, chunkLines, chunkHasANSI, chunkIndices}
				}(w, start, end)
			}

			// Collect results in order
			results := make([]appendChunkResult, numWorkers)
			expectedWorkers := numWorkers
			if totalLines < numWorkers {
				expectedWorkers = 1
			}
			for i := 0; i < expectedWorkers; i++ {
				result := <-resultChan
				results[result.chunkIdx] = result
			}
			close(resultChan)

			// Merge results in order and stream to viewer
			foundCurrentLine := false
			lineCount := 0
			var allIndices []int

			for chunkIdx := 0; chunkIdx < numWorkers; chunkIdx++ {
				chunk := results[chunkIdx]
				for j, line := range chunk.lines {
					newViewer.mu.Lock()
					newViewer.lines = append(newViewer.lines, line)
					newViewer.hasANSI = append(newViewer.hasANSI, chunk.hasANSI[j])
					if !foundCurrentLine && line == currentLine {
						foundCurrentLine = true
						newViewer.topLine = len(newViewer.lines) - 1
					}
					newViewer.mu.Unlock()

					allIndices = append(allIndices, chunk.indices[j])

					lineCount++
					if lineCount <= 100 || lineCount%1000 == 0 {
						termbox.Interrupt()
					}
				}
			}

			newViewer.mu.Lock()
			newViewer.originIndices = allIndices
			newViewer.loading = false
			newViewer.mu.Unlock()
			termbox.Interrupt()
		}()
	}
}

// HandleGotoLine prompts for a line number and jumps to it
func (a *App) HandleGotoLine() {
	current := a.stack.Current()
	input, ok := current.promptForInput(":")
	if ok && input != "" {
		lineNum, err := strconv.Atoi(input)
		if err != nil {
			a.ShowTempMessage("Invalid line number")
			return
		}
		// Convert to 0-based index
		lineIdx := lineNum - 1
		if lineIdx < 0 {
			lineIdx = 0
		}
		maxLine := current.LineCount() - 1
		if lineIdx > maxLine {
			lineIdx = maxLine
		}
		current.topLine = lineIdx
	}
}

// HandleExport saves the current filtered view to a file
func (a *App) HandleExport() {
	current := a.stack.Current()
	filename, ok := current.promptForInput(";")
	if !ok || filename == "" {
		return
	}

	lines := current.GetLines()
	content := strings.Join(lines, "\n")

	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		a.ShowTempMessage(fmt.Sprintf("Error: %v", err))
		return
	}

	a.ShowTempMessage(fmt.Sprintf("Saved %d lines to %s", len(lines), filename))
}

// HandleStickyLeft prompts for the number of sticky left columns
func (a *App) HandleStickyLeft() {
	current := a.stack.Current()
	input, ok := current.promptForInput("K (sticky cols): ")
	if !ok {
		return
	}
	if input == "" {
		// Empty input disables the feature
		current.stickyLeft = 0
		a.ShowTempMessage("Sticky left disabled")
		return
	}
	num, err := strconv.Atoi(input)
	if err != nil || num < 0 {
		a.ShowTempMessage("Invalid number")
		return
	}
	current.stickyLeft = num
	if num > 0 {
		a.ShowTempMessage(fmt.Sprintf("Sticky left: %d chars", num))
	} else {
		a.ShowTempMessage("Sticky left disabled")
	}
}

// ToggleFollow toggles follow mode for the root viewer
func (a *App) ToggleFollow() {
	// Follow mode only works on the root viewer
	root := a.stack.viewers[0]
	root.follow = !root.follow
	if root.follow {
		// Start following if not already
		go root.followFile(root.filename)
		// Jump to end
		root.goToEnd()
		a.ShowTempMessage("Follow mode ON")
	} else {
		a.ShowTempMessage("Follow mode OFF")
	}
}

// HandleSearch performs a search starting from current line
// If backward is true, searches upward with "?" prompt; otherwise searches downward with "/" prompt
func (a *App) HandleSearch(backward bool) {
	current := a.stack.Current()
	prompt := "/"
	noMatchMsg := "EOF - no more matches"
	if backward {
		prompt = "?"
		noMatchMsg = "BOF - no more matches"
	}

	query, isRegex, ignoreCase, ok := a.promptForSearch(prompt)
	if ok && query != "" {
		lines := current.GetLines()
		hasANSI := current.GetHasANSI()
		lineIdx := a.search.Search(lines, hasANSI, query, current.topLine, backward, isRegex, ignoreCase)
		if lineIdx >= 0 {
			current.topLine = lineIdx
		} else if a.search.HasResults() {
			a.ShowTempMessage(noMatchMsg)
		}
	}
}

// HandleSearchNav navigates search results
// If reverse is false (n key): continues in search direction
// If reverse is true (N key): goes opposite to search direction
func (a *App) HandleSearchNav(reverse bool) {
	if !a.search.HasResults() {
		return
	}

	current := a.stack.Current()
	topLine := current.topLine

	// Determine if we should go forward (down) or backward (up) in the file
	goingUp := a.search.backward != reverse

	if goingUp {
		// Find the last match BEFORE topLine
		found := false
		for i := len(a.search.matches) - 1; i >= 0; i-- {
			if a.search.matches[i] < topLine {
				current.topLine = a.search.matches[i]
				a.search.current = i
				found = true
				break
			}
		}
		if !found {
			a.ShowTempMessage("BOF")
		}
	} else {
		// Find the first match AFTER topLine
		found := false
		for i, lineIdx := range a.search.matches {
			if lineIdx > topLine {
				current.topLine = lineIdx
				a.search.current = i
				found = true
				break
			}
		}
		if !found {
			a.ShowTempMessage("EOF")
		}
	}
}

// HandleStackNav navigates the viewer stack
// If reset is true (=), resets to first viewer; if false (^U), pops one level
func (a *App) HandleStackNav(reset bool) {
	current := a.stack.Current()
	topLine := current.topLine

	// Get the target line index in the parent/original viewer
	var targetLine int
	if len(current.originIndices) > 0 && topLine < len(current.originIndices) {
		targetLine = current.originIndices[topLine]
	} else {
		targetLine = topLine
	}

	// For reset, we need to trace back through all viewers to find original index
	if reset && len(a.stack.viewers) > 1 {
		// Walk up the stack to find the original line number
		for i := len(a.stack.viewers) - 1; i >= 1; i-- {
			v := a.stack.viewers[i]
			if len(v.originIndices) > 0 && targetLine < len(v.originIndices) {
				targetLine = v.originIndices[targetLine]
			}
		}
	}

	var changed bool
	if reset {
		changed = a.stack.Reset()
	} else {
		changed = a.stack.Pop()
	}

	if changed {
		newCurrent := a.stack.Current()
		newCurrent.topLineOffset = 0

		// If newCurrent has originIndices, find closest line using binary search
		if len(newCurrent.originIndices) > 0 {
			// Binary search for the target line or closest below it
			idx := sort.Search(len(newCurrent.originIndices), func(i int) bool {
				return newCurrent.originIndices[i] >= targetLine
			})
			if idx < len(newCurrent.originIndices) {
				newCurrent.topLine = idx
			} else if len(newCurrent.originIndices) > 0 {
				newCurrent.topLine = len(newCurrent.originIndices) - 1
			}
		} else {
			// No originIndices (original file), just use the target line clamped to bounds
			lineCount := newCurrent.LineCount()
			if targetLine >= lineCount {
				newCurrent.topLine = lineCount - 1
			} else {
				newCurrent.topLine = targetLine
			}
		}
	}
	a.search.Clear()
}

// Draw renders the current view
func (a *App) Draw() {
	current := a.stack.Current()
	current.resize(termbox.Size())
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	lineCount := current.LineCount()

	if current.wordWrap {
		a.drawWrapped(current, lineCount)
	} else {
		a.drawNormal(current, lineCount)
	}

	if a.visualMode {
		// Visual mode status bar
		startLine := a.visualStart
		endLine := a.visualCursor
		if startLine > endLine {
			startLine, endLine = endLine, startLine
		}
		status := fmt.Sprintf(" VISUAL: Line %d/%d | Marked %d-%d ",
			a.visualCursor+1, current.LineCount(), startLine+1, endLine+1)
		a.drawVisualStatusBar(current, status)
		termbox.Flush()
	} else if a.statusMessage != "" && time.Now().Before(a.messageExpiry) {
		current.showMessage(a.statusMessage)
	} else {
		a.statusMessage = ""
		// Calculate original line number by tracing through the stack
		origLine := current.topLine
		for i := len(a.stack.viewers) - 1; i >= 1; i-- {
			v := a.stack.viewers[i]
			if len(v.originIndices) > 0 && origLine < len(v.originIndices) {
				origLine = v.originIndices[origLine]
			}
		}
		origTotal := a.stack.viewers[0].LineCount()
		current.drawStatusBarWithDepth(len(a.stack.viewers), origLine, origTotal)
		termbox.Flush()
	}
}

// drawNormal renders without word wrap
func (a *App) drawNormal(current *Viewer, lineCount int) {
	screenY := 0
	lineIndex := current.topLine
	skipRows := current.topLineOffset // Skip this many rows at start

	// Pastel blue color (using 256-color mode: color 117 is a light blue)
	stickyFg := termbox.Attribute(117 + 1) // +1 because termbox uses 1-indexed colors

	// Calculate effective sticky columns
	stickyActive := current.stickyLeft > 0
	stickyWidth := current.stickyLeft
	if stickyActive && stickyWidth > current.width/2 {
		stickyWidth = current.width / 2 // Cap at half screen
	}

	// Visual selection range
	var visualStart, visualEnd int
	if a.visualMode {
		visualStart = a.visualStart
		visualEnd = a.visualCursor
		if visualStart > visualEnd {
			visualStart, visualEnd = visualEnd, visualStart
		}
	}

	for screenY < current.height && lineIndex < lineCount {
		line := current.GetLine(lineIndex)

		// Check if this line is in visual selection
		inVisualSelection := a.visualMode && lineIndex >= visualStart && lineIndex <= visualEnd

		// Expand JSON if enabled
		var linesToRender []string
		if current.jsonPretty && isJSON(line) {
			linesToRender = formatJSON(line)
		} else {
			linesToRender = []string{line}
		}

		for _, renderLine := range linesToRender {
			if skipRows > 0 {
				skipRows--
				continue
			}
			if screenY >= current.height {
				break
			}

			cells := parseANSI(renderLine)
			matchPositions := a.getMatchPositions(cells)

			screenX := 0

			// Visual selection background color
			visualBg := termbox.Attribute(239) // Dark gray for selection

			if stickyActive {
				// Draw sticky left columns in pastel blue
				for i := 0; i < stickyWidth && i < len(cells); i++ {
					if screenX >= current.width {
						break
					}
					fg := stickyFg
					bg := termbox.ColorDefault
					if inVisualSelection {
						bg = visualBg
					}
					// Preserve search highlighting even in sticky area
					if matchPositions != nil && i < len(matchPositions) && matchPositions[i] {
						fg = termbox.ColorBlack
						bg = termbox.ColorYellow
					}
					termbox.SetCell(screenX, screenY, cells[i].char, fg, bg)
					screenX++
				}


				// Draw the rest of the line starting from leftCol (or after sticky if not scrolled)
				startCol := current.leftCol
				if current.leftCol == 0 {
					startCol = stickyWidth // Skip sticky chars that were already drawn
				}
				for i := startCol; i < len(cells); i++ {
					if screenX >= current.width {
						break
					}
					fg, bg := cells[i].fg, cells[i].bg
					if inVisualSelection {
						bg = visualBg
					}
					if matchPositions != nil && i < len(matchPositions) && matchPositions[i] {
						fg = termbox.ColorBlack
						bg = termbox.ColorYellow
					}
					termbox.SetCell(screenX, screenY, cells[i].char, fg, bg)
					screenX++
				}
				// Fill rest of line with selection color if in visual mode
				if inVisualSelection {
					for screenX < current.width {
						termbox.SetCell(screenX, screenY, ' ', termbox.ColorDefault, visualBg)
						screenX++
					}
				}
			} else {
				// Normal rendering (no sticky)
				for i, cell := range cells {
					if i < current.leftCol {
						continue
					}
					if screenX >= current.width {
						break
					}
					fg, bg := cell.fg, cell.bg
					if inVisualSelection {
						bg = visualBg
					}
					if matchPositions != nil && i < len(matchPositions) && matchPositions[i] {
						fg = termbox.ColorBlack
						bg = termbox.ColorYellow
					}
					termbox.SetCell(screenX, screenY, cell.char, fg, bg)
					screenX++
				}
				// Fill rest of line with selection color if in visual mode
				if inVisualSelection {
					for screenX < current.width {
						termbox.SetCell(screenX, screenY, ' ', termbox.ColorDefault, visualBg)
						screenX++
					}
				}
			}
			screenY++
		}
		lineIndex++
	}
}

// drawWrapped renders with word wrap
func (a *App) drawWrapped(current *Viewer, lineCount int) {
	screenY := 0
	lineIndex := current.topLine
	skipRows := current.topLineOffset // Skip this many rows at start

	for screenY < current.height && lineIndex < lineCount {
		line := current.GetLine(lineIndex)

		// Expand JSON if enabled
		var linesToRender []string
		if current.jsonPretty && isJSON(line) {
			linesToRender = formatJSON(line)
		} else {
			linesToRender = []string{line}
		}

		for _, renderLine := range linesToRender {
			cells := parseANSI(renderLine)
			matchPositions := a.getMatchPositions(cells)

			if len(cells) == 0 {
				// Empty line
				if skipRows > 0 {
					skipRows--
				} else if screenY < current.height {
					screenY++
				}
				continue
			}

			// Wrap the line across multiple screen rows
			cellIdx := 0
			for cellIdx < len(cells) {
				if skipRows > 0 {
					// Skip this wrapped row
					skipRows--
					// Advance cellIdx by one row's worth
					cellIdx += current.width
					continue
				}
				if screenY >= current.height {
					break
				}

				screenX := 0
				for screenX < current.width && cellIdx < len(cells) {
					cell := cells[cellIdx]
					fg, bg := cell.fg, cell.bg
					if matchPositions != nil && cellIdx < len(matchPositions) && matchPositions[cellIdx] {
						fg = termbox.ColorBlack
						bg = termbox.ColorYellow
					}
					termbox.SetCell(screenX, screenY, cell.char, fg, bg)
					screenX++
					cellIdx++
				}
				screenY++
			}
		}
		lineIndex++
	}
}

// getMatchPositions returns search match positions for highlighting
func (a *App) getMatchPositions(cells []ansiCell) []bool {
	if a.search.query == "" {
		return nil
	}

	matchPositions := make([]bool, len(cells))
	plainText := make([]rune, len(cells))
	for i, c := range cells {
		plainText[i] = c.char
	}
	plainStr := string(plainText)

	if a.search.regex != nil {
		// Regex search - use regex for highlighting
		matches := a.search.regex.FindAllStringIndex(plainStr, -1)
		for _, match := range matches {
			startRune := len([]rune(plainStr[:match[0]]))
			endRune := len([]rune(plainStr[:match[1]]))
			for j := startRune; j < endRune && j < len(matchPositions); j++ {
				matchPositions[j] = true
			}
		}
	} else if a.search.ignoreCase {
		// Case-insensitive literal search
		lowerStr := strings.ToLower(plainStr)
		lowerQuery := strings.ToLower(a.search.query)
		queryLen := len([]rune(lowerQuery))
		idx := 0
		for {
			pos := strings.Index(lowerStr[idx:], lowerQuery)
			if pos == -1 {
				break
			}
			// Convert byte position to rune position
			runePos := len([]rune(lowerStr[:idx+pos]))
			for j := runePos; j < runePos+queryLen && j < len(matchPositions); j++ {
				matchPositions[j] = true
			}
			idx += pos + 1
		}
	} else {
		// Case-sensitive literal search - use strings.Index
		query := a.search.query
		queryLen := len([]rune(query))
		idx := 0
		for {
			pos := strings.Index(plainStr[idx:], query)
			if pos == -1 {
				break
			}
			// Convert byte position to rune position
			runePos := len([]rune(plainStr[:idx+pos]))
			for j := runePos; j < runePos+queryLen && j < len(matchPositions); j++ {
				matchPositions[j] = true
			}
			idx += pos + 1
		}
	}
	return matchPositions
}

func (v *Viewer) run() error {
	fmt.Print("\033[?1049h\033[H")
	defer fmt.Print("\033[?1049l")

	if err := termbox.Init(); err != nil {
		return err
	}
	defer termbox.Close()

	termbox.SetInputMode(termbox.InputEsc)
	termbox.SetOutputMode(termbox.Output256)

	app := NewApp(v)
	app.Draw()

	for {
		current := app.stack.Current()

		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			app.ClearMessage()

			if ev.Ch != 0 {
				switch ev.Ch {
				case 'q':
					if app.visualMode {
						app.ExitVisualMode()
					} else {
						return nil
					}
				case 'H':
					app.ShowHelp()
				case 'j':
					if app.visualMode {
						app.VisualCursorDown()
					} else {
						current.navigateDown()
					}
				case 'k':
					if app.visualMode {
						app.VisualCursorUp()
					} else {
						current.navigateUp()
					}
				case 'h':
					current.navigateLeft(15)
				case 'l':
					current.navigateRight(15)
				case 'w':
					current.wordWrap = !current.wordWrap
					current.leftCol = 0         // Reset horizontal scroll when toggling wrap
					current.topLineOffset = 0   // Reset line offset
				case 'g':
					if app.visualMode {
						app.VisualGoToStart()
					} else {
						current.goToStart()
					}
				case 'G':
					if app.visualMode {
						app.VisualGoToEnd()
					} else {
						current.goToEnd()
					}
				case ':':
					app.HandleGotoLine()
				case ';':
					app.HandleExport()
				case 'f':
					current.jsonPretty = !current.jsonPretty
					current.topLineOffset = 0 // Reset line offset
				case 'F':
					app.ToggleFollow()
				case '&':
					app.HandleFilter(true)
				case '-':
					app.HandleFilter(false)
				case '+':
					app.HandleFilterAppend()
				case '/':
					app.HandleSearch(false)
				case '?':
					app.HandleSearch(true)
				case 'n':
					app.HandleSearchNav(false)
				case 'N':
					app.HandleSearchNav(true)
				case '=':
					app.HandleStackNav(true)
				case '>':
					current.navigateRight(1)
				case '<':
					current.navigateLeft(1)
				case 'K':
					app.HandleStickyLeft()
				case 'v':
					if !app.visualMode {
						app.EnterVisualMode()
					}
				case 'y':
					if app.visualMode {
						app.YankVisualSelection()
					}
				case 't':
					app.HandleSetTimestampFormat()
				case 'b':
					app.HandleTimestampSearch()
				case 'U':
					app.HandleStackNav(false)
				}
			} else {
				switch ev.Key {
				case termbox.KeyArrowUp:
					if app.visualMode {
						app.VisualCursorUp()
					} else {
						current.navigateUp()
					}
				case termbox.KeyArrowDown:
					if app.visualMode {
						app.VisualCursorDown()
					} else {
						current.navigateDown()
					}
				case termbox.KeyArrowLeft:
					current.navigateLeft(15)
				case termbox.KeyArrowRight:
					current.navigateRight(15)
				case termbox.KeyPgdn, termbox.KeySpace, termbox.KeyCtrlD:
					if app.visualMode {
						app.VisualPageDown()
					} else {
						current.pageDown()
					}
				case termbox.KeyPgup, termbox.KeyCtrlU:
					if app.visualMode {
						app.VisualPageUp()
					} else {
						current.pageUp()
					}
				case termbox.KeyHome:
					if app.visualMode {
						app.VisualGoToStart()
					} else {
						current.goToStart()
					}
				case termbox.KeyEnd:
					if app.visualMode {
						app.VisualGoToEnd()
					} else {
						current.goToEnd()
					}
				case termbox.KeyF1:
					app.ShowHelp()
				case termbox.KeyEsc:
					if app.visualMode {
						app.ExitVisualMode()
					}
				case termbox.KeyCtrlC:
					return nil
				}
			}
			app.Draw()

		case termbox.EventResize:
			termbox.Sync()
			app.Draw()

		case termbox.EventInterrupt:
			app.Draw()

		case termbox.EventError:
			return ev.Err
		}
	}
}

// fileStream represents an open file with its current line buffered
type fileStream struct {
	scanner   *bufio.Scanner
	file      *os.File
	fileIdx   int
	prefix    string
	currLine  string
	currTime  time.Time
	hasTime   bool
	exhausted bool
}

// NewViewerFromMultipleFiles creates a viewer by streaming and merging multiple files by timestamp
func NewViewerFromMultipleFiles(filenames []string) (*Viewer, error) {
	if len(filenames) == 0 {
		return nil, fmt.Errorf("no files provided")
	}
	if len(filenames) == 1 {
		return NewViewer(filenames[0])
	}

	// Build filename legend
	var legend []string
	for i, name := range filenames {
		legend = append(legend, fmt.Sprintf("%d> %s", i, name))
	}
	legendStr := strings.Join(legend, " ")

	v := &Viewer{
		lines:    nil,
		loading:  true,
		filename: legendStr,
		topLine:  0,
		leftCol:  0,
	}

	go func() {
		// Open all files and create streams
		var streams []*fileStream
		var detectedFormat string

		for fileIdx, filename := range filenames {
			file, err := os.Open(filename)
			if err != nil {
				continue
			}

			scanner := bufio.NewScanner(file)
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, 10*1024*1024)

			stream := &fileStream{
				scanner: scanner,
				file:    file,
				fileIdx: fileIdx,
				prefix:  fmt.Sprintf("%d> ", fileIdx),
			}

			// Read first line to prime the stream
			if scanner.Scan() {
				line := scanner.Text()
				stream.currLine = stream.prefix + line

				// Try to detect format from first line if not set
				if detectedFormat == "" {
					detectedFormat = detectTimestampFormat(line)
				}

				// Parse timestamp
				if detectedFormat != "" {
					if ts, ok := extractTimestamp(line, detectedFormat); ok {
						stream.currTime = ts
						stream.hasTime = true
					}
				}
			} else {
				stream.exhausted = true
				file.Close()
			}

			streams = append(streams, stream)
		}

		// K-way merge: always pick the stream with the oldest timestamp
		const batchSize = 10000
		batch := make([]string, 0, batchSize)
		batchHasANSI := make([]bool, 0, batchSize)
		totalLines := 0

		for {
			// Find stream to pick: prioritize lines without timestamps, then oldest timestamp
			var picked *fileStream
			for _, s := range streams {
				if s.exhausted {
					continue
				}
				if picked == nil {
					picked = s
				} else if !s.hasTime && picked.hasTime {
					// Lines without timestamp are output immediately (priority)
					picked = s
				} else if s.hasTime && !picked.hasTime {
					// Keep the one without timestamp (it has priority)
					// picked stays
				} else if s.hasTime && picked.hasTime {
					// Both have timestamps: pick the oldest
					if s.currTime.Before(picked.currTime) {
						picked = s
					}
				}
				// If neither has timestamp, keep first found (preserve order)
			}

			// All streams exhausted
			if picked == nil {
				break
			}

			// Add the picked line to batch
			batch = append(batch, picked.currLine)
			batchHasANSI = append(batchHasANSI, lineHasANSI(picked.currLine))

			// Advance that stream to its next line
			if picked.scanner.Scan() {
				line := picked.scanner.Text()
				picked.currLine = picked.prefix + line
				picked.hasTime = false

				if detectedFormat != "" {
					if ts, ok := extractTimestamp(line, detectedFormat); ok {
						picked.currTime = ts
						picked.hasTime = true
					}
				}
			} else {
				picked.exhausted = true
				picked.file.Close()
			}

			// Flush batch periodically
			if len(batch) >= batchSize {
				v.mu.Lock()
				v.lines = append(v.lines, batch...)
				v.hasANSI = append(v.hasANSI, batchHasANSI...)
				v.mu.Unlock()
				totalLines += len(batch)
				batch = batch[:0]
				batchHasANSI = batchHasANSI[:0]

				if totalLines == batchSize || totalLines%100000 == 0 {
					termbox.Interrupt()
				}
			}
		}

		// Append remaining
		if len(batch) > 0 {
			v.mu.Lock()
			v.lines = append(v.lines, batch...)
			v.hasANSI = append(v.hasANSI, batchHasANSI...)
			v.mu.Unlock()
		}

		v.mu.Lock()
		v.loading = false
		v.mu.Unlock()
		termbox.Interrupt()
	}()

	return v, nil
}

const version = "1.0.0"

func main() {
	// Parse command line flags
	followFlag := flag.Bool("f", false, "Follow mode (like tail -f)")
	followLongFlag := flag.Bool("follow", false, "Follow mode (like tail -f)")
	helpFlag := flag.Bool("h", false, "Show help")
	helpLongFlag := flag.Bool("help", false, "Show help")
	versionFlag := flag.Bool("version", false, "Show version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "sieve - An in-memory file viewer with powerful filtering\n\n")
		fmt.Fprintf(os.Stderr, "Usage: sieve [OPTIONS] <filename> [filename2] [filename3] ...\n")
		fmt.Fprintf(os.Stderr, "       command | sieve\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -f, --follow    Follow mode (like tail -f)\n")
		fmt.Fprintf(os.Stderr, "  -h, --help      Show this help message\n")
		fmt.Fprintf(os.Stderr, "      --version   Show version\n\n")
		fmt.Fprintf(os.Stderr, "Press 'H' or F1 while running for keybinding help.\n")
	}

	flag.Parse()

	if *helpFlag || *helpLongFlag {
		flag.Usage()
		os.Exit(0)
	}

	if *versionFlag {
		fmt.Printf("sieve version %s\n", version)
		os.Exit(0)
	}

	follow := *followFlag || *followLongFlag
	args := flag.Args()

	var viewer *Viewer
	var err error

	// Check if data is being piped via stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// stdin has data (pipe or redirect)
		viewer = NewViewerFromStdin()
	} else if len(args) >= 2 {
		// Multiple files - merge sort by timestamp
		viewer, err = NewViewerFromMultipleFiles(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading files: %v\n", err)
			os.Exit(1)
		}
	} else if len(args) >= 1 {
		// Single file
		viewer, err = NewViewer(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading file: %v\n", err)
			os.Exit(1)
		}
	} else {
		flag.Usage()
		os.Exit(1)
	}

	// Set follow mode
	viewer.follow = follow

	if err := viewer.run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
