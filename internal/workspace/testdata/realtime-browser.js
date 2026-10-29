const NativeWebSocket = window.WebSocket;
window.WebSocket = class extends NativeWebSocket {
  constructor(...args) {
    super(...args);
    if (new URL(args[0], location.href).pathname === "/api/events")
      window.workspaceSocket = this;
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

async function run() {
  const report = {};
  try {
    await waitFor(() => row("Watching chat") && window.workspaceSocket?.readyState === 1, "initial navigation/socket");
    if (innerWidth < 900) document.querySelector(".panel-toggle-left")?.click();
    row("Watching chat").querySelector(".nav-chat-open").click();
    await waitFor(() => document.querySelector(".chat-heading strong")?.textContent === "Watching chat", "select watching chat");
    await action("actor");
    await waitFor(() => operators("Background chat")?.getAttribute("aria-label").includes("bob"), "background actor push");
    report.actorStack = operators("Background chat").querySelectorAll("img").length === 2;
    report.activeUnchanged = document.querySelector(".chat-heading strong").textContent === "Watching chat";
    const avatars = () => [...operators("Background chat").querySelectorAll("img")].map((img) => img.src).join("|");
    const avatar = avatars();
    await action("avatar");
    await waitFor(() => avatars() !== avatar, "avatar push");
    report.avatarUpdated = true;
    await action("join");
    await waitFor(isBobOnline, "online member push");
    report.presenceJoined = true;
    await action("leave");
    await waitFor(() => !isBobOnline(), "offline member push");
    report.presenceLeft = true;
    const oldSocket = window.workspaceSocket;
    oldSocket.close();
    await action("rename");
    await waitFor(() => window.workspaceSocket !== oldSocket && row("Changed while disconnected"), "reconnect snapshot");
    report.reconnected = true;
    const state = await fetch("/smoke/state").then((response) => response.json());
    report.events = state.events;
    report.navigationGets = state.navigationGets;
    report.presencePosts = state.presencePosts;
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
