import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { createServer } from "vite";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = fileURLToPath(
  new URL("../../build/artifacts/remote-agents/", import.meta.url),
);
const { chromium } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "node_modules"),
    "__remote__.cjs",
  ),
)("playwright");
const route = "/__remote_agents";
const entry = route + ".tsx";
const plugin = {
  name: "remote-agents-smoke",
  resolveId: (id) => (id === entry ? id : undefined),
  load: (id) =>
    id !== entry
      ? undefined
      : `
    import {StrictMode, useState} from "react";
    import {createRoot} from "react-dom/client";
    import {translator} from "/src/i18n.ts";
    import {RemoteCalls, RemoteTargetChip, RemoteTargetMenu, useRemoteAgents} from "/src/remote-agents.tsx";
    import {MCPChips, MCPMenu, mcpCommand, builtinMCPs, useComposerMCP} from "/src/composer-mcp.tsx";
    import {SlashCommandMenu} from "/src/slash-command-menu.tsx";
    import {commandSuggestions} from "/src/slash-commands.ts";
    import {api} from "/src/api.ts";
    import {AgentModelMenu} from "/src/agent-model-menu.tsx";
    import "/src/style.css";
    function Fixture() {
      const [draft, setDraft] = useState("");
      const [focused, setFocused] = useState(false);
      const [error, setError] = useState("");
      const [picker, setPicker] = useState(false);
      const [scope, setScope] = useState("first");
      const [localChat, setLocalChat] = useState("");
      const [catalog, setCatalog] = useState(builtinMCPs);
      const [writable, setWritable] = useState(true);
      const remote = useRemoteAgents({account: "user", originChat: "parent-chat", scope, enabled: writable, draft, focused, disabled: false, onDraft: setDraft, onError: e => {window.remoteErrors = (window.remoteErrors || 0) + 1; setError(String(e));}});
      const mcp = useComposerMCP({scope, chatID: localChat, draft, focused, enabled: !remote.target, disabled: false, available: remote.serversLoaded ? !!remote.servers.length : undefined, onDraft: setDraft, catalog});
      const commands = commandSuggestions(draft, [mcpCommand]);
      const commandOpen = focused && !remote.target && !mcp.open && commands !== null;
      const send = async () => { try { setError(""); if(mcp.open) {mcp.choose();return;} if(remote.open) {remote.select();return;} if(remote.target) await remote.send(draft); else await api("/chats/parent-chat/messages", {message: draft, mcp_servers: mcp.selected}); setDraft(""); } catch(e) {setError(String(e));} };
      return <main style={{maxWidth: 700, margin: "auto", padding: 16}}>
        <div style={{minHeight: 350}} />
        {error && <p role="alert">{error}</p>}
        <form className="composer" onSubmit={e => {e.preventDefault(); void send();}}>
          {remote.open && <RemoteTargetMenu remote={remote} t={translator("en")} />}
          {mcp.open && <MCPMenu mcp={mcp} t={translator("en")} />}
          {commandOpen && <SlashCommandMenu commands={commands} t={translator("en")} activeIndex={0} onActiveIndex={() => {}} onSelect={() => mcp.show()} />}
          <RemoteTargetChip remote={remote} t={translator("en")} />
          {!remote.target && <MCPChips mcp={mcp} t={translator("en")} />}
          <RemoteCalls remote={remote} t={translator("en")} onError={e => setError(String(e))} onContext={text => {remote.clearTarget();setDraft(text);}} />
          <textarea id="chat-message" aria-label="Message" value={draft} disabled={remote.busy} onChange={e => setDraft(e.target.value)} onFocus={() => setFocused(true)} onBlur={e => {if(!e.relatedTarget?.closest(".slash-command-menu")) setFocused(false);}} onKeyDown={e => {if(mcp.onKeyDown(e)) return; if(commandOpen && ["Enter","ArrowRight","Tab"].includes(e.key)) {e.preventDefault();mcp.show();return;} if(remote.onKeyDown(e)) return; if(e.key === "Enter" && !e.shiftKey) {e.preventDefault(); void send();}}} />
          <div className="composer-bottom"><div className="model-picker"><button type="button" onClick={() => setPicker(!picker)}>Choose agent and model</button>
            {picker && <AgentModelMenu agents={remote.agents} selection={remote.selection} t={translator("en")} onChange={remote.setSelection} />}</div>
          <button type="submit" disabled={(remote.target && !remote.ready) || remote.busy || !draft.trim()}>Send message</button></div>
        </form><button onClick={() => setScope("second")}>Switch local chat</button><button onClick={() => {mcp.adoptChat("created-chat");setLocalChat("created-chat");}}>Adopt created chat</button>
        <button onClick={() => setWritable(!writable)}>Toggle composer</button>
        <button onClick={() => setCatalog([...builtinMCPs, {id: "fixture_search", name: "Fixture search", description: "Second MCP fixture", removeLabel: "Remove fixture MCP"}])}>Add fixture MCP</button>
      </main>;
    }
    createRoot(document.getElementById("root")).render(<StrictMode><Fixture /></StrictMode>);
  `,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url !== route) return next();
      res.setHeader("Content-Type", "text/html");
      res.end(
        await server.transformIndexHtml(
          route,
          `<!doctype html><html lang="en"><div id="root"></div><script type="module" src="${entry}"></script></html>`,
        ),
      );
    });
  },
};
const server = await createServer({
  root,
  configFile: false,
  cacheDir: join(requireRunTemp(), "vite-cache"),
  plugins: [plugin, react()],
  server: { host: "127.0.0.1", port: 0, strictPort: true },
});
const calls = [];
const receipts = [];
let failFirst = true;
let projectWritable = true;
let serversDown = false;
let noServers = false;
let historyWait;
const unreachable = [];
const localMessages = [];
const model = (id) => ({
  model: id,
  displayName: id,
  description: "Provider guidance",
  summary: { text: "Maintained model guidance" },
  defaultReasoningEffort: "low",
  supportedReasoningEfforts: [
    { reasoningEffort: "low" },
    { reasoningEffort: "high" },
  ],
});
let browser;
try {
  // Vite treats configured port 0 as its default; bind the owned HTTP server
  // directly so concurrent smoke runs receive independent ephemeral ports.
  await new Promise((resolve, reject) => {
    server.httpServer.once("error", reject);
    server.httpServer.listen(0, "127.0.0.1", resolve);
  });
  browser = await chromium.launchPersistentContext(
    join(requireRunTemp(), "chrome"),
    {
      channel: "chrome",
      headless: true,
      viewport: { width: 960, height: 900 },
    },
  );
  const page = browser.pages()[0];
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.route("**/api/chats/parent-chat/messages*", async (route) => {
    localMessages.push(route.request().postDataJSON());
    await route.fulfill({ json: { ok: true } });
  });
  await page.route("**/api/federation/agents", async (route) => {
    const input = route.request().postDataJSON();
    calls.push(input);
    assert.equal(input.origin_chat, "parent-chat");
    const respond = (json, status = 200) => route.fulfill({ status, json });
    switch (input.operation) {
      case "servers":
        if (serversDown)
          return respond({ error: "Server list unavailable" }, 503);
        return respond(
          noServers ? [] : [{ id: "remote-server", name: "Trusted server" }],
        );
      case "projects":
        return respond([
          { id: "project-read", name: "Read only", can_create: false },
          {
            id: "project-write",
            name: "Remote project",
            can_create: projectWritable,
            summary: { text: "Remote project purpose" },
          },
        ]);
      case "agents":
        return respond(
          ["codex", "claude"].map((backend) => ({
            backend,
            enabled: true,
            installed: true,
            summary: { text: "Maintained agent guidance" },
            catalog: {
              defaultModel: backend + "-one",
              models: [model(backend + "-one"), model(backend + "-two")],
            },
          })),
        );
      case "calls":
        return respond(receipts);
      case "delegate": {
        if (failFirst) {
          failFirst = false;
          return respond({ error: "Temporary connection failure" }, 502);
        }
        const task = {
          task_id: "task-" + receipts.length,
          request_id: input.request_id,
          conversation_id: input.conversation_id || "remote-conversation",
          turn_id: "remote-turn",
          project_id: input.project,
          project_name: "Remote project",
          agent: input.agent,
          model: input.model,
          reasoning_effort: input.reasoning_effort,
          status: "awaiting_approval",
          progress: "Unfinished remote update",
          // Stale/older servers may still put partial output in text. The
          // browser must not offer it as a completed result either.
          text: "Unfinished result must not enter context",
          pending_approval: {
            id: "approval-" + receipts.length,
            title: "Remote approval",
            fields: [
              {
                name: "confirm",
                label: "Confirm remote action",
                type: "boolean",
              },
            ],
          },
        };
        receipts.unshift({
          server: input.server,
          request_id: input.request_id,
          message: input.message,
          task,
        });
        return respond(task);
      }
      case "task": {
        const call = receipts.find(
          (call) => call.task.task_id === input.task_id,
        );
        if (call.unreachable) {
          unreachable.push(Date.now());
          return respond({ error: "Remote server unreachable" }, 502);
        }
        return respond(call.task);
      }
      case "approve": {
        assert.equal(input.approval.turn_id, "remote-turn");
        assert.equal(input.approval.content.confirm, true);
        const task = receipts.find(
          (call) => call.task.task_id === input.task_id,
        ).task;
        task.status = "completed";
        task.text = "**Remote result to use as context**";
        delete task.progress;
        delete task.pending_approval;
        return respond({ ok: true });
      }
      case "cancel": {
        const task = receipts.find(
          (call) => call.task.task_id === input.task_id,
        ).task;
        task.status = "stopped";
        delete task.pending_approval;
        return respond(task);
      }
      case "history":
        if (historyWait) await historyWait;
        return respond({
          conversation_id: "remote-conversation",
          turns: [
            {
              id: input.before ? "older-turn" : "new-turn",
              messages: [
                {
                  role: "assistant",
                  text: input.before
                    ? "**Earlier remote history**"
                    : "**Latest remote history**",
                },
                { role: "user", text: "Literal *task* text" },
              ],
              truncated: !!input.before,
            },
          ],
          next: input.before ? "" : "older-cursor",
        });
      default:
        throw new Error("Unexpected operation " + input.operation);
    }
  });
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}${route}`,
  );
  const draft = page.getByRole("textbox", { name: "Message", exact: true });
  const menu = page.getByRole("listbox");
  const send = page.getByRole("button", { name: "Send message", exact: true });
  const callsControl = page.locator(".remote-calls-control > button");
  const callsDialog = page.getByRole("dialog");
  const openTask = async (index = 0) => {
    await callsControl.click();
    await callsDialog.locator(".remote-calls-list > button").nth(index).click();
  };
  const closeCalls = async () => {
    await callsDialog
      .getByRole("button", { name: "Close", exact: true })
      .click();
    await callsDialog.waitFor({ state: "detached" });
    await page.waitForFunction(() =>
      document.activeElement?.closest(".remote-calls-control"),
    );
  };
  const sendLocal = () =>
    Promise.all([
      page.waitForResponse((response) =>
        response.url().includes("/api/chats/parent-chat/messages"),
      ),
      send.click(),
    ]);
  const removeMCP = page.getByRole("button", {
    name: "Remove remote agent MCP",
    exact: true,
  });
  assert.equal(await removeMCP.count(), 0);
  await draft.fill("Local task without MCP");
  await sendLocal();
  assert.deepEqual(localMessages.at(-1), {
    message: "Local task without MCP",
    mcp_servers: [],
  });
  await draft.focus();
  await draft.fill("/");
  const mcpRow = page.getByRole("option", {
    name: "mcp Choose MCP tools for this chat",
    exact: true,
  });
  await mcpRow.waitFor();
  assert.equal(
    await mcpRow.locator(".slash-command-next").count(),
    1,
    "mcp row has a right chevron",
  );
  await page.setViewportSize({ width: 390, height: 900 });
  await mkdir(output, { recursive: true });
  await page.screenshot({ path: join(output, "mcp-root-390.png") });
  await draft.press("ArrowRight");
  const mcpList = page.getByRole("listbox", { name: "MCP tools", exact: true });
  await mcpList.waitFor();
  await page.screenshot({ path: join(output, "mcp-list-390.png") });
  await draft.press("ArrowLeft");
  await mcpRow.waitFor();
  await mcpRow.click();
  await mcpList.waitFor();
  await draft.press("Enter");
  await removeMCP.waitFor();
  assert.equal(
    await draft.inputValue(),
    "",
    "MCP selector is not part of the message",
  );
  assert.equal(
    localMessages.length,
    1,
    "Selecting MCP must not start an agent",
  );
  await page
    .getByRole("button", { name: "Adopt created chat", exact: true })
    .click();
  await removeMCP.waitFor();
  await draft.fill("Task with selected tools");
  await sendLocal();
  assert.deepEqual(localMessages.at(-1), {
    message: "Task with selected tools",
    mcp_servers: ["spool_federation"],
  });
  await removeMCP.click();
  await draft.fill("Next task without tools");
  await sendLocal();
  assert.deepEqual(localMessages.at(-1).mcp_servers, []);
  await draft.focus();
  await draft.fill("/mcp");
  await mcpList.waitFor();
  await page
    .getByRole("option", {
      name: "Remote agents Delegate to agents on trusted servers",
      exact: true,
    })
    .click();
  await removeMCP.waitFor();
  await page.reload();
  assert.equal(
    await removeMCP.count(),
    0,
    "MCP selection has no account-wide or persisted default",
  );
  await page.setViewportSize({ width: 960, height: 900 });
  await draft.focus();
  await draft.fill("@");
  const serverRow = page.getByRole("option", {
    name: "Trusted server",
    exact: true,
  });
  await serverRow.waitFor();
  assert.equal(
    await serverRow.locator(".remote-server-next").count(),
    1,
    "Server row must have a right chevron",
  );
  assert.equal(
    calls.filter((call) => call.operation === "delegate").length,
    0,
    "Typing @ must not launch an agent",
  );
  await draft.press("Enter");
  await page
    .getByRole("option", {
      name: "Remote project Remote project purpose",
      exact: true,
    })
    .waitFor();
  await draft.press("ArrowDown");
  await draft.press("Enter");
  await page
    .getByText("@Trusted server/Remote project", { exact: true })
    .waitFor();
  assert.equal(
    await draft.inputValue(),
    "",
    "Target selector must not become message text",
  );
  await page
    .getByRole("button", { name: "Choose agent and model", exact: true })
    .click();
  await page.getByRole("button", { name: "claude-two", exact: true }).click();
  await page
    .getByRole("slider", { name: "Reasoning level", exact: true })
    .fill("2");
  await page
    .getByRole("button", { name: "Choose agent and model", exact: true })
    .click();
  await draft.fill("Analyze the remote project");
  await send.click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Temporary connection failure" })
    .waitFor();
  await send.click();
  await callsControl
    .filter({ hasText: "Remote agents · 1 · Approval required" })
    .waitFor();
  assert.equal(
    await page.locator(".remote-agent-task").count(),
    0,
    "Call details stay out of the transcript and composer until opened",
  );
  await openTask();
  await page.getByText("Remote approval", { exact: true }).waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "Use result as context", exact: true })
      .count(),
    0,
  );
  const firstTask = callsDialog.locator(".remote-agent-task");
  await firstTask
    .getByText("Analyze the remote project", { exact: true })
    .waitFor();
  await firstTask
    .getByText("Unfinished remote update", { exact: true })
    .waitFor();
  assert.equal(
    await firstTask
      .getByText("Unfinished result must not enter context", { exact: true })
      .count(),
    0,
  );
  const submitted = calls.filter((call) => call.operation === "delegate");
  assert.equal(
    submitted[0].request_id,
    submitted[1].request_id,
    "Retry must reuse request ID",
  );
  assert.equal(submitted[1].agent, "claude");
  assert.equal(submitted[1].model, "claude-two");
  assert.equal(submitted[1].reasoning_effort, "high");
  assert.equal(submitted[1].message, "Analyze the remote project");
  assert.equal(submitted[1].context, undefined);
  assert.equal(
    await removeMCP.count(),
    0,
    "Direct @ calls do not require selecting MCP",
  );
  assert.ok(
    !JSON.stringify(submitted[1]).includes("@Trusted server/"),
    "Routing label must stay out of request message",
  );
  await page.getByRole("checkbox", { name: "Confirm remote action" }).check();
  await page.getByRole("button", { name: "Allow once", exact: true }).click();
  assert.equal(
    calls.filter((call) => call.operation === "delegate").length,
    2,
    "Approval submission must not submit the composer form",
  );
  await page
    .getByText("Remote result to use as context", { exact: true })
    .waitFor();
  assert.equal(
    await firstTask
      .locator("strong")
      .filter({ hasText: "Remote result to use as context" })
      .count(),
    1,
    "Final answers use Markdown rendering",
  );
  await page
    .getByRole("button", { name: "Remote history", exact: true })
    .click();
  await page.getByText("Latest remote history", { exact: true }).waitFor();
  assert.equal(
    await callsDialog
      .locator("strong")
      .filter({ hasText: "Latest remote history" })
      .count(),
    1,
    "History renders assistant Markdown",
  );
  await callsDialog.getByText("Literal *task* text", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Load older messages" }).click();
  await page.getByText("Earlier remote history", { exact: true }).waitFor();
  await callsDialog
    .getByText("Output truncated. Read remote history for details.", {
      exact: true,
    })
    .waitFor();
  await callsDialog.getByRole("button", { name: "Back", exact: true }).click();
  let releaseHistory;
  historyWait = new Promise((resolve) => {
    releaseHistory = resolve;
  });
  const pendingHistory = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/federation/agents") &&
      response.request().postDataJSON().operation === "history",
  );
  await callsDialog
    .getByRole("button", { name: "Remote history", exact: true })
    .click();
  await callsDialog
    .getByRole("status")
    .filter({ hasText: "Loading conversation…" })
    .waitFor();
  await callsDialog.getByRole("button", { name: "Back", exact: true }).click();
  releaseHistory();
  historyWait = undefined;
  await (await pendingHistory).finished();
  await callsDialog
    .getByText("Remote result to use as context", { exact: true })
    .waitFor();
  await closeCalls();
  assert.ok(
    await callsControl.evaluate(
      (element) => document.activeElement === element,
    ),
    "Closing returns focus to the calls entry",
  );
  await mkdir(output, { recursive: true });
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await draft.fill("");
    await draft.fill("@");
    await draft.focus();
    await menu.waitFor();
    const bounds = await menu.boundingBox();
    assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width);
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "Composer and remote calls must fit viewport",
    );
    await page.screenshot({ path: join(output, `remote-${width}.png`) });
    await draft.press("Escape");
    await callsControl.click();
    const dialogBounds = await callsDialog.boundingBox();
    assert.ok(
      dialogBounds.x >= 0 && dialogBounds.x + dialogBounds.width <= width,
      "Calls dialog fits the viewport",
    );
    assert.ok(
      await callsDialog.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
      "Calls list has no horizontal overflow",
    );
    await page.screenshot({ path: join(output, `remote-calls-${width}.png`) });
    await callsDialog.locator(".remote-calls-list > button").first().click();
    await firstTask.waitFor();
    assert.equal(
      await callsDialog.locator(".remote-agent-history").count(),
      0,
      "A closed history request cannot replace reopened task details",
    );
    assert.ok(
      await callsDialog
        .locator(".remote-calls-body")
        .evaluate((element) => element.scrollWidth <= element.clientWidth),
      "Task details have no horizontal overflow",
    );
    await page.screenshot({ path: join(output, `remote-task-${width}.png`) });
    await closeCalls();
  }
  await openTask();
  await page
    .getByRole("button", { name: "Continue conversation", exact: true })
    .click();
  await callsDialog.waitFor({ state: "detached" });
  await draft.fill("Follow up");
  await send.click();
  await openTask();
  await page
    .getByRole("button", { name: "Stop response", exact: true })
    .click();
  await firstTask.getByRole("status").filter({ hasText: "Stopped" }).waitFor();
  assert.equal(
    calls.filter((call) => call.operation === "delegate").at(-1)
      .conversation_id,
    "remote-conversation",
  );
  await callsDialog.getByRole("button", { name: "Back", exact: true }).click();
  assert.ok(
    (
      await callsDialog
        .locator(".remote-calls-list > button")
        .first()
        .innerText()
    ).includes("Stopped"),
    "Most recent calls appear first",
  );
  await callsDialog.locator(".remote-calls-list > button").nth(1).click();
  await page
    .getByRole("button", { name: "Use result as context", exact: true })
    .click();
  const context = JSON.parse(await draft.inputValue());
  assert.equal(context.remote_server, "remote-server");
  assert.equal(context.reference_output, "**Remote result to use as context**");
  assert.ok(!JSON.stringify(context).includes("Unfinished"));
  await callsDialog.waitFor({ state: "detached" });
  await draft.focus();
  // MCP calls are created by the server while the local agent runs. They must
  // appear through receipt polling without selecting a remote composer target.
  receipts.unshift({
    server: "remote-server",
    request_id: "mcp-request",
    message: "Agent delegated task",
    task: {
      task_id: "mcp-task",
      request_id: "mcp-request",
      conversation_id: "mcp-conversation",
      project_id: "project-write",
      project_name: "Remote project",
      agent: "codex",
      model: "codex-two",
      status: "running",
      progress: "MCP remote work in progress",
    },
  });
  await callsControl
    .filter({ hasText: "Remote agents · 3 · Running" })
    .waitFor();
  await openTask();
  const mcpTask = callsDialog
    .locator(".remote-agent-task")
    .filter({ hasText: "codex-two" });
  await mcpTask
    .getByText("MCP remote work in progress", { exact: true })
    .waitFor();
  assert.equal(
    await mcpTask
      .getByRole("button", { name: "Use result as context", exact: true })
      .count(),
    0,
  );
  receipts[0].task.status = "completed";
  receipts[0].task.text = "**MCP final answer**";
  delete receipts[0].task.progress;
  await mcpTask.getByText("MCP final answer", { exact: true }).waitFor();
  await mcpTask
    .getByRole("button", { name: "Use result as context", exact: true })
    .click();
  assert.equal(
    JSON.parse(await draft.inputValue()).reference_output,
    "**MCP final answer**",
  );
  await callsDialog.waitFor({ state: "detached" });
  await openTask();
  projectWritable = false;
  const agentsBeforeResume = calls.filter(
    (call) => call.operation === "agents",
  ).length;
  await mcpTask
    .getByRole("button", { name: "Continue conversation", exact: true })
    .click();
  await callsDialog
    .getByRole("alert")
    .filter({ hasText: "Read-only" })
    .waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "Remove remote target", exact: true })
      .count(),
    0,
    "Permission revocation must block continuation without setting a target",
  );
  assert.equal(
    JSON.parse(await draft.inputValue()).reference_output,
    "**MCP final answer**",
  );
  assert.equal(
    calls.filter((call) => call.operation === "agents").length,
    agentsBeforeResume,
  );
  await closeCalls();
  projectWritable = true;
  await draft.fill("Keep this text @Trust");
  await draft.focus();
  await page
    .getByRole("option", { name: "Trusted server", exact: true })
    .click();
  const readOnly = page.getByRole("option", {
    name: "Read only Read-only",
    exact: true,
  });
  await readOnly.waitFor();
  assert.ok(
    await readOnly.isDisabled(),
    "Read-only projects must be disabled in the selector",
  );
  // Dispatch directly as well as using the composer keyboard, exercising both
  // the event handler guard and native button semantics without an action timeout.
  await readOnly.dispatchEvent("click");
  await draft.press("Enter");
  await draft.press("Tab");
  assert.equal(
    await draft.inputValue(),
    "Keep this text @",
    "Read-only selection must leave the draft and selector unchanged",
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Remove remote target", exact: true })
      .count(),
    0,
  );
  assert.ok(await menu.isVisible());
  await page
    .getByRole("option", {
      name: "Remote project Remote project purpose",
      exact: true,
    })
    .click();
  assert.equal(
    await draft.inputValue(),
    "Keep this text",
    "Writable selection preserves the task body",
  );
  await page
    .getByRole("button", { name: "Remove remote target", exact: true })
    .click();
  await draft.fill("/mcp");
  await draft.focus();
  await mcpList.waitFor();
  await draft.press("Enter");
  await removeMCP.waitFor();
  await openTask();
  await page
    .getByRole("button", {
      name: "Switch local chat",
      exact: true,
      includeHidden: true,
    })
    .evaluate((button) => button.click());
  await callsDialog.waitFor({ state: "detached" });
  assert.equal(
    await page
      .getByRole("button", { name: "Remove remote target", exact: true })
      .count(),
    0,
    "Target must not leak into another local chat",
  );
  assert.equal(
    await removeMCP.count(),
    0,
    "MCP selection must not leak into another local chat",
  );
  // An injected fixture entry exercises list navigation and independent
  // selection. The production catalog still contains only spool_federation.
  await page
    .getByRole("button", { name: "Add fixture MCP", exact: true })
    .click();
  await draft.fill("/mcp");
  await draft.focus();
  await mcpList.waitFor();
  assert.equal(await mcpList.getByRole("option").count(), 2);
  await draft.press("Enter");
  await removeMCP.waitFor();
  await draft.fill("/mcp");
  await mcpList.waitFor();
  await draft.press("ArrowDown");
  assert.equal(
    await mcpList.getByRole("option", { selected: true }).innerText(),
    "Fixture search\nSecond MCP fixture",
  );
  await draft.press("Enter");
  const removeFixture = page.getByRole("button", {
    name: "Remove fixture MCP",
    exact: true,
  });
  await removeFixture.waitFor();
  assert.equal(await page.locator(".composer-mcp-chip").count(), 2);
  await draft.fill("Task with two MCPs");
  await sendLocal();
  assert.deepEqual(localMessages.at(-1).mcp_servers, [
    "spool_federation",
    "fixture_search",
  ]);
  await removeMCP.click();
  assert.equal(await page.locator(".composer-mcp-chip").count(), 1);
  await removeFixture.waitFor();
  await draft.fill("/mcp fixture");
  await draft.focus();
  await mcpList.waitFor();
  assert.equal(await mcpList.getByRole("option").count(), 1);
  assert.equal(await mcpList.locator(".lucide-check").count(), 1);
  await draft.press("Tab");
  assert.equal(
    await removeFixture.count(),
    0,
    "Toggling one MCP removes only that selection",
  );
  // Text ending in "@word" that names no server is a message, not a menu.
  await draft.fill("Ask @nobody");
  await draft.focus();
  assert.equal(
    await page.locator("#remote-target-menu").count(),
    0,
    "A mention that matches no server opens no menu",
  );
  await Promise.all([
    page.waitForResponse((response) =>
      response.url().includes("/api/chats/parent-chat/messages"),
    ),
    draft.press("Enter"),
  ]);
  assert.equal(localMessages.at(-1).message, "Ask @nobody");
  await draft.fill("@Trust");
  await serverRow.waitFor();
  await draft.press("ArrowLeft");
  assert.equal(
    await draft.evaluate((element) => element.selectionStart),
    5,
    "The server list leaves the caret keys to the text",
  );
  await draft.press("Escape");
  // A composer that is briefly not writable, or a failed refresh of the server
  // list, says nothing about trust and must keep the chosen MCP.
  await draft.fill("/mcp remote");
  await draft.focus();
  await mcpList.waitFor();
  await draft.press("Enter");
  await removeMCP.waitFor();
  const toggle = page.getByRole("button", {
    name: "Toggle composer",
    exact: true,
  });
  await toggle.click();
  serversDown = true;
  await toggle.click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Server list unavailable" })
    .waitFor();
  serversDown = false;
  await draft.fill("Task after an interruption");
  await sendLocal();
  assert.deepEqual(
    localMessages.at(-1).mcp_servers,
    ["spool_federation"],
    "A passing interruption cleared the selected MCP",
  );
  // An unreachable server is reported once and asked less often, not every
  // 1.5 seconds; its task still updates when the server returns.
  const reportedBefore = await page.evaluate(() => window.remoteErrors || 0);
  receipts.unshift({
    server: "remote-server",
    request_id: "offline-request",
    message: "Task on an unreachable server",
    unreachable: true,
    task: {
      task_id: "offline-task",
      request_id: "offline-request",
      conversation_id: "offline-conversation",
      project_id: "project-write",
      project_name: "Remote project",
      agent: "codex",
      model: "codex-offline",
      status: "running",
    },
  });
  await page
    .getByRole("alert")
    .filter({ hasText: "Remote server unreachable" })
    .waitFor();
  for (let waited = 0; unreachable.length < 2; waited += 100) {
    assert.ok(waited < 9000, "Polling stopped after a failure");
    await page.waitForTimeout(100);
  }
  assert.ok(
    unreachable[1] - unreachable[0] >= 2500,
    "A failing server is still polled at the normal rate",
  );
  assert.equal(
    (await page.evaluate(() => window.remoteErrors)) - reportedBefore,
    1,
    "The same polling failure was reported repeatedly",
  );
  receipts[0].unreachable = false;
  receipts[0].task.status = "completed";
  receipts[0].task.text = "Recovered remote answer";
  await callsControl.filter({ hasText: "Completed" }).waitFor();
  await openTask();
  await page.getByText("Recovered remote answer", { exact: true }).waitFor();
  await page.keyboard.press("Escape");
  await callsDialog.waitFor({ state: "detached" });
  // An imported native session has historical snapshots but no local live
  // delegation records or available trusted servers. Its entry still appears.
  noServers = true;
  receipts.splice(
    0,
    receipts.length,
    ...["completed", "awaiting_approval"].map((status, index) => ({
      server: "original-server",
      server_name: "Original PC",
      history_only: true,
      request_id: "historical-request-" + index,
      message: "Historical task " + index,
      task: {
        task_id: "historical-task-" + index,
        conversation_id: "original-conversation",
        project_id: "original-project",
        project_name: "Historical project",
        agent: "codex",
        status,
        text:
          status === "completed" ? "**Historical final answer**" : undefined,
        pending_approval:
          status === "awaiting_approval"
            ? { id: "obsolete-approval", title: "Obsolete approval" }
            : undefined,
      },
    })),
  );
  const beforeHistory = calls.length;
  await page.reload();
  await callsControl
    .filter({ hasText: "Remote agents · 2 · Completed" })
    .waitFor();
  await openTask();
  await callsDialog
    .getByRole("heading", {
      name: "@Original PC/Historical project",
      exact: true,
    })
    .waitFor();
  await callsDialog
    .getByText("Historical final answer", { exact: true })
    .waitFor();
  await callsDialog
    .getByText(
      "Historical remote call. Open the original server to refresh or continue.",
      { exact: true },
    )
    .waitFor();
  for (const action of [
    "Continue conversation",
    "Remote history",
    "Stop response",
  ]) {
    assert.equal(
      await callsDialog
        .getByRole("button", { name: action, exact: true })
        .count(),
      0,
      "Historical snapshots grant no live task actions",
    );
  }
  await callsDialog
    .getByRole("button", { name: "Use result as context", exact: true })
    .click();
  assert.equal(
    JSON.parse(await draft.inputValue()).reference_output,
    "**Historical final answer**",
  );
  await openTask(1);
  assert.equal(
    await callsDialog.getByText("Obsolete approval", { exact: true }).count(),
    0,
  );
  assert.equal(
    await callsDialog
      .getByRole("button", { name: "Use result as context", exact: true })
      .count(),
    0,
  );
  await page.screenshot({ path: join(output, "historical-remote-call.png") });
  await closeCalls();
  assert.ok(
    calls
      .slice(beforeHistory)
      .every(
        (call) =>
          !["task", "approve", "cancel", "history", "projects"].includes(
            call.operation,
          ),
      ),
    "Historical records must not trigger remote control or polling requests",
  );
  assert.deepEqual(errors, []);
  console.log(
    "Android-style remote calls entry, list and live details, Markdown history, approvals outside the composer form, close/back navigation, late history isolation, MCP routing, read-only project denial, chat isolation, quiet polling failures and narrow layouts passed",
  );
} finally {
  await browser?.close();
  await server.close();
}
