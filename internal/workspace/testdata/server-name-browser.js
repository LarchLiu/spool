(async () => {
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const waitFor = async (check, label) => {
    for (let i = 0; i < 200; i++) { if (check()) return; await pause(25); }
    throw new Error("Timed out: " + label);
  };
  const browser = async commands => {
    const response = await fetch("/smoke/browser", { method: "POST", body: JSON.stringify(commands) });
    if (!response.ok) throw new Error(await response.text());
  };
  await browser([{ method: "Emulation.setDeviceMetricsOverride", params: { width: window.smokeWidth, height: 900, deviceScaleFactor: 1, mobile: false } }]);
  const brand = () => document.querySelector(".sidebar .brand-name");
  const footer = () => document.querySelector(".side-foot small");
  const named = name => document.title === "Spool · " + name && brand()?.textContent === "Spool · " + name && footer()?.textContent === name;
  await waitFor(() => named("Home"), "default Home name");
  if (innerWidth < 900) {
    document.querySelector(".panel-toggle-left").click();
    await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 0.1, "sidebar opened");
  }
  for (const [raw, expected] of [
    ["  办公室 · Mac  ", "办公室 · Mac"],
    ["<img src=x onerror=alert(1)> & Office", "<img src=x onerror=alert(1)> & Office"],
    ["这是一个很长的服务器名称 Long server name ".repeat(8), "这是一个很长的服务器名称 Long server name ".repeat(8).trim()],
    ["", "Home"],
  ]) {
    const response = await fetch("/smoke/name?name=" + encodeURIComponent(raw), { method: "POST" });
    if (!response.ok) throw new Error(await response.text());
    await waitFor(() => named(expected), "live name " + expected);
    if (brand().querySelector("img") || footer().querySelector("img")) throw new Error("Server name rendered as HTML");
    const sidebar = document.querySelector(".sidebar").getBoundingClientRect();
    const close = document.querySelector(".sidebar-collapse").getBoundingClientRect();
    if (close.right > sidebar.right || document.documentElement.scrollWidth > innerWidth) throw new Error("Server name overflowed the sidebar");
  }
  await fetch("/smoke/name?name=" + encodeURIComponent("办公室 · Mac"), { method: "POST" });
  await waitFor(() => named("办公室 · Mac"), "final name");
  await browser([{ method: "Page.captureScreenshot", params: { format: "png" } }]);
  await fetch("/smoke/report", { method: "POST", body: JSON.stringify({ ok: true }) });
})().catch(async error => {
  await fetch("/smoke/report", { method: "POST", body: JSON.stringify({ error: String(error) }) });
});
