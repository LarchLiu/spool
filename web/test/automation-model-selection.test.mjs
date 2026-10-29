import assert from "node:assert/strict";
import test from "node:test";
import {
  automationModelChoices,
  chooseAutomationModel,
} from "../.test-build/components/web/src/automation-model-selection.js";

const entry = (model, levels) => ({
  model,
  displayName: model,
  supportedReasoningEfforts: levels.map((reasoningEffort) => ({
    reasoningEffort,
  })),
});
const codex = {
  backend: "codex",
  catalog: {
    defaultModel: "shared",
    models: [entry("shared", ["low", "high"]), entry("fixed", [])],
  },
};
const claude = {
  backend: "claude",
  catalog: {
    defaultModel: "shared",
    models: [entry("shared", ["medium", "max"])],
  },
};

test("automation lists and reasoning levels stay scoped to the selected agent and model", () => {
  assert.deepEqual(automationModelChoices(codex, "shared").efforts, [
    "low",
    "high",
  ]);
  assert.deepEqual(automationModelChoices(claude, "shared").efforts, [
    "medium",
    "max",
  ]);
  assert.deepEqual(automationModelChoices(codex, "fixed").efforts, []);
  assert.equal(automationModelChoices(codex, "").selected.model, "shared");
  assert.deepEqual(automationModelChoices(codex, "").efforts, ["low", "high"]);
});

test("changing automation models preserves supported efforts and resets incompatible levels", () => {
  assert.deepEqual(chooseAutomationModel(codex, "shared", "high"), {
    model: "shared",
    effort: "high",
  });
  assert.deepEqual(chooseAutomationModel(codex, "fixed", "high"), {
    model: "fixed",
    effort: "",
  });
  assert.deepEqual(chooseAutomationModel(claude, "shared", "high"), {
    model: "shared",
    effort: "",
  });
  assert.deepEqual(chooseAutomationModel(codex, "", "high"), {
    model: "",
    effort: "high",
  });
});

test("unavailable catalogs and unknown defaults never invent models or reasoning choices", () => {
  for (const agent of [
    undefined,
    { catalog: { models: null } },
    { catalog: { models: [], defaultModel: "missing" } },
  ]) {
    assert.deepEqual(automationModelChoices(agent, "saved-model").models, []);
    assert.deepEqual(automationModelChoices(agent, "").efforts, []);
    assert.deepEqual(chooseAutomationModel(agent, "", "high"), {
      model: "",
      effort: "",
    });
  }
  assert.deepEqual(
    automationModelChoices({ catalog: { models: codex.catalog.models } }, "")
      .efforts,
    [],
  );
});

test("catalog duplicates and empty levels do not create duplicate options", () => {
  const agent = {
    catalog: {
      models: [
        entry("shared", ["", "high", "high"]),
        entry("shared", ["low"]),
        entry("", []),
      ],
    },
  };
  const choices = automationModelChoices(agent, "shared");
  assert.equal(choices.models.length, 1);
  assert.deepEqual(choices.efforts, ["high"]);
});
