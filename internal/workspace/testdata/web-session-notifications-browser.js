async function checkWebSessionNotifications(current, other, kind) {
  const rows = (title) =>
    [...document.querySelectorAll(".nav-chat")].filter(
      (row) => row.querySelector(".nav-chat-title")?.textContent === title,
    );
  const deadline = Date.now() + 6000;
  while (Date.now() < deadline) {
    const updated = rows(other);
    if (
      document.querySelector(".chat-heading strong")?.textContent === current &&
      updated.length &&
      updated.every(
        (row) =>
          row.querySelector(".session-notification-icon")?.dataset.status ===
          kind,
      )
    ) {
      if (
        rows(current).some((row) =>
          row.querySelector(".session-notification-icon"),
        )
      )
        throw new Error(
          "Foreground current session received an unread indicator",
        );
      for (const row of updated) {
        const marker = row.querySelector(".session-notification-icon");
        if (
          row.lastElementChild !== marker ||
          row.scrollWidth > row.clientWidth
        )
          throw new Error(
            "Web session indicator overflowed or moved before hover controls",
          );
      }
      return { ok: true, kind, desktop: window.spoolDesktop === true };
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("Web session indicator did not update: " + kind);
}
