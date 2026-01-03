package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nsf/termbox-go"
)

type SimpleViewer struct {
	lines   []string // All lines from the file
	topLine int      // Index of the line at the top of the screen
	leftCol int      // Horizontal scroll offset
	width   int      // Terminal width
	height  int      // Terminal height
}

// ViewerStack manages a stack of viewers for filtering navigation
type ViewerStack struct {
	viewers []*SimpleViewer
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
	query   string // Current search query
	matches []int  // Line indices that match
	current int    // Current match index (-1 if none)
}

// Clear resets the search state
func (s *SearchState) Clear() {
	s.query = ""
	s.matches = nil
	s.current = -1
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

// Search performs a search and returns the first match line index or -1
func (s *SearchState) Search(lines []string, query string) int {
	s.query = query
	s.matches = nil
	s.current = -1

	for i, line := range lines {
		if strings.Contains(line, query) {
			s.matches = append(s.matches, i)
		}
	}

	if len(s.matches) > 0 {
		s.current = 0
		return s.matches[0]
	}
	return -1
}

func NewSimpleViewer(filename string) (*SimpleViewer, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	// Handle very long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return &SimpleViewer{
		lines:   lines,
		topLine: 0,
		leftCol: 0,
	}, nil
}

// NewSimpleViewerFromLines creates a SimpleViewer from an existing slice of lines
func NewSimpleViewerFromLines(lines []string) *SimpleViewer {
	return &SimpleViewer{
		lines:   lines,
		topLine: 0,
		leftCol: 0,
	}
}

