-- acm-ls LSP client for Neovim.
-- Starts the acm-ls Go binary as an LSP server and attaches it to
-- buffers under ACM/policy directories.

local M = {}

local default_config = {
  -- Path to the acm-ls binary. Default assumes it's on PATH.
  cmd = { "acm-ls" },
  -- File patterns the LSP attaches to.
  filetypes = { "yaml", "helm" },
  -- Root directory markers — first match wins. The LSP receives this as rootUri.
  root_markers = { "Chart.yaml", ".git", "policies" },
  -- Whether to enable LSP semantic tokens for attached buffers (Neovim 0.9+).
  semantic_tokens = true,
  -- Register default highlight links for the LSP semantic tokens this server
  -- emits. Uses Neovim's standard tree-sitter capture groups (@variable,
  -- @function, @keyword, ...) as link targets, so any colorscheme that styles
  -- those groups gives acm-ls content distinct colors out of the box.
  -- Set false to leave highlight setup entirely to your colorscheme.
  apply_default_highlights = true,
  -- Per-group highlight overrides. Keys are LSP semantic-token groups
  -- (e.g. "@lsp.type.variable.yaml"), values are any spec accepted by
  -- nvim_set_hl ({ fg = "#bb9af7" }, { link = "Identifier" }, ...). These
  -- replace the corresponding default link entirely and are re-applied on
  -- ColorScheme and LspAttach so they survive theme changes and the
  -- competing default-true links Neovim's stock semantic-token module
  -- registers at attach time. Groups not in default_links are also set.
  highlights = {},
  -- Warn on startup if required treesitter parsers are missing.
  warn_missing_parsers = true,
  -- Settings forwarded to the server via initializationOptions and
  -- workspace/didChangeConfiguration. Mirrors the VSCode extension's schema.
  settings = {
    acm = {
      enabled = true,
      acm = { version = "2.15" },
      rules = {
        ["policy-name-length"] = {
          enabled = true,
          severity = "warning",
          maxLength = 63,
          kinds = {
            "Policy", "PlacementBinding", "PlacementRule", "Placement",
            "ConfigurationPolicy", "OperatorPolicy",
          },
        },
        ["policy-name-pattern"] = { enabled = false },
        ["policy-name-template"] = { enabled = true, mode = "strict" },
        ["hub-forbidden-functions"] = { enabled = true, severity = "error" },
        ["lookup-default-dict"] = { enabled = true, severity = "warning" },
        ["unclosed-delimiters"] = { enabled = true, severity = "error" },
        -- Default off: catalog sprig coverage is intentionally a subset,
        -- so false positives are non-zero. Opt in once you've confirmed
        -- your chart's sprig usage is in the catalog, or extend via
        -- rules.unknown-function.allowedFunctions.
        ["unknown-function"] = { enabled = false, severity = "warning", allowedFunctions = {} },
        -- Per-`object-templates-raw:` block-scalar parse via Go's
        -- text/template/parse. Catches malformed actions, control-flow
        -- nesting errors, bad pipelines, and similar syntax issues.
        -- Doesn't validate variable paths or function arity.
        ["template-syntax"] = { enabled = true, severity = "warning" },
      },
    },
  },
}

local cfg = nil
local active_clients = {}

local function find_root(buf, markers)
  local fname = vim.api.nvim_buf_get_name(buf)
  if fname == "" then return vim.fn.getcwd() end
  local found = vim.fs.find(markers, { upward = true, path = vim.fs.dirname(fname) })
  if found and #found > 0 then
    return vim.fs.dirname(found[1])
  end
  return vim.fs.dirname(fname)
end

local function get_clients()
  -- vim.lsp.get_clients is the modern name (Neovim 0.10+); fall back to
  -- get_active_clients for older versions.
  if vim.lsp.get_clients then
    return vim.lsp.get_clients({ name = "acm-ls" })
  end
  return vim.lsp.get_active_clients({ name = "acm-ls" })
end

local function start_for_buffer(buf)
  if cfg == nil then return end
  local root = find_root(buf, cfg.root_markers)
  if active_clients[root] == nil then
    active_clients[root] = vim.lsp.start({
      name = "acm-ls",
      cmd = cfg.cmd,
      root_dir = root,
      init_options = cfg.settings,
      settings = cfg.settings,
      on_attach = function(client, bufnr)
        if cfg.semantic_tokens and vim.lsp.semantic_tokens then
          pcall(vim.lsp.semantic_tokens.start, bufnr, client.id)
        end
      end,
    }, { bufnr = buf })
  else
    vim.lsp.buf_attach_client(buf, active_clients[root])
  end
end

--- Stop every running acm-ls client and re-attach to the active buffer.
--- Useful after rebuilding the binary so Neovim picks up the new server.
function M.restart()
  for _, c in ipairs(get_clients()) do
    pcall(function() c:stop(true) end)
  end
  active_clients = {}
  local buf = vim.api.nvim_get_current_buf()
  local ft = vim.bo[buf].filetype
  if cfg and vim.tbl_contains(cfg.filetypes, ft) then
    -- Defer so the stop fully unwinds before we start a new client.
    vim.defer_fn(function() start_for_buffer(buf) end, 50)
  end
end

--- Stop every running acm-ls client without restarting.
function M.stop()
  for _, c in ipairs(get_clients()) do
    pcall(function() c:stop(true) end)
  end
  active_clients = {}
end

