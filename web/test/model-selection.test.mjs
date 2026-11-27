import { test } from "node:test";
import assert from "node:assert/strict";
import {
  chooseModel,
  effortChoices,
  chatSelection,
  ModelSelectionMemory,
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

test("switching agents restores each model's effort, including matching model IDs", () => {
  const memory = new ModelSelectionMemory();
  const codexAgent = {
    ...codex,
    catalog: { ...codex.catalog, models: [claude.catalog.models[0]] },
  };
  memory.remember({ backend: "claude", model: "shared-id", effort: "high" });
  let selected = memory.choose(codexAgent, "shared-id");
  assert.equal(selected.effort, "high");
  memory.remember({ ...selected, effort: "low" });
  selected = memory.choose(claude, "shared-id");
  assert.equal(selected.effort, "high");
  memory.remember(selected);
  assert.equal(memory.choose(codexAgent, "shared-id").effort, "low");
  assert.equal(memory.choose(claude, "shared-id").effort, "high");
});

test("remembered efforts are per model and preserve an explicit Claude default", () => {
  const memory = new ModelSelectionMemory();
  const agent = {
    ...claude,
    catalog: {
      ...claude.catalog,
      models: [
        claude.catalog.models[0],
        { ...claude.catalog.models[0], model: "other-model" },
      ],
    },
  };
  memory.remember({ backend: "claude", model: "shared-id", effort: "" });
  memory.remember({ backend: "claude", model: "other-model", effort: "low" });
  assert.equal(memory.choose(agent, "shared-id").effort, "");
  assert.equal(memory.choose(agent, "other-model").effort, "low");
  memory.remember({ backend: "claude", model: "shared-id", effort: "high" });
  assert.equal(memory.choose(agent, "shared-id").effort, "high");
  assert.equal(memory.choose(agent, "other-model").effort, "low");
});

test("catalog changes fall back when a remembered effort is no longer supported", () => {
  const memory = new ModelSelectionMemory();
  memory.remember({ backend: "claude", model: "shared-id", effort: "max" });
  assert.deepEqual(memory.choose(claude, "shared-id"), chooseModel(claude, "shared-id"));
  memory.remember({ backend: "claude", model: "fixed", effort: "high" });
  const fixed = { ...claude, catalog: { models: [{ model: "fixed", supportedReasoningEfforts: [] }] } };
  assert.equal(memory.choose(fixed, "fixed").effort, "");
  assert.deepEqual(memory.choose({ ...claude, catalog: { models: null } }, "fixed"), {
    backend: "claude", model: "fixed", effort: "",
  });
});
