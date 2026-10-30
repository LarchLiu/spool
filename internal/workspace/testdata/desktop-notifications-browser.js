async function runDesktopNotificationSmoke(owner, peer, group, chat) {
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
  click(`/?chat=${chat}`);
  await wait(
    () =>
      document.querySelector(".chat-heading strong")?.textContent ===
        "Notification target" &&
      !document.querySelector(".direct-message-panel"),
    "native task click",
  );
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
  return { ok: true, native: true };
}
