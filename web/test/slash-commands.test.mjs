import { test } from "node:test";
import assert from "node:assert/strict";
import {
  goalSuggestion,
  parseGoalCommand,
} from "../.test-build/slash-commands.js";

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
