-- VSCode Recent Projects Lua Menu for Elephant
-- Reads the VS Code state SQLite DB and exposes recent projects.
-- Mirrors functionality of the Go provider but runs as a Lua menu.

Name = "vscodeprojects"
NamePretty = "VSCode Projects"
Icon = "visual-studio-code"
Description = "Recent VS Code folders / workspaces"
Cache = true -- cache between empty queries to avoid constant sqlite calls
SearchName = true

local home = os.getenv("HOME") or ""
local db_path = home .. "/.config/Code/User/globalStorage/state.vscdb"

-- Attempt to get list JSON via sqlite3. Returns raw JSON string or nil.
local function read_recent_json()
    local f = io.open(db_path, "r")
    if not f then return nil end
    f:close()

    local cmd = "sqlite3 '" .. db_path .. "' " ..
        "\"SELECT value FROM ItemTable WHERE key='history.recentlyOpenedPathsList'\" 2>/dev/null"
    local handle = io.popen(cmd)
    if not handle then return nil end
    local data = handle:read("*a")
    handle:close()
    if data == '' then return nil end
    return data
end

-- Minimal JSON array/object extractor for our expected structure.
-- We only need folderUri/fileUri/workspace.configPath and label.
-- If jq is available we prefer using it for robustness.
local function parse_entries()
    local entries = {}
    local json = read_recent_json()
    if not json then return entries end

    -- Prefer jq if installed.
    local jq_test = os.execute("command -v jq >/dev/null 2>&1")
    if jq_test == 0 then
        local jq_cmd = "sqlite3 '" .. db_path .. "' \"SELECT value FROM ItemTable WHERE key='history.recentlyOpenedPathsList'\" | jq -r '.entries[] | @base64' 2>/dev/null"
        local h = io.popen(jq_cmd)
        if h then
            for line in h:lines() do
                -- decode base64 JSON entry
                local entry_json = io.popen("printf '%s' '" .. line .. "' | base64 -d 2>/dev/null")
                if entry_json then
                    local raw = entry_json:read("*a")
                    entry_json:close()
                    -- Extract fields with jq again for safety
                    local label = io.popen("printf '%s' '" .. raw .. "' | jq -r '.label // empty' 2>/dev/null")
                    local folderUri = io.popen("printf '%s' '" .. raw .. "' | jq -r '.folderUri // empty' 2>/dev/null")
                    local fileUri = io.popen("printf '%s' '" .. raw .. "' | jq -r '.fileUri // empty' 2>/dev/null")
                    local wsConfig = io.popen("printf '%s' '" .. raw .. "' | jq -r '.workspace.configPath // empty' 2>/dev/null")
                    local lbl = label and label:read("*l") or ""
                    local folder = folderUri and folderUri:read("*l") or ""
                    local file = fileUri and fileUri:read("*l") or ""
                    local workspace = wsConfig and wsConfig:read("*l") or ""
                    if label then label:close() end
                    if folderUri then folderUri:close() end
                    if fileUri then fileUri:close() end
                    if wsConfig then wsConfig:close() end

                    local chosen = folder ~= '' and folder or (file ~= '' and file or workspace)
                    if chosen ~= '' then
                        local path = chosen:gsub("^file://", "")
                        local base = path:match("([^/]+)$") or path
                        local kind = folder ~= '' and 'folder' or (file ~= '' and 'file' or 'workspace')
                        local branch = ''
                        -- git branch (silent fail)
                        local git_dir = path
                        -- If file, use directory
                        local attr = io.popen("stat -c %F '" .. path .. "' 2>/dev/null")
                        if attr then
                            local t = attr:read("*l") or ''
                            attr:close()
                            if not t:match("directory") then
                                git_dir = path:gsub("/[^/]+$", "")
                            end
                        end
                        local head_cmd = "bash -c 'd=\"" .. git_dir .. "\"; for i in {1..6}; do if [ -f \"$d/.git/HEAD\" ]; then cat \"$d/.git/HEAD\"; break; fi; nd=\"$(dirname \"$d\")\"; [ \"$nd\" = \"$d\" ] && break; d=\"$nd\"; done'"
                        local head = io.popen(head_cmd)
                        if head then
                            local headContent = head:read("*l") or ''
                            head:close()
                            if headContent:match("ref:") then
                                branch = headContent:gsub(".*refs/heads/", "")
                            elseif headContent ~= '' then
                                branch = "detached:" .. headContent:sub(1,7)
                            end
                        end
                        local subtext = path
                        if branch ~= '' then
                            subtext = subtext .. " [" .. branch .. "]"
                        end

                        local id = base
                        if id:match("%.code%-workspace$") then
                            id = id:gsub("%.code%-workspace$", "") .. " \\(Workspace\\)"
                        end
                        local actionStart = string.format("omarchy-launch-or-focus \"%s - Visual Studio Code\" \"code '%s'\"", id, path)
                        local revealDir = path
                        -- If file (workspace file etc.), reveal its parent directory
                        local attr2 = io.popen("stat -c %F '" .. path .. "' 2>/dev/null")
                        if attr2 then
                            local t2 = attr2:read("*l") or ''
                            attr2:close()
                            if not t2:match("directory") then
                                revealDir = path:gsub("/[^/]+$", "")
                            end
                        end
                        local actionReveal = string.format("xdg-open '%s'", revealDir)
                        table.insert(entries, {
                            Text = lbl ~= '' and lbl or base,
                            Subtext = subtext,
                            Value = path,
                            Actions = { start = actionStart, reveal = actionReveal },
                            Icon = Icon,
                        })
                    end
                end
            end
            h:close()
        end
        return entries
    end

    -- Fallback naive pattern parser: look for objects with folderUri/fileUri/workspace
    for obj in json:gmatch('{(.-)}') do
        local folder = obj:match('"folderUri"%s*:%s*"(.-)"') or ''
        local file = obj:match('"fileUri"%s*:%s*"(.-)"') or ''
        local wsConfig = obj:match('"configPath"%s*:%s*"(.-)"') or ''
        local label = obj:match('"label"%s*:%s*"(.-)"') or ''
        local chosen = folder ~= '' and folder or (file ~= '' and file or wsConfig)
        if chosen ~= '' then
            local path = chosen:gsub('^file://', '')
            local base = path:match('([^/]+)$') or path
            local id = base
            if id:match("%.code%-workspace$") then
                id = id:gsub("%.code%-workspace$", "") .. " \\(Workspace\\)"
            end
            -- git branch (silent fail) fallback
            local git_dir = path
            local attr = io.popen("stat -c %F '" .. path .. "' 2>/dev/null")
            if attr then
                local t = attr:read("*l") or ''
                attr:close()
                if not t:match("directory") then
                    git_dir = path:gsub("/[^/]+$", "")
                end
            end
            local branch = ''
            local head_cmd = "bash -c 'd=\"" .. git_dir .. "\"; for i in {1..6}; do if [ -f \"$d/.git/HEAD\" ]; then cat \"$d/.git/HEAD\"; break; fi; nd=\"$(dirname \"$d\")\"; [ \"$nd\" = \"$d\" ] && break; d=\"$nd\"; done'"
            local head = io.popen(head_cmd)
            if head then
                local headContent = head:read("*l") or ''
                head:close()
                if headContent:match("ref:") then
                    branch = headContent:gsub(".*refs/heads/", "")
                elseif headContent ~= '' then
                    branch = "detached:" .. headContent:sub(1,7)
                end
            end
            local subtext = path
            if branch ~= '' then
                subtext = subtext .. " [" .. branch .. "]"
            end
            local actionStart = string.format("omarchy-launch-or-focus \"%s - Visual Studio Code\" \"code '%s'\"", id, path)
            local revealDir = path
            local attr2 = io.popen("stat -c %F '" .. path .. "' 2>/dev/null")
            if attr2 then
                local t2 = attr2:read("*l") or ''
                attr2:close()
                if not t2:match("directory") then
                    revealDir = path:gsub("/[^/]+$", "")
                end
            end
            local actionReveal = string.format("xdg-open '%s'", revealDir)
            table.insert(entries, {
                Text = label ~= '' and label or base,
                Subtext = subtext,
                Value = path,
                Actions = { start = actionStart, reveal = actionReveal },
                Icon = Icon,
            })
        end
    end
    return entries
end

function GetEntries()
    return parse_entries()
end
