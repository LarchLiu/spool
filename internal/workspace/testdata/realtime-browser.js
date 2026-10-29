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
    const groupInput = groupPanel.querySelector("textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(groupInput, "Group browser reply");
    groupInput.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => !groupPanel.querySelector('button[type="submit"]').disabled, "group-message send enabled");
    groupPanel.querySelector('button[type="submit"]').click();
    await waitFor(() => groupPanel.querySelector(".direct-message.own") && groupInput.value === "", "group-message acknowledgement");
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
    selectedPanel.querySelector('[aria-label="Close group chat"]').click();
    await action("direct-send");
    await waitFor(() => document.querySelector(".direct-inbox-button .direct-unread")?.textContent === "1", "private-message unread badge");
    report.directUnread = true;
    document.querySelector(".online summary").click();
    document.querySelector('.online-chat-person[title="Chat with bob"]').click();
    await waitFor(() => document.querySelector(".direct-message-panel[open] h2")?.textContent === "Chat with bob" && document.querySelector(".direct-message-panel .direct-message p")?.textContent === "Hello from Bob <script>not executable</script>", "open Bob's private message");
    const panel = document.querySelector(".direct-message-panel");
    if (panel.querySelector("script") || panel.querySelector(".direct-message p")?.textContent !== "Hello from Bob <script>not executable</script>")
      throw new Error("Private message was not rendered as plain text");
    const bounds = panel.getBoundingClientRect();
    report.directLayout = bounds.left >= 0 && bounds.right <= innerWidth && bounds.top >= 0 && bounds.bottom <= innerHeight && panel.scrollWidth <= panel.clientWidth;
    const textarea = panel.querySelector("textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "Browser reply");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    await waitFor(() => !panel.querySelector('button[type="submit"]').disabled, "private-message send enabled");
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
    const conversations = document.querySelector(".direct-message-people");
    conversations.value = "online-room";
    conversations.dispatchEvent(new Event("change", { bubbles: true }));
    await waitFor(() => document.querySelector(".direct-message-panel h2")?.textContent === "Online group chat", "switch private chat to group chat");
    report.groupNoReplay = document.querySelectorAll(".direct-message-panel .direct-message").length === 2 && !document.querySelector(".direct-message-status");
    report.selectedClosed = !window.workspaceSocket.groups.some((group) => group.name === "Pair group");
    document.querySelector('.direct-message-heading [aria-label="Close group chat"]').click();
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
