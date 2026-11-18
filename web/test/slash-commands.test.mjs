import { test } from "node:test";
import assert from "node:assert/strict";
import {
  goalSuggestion,
  parseGoalCommand,
  commandSuggestions,
  findSlashCommand,
  parseSlashCommand,
} from "../.test-build/slash-commands.js";

const commands = [
  { name: "goal", description: "Set a goal", builtin: true, kind: "prompt" },
  {
    name: "code-review",
    description: "Review changes",
    argumentHint: "[target]",
    aliases: ["review"],
    builtin: true,
    kind: "prompt",
  },
  {
    name: "project:check",
    description: "Run project checks",
    builtin: false,
    kind: "prompt",
  },
];

test("commands filter by name, alias and description without matching ordinary text or paths", () => {
  assert.equal(commandSuggestions("/", commands).length, 3);
  assert.deepEqual(
    commandSuggestions("/rev", commands).map((item) => item.name),
    ["code-review"],
  );
  assert.deepEqual(
    commandSuggestions("/checks", commands).map((item) => item.name),
    ["project:check"],
  );
  assert.deepEqual(commandSuggestions("/unknown", commands), []);
  for (const text of [" /goal", "/goal task", "/tmp/file", "explain /goal"])
    assert.equal(commandSuggestions(text, commands), null);
  assert.equal(findSlashCommand(commands, "review").name, "code-review");
  assert.equal(findSlashCommand(commands, "unknown"), undefined);
});

test("suggestions come only from the selected agent's catalog", () => {
  const codex = ["goal", "plan", "review"].map((name) => ({
    name,
    description: name,
    builtin: true,
    kind: "prompt",
  }));
  assert.deepEqual(
    commandSuggestions("/", codex).map((command) => command.name),
    ["goal", "plan", "review"],
  );
  assert.deepEqual(
    commandSuggestions("/", commands).map((command) => command.name),
    ["code-review", "goal", "project:check"],
  );
});

test("skills search display names and stay after ordinary commands in keyboard order", () => {
  const skills = [
    {
      name: "z:inspect",
      displayName: "Analyze project",
      description: "Check project health",
      scope: "repo",
      kind: "skill",
    },
    {
      name: "docs",
      displayName: "Documentation",
      description: "Write guides",
      scope: "user",
      kind: "skill",
    },
  ];
  const catalog = [...skills, ...commands];
  assert.deepEqual(
    commandSuggestions("/", catalog).map((command) => command.name),
    ["code-review", "goal", "project:check", "z:inspect", "docs"],
  );
  assert.deepEqual(commandSuggestions("/analyze", catalog), [skills[0]]);
  assert.deepEqual(commandSuggestions("/guides", catalog), [skills[1]]);
  assert.equal(findSlashCommand(catalog, "z:inspect"), skills[0]);
});

test("Code review uses the shared display name while preserving each agent's invocation token", () => {
  for (const name of ["review", "code-review"]) {
    const review = { name, displayName: "Code review", description: "Review changes", builtin: true, kind: "prompt" };
    const catalog = [review, { name: "goal", description: "Set a goal", builtin: true, kind: "prompt" }];
    assert.deepEqual(commandSuggestions("/code", catalog), [review]);
    assert.equal(findSlashCommand(catalog, review.name), review);
    assert.deepEqual(parseSlashCommand(`/${review.name} main`), { name, arguments: "main" });
  }
});

test("slash parsing retains multiline arguments and plugin skill names", () => {
  assert.deepEqual(parseSlashCommand(" /project:check first\nsecond "), {
    name: "project:check",
    arguments: "first\nsecond",
  });
  for (const text of ["", "/", "/tmp/file", "explain /goal", "`/goal`"])
    assert.equal(parseSlashCommand(text), null);
});

test("slash suggestions match a leading command, not paths or ordinary text", () => {
  for (const input of ["/", "/g", "/go", "/goa", "/goal"])
    assert.equal(goalSuggestion(input), true, input);
  for (const input of [
    "",
    "/goals",
    "/goal/path",
    "explain /goal",
    "/goal ",
    "/goal fix",
    " /goal",
    "/unknown",
  ])
    assert.equal(goalSuggestion(input), false, input);
});

test("goal commands distinguish native controls from work", () => {
  assert.deepEqual(parseGoalCommand(" \n/goal\t"), { action: "get" });
  for (const alias of ["clear", "stop", "off", "reset", "none", "cancel"])
    assert.deepEqual(parseGoalCommand(`/goal ${alias}`), { action: "clear" });
  assert.deepEqual(parseGoalCommand("/goal\u3000修好测试\nand verify"), {
    action: "set",
    objective: "修好测试\nand verify",
  });
  assert.deepEqual(parseGoalCommand("/goal clear the tests"), {
    action: "set",
    objective: "clear the tests",
  });
  for (const input of [
    "",
    "/",
    "/goals",
    "/goal/file",
    "/Goal",
    "`/goal`",
    "explain /goal",
  ])
    assert.equal(parseGoalCommand(input), null, input);
});
