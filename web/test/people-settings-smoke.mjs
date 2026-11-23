import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const { chromium } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "node_modules"),
    "__people__.cjs",
  ),
)("playwright");
const scratch = requireRunTemp();
const server = await createServer({
  root,
  cacheDir: join(scratch, "vite-people"),
  logLevel: "silent",
  optimizeDeps: { entries: ["desktop.html"] },
  server: { host: "127.0.0.1", port: 0, hmr: false, watch: null },
});
let context;
try {
  await server.listen();
  context = await chromium.launchPersistentContext(join(scratch, "chrome"), {
    channel: "chrome",
    headless: true,
    locale: "en-US",
  });
  const page = context.pages()[0];
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.stack ?? String(error)));
  await page.addInitScript(() => {
    const data = {
      section: "People",
      settings: {
        host: "127.0.0.1",
        port: 8787,
        public_url: "",
        idle_minutes: 15,
      },
      status: { Running: true, Address: "127.0.0.1:8787", Online: [] },
      projects: [],
      agents: [],
      updates: { Enabled: false },
      preferences: { theme: "light", language: "en" },
      update_preferences: {},
      users: [
        {
          id: "visitor",
          username: "external:visitor",
          nickname: "External visitor",
          source_server_id: "source-server-id",
          disabled: false,
          remote_desktop: false,
          adb_bridge: false,
        },
        { id: "alice", username: "alice", nickname: "Alice", disabled: false },
      ],
    };
    window.savedPeople = [];
    window.mygo = {
      on: () => () => {},
      async call(method, command) {
        if (method === "DesktopSettings.Snapshot") return structuredClone(data);
        if (method === "DesktopSettings.Action" && command.kind === "navigate")
          return true;
        if (
          method !== "DesktopSettings.Execute" ||
          command.kind !== "save-person"
        ) {
          throw new Error(`Unexpected settings call: ${method}`);
        }
        const { password, ...person } = command.person;
        window.savedPeople.push(structuredClone(command.person));
        const index = data.users.findIndex(({ id }) => id === person.id);
        const current = data.users[index];
        // The source can refresh the name while the administrator is editing.
        if (current.source_server_id) current.nickname = "Updated by source";
        data.users[index] = { ...current, ...person };
        return structuredClone({ ...data, saved_id: person.id });
      },
    };
  });
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}/desktop.html`,
  );
  const visitor = page.getByRole("button", {
    name: /External visitor.*Federated sign-in/,
  });
  try {
    await visitor.click({ timeout: 5000 });
  } catch (error) {
    throw new Error(
      `People page did not load: ${errors.join("; ")}\n${await page.locator("body").innerText()}`,
      { cause: error },
    );
  }
  const editor = page
    .locator("form")
    .filter({ has: page.getByRole("heading", { name: "Edit user" }) });
  await editor
    .getByText("Federation source: source-server-id", { exact: false })
    .waitFor();
  await editor.getByText(/managed by its source server/).waitFor();
  await editor.getByText("external:visitor", { exact: true }).waitFor();
  await editor.getByText("External visitor", { exact: true }).waitFor();
  assert.equal(await editor.getByRole("textbox").count(), 0);
  assert.equal(await editor.getByLabel("Password", { exact: true }).count(), 0);
  assert.equal(await editor.locator('input[type="password"]').count(), 0);
  const save = page.getByRole("button", { name: "Save user" });
  assert.equal(await save.count(), 0);
  for (const name of [
    "Allow remote desktop connection",
    "Allow ADB bridge",
    "Disable account",
  ]) {
    const control = editor.getByRole("switch", { name });
    await control.click();
    assert.equal(await control.getAttribute("aria-checked"), "true");
  }
  await save.click();
  await editor.getByText("Updated by source", { exact: true }).waitFor();
  assert.deepEqual(await page.evaluate(() => window.savedPeople), [
    { id: "visitor", disabled: true, remote_desktop: true, adb_bridge: true },
  ]);

  await page.getByRole("button", { name: "Back to list" }).click();
  await page.getByRole("button", { name: /^Alice alice$/ }).click();
  await editor.getByRole("textbox", { name: "Username" }).fill("alice-new");
  await editor.getByRole("textbox", { name: "Display name" }).fill("Alice New");
  await editor.getByLabel("Password", { exact: true }).fill("fixture-password");
  await save.click();
  await save.waitFor({ state: "hidden" });
  assert.deepEqual(await page.evaluate(() => window.savedPeople.at(-1)), {
    id: "alice",
    username: "alice-new",
    nickname: "Alice New",
    disabled: false,
    password: "fixture-password",
  });
  assert.deepEqual(errors, []);
  console.log("People settings browser checks passed");
} finally {
  try {
    await context?.close();
  } finally {
    await server.close();
  }
}
