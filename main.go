package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
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

type Viewer struct {
	lines   []string     // All lines from the file
	mu      sync.RWMutex // Protects lines during background loading
	loading bool         // True while file is still loading
	topLine int          // Index of the line at the top of the screen
	leftCol int          // Horizontal scroll offset
	width   int          // Terminal width
	height  int          // Terminal height
}

// ViewerStack manages a stack of viewers for filtering navigation
type ViewerStack struct {
	viewers []*Viewer
}

// App holds the application state
type App struct {
	stack         *ViewerStack
	search        *SearchState
	statusMessage string
	messageExpiry time.Time
}

// SearchState holds the current search results
type SearchState struct {
	query    string         // Current search query
	regex    *regexp.Regexp // Compiled regex pattern
	isRegex  bool           // True if regex mode is enabled
	matches  []int          // Line indices that match
	current  int            // Current match index (-1 if none)
	backward bool           // True if last search was backward (?)
}

// Clear resets the search state
func (s *SearchState) Clear() {
	s.query = ""
	s.regex = nil
	s.isRegex = false
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
func (s *SearchState) Search(lines []string, query string, startLine int, backward bool, isRegex bool) int {
	s.query = query
	s.isRegex = isRegex
	s.matches = nil
	s.current = -1
	s.backward = backward

	// Compile regex pattern
	var re *regexp.Regexp
	if isRegex {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			// Invalid regex, treat as literal
			re = regexp.MustCompile(regexp.QuoteMeta(query))
		}
	} else {
		// Literal string search - escape regex metacharacters
		re = regexp.MustCompile(regexp.QuoteMeta(query))
	}
	s.regex = re

	for i, line := range lines {
		if s.regex.MatchString(line) {
			s.matches = append(s.matches, i)
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
		lines:   nil,
		loading: true,
		topLine: 0,
		leftCol: 0,
	}

	// Load file in background (sequential - optimal for I/O bound disk reads)
	go func() {
		defer file.Close()

		scanner := bufio.NewScanner(file)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)

		lineCount := 0
		for scanner.Scan() {
			v.mu.Lock()
			v.lines = append(v.lines, scanner.Text())
			v.mu.Unlock()

			lineCount++
			if lineCount <= 100 || lineCount%1000 == 0 {
				termbox.Interrupt()
			}
		}

		v.mu.Lock()
		v.loading = false
		v.mu.Unlock()
		termbox.Interrupt()
	}()

	return v, nil
}

