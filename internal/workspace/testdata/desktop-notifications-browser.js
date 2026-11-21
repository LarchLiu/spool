async function runDesktopNotificationSmoke(owner, peer, group, chat, otherChat) {
  const wait = async (check, label) => {
    const deadline = Date.now() + 6000;
    while (Date.now() < deadline) {
      if (check()) return;
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    throw new Error("Timed out: " + label);
  };
  const selected = (id) =>
    document
      .querySelector(`.direct-message-panel [data-conversation='${id}']`)
      ?.getAttribute("aria-selected") === "true";
  await wait(() => selected(group), "cold native group click");
  const start = performance.timeOrigin;
  const click = (url, user = owner) => {
    window.spoolNotificationTarget = { url, user };
    window.dispatchEvent(new Event("spool:notification-changed"));
  };
  const notify = (kind, user = owner) => {
    window.spoolSessionNotifications = [{ chat, user, kind }];
    window.dispatchEvent(new Event("spool:session-notification-changed"));
  };
  const view = (chat) => {
    window.spoolViewedChat = chat;
    window.dispatchEvent(new Event("spool:session-view-changed"));
  };
  const rows = () => [...document.querySelectorAll(".nav-chat")].filter(
    (row) => row.querySelector(".nav-chat-title")?.textContent === "Notification target",
  );
  const markers = () => rows().map((row) => row.querySelector(".session-notification-icon"));
  const colors = {};
  document.documentElement.dataset.theme = "light";
  for (const [kind, label] of [["completed", "Completed"], ["failed", "Failed"], ["approval", "Approval required"]]) {
    notify(kind);
    await wait(() => markers().length > 0 && markers().every((marker) => marker?.dataset.status === kind), kind + " session marker");
    const marker = markers()[0], row = rows()[0];
    if (marker.getAttribute("aria-label") !== label) throw new Error("Missing accessible status: " + kind);
    const title = row.querySelector(".nav-chat-title");
    if (!title || marker.getBoundingClientRect().left < title.getBoundingClientRect().right)
      throw new Error("Status must follow the session title");
    if (row.lastElementChild !== marker) throw new Error("Status must remain at the end of the row");
    if (row.scrollWidth > row.clientWidth) throw new Error("Session marker overflowed its row");
    colors[kind] = getComputedStyle(marker).color;
    window.dispatchEvent(new Event("focus"));
    await new Promise((resolve) => requestAnimationFrame(resolve));
    if (markers().some((marker) => !marker)) throw new Error("Focus cleared unread status");
  }
  if (new Set(Object.values(colors)).size !== 3) throw new Error("Status colors must differ");
  notify("failed", "another-user-123456");
  await new Promise((resolve) => requestAnimationFrame(resolve));
  if (markers().some((marker) => marker?.dataset.status !== "approval"))
    throw new Error("A different account changed session status");
  document.documentElement.dataset.theme = "dark";
  const dark = getComputedStyle(markers()[0]).color;
  if (dark === colors.approval) throw new Error("Dark theme status color did not adapt");
  document.documentElement.dataset.theme = "light";
  rows()[0].querySelector(".nav-chat-open").click();
  await wait(() => markers().every((marker) => !marker), "viewed session clears all copies");
  notify("completed");
  await wait(() => markers().some(Boolean), "new notification after viewing");
  click(`/?chat=${chat}`);
  await wait(
    () =>
      document.querySelector(".chat-heading strong")?.textContent ===
        "Notification target" &&
      !document.querySelector(".direct-message-panel"),
    "native task click",
  );
  await wait(() => markers().every((marker) => !marker), "native click clears session marker");
  for (const kind of ["completed", "failed", "approval"]) {
    view(""); // The native window is unfocused or displaying settings.
    window.spoolSessionNotifications = [
      { chat, user: owner, kind },
      { chat: otherChat, user: owner, kind: "failed" },
    ];
    window.dispatchEvent(new Event("spool:session-notification-changed"));
    await wait(() => markers().length > 0 && markers().every(Boolean), "background current session is unread");
    window.dispatchEvent(new Event("focus"));
    await new Promise((resolve) => requestAnimationFrame(resolve));
    if (markers().some((marker) => !marker)) throw new Error("DOM focus overrode the native settings/background view");
    view(chat); // Native focus returns to the loaded Chat view.
    await wait(() => markers().every((marker) => !marker), "returning to the current session marks it read");
    const otherRows = [...document.querySelectorAll(".nav-chat")].filter(
      (row) => row.querySelector(".nav-chat-title")?.textContent === "Other chat",
    );
    if (!otherRows.length || otherRows.some((row) => row.querySelector(".session-notification-icon")?.dataset.status !== "failed"))
      throw new Error("Returning to the current session cleared another session's status");
    notify(kind);
    await new Promise((resolve) => requestAnimationFrame(resolve));
    if (markers().some(Boolean)) throw new Error("Delayed delivery marked the session being read as unread");
  }
  view("");
  click(`/?inbox=${peer}&recipient=${owner}`);
  await wait(() => selected(peer), "native private click");
  click(`/?inbox=online-room&recipient=${owner}`);
  await wait(() => selected("online-room"), "native online room click");
  click(`/?inbox=${group}&recipient=${owner}`, "another-user-123456");
  await new Promise((resolve) => setTimeout(resolve, 100));
  if (!selected("online-room"))
    throw new Error("Native click crossed account boundary");
  if (start !== performance.timeOrigin)
    throw new Error("Native click reloaded chat");
  return { ok: true, native: true, sessionStatuses: colors };
}
