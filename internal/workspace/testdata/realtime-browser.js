const NativeWebSocket = window.WebSocket;
window.WebSocket = class extends NativeWebSocket {
  constructor(...args) {
    super(...args);
    if (new URL(args[0], location.href).pathname === "/api/events") {
      window.workspaceSocket = this;
      this.addEventListener("message", (event) => {
        const message = JSON.parse(event.data);
        if (message.type === "profiles") this.profiles = message.data;
        if (message.type === "chat_groups") this.groups = message.data;
      });
    }
  }
};
const NativeEventSource = window.EventSource;
window.EventSource = class extends NativeEventSource {
  constructor(...args) {
    super(...args);
    window.chatSource = this;
    this.addEventListener("snapshot", (event) => { this.snapshot = JSON.parse(event.data); });
  }
};

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, label) {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    if (check()) return;
    await pause(25);
  }
  throw new Error("Timed out: " + label);
}
const action = (step) => fetch("/smoke/action?step=" + step).then(async (response) => {
  if (!response.ok) throw new Error(await response.text());
});
const row = (title) => [...document.querySelectorAll(".nav-chat")].find(
  (el) => el.querySelector(".nav-chat-title")?.textContent === title,
);
const operators = (title) => row(title)?.querySelector(".chat-operators");
const isBobOnline = () => [...document.querySelectorAll(".online-list > div")].some(
  (el) => el.textContent.includes("bob"),
);

