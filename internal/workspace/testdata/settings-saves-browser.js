(async () => {
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const waitFor = async (check, label) => {
    for (let i = 0; i < 100; i++) {
      if (check()) return;
      await pause(30);
    }
    throw new Error("Timed out: " + label);
  };
  const fixture = window.smokeSettings;
  const page = section => document.querySelector(`[data-page="${section}"]`);
  const field = (section, label) => page(section).querySelector(`[aria-label="${label}"]`);
  const row = id => document.querySelector(`[data-settings-save="${id}"]`);
  const idle = () => !document.querySelector('.settings-page-controls[aria-busy="true"]');
  const navigate = async section => {
    document.querySelector(`[data-section="${section}"]`).click();
    await waitFor(() => !page(section).hidden, section);
  };
  const set = (input, value) => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const act = async (id, action, gone = true) => {
    const button = document.querySelector(`[data-${action}-settings-save="${id}"]`);
    if (!button || button.disabled) throw new Error(`Unavailable ${action}: ${id}`);
    button.click();
    if (gone) await waitFor(() => !row(id) && idle(), `${action}: ${id}`);
  };
  const saved = kind => fixture.commands.filter(command => command.kind === kind);
  await waitFor(() => page("People")?.querySelector("[data-new-entity]"), "people list");
  if (document.querySelector('.settings-restart-bar')) throw new Error("Clean settings showed pending actions");
  page("People").querySelector("[data-new-entity]").click();
  await waitFor(() => row("person"), "new person actions");

  await navigate("Agents");
  page("Agents").querySelector('[data-entity-id="codex"]').click();
  await waitFor(() => page("Agents").querySelector(".settings-path input"), "agent editor");
  const agentInput = page("Agents").querySelector(".settings-path input");
  set(agentInput, "/fixture/codex-custom");
  page("Agents").querySelector('[role="switch"]').click();
  await waitFor(() => row("agent-codex"), "agent actions");

  await navigate("Projects");
  page("Projects").querySelector('[aria-controls="github-cli-path-settings"]').click();
  const githubInput = page("Projects").querySelector('#github-cli-path-settings input');
  set(githubInput, "/fixture/gh-custom");
  await waitFor(() => row("github"), "GitHub actions");
  page("Projects").querySelector('[data-entity-id="select-fixture"]').click();
  await waitFor(() => field("Projects", "项目名称"), "project editor");
  set(field("Projects", "项目名称"), "待保存项目");
  await waitFor(() => row("project"), "project actions");
  const original = structuredClone(fixture.snapshot.settings);
  await mygo.call("DesktopSettings.Execute", { kind: "save-settings", settings: { ...original, port: 29876 } });
  fixture.sectionListeners.forEach(listener => listener("Projects"));
  await waitFor(() => document.querySelector('[data-service-change="web"]'), "Web pending changes");
  const bar = document.querySelector('.settings-restart-bar');
  if (bar.querySelectorAll('[data-settings-save]').length !== 4 || bar.querySelectorAll('[data-service-change]').length !== 1) throw new Error("Save forms did not coexist with service changes");
  if ([...document.querySelectorAll('button[type="submit"][hidden]')].some(button => button.getBoundingClientRect().width > 0)) throw new Error("Hidden form submit button is visible");
  const bounds = bar.getBoundingClientRect();
  if (bounds.top < 0 || bounds.bottom > innerHeight || innerHeight - bounds.bottom > 24 || bounds.right > innerWidth) throw new Error("Save bar escaped viewport");
  document.querySelector('.settings-main').scrollTop = 100000;
  await pause(40);
  if (Math.abs(bar.getBoundingClientRect().top - bounds.top) > 1) throw new Error("Save bar moved on scroll");
  const capture = await fetch('/capture', { method: 'POST' });
  if (!capture.ok) throw new Error("Capture failed");

  // Saving from a different page must reveal and validate the actual form.
  await act("person", "apply", false);
  await waitFor(() => !page("People").hidden && document.activeElement === field("People", "用户名"), "required-field validation");
  if (saved("save-person").length) throw new Error("Invalid new account was submitted");
  set(field("People", "用户名"), "fixture-user");
  set(field("People", "显示名称"), "测试成员");
  set(field("People", "密码"), "fixture-password-only");
  await pause(40);
  await navigate("Network");
  fixture.failNextCommand = "save-person";
  await act("person", "apply", false);
  await waitFor(() => saved("save-person").length === 1 && idle(), "failed save");
  if (!row("person") || field("People", "密码").value !== "fixture-password-only" || fixture.snapshot.users.length) throw new Error("Failed save lost the draft or persisted a user");
  await act("person", "apply");
  if (fixture.snapshot.users.length !== 1 || field("People", "密码").value) throw new Error("Saved account did not clear the password");
  if (!row("project") || !row("github") || !row("agent-codex")) throw new Error("Saving person dropped unrelated drafts");
  await act("project", "cancel");
  if (field("Projects", "项目名称").value !== "Select fixture" || saved("save-project").length) throw new Error("Project cancel saved or failed to restore");
  await act("github", "cancel");
  await navigate("Projects");
  page("Projects").querySelector('.settings-back').click();
  await waitFor(() => page("Projects").querySelector('#github-cli-path-settings input'), "project list restored");
  if (page("Projects").querySelector('#github-cli-path-settings input').value !== "") throw new Error("GitHub cancel did not restore path");
  set(page("Projects").querySelector('#github-cli-path-settings input'), "/fixture/gh-saved");
  await waitFor(() => row("github"), "GitHub dirty again");
  await act("github", "apply");
  if (fixture.snapshot.github_path !== "/fixture/gh-saved") throw new Error("GitHub path was not saved");
  await act("agent-codex", "cancel");
  await navigate("Agents");
  if (page("Agents").querySelector('.settings-path input').value !== "" || page("Agents").querySelector('[role="switch"]').getAttribute('aria-checked') !== "true") throw new Error("Agent cancel failed to restore fields");
  set(page("Agents").querySelector('.settings-path input'), "/fixture/codex-saved");
  await waitFor(() => row("agent-codex"), "agent dirty again");
  await act("agent-codex", "apply");
  if (fixture.snapshot.agents[0].executable !== "/fixture/codex-saved") throw new Error("Agent path was not saved");
  set(page("Agents").querySelector('.settings-path input'), "/fixture/temporary");
  await waitFor(() => row("agent-codex"), "agent changed after save");
  await act("agent-codex", "cancel");
  if (page("Agents").querySelector('.settings-path input').value !== "/fixture/codex-saved") throw new Error("Agent cancel used a stale baseline");

  await navigate("Projects");
  page("Projects").querySelector('[data-entity-id="select-fixture"]').click();
  await waitFor(() => field("Projects", "项目名称"), "project reopened");
  set(field("Projects", "项目名称"), "已保存项目");
  await waitFor(() => row("project"), "project changed again");
  await act("project", "apply");
  if (fixture.snapshot.projects[0].name !== "已保存项目") throw new Error("Project was not saved");
  await navigate("People");
  set(field("People", "显示名称"), "暂存名称");
  set(field("People", "密码"), "fixture-password-change");
  await waitFor(() => row("person"), "existing person dirty");
  await act("person", "cancel");
  if (field("People", "显示名称").value !== "测试成员" || field("People", "密码").value) throw new Error("Person cancel did not restore saved fields and clear password");
  page("People").querySelector('.settings-back').click();
  await waitFor(() => page("People").querySelector('[data-new-entity]'), "people list restored");
  page("People").querySelector('[data-new-entity]').click();
  await waitFor(() => row("person"), "another new person");
  await act("person", "cancel");
  if (!page("People").querySelector('[data-new-entity]') || fixture.snapshot.users.length !== 1) throw new Error("Cancel new account failed");
  document.querySelector('[data-cancel-service-change="web"]').click();
  await waitFor(() => !document.querySelector('.settings-restart-bar') && idle(), "all pending actions cleared");
  await fetch('/report', { method: 'POST', body: JSON.stringify({ ok: true, width: innerWidth }) });
})().catch(async error => {
  await fetch('/report', { method: 'POST', body: JSON.stringify({ error: String(error) }) });
});
