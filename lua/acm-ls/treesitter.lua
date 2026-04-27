-- Treesitter parser checks.
-- The plugin's queries assume tree-sitter-yaml and tree-sitter-go-template
-- (gotmpl) are installed. Helm-specific queries are optional but nice if
-- tree-sitter-helm is present.
--
-- Detection has to work in three setups:
--   1. Standard install: parser .so under <rtp>/parser/<lang>.so
--   2. nvim-treesitter with custom parser_install_dir (e.g. noexec /home
--      systems where parsers must live on a different filesystem)
--   3. Manual `vim.treesitter.language.add(lang, { path = ... })`
-- We try each in turn and only warn if every probe fails.

local M = {}

local required = { "yaml", "gotmpl" }
local optional = { "helm" }

local function check_runtime_file(lang)
  local matches = vim.api.nvim_get_runtime_file("parser/" .. lang .. ".so", false)
  if #matches > 0 then return true end
  matches = vim.api.nvim_get_runtime_file("parser/" .. lang .. ".dll", false)
  return #matches > 0
end

local function check_nvim_treesitter(lang)
  local ok, parsers = pcall(require, "nvim-treesitter.parsers")
  if not ok or parsers == nil then return false end
  -- Newer API.
  if type(parsers.has_parser) == "function" then
    local hp_ok, hp = pcall(parsers.has_parser, lang)
    if hp_ok and hp then return true end
  end
  -- Older API exposed configs but no install-state probe; rely on language.add
  -- below for those versions.
  return false
end

local function check_language_add(lang)
  if not (vim.treesitter and vim.treesitter.language and vim.treesitter.language.add) then
    return false
  end
  -- vim.treesitter.language.add succeeds (returns nil without error) when the
  -- parser is loadable from anywhere Neovim knows about — runtimepath, or a
  -- path previously registered by nvim-treesitter / language.add.
  local ok = pcall(vim.treesitter.language.add, lang)
  return ok
end

local function parser_available(lang)
  return check_runtime_file(lang)
      or check_nvim_treesitter(lang)
      or check_language_add(lang)
end

-- Check returns (missing_required, missing_optional) lists.
function M.check()
  local missing_required = {}
  local missing_optional = {}
  for _, lang in ipairs(required) do
    if not parser_available(lang) then
      table.insert(missing_required, lang)
    end
  end
  for _, lang in ipairs(optional) do
    if not parser_available(lang) then
      table.insert(missing_optional, lang)
    end
  end
  return missing_required, missing_optional
end

-- Diagnose returns a table reporting which probe paths each parser hit.
-- Useful for `:lua print(vim.inspect(require("acm-ls.treesitter").diagnose()))`.
function M.diagnose()
  local langs = {}
  for _, l in ipairs(required) do table.insert(langs, l) end
  for _, l in ipairs(optional) do table.insert(langs, l) end
  local out = {}
  for _, lang in ipairs(langs) do
    out[lang] = {
      runtime_file   = check_runtime_file(lang),
      nvim_treesitter = check_nvim_treesitter(lang),
      language_add   = check_language_add(lang),
    }
  end
  return out
end

-- Notify reports missing parsers via vim.notify (called from setup()
-- when warn_missing_parsers is enabled).
function M.notify_missing()
  local missing_required, missing_optional = M.check()
  if #missing_required > 0 then
    vim.notify(
      "acm-ls: missing required treesitter parsers: " .. table.concat(missing_required, ", ")
        .. "\nInstall with :TSInstall " .. table.concat(missing_required, " ")
        .. "\nIf they are installed, run :lua print(vim.inspect(require('acm-ls.treesitter').diagnose())) for detection details.",
      vim.log.levels.WARN
    )
  end
  if #missing_optional > 0 then
    vim.notify(
      "acm-ls: optional treesitter parsers not found: " .. table.concat(missing_optional, ", ")
        .. " (install with :TSInstall " .. table.concat(missing_optional, " ") .. " for richer highlighting)",
      vim.log.levels.INFO
    )
  end
end

return M
