import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFile } from "node:fs/promises";
import { UserAvatar } from "../.test-build/user-avatar.js";
import { avatarDataURI } from "../.test-build/avatar.js";
import {
  SettingsBack,
  SettingsList,
} from "../.test-build/components/web/src/settings-list.js";

test("management lists render full-row buttons in a semantic list, not inline editor tabs", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "People",
      summary: "2 people",
      empty: "No people yet.",
      onSelect() {},
      items: [
        {
          id: "one",
          title: "First Person",
          description: "first-user",
          status: "Enabled",
          icon: createElement("svg"),
        },
        {
          id: "two",
          title: "Second Person",
          description: "second-user",
          status: "Disabled",
          icon: createElement("svg"),
        },
      ],
      action: createElement("button", { type: "button" }, "New person"),
    }),
  );
  assert.match(html, /section[^>]*aria-label="People"/);
  assert.match(html, /<ul class="settings-items">/);
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
  assert.equal((html.match(/class="settings-list-item"/g) ?? []).length, 2);
  assert.match(html, /type="button"[^>]*data-entity-id="one"/);
  for (const value of [
    "First Person",
    "first-user",
    "Enabled",
    "Second Person",
    "Disabled",
    "New person",
  ])
    assert.ok(html.includes(value));
  assert.doesNotMatch(html, /<form|<input|settings-entity-list/);
});

test("empty management lists keep the create action visible and have no editor", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "Projects",
      summary: "0 projects",
      empty: "No projects yet.",
      items: [],
      onSelect() {},
      action: createElement("button", { type: "button" }, "New project"),
    }),
  );
  assert.match(html, /No projects yet\./);
  assert.match(html, /New project/);
  assert.doesNotMatch(html, /<ul|<form|<input/);
});

test("people list rows render the user's selected chat avatar before their name", () => {
  const person = {
    id: "person",
    username: "alice",
    nickname: "Alice",
    avatar: { style: "thumbs", seed: "alice-custom-avatar" },
  };
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "People",
      summary: "1 person",
      empty: "Empty",
      onSelect() {},
      items: [
        {
          id: person.id,
          title: person.nickname,
          description: person.username,
          status: "Enabled",
          icon: createElement(UserAvatar, { person, size: 34 }),
        },
      ],
    }),
  );
  assert.ok(html.includes(`src="${avatarDataURI(person)}"`));
  assert.match(
    html,
    /settings-list-icon[^>]*aria-hidden="true"><img class="user-avatar"/,
  );
  assert.match(html, /width="34" height="34" alt=""/);
  assert.ok(
    html.indexOf('class="user-avatar"') < html.indexOf("<strong>Alice"),
  );
});

test("list rows escape local names and paths, and show long metadata without editable fields", () => {
  const path = "/Users/test/projects/very-long-project-directory";
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "Projects",
      summary: "1 project",
      empty: "Empty",
      onSelect() {},
      items: [
        {
          id: "project",
          title: "<script>not code</script>",
          description: path,
          status: "Archived",
          icon: null,
        },
      ],
    }),
  );
  assert.ok(html.includes(`title="${path}"`));
  assert.match(html, /&lt;script&gt;not code&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>|<input/);
});

test("editors provide a non-submit back action", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsBack, { label: "Back to list", onClick() {} }),
  );
  assert.match(html, /type="button" class="settings-back"/);
  assert.match(html, /Back to list/);
});

test("people, projects and agents share the list-to-editor flow", async () => {
  const pages = await readFile(
    new URL("../src/settings-pages.tsx", import.meta.url),
    "utf8",
  );
  const projects = await readFile(
    new URL("../src/settings-projects.tsx", import.meta.url),
    "utf8",
  );
  const people = pages.slice(
    pages.indexOf("export function PeoplePage"),
    pages.indexOf("export function AgentsPage"),
  );
  const agents = pages.slice(
    pages.indexOf("export function AgentsPage"),
    pages.indexOf("function AgentCard"),
  );
  for (const source of [people, projects, agents]) {
    assert.match(source, /<SettingsList/);
    assert.match(source, /<SettingsBack/);
    assert.doesNotMatch(source, /settings-entity-list/);
  }
  assert.match(people, /if \(!editing\)/);
  assert.match(projects, /if \(!editing\)/);
  assert.match(agents, /useState<Backend>\(\)/);
  assert.match(people, /icon: <UserAvatar person=\{person\} size=\{34\}/);
  assert.match(
    agents,
    /icon: <AgentIcon backend=\{agent.backend\} size=\{20\} decorative/,
  );
  const online = pages.slice(
    pages.indexOf("export function OnlinePage"),
    pages.indexOf("export function AppearancePage"),
  );
  assert.match(online, /icon=\{<UserAvatar person=\{person\} size=\{34\}/);
});

test("global GitHub CLI settings follow the project list and never appear in a project editor", async () => {
  const projects = await readFile(
    new URL("../src/settings-projects.tsx", import.meta.url),
    "utf8",
  );
  const overview = projects.slice(
    projects.indexOf("if (!editing)"),
    projects.indexOf("<SettingsBack"),
  );
  const editor = projects.slice(projects.indexOf("<SettingsBack"));
  assert.match(
    overview,
    /<SettingsList[\s\S]*className="settings-github-cli"[\s\S]*<Card title="GitHub CLI">/,
  );
  assert.match(overview, /help=\{data.github_message\}/);
  assert.match(overview, /kind: "save-github", path: githubPath/);
  assert.match(overview, /kind: "check-github"/);
  assert.doesNotMatch(
    editor,
    /settings-github-cli|githubPath|save-github|check-github|data.github_message/,
  );
  assert.match(editor, /className="icon"[\s\S]*?<X size=\{16\}/);
});

test("GitHub CLI path settings start collapsed behind the status row's rightmost action", async () => {
  const projects = await readFile(
    new URL("../src/settings-projects.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    projects,
    /\[githubPathOpen, setGitHubPathOpen\] = useState\(false\)/,
  );
  const github = projects.slice(
    projects.indexOf('className="settings-github-cli"'),
    projects.indexOf("<SettingsBack"),
  );
  const status = github.slice(0, github.indexOf("</Row>"));
  assert.match(status, /help=\{data.github_message\}/);
  assert.match(
    status,
    /kind: "check-github"[\s\S]*aria-expanded=\{githubPathOpen\}[\s\S]*aria-controls="github-cli-path-settings"/,
  );
  assert.match(
    status,
    /onClick=\{\(\) => setGitHubPathOpen\(\(open\) => !open\)\}/,
  );
  assert.match(status, /\{t\("Path settings"\)\}\s*<\/button>\s*$/);
  assert.match(
    github,
    /<div id="github-cli-path-settings" hidden=\{!githubPathOpen\}>[\s\S]*<PathField[\s\S]*\{t\("Save GitHub CLI path"\)\}/,
  );
  assert.match(github, /\{githubPathOpen && \(\s*<Hint>/);
});
