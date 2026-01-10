# CUT Developer Documentation

This document explains the data structures, their relationships, and how data flows through the application.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                           App                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ ViewerStack │  │ SearchState │  │       History       │  │
│  │  []*Viewer  │  │   matches   │  │ entries (persisted) │  │
│  └──────┬──────┘  └─────────────┘  └─────────────────────┘  │
│         │                                                   │
│    ┌────┴────┬────────┬────────┐                            │
│    ▼         ▼        ▼        ▼                            │
│ Viewer₀  Viewer₁  Viewer₂  Viewer₃  (stack grows right →)   │
│ (file)   (filter) (filter) (filter)                         │
└─────────────────────────────────────────────────────────────┘
```

## Core Data Structures

### `App`
The root container holding all application state.

```go
type App struct {
    stack           *ViewerStack  // Stack of viewers for filter navigation
    search          *SearchState  // Current search query and results
    history         *History      // Shared history for filters and searches
    statusMessage   string        // Temporary message displayed in status bar
    messageExpiry   time.Time     // When to clear the status message
    timestampFormat string        // User-defined timestamp format for 't'/'b' commands
    visualMode      bool          // Visual selection mode active
    visualStart     int           // Starting line of visual selection
    visualCursor    int           // Current cursor position in visual mode
}
```

**Lifecycle:**
- Created once at startup via `NewApp(viewer)`
- Lives for the entire application session
- Never replaced, only its fields are mutated

---

### `ViewerStack`
Manages the stack of viewers for filter drill-down and navigation.

```go
type ViewerStack struct {
    viewers []*Viewer  // Index 0 is always the original file
}
```

**Operations:**
| Operation | Trigger | Effect |
|-----------|---------|--------|
| `Push(v)` | `&`, `-`, `+` filters | Adds new filtered viewer to top |
| `Pop()` | `Ctrl+U` | Removes top viewer, returns to previous |
| `Reset()` | `=` | Removes all except index 0 (original file) |
| `Current()` | Every operation | Returns `viewers[len-1]` |

**Invariants:**
- Always has at least one viewer (the original file)
- `viewers[0]` is always the unfiltered original file
- Only `Current()` (top of stack) is displayed

---

### `Viewer`
Represents a view of lines (either a file or filtered subset).

```go
type Viewer struct {
    // Content
    lines         []string      // The actual line content
    originIndices []int         // Maps line[i] → index in parent viewer
    
    // Loading state
    mu      sync.RWMutex  // Protects lines during async loading
    loading bool          // True while background loading in progress
    
    // Display state
    topLine       int           // First visible line index
    topLineOffset int           // Row offset within expanded line (wrap/JSON)
    leftCol       int           // Horizontal scroll offset
    width, height int           // Terminal dimensions
    
    // Display modes
    wordWrap   bool  // Wrap long lines
    jsonPretty bool  // Pretty-print JSON
    stickyLeft int   // Number of left chars to keep visible when scrolling (K)
    follow     bool  // Follow mode (like tail -f)
    
    // Performance
    expandedCache    map[int]int  // lineIdx → screen row count
    expandedCacheKey string       // Invalidation key
    
    // Metadata
    filename string  // Source filename (empty for filtered views)
}
```

**Construction:**

| Constructor | Source | `originIndices` | `loading` |
|-------------|--------|-----------------|-----------|
| `NewViewer(filename)` | File on disk | `nil` | `true` initially |
| `NewViewerFromLines([]string)` | Test data | `nil` | `false` |
| Filter operations | Parent viewer | Populated | `true` → `false` |

**The `originIndices` Field:**

This is the key to efficient navigation when popping filters. For a filtered viewer:

```
Parent Viewer (lines):     [A, B, C, D, E, F, G, H]
                            0  1  2  3  4  5  6  7

Filter "contains D or G":

Child Viewer (lines):      [D, G]
                            0  1