// NewViewerFromLines creates a Viewer from an existing slice of lines
func NewViewerFromLines(lines []string) *Viewer {
	return &Viewer{
		lines:   lines,
		loading: false,
		topLine: 0,
		leftCol: 0,
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
	v.drawStatusBarWithDepth(1)
}

func (v *Viewer) drawStatusBarWithDepth(depth int) {
	statusY := v.height
	lineCount := v.LineCount()
	loadingStr := ""
	if v.IsLoading() {
		loadingStr = " [loading...]"
	}

	var status string
	if depth > 1 {
		status = fmt.Sprintf(" Line %d/%d | Col %d | Depth %d | ^U:back =:reset q:quit%s ",
			v.topLine+1, lineCount, v.leftCol, depth, loadingStr)
	} else {
		status = fmt.Sprintf(" Line %d/%d | Col %d | Press 'q' to quit%s ",
			v.topLine+1, lineCount, v.leftCol, loadingStr)
	}

	// Clear the status line first
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

func (v *Viewer) navigateUp() {
	if v.topLine > 0 {
		v.topLine--
	}
}

func (v *Viewer) navigateDown() {
	maxTop := v.LineCount() - 1
	if maxTop < 0 {
		maxTop = 0
	}
	if v.topLine < maxTop {
		v.topLine++
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

func (v *Viewer) pageUp() {
	v.topLine -= v.height
	if v.topLine < 0 {
		v.topLine = 0
	}
}

func (v *Viewer) goToStart() {
	v.topLine = 0
}

func (v *Viewer) goToEnd() {
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
	input, _, ok := v.promptForInputWithRegex(prompt, false)
	return input, ok
}

// promptForInputWithRegex prompts for input with optional regex toggle (Ctrl+R)
// Returns: input string, isRegex flag, ok
func (v *Viewer) promptForInputWithRegex(prompt string, allowRegexToggle bool) (string, bool, bool) {
	input := ""
	isRegex := false

	for {
		// Draw the prompt line at the bottom
		statusY := v.height
		regexIndicator := ""
		if allowRegexToggle && isRegex {
			regexIndicator = "[regex] "
		}
		line := prompt + regexIndicator + input

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
				return input, isRegex, true
			} else if ev.Key == termbox.KeyEsc {
				termbox.HideCursor()
				return "", false, false
			} else if ev.Key == termbox.KeyBackspace || ev.Key == termbox.KeyBackspace2 {
				if len(input) > 0 {
					runes := []rune(input)
					input = string(runes[:len(runes)-1])
				}
			} else if ev.Key == termbox.KeyCtrlR && allowRegexToggle {
				isRegex = !isRegex
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
		stack:  NewViewerStack(viewer),
		search: &SearchState{},
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

// ClearMessage clears the status message
func (a *App) ClearMessage() {
	a.statusMessage = ""
}

// filterChunkResult holds the result of filtering a chunk
type filterChunkResult struct {
	chunkIdx int
	lines    []string
	indices  []int // Original line indices
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

	query, ok := current.promptForInput(prompt)
	if ok && query != "" {
		lines := current.GetLines() // Get snapshot for thread-safety

		// Create new viewer immediately with loading state
		newViewer := &Viewer{
			lines:   nil,
			loading: true,
			topLine: 0,
			leftCol: 0,
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
					var chunkIndices []int
					for i := start; i < end; i++ {
						matches := strings.Contains(lines[i], query)
						if matches == keep {
							chunkLines = append(chunkLines, lines[i])
							chunkIndices = append(chunkIndices, i)
						}
					}
					resultChan <- filterChunkResult{chunkIdx, chunkLines, chunkIndices}
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

			for chunkIdx := 0; chunkIdx < numWorkers; chunkIdx++ {
				chunk := results[chunkIdx]
				for j, line := range chunk.lines {
					newViewer.mu.Lock()
					newViewer.lines = append(newViewer.lines, line)
					newViewer.mu.Unlock()

					origIdx := chunk.indices[j]
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

	query, ok := current.promptForInput("+")
	if ok && query != "" {
		original := a.stack.viewers[0]
		currentLines := current.GetLines()
		originalLines := original.GetLines()

		// Create new viewer immediately with loading state
		newViewer := &Viewer{
			lines:   nil,
			loading: true,
			topLine: 0,
			leftCol: 0,
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
					for i := start; i < end; i++ {
						if inCurrent[i] || strings.Contains(originalLines[i], query) {
							chunkLines = append(chunkLines, originalLines[i])
						}
					}
					resultChan <- appendChunkResult{chunkIdx, chunkLines}
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

			for chunkIdx := 0; chunkIdx < numWorkers; chunkIdx++ {
				chunk := results[chunkIdx]
				for _, line := range chunk.lines {
					newViewer.mu.Lock()
					newViewer.lines = append(newViewer.lines, line)
					if !foundCurrentLine && line == currentLine {
						foundCurrentLine = true
						newViewer.topLine = len(newViewer.lines) - 1
					}
					newViewer.mu.Unlock()

					lineCount++
					if lineCount <= 100 || lineCount%1000 == 0 {
						termbox.Interrupt()
					}
				}
			}

			newViewer.mu.Lock()
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

	query, isRegex, ok := current.promptForInputWithRegex(prompt, true)
	if ok && query != "" {
		lines := current.GetLines()
		lineIdx := a.search.Search(lines, query, current.topLine, backward, isRegex)
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

	// Determine if we should go forward (down) or backward (up) in the file
	goingUp := a.search.backward != reverse

	if goingUp {
		if a.search.AtStart() {
			a.ShowTempMessage("BOF")
		} else if lineIdx := a.search.Prev(); lineIdx >= 0 {
			a.stack.Current().topLine = lineIdx
		}
	} else {
		if a.search.AtEnd() {
			a.ShowTempMessage("EOF")
		} else if lineIdx := a.search.Next(); lineIdx >= 0 {
			a.stack.Current().topLine = lineIdx
		}
	}
}

// HandleStackNav navigates the viewer stack
// If reset is true (=), resets to first viewer; if false (^U), pops one level
func (a *App) HandleStackNav(reset bool) {
	current := a.stack.Current()
	currentLine := current.GetLine(current.topLine)

	var changed bool
	if reset {
		changed = a.stack.Reset()
	} else {
		changed = a.stack.Pop()
	}

	if changed && currentLine != "" {
		// Find this line in the new current viewer to stay on the same line
		newCurrent := a.stack.Current()
		newLines := newCurrent.GetLines()
		for i, line := range newLines {
			if line == currentLine {
				newCurrent.topLine = i
				break
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

	for screenY := 0; screenY < current.height; screenY++ {
		lineIndex := current.topLine + screenY
		if lineIndex >= lineCount {
			break
		}
		line := current.GetLine(lineIndex)
		cells := parseANSI(line)

		// Find search match positions in this line (on the raw text without ANSI)
		var matchPositions []bool
		if a.search.regex != nil {
			matchPositions = make([]bool, len(cells))
			// Strip ANSI for search matching
			plainText := make([]rune, len(cells))
			for i, c := range cells {
				plainText[i] = c.char
			}
			plainStr := string(plainText)
			// Use regex to find all matches
			matches := a.search.regex.FindAllStringIndex(plainStr, -1)
			for _, match := range matches {
				// Convert byte indices to rune indices
				startRune := len([]rune(plainStr[:match[0]]))
				endRune := len([]rune(plainStr[:match[1]]))
				for j := startRune; j < endRune && j < len(matchPositions); j++ {
					matchPositions[j] = true
				}
			}
		}

		screenX := 0
		for i, cell := range cells {
			if i < current.leftCol {
				continue
			}
			if screenX >= current.width {
				break
			}
			fg, bg := cell.fg, cell.bg
			// Highlight search matches
			if matchPositions != nil && i < len(matchPositions) && matchPositions[i] {
				fg = termbox.ColorBlack
				bg = termbox.ColorYellow
			}
			termbox.SetCell(screenX, screenY, cell.char, fg, bg)
			screenX++
		}
	}

	if a.statusMessage != "" && time.Now().Before(a.messageExpiry) {
		current.showMessage(a.statusMessage)
	} else {
		a.statusMessage = ""
		current.drawStatusBarWithDepth(len(a.stack.viewers))
		termbox.Flush()
	}
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
					return nil
				case 'j':
					current.navigateDown()
				case 'k':
					current.navigateUp()
				case 'h':
					current.navigateLeft(current.width / 4)
				case 'l':
					current.navigateRight(current.width / 4)
				case 'g':
					current.goToStart()
				case 'G':
					current.goToEnd()
				case ':':
					app.HandleGotoLine()
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
				}
			} else {
				switch ev.Key {
				case termbox.KeyArrowUp:
					current.navigateUp()
				case termbox.KeyArrowDown:
					current.navigateDown()
				case termbox.KeyArrowLeft:
					current.navigateLeft(current.width / 4)
				case termbox.KeyArrowRight:
					current.navigateRight(current.width / 4)
				case termbox.KeyPgdn, termbox.KeySpace:
					current.pageDown()
				case termbox.KeyPgup:
					current.pageUp()
				case termbox.KeyHome:
					current.goToStart()
				case termbox.KeyEnd:
					current.goToEnd()
				case termbox.KeyCtrlU:
					app.HandleStackNav(false)
				case termbox.KeyEsc, termbox.KeyCtrlC:
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

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cut <filename>")
		os.Exit(1)
	}

	viewer, err := NewViewer(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading file: %v\n", err)
		os.Exit(1)
	}

	if err := viewer.run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
