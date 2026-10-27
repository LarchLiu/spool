import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import {
  chooseModel,
  effortChoices,
  chatSelection,
} from "../.test-build/model-selection.js";

const codex = {
  backend: "codex",
  catalog: {
    defaultModel: "shared-id",
    defaultEffort: "high",
    models: [{ model: "shared-id", defaultReasoningEffort: "medium" }],
  },
};
const claude = {
  backend: "claude",
  catalog: {
    defaultModel: "shared-id",
    defaultEffort: "low",
    models: [
      {
        model: "shared-id",
        defaultReasoningEffort: "high",
        supportedReasoningEfforts: [
          { reasoningEffort: "low", description: "" },
          { reasoningEffort: "high", description: "" },
        ],
      },
    ],
  },
};

test("model IDs and effort defaults are scoped to the chosen agent", () => {
  assert.deepEqual(chooseModel(codex, "shared-id"), {
    backend: "codex",
    model: "shared-id",
    effort: "high",
  });
  assert.deepEqual(chooseModel(claude, "shared-id"), {
    backend: "claude",
    model: "shared-id",
    effort: "low",
  });
});

test("chat selection restores its own agent, model and reasoning preference", () => {
  assert.deepEqual(
    chatSelection([codex, claude], {
      backend: "claude",
      model: "custom-claude",
      reasoning_effort: "high",
    }),
    { backend: "claude", model: "custom-claude", effort: "high" },
  );
  assert.deepEqual(
    chatSelection([codex, claude], {
      backend: "codex",
      model: "shared-id",
      reasoning_effort: "low",
    }),
    { backend: "codex", model: "shared-id", effort: "low" },
  );
});

test("Claude offers only advertised levels plus an explicit CLI-default reset", () => {
  const model = claude.catalog.models[0];
  assert.deepEqual(
    effortChoices("claude", model).map((item) => item.reasoningEffort),
    ["", "low", "high"],
  );
  assert.deepEqual(
    effortChoices("codex", model),
    model.supportedReasoningEfforts,
  );
  assert.deepEqual(
    effortChoices("claude", { ...model, supportedReasoningEfforts: [] }),
    [],
  );
  assert.deepEqual(effortChoices("claude"), []);
});

test("Claude does not invent a default or carry an effort to a non-adjustable model", () => {
  const agent = {
    ...claude,
    catalog: {
      ...claude.catalog,
      defaultEffort: "",
      models: [
        { ...claude.catalog.models[0], defaultReasoningEffort: "" },
        {
          model: "fixed",
          defaultReasoningEffort: "",
          supportedReasoningEfforts: [],
        },
      ],
    },
  };
  assert.equal(chooseModel(agent, "shared-id").effort, "");
  assert.equal(chooseModel(agent, "fixed").effort, "");
});

test("an undiscovered catalog still allows a safe CLI-default selection", () => {
  const missing = { ...claude, catalog: { models: null } };
  assert.deepEqual(chatSelection([missing], { backend: "claude" }), {
    backend: "claude",
    model: "",
    effort: "",
  });
});

test("existing chats offer both agent groups and explain automatic conversion", async () => {
  const menu = await readFile(new URL("../src/agent-model-menu.tsx", import.meta.url), "utf8");
  const main = await readFile(new URL("../src/main.tsx", import.meta.url), "utf8");
  assert.match(menu, /agents\.map\(/);
  assert.doesNotMatch(menu + main, /lockedBackend|next\.backend !== chat\.backend/);
  assert.match(menu, /The next message converts this conversation/);
  assert.match(menu, /disabled=\{!available\}/);
  assert.match(main, /!composerWritable \|\| chat\?\.status === "running" \|\| posting/);
});
