-- AutoShift LSP client for Neovim.
-- Starts the autoshift-lsp Go binary as an LSP server and attaches it to
-- buffers under autoshift/policy directories.

local M = {}

local default_config = {
  -- Path to the autoshift-lsp binary. Default assumes it's on PATH.
  cmd = { "autoshift-lsp" },
  -- File patterns the LSP attaches to.
  filetypes = { "yaml", "helm" },
  -- Root directory markers — first match wins. The LSP receives this as rootUri.
  root_markers = { "Chart.yaml", ".git", "policies" },
  -- Whether to enable LSP semantic tokens for attached buffers (Neovim 0.9+).
  semantic_tokens = true,
  -- Warn on startup if required treesitter parsers are missing.
  warn_missing_parsers = true,
  -- Settings forwarded to the server via initializationOptions and
  -- workspace/didChangeConfiguration. Mirrors the VSCode extension's schema.
  settings = {
    autoshift = {
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
    return vim.lsp.get_clients({ name = "autoshift-lsp" })
  end
  return vim.lsp.get_active_clients({ name = "autoshift-lsp" })
end

local function start_for_buffer(buf)
  if cfg == nil then return end
  local root = find_root(buf, cfg.root_markers)
  if active_clients[root] == nil then
    active_clients[root] = vim.lsp.start({
      name = "autoshift-lsp",
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

--- Stop every running autoshift-lsp client and re-attach to the active buffer.
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

--- Stop every running autoshift-lsp client without restarting.
function M.stop()
  for _, c in ipairs(get_clients()) do
    pcall(function() c:stop(true) end)
  end
  active_clients = {}
end

--- Print one line per running autoshift-lsp client (id, root_dir, cmd).
function M.status()
  local clients = get_clients()
  if #clients == 0 then
    print("autoshift-lsp: no clients running")
    return
  end
  for _, c in ipairs(clients) do
    print(string.format(
      "autoshift-lsp[id=%d] root=%s cmd=%s",
      c.id,
      c.config.root_dir or "?",
      vim.inspect(c.config.cmd)
    ))
  end
end

function M.setup(opts)
  cfg = vim.tbl_deep_extend("force", default_config, opts or {})

  if cfg.warn_missing_parsers then
    pcall(function() require("autoshift.treesitter").notify_missing() end)
  end

  vim.api.nvim_create_autocmd("FileType", {
    pattern = cfg.filetypes,
    callback = function(args) start_for_buffer(args.buf) end,
  })

  vim.api.nvim_create_user_command("AutoshiftRestart", function() M.restart() end,
    { desc = "Stop and re-attach the autoshift-lsp client(s)" })
  vim.api.nvim_create_user_command("AutoshiftStop", function() M.stop() end,
    { desc = "Stop the autoshift-lsp client(s) without restarting" })
  vim.api.nvim_create_user_command("AutoshiftStatus", function() M.status() end,
    { desc = "Print autoshift-lsp client status" })
end

return M
