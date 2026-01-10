# CUT

An in-memory file viewer with powerful filtering capabilities for navigating large log files.

**Inspired by [slit](https://github.com/tigrawap/slit)** - a modern pager for viewing logs.

## Features

- **In-Memory Filtering**: Filter logs with `&` (keep), `-` (exclude), `+` (add from original)
- **Filter Stacking**: Chain multiple filters and navigate back through filter history
- **Multi-File Merge**: Open multiple files, merge-sorted by timestamp
- **Follow Mode**: Like `tail -f`, auto-scroll as files grow
- **Search**: Forward (`/`) and backward (`?`) search with regex and case-insensitive options
- **Timestamp Jump**: Jump to specific timestamps in logs
- **Visual Selection**: Select and copy lines to clipboard
- **JSON Pretty-Print**: Auto-format JSON embedded in log lines
- **Word Wrap**: Toggle word wrap for long lines
- **ANSI Color Support**: Renders colored log output correctly
- **Sticky Left Columns**: Keep timestamps visible while scrolling horizontally
- **Export**: Save filtered view to a file

## Installation

### From Source

Requires Go 1.18+

```bash
git clone https://github.com/yourusername/cut.git
cd cut
go build
```

The binary will be created as `./cut`

### Move to PATH (optional)

```bash
sudo mv cut /usr/local/bin/
```

## Usage

```bash
# View a single file
cut logfile.log

# View multiple files (merged by timestamp)
cut app1.log app2.log app3.log

# Follow mode (like tail -f)
cut -f logfile.log

# Read from stdin
cat logfile.log | cut
kubectl logs pod-name | cut
```

## Keybindings

### Navigation
| Key | Action |
|-----|--------|
| `j` / `↓` | Move down |
| `k` / `↑` | Move up |
| `h` / `←` | Scroll left |
| `l` / `→` | Scroll right |
| `g` / `Home` | Go to first line |
| `G` / `End` | Go to last line |
| `Space` / `PgDn` | Page down |
| `PgUp` | Page up |
| `:<number>` | Go to line number |

### Search
| Key | Action |
|-----|--------|
| `/` | Search forward |
| `?` | Search backward |
| `n` | Next match |
| `N` | Previous match |
| `Ctrl+R` | Toggle regex (in prompt) |
| `Ctrl+I` | Toggle case-insensitive (in prompt) |

### Filtering
| Key | Action |
|-----|--------|
| `&` | Keep lines matching pattern |
| `-` | Exclude lines matching pattern |
| `+` | Add matching lines from original |
| `=` | Reset to original file |
| `u` / `Ctrl+U` | Pop last filter |

### Display
| Key | Action |
|-----|--------|
| `w` | Toggle word wrap |
| `f` | Toggle JSON pretty-print |
| `F` | Toggle follow mode |
| `K` | Set sticky left columns |

### Other
| Key | Action |
|-----|--------|
| `v` | Visual selection mode |
| `y` | Yank (copy) selection |
| `;` | Export to file |
| `t` | Set timestamp format |
| `b` | Jump to timestamp |
| `H` / `F1` | Show help |
| `q` / `Esc` | Quit |

## Examples

### Filtering Workflow

```
# Open a large log file
cut application.log

# Press & and type "ERROR" to keep only error lines
# Press - and type "timeout" to exclude timeout errors
# Press + and type "FATAL" to also show fatal errors from original
# Press = to reset and see all lines again
```

### Multi-File Log Correlation

```bash
# View logs from multiple services, merged by timestamp
cut frontend.log backend.log database.log

# Each line is prefixed with file index:
# 0> 2024-01-15 10:00:01 Frontend request received
# 1> 2024-01-15 10:00:02 Backend processing
# 2> 2024-01-15 10:00:03 Database query executed
```

## Command Line Options

```
-f, --follow    Follow mode (like tail -f)
-h, --help      Show help message
    --version   Show version
```

## License

MIT

## Acknowledgments

- Inspired by [slit](https://github.com/tigrawap/slit) by tigrawap
- Built with [termbox-go](https://github.com/nsf/termbox-go)
