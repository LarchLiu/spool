(async () => {
  const report = {};
  const requests = [];
  const errors = [];
  window.addEventListener("error", event => errors.push(event.error?.stack || event.message));
  window.addEventListener("unhandledrejection", event => errors.push(String(event.reason)));
  const nativeFetch = window.fetch;
  window.fetch = async (...args) => {
    const response = await nativeFetch(...args);
    if (/\/api\/chats(?:\?|\/|$)/.test(String(args[0])) && args[1]?.method === "POST") {
      const value = await response.clone().json().catch(() => ({}));
      requests.push({ url: String(args[0]), status: response.status, id: value.id, turns: value.turns?.length, error: value.error });
    }
    return response;
  };
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const wait = async (check, label) => {
    for (let i = 0; i < 200; i++) { if (check()) return; await pause(30); }
    throw new Error("Timed out: " + label);
  };
  const input = () => document.querySelector("#chat-message");
  const options = () => [...document.querySelectorAll(".slash-command-menu [role=option]")];
  const type = async text => {
    input().focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input(), text);
    input().dispatchEvent(new Event("input", { bubbles: true }));
    await pause(80);
  };
  const key = value => input().dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true }));
  const has = text => options().some(item => {
    const title = item.querySelector("strong").textContent.trim();
    return text.startsWith("/") ? item.dataset.command === text.slice(1) : title.startsWith(text);
  });
  const checkPresentation = reviewName => {
    const review = options().find(item => item.dataset.command === reviewName);
    const goal = options().find(item => item.dataset.command === "goal");
    return review?.querySelector("strong")?.textContent === "Code review" &&
      review?.textContent.includes("Review uncommitted changes or compare against a branch") &&
      !review?.querySelector(".lucide-chevron-right") &&
      goal?.querySelector("strong")?.textContent === "Goal" &&
      goal?.textContent.includes("Set a goal to keep pursuing");
  };
  const switchAgent = async name => {
    await wait(() => document.querySelector(".picker-summary:not(:disabled)"), "model picker ready");
    document.querySelector(".picker-summary").click();
    await wait(() => document.querySelector(`.model-popover [role=group][aria-label="${name}"] button`), name);
    document.querySelector(`.model-popover [role=group][aria-label="${name}"] button`).click();
    document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    await wait(() => !document.querySelector(".model-popover"), "picker dismissed");
  };
  const checkSubagents = async backend => {
    await type("subagent-live wait-progress");
    await wait(() => document.querySelector(".send:not(:disabled)"), "subagent submission");
    key("Enter");
    await wait(() => document.querySelectorAll(".tool-task-running").length === 2 && document.querySelector(".turn:last-child")?.textContent.includes("Main report"), backend + " parallel tasks after main reply");
    await wait(() => [...document.querySelectorAll(".tool-task-running")].every(task => task.textContent.includes("Checking child files")), backend + " task summaries");
    const chatID = requests.filter(request => request.id && request.url.includes("/messages")).at(-1)?.id;
    const snapshot = await (await fetch("/api/chats/" + chatID)).json();
    const tasks = snapshot.transcript.parts.filter(part => part.tool?.kind === "task");
    if (snapshot.status !== "running" || tasks.length !== 2 || tasks.some(part => part.tool.status !== "running" || !part.tool.task?.id)) throw Error("Refresh snapshot lost child tasks");
    if (document.documentElement.scrollWidth > innerWidth + 1) throw Error("Child tasks overflow the viewport");
    if (!(await fetch("/smoke/capture?agent=" + backend + "-subagents")).ok) throw Error("Subagent screenshot failed");
    await wait(() => document.querySelector(".picker-summary:not(:disabled)"), backend + " tasks finished");
    report[backend + "Subagents"] = true;
  };
  try {
    if (!(await fetch("/smoke/viewport")).ok) throw Error("viewport setup failed");
    await wait(() => innerWidth === window.smokeWidth, "viewport width");
    await wait(() => input() && !input().disabled && document.querySelector(".permission-summary:not(:disabled)"), "composer");
    await type("/");
    await wait(() => has("/code-review") && has("/project:check"), "Claude catalog");
    report.cachedCatalog = !has("/context") && !has("/model") && !has("/compact") && !has("/simplify");
    report.claudeList = options().map(item => item.dataset.command).join(",") === "code-review,deep-research,goal,loop,plan,project:check" && document.querySelector(".slash-command-group")?.textContent === "Skills" && options().find(item => item.dataset.command === "project:check")?.textContent.includes("Project");
    report.claudePresentation = checkPresentation("code-review");
    if (!(await fetch("/smoke/capture?agent=claude")).ok) throw Error("Claude screenshot failed");
    options().find(item => item.dataset.command === "code-review").click(); await pause(40);
    report.claudeReviewSelection = input().value === "/code-review ";
    await type("/");
    key("ArrowDown"); await pause(50);
    const active = document.querySelector(".slash-command-menu [aria-selected=true]");
    const name = "/" + active.dataset.command;
    key("Tab"); await pause(50);
    report.keyboard = input().value === name + " " && !document.querySelector(".slash-command-menu");
    await type("/");
    key("Escape"); await pause(40);
    if (document.querySelector(".slash-command-menu")) throw Error("Escape did not dismiss commands");
    await switchAgent("Codex");
    await type("/");
    await wait(() => has("/review") && !has("/context"), "Codex catalog switched");
    report.agentSwitch = true;
    report.codexPresentation = checkPresentation("review");
    await wait(() => has("Inspect project") && has("Documentation"), "native skills loaded");
    const names = options().map(item => item.dataset.command);
    report.skillList = names.slice(0, 3).join(",") === "review,goal,plan" && names.length === 10 && document.querySelector(".slash-command-group")?.textContent === "Skills" && options().some(item => item.textContent.includes("Project")) && options().some(item => item.textContent.includes("Personal"));
    const rect = document.querySelector(".slash-command-menu").getBoundingClientRect();
    report.bounds = rect.left >= 0 && rect.right <= innerWidth && rect.top >= 0 && rect.bottom <= innerHeight && rect.height <= innerHeight * .5 + 1;
    if (!(await fetch("/smoke/capture")).ok) throw Error("screenshot failed");
    options().find(item => item.dataset.command === "review").click(); await pause(40);
    report.codexReviewSelection = input().value === "/review ";
    await type("/Inspect");
    await wait(() => options()[0]?.dataset.command === "inspect" && has("Inspect project"), "skill display name search");
    options()[0].click(); await pause(60);
    report.skillSelection = input().value === "/inspect " && !document.querySelector(".slash-command-menu");
    await switchAgent("Claude");
    await type("/");
    await wait(() => has("/code-review") && !has("/review") && !has("Inspect project"), "Claude catalog restored");
    await type("/plan ");
    await wait(() => document.querySelector(".send:not(:disabled)"), "permissions ready");
    key("Enter");
    await wait(() => document.querySelector(".goal-command-notice")?.textContent.includes("Plan mode enabled."), "native plan executed");
    report.nativeExecution = input().value === "";
    await wait(() => document.querySelector(".permission-summary")?.getAttribute("aria-label")?.includes("Plan") && input().value === "", "plan permissions synced");
    report.settingsSynced = true;
    report.noTurns = !document.querySelector(".turn");
    // A command can create the provider session for a previously local draft.
    const sidebarButton = document.querySelector('[aria-label="Open sidebar"]');
    sidebarButton?.click();
    await wait(() => document.querySelector(".project-new"), "new chat button");
    document.querySelector(".project-new").click();
    await wait(() => input() && !input().disabled && !document.querySelector(".goal-command-notice"), "new draft");
    await type("/plan ");
    await wait(() => document.querySelector(".send:not(:disabled)"), "new draft permissions");
    key("Enter");
    await wait(() => document.querySelector(".goal-command-notice")?.textContent.includes("Plan mode enabled.") && input().value === "", "command from new draft");
    report.draftExecution = true;
    await type("/project:check");
    await wait(() => options()[0]?.dataset.command === "project:check", "Claude skill search");
    options()[0].click(); await pause(40);
    report.claudeSkillSelection = input().value === "/project:check ";
    await type(input().value + "Check Claude browser selection");
    await wait(() => document.querySelector(".send:not(:disabled)"), "Claude skill submission ready");
    key("Enter");
    await wait(() => document.querySelector(".turn")?.textContent.includes("Hello world"), "native Claude skill response");
    await wait(() => document.querySelector(".picker-summary:not(:disabled)"), "Claude skill turn completed");
    report.claudeSkillExecution = input().value === "";
    await type("/code-review wait-progress");
    await wait(() => document.querySelector(".send:not(:disabled)"), "Claude review ready");
    key("Enter");
    await wait(() => [...document.querySelectorAll(".tool-activity-item-summary")].some(row => row.textContent.includes("/code-review") && row.textContent.includes("Running")), "foreground review running progress");
    [...document.querySelectorAll(".tool-activity-item-summary")].find(row => row.textContent.includes("/code-review") && row.textContent.includes("Running")).click();
    await wait(() => [...document.querySelectorAll(".tool-detail")].some(detail => detail.textContent.includes("Tool uses: 3") && detail.textContent.includes("Duration: 1.5s")), "native review progress details");
    report.claudeReviewProgress = true;
    if (!(await fetch("/smoke/capture?agent=claude-review-progress")).ok) throw Error("review progress screenshot failed");
    await wait(() => document.querySelector(".picker-summary:not(:disabled)"), "Claude review finished");
    const reviewTurn = [...document.querySelectorAll(".turn")].find(turn => turn.textContent.includes("/code-review wait-progress"));
    report.claudeReviewReport = reviewTurn?.textContent.includes("Hello world") && !reviewTurn.textContent.includes("PRIVATE") && (reviewTurn.textContent.match(/Hello world/g) || []).length === 1;
    await checkSubagents("claude");
    document.querySelector('[aria-label="Open sidebar"]')?.click();
    await wait(() => document.querySelector(".project-new"), "Codex new chat button");
    document.querySelector(".project-new").click();
    await wait(() => input() && !input().disabled && !document.querySelector(".goal-command-notice") && !document.querySelector(".turn"), "Codex new draft");
    await switchAgent("Codex");
    await type("/plan ");
    await wait(() => document.querySelector(".send:not(:disabled)"), "Codex plan ready");
    key("Enter");
    await wait(() => document.querySelector(".goal-command-notice")?.textContent.includes("Plan mode enabled.") && input().value === "", "native plan from new draft");
    await type("/Inspect");
    await wait(() => options()[0]?.dataset.command === "inspect" && has("Inspect project"), "Codex draft skill");
    key("Tab"); await pause(40);
    if (input().value !== "/inspect ") throw Error("keyboard skill selection failed");
    await type(input().value + "Check browser selection");
    await wait(() => document.querySelector(".send:not(:disabled)"), "skill submission ready");
    report.skillSubmit = { value: input().value, send: document.querySelector(".send").getAttribute("aria-label"), placeholder: input().placeholder };
    key("Enter");
    await wait(() => [...document.querySelectorAll(".turn")].some(turn => turn.textContent.includes("Check browser selection") && turn.textContent.includes("Hello world")), "native skill response");
    report.skillExecution = input().value === "";
    await wait(() => document.querySelector(".picker-summary:not(:disabled)"), "Codex skill finished");
    await checkSubagents("codex");
    document.querySelector('[aria-label="Open sidebar"]')?.click();
    await wait(() => [...document.querySelectorAll(".nav-chat-open")].some(button => button.textContent.includes("Origin check question")), "converted history chat");
    [...document.querySelectorAll(".nav-chat-open")].find(button => button.textContent.includes("Origin check question")).click();
    await wait(() => [...document.querySelectorAll(".turn")].some(turn => turn.textContent.includes("Follow up in Claude")), "converted history loaded");
    const convertedTurns = [...document.querySelectorAll(".turn")];
    const original = convertedTurns.find(turn => turn.textContent.includes("Origin check question"));
    const followup = convertedTurns.find(turn => turn.textContent.includes("Follow up in Claude"));
    const userOnly = convertedTurns.find(turn => turn.textContent.includes("Earlier Codex input without reply"));
    report.convertedOrigins = original?.querySelector(".work-metadata")?.textContent.includes("Codex · original-codex") && followup?.querySelector(".work-metadata")?.textContent.includes("Claude · original-claude") && userOnly?.querySelector(".work-metadata")?.textContent.trim() === "· Codex";
    if (!(await fetch("/smoke/capture?agent=converted-origin")).ok) throw Error("converted origin screenshot failed");
    report.noBrowserErrors = errors.length === 0;
    report.complete = true;
  } catch (error) { report.error = String(error); report.body = document.body.innerText.slice(-2500); report.requests = requests; report.browserErrors = errors; }
  await fetch("/smoke/report", { method: "POST", body: JSON.stringify(report) });
})();
