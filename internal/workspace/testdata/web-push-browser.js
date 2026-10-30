async function runPushBrowserSmoke(otherChat, width, owner, peer, group) {
  const wait = async (check, label) => {
    const until = Date.now() + 7000;
    while (Date.now() < until) {
      if (check()) return;
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    throw new Error("Timed out: " + label);
  };
  const button = (label) =>
    [...document.querySelectorAll(".notification-actions button")].find(
      (item) => item.textContent === label,
    );
  await wait(
    () => document.querySelector(".chat-heading strong"),
    "chat loaded",
  );
  if (width > 900) {
    await wait(
      () =>
        document.querySelector(".chat-heading strong").textContent ===
        "Notification target",
      "notification deep link",
    );
    if (location.search)
      throw new Error("Consumed deep link remained in address bar");
    const start = performance.timeOrigin;
    const channel = new MessageChannel();
    const acknowledged = new Promise((resolve) => {
      channel.port1.onmessage = resolve;
    });
    navigator.serviceWorker.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "OPEN_CHAT", chatID: otherChat },
        ports: [channel.port2],
      }),
    );
    await acknowledged;
    channel.port1.close();
    channel.port2.close();
    await wait(
      () =>
        document.querySelector(".chat-heading strong").textContent ===
        "Other chat",
      "focused notification chat",
    );
    if (start !== performance.timeOrigin)
      throw new Error("Notification navigation reloaded app");
    const openInbox = async (peerID, userID = owner) => {
      const channel = new MessageChannel();
      const acknowledged = new Promise((resolve) => {
        channel.port1.onmessage = resolve;
      });
      navigator.serviceWorker.dispatchEvent(
        new MessageEvent("message", {
          data: { type: "OPEN_INBOX", peerID, userID },
          ports: [channel.port2],
        }),
      );
      await acknowledged;
      channel.port1.close();
      channel.port2.close();
    };
    for (const conversation of [peer, group, "online-room"]) {
      await openInbox(conversation);
      await wait(
        () =>
          document
            .querySelector(
              `.direct-message-panel [data-conversation='${conversation}']`,
            )
            ?.getAttribute("aria-selected") === "true",
        "notification inbox " + conversation,
      );
    }
    // An old notification for another account must not change conversations.
    await openInbox(peer, "another-user-123456");
    await new Promise((resolve) => setTimeout(resolve, 100));
    if (
      document
        .querySelector(".direct-message-panel [aria-selected='true']")
        ?.getAttribute("data-conversation") !== "online-room"
    )
      throw new Error("Notification crossed account boundary");
    if (start !== performance.timeOrigin)
      throw new Error("Inbox notification reloaded app");
  } else {
    await wait(
      () =>
        document
          .querySelector(`.direct-message-panel [data-conversation='${group}']`)
          ?.getAttribute("aria-selected") === "true",
      "fresh-page group notification",
    );
    if (location.search)
      throw new Error("Inbox notification URL was not consumed");
  }
  document
    .querySelector(".direct-message-panel [aria-label='Close group chat']")
    ?.click();
  document
    .querySelector(".appearance-dialog [aria-label='Close settings']")
    ?.click();
  document.querySelector("[aria-label='Appearance and language']").click();
  await wait(
    () =>
      document
        .querySelector(".notification-settings")
        ?.getAttribute("aria-busy") === "false",
    "notification settings",
  );
  if (Notification.permission !== "granted")
    throw new Error("Notification permission was not configured");
  if (button("Disable notifications")) {
    button("Disable notifications").click();
    await wait(
      () =>
        button("Enable notifications") &&
        !button("Enable notifications").disabled,
      "disable notifications",
    );
  }
  button("Enable notifications").click();
  await wait(
    () =>
      button("Disable notifications") &&
      !button("Disable notifications").disabled,
    "enable notifications",
  );
  if (width > 900) {
    button("Send test notification").click();
    await wait(
      () => document.querySelector(".notification-settings [role='status']"),
      "test push accepted",
    );
  }
  const dialog = document.querySelector(".appearance-dialog");
  const bounds = dialog.getBoundingClientRect();
  if (
    bounds.left < 0 ||
    bounds.right > innerWidth ||
    bounds.top < 0 ||
    bounds.bottom > innerHeight
  )
    throw new Error("Settings overflow viewport");
  if (document.querySelector(".notification-settings [role='alert']"))
    throw new Error("Notification settings show an error");
  return {
    ok: true,
    width,
    secure: isSecureContext,
    serviceWorker: !!(await navigator.serviceWorker.getRegistration("/"))
      ?.active,
    enabled:
      button("Disable notifications").getAttribute("aria-pressed") === "true",
  };
}