func (v *SimpleViewer) draw() {
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	// Draw visible lines
	for screenY := 0; screenY < v.height; screenY++ {
		lineIndex := v.topLine + screenY

		// Check if we've run out of lines
		if lineIndex >= len(v.lines) {
			break
		}

		line := v.lines[lineIndex]
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

func (v *SimpleViewer) drawStatusBar() {
	v.drawStatusBarWithDepth(1)
}

func (v *SimpleViewer) drawStatusBarWithDepth(depth int) {
	statusY := v.height
	var status string
	if depth > 1 {
		status = fmt.Sprintf(" Line %d/%d | Col %d | Depth %d | ^U:back =:reset q:quit ",
			v.topLine+1, len(v.lines), v.leftCol, depth)
	} else {
		status = fmt.Sprintf(" Line %d/%d | Col %d | Press 'q' to quit ",
			v.topLine+1, len(v.lines), v.leftCol)
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
func (v *SimpleViewer) showMessage(msg string) {
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

func (v *SimpleViewer) navigateUp() {
	if v.topLine > 0 {
		v.topLine--
	}
}

func (v *SimpleViewer) navigateDown() {
	maxTop := len(v.lines) - 2
	if maxTop < 0 {
		maxTop = 0
	}
	if v.topLine < maxTop {
		v.topLine++
	}
}

func (v *SimpleViewer) navigateLeft(amount int) {
	newValue := v.leftCol - amount
	if newValue < 0 {
		newValue = 0
	}
	v.leftCol = newValue
}

func (v *SimpleViewer) navigateRight(amount int) {
	v.leftCol += amount
}

func (v *SimpleViewer) pageDown() {
	v.topLine += v.height
	// Allow scrolling until last line is at top
	maxTop := len(v.lines) - 2
	if maxTop < 0 {
		maxTop = 0
	}
	if v.topLine > maxTop {
		v.topLine = maxTop
	}
}

func (v *SimpleViewer) pageUp() {
	v.topLine -= v.height
	if v.topLine < 0 {
		v.topLine = 0
	}
}

func (v *SimpleViewer) goToStart() {
	v.topLine = 0
}

func (v *SimpleViewer) goToEnd() {
	// Go to last line at top
	v.topLine = len(v.lines) - 2
	if v.topLine < 0 {
		v.topLine = 0
	}
}

func (v *SimpleViewer) resize(width, height int) {
	v.width = width
	v.height = height - 1 // Reserve one line for status bar
}

// promptForInput shows a prompt at the bottom line and collects user input
func (v *SimpleViewer) promptForInput(prompt string) (string, bool) {
	input := ""

	for {
		// Draw the prompt line at the bottom
		statusY := v.height
		line := prompt + input

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
			v.resize(ev.Width, ev.Height)
			v.draw()
		}
	}
}

// filterLines returns all lines containing the query string
func (v *SimpleViewer) filterLines(query string) []string {
	var filtered []string
	for _, line := range v.lines {
		if strings.Contains(line, query) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

// excludeLines returns all lines NOT containing the query string
func (v *SimpleViewer) excludeLines(query string) []string {
	var filtered []string
	for _, line := range v.lines {
		if !strings.Contains(line, query) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

// NewViewerStack creates a new ViewerStack with the initial viewer
func NewViewerStack(initial *SimpleViewer) *ViewerStack {
	return &ViewerStack{
		viewers: []*SimpleViewer{initial},
	}
}

// Current returns the current (top) viewer
func (s *ViewerStack) Current() *SimpleViewer {
	return s.viewers[len(s.viewers)-1]
}

// Push adds a new viewer to the stack
func (s *ViewerStack) Push(v *SimpleViewer) {
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

// Depth returns the number of viewers in the stack
func (s *ViewerStack) Depth() int {
	return len(s.viewers)
}

// First returns the first (original) viewer in the stack
func (s *ViewerStack) First() *SimpleViewer {
	return s.viewers[0]
}

// NewApp creates a new App with the given viewer
func NewApp(viewer *SimpleViewer) *App {
	return &App{
		stack:  NewViewerStack(viewer),
		search: &SearchState{},
	}
}

// Current returns the current viewer
func (a *App) Current() *SimpleViewer {
	return a.stack.Current()
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

// HandleFilterKeep filters keeping matching lines
func (a *App) HandleFilterKeep() {
	current := a.Current()
	query, ok := current.promptForInput("&")
	if ok && query != "" {
		filtered := current.filterLines(query)
		if len(filtered) > 0 {
			a.stack.Push(NewSimpleViewerFromLines(filtered))
			a.search.Clear()
		}
	}
}

// HandleFilterExclude filters excluding matching lines
func (a *App) HandleFilterExclude() {
	current := a.Current()
	query, ok := current.promptForInput("-")
	if ok && query != "" {
		filtered := current.excludeLines(query)
		if len(filtered) > 0 {
			a.stack.Push(NewSimpleViewerFromLines(filtered))
			a.search.Clear()
		}
	}
}

// HandleFilterAppend appends matching lines from original
func (a *App) HandleFilterAppend() {
	current := a.Current()
	query, ok := current.promptForInput("+")
	if ok && query != "" {
		original := a.stack.First()

		currentCounts := make(map[string]int)
		for _, line := range current.lines {
			currentCounts[line]++
		}

		var combined []string
		for _, line := range original.lines {
			if currentCounts[line] > 0 {
				combined = append(combined, line)
				currentCounts[line]--
			} else if strings.Contains(line, query) {
				combined = append(combined, line)
			}
		}

		if len(combined) > 0 {
			a.stack.Push(NewSimpleViewerFromLines(combined))
			a.search.Clear()
		}
	}
}

// HandleSearch performs a search
func (a *App) HandleSearch() {
	current := a.Current()
	query, ok := current.promptForInput("/")
	if ok && query != "" {
		if lineIdx := a.search.Search(current.lines, query); lineIdx >= 0 {
			current.topLine = lineIdx
		}
	}
}

// HandleSearchNext goes to next search result
func (a *App) HandleSearchNext() {
	if a.search.HasResults() {
		if a.search.AtEnd() {
			a.ShowTempMessage("EOF")
		} else if lineIdx := a.search.Next(); lineIdx >= 0 {
			a.Current().topLine = lineIdx
		}
	}
}

// HandleSearchPrev goes to previous search result
func (a *App) HandleSearchPrev() {
	if a.search.HasResults() {
		if a.search.AtStart() {
			a.ShowTempMessage("BOF")
		} else if lineIdx := a.search.Prev(); lineIdx >= 0 {
			a.Current().topLine = lineIdx
		}
	}
}

// HandleReset resets to first viewer
func (a *App) HandleReset() {
	a.stack.Reset()
	a.search.Clear()
}

// HandlePop pops current viewer
func (a *App) HandlePop() {
	a.stack.Pop()
	a.search.Clear()
}

// Draw renders the current view
func (a *App) Draw() {
	current := a.Current()
	current.resize(termbox.Size())
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	for screenY := 0; screenY < current.height; screenY++ {
		lineIndex := current.topLine + screenY
		if lineIndex >= len(current.lines) {
			break
		}
		line := current.lines[lineIndex]
		runes := []rune(line)
		screenX := 0
		for i, char := range runes {
			if i < current.leftCol {
				continue
			}
			if screenX >= current.width {
				break
			}
			termbox.SetCell(screenX, screenY, char, termbox.ColorDefault, termbox.ColorDefault)
			screenX++
		}
	}

	if a.statusMessage != "" && time.Now().Before(a.messageExpiry) {
		current.showMessage(a.statusMessage)
	} else {
		a.statusMessage = ""
		current.drawStatusBarWithDepth(a.stack.Depth())
		termbox.Flush()
	}
}

func (v *SimpleViewer) run() error {
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
		current := app.Current()

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
					current.navigateLeft(current.width / 3)
				case 'l':
					current.navigateRight(current.width / 3)
				case 'g':
					current.goToStart()
				case 'G':
					current.goToEnd()
				case '&':
					app.HandleFilterKeep()
				case '-':
					app.HandleFilterExclude()
				case '+':
					app.HandleFilterAppend()
				case '/':
					app.HandleSearch()
				case 'n':
					app.HandleSearchNext()
				case 'N':
					app.HandleSearchPrev()
				case '=':
					app.HandleReset()
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
					current.navigateLeft(current.width / 3)
				case termbox.KeyArrowRight:
					current.navigateRight(current.width / 3)
				case termbox.KeyPgdn, termbox.KeySpace:
					current.pageDown()
				case termbox.KeyPgup:
					current.pageUp()
				case termbox.KeyHome:
					current.goToStart()
				case termbox.KeyEnd:
					current.goToEnd()
				case termbox.KeyCtrlU:
					app.HandlePop()
				case termbox.KeyEsc, termbox.KeyCtrlC:
					return nil
				}
			}
			app.Draw()

		case termbox.EventResize:
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
		fmt.Println("Usage: simple-viewer <filename>")
		os.Exit(1)
	}

	viewer, err := NewSimpleViewer(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading file: %v\n", err)
		os.Exit(1)
	}

	if err := viewer.run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
