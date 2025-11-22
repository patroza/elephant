# VSCode Projects Provider

The `vscodeprojects` provider exposes the list of recently opened VS Code folders / workspaces, allowing quick fuzzy search and opening of a project directly from Elephant.

## Features
- Lists recent VS Code projects (folders & workspaces)
- Fuzzy search by folder name or full path
- Open the selected project in VS Code
- Lightweight caching; reloads when VS Code updates its state database

## Source of Truth
Data is read from the SQLite database at:
```
~/.config/Code/User/globalStorage/state.vscdb
```
Rows are taken from the `ItemTable` where the key contains `recent` (fallback) or matches the known `history.recentlyOpenedPathsList` key. The value is JSON.

## Configuration
| Key | Description | Default |
| --- | --- | --- |
| `icon` | Icon name to use for entries | `visual-studio-code` |
| `min_score` | Minimum fuzzy score threshold | `20` |
| `command` | VS Code command (e.g. `code`, `code-insiders`) | `code` |
| `db_path` | Override path to the VS Code state DB | auto-detected |
| `max_entries` | Maximum number of recent entries to show | `100` |

Example (in `providers.toml`):
```toml
[vscodeprojects]
icon = "visual-studio-code"
command = "code-insiders"
min_score = 15
max_entries = 200
```

## Actions
| Action | Description |
| ------ | ----------- |
| `open` | Open project in VS Code |
| `reveal` | Reveal project folder with `xdg-open` |

## Notes
- If the database or key is missing, provider is marked unavailable.
- Values are parsed leniently; unknown JSON formats just emit raw path strings.
- For very large histories only the first `max_entries` are used.

## TODO / Future Ideas
- Support multi-root workspaces (`.code-workspace` files)
- Show last opened time
- Add action to remove an entry