async function browser(commands) {
  const response = await fetch("/smoke/browser", { method: "POST", body: JSON.stringify(commands) });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

function setComposer(input, text) {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, text);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

async function selectConversation(id) {
  document.querySelector(".direct-message-conversation").click();
  await waitFor(() => document.querySelector(`.direct-message-conversations [data-conversation="${id}"]`), "retained conversation " + id);
  document.querySelector(`.direct-message-conversations [data-conversation="${id}"]`).click();
  await waitFor(() => !document.querySelector(".direct-message-conversations"), "conversation selected");
}

async function checkUnicodeComposer(panel, report) {
  const input = panel.querySelector("textarea");
  const submit = panel.querySelector('button[type="submit"]');
  input.focus();
  await browser([{ method: "Input.insertText", params: { text: "🙂".repeat(2000) } }]);
  await waitFor(() => [...input.value].length === 2000 && !submit.disabled, "2000 emoji accepted");
  await browser([{ method: "Input.insertText", params: { text: "你" } }]);
  await waitFor(() => [...input.value].length === 2001 && submit.disabled && input.getAttribute("aria-invalid") === "true", "over-limit Unicode validation");
  report.unicodeComposer = input.value === "🙂".repeat(2000) + "你";

  setComposer(input, "a".repeat(1999));
  await waitFor(() => input.value.length === 1999 && !submit.disabled, "IME boundary setup");
  input.setSelectionRange(input.value.length, input.value.length);
  await browser([{ method: "Input.imeSetComposition", params: { text: "你🙂", selectionStart: 3, selectionEnd: 3 } }]);
  await waitFor(() => input.value === "a".repeat(1999) + "你🙂", "IME provisional text remains intact");
  const count = panel.querySelectorAll(".direct-message").length;
  input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  report.imeComposer = submit.disabled && !input.hasAttribute("aria-invalid") && !panel.querySelector('[role="alert"]');
  await browser([{ method: "Input.insertText", params: { text: "你🙂" } }]);
  await waitFor(() => input.getAttribute("aria-invalid") === "true" && submit.disabled, "IME committed over-limit validation");
  report.imeComposer &&= panel.querySelectorAll(".direct-message").length === count && input.value === "a".repeat(1999) + "你🙂";
  setComposer(input, "a".repeat(1998));
  input.setSelectionRange(input.value.length, input.value.length);
  await browser([{ method: "Input.imeSetComposition", params: { text: "你🙂", selectionStart: 3, selectionEnd: 3 } }]);
  await waitFor(() => input.value === "a".repeat(1998) + "你🙂" && submit.disabled, "valid-length IME composition");
  const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
  input.dispatchEvent(enter);
  panel.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  report.imeComposer &&= !enter.defaultPrevented && panel.querySelectorAll(".direct-message").length === count;
  await browser([{ method: "Input.insertText", params: { text: "你🙂" } }]);
  await waitFor(() => !submit.disabled && !input.hasAttribute("aria-invalid"), "valid-length IME commit");
  report.imeComposer &&= input.value === "a".repeat(1998) + "你🙂";
  const webkitEnter = new KeyboardEvent("keydown", { key: "Enter", keyCode: 229, bubbles: true, cancelable: true });
  input.dispatchEvent(webkitEnter);
  await pause(25);
  report.imeComposer &&= !webkitEnter.defaultPrevented && panel.querySelectorAll(".direct-message").length === count && input.value === "a".repeat(1998) + "你🙂";
  setComposer(input, "你🙂".repeat(1000));
  await waitFor(() => !submit.disabled && !input.hasAttribute("aria-invalid"), "mixed Unicode boundary accepted");
  report.unicodeComposer &&= input.value === "你🙂".repeat(1000);
  setComposer(input, "");
}

async function pointerDrag(element, dx, dy) {
  const bounds = element.getBoundingClientRect();
  const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height / 2;
  await browser([
    { method: "Input.dispatchMouseEvent", params: { type: "mouseMoved", x, y } },
    { method: "Input.dispatchMouseEvent", params: { type: "mousePressed", x, y, button: "left", buttons: 1, clickCount: 1 } },
    { method: "Input.dispatchMouseEvent", params: { type: "mouseMoved", x: x + dx, y: y + dy, button: "left", buttons: 1 } },
    { method: "Input.dispatchMouseEvent", params: { type: "mouseReleased", x: x + dx, y: y + dy, button: "left", buttons: 0, clickCount: 1 } },
  ]);
  await pause(60);
}

const windowBounds = (panel) => {
  const { x, y, width, height } = panel.getBoundingClientRect();
  return { x, y, width, height };
};
const sameBounds = (a, b) => Object.keys(a).every((key) => Math.abs(a[key] - b[key]) < 1);
const fitsViewport = (panel) => {
  const { x, y, width, height } = windowBounds(panel);
  return x >= 0 && y >= 0 && x + width <= innerWidth && y + height <= innerHeight;
};

async function checkFloatingWindow(panel, report) {
  report.chatInitialFocus = document.activeElement === panel.querySelector("textarea") && !panel.querySelector(".direct-message-drag").matches(":focus-visible");
  const mainInput = document.querySelector("#chat-message");
  mainInput.focus();
  report.chatNonmodal = !panel.matches(":modal") && document.activeElement === mainInput && !panel.contains(document.elementFromPoint(16, 16));

  const original = windowBounds(panel);
  await pointerDrag(panel.querySelector(".direct-message-drag"), -60, -80);
  const moved = windowBounds(panel);
  report.chatDrag = moved.y < original.y && moved.width === original.width && moved.height === original.height;

  await pointerDrag(panel.querySelector('.direct-message-edge[data-edge="se"]'), -80, -90);
  const smaller = windowBounds(panel);
  await pointerDrag(panel.querySelector('.direct-message-edge[data-edge="se"]'), 24, 30);
  const larger = windowBounds(panel);
  report.chatResize = smaller.width < original.width && smaller.height < original.height && larger.width > smaller.width && larger.height > smaller.height && larger.x === smaller.x && larger.y === smaller.y;

  let last = larger;
  const edgeMoves = { n: [0, 10], e: [-10, 0], s: [0, -10], w: [10, 0], ne: [-10, 10], sw: [10, -10], nw: [10, 10] };
  report.chatEdges = true;
  for (const [edge, [dx, dy]] of Object.entries(edgeMoves)) {
    await pointerDrag(panel.querySelector(`.direct-message-edge[data-edge="${edge}"]`), dx, dy);
    const current = windowBounds(panel);
    report.chatEdges &&= !sameBounds(last, current) && fitsViewport(panel);
    last = current;
  }

  await pointerDrag(panel.querySelector(".direct-message-drag"), -3000, -3000);
  const atTop = windowBounds(panel);
  await pointerDrag(panel.querySelector('.direct-message-edge[data-edge="se"]'), 3000, 3000);
  const atMaximum = windowBounds(panel);
  await pointerDrag(panel.querySelector('.direct-message-edge[data-edge="se"]'), -3000, -3000);
  const atMinimum = windowBounds(panel);
  report.chatBounds = atTop.x === 12 && atTop.y === 12 && atMaximum.width === innerWidth - 24 && atMaximum.height === innerHeight - 24 && atMinimum.width === 300 && atMinimum.height === 360 && fitsViewport(panel) && !panel.dataset.operation;

  const title = panel.querySelector(".direct-message-drag");
  title.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
  title.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", shiftKey: true, bubbles: true }));
  panel.querySelector('.direct-message-edge[data-edge="e"]').dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
  panel.querySelector('.direct-message-edge[data-edge="s"]').dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
  await pause(50);
  const keyboard = windowBounds(panel);
  report.chatKeyboard = keyboard.x === atMinimum.x + 10 && keyboard.y === atMinimum.y + 40 && keyboard.width === atMinimum.width + 10 && keyboard.height === atMinimum.height + 10;

  await browser([{ method: "Emulation.setDeviceMetricsOverride", params: { width: 280, height: 320, deviceScaleFactor: 1, mobile: false } }]);
  await waitFor(() => innerWidth === 280 && fitsViewport(panel), "window clamped after viewport shrink");
  report.chatViewport = panel.getBoundingClientRect().width === 256 && panel.getBoundingClientRect().height === 296;
  await browser([{ method: "Emulation.setDeviceMetricsOverride", params: { width: window.smokeWidth, height: 900, deviceScaleFactor: 1, mobile: false } }]);
  await waitFor(() => innerWidth > 280 && fitsViewport(panel) && panel.getBoundingClientRect().width >= 300 && panel.getBoundingClientRect().height >= 360, "restored viewport");
  return windowBounds(panel);
}

function sendLayout(panel) {
  const send = panel.querySelector('button[type="submit"]');
  const button = send.getBoundingClientRect();
  const arrow = send.querySelector("svg").getBoundingClientRect();
  const input = panel.querySelector("textarea").getBoundingClientRect();
  return Math.abs(button.x + button.width / 2 - arrow.x - arrow.width / 2) < 1 && Math.abs(button.y + button.height / 2 - arrow.y - arrow.height / 2) < 1 && Math.abs(input.bottom - button.bottom) < 1 && getComputedStyle(send).margin === "0px";
}

async function checkSettingsChoiceStyles() {
  const html = await fetch("/desktop.html").then((response) => response.text());
  const settings = new DOMParser().parseFromString(html, "text/html");
  const links = [...settings.querySelectorAll('link[rel="stylesheet"]')].map((link) => {
    const node = document.createElement("link");
    node.rel = "stylesheet";
    node.href = link.getAttribute("href");
    return node;
  });
  await Promise.all(links.map((link) => new Promise((resolve, reject) => {
    link.onload = resolve;
    link.onerror = reject;
    document.head.append(link);
  })));
  const fixture = document.createElement("div");
  fixture.className = "desktop-settings-shell";
  fixture.style.cssText = "position:fixed;visibility:hidden;width:350px;height:auto;left:0;top:0";
  fixture.innerHTML = `<div class="settings-member-access"><button aria-pressed="true">Read-only</button><button aria-pressed="false">Can create</button></div><div class="settings-check-interval"><button aria-pressed="false">30 minutes</button><button aria-pressed="true">12 hours</button></div><span style="color:var(--color-primary)"></span>`;
  document.body.append(fixture);
  const originalTheme = document.documentElement.dataset.theme;
  try {
    for (const theme of ["light", "dark"]) {
      document.documentElement.dataset.theme = theme;
      const selected = [...fixture.querySelectorAll('[aria-pressed="true"]')].map((node) => getComputedStyle(node));
      const primary = getComputedStyle(fixture.querySelector("span")).color;
      if (selected.some((style) => style.color !== primary) || selected[0].backgroundColor !== selected[1].backgroundColor || selected[0].borderRadius !== selected[1].borderRadius)
        throw new Error("Permission/interval selection style mismatch in " + theme);
    }
  } finally {
    document.documentElement.dataset.theme = originalTheme;
    fixture.remove();
    links.forEach((link) => link.remove());
  }
}

async function run() {
  const report = {};
  try {
    // Headless Chrome can impose a 500px minimum native window width. Override
    // the page viewport so the 390px case genuinely tests phone layouts.
    await browser([{ method: "Emulation.setDeviceMetricsOverride", params: { width: window.smokeWidth, height: 900, deviceScaleFactor: 1, mobile: false } }]);
    await waitFor(() => innerWidth === window.smokeWidth, "requested browser viewport");
    report.viewportWidth = innerWidth;
    await waitFor(() => row("Watching chat") && window.workspaceSocket?.readyState === 1, "initial navigation/socket");
    await waitFor(() => row("Local browser-local-session")?.querySelector(".nav-chat-local svg"), "local session source icon");
    report.localIcon = row("Local browser-local-session").querySelector(".nav-chat-local").getAttribute("aria-label") === "Local session";
    if (innerWidth < 900) document.querySelector(".panel-toggle-left")?.click();
    row("Watching chat").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector(".chat-heading strong")?.textContent === "Watching chat", "select watching chat");
    await action("actor");
    await waitFor(() => operators("Background chat")?.getAttribute("aria-label").includes("bob"), "background actor push");
    report.actorStack = operators("Background chat").querySelectorAll("img").length === 2;
    report.activeUnchanged = document.querySelector(".chat-heading strong").textContent === "Watching chat";
    const avatars = () => [...operators("Background chat").querySelectorAll("img")].map((img) => img.src).join("|");
    const avatar = avatars();
    row("Background chat").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector('.turn-operator-avatar[aria-label="bob"]') && window.chatSource?.snapshot, "active chat actor");
    const oldChat = structuredClone(window.chatSource.snapshot);
    const oldProfiles = structuredClone(window.workspaceSocket.profiles);
    await action("join");
    await waitFor(isBobOnline, "online member push");
    report.presenceJoined = true;
    await action("group-send");
    await waitFor(() => document.querySelector(".direct-inbox-button .direct-unread")?.textContent === "1", "group-message unread badge");
    document.querySelector(".online summary").click();
    document.querySelector(".online-group-row button").click();
    await waitFor(() => document.querySelector(".direct-message-panel[open] .group-message-author")?.textContent === "bob", "group sender identity");
    const groupPanel = document.querySelector(".direct-message-panel");
    const groupBounds = groupPanel.getBoundingClientRect();
    report.groupLayout = groupBounds.left >= 0 && groupBounds.right <= innerWidth && groupBounds.top >= 0 && groupBounds.bottom <= innerHeight && groupPanel.scrollWidth <= groupPanel.clientWidth;
    const retainedBounds = await checkFloatingWindow(groupPanel, report);
    const groupInput = groupPanel.querySelector("textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(groupInput, "A line of draft text\n".repeat(8));
    groupInput.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => groupInput.getBoundingClientRect().height > 60, "multiline draft");
    const draftSend = groupPanel.querySelector('button[type="submit"]').getBoundingClientRect();
    report.chatLongDraft = draftSend.bottom <= groupPanel.getBoundingClientRect().bottom && groupPanel.querySelector(".direct-message-log").clientHeight > 0;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(groupInput, "Group browser reply");
    groupInput.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => !groupPanel.querySelector('button[type="submit"]').disabled, "group-message send enabled");
    groupPanel.querySelector('button[type="submit"]').click();
    document.querySelector("#chat-message").focus();
    await waitFor(() => groupPanel.querySelector(".direct-message.own") && groupInput.value === "", "group-message acknowledgement");
    report.chatDoesNotStealFocus = document.activeElement === document.querySelector("#chat-message");
    await action("group-received");
    report.groupSent = groupPanel.querySelector("h2")?.textContent === "Online group chat" && !document.querySelector(".direct-inbox-button .direct-unread");
    groupPanel.querySelector('.direct-message-heading button').click();
    document.querySelector(".online summary").click();
    document.querySelector(".online-new-group").click();
    await waitFor(() => document.querySelector(".people-group-picker[open]"), "selected-member picker");
    const picker = document.querySelector(".people-group-picker");
    const pickerBounds = picker.getBoundingClientRect();
    report.selectedLayout = pickerBounds.left >= 0 && pickerBounds.right <= innerWidth && pickerBounds.top >= 0 && pickerBounds.bottom <= innerHeight && picker.scrollWidth <= picker.clientWidth;
    const nameInput = picker.querySelector('input:not([type="checkbox"])');
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(nameInput, "Pair group");
    nameInput.dispatchEvent(new Event("input", { bubbles: true }));
    picker.querySelector('input[type="checkbox"]').click();
    await waitFor(() => !picker.querySelector('button[type="submit"]').disabled, "selected-group create enabled");
    picker.querySelector('button[type="submit"]').click();
    await waitFor(() => document.querySelector(".direct-message-panel[open] h2")?.textContent === "Pair group", "selected-group created");
    const selectedPanel = document.querySelector(".direct-message-panel");
    report.chatRetained = sameBounds(retainedBounds, windowBounds(selectedPanel));
    const selectedInput = selectedPanel.querySelector("textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(selectedInput, "Selected browser secret");
    selectedInput.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => !selectedPanel.querySelector('button[type="submit"]').disabled, "selected-group send enabled");
    selectedPanel.querySelector('button[type="submit"]').click();
    await waitFor(() => selectedPanel.querySelector(".direct-message.own") && selectedInput.value === "", "selected-group acknowledgement");
    await action("selected-received");
    report.selectedCreated = window.workspaceSocket.groups?.some((group) => group.name === "Pair group" && group.members.length === 2);
    await action("third-join");
    await waitFor(() => [...document.querySelectorAll(".online-list > div")].some((el) => el.textContent.includes("charlie")), "third user connected");
    selectedPanel.querySelector('[aria-label="Invite people"]').click();
    await waitFor(() => document.querySelector(".people-group-picker[open] input[type=checkbox]"), "invite picker");
    const invitePicker = document.querySelector(".people-group-picker");
    invitePicker.querySelector('input[type="checkbox"]').click();
    await waitFor(() => !invitePicker.querySelector('button[type="submit"]').disabled, "invite enabled");
    invitePicker.querySelector('button[type="submit"]').click();
    await waitFor(() => !document.querySelector(".people-group-picker") && window.workspaceSocket.groups?.some((group) => group.name === "Pair group" && group.members.length === 3), "selected-group invitation accepted");
    await action("selected-invited");
    report.selectedInvited = document.querySelectorAll("dialog").length === 1;
    const selectedID = window.workspaceSocket.groups.find((group) => group.name === "Pair group").id;
    setComposer(selectedInput, "Unsent selected-group draft");
    await pause(25);
    selectedPanel.querySelector('[aria-label="Close group chat"]').click();
    await action("direct-send");
    await waitFor(() => document.querySelector(".direct-inbox-button .direct-unread")?.textContent === "1", "private-message unread badge");
    report.directUnread = true;
    document.querySelector(".online summary").click();
    document.querySelector('.online-chat-person[title="Chat with bob"]').click();
    await waitFor(() => document.querySelector(".direct-message-panel[open] h2")?.textContent === "Chat with bob" && document.querySelector(".direct-message-panel .direct-message p")?.textContent === "Hello from Bob <script>not executable</script>", "open Bob's private message");
    let panel = document.querySelector(".direct-message-panel");
    const bobID = window.workspaceSocket.profiles.people.find((person) => person.nickname === "bob").id;
    setComposer(panel.querySelector("textarea"), "Unsent private draft");
    await pause(25);
    await selectConversation(selectedID);
    await waitFor(() => document.querySelector(".direct-message-panel textarea")?.value === "Unsent selected-group draft", "closed/reopened group draft");
    await selectConversation(bobID);
    await waitFor(() => document.querySelector(".direct-message-panel textarea")?.value === "Unsent private draft", "private draft restored after switch");
    document.querySelector('[aria-label="Close private chat"]').click();
    document.querySelector(".direct-inbox-button").click();
    await waitFor(() => document.querySelector(".direct-message-panel textarea")?.value === "Unsent private draft", "private draft restored after close");
    report.draftRetained = true;
    panel = document.querySelector(".direct-message-panel");
    if (panel.querySelector("script") || panel.querySelector(".direct-message p")?.textContent !== "Hello from Bob <script>not executable</script>")
      throw new Error("Private message was not rendered as plain text");
    const bounds = panel.getBoundingClientRect();
    report.directLayout = bounds.left >= 0 && bounds.right <= innerWidth && bounds.top >= 0 && bounds.bottom <= innerHeight && panel.scrollWidth <= panel.clientWidth;
    report.chatRetained &&= sameBounds(retainedBounds, windowBounds(panel));
    report.chatSendLayout = sendLayout(panel);
    setComposer(panel.querySelector("textarea"), "");
    await pause(25);
    await checkUnicodeComposer(panel, report);
    const textarea = panel.querySelector("textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Browser reply");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => !panel.querySelector('button[type="submit"]').disabled, "private-message send enabled");
    report.chatSendLayout &&= sendLayout(panel);
    textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true }));
    await pause(50);
    if (panel.querySelectorAll(".direct-message").length !== 1) throw new Error("IME composition submitted a message");
    panel.querySelector('button[type="submit"]').click();
    await waitFor(() => panel.querySelector(".direct-message.own") && textarea.value === "", "private-message server acknowledgement");
    await action("direct-received");
    report.directSent = !document.querySelector(".direct-inbox-button .direct-unread");
    panel.querySelector('.direct-message-heading button').click();
    if (innerWidth < 900) document.querySelector(".panel-toggle-left")?.click();
    operators("Background chat").click();
    await waitFor(() => document.querySelector(".chat-operator-popover"), "open operators");
    await action("avatar");
    await waitFor(() => avatars() !== avatar, "avatar push");
    report.avatarUpdated = true;
    const messageAvatar = () => document.querySelector('.turn-operator-avatar[aria-label="bob"] img')?.src;
    const onlineBob = () => [...document.querySelectorAll(".online-list > div")].find((el) => el.textContent.includes("bob"));
    const popupBob = () => [...document.querySelectorAll(".chat-operator-list li")].find((el) => el.textContent.includes("bob"));
    await waitFor(() => messageAvatar() && onlineBob()?.querySelector("img")?.src === messageAvatar() && popupBob()?.querySelector("img")?.src === messageAvatar(), "shared avatar in message/presence/popover");
    const latestAvatar = messageAvatar();
    window.workspaceSocket.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ type: "profiles", data: oldProfiles }) }));
    // Also tolerate a buffered pre-upgrade SSE frame containing old profiles.
    oldChat.participants = oldChat.participants.map((person) => ({ ...person, nickname: "old-name", avatar: { style: "clay", seed: "old-seed" } }));
    for (const type of ["snapshot", "metadata"])
      window.chatSource.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(oldChat) }));
    await pause(150);
    if (messageAvatar() !== latestAvatar || avatars() === avatar || !operators("Background chat").getAttribute("aria-label").includes("bob"))
      throw new Error("Delayed SSE/profile frame reverted current identity");
    report.staleProfilesIgnored = true;
    report.sharedAvatars = true;
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await action("leave");
    await waitFor(() => !isBobOnline(), "offline member push");
    report.presenceLeft = true;
    document.querySelector(".direct-inbox-button").click();
    await waitFor(() => document.querySelector(".direct-message-panel[open]"), "reopen offline private chat");
    report.directOffline = document.querySelector('.direct-message-panel button[type="submit"]').disabled && document.querySelector(".direct-message-status")?.textContent.includes("offline");
    document.querySelector('.direct-message-heading button').click();
    await action("profile-name");
    await waitFor(() => operators("Background chat").getAttribute("aria-label").includes("Robert") && document.querySelector('.turn-operator-avatar[aria-label="Robert"]'), "shared profile name update");
    report.nameUpdated = true;
    row("Watching chat").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector(".chat-heading strong")?.textContent === "Watching chat", "restore watching chat");
    const oldSocket = window.workspaceSocket;
    oldSocket.close();
    await action("rename");
    await waitFor(() => window.workspaceSocket !== oldSocket && row("Changed while disconnected"), "reconnect snapshot");
    report.reconnected = true;
    document.querySelector(".direct-inbox-button").click();
    await waitFor(() => document.querySelector(".direct-message-panel[open]"), "retained in-page conversation");
    report.directNoReplay = document.querySelectorAll(".direct-message-panel .direct-message").length === 2;
    document.querySelector(".direct-message-conversation").click();
    await waitFor(() => document.querySelector('.direct-message-conversations [data-conversation="online-room"]'), "conversation popup");
    const menu = document.querySelector(".direct-message-conversations");
    const menuBounds = menu.getBoundingClientRect();
    const conversationBounds = document.querySelector(".direct-message-conversation").getBoundingClientRect();
    const popupPanelBounds = document.querySelector(".direct-message-panel").getBoundingClientRect();
    report.chatConversationMenu = Math.abs(menuBounds.left - conversationBounds.left) < 1 && Math.abs(menuBounds.top - conversationBounds.bottom - 4) < 1 && menuBounds.right <= popupPanelBounds.right && menuBounds.bottom <= popupPanelBounds.bottom && menu.scrollWidth <= menu.clientWidth;
    await browser([{ method: "Page.captureScreenshot", params: { format: "png" } }]);
    menu.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await waitFor(() => !document.querySelector(".direct-message-conversations"), "escape dismisses conversation menu");
    report.chatConversationMenu &&= !!document.querySelector(".direct-message-panel[open]") && document.activeElement === document.querySelector(".direct-message-conversation");
    document.querySelector(".direct-message-conversation").dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    await waitFor(() => document.querySelector('.direct-message-conversations [aria-selected="true"]') === document.activeElement, "keyboard opens conversation menu");
    document.querySelector('.direct-message-conversations [data-conversation="online-room"]').click();
    await waitFor(() => document.querySelector(".direct-message-panel h2")?.textContent === "Online group chat", "switch private chat to group chat");
    report.chatRetained &&= sameBounds(retainedBounds, windowBounds(document.querySelector(".direct-message-panel")));
    report.groupNoReplay = document.querySelectorAll(".direct-message-panel .direct-message").length === 2 && !document.querySelector(".direct-message-status");
    report.selectedClosed = !window.workspaceSocket.groups.some((group) => group.name === "Pair group");
    await selectConversation(selectedID);
    await waitFor(() => document.querySelector(".direct-message-panel h2")?.textContent === "Pair group", "closed group retains its name");
    const closedPanel = document.querySelector(".direct-message-panel");
    report.closedGroupAccessible = closedPanel.querySelectorAll(".direct-message").length === 1 && closedPanel.querySelector("textarea").value === "Unsent selected-group draft" && closedPanel.querySelector('button[type="submit"]').disabled && !closedPanel.querySelector('[aria-label="Invite people"]') && closedPanel.textContent.includes("This group is no longer available to you.");
    document.querySelector('.direct-message-panel textarea').dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await waitFor(() => !document.querySelector(".direct-message-panel"), "escape closes only focused chat");
    report.chatEscape = true;
    await action("readonly");
    await waitFor(() => document.querySelector("#chat-message")?.disabled, "read-only controls");
    await action("control");
    await waitFor(() => document.querySelector("#chat-message")?.disabled === false, "restored session controls");
    report.controlsUpdated = true;
    row("Changed while disconnected").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector(".chat-heading strong")?.textContent === "Changed while disconnected", "select personal chat");
    await action("ownreadonly");
    await waitFor(() => document.querySelector("#chat-message")?.disabled && document.querySelector(".project-new")?.disabled, "personal read-only controls");
    row("Watching chat").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector(".chat-heading strong")?.textContent === "Watching chat" && document.querySelector("#chat-message")?.disabled === false, "independent shared-chat controls");
    await action("owncreate");
    await waitFor(() => document.querySelector(".project-new")?.disabled === false, "restored personal creation");
    report.ownControlsUpdated = true;
    await checkSettingsChoiceStyles();
    report.segmentedStyles = true;
    await checkCustomSelects(report);
    const state = await fetch("/smoke/state").then((response) => response.json());
    report.events = state.events;
    report.navigationGets = state.navigationGets;
    report.presencePosts = state.presencePosts;
    report.profileGets = state.profileGets;
    report.overflow = document.documentElement.scrollWidth > innerWidth;
    await action("revoke");
    await waitFor(() => !row("Watching chat") && !row("Changed while disconnected"), "permission revocation");
    report.revoked = true;
  } catch (error) {
    report.error = String(error);
  }
  await fetch("/smoke/report", { method: "POST", body: JSON.stringify(report) });
}
void run();
