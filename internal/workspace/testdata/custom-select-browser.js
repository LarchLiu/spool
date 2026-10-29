async function checkCustomSelects(report) {
  const select = async (doc, label, value) => {
    const trigger = doc.querySelector(`.custom-select-trigger[aria-label="${label}"]`);
    trigger.scrollIntoView({ block: "center", behavior: "instant" });
    await pause(60);
    trigger.click();
    await waitFor(() => doc.querySelector('.custom-select-menu'), label + " popup");
    const popup = doc.querySelector('.custom-select-menu');
    const option = [...popup.querySelectorAll('[role="option"]')].find((item) => item.dataset.value === value);
    if (!option || option.disabled) throw new Error("Unavailable option: " + label + " / " + value);
    const bounds = popup.getBoundingClientRect();
    const view = doc.defaultView;
    if (bounds.left < 0 || bounds.top < 0 || bounds.right > view.innerWidth || bounds.bottom > view.innerHeight || !popup.contains(doc.elementFromPoint(bounds.left + 10, bounds.top + 10)))
      throw new Error("Popup clipped or outside viewport: " + label);
    option.click();
    await waitFor(() => !doc.querySelector('.custom-select-menu'), label + " dismissed");
  };

  document.querySelector('[aria-label="Appearance and language"]').click();
  await waitFor(() => document.querySelector('.appearance-dialog[open]'), "appearance dialog");
  const dialog = document.querySelector('.appearance-dialog');
  await select(document, "Language", "ja");
  await waitFor(() => dialog.querySelector('.custom-select-trigger span')?.textContent === "日本語" && !dialog.querySelector('.custom-select-trigger').disabled, "language saved");
  const trigger = dialog.querySelector('.custom-select-trigger');
  trigger.click();
  await waitFor(() => document.querySelector('.custom-select-menu [aria-selected="true"]') === document.activeElement, "selected option focus");
  document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
  if (document.activeElement.dataset.value !== "system") throw new Error("Home did not focus first option");
  document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "e", bubbles: true }));
  if (document.activeElement.dataset.value !== "en") throw new Error("Typeahead did not find English");
  document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await waitFor(() => !document.querySelector('.custom-select-menu'), "escape dismisses popup");
  report.selectModal = dialog.matches(":modal") && document.activeElement === trigger;
  trigger.click();
  await waitFor(() => document.querySelector('.custom-select-menu [data-value="en"]'), "restore English option");
  document.querySelector('.custom-select-menu [data-value="en"]').click();
  await waitFor(() => dialog.querySelector('.custom-select-trigger span')?.textContent === "English" && !dialog.querySelector('.custom-select-trigger').disabled, "English restored");
  dialog.querySelector('.dialog-heading button').click();

  const frame = document.createElement("iframe");
  frame.src = "/smoke/settings";
  frame.style.cssText = "position:fixed;inset:0;width:100%;height:100%;z-index:200;border:0";
  document.body.append(frame);
  try {
    await waitFor(() => frame.contentDocument?.querySelector('[data-page="Appearance"] .custom-select-trigger'), "settings UI fixture");
    const doc = frame.contentDocument;
    await select(doc, "Language", "en");
    await waitFor(() => frame.contentWindow.smokeSettings.snapshot.preferences.language === "en" && !doc.querySelector('.settings-content').disabled, "settings language saved");
    report.selectSettingsLanguage = !doc.querySelector("select");

    doc.querySelector('[data-section="Projects"]').click();
    await waitFor(() => !doc.querySelector('.settings-content').disabled, "projects ready");
    doc.querySelector('[data-page="Projects"] [data-entity-id="select-fixture"]').click();
    await waitFor(() => doc.querySelector('[aria-label="Automation model"]'), "automation editor");
    await select(doc, "Automation model", "codex-fixed");
    const effort = () => doc.querySelector('[aria-label="Automation reasoning level"]');
    await waitFor(() => effort().textContent === "Default", "incompatible effort reset");
    effort().click();
    await waitFor(() => doc.querySelector('.custom-select-menu'), "fixed reasoning menu");
    report.selectModelEffort = doc.querySelectorAll('.custom-select-menu [role="option"]').length === 1;
    doc.querySelector('.custom-select-menu [role="option"]').click();
    await select(doc, "Automation model", "");
    await select(doc, "Automation reasoning level", "high");
    await select(doc, "Automation agent", "claude");
    await waitFor(() => effort().textContent === "Default" && doc.querySelector('[aria-label="Automation model"]').textContent === "Default", "agent resets choices");
    const modelTrigger = doc.querySelector('[aria-label="Automation model"]');
    modelTrigger.click();
    await waitFor(() => doc.querySelector('.custom-select-menu'), "Claude models");
    report.selectAgentScoped = ![...doc.querySelectorAll('.custom-select-menu [role="option"]')].some((option) => option.dataset.value.startsWith("codex"));
    doc.querySelector('.custom-select-menu [data-value="claude-a"]').click();
    await select(doc, "Automation reasoning level", "max");
    await select(doc, "Automation permissions", "plan");
    const submit = doc.querySelector('[data-page="Projects"] button[type="submit"]');
    submit.click();
    await waitFor(() => frame.contentWindow.smokeSettings.commands.some((command) => command.kind === "save-project"), "automation saved");
    const saved = frame.contentWindow.smokeSettings.commands.findLast((command) => command.kind === "save-project").project.issues;
    report.selectAutomationSaved = saved.backend === "claude" && saved.model === "claude-a" && saved.effort === "max" && saved.permission === "plan";

    await waitFor(() => !doc.querySelector('.settings-content').disabled, "save complete");
    modelTrigger.scrollIntoView({ block: "center", behavior: "instant" });
    await pause(60);
    modelTrigger.click();
    await waitFor(() => doc.querySelector('.custom-select-menu'), "disabled fieldset menu");
    doc.querySelector('.settings-content').disabled = true;
    await waitFor(() => !doc.querySelector('.custom-select-menu'), "fieldset disables open menu");
    report.selectDisabled = modelTrigger.matches(":disabled");
    doc.querySelector('.settings-content').disabled = false;
  } finally {
    frame.remove();
  }
}
