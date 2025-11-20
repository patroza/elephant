# VSCode Recent Projects

Opens recently opened VSCode projects and workspaces.

## Configuration

```yaml
vscode:
  icon: vscode
  min_score: 20
  history: true
  history_when_empty: false
  db_path: ~/.config/Code/User/globalStorage/state.vscdb
  code_command: code
  max_entries: 50
```

## Config options

### db_path

Path to the VSCode state database. Defaults to `~/.config/Code/User/globalStorage/state.vscdb`.

For VSCode Insiders, use `~/.config/Code - Insiders/User/globalStorage/state.vscdb`.

### code_command

Command to launch VSCode. Defaults to `code`.

For VSCode Insiders, use `code-insiders`.

### max_entries

Maximum number of recent entries to show. Defaults to 50.

### history

Make use of history for sorting. Defaults to `true`.

### history_when_empty

Consider history when query is empty. Defaults to `false`.

### min_score

Minimum score for items to be shown. Defaults to 20.

### icon

Icon to use for items. Defaults to `vscode`.

## Actions

- `open`: Opens the project in VSCode (default)
- `delete_history`: Deletes the item from elephant's history