Child originIndices:       [3, 7]  ← maps back to parent indices
```

When you press `Ctrl+U` at line 1 (showing "G"), we look up `originIndices[1] = 7` and binary search to position at line 7 in the parent.

**Background Loading Flow:**

```
NewViewer(filename)
    │
    ├──► Returns immediately with loading=true, lines=nil
    │
    └──► Spawns goroutine:
              │
              ├── Read 10,000 lines into batch
              ├── Lock mutex, append batch to lines, unlock
              ├── termbox.Interrupt() to trigger redraw
              └── Repeat until EOF
              │
              └── Set loading=false, final Interrupt()
```

---

### `SearchState`
Tracks current search query and cached results.

```go
type SearchState struct {
    query      string          // Raw search string
    regex      *regexp.Regexp  // Compiled pattern (if regex mode)
    isRegex    bool            // Regex mode enabled
    ignoreCase bool            // Case-insensitive mode
    matches    []int           // Sorted line indices that match
    current    int             // Index into matches (-1 = none selected)
    backward   bool            // Search direction (? vs /)
}
```

**State Transitions:**

```
                    ┌─────────────────┐
      Search(/?)    │                 │
    ───────────────►│ query set       │
                    │ matches filled  │
                    │ current = first │
    ◄───────────────│ match near pos  │
       Found        └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
           n (next)     N (prev)      Filter op
              │              │              │
              ▼              ▼              ▼
         current++      current--      Clear()
```

**Cleared when:** Any filter operation (`&`, `-`, `+`, `=`, `Ctrl+U`)

---

### `History`
Persistent storage for filter and search queries.

```go
type History struct {
    entries   []string  // Past queries (newest at end)
    index     int       // Navigation cursor (-1 = new input)
    tempInput string    // Saves current input when navigating
    filename  string    // Persistence file path
}
```

**Persistence:**
- File: `/tmp/sieve_history`
- Format: Newline-separated entries
- Max entries: 100 (oldest trimmed on save)
- Loaded at startup, saved after each `Add()`

**Navigation Flow (↑/↓ in prompt):**

```
User typing: "error"     index=-1, tempInput=""
                              │
Press ↑                       ▼
                         index=len-1, tempInput="error"
                         return entries[index]
                              │
Press ↑                       ▼
                         index-- (if > 0)
                         return entries[index]
                              │
Press ↓                       ▼
                         index++ 
                         if index >= len: return tempInput
```

---

## Data Flow Diagrams

### File Loading

```
main()
  │
  ▼
NewViewer(filename) ──────────────────────────────────────┐
  │                                                       │
  ▼                                                       │
NewApp(viewer) ──► ViewerStack{viewers: [viewer]}         │
  │                                                       │
  ▼                                                       │
viewer.run() ──► termbox event loop                       │
  │                                                       │
  │     ┌─────────────────────────────────────────────────┘
  │     │ Background goroutine
  │     ▼
  │   Read file in batches
  │     │
  │     ├── Lock mutex
  │     ├── Append to viewer.lines
  │     ├── Unlock mutex
  │     ├── termbox.Interrupt()
  │     │         │
  │     │         ▼
  │◄────┼─── EventInterrupt ──► app.Draw()
  │     │
  │     └── (repeat until EOF)
```

### Filter Operation

```
User presses '&', enters "error"
         │
         ▼
HandleFilter(keep=true)
         │
         ├── Get lines from current viewer
         │
         ├── Create newViewer{loading: true, lines: nil}
         │
         ├── stack.Push(newViewer)  ← UI now shows empty viewer
         │
         └── Spawn goroutine:
                   │
                   ├── Divide lines into 8 chunks
                   │
                   ├── Spawn 8 worker goroutines
                   │   each returns {lines, indices} for its chunk
                   │
                   ├── Collect results in order
                   │
                   ├── For each matched line:
                   │   ├── Append to newViewer.lines
                   │   ├── Append to allIndices
                   │   └── termbox.Interrupt() (periodically)
                   │
                   └── Set newViewer.originIndices = allIndices
                       Set newViewer.loading = false
