-- Treesitter parser checks.
-- The plugin's queries assume tree-sitter-yaml and tree-sitter-go-template
-- (gotmpl) are installed. Helm-specific queries are optional but nice if
-- tree-sitter-helm is present.

local M = {}

local required = { "yaml", "gotmpl" }
local optional = { "helm" }

local function parser_available(lang)
  local ok, parsers = pcall(require, "nvim-treesitter.parsers")
  if ok and parsers and parsers.has_parser then
    return parsers.has_parser(lang)
  end
  if vim.treesitter and vim.treesitter.language and vim.treesitter.language.get_lang_for_filetype then
    -- Newer Neovim — try loading the parser directly.
    local lok = pcall(vim.treesitter.language.add, lang)
    return lok
  end
  return false
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

-- Notify reports missing parsers via vim.notify (called from setup()
-- when warn_missing_parsers is enabled).
function M.notify_missing()
  local missing_required, missing_optional = M.check()
  if #missing_required > 0 then
    vim.notify(
      "AutoShift: missing required treesitter parsers: " .. table.concat(missing_required, ", ")
        .. "\nInstall with :TSInstall " .. table.concat(missing_required, " "),
      vim.log.levels.WARN
    )
  end
  if #missing_optional > 0 then
    vim.notify(
      "AutoShift: optional treesitter parsers not found: " .. table.concat(missing_optional, ", ")
        .. " (install with :TSInstall " .. table.concat(missing_optional, " ") .. " for richer highlighting)",
      vim.log.levels.INFO
    )
  end
end

return M