--- Print one line per running acm-ls client (id, root_dir, cmd).
function M.status()
  local clients = get_clients()
  if #clients == 0 then
    print("acm-ls: no clients running")
    return
  end
  for _, c in ipairs(clients) do
    print(string.format(
      "acm-ls[id=%d] root=%s cmd=%s",
      c.id,
      c.config.root_dir or "?",
      vim.inspect(c.config.cmd)
    ))
  end
end

-- Highlight-group link table: per-language LSP semantic groups → @-namespace
-- tree-sitter captures most colorschemes already style.
--
-- Neovim's vim.lsp.semantic_tokens module emits three families of groups
-- per token, and you have to target the exact name it generates:
--   @lsp.type.<type>.<lang>                          base type
--   @lsp.mod.<modifier>.<lang>                       per modifier
--   @lsp.typemod.<type>.<modifier>.<lang>            combined (most specific)
-- There is no @lsp.type.<type>.<modifier> form — links pointed at that
-- shape are dead.
--
-- The language-specific entries are set unconditionally because Neovim's
-- own attach-time defaults otherwise route them through a no-fg chain
-- (.helm → @lsp.type.variable → @variable, where @variable usually has no
-- explicit color either). The @-namespace targets used here are ones
-- most colorschemes already style, so we get sensible colors out of the
-- box.
local default_links = {
  ["@lsp.type.variable.yaml"]                            = "@variable",
  ["@lsp.type.variable.helm"]                            = "@variable",
  ["@lsp.type.property.yaml"]                            = "@property",
  ["@lsp.type.property.helm"]                            = "@property",
  ["@lsp.typemod.property.defaultLibrary.yaml"]          = "@variable.builtin",
  ["@lsp.typemod.property.defaultLibrary.helm"]          = "@variable.builtin",
  ["@lsp.typemod.property.readonly.yaml"]                = "@variable.builtin",
  ["@lsp.typemod.property.readonly.helm"]                = "@variable.builtin",
  ["@lsp.type.function.yaml"]                            = "@function",
  ["@lsp.type.function.helm"]                            = "@function",
  ["@lsp.typemod.function.defaultLibrary.yaml"]          = "@function.builtin",
  ["@lsp.typemod.function.defaultLibrary.helm"]          = "@function.builtin",
  ["@lsp.type.keyword.yaml"]                             = "@keyword",
  ["@lsp.type.keyword.helm"]                             = "@keyword",
  -- ACM-side keyword markers (the `hub` identifier and the inner `{{`/`}}`
  -- of escape-form strings). Same TYPE as go-template keywords but tagged
  -- with the `defaultLibrary` modifier so colorschemes / user overrides
  -- can give them a distinct color from `if`/`range`/`else`/etc.
  ["@lsp.typemod.keyword.defaultLibrary.yaml"]           = "@keyword.directive",
  ["@lsp.typemod.keyword.defaultLibrary.helm"]           = "@keyword.directive",
  ["@lsp.type.string.yaml"]                              = "@string",
  ["@lsp.type.string.helm"]                              = "@string",
  ["@lsp.type.number.yaml"]                              = "@number",
  ["@lsp.type.number.helm"]                              = "@number",
  ["@lsp.type.operator.yaml"]                            = "@operator",
  ["@lsp.type.operator.helm"]                            = "@operator",
  ["@lsp.type.comment.yaml"]                             = "@comment",
  ["@lsp.type.comment.helm"]                             = "@comment",
}

local function apply_default_highlights()
  local overrides = (cfg and cfg.highlights) or {}
  for group, link in pairs(default_links) do
    vim.api.nvim_set_hl(0, group, overrides[group] or { link = link })
  end
  for group, spec in pairs(overrides) do
    if default_links[group] == nil then
      vim.api.nvim_set_hl(0, group, spec)
    end
  end
end

function M.setup(opts)
  cfg = vim.tbl_deep_extend("force", default_config, opts or {})

  if cfg.warn_missing_parsers then
    pcall(function() require("acm-ls.treesitter").notify_missing() end)
  end

  if cfg.apply_default_highlights then
    apply_default_highlights()
    -- Re-apply on colorscheme change so theme switches don't blow our links
    -- away (vim.cmd colorscheme clears the highlight namespace).
    vim.api.nvim_create_autocmd("ColorScheme", {
      callback = apply_default_highlights,
      desc = "Re-register acm-ls LSP semantic-token highlight links",
    })
    -- Re-apply when the LSP attaches; vim.lsp.semantic_tokens registers its
    -- own default-true links at that point that would otherwise win the race.
    vim.api.nvim_create_autocmd("LspAttach", {
      callback = function(args)
        local client = vim.lsp.get_client_by_id(args.data.client_id)
        if client and client.name == "acm-ls" then
          apply_default_highlights()
        end
      end,
      desc = "Re-apply acm-ls highlight links after LSP attach",
    })
  end

  vim.api.nvim_create_autocmd("FileType", {
    pattern = cfg.filetypes,
    callback = function(args) start_for_buffer(args.buf) end,
  })

  vim.api.nvim_create_user_command("AcmRestart", function() M.restart() end,
    { desc = "Stop and re-attach the acm-ls client(s)" })
  vim.api.nvim_create_user_command("AcmStop", function() M.stop() end,
    { desc = "Stop the acm-ls client(s) without restarting" })
  vim.api.nvim_create_user_command("AcmStatus", function() M.status() end,
    { desc = "Print acm-ls client status" })
end

return M
