import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir } from "node:fs/promises";
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
  server: {
    host: "127.0.0.1",
    port: 0,
    strictPort: false,
    hmr: false,
    watch: null,
  },
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
      projects: [
        {
          id: "project",
          name: "Visitor project",
          path: "/fixture/project",
          members: ["alice", "visitor"],
          member_access: {},
          member_own_access: {},
          archived: false,
          shared_chats: false,
          shared_local_chats: false,
          issues: {
            enabled: false,
            interval_minutes: 60,
            labels: [],
            repository: "",
            user_id: "",
            assignee_ids: [],
            backend: "codex",
          },
        },
      ],
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
          source_server_name: "Source Office",
          source_user_id: "source-person-id",
          disabled: false,
          remote_desktop: false,
          adb_bridge: false,
        },
        { id: "alice", username: "alice", nickname: "Alice", disabled: false },
        ...Array.from({ length: 12 }, (_, index) => ({
          id: `retired-${index + 1}`,
          username: `retired-${index + 1}`,
          nickname: `Retired person ${index + 1}`,
          disabled: true,
        })),
      ],
    };
    window.savedPeople = [];
    window.deletedPeople = [];
    window.mygo = {
      callWith(method, args, { signal } = {}) { signal?.throwIfAborted(); return this.call(method, ...args); },
      on: () => () => {},
      async call(method, command) {
        if (method === "DesktopSettings.Snapshot") return structuredClone(data);
        if (method === "DesktopSettings.Action" && command.kind === "navigate")
          return true;
        if (
          method === "DesktopSettings.Execute" &&
          command.kind === "delete-person"
        ) {
          const person = data.users.find(
            (person) => person.id === command.target,
          );
          if (!person?.disabled)
            throw new Error("Cannot delete an enabled account");
          window.deletedPeople.push(command.target);
          data.users = data.users.filter(
            (person) => person.id !== command.target,
          );
          return structuredClone(data);
        }
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
    name: /External visitor · Source Office.*Federated sign-in$/,
  });
  await page
    .getByText(/External people appear after their first trusted sign-in/)
    .waitFor();
  await page
    .getByText("Federation source: Source Office", { exact: true })
    .waitFor();
  await page.locator('nav button[data-section="Projects"]').click();
  await page.getByRole("button", { name: /Visitor project/ }).click();
  await page
    .getByRole("switch", {
      name: "Users allowed to use this project · External visitor · Source Office",
      exact: true,
    })
    .waitFor();
  await page
    .getByRole("group", {
      name: "External visitor · Source Office · In-app session permissions",
      exact: true,
    })
    .waitFor();
  await page.getByRole("button", { name: "Back to list" }).click();
  await page.locator('nav button[data-section="People"]').click();
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
    .getByText("Federation source: Source Office", { exact: false })
    .waitFor();
  await editor.getByText(/managed by its source server/).waitFor();
  await editor.getByText("source-server-id", { exact: false }).waitFor();
  await editor.getByText("source-person-id", { exact: true }).waitFor();
  await editor
    .getByText("External visitor · Source Office", { exact: true })
    .waitFor();
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
  await editor
    .getByText("Updated by source · Source Office", { exact: true })
    .waitFor();
  assert.deepEqual(await page.evaluate(() => window.savedPeople), [
    { id: "visitor", disabled: true, remote_desktop: true, adb_bridge: true },
  ]);

  await page.getByRole("button", { name: "Back to list" }).click();
  const disabledPeople = page.getByRole("region", {
    name: "Disabled people",
    exact: true,
  });
  const disabledSummary = page
    .locator("summary")
    .filter({ hasText: "Disabled people" });
  await disabledSummary.waitFor();
  assert.equal(await disabledSummary.textContent(), "Disabled people");
  await disabledPeople.waitFor({ state: "hidden" });
  await page.getByText("13 users", { exact: true }).waitFor({ state: "hidden" });
  await mkdir(join(root, "../build/artifacts/people-access"), {
    recursive: true,
  });
  await page.screenshot({
    path: join(
      root,
      "../build/artifacts/people-access/disabled-people-collapsed.png",
    ),
    fullPage: true,
  });
  await disabledSummary.click();
  await disabledPeople.getByText("13 users", { exact: true }).waitFor();
  await disabledSummary.press("Space");
  await disabledPeople.waitFor({ state: "hidden" });
  await page.getByText("13 users", { exact: true }).waitFor({ state: "hidden" });
  await disabledSummary.press("Space");
  await disabledPeople
    .getByText("Federated sign-in · Disabled", { exact: true })
    .waitFor();
  assert.equal(
    await disabledPeople
      .locator("..")
      .evaluate((group) => getComputedStyle(group).borderStyle),
    "none",
  );
  assert.equal(await disabledPeople.getByRole("listitem").count(), 13);
  assert.equal(
    await page
      .getByRole("region", { name: "User", exact: true })
      .locator("li")
      .count(),
    1,
  );
  await page.locator('nav button[data-section="Projects"]').click();
  await page.getByRole("button", { name: /Visitor project/ }).click();
  assert.equal(
    await page
      .getByRole("switch", {
        name: /Users allowed to use this project · Updated by source/,
      })
      .count(),
    0,
  );
  await page
    .getByRole("switch", {
      name: "Users allowed to use this project · Alice",
      exact: true,
    })
    .waitFor();
  await page.locator('nav button[data-section="People"]').click();
  await disabledPeople.getByText("13 users", { exact: true }).waitFor();
  const deletePerson = disabledPeople.getByRole("button", {
    name: "Delete user Updated by source · Source Office",
    exact: true,
  });
  await deletePerson.click();
  const confirmation = page.getByRole("group", {
    name: "Delete user Updated by source · Source Office?",
    exact: true,
  });
  await confirmation.waitFor();
  assert.deepEqual(
    await confirmation.evaluate((element) => {
      const person = element.closest("li");
      const details = element.getBoundingClientRect();
      const row = person
        .querySelector(".settings-list-item")
        .getBoundingClientRect();
      const nextPerson = person.nextElementSibling.getBoundingClientRect();
      const content = document
        .getElementById("settings-content")
        .getBoundingClientRect();
      return {
        person: person.querySelector("strong").textContent,
        belowPerson: details.top >= row.bottom,
        beforeNextPerson: details.bottom <= nextPerson.top,
        visible: details.top >= content.top && details.bottom <= content.bottom,
      };
    }),
    {
      person: "Updated by source · Source Office",
      belowPerson: true,
      beforeNextPerson: true,
      visible: true,
    },
  );
  await confirmation
    .getByRole("button", { name: "Cancel", exact: true })
    .click();
  await confirmation.waitFor({ state: "hidden" });
  assert.deepEqual(await page.evaluate(() => window.deletedPeople), []);
  await disabledPeople
    .getByRole("button", { name: "Delete user Retired person 1", exact: true })
    .click();
  const otherConfirmation = disabledPeople.getByRole("group", {
    name: "Delete user Retired person 1?",
    exact: true,
  });
  await otherConfirmation.waitFor();
  await deletePerson.click();
  await otherConfirmation.waitFor({ state: "hidden" });
  await confirmation.waitFor();
  await mkdir(join(root, "../build/artifacts/people-access"), {
    recursive: true,
  });
  await page.screenshot({
    path: join(root, "../build/artifacts/people-access/disabled-people.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 900, height: 780 });
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  assert.equal(
    await page
      .locator("#settings-content")
      .evaluate((element) => element.scrollWidth <= element.clientWidth),
    true,
  );
  await page.screenshot({
    path: join(
      root,
      "../build/artifacts/people-access/disabled-people-compact.png",
    ),
    fullPage: true,
  });
  await confirmation
    .getByRole("button", { name: "Delete", exact: true })
    .click();
  await deletePerson.waitFor({ state: "hidden" });
  await confirmation.waitFor({ state: "hidden" });
  assert.equal(await disabledPeople.getByRole("listitem").count(), 12);
  assert.deepEqual(await page.evaluate(() => window.deletedPeople), [
    "visitor",
  ]);
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
