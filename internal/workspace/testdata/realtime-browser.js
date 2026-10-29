const NativeWebSocket = window.WebSocket;
window.WebSocket = class extends NativeWebSocket {
  constructor(...args) {
    super(...args);
    if (new URL(args[0], location.href).pathname === "/api/events") {
      window.workspaceSocket = this;
      this.addEventListener("message", (event) => {
        const message = JSON.parse(event.data);
        if (message.type === "profiles") this.profiles = message.data;
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