```

### Stack Navigation (Pop/Reset)

```
User at Viewer₃, topLine=5
         │
         ▼
HandleStackNav(reset=false)  ← Ctrl+U
         │
         ├── Look up: originIndices[5] = 47
         │   (line 5 in Viewer₃ came from line 47 in Viewer₂)
         │
         ├── stack.Pop()  ← removes Viewer₃
         │
         └── Binary search Viewer₂.originIndices for 47
             Set Viewer₂.topLine to found index
```

For `reset=true` (`=`), we trace through ALL viewers to find the original line number in Viewer₀.

### Rendering with Word Wrap / JSON

```
app.Draw()
    │
    ├── wordWrap=true? ──► drawWrapped()
    │                           │
    │                           ├── For each logical line:
    │                           │   └── Split into chunks of width
    │                           │       (respecting topLineOffset)
    │                           │
    └── wordWrap=false? ─► drawNormal()
                                │
                                ├── jsonPretty=true?
                                │   └── formatJSON() → multiple screen lines
                                │
                                └── Render with ANSI color parsing

Navigation (j/k) adjusts:
  - topLine (logical line index)
  - topLineOffset (row within expanded line)

expandedCache stores: lineIdx → total screen rows
  (invalidated when width/modes change)
```

---

## Thread Safety

| Field | Protected By | Accessed From |
|-------|--------------|---------------|
| `Viewer.lines` | `Viewer.mu` | Main thread (read), Loader goroutine (write) |
| `Viewer.loading` | `Viewer.mu` | Main thread (read), Loader goroutine (write) |
| `Viewer.originIndices` | `Viewer.mu` | Set once by filter goroutine, then read-only |
| All other fields | Main thread only | Single-threaded access |

**Pattern:**
- Background loaders acquire lock, batch-append, release lock
- Main thread uses `GetLine()`, `GetLines()`, `LineCount()` which acquire read locks
- `termbox.Interrupt()` signals main thread to redraw with latest data

---

## Key Constants

| Constant | Value | Purpose |
|----------|-------|---------|
| `batchSize` (file load) | 10,000 | Lines per mutex lock during file load |
| `numWorkers` (filter) | 8 | Parallel workers for filtering |
| `maxHistoryEntries` | 100 | Max persisted history entries |
| `version` | "1.0.0" | Application version |

---

## Additional Features

### Multi-File Support

When multiple files are passed as arguments, they are merged by timestamp:

```go
type fileStream struct {
    scanner   *bufio.Scanner  // File scanner
    file      *os.File        // Open file handle
    fileIdx   int             // Index (0, 1, 2...)
    prefix    string          // Line prefix ("0> ", "1> ", etc.)
    currLine  string          // Current buffered line
    currTime  time.Time       // Parsed timestamp
    hasTime   bool            // Whether timestamp was found
    exhausted bool            // EOF reached
}
```

**K-way Merge Algorithm:**
1. Open all files, read first line from each
2. Pick stream with oldest timestamp (or no timestamp = priority)
3. Add line to viewer, advance that stream
4. Repeat until all streams exhausted

### Follow Mode

When `follow=true` (via `-f` flag or `F` key):
- Background goroutine polls file every 100ms
- New lines are appended to viewer
- If user is at bottom, auto-scrolls to show new content
- Stops following if user scrolls up

### Visual Mode

Activated by pressing `v`:
- `visualStart`: Line where selection began
- `visualCursor`: Current cursor position
- Navigation moves cursor, window scrolls when cursor hits edges
- `y` yanks (copies) selected lines to clipboard via `pbcopy`/`xclip`

### Timestamp Search

- `t` sets `timestampFormat` (Python datetime syntax)
- `b` prompts for timestamp (`[yymmdd]hhmmss`)
- Auto-detects format from common patterns if not set
- Jumps to first line with timestamp >= input

### Sticky Left Columns

When `stickyLeft > 0`:
- First N characters always visible when scrolling horizontally
- Displayed in pastel blue color
- Useful for keeping timestamps visible while viewing long lines
