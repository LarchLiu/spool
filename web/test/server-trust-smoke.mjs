import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { createServer } from "vite";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const { chromium } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "node_modules"),
    "__server_trust__.cjs",
  ),
)("playwright");
const route = "/__server-trust";
const entry = route + ".tsx";
const plugin = {
  name: "server-trust-smoke",
  resolveId: (id) => (id === entry ? id : undefined),
  load: (id) =>
    id === entry
      ? `
    import { useEffect, useState } from "react";
    import { createRoot } from "react-dom/client";
    import { translator } from "/src/i18n.ts";
    import { ServerTrustPage } from "/src/settings-federation.tsx";
    import "/src/style.css";
    import "/src/settings.css";
    function Fixture() {
      const [TSNet, setTSNet] = useState({ State: 'Disabled' });
      useEffect(() => {
        const listener = (event) => setTSNet(event.detail);
        window.addEventListener('test:tailnet', listener);
        return () => window.removeEventListener('test:tailnet', listener);
      }, []);
      const data = {
        status: { TSNet, TSNetOutboundReady: TSNet.State === 'Registered' },
        projects: [{id: 'alpha', name: 'Alpha'}, {id: 'beta', name: 'Beta'}, {id: 'archive', name: 'Archived', archived: true}],
        users: [
          {id: 'alice', username: 'alice', nickname: 'Alice'},
          {id: 'bob', username: 'bob', nickname: 'Bob'},
          {id: 'disabled', username: 'disabled', nickname: 'Disabled user', disabled: true},
          {id: 'visitor', username: 'visitor', nickname: 'External visitor', source_server_id: 'external'},
        ],
      };
      return <main className="desktop-settings-shell"><div className="settings-page"><ServerTrustPage t={translator("en")} data={data} /></div></main>;
    }
    createRoot(document.getElementById("root")).render(<Fixture />);
  `
      : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url !== route) return next();
      res.setHeader("Content-Type", "text/html");
      res.end(
        await server.transformIndexHtml(
          route,
          `<!doctype html><div id="root"></div><script type="module" src="${entry}"></script>`,
        ),
      );
    });
  },
};
const server = await createServer({
  root,
  configFile: false,
  cacheDir: join(requireRunTemp(), "vite-cache"),
  plugins: [plugin, react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;
try {
  await server.listen();
  browser = await chromium.launchPersistentContext(
    join(requireRunTemp(), "chrome"),
    {
      channel: "chrome",
      headless: true,
      viewport: { width: 960, height: 850 },
    },
  );
  const page = browser.pages()[0];
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.addInitScript(() => {
    const expires_at = Date.now() / 1000 + 3600;
    const requests = [
      {
        id: "in",
        peer_id: "a".repeat(52),
        peer_name: "Source",
        outgoing: false,
        status: "pending",
        expires_at,
      },
    ];
    const peers = [];
    window.calls = [];
    window.mygo = {
      on() {},
      async call(method, ...args) {
        window.calls.push([method, ...args]);
        if (method === "DesktopSettings.TSNetLogin") return true;
        if (method === "DesktopSettings.PreviewFederationTrust")
          return { id: "c".repeat(52), name: "Destination", accepting: true };
        if (method === "DesktopSettings.SubmitFederationTrust")
          requests.push({
            id: "out",
            peer_id: args[2],
            outgoing: true,
            network: true,
            status: "pending",
            expires_at,
          });
        if (method === "DesktopSettings.ApproveFederationTrust") {
          requests.find((r) => r.id === args[0]).status = "approved";
          peers.push({
            id: "a".repeat(52),
            name: "Source",
            incoming: true,
            outgoing: false,
          });
        }
        if (args[0] === "check") {
          requests.find((r) => r.outgoing).status = "approved";
          peers.push({
            id: "c".repeat(52),
            name: "Destination",
            incoming: false,
            outgoing: true,
            all_users: false,
            users: [],
          });
        }
        if (method === "DesktopSettings.UpdateFederationTrustReturnRoute") {
          peers.find((p) => p.id === args[0]).accept = true;
        }
        if (method === "DesktopSettings.SetFederationTrustUsers") {
          const peer = peers.find((p) => p.id === args[0]);
          peer.all_users = args[1];
          peer.users = args[2];
        }
        return {
          server_id: "b".repeat(52),
          inbox_open: true,
          requests: structuredClone(requests),
          peers: structuredClone(peers),
        };
      },
    };
  });
  await page.goto(
    `http://127.0.0.1:${server.httpServer.address().port}${route}`,
  );
  await page.getByText("No trusted servers yet").waitFor();
  const sent = page.getByRole("heading", { name: "Outgoing trust requests" });
  assert.equal(await sent.count(), 0);
  assert.equal(
    await page.getByRole("button", { name: "Manual code exchange" }).count(),
    0,
  );
  await page.getByRole("button", { name: "Copy server ID" }).click();
  const tabs = page.getByRole("tablist", { name: "Connection method" });
  const publicTab = tabs.getByRole("tab", { name: "Public HTTPS" });
  const tailscaleTab = tabs.getByRole("tab", { name: "Tailscale" });
  assert.equal(await publicTab.getAttribute("aria-selected"), "true");
  await publicTab.press("ArrowRight");
  assert.equal(await tailscaleTab.getAttribute("aria-selected"), "true");
  const address = page.getByRole("textbox", {
    name: "Tailscale HTTPS address",
  });
  await address.fill("https://destination.example.ts.net");
  await tabs.getByRole("tab", { name: "Spool Relay" }).click();
  assert.equal(
    await page
      .locator('input[aria-label="Trust Relay route"]')
      .getAttribute("type"),
    "password",
  );
  await tailscaleTab.click();
  assert.equal(
    await address.inputValue(),
    "https://destination.example.ts.net",
  );
  const connect = page.getByRole("button", {
    name: "Connect and verify server",
  });
  assert.ok(await connect.isDisabled());
  await page.getByRole("button", { name: "Sign in to Tailscale" }).click();
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("test:tailnet", { detail: { State: "Registered" } }),
    ),
  );
  await page.getByRole("status").filter({ hasText: "Connected" }).waitFor();
  await connect.click();
  await page
    .getByText("Verify this server ID before sending the request.")
    .waitFor();
  assert.ok(
    !(await page.evaluate(() => window.calls)).some(
      ([method]) => method === "DesktopSettings.SubmitFederationTrust",
    ),
  );
  await page.getByRole("button", { name: "Send trust request" }).click();
  await page.getByRole("button", { name: "Check decision" }).waitFor();
  await sent.waitFor();
  assert.equal(
    await page.getByRole("button", { name: "Copy code", exact: true }).count(),
    0,
  );
  await page.getByRole("button", { name: "Review trust request" }).click();
  await page.getByRole("checkbox", { name: "Alpha" }).check();
  await page.getByRole("checkbox", { name: "Beta" }).check();
  assert.equal(
    await page.getByRole("checkbox", { name: "Archived" }).count(),
    0,
  );
  await page
    .getByRole("combobox", { name: "Alpha · Shared session permissions" })
    .selectOption("control");
  await page
    .getByRole("combobox", { name: "Beta · In-app session permissions" })
    .selectOption("create");
  await page.getByRole("button", { name: "Approve trust" }).click();
  await page.getByText("Peer → this server", { exact: true }).waitFor();
  assert.equal(
    await page.getByRole("button", { name: "Manage allowed users" }).count(),
    0,
  );
  await page.getByText("Return route not configured").waitFor();
  await page.getByRole("button", { name: "Configure return route" }).click();
  const back = page.getByRole("tablist", {
    name: "Route back to the requesting server",
  });
  const saveRoute = page.getByRole("button", { name: "Save return route" });
  assert.ok(await saveRoute.isDisabled());
  await back.getByRole("tab", { name: "Public HTTPS" }).click();
  await page
    .getByRole("textbox", { name: "Public HTTPS address" })
    .fill("https://source.example.test");
  await saveRoute.click();
  await page.getByText("Cross-server sign-in enabled").waitFor();
  await page.getByRole("button", { name: "Change return route" }).waitFor();
  await page.getByRole("button", { name: "Check decision" }).click();
  const manage = page.getByRole("button", { name: "Manage allowed users" });
  await sent.waitFor({ state: "hidden" });
  assert.equal(
    await page.getByRole("button", { name: "Copy code", exact: true }).count(),
    0,
  );
  await manage.click();
  await page.getByRole("checkbox", { name: "Alice (alice)" }).check();
  assert.ok(
    await page
      .getByRole("checkbox", { name: "Disabled user (disabled)" })
      .isDisabled(),
  );
  assert.equal(
    await page
      .getByRole("checkbox", { name: "External visitor (visitor)" })
      .count(),
    0,
  );
  await page.getByRole("button", { name: "Save allowed users" }).click();
  await page.getByText("Allowed users: 1").waitFor();
  await manage.click();
  assert.ok(
    await page.getByRole("checkbox", { name: "Alice (alice)" }).isChecked(),
  );
  await page.getByRole("switch", { name: "Allow all local users" }).click();
  await page.getByRole("button", { name: "Save allowed users" }).click();
  await page.getByText("All enabled local users are allowed.").waitFor();
  assert.deepEqual(await page.evaluate(() => window.calls), [
    ["DesktopSettings.FederationTrust", "list", ""],
    ["DesktopSettings.Action", { kind: "copy-server-trust-id" }],
    ["DesktopSettings.TSNetLogin"],
    [
      "DesktopSettings.PreviewFederationTrust",
      "tailscale",
      "https://destination.example.ts.net",
    ],
    [
      "DesktopSettings.SubmitFederationTrust",
      "tailscale",
      "https://destination.example.ts.net",
      "c".repeat(52),
    ],
    [
      "DesktopSettings.ApproveFederationTrust",
      "in",
      {
        projects: [
          { id: "alpha", access: "control", own_access: "read-only" },
          { id: "beta", access: "read-only", own_access: "create" },
        ],
        transport: "",
        address: "",
      },
    ],
    [
      "DesktopSettings.UpdateFederationTrustReturnRoute",
      "a".repeat(52),
      "https",
      "https://source.example.test",
    ],
    ["DesktopSettings.FederationTrust", "check", "out"],
    [
      "DesktopSettings.SetFederationTrustUsers",
      "c".repeat(52),
      false,
      ["alice"],
    ],
    ["DesktopSettings.SetFederationTrustUsers", "c".repeat(52), true, []],
  ]);
  assert.deepEqual(errors, []);
  console.log(
    "Server trust tabs, Tailscale sign-in, project approval, and allowed users passed",
  );
} finally {
  await browser?.close();
  await server.close();
}
