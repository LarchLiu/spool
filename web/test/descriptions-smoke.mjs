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
  new URL("../../build/artifacts/descriptions/", import.meta.url),
);
const { chromium } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "node_modules"),
    "__descriptions__.cjs",
  ),
)("playwright");
const route = "/__descriptions";
const entry = route + ".tsx";
const plugin = {
  name: "descriptions-smoke",
  resolveId: (id) => (id === entry ? id : undefined),
  load: (id) =>
    id !== entry
      ? undefined
      : `
    import {useEffect, useState} from "react";
    import {createRoot} from "react-dom/client";
    import {translator} from "/src/i18n.ts";
    import {AgentsPage} from "/src/settings-pages.tsx";
    import {ProjectsPage} from "/src/settings-projects.tsx";
    import {newProject} from "/src/settings-client.ts";
    import "/src/style.css";
    import "/src/settings.css";
    const model = (id, name) => ({model: id, displayName: name, description: "Provider description", defaultReasoningEffort: "low", supportedReasoningEfforts: [{reasoningEffort: "low"}, {reasoningEffort: "high"}]});
    window.snapshot = {
      users: [], status: {}, github_path: "", github_message: "",
      projects: [{...newProject(), id: "project", name: "Demo project", path: "/fixture/project"}],
      agents: ["codex", "claude"].map(backend => ({backend, enabled: true, installed: true, executable: "", resolved_path: "", version: "1.0", catalog: {defaultModel: backend + "-one", defaultEffort: "low", models: [model(backend + "-one", backend + " One"), model(backend + "-two", backend + " Two")]}})),
      description_prompts: {project: {default: "Built-in project prompt", value: "Saved project prompt"}, agent: {default: "Built-in agent prompt", value: "Saved agent prompt"}},
    };
    window.calls = [];
    window.mygo = {callWith(method, args, { signal } = {}) { signal?.throwIfAborted(); return this.call(method, ...args); }, on() {}, async call(method, request) {
      window.calls.push([method, request]);
      if (method === "DesktopSettings.CancelDescriptionSummary") return true;
      if (window.summaryError) throw new Error(window.summaryError);
      const ids = request.kind === "project" ? ["project"] : ["agent", ...window.snapshot.agents.find(a => a.backend === request.target).catalog.models.map(m => "model:" + m.model)];
      const targetAgent = window.snapshot.agents.find(a => a.backend === request.target);
      const summary = id => request.kind === "project" ? window.snapshot.projects[0].summary : id === "agent" ? targetAgent.summary : targetAgent.catalog.models.find(m => "model:" + m.model === id).summary;
      const result = {backend: request.backend, model: request.model, items: ids.map(id => ({id, text: "Generated " + id, expected_version: summary(id)?.version ?? "", backend: request.backend, model: request.model}))};
      if (window.holdSummary) return new Promise(resolve => window.releaseSummary = () => resolve(result));
      return result;
    }};
    function Fixture() {
      const [data, setData] = useState(window.snapshot);
      const [section, setSection] = useState("Agents");
      const [language, setLanguage] = useState("en");
      useEffect(() => {
        const update = () => setData(structuredClone(window.snapshot));
        const translate = event => setLanguage(event.detail);
        window.addEventListener("test:update", update);
        window.addEventListener("test:language", translate);
        return () => {window.removeEventListener("test:update", update); window.removeEventListener("test:language", translate);};
      }, []);
      const run = async command => {
        window.calls.push(["Execute", command]);
        const next = structuredClone(window.snapshot);
        if (command.kind === "save-description-prompt") next.description_prompts[command.target].value = command.text;
        if (command.kind === "save-descriptions") {
          for (const item of command.descriptions.items) {
            const summary = {text: item.text, version: crypto.randomUUID(), updated_at: Date.now()};
            if (command.descriptions.kind === "project") next.projects[0].summary = summary;
            else {
              const agent = next.agents.find(a => a.backend === command.descriptions.target);
              if (item.id === "agent") agent.summary = summary;
              else agent.catalog.models.find(m => "model:" + m.model === item.id).summary = summary;
            }
          }
        }
        window.snapshot = next;
        setData(next);
        return next;
      };
      const Page = section === "Agents" ? AgentsPage : ProjectsPage;
      return <div className="desktop-settings-shell"><div className="settings-main-area"><nav><button onClick={() => setSection("Agents")}>Agents page</button><button onClick={() => setSection("Projects")}>Projects page</button></nav><main className="settings-main"><div className="settings-page"><Page data={data} run={run} t={translator(language)} /></div></main></div></div>;
    }
    createRoot(document.getElementById("root")).render(<Fixture />);
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
let browser;
try {
  await server.listen();
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
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}${route}`,
  );
  await page.locator('[data-entity-id="codex"]').click();
  const agentText = page.getByRole("textbox", { name: "Codex", exact: true });
  const modelText = page.getByRole("textbox", {
    name: "codex One",
    exact: true,
  });
  await agentText.fill("Manual Agent description");
  await modelText.fill("Manual model description");
  await page
    .getByRole("button", { name: "Save descriptions", exact: true })
    .click();
  await page.getByRole("status").filter({ hasText: "Saved" }).waitFor();
  assert.equal(await agentText.inputValue(), "Manual Agent description");
  assert.equal(
    (
      await page.evaluate(() =>
        window.calls.find(([method]) => method === "Execute"),
      )
    )[1].descriptions.items.length,
    2,
  );
  await page
    .getByRole("button", { name: "Summarize descriptions", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Agent", exact: true }).click();
  await page.getByRole("option", { name: "Claude Code", exact: true }).click();
  await dialog.getByRole("button", { name: "Model", exact: true }).click();
  await page.getByRole("option", { name: "claude Two", exact: true }).click();
  await dialog
    .getByRole("button", { name: "Reasoning level", exact: true })
    .click();
  await page.getByRole("option", { name: "High", exact: true }).click();
  const prompt = dialog.getByRole("textbox", {
    name: "System prompt",
    exact: true,
  });
  assert.equal(await prompt.inputValue(), "Saved agent prompt");
  await dialog.getByRole("button", { name: "Restore default prompt" }).click();
  assert.equal(await prompt.inputValue(), "Built-in agent prompt");
  await prompt.fill("Custom combined Agent/model prompt");
  await dialog
    .getByRole("button", { name: "Save prompt", exact: true })
    .click();
  await dialog.getByRole("status").filter({ hasText: "Saved" }).waitFor();
  await mkdir(output, { recursive: true });
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 900 });
    const bounds = await dialog.boundingBox();
    assert.ok(
      bounds.x >= 0 && bounds.x + bounds.width <= width,
      "Summary dialog must fit the viewport",
    );
    assert.ok(
      await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth),
      "Summary controls must not overflow horizontally",
    );
    await dialog.screenshot({ path: join(output, `generator-${width}.png`) });
  }
  await dialog
    .getByRole("button", { name: "Generate descriptions", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  const sent = await page.evaluate(
    () =>
      window.calls.find(
        ([method]) => method === "DesktopSettings.SummarizeDescriptions",
      )[1],
  );
  assert.equal(sent.backend, "claude");
  assert.equal(sent.model, "claude-two");
  assert.equal(sent.reasoning_effort, "high");
  assert.equal(sent.prompt, "Custom combined Agent/model prompt");
  assert.equal(await agentText.inputValue(), "Generated agent");
  assert.equal(
    await page.evaluate(() => window.snapshot.agents[0].summary.text),
    "Manual Agent description",
    "Generation must remain a draft",
  );
  await modelText.fill("Revised generated model description");
  await page
    .getByRole("button", { name: "Save descriptions", exact: true })
    .click();
  await page.getByRole("status").filter({ hasText: "Saved" }).waitFor();
  assert.equal(
    await page.evaluate(
      () => window.snapshot.agents[0].catalog.models[0].summary.text,
    ),
    "Revised generated model description",
  );
  await page
    .getByRole("button", { name: "Summarize descriptions", exact: true })
    .click();
  await page.evaluate(() => {
    window.holdSummary = true;
  });
  await dialog
    .getByRole("button", { name: "Generate descriptions", exact: true })
    .click();
  await page.waitForFunction(() => !!window.releaseSummary);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.evaluate(() => {
    window.releaseSummary();
    window.holdSummary = false;
  });
  assert.equal(
    await modelText.inputValue(),
    "Revised generated model description",
    "Canceled generation must not replace edits",
  );
  assert.ok(
    await page.evaluate(() =>
      window.calls.some(
        ([method]) => method === "DesktopSettings.CancelDescriptionSummary",
      ),
    ),
  );
  await modelText.fill("Local unsaved revision");
  await page.evaluate(() => {
    window.snapshot.agents[0].catalog.models[0].summary = {
      text: "External revision",
      version: "external",
      updated_at: Date.now(),
    };
    window.dispatchEvent(new Event("test:update"));
  });
  await page
    .getByText("Descriptions changed elsewhere. Reload them before saving.", {
      exact: true,
    })
    .waitFor();
  assert.equal(await modelText.inputValue(), "Local unsaved revision");
  assert.ok(
    await page
      .getByRole("button", { name: "Save descriptions", exact: true })
      .isDisabled(),
  );
  await page
    .getByRole("button", { name: "Reload descriptions", exact: true })
    .click();
  assert.equal(await modelText.inputValue(), "External revision");
  await modelText.fill("Another local revision");
  await page.evaluate(() => {
    window.snapshot.agents[0].catalog.models[0].summary = {
      text: "New external revision",
      version: "new-external",
      updated_at: Date.now(),
    };
    window.dispatchEvent(new Event("test:update"));
  });
  await page
    .getByText("Descriptions changed elsewhere. Reload them before saving.", {
      exact: true,
    })
    .waitFor();
  await page
    .getByRole("button", { name: "Summarize descriptions", exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Generate descriptions", exact: true })
    .click();
  await dialog.waitFor({ state: "hidden" });
  assert.ok(
    await page
      .getByRole("button", { name: "Save descriptions", exact: true })
      .isEnabled(),
    "Regeneration must use the new source versions",
  );
  await page
    .getByRole("button", { name: "Discard changes", exact: true })
    .click();
  assert.equal(await modelText.inputValue(), "New external revision");
  await page
    .getByRole("button", { name: "Projects page", exact: true })
    .click();
  await page.locator('[data-entity-id="project"]').click();
  const projectText = page.getByRole("textbox", {
    name: "Project description",
    exact: true,
  });
  await projectText.fill("Manual project description");
  await page
    .getByRole("button", { name: "Save descriptions", exact: true })
    .click();
  await page.getByRole("status").filter({ hasText: "Saved" }).waitFor();
  await page
    .getByRole("button", { name: "Summarize descriptions", exact: true })
    .click();
  assert.equal(
    await prompt.inputValue(),
    "Saved project prompt",
    "Project prompt must remain independent",
  );
  await page.evaluate(() => {
    window.summaryError = "The agent returned invalid descriptions. Try again.";
  });
  await dialog
    .getByRole("button", { name: "Generate descriptions", exact: true })
    .click();
  await dialog.getByRole("alert").waitFor();
  assert.ok(
    await dialog
      .getByRole("button", { name: "Generate descriptions", exact: true })
      .isEnabled(),
  );
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  assert.equal(await projectText.inputValue(), "Manual project description");
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "zh-CN" }));
  });
  await page.getByRole("heading", { name: "项目说明", exact: true }).waitFor();
  await page
    .locator(".settings-group")
    .filter({
      has: page.getByRole("heading", { name: "项目说明", exact: true }),
    })
    .screenshot({ path: join(output, "project-dark-390.png") });
  assert.deepEqual(errors, []);
  console.log(
    "Description editing, combined generation, prompt/model/effort selection, cancellation, conflicts and narrow layouts passed",
  );
} finally {
  await browser?.close();
  await server.close();
}
