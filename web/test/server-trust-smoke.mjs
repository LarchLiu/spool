import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { createServer } from "vite";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = fileURLToPath(
  new URL("../../build/artifacts/server-trust/", import.meta.url),
);
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
      const [language, setLanguage] = useState('en');
      useEffect(() => {
        const listener = (event) => setTSNet(event.detail);
        const translate = (event) => setLanguage(event.detail);
        window.addEventListener('test:tailnet', listener);
        window.addEventListener('test:language', translate);
        return () => {
          window.removeEventListener('test:tailnet', listener);
          window.removeEventListener('test:language', translate);
        };
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
      return <div className="desktop-settings-shell"><div className="settings-main-area"><main className="settings-main"><div className="settings-page"><ServerTrustPage t={translator(language)} data={data} /></div></main></div></div>;
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
  server: { host: "127.0.0.1", port: 0, strictPort: true },
});
async function assertDirectionLayout(controls, revokeLabel = "Revoke trust") {
  const left = controls.locator(".settings-trust-details");
  const right = controls.getByRole("button", {
    name: revokeLabel,
    exact: true,
  });
  const status = left.locator(".settings-trust-status");
  const action = left.getByRole("button").first();
  const positions = await Promise.all([
    controls.boundingBox(),
    left.boundingBox(),
    right.boundingBox(),
    status.boundingBox(),
    action.boundingBox(),
  ]);
  const [rowBox, leftBox, rightBox, statusBox, actionBox] = positions;
  assert.ok(positions.every(Boolean));
  assert.ok(
    Math.abs(rightBox.x + rightBox.width - rowBox.x - rowBox.width) < 1,
    "Revoke must align with the right edge",
  );
  assert.ok(
    leftBox.x + leftBox.width <= rightBox.x,
    "Revoke must stay separate from settings and status",
  );
  assert.ok(
    statusBox.y >= actionBox.y + actionBox.height ||
      statusBox.x >= actionBox.x + actionBox.width,
    "Status must follow the settings buttons",
  );
  const directionBox = await controls
    .locator(".settings-trust-direction")
    .boundingBox();
  assert.ok(
    directionBox && directionBox.x + directionBox.width <= actionBox.x,
    "Direction must precede its action button",
  );
}
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
    window.trustPeers = peers;
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
          const peer = peers.find((p) => p.id === args[0]);
          peer.accept = peer.incoming;
        }
        if (method === "DesktopSettings.SetFederationTrustUsers") {
          const peer = peers.find((p) => p.id === args[0]);
          peer.all_users = args[1];
          peer.users = args[2];
        }
        if (
          method === "DesktopSettings.FederationTrust" &&
          ["revoke", "withdraw"].includes(args[0])
        ) {
          if (window.revokeError) throw new Error(window.revokeError);
          const peer = peers.find((p) => p.id === args[1]);
          if (args[0] === "revoke") {
            peer.incoming = false;
            peer.accept = false;
          } else {
            peer.outgoing = false;
            peer.all_users = false;
            peer.users = [];
          }
          peer.revoked = !peer.incoming && !peer.outgoing;
          peer.revocations = [
            ...(peer.revocations ?? []).filter(
              (r) => r.outgoing !== (args[0] === "withdraw"),
            ),
            {
              outgoing: args[0] === "withdraw",
              initiator: "b".repeat(52),
              recorded_at: Date.now(),
              sync_pending: !!window.pendingRevocation,
            },
          ];
          peer.sync_pending = !!window.pendingRevocation;
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
  const alpha = page.getByRole("switch", { name: "Alpha", exact: true });
  const beta = page.getByRole("switch", { name: "Beta", exact: true });
  assert.ok(!(await alpha.isChecked()));
  await alpha.focus();
  await alpha.press("Space");
  assert.ok(await alpha.isChecked());
  await beta.click();
  assert.ok(await beta.isChecked());
  await beta.click();
  assert.ok(!(await beta.isChecked()));
  assert.equal(
    await page
      .getByRole("group", { name: "Beta · Shared session permissions" })
      .count(),
    0,
  );
  await beta.click();
  assert.equal(await page.getByRole("switch", { name: "Archived" }).count(), 0);
  assert.equal(await page.getByRole("checkbox").count(), 0);
  for (const toggle of [alpha, beta]) {
    const bounds = await toggle.boundingBox();
    assert.ok(bounds && bounds.width <= 40 && bounds.height <= 24);
  }
  assert.equal(await page.locator("select").count(), 0);
  const sharedPermissions = page.getByRole("group", {
    name: "Alpha · Shared session permissions",
  });
  const ownPermissions = page.getByRole("group", {
    name: "Beta · In-app session permissions",
  });
  for (const group of [sharedPermissions, ownPermissions]) {
    assert.equal(
      await group
        .getByRole("button", { name: "Read-only", exact: true })
        .getAttribute("aria-pressed"),
      "true",
    );
  }
  const controlSessions = sharedPermissions.getByRole("button", {
    name: "Can control sessions",
    exact: true,
  });
  await controlSessions.click();
  assert.equal(await controlSessions.getAttribute("aria-pressed"), "true");
  const createSessions = ownPermissions.getByRole("button", {
    name: "Can create",
    exact: true,
  });
  await createSessions.focus();
  await createSessions.press("Space");
  assert.equal(await createSessions.getAttribute("aria-pressed"), "true");
  for (const group of [sharedPermissions, ownPermissions]) {
    assert.equal(
      await group
        .getByRole("button", { name: "Read-only", exact: true })
        .getAttribute("aria-pressed"),
      "false",
    );
  }
  const reviewForm = page.locator("form").filter({ has: alpha });
  await mkdir(output, { recursive: true });
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 850 });
    for (const control of [sharedPermissions, ownPermissions]) {
      const permission = page
        .locator(".settings-member-permission")
        .filter({ has: control });
      const label = await permission.locator(":scope > span").boundingBox();
      const bounds = await control.boundingBox();
      assert.ok(label && bounds);
      assert.ok(
        bounds.x >= 0 && bounds.x + bounds.width <= width,
        `Permission buttons must fit the ${width}px viewport: ${JSON.stringify(bounds)}`,
      );
      assert.ok(
        bounds.y >= label.y + label.height ||
          (bounds.x >= label.x + label.width &&
            Math.abs(
              bounds.y + bounds.height / 2 - label.y - label.height / 2,
            ) <= 2),
        `Permission label and buttons must not overlap at ${width}px`,
      );
    }
    await reviewForm.screenshot({
      path: join(output, `default-projects-${width}.png`),
    });
  }
  await page.setViewportSize({ width: 960, height: 850 });
  await page.getByRole("button", { name: "Approve trust" }).click();
  await page.getByText("Peer → this server", { exact: true }).waitFor();
  assert.equal(
    await page.getByRole("button", { name: "Manage allowed users" }).count(),
    0,
  );
  await page.getByText("Return route not configured").waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "Revoke trust", exact: true })
      .count(),
    1,
    "Approval can be revoked before a return route is configured",
  );
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
  assert.equal(
    await page
      .getByRole("button", { name: "Revoke trust", exact: true })
      .count(),
    2,
  );
  await sent.waitFor({ state: "hidden" });
  assert.equal(
    await page.getByRole("button", { name: "Copy code", exact: true }).count(),
    0,
  );
  await manage.click();
  const alice = page.getByRole("switch", { name: "Alice (alice)" });
  assert.ok(!(await alice.isChecked()));
  await alice.click();
  assert.ok(await alice.isChecked());
  assert.equal(await page.getByRole("checkbox").count(), 0);
  const userToggleBounds = await alice.boundingBox();
  assert.ok(
    userToggleBounds &&
      userToggleBounds.width <= 40 &&
      userToggleBounds.height <= 24,
  );
  assert.ok(
    await page
      .getByRole("switch", { name: "Disabled user (disabled)" })
      .isDisabled(),
  );
  assert.equal(
    await page
      .getByRole("switch", { name: "External visitor (visitor)" })
      .count(),
    0,
  );
  const allowedUsersForm = page.locator("form").filter({ has: alice });
  for (const toggle of [
    alice,
    page.getByRole("switch", { name: "Bob (bob)" }),
  ]) {
    const position = await toggle.evaluate(async (button) => {
      await Promise.all(
        button
          .getAnimations({ subtree: true })
          .map((animation) => animation.finished),
      );
      const track = button.getBoundingClientRect();
      const thumb = button.querySelector("span").getBoundingClientRect();
      const checked = button.getAttribute("aria-checked") === "true";
      return {
        label: button.getAttribute("aria-label"),
        checked,
        inset: checked ? track.right - thumb.right : thumb.left - track.left,
      };
    });
    assert.ok(
      Math.abs(position.inset - 2) < 0.1,
      `Switch thumb must rest at the ${position.checked ? "right" : "left"} edge: ${JSON.stringify(position)}`,
    );
  }
  await allowedUsersForm.screenshot({
    path: join(output, "allowed-users.png"),
    animations: "disabled",
  });
  await page.getByRole("button", { name: "Save allowed users" }).click();
  await page.getByText("Allowed users: 1").waitFor();
  const trustedServersCard = page
    .locator(".settings-card")
    .filter({ has: manage });
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 850 });
    for (const controls of await page
      .locator(".settings-trust-controls")
      .all()) {
      await assertDirectionLayout(controls);
    }
    await trustedServersCard.screenshot({
      path: join(output, `allowed-users-summary-${width}.png`),
      animations: "disabled",
    });
  }
  await page.setViewportSize({ width: 960, height: 850 });
  await manage.click();
  assert.ok(
    await page.getByRole("switch", { name: "Alice (alice)" }).isChecked(),
  );
  await page.getByRole("switch", { name: "Allow all local users" }).click();
  await page
    .locator("form")
    .filter({
      has: page.getByRole("switch", { name: "Allow all local users" }),
    })
    .screenshot({
      path: join(output, "allowed-users-all.png"),
      animations: "disabled",
    });
  await page.getByRole("button", { name: "Save allowed users" }).click();
  await page.getByText("All enabled local users are allowed.").waitFor();
  await trustedServersCard.screenshot({
    path: join(output, "allowed-users-summary-all.png"),
    animations: "disabled",
  });
  assert.deepEqual(
    await page.evaluate(() =>
      window.calls.filter(
        (call, index, all) =>
          call[0] !== "DesktopSettings.FederationTrust" ||
          call[1] !== "list" ||
          index ===
            all.findIndex(
              (c) =>
                c[0] === "DesktopSettings.FederationTrust" && c[1] === "list",
            ),
      ),
    ),
    [
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
    ],
  );
  const sourceRow = page
    .locator(".settings-row")
    .filter({ has: page.locator(".settings-trust-directions") })
    .filter({ has: page.getByText("Source", { exact: true }) });
  const activeCard = page.locator("section.settings-group").filter({
    has: page.getByRole("heading", { name: "Trusted servers", exact: true }),
  });
  const revokedSection = page.locator("details.settings-trust-revoked");
  const revokedSummary = revokedSection.locator("summary");
  assert.equal(await revokedSection.count(), 0);
  const revoke = sourceRow.getByRole("button", {
    name: "Revoke trust",
    exact: true,
  });
  const confirm = page.getByRole("button", {
    name: "Confirm revocation",
    exact: true,
  });
  await page.evaluate(() =>
    window.dispatchEvent(new CustomEvent("test:language", { detail: "zh-CN" })),
  );
  const trustedCard = page.locator(".settings-group").filter({
    has: page.getByRole("heading", { name: "已建立信任的服务器", exact: true }),
  });
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => {
      document.documentElement.dataset.theme = theme;
    }, theme);
    await trustedCard.screenshot({
      path: join(output, `trusted-server-controls-${theme}.png`),
      animations: "disabled",
    });
  }
  await page.setViewportSize({ width: 390, height: 850 });
  await trustedCard.screenshot({
    path: join(output, "trusted-server-controls-narrow.png"),
    animations: "disabled",
  });
  await page.setViewportSize({ width: 960, height: 850 });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "light";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "en" }));
  });
  await revoke.click();
  await confirm.waitFor();
  assert.equal(
    (await page.evaluate(() => window.calls)).filter(
      ([method, action]) =>
        method === "DesktopSettings.FederationTrust" && action === "revoke",
    ).length,
    0,
    "Opening confirmation must not revoke access",
  );
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  assert.equal(await confirm.count(), 0);
  await revoke.click();
  await page.evaluate(() => {
    window.revokeError = "Could not revoke authorization";
  });
  await confirm.click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Could not revoke authorization" })
    .waitFor();
  assert.ok(
    await confirm.isEnabled(),
    "Failed revocation must remain retryable",
  );
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  await page.evaluate(() => {
    window.revokeError = "";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "zh-CN" }));
  });
  await page.getByRole("button", { name: "确认撤销", exact: true }).waitFor();
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => {
      document.documentElement.dataset.theme = theme;
    }, theme);
    await trustedCard.screenshot({
      path: join(output, `revoke-authorization-${theme}.png`),
      animations: "disabled",
    });
  }
  await page.setViewportSize({ width: 390, height: 850 });
  const narrowConfirm = await page
    .getByRole("button", { name: "确认撤销", exact: true })
    .boundingBox();
  assert.ok(
    narrowConfirm &&
      narrowConfirm.x >= 0 &&
      narrowConfirm.x + narrowConfirm.width <= 390,
  );
  await trustedCard.screenshot({
    path: join(output, "revoke-authorization-narrow.png"),
    animations: "disabled",
  });
  await page.setViewportSize({ width: 960, height: 850 });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "light";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "en" }));
  });
  await page.evaluate(() => {
    window.pendingRevocation = true;
  });
  await confirm.click();
  await revokedSummary
    .getByText("Revoked trust (1)", { exact: true })
    .waitFor();
  assert.equal(
    await revokedSection.getAttribute("open"),
    null,
    "Revoked records are collapsed by default",
  );
  assert.equal(
    await activeCard.getByText("Source", { exact: true }).count(),
    0,
  );
  assert.equal(await sourceRow.isVisible(), false);
  await revokedSummary.focus();
  await revokedSummary.press("Enter");
  await sourceRow.getByText("Trust revoked", { exact: true }).waitFor();
  await sourceRow
    .getByText("Revoked by this server", { exact: true })
    .waitFor();
  await sourceRow
    .getByText("Waiting to sync revocation", { exact: true })
    .waitFor();
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "Configure return route", exact: true })
      .count(),
    1,
    "A target that initiated revocation may configure its pending notification route",
  );
  assert.equal(
    await revokedSection.getByText("Source", { exact: true }).count(),
    1,
    "Local revocation is archived before the peer acknowledges it",
  );
  assert.equal(await revoke.count(), 0);
  assert.equal(
    await page.getByRole("button", { name: "Change return route" }).count(),
    0,
  );
  await page.getByText("All enabled local users are allowed.").waitFor();
  await page.evaluate(() => {
    window.pendingRevocation = false;
  });
  await page.getByRole("button", { name: "Revoke trust", exact: true }).click();
  await confirm.click();
  await revokedSummary
    .getByText("Revoked trust (2)", { exact: true })
    .waitFor();
  await activeCard
    .getByText("No active server trust", { exact: true })
    .waitFor();
  assert.equal(
    await activeCard.locator(".settings-trust-directions").count(),
    0,
  );
  assert.equal(
    await page.getByText("Trust revoked", { exact: true }).count(),
    2,
  );
  assert.equal(
    await page.getByRole("button", { name: "Manage allowed users" }).count(),
    0,
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Revoke trust", exact: true })
      .count(),
    0,
  );
  assert.deepEqual(
    (await page.evaluate(() => window.calls)).filter(
      ([method, action]) =>
        method === "DesktopSettings.FederationTrust" &&
        ["revoke", "withdraw"].includes(action),
    ),
    [
      ["DesktopSettings.FederationTrust", "revoke", "a".repeat(52)],
      ["DesktopSettings.FederationTrust", "revoke", "a".repeat(52)],
      ["DesktopSettings.FederationTrust", "withdraw", "c".repeat(52)],
    ],
  );
  // A reciprocal relationship lets the administrator choose one direction.
  await page.evaluate(() => {
    Object.assign(window.trustPeers[0], {
      incoming: true,
      outgoing: true,
      accept: true,
      revoked: false,
      users: ["alice"],
      all_users: false,
      sync_pending: false,
      revocations: [],
    });
    document.documentElement.dataset.theme = "dark";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "zh-CN" }));
  });
  await sourceRow.getByText("已放行 1 人", { exact: true }).waitFor();
  await sourceRow.getByText("已开启跨服登录", { exact: true }).waitFor();
  await revokedSummary
    .getByText("已撤销的信任（1）", { exact: true })
    .waitFor();
  assert.equal(
    await revokedSection.getByText("Source", { exact: true }).count(),
    0,
    "Reapproval moves the server back to active trust",
  );
  const directionRows = sourceRow.locator(".settings-trust-controls");
  assert.equal(await directionRows.count(), 2);
  assert.equal(
    await directionRows
      .nth(0)
      .getByRole("button", { name: "管理放行人员", exact: true })
      .count(),
    1,
  );
  assert.equal(
    await directionRows
      .nth(0)
      .getByText("已开启跨服登录", { exact: true })
      .count(),
    0,
  );
  assert.equal(
    await directionRows
      .nth(1)
      .getByRole("button", { name: "更改回连线路", exact: true })
      .count(),
    1,
  );
  assert.equal(
    await directionRows
      .nth(1)
      .getByText("已放行 1 人", { exact: true })
      .count(),
    0,
  );
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 850 });
    for (const controls of await directionRows.all())
      await assertDirectionLayout(controls, "撤销信任");
    const [first, second] = await Promise.all([
      directionRows.nth(0).boundingBox(),
      directionRows.nth(1).boundingBox(),
    ]);
    assert.ok(
      second.y >= first.y + first.height,
      "Trust directions must occupy separate rows",
    );
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "Direction rows must fit the viewport",
    );
    for (const theme of ["light", "dark"]) {
      await page.evaluate((theme) => {
        document.documentElement.dataset.theme = theme;
      }, theme);
      await sourceRow.screenshot({
        path: join(
          output,
          width === 960
            ? `reciprocal-trust-${theme}.png`
            : `reciprocal-trust-narrow-${theme}.png`,
        ),
        animations: "disabled",
      });
    }
  }
  await page.setViewportSize({ width: 960, height: 850 });
  await page.evaluate(() => {
    window.trustPeers[0].sync_pending = true;
    document.documentElement.dataset.theme = "light";
    window.dispatchEvent(new CustomEvent("test:language", { detail: "en" }));
  });
  await page.getByText("Waiting to sync revocation", { exact: true }).waitFor();
  await sourceRow
    .getByRole("group", { name: "This server → peer", exact: true })
    .getByRole("button", { name: "Revoke trust", exact: true })
    .click();
  assert.equal(
    await page
      .getByRole("combobox", { name: "Trust direction", exact: true })
      .count(),
    0,
  );
  await page
    .getByText(
      "Revokes This server → peer and immediately notifies the peer.",
      { exact: false },
    )
    .waitFor();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await sourceRow
    .getByRole("group", { name: "Peer → this server", exact: true })
    .getByRole("button", { name: "Revoke trust", exact: true })
    .click();
  await page
    .getByText(
      "Revokes Peer → this server and immediately notifies the peer.",
      { exact: false },
    )
    .waitFor();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await sourceRow
    .getByRole("group", { name: "This server → peer", exact: true })
    .getByRole("button", { name: "Revoke trust", exact: true })
    .click();
  // A remote cancellation during confirmation must not switch to the reverse direction.
  await page.evaluate(() => {
    window.trustPeers[0].outgoing = false;
  });
  await confirm.waitFor({ state: "hidden" });
  assert.equal(
    await sourceRow.getByText("Peer → this server", { exact: true }).count(),
    1,
  );
  await page.evaluate(() => {
    window.trustPeers[0].outgoing = true;
  });
  await sourceRow.getByText("This server → peer", { exact: true }).waitFor();
  await sourceRow
    .getByRole("group", { name: "This server → peer", exact: true })
    .getByRole("button", { name: "Revoke trust", exact: true })
    .click();
  assert.equal(
    await page
      .getByRole("combobox", { name: "Trust direction", exact: true })
      .count(),
    0,
  );
  await confirm.click();
  await sourceRow.getByText("Peer → this server", { exact: true }).waitFor();
  assert.equal(
    await activeCard.getByText("Source", { exact: true }).count(),
    1,
    "A remaining direction keeps the server in active trust",
  );
  assert.equal(
    await revokedSection.getByText("Source", { exact: true }).count(),
    0,
  );
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "Manage allowed users" })
      .count(),
    0,
  );
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "Change return route" })
      .count(),
    1,
  );
  assert.deepEqual(
    (await page.evaluate(() => window.calls))
      .filter(
        ([method, action]) =>
          method === "DesktopSettings.FederationTrust" && action === "withdraw",
      )
      .at(-1),
    ["DesktopSettings.FederationTrust", "withdraw", "a".repeat(52)],
  );
  // Receipt of the other server's cancellation refreshes this view automatically.
  await page.evaluate(() =>
    Object.assign(window.trustPeers[0], {
      incoming: false,
      accept: false,
      revoked: true,
      sync_pending: false,
      revocations: [
        ...window.trustPeers[0].revocations,
        { outgoing: false, initiator: "a".repeat(52), recorded_at: Date.now() },
      ],
    }),
  );
  await sourceRow.getByText("Trust revoked", { exact: true }).waitFor();
  await sourceRow
    .getByText("Revoked by this server", { exact: true })
    .waitFor();
  await sourceRow.getByText("Revoked by peer", { exact: true }).waitFor();
  await activeCard
    .getByText("No active server trust", { exact: true })
    .waitFor();
  assert.equal(
    await revokedSection.getByText("Source", { exact: true }).count(),
    1,
    "The last remote revocation moves the record into revoked trust",
  );
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "Revoke trust", exact: true })
      .count(),
    0,
  );
  assert.equal(
    await page.getByText("Waiting to sync revocation", { exact: true }).count(),
    0,
  );
  await revokedSummary.click();
  await page.evaluate(() => {
    window.trustPeers[0].sync_pending = true;
    window.trustPeers[0].revocations.find(
      (record) => record.outgoing,
    ).sync_pending = true;
  });
  await revokedSummary
    .getByText("Waiting to sync revocation", { exact: false })
    .waitFor();
  assert.equal(
    await sourceRow.isVisible(),
    false,
    "Background polling preserves the collapsed state",
  );
  await page.evaluate(() =>
    window.dispatchEvent(new CustomEvent("test:language", { detail: "zh-CN" })),
  );
  await revokedSummary
    .getByText("已撤销的信任（2）", { exact: true })
    .waitFor();
  const trustLists = page.locator(".settings-trust-lists");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => {
      document.documentElement.dataset.theme = theme;
    }, theme);
    await trustLists.screenshot({
      path: join(output, `revoked-trust-collapsed-${theme}.png`),
      animations: "disabled",
    });
  }
  await revokedSummary.click();
  await sourceRow.getByText("等待同步撤销", { exact: true }).waitFor();
  await sourceRow.getByText("本机主动撤销", { exact: true }).waitFor();
  await sourceRow.getByText("对方主动撤销", { exact: true }).waitFor();
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "配置回连线路", exact: true })
      .count(),
    0,
    "A source withdrawal or a peer-initiated revocation cannot configure a notification route",
  );
  // A completed target-side notification must not borrow the other direction's pending state.
  await page.evaluate(() => {
    const incoming = window.trustPeers[0].revocations.find(
      (record) => !record.outgoing,
    );
    incoming.initiator = "b".repeat(52);
    incoming.sync_pending = false;
  });
  await sourceRow
    .getByText("对方主动撤销", { exact: true })
    .waitFor({ state: "hidden" });
  assert.equal(
    await sourceRow
      .getByRole("button", { name: "配置回连线路", exact: true })
      .count(),
    0,
    "The completed incoming revocation stays hidden while outgoing synchronization is pending",
  );
  await page.evaluate(() => {
    window.trustPeers[0].revocations.find(
      (record) => !record.outgoing,
    ).sync_pending = true;
  });
  const configureRevokedRoute = sourceRow.getByRole("button", {
    name: "配置回连线路",
    exact: true,
  });
  await configureRevokedRoute.waitFor();
  await configureRevokedRoute.click();
  const saveRevokedRoute = revokedSection.getByRole("button", {
    name: "保存回连线路",
    exact: true,
  });
  await saveRevokedRoute.waitFor();
  await page.evaluate(() => {
    window.trustPeers[0].revocations.find(
      (record) => !record.outgoing,
    ).sync_pending = false;
  });
  await saveRevokedRoute.waitFor({ state: "hidden" });
  assert.equal(
    await configureRevokedRoute.count(),
    0,
    "Acknowledgement closes an open route editor even if another direction is pending",
  );
  await page.evaluate(() => {
    const incoming = window.trustPeers[0].revocations.find(
      (record) => !record.outgoing,
    );
    incoming.initiator = "a".repeat(52);
    incoming.sync_pending = true;
  });
  await sourceRow.getByText("对方主动撤销", { exact: true }).waitFor();
  assert.equal(
    await configureRevokedRoute.count(),
    0,
    "A peer-initiated incoming revocation has no route action even with pending state",
  );
  for (const width of [960, 390]) {
    await page.setViewportSize({ width, height: 850 });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((theme) => {
        document.documentElement.dataset.theme = theme;
      }, theme);
      await revokedSection.screenshot({
        path: join(
          output,
          width === 960
            ? `revoked-trust-directions-${theme}.png`
            : `revoked-trust-narrow-${theme}.png`,
        ),
        animations: "disabled",
      });
    }
  }
  await page.setViewportSize({ width: 960, height: 850 });
  await page.evaluate(() => {
    Object.assign(window.trustPeers[0], {
      incoming: true,
      accept: true,
      revoked: false,
      sync_pending: false,
    });
    Object.assign(window.trustPeers[1], { outgoing: true, revoked: false });
    window.dispatchEvent(new CustomEvent("test:language", { detail: "en" }));
  });
  await activeCard.getByText("Source", { exact: true }).waitFor();
  await activeCard.getByText("Destination", { exact: true }).waitFor();
  assert.equal(
    await revokedSection.count(),
    0,
    "The empty revoked section disappears after approvals are restored",
  );
  assert.deepEqual(errors, []);
  console.log(
    "Server trust tabs, Tailscale sign-in, project approval, allowed users, and authorization revocation passed",
  );
} finally {
  await browser?.close();
  await server.close();
}
