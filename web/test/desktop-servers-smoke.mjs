// Production chat and embedded settings pages with synthetic native/API boundaries.
// Run through tools/run.sh. Set SPOOL_NODE_MODULES for Playwright or
// SPOOL_SMOKE_ENGINE=chromium|webkit to select one browser.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import {
  createTestViteServer,
  mockDesktopNavigation,
} from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__servers__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/desktop-servers");
const engines = [
  ["chromium", chromium, { channel: "chrome" }],
  ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
].filter(
  ([engine]) =>
    !process.env.SPOOL_SMOKE_ENGINE || process.env.SPOOL_SMOKE_ENGINE === engine,
);
assert.ok(engines.length, "SPOOL_SMOKE_ENGINE must be chromium or webkit");
const cleanup = [],
  reports = [];
const pairingFixture = path.join(process.env.TMPDIR, "server-pairing");
execFileSync(
  "go",
  ["run", "./tools/relay-qr-fixture", "-out", pairingFixture],
  {
    cwd: root,
    stdio: "inherit",
  },
);
const pairingCode = await readFile(
  path.join(pairingFixture, "pairing-synthetic.txt"),
  "utf8",
);
const pairingPNG = await readFile(
  path.join(pairingFixture, "pairing-synthetic.png"),
);

async function verifySignedOutAction(page, action) {
  const footer = page.locator(".desktop-server-start .side-foot");
  const button = footer.getByRole("button", { name: action, exact: true });
  await button.waitFor();
  const settings = await footer
    .getByRole("link", { name: "Settings", exact: true })
    .boundingBox();
  const box = await button.boundingBox();
  assert.ok(
    box.x >= settings.x + settings.width,
    `${action} follows Settings without an account`,
  );
  for (const control of [
    button,
    page.locator(".desktop-server-welcome > button"),
  ]) {
    const bounds = await control.evaluate((element) => {
      const button = element.getBoundingClientRect();
      const range = document.createRange();
      range.selectNodeContents(element);
      const text = range.getBoundingClientRect();
      return {
        width: button.width,
        dx: Math.abs(text.x + text.width / 2 - button.x - button.width / 2),
        dy: Math.abs(text.y + text.height / 2 - button.y - button.height / 2),
      };
    });
    assert.ok(bounds.width < 180, `${action} stays compact`);
    assert.ok(
      bounds.dx < 1 && bounds.dy < 2,
      `${action} text is centered: ${JSON.stringify(bounds)}`,
    );
  }
}

async function verifyWideWorkspace(page) {
  await page.waitForFunction(() => {
    const sidebar = document.querySelector(".sidebar");
    const workspace = document.querySelector(".workspace");
    return (
      !matchMedia("(max-width: 900px)").matches &&
      sidebar &&
      !sidebar.inert &&
      !sidebar.classList.contains("open") &&
      sidebar.querySelector(".sidebar-collapse svg.lucide-panel-left-close") &&
      !workspace.querySelector(".panel-toggle-left") &&
      Math.abs(
        sidebar.getBoundingClientRect().right -
          workspace.getBoundingClientRect().left,
      ) < 1
    );
  });
}

async function verifyResponsiveWorkspace(page) {
  await page.waitForFunction(
    () => typeof window.fixtureInitialLayoutNarrow === "boolean",
  );
  assert.equal(
    await page.evaluate(() => window.fixtureInitialLayoutNarrow),
    true,
    "the first viewport sample simulates a not-yet-sized embedded view",
  );
  await verifyWideWorkspace(page);
  await page
    .getByRole("button", { name: "Open file sidebar", exact: true })
    .click();
  await page.waitForFunction(
    () => !document.querySelector(".file-sidebar").inert,
  );
  for (const width of [900, 840]) {
    await page.setViewportSize({ width, height: 700 });
    await page.waitForFunction(
      () =>
        document.querySelector(".sidebar").inert &&
        document.querySelector(".file-sidebar").inert &&
        document.querySelector(".panel-toggle-left svg.lucide-menu"),
    );
    await page
      .getByRole("button", { name: "Open sidebar", exact: true })
      .click();
    const sidebar = page.locator(".sidebar");
    await sidebar
      .getByRole("button", { name: "Close sidebar", exact: true })
      .waitFor();
    assert.equal(
      Math.round((await sidebar.boundingBox()).width),
      width,
      "narrow windows show the sidebar as a full-width drawer",
    );
    await sidebar
      .getByRole("button", { name: "Close sidebar", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Open file sidebar", exact: true })
      .click();
    await page.waitForFunction(
      () =>
        document.querySelector(".file-sidebar").classList.contains("open") &&
        !document.querySelector(".file-sidebar").inert,
    );
    await page
      .locator(".file-sidebar")
      .getByRole("button", { name: "Collapse file sidebar", exact: true })
      .click();
    await page.setViewportSize({ width: 901, height: 700 });
    await verifyWideWorkspace(page);
    await page.waitForFunction(() => {
      const files = document.querySelector(".file-sidebar");
      const workspace = document
        .querySelector(".workspace")
        .getBoundingClientRect();
      return (
        !files.inert &&
        !files.classList.contains("open") &&
        Math.abs(files.getBoundingClientRect().left - workspace.right) < 1
      );
    });
  }
  await page.setViewportSize({ width: 840, height: 700 });
  await page.reload();
  await page.waitForFunction(
    () => typeof window.fixtureInitialLayoutNarrow === "boolean",
  );
  assert.equal(
    await page.evaluate(() => window.fixtureInitialLayoutNarrow),
    false,
    "a narrow startup also corrects an initial wide viewport sample",
  );
  await page
    .locator(".sidebar")
    .getByRole("button", { name: "Close sidebar", exact: true })
    .click();
  await page.waitForFunction(
    () =>
      document.querySelector(".sidebar").inert &&
      document.querySelector(".panel-toggle-left svg.lucide-menu"),
  );
  await page.setViewportSize({ width: 1280, height: 860 });
  await verifyWideWorkspace(page);
  await page.reload();
  await verifyWideWorkspace(page);
}

async function verifyTabSlide(page, selector, button, animate = true) {
  const motion = await button.evaluate((button) => {
    const tabs = button.closest(".segmented-tabs");
    const marker = tabs.querySelector(".segmented-tabs-indicator");
    button.click();
    return new Promise((resolve) =>
      requestAnimationFrame(() =>
        resolve({
          running: marker
            .getAnimations()
            .some((animation) => animation.playState === "running"),
          selected: button.matches(
            '[aria-current="page"], [aria-selected="true"]',
          ),
          scrollLeft: tabs.querySelector(".segmented-tabs-viewport").scrollLeft,
        }),
      ),
    );
  });
  assert.equal(motion.selected, true);
  assert.equal(
    motion.running,
    animate,
    "the selected background slides unless reduced motion is requested",
  );
  await page.waitForFunction((selector) => {
    const tabs = document.querySelector(selector);
    const marker = tabs.querySelector(".segmented-tabs-indicator");
    const selected = tabs.querySelector(
      '[aria-current="page"], [aria-selected="true"]',
    );
    const a = marker.getBoundingClientRect(),
      b = selected.getBoundingClientRect();
    return (
      marker
        .getAnimations()
        .every((animation) => animation.playState !== "running") &&
      Math.abs(a.x - b.x) < 1 &&
      Math.abs(a.width - b.width) < 1
    );
  }, selector);
  assert.equal(
    await button.evaluate(
      (element) => getComputedStyle(element).backgroundColor,
    ),
    "rgba(0, 0, 0, 0)",
    "the sliding background remains visible behind the selected tab",
  );
  return motion;
}

async function verifyTabPositions(tabs) {
  const geometry = await tabs.evaluate(async (tabs) => {
    const indicator = tabs.querySelector(".segmented-tabs-indicator");
    await Promise.allSettled(indicator.getAnimations().map((animation) => animation.finished));
    const selected = tabs.querySelector(
      '[aria-current="page"], [aria-selected="true"]',
    );
    const marker = tabs
      .querySelector(".segmented-tabs-indicator")
      .getBoundingClientRect();
    const box = selected.getBoundingClientRect();
    const viewport = tabs
      .querySelector(".segmented-tabs-viewport")
      .getBoundingClientRect();
    const buttons = [...tabs.querySelectorAll(".segmented-tab")].map(
      (button) => {
        const bounds = button.getBoundingClientRect();
        const x =
          (Math.max(bounds.left, viewport.left) +
            Math.min(bounds.right, viewport.right)) /
          2;
        return (
          bounds.right <= viewport.left ||
          bounds.left >= viewport.right ||
          button.contains(
            document.elementFromPoint(x, bounds.y + bounds.height / 2),
          )
        );
      },
    );
    return {
      dx: marker.x - box.x,
      dy: marker.y - box.y,
      dw: marker.width - box.width,
      buttons,
    };
  });
  assert.ok(
    Math.abs(geometry.dx) < 1 &&
      Math.abs(geometry.dy) < 1 &&
      Math.abs(geometry.dw) < 1,
    `the selection stays aligned after scrolling: ${JSON.stringify(geometry)}`,
  );
  assert.ok(
    geometry.buttons.every(Boolean),
    "visible tab hit targets follow their rendered positions",
  );
}

const navigation = mockDesktopNavigation();
const load = navigation.load;
navigation.load = function (id) {
  const source = load.call(this, id);
  return (
    source &&
    source
      .replace("export function notifyDesktopSidebarWidth() {}", "")
      .replace("export function reportDesktopServerState() { return () => {}; }", "")
      .replace(
        /export function notifyDesktopServerReady\([^\n]*\) \{[^\n]*\}/,
        "",
      ) +
      `
import { reportDesktopServerState as reportHealth } from ${JSON.stringify(path.join(root, "web/src/runtime.ts"))};
export function reportDesktopServerState(state, surface, authenticated) {
  return reportHealth(state, {
    spoolDesktop: true,
    addEventListener: window.addEventListener.bind(window),
    removeEventListener: window.removeEventListener.bind(window),
    location: { assign(url) {
      const dropped = state === 'login' && window.fixtureLoseHealthState;
      window.fixtureHealthAttempts.push({state, acknowledged: !dropped});
      if (dropped) { window.fixtureLoseHealthState = false; return; }
      const received = new URL(url, location.href).searchParams.get('state');
      window.spoolServers = {...window.spoolServers, entries: window.spoolServers.entries.map(server => server.id === 'local' ? {...server, status: received} : server)};
      window.dispatchEvent(new Event('spool:servers-changed'));
      window.dispatchEvent(new CustomEvent('spool:server-state-ack', {detail: {state: received, authenticated: new URL(url, location.href).searchParams.get('authenticated') === 'true'}}));
    }},
  }, authenticated);
}
export function notifyDesktopServerReady(state, selection, surface, authenticated) {
  const dropped = window.fixtureDropReady > 0 || (state === 'login' && window.fixtureLoseLoginReady);
  window.fixtureReadyAttempts.push({server: window.spoolServers.selected, acknowledged: !dropped});
  if (dropped) {
    if (window.fixtureDropReady > 0) window.fixtureDropReady--;
    if (state === 'login') window.fixtureLoseLoginReady = false;
    window.fixtureDroppedStates.push(state);
  } else {
    window.spoolServers = {...window.spoolServers, entries: window.spoolServers.entries.map(server => server.id === 'local' ? {...server, status: state} : server)};
    window.dispatchEvent(new Event('spool:servers-changed'));
    window.dispatchEvent(new CustomEvent('spool:server-ready-ack', {detail: {state, selection, authenticated}}));
  }
}
export function selectDesktopServer(id, surface, scrollLeft) {
  window.fixtureSelections.push(id);
  if (typeof window.fixtureRestoreScroll === 'number') {
    document.querySelector('.desktop-server-track').scrollLeft = window.fixtureRestoreScroll;
    delete window.fixtureRestoreScroll;
  }
  window.spoolServers = {...window.spoolServers, selected: id, transition: {from: window.spoolServers.selected, started_at: Date.now(), scroll_left: scrollLeft}};
  window.dispatchEvent(new Event('spool:servers-changed'));
  window.dispatchEvent(new Event('spool:server-selected'));
}
export function requestDesktopFederationLogin(source, target, surface, scrollLeft) {
  window.fixtureFederationSelections.push({source, target, scrollLeft});
}
export function connectDesktopServer() {
  window.fixtureConnections++;
}
export function notifyDesktopSidebarWidth(width) {
  window.spoolServers = {...window.spoolServers, sidebar_width: width};
  window.dispatchEvent(new Event('spool:servers-changed'));
}`
  );
};
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [react(), navigation],
    server: { host: "127.0.0.1", port: 0 },
  },
);
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, options] of engines) {
    browser = await type.launch({ ...options, headless: true });
    const context = await browser.newContext({
      viewport: { width: 1280, height: 860 },
      serviceWorkers: "block",
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    page.setDefaultTimeout(10000);
    try {
      await page.addInitScript(() => {
        const nativeMatchMedia = window.matchMedia.bind(window);
        let sampledLayout = false;
        window.matchMedia = (query) => {
          const media = nativeMatchMedia(query);
          if (query !== "(max-width: 900px)" || sampledLayout) return media;
          sampledLayout = true;
          window.fixtureInitialLayoutNarrow = !media.matches;
          return new Proxy(media, {
            get(target, property) {
              if (property === "matches")
                return window.fixtureInitialLayoutNarrow;
              const value = Reflect.get(target, property, target);
              return typeof value === "function" ? value.bind(target) : value;
            },
          });
        };
        window.spoolDesktop = true;
        window.mygo = { platform: "darwin", locale: "en", on: () => () => {} };
        const chrome = () => {
          document.documentElement.dataset.desktopPlatform = "darwin";
        };
        if (document.documentElement) chrome();
        else
          document.addEventListener("DOMContentLoaded", chrome, { once: true });
        window.fixtureSelections = [];
        window.fixtureFederationSelections = [];
        window.fixtureReadyAttempts = [];
        window.fixtureHealthAttempts = [];
        window.fixtureDroppedStates = [];
        window.fixtureDropReady = 0;
        window.fixtureConnections = 0;
        window.fixtureSockets = [];
        window.fixtureSocketReady = true;
        const setInterval = window.setInterval.bind(window);
        window.setInterval = (callback, delay, ...args) =>
          setInterval(callback, delay === 15000 ? 250 : delay, ...args);
        window.spoolServers = {
          selected: "local",
          entries: [
            {
              id: "local",
              name: "本机开发",
              local: true,
              status: "connecting",
            },
            {
              id: "home-1234",
              name: "家里的服务器",
              local: false,
              status: "login",
              client_url: "https://home.example/",
            },
            {
              id: "custom-1234",
              name: "自定义显示名称",
              local: false,
              status: "error",
            },
            {
              id: "office-1234",
              name: "Office workspace with a long name",
              local: false,
              status: "disconnected",
            },
          ],
        };
        window.WebSocket = class extends EventTarget {
          readyState = 0;
          constructor() {
            super();
            window.fixtureSockets.push(this);
            queueMicrotask(() => {
              this.readyState = 1;
              this.onopen?.();
              if (window.fixtureSocketReady)
                this.onmessage?.({ data: JSON.stringify({ type: "ready" }) });
            });
          }
          send() {}
          close() {
            this.readyState = 3;
          }
        };
        localStorage.setItem(
          "spool.panel-layout.v1",
          JSON.stringify({ leftOpen: true, rightOpen: false }),
        );
      });
      let signedIn = true,
        reachable = true,
        sessionStatus = 0,
        invalidSession = false,
        versionStatus = 200,
        invalidVersion = false,
        httpTargets = [];
      let targetPolls = 0;
      await page.route("**/api/**", (route) => {
        const endpoint = new URL(route.request().url()).pathname;
        const json = (value, status = 200) =>
          route.fulfill({
            status,
            contentType: "application/json",
            body: JSON.stringify(value),
          });
        if (endpoint === "/api/me")
          return !reachable
            ? json({ error: "Could not connect to this server." }, 503)
            : sessionStatus
              ? json({ error: "Session endpoint failed." }, sessionStatus)
              : invalidSession
                ? json({})
                : signedIn
                  ? json({
                      user: { id: "user-1234", nickname: "Alex" },
                      csrf: "fixture",
                    })
                  : json({ error: "sign in" }, 401);
        if (endpoint === "/api/version")
          return json(
            invalidVersion
              ? {}
              : { version: "fixture", server_name: "本机开发" },
            versionStatus,
          );
        if (endpoint === "/api/federation/targets") {
          targetPolls++;
          return json(httpTargets);
        }
        if (endpoint === "/api/navigation")
          return json({ projects: [], chats: [] });
        if (endpoint === "/api/agents") return json([]);
        if (endpoint === "/api/profiles")
          return json({ revision: 1, people: [] });
        if (endpoint === "/api/adb") return json({ available: false });
        return json({});
      });
      await page.goto(origin);
      await verifyResponsiveWorkspace(page);
      const tabs = page.locator(".sidebar .desktop-server-tabs");
      await tabs.waitFor();
      await tabs
        .getByRole("button", { name: "本机开发, Connected · Signed in", exact: true })
        .waitFor();
      assert.equal(
        await tabs
          .getByRole("button", { name: "本机开发, Connected · Signed in", exact: true })
          .count(),
        1,
      );
      assert.equal(
        await tabs
          .getByRole("button", {
            name: "自定义显示名称, Connection failed",
            exact: true,
          })
          .count(),
        1,
      );
      const lab = tabs.getByRole("button", {
        name: "Sign in to Shared Lab with this account",
      });
      const savedHome = tabs.getByRole("button", {
        name: "家里的服务器, Connected · Sign-in required",
        exact: true,
      });
      const publishTargets = (targets) => page.evaluate((data) => {
        const socket = window.fixtureSockets.at(-1);
        socket.onmessage({ data: JSON.stringify({ type: "federation_targets", data }) });
      }, targets);
      const labTarget = {
        id: "b".repeat(52), name: "Shared Lab", client_url: "https://lab.example/chat/", client_mode: "tailscale",
      };
      const homeTarget = { id: "c".repeat(52), name: "Home", client_url: "https://home.example/" };
      const readDotColor = (state) => tabs.locator(`.server-status-dot[data-state="${state}"]`)
        .first().evaluate((dot) => getComputedStyle(dot).backgroundColor);
      const dotColors = {
        connected: await readDotColor("connected"),
        login: await readDotColor("login"),
        error: await readDotColor("error"),
        disconnected: await readDotColor("disconnected"),
      };
      const verifyKey = async (button, color) => {
        const icon = button.locator("svg.lucide-key-round");
        await icon.waitFor();
        assert.equal(await button.locator("svg, .server-status-dot").count(), 1);
        const stroke = await icon.evaluate((key) => getComputedStyle(key).stroke);
        if (color) assert.equal(stroke, color);
        return stroke;
      };
      const publishNativeTargets = (targets, federated = false) => page.evaluate(({ targets, federated }) => {
        window.spoolServers = {
          ...window.spoolServers,
          entries: window.spoolServers.entries.map((server) => server.id === "local"
            ? { ...server, federation_targets: targets, federated }
            : server),
        };
        window.dispatchEvent(new Event("spool:servers-changed"));
      }, { targets, federated });
      const publishProgress = (state) => page.evaluate(({ target, state }) => {
        window.dispatchEvent(new CustomEvent("spool:federation", { detail: { source: "local", target, state } }));
      }, { target: labTarget.id, state });
      const labWithStatus = (status) => tabs.getByRole("button", {
        name: `本机开发 → Sign in to Shared Lab with this account, ${status}`,
        exact: true,
      });
      // An API response grants the link, but can't provide its status.
      await publishTargets([{ ...labTarget, status: "connected" }, homeTarget]);
      await verifyKey(labWithStatus("Disconnected"), dotColors.disconnected);
      const homeAlias = tabs.getByRole("button", {
        name: /^本机开发 → Sign in to Home with this account, /,
      });
      await verifyKey(homeAlias, dotColors.disconnected);
      await savedHome.waitFor();
      assert.equal(await homeAlias.evaluate((button) => getComputedStyle(button).borderTopStyle), "solid");

      // Native snapshots repaint the keys on the existing page, without a new live list.
      const nativeLab = { ...labTarget, client_url: "https://LAB.example:443/chat" };
      await publishNativeTargets([{ ...nativeLab, status: "connecting" }]);
      const connectingColor = await verifyKey(labWithStatus("Connecting…"));
      assert.ok(!Object.values(dotColors).includes(connectingColor));
      await publishNativeTargets([{ ...nativeLab, status: "login" }, { ...homeTarget, status: "error" }], true);
      await verifyKey(labWithStatus("Connected · Sign-in required"), dotColors.login);
      await verifyKey(homeAlias, dotColors.error);
      await verifyKey(tabs.getByRole("button", {
        name: "本机开发, Connected · Signed in · Federated sign-in", exact: true,
      }), dotColors.connected);
      await publishNativeTargets([{ ...nativeLab, status: "error" }]);
      await verifyKey(labWithStatus("Connection failed"), dotColors.error);
      const unknownTarget = { id: "d".repeat(52), name: "Studio" };
      await publishTargets([labTarget, homeTarget, unknownTarget]);
      await verifyKey(tabs.getByRole("button", { name: /Sign in to Studio with this account, Disconnected/ }),
        dotColors.disconnected);
      const setFixtureSidebarWidth = (sidebar_width) => page.evaluate((width) => {
        window.spoolServers = { ...window.spoolServers, sidebar_width: width };
        window.dispatchEvent(new Event("spool:servers-changed"));
      }, sidebar_width);
      await page.setViewportSize({ width: 1600, height: 860 });
      await setFixtureSidebarWidth(480);
      await page.waitForFunction(() => Math.round(document.querySelector(".sidebar").getBoundingClientRect().width) === 480);
      await tabs.screenshot({ path: path.join(output, `${engine}-tabs-federation.png`) });
      await setFixtureSidebarWidth(undefined);
      await page.setViewportSize({ width: 1280, height: 860 });
      await publishTargets([{ ...labTarget, client_mode: "" }, homeTarget]);
      await verifyKey(labWithStatus("Disconnected"), dotColors.disconnected);

      await publishProgress("pending");
      const signingIn = tabs.getByRole("button", { name: /Signing in to Shared Lab…/ });
      await signingIn.locator("svg.lucide-loader-circle").waitFor();
      assert.equal(await signingIn.locator("svg, .server-status-dot").count(), 1);
      assert.ok(await signingIn.isDisabled());
      await publishProgress("failed");
      const retry = tabs.getByRole("button", { name: /Could not sign in to Shared Lab\. Try again\./ });
      await verifyKey(retry, dotColors.disconnected);
      assert.deepEqual(await retry.evaluate((button) => {
        const style = getComputedStyle(button);
        return [style.borderTopStyle, style.borderTopColor];
      }), ["solid", dotColors.error]);
      await publishProgress("done");
      await publishNativeTargets([]);
      await labWithStatus("Disconnected").waitFor();

      // 模拟另外两个页面已登录后，从与来源无关的 C 页面选择其中一个入口。
      await publishTargets([]);
      await lab.waitFor({ state: "detached" });
      await page.evaluate(() => {
        window.fixtureOriginalServers = window.spoolServers;
        const target = { id: "c".repeat(52), name: "Home", client_url: "https://home.example/" };
        window.spoolServers = {
          ...window.spoolServers,
          selected: "office-1234",
          entries: [...window.spoolServers.entries.map((server) => {
            if (server.id !== "local" && server.id !== "custom-1234") return server;
            return { ...server, status: "connected", federation_targets: [target] };
          }),
            { id: "trusted-local-home", name: "Home", local: false, status: "connected", federation_source: "local", federation_target: target.id },
            { id: "trusted-custom-home", name: "Home", local: false, status: "login", federation_source: "custom-1234", federation_target: target.id },
          ],
        };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      const aliases = tabs.locator(".desktop-federation-target");
      const groupedOffice = aliases.filter({ hasText: "Home" });
      await groupedOffice.nth(1).waitFor({ state: "attached" });
      assert.deepEqual(await groupedOffice.evaluateAll((buttons) => buttons.map((button) => [
        button.previousElementSibling.textContent, button.getAttribute("aria-label"),
      ])), [
        ["本机开发", "本机开发 → Sign in to Home with this account, Connected · Signed in"],
        ["自定义显示名称", "自定义显示名称 → Sign in to Home with this account, Connected · Sign-in required"],
      ]);
      await savedHome.waitFor();
      await groupedOffice.nth(1).click();
      await groupedOffice.nth(0).click();
      const trustedSelections = await page.evaluate(() => window.fixtureFederationSelections);
      assert.deepEqual(trustedSelections.map(({ source, target }) => [source, target]), [
        ["custom-1234", "c".repeat(52)], ["local", "c".repeat(52)],
      ]);
      assert.ok(trustedSelections.every(({ scrollLeft }) => Number.isFinite(scrollLeft)));
      await page.evaluate(() => {
        window.spoolServers = {
          ...window.spoolServers,
          selected: "trusted-custom-home",
          selected_tab: `federation:custom-1234:${"c".repeat(52)}`,
        };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await tabs.locator('[aria-current="page"][aria-label^="自定义显示名称 →"]').waitFor();
      await page.waitForFunction(() => {
        const tabs = document.querySelector(".sidebar .desktop-server-tabs");
        const selected = tabs.querySelector(".desktop-federation-target.selected");
        const indicator = tabs.querySelector(".segmented-tabs-indicator");
        if (!selected || !indicator) return false;
        const button = selected.getBoundingClientRect();
        const marker = indicator.getBoundingClientRect();
        return parseFloat(getComputedStyle(indicator).borderTopLeftRadius) >= marker.height / 2 &&
          Math.abs(button.x - marker.x) < 1 && Math.abs(button.y - marker.y) < 1 &&
          Math.abs(button.width - marker.width) < 1 && Math.abs(button.height - marker.height) < 1;
      });
      // Capture an unclipped source and its selected trusted capsule together.
      await setFixtureSidebarWidth(480);
      await page.evaluate(() => {
        window.spoolServers = {
          ...window.spoolServers,
          selected: "trusted-local-home",
          selected_tab: `federation:local:${"c".repeat(52)}`,
        };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await page.waitForFunction(() => {
        const tabs = document.querySelector(".sidebar .desktop-server-tabs");
        const selected = tabs.querySelector('.desktop-federation-target[aria-current="page"]');
        const indicator = tabs.querySelector(".segmented-tabs-indicator");
        const marker = indicator.getBoundingClientRect();
        const button = selected?.getBoundingClientRect();
        const viewport = tabs.getBoundingClientRect();
        return button && Math.abs(button.x - marker.x) < 1 &&
          button.left >= viewport.left && button.right <= viewport.right;
      });
      await page.mouse.move(800, 300);
      await page.screenshot({ path: path.join(output, `${engine}-trusted-selected.png`) });
      await tabs.screenshot({ path: path.join(output, `${engine}-trusted-selected-tabs.png`) });
      const theme = await page.evaluate(() => {
        const previous = document.documentElement.dataset.theme;
        document.documentElement.dataset.theme = "dark";
        return previous;
      });
      assert.ok(await tabs.locator(".segmented-tabs-indicator").evaluate((marker) =>
        parseFloat(getComputedStyle(marker).borderTopLeftRadius) >= marker.getBoundingClientRect().height / 2));
      await tabs.screenshot({ path: path.join(output, `${engine}-trusted-selected-tabs-dark.png`) });
      await page.evaluate((theme) => {
        if (theme) document.documentElement.dataset.theme = theme;
        else delete document.documentElement.dataset.theme;
      }, theme);
      await setFixtureSidebarWidth(undefined);
      await page.evaluate(() => {
        window.spoolServers = window.fixtureOriginalServers;
        delete window.fixtureOriginalServers;
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      assert.ok(await tabs.locator(".segmented-tabs-indicator").evaluate((marker) =>
        parseFloat(getComputedStyle(marker).borderTopLeftRadius) < marker.getBoundingClientRect().height / 2));
      await publishTargets([]);
      await lab.waitFor({ state: "detached" });
      await savedHome.waitFor();
      const signedInDotColor = await tabs
        .locator('.server-status-dot[data-state="connected"]')
        .first()
        .evaluate((dot) => getComputedStyle(dot).backgroundColor);
      const signedInChannels = signedInDotColor.match(/\d+/g).map(Number);
      assert.ok(
        signedInChannels[1] > signedInChannels[0] &&
          signedInChannels[1] > signedInChannels[2],
        "a connected signed-in server has a green dot",
      );
      assert.ok(
        await tabs
          .locator(".desktop-server-track")
          .evaluate((el) => el.scrollWidth > el.clientWidth),
        "many servers scroll inside the sidebar",
      );
      const sidebar = await page.locator(".sidebar").boundingBox();
      const track = await tabs.boundingBox();
      assert.ok(
        track.x >= sidebar.x &&
          track.x + track.width <= sidebar.x + sidebar.width + 1,
      );
      assert.ok(
        track.y < (await page.locator(".sidebar-scroll").boundingBox()).y,
        "tabs sit above workspace navigation",
      );
      const scroll = tabs.locator(".segmented-tabs-viewport");
      const add = tabs.getByRole("link", { name: "Add server", exact: true });
      const initialScroll = await scroll.boundingBox();
      const addAtStart = await add.boundingBox();
      const lastTab = await tabs.getByRole("button").last().boundingBox();
      assert.ok(
        addAtStart.x >= lastTab.x + lastTab.width,
        "add follows the final server inside the scroll content",
      );
      assert.ok(
        addAtStart.x > initialScroll.x + initialScroll.width,
        "add scrolls out of view with the server list",
      );
      const selections = await page.evaluate(
        () => window.fixtureSelections.length,
      );
      for (let i = 0; i < 3; i++) {
        await page.mouse.move(
          initialScroll.x + initialScroll.width - 12,
          initialScroll.y + initialScroll.height / 2,
        );
        await page.mouse.down();
        await page.mouse.move(
          initialScroll.x + 12,
          initialScroll.y + initialScroll.height / 2,
          { steps: 10 },
        );
        await page.mouse.up();
      }
      assert.ok(
        await scroll.evaluate((element) => element.scrollLeft > 100),
        "holding the mouse scrolls the server tabs",
      );
      assert.equal(
        await page.evaluate(() => window.fixtureSelections.length),
        selections,
        "dragging does not activate tabs or the add action",
      );
      const addAtEnd = await add.boundingBox();
      await verifyTabPositions(tabs);
      assert.ok(
        addAtEnd.x + addAtEnd.width <=
          initialScroll.x + initialScroll.width + 1,
        "dragging to the end reveals add",
      );
      await add.hover();
      const outline = await add.evaluate((element) => {
        const style = getComputedStyle(element),
          box = element.getBoundingClientRect();
        return {
          width: box.width,
          height: box.height,
          radius: style.borderRadius,
          border: style.borderTopColor,
        };
      });
      assert.equal(outline.width, outline.height);
      assert.equal(outline.radius, "50%");
      assert.notEqual(outline.border, "rgba(0, 0, 0, 0)");
      await tabs.screenshot({
        path: path.join(output, `${engine}-tabs-end.png`),
      });
      const outgoingScroll = await scroll.evaluate(
        (element) => element.scrollLeft,
      );
      await page.evaluate(() => {
        window.fixtureRestoreScroll = 0;
      });
      const handoff = await verifyTabSlide(
        page,
        ".desktop-server-tabs",
        tabs.getByRole("button").last(),
      );
      assert.ok(
        handoff.scrollLeft >= outgoingScroll - 8,
        "a cached page continues from the outgoing scroll position instead of its stale position",
      );
      await verifyTabPositions(tabs);
      await tabs.getByRole("button").last().focus();
      await page.keyboard.press("Home");
      await page.waitForFunction(
        () => document.querySelector(".desktop-server-track").scrollLeft < 1,
      );
      await page.evaluate(() => {
        window.fixtureReadyAttempts = [];
        window.fixtureDropReady = 1;
      });
      await verifyTabSlide(
        page,
        ".desktop-server-tabs",
        tabs.getByRole("button").nth(1),
      );
      await page.waitForFunction(() => window.fixtureReadyAttempts.length >= 2);
      assert.deepEqual(
        await page.evaluate(() => window.fixtureReadyAttempts),
        [
          { server: "home-1234", acknowledged: false },
          { server: "home-1234", acknowledged: true },
        ],
        "a lost paint notification retries until native acknowledges it",
      );
      await verifyTabSlide(
        page,
        ".desktop-server-tabs",
        tabs.getByRole("button").last(),
      );
      await page.waitForFunction(() => {
        const track = document.querySelector(".desktop-server-track");
        const add = track
          .querySelector(".desktop-server-add")
          .getBoundingClientRect();
        const bounds = track.getBoundingClientRect();
        return add.x >= bounds.x && add.right <= bounds.right + 1;
      });
      await verifyTabPositions(tabs);
      await page.emulateMedia({ reducedMotion: "reduce" });
      await verifyTabSlide(
        page,
        ".desktop-server-tabs",
        tabs.getByRole("button").first(),
        false,
      );
      await page.emulateMedia({ reducedMotion: "no-preference" });
      await tabs.getByRole("button").first().focus();
      await page.keyboard.press("End");
      await page.waitForFunction(
        () => window.fixtureSelections.at(-1) === "office-1234",
      );
      assert.equal(
        await tabs.getByRole("button").last().getAttribute("aria-current"),
        "page",
      );
      await page.waitForFunction(() => {
        const track = document.querySelector(".desktop-server-track");
        return (
          Math.abs(track.scrollWidth - track.clientWidth - track.scrollLeft) < 1
        );
      });
      assert.ok(await add.isVisible());
      await verifyTabPositions(tabs);
      await page.keyboard.press("Home");
      await page.waitForFunction(
        () => window.fixtureSelections.at(-1) === "local",
      );
      await page.screenshot({
        path: path.join(output, `${engine}-sidebar.png`),
      });

      await page.evaluate(() => {
        window.fixtureShell = [
          document.querySelector(".app"),
          document.querySelector(".sidebar"),
          document.querySelector(".workspace > header"),
        ];
      });
      await page
        .getByRole("separator", { name: "Resize left sidebar", exact: true })
        .press("ArrowRight");
      await page.waitForFunction(
        () =>
          Math.round(
            document.querySelector(".sidebar").getBoundingClientRect().width,
          ) === 308,
      );
      for (const id of ["custom-1234", "local"]) {
        await page.evaluate((id) => {
          window.spoolServers = { ...window.spoolServers, selected: id };
          window.dispatchEvent(new Event("spool:servers-changed"));
          window.dispatchEvent(new Event("spool:server-selected"));
        }, id);
        assert.equal(
          Math.round((await page.locator(".sidebar").boundingBox()).width),
          308,
        );
      }

      // 不触发 focus 或标签选择，让周期性 HTTP 回退独自刷新权限。
      await page.evaluate(() => {
        window.fixtureSocketReady = false;
        window.fixtureSockets
          .findLast((socket) => typeof socket.onerror === "function")
          .onerror();
      });
      const accountFooter = page.locator(".sidebar .side-foot");
      const retryIcon = accountFooter.getByRole("button", {
        name: "Retry",
        exact: true,
      });
      await retryIcon.waitFor();
      const pollsBeforeChange = targetPolls;
      httpTargets = [{ id: "b".repeat(52), name: "Shared Lab" }];
      await lab.waitFor();
      assert.ok(targetPolls > pollsBeforeChange);
      httpTargets = [];
      await lab.waitFor({ state: "detached" });
      assert.ok(
        await page.evaluate(() =>
          window.fixtureShell.every(
            (node, i) =>
              node ===
              [
                document.querySelector(".app"),
                document.querySelector(".sidebar"),
                document.querySelector(".workspace > header"),
              ][i],
          ),
        ),
        "connection errors preserve the normal window shell",
      );
      const retryBox = await retryIcon.boundingBox();
      const accountSettings = await accountFooter
        .getByRole("link", { name: "Settings", exact: true })
        .boundingBox();
      assert.ok(retryBox.x + retryBox.width <= accountSettings.x);
      assert.equal(await retryIcon.locator("svg").count(), 1);
      assert.equal(
        await accountFooter
          .getByRole("button", { name: "Sign out", exact: true })
          .isDisabled(),
        true,
      );
      await retryIcon.click();
      assert.equal(await page.evaluate(() => window.fixtureConnections), 1);
      await page.evaluate(() => {
        window.fixtureSocketReady = true;
        // A reconnect may already have opened while the fixture withheld ready.
        const socket = window.fixtureSockets.at(-1);
        if (socket?.readyState === 1) socket.onmessage?.({ data: JSON.stringify({ type: "ready" }) });
      });
      await retryIcon.waitFor({ state: "detached" });

      signedIn = false;
      await page.evaluate(() => {
        window.fixtureSockets
          .findLast((socket) => typeof socket.onerror === "function")
          .onerror();
      });
      const loginPlug = page
        .locator(".side-foot")
        .getByRole("button", { name: "Sign in", exact: true });
      await loginPlug.waitFor();
      assert.ok(
        await page.evaluate(() =>
          window.fixtureShell.every(
            (node, i) =>
              node ===
              [
                document.querySelector(".app"),
                document.querySelector(".sidebar"),
                document.querySelector(".workspace > header"),
              ][i],
          ),
        ),
        "signed-out servers reuse the same sidebar and header",
      );
      assert.equal(
        await loginPlug.locator("svg.lucide-plug").count(),
        1,
        "an existing account uses Android's plug for sign-in",
      );
      const plugBox = await loginPlug.boundingBox();
      const plugSettings = await page
        .locator(".side-foot")
        .getByRole("link", { name: "Settings", exact: true })
        .boundingBox();
      assert.ok(plugBox.x + plugBox.width <= plugSettings.x);
      await loginPlug.click();
      await page.locator('input[name="password"]').waitFor();

      signedIn = false;
      await page.addInitScript(() => {
        window.fixtureLoseLoginReady = true;
        if (sessionStorage.getItem("pause-health-paint") === "true") {
          sessionStorage.removeItem("pause-health-paint");
          window.fixtureLoseHealthState = true;
          window.requestAnimationFrame = () => 0;
        }
      });
      await page.evaluate(() => sessionStorage.setItem("pause-health-paint", "true"));
      await page.reload();
      await page.waitForFunction(
        () => window.spoolServers.entries.find((server) => server.id === "local").status === "login",
        null,
        { polling: 100 },
      );
      assert.equal(await page.evaluate(() => window.fixtureReadyAttempts.length), 0);
      assert.deepEqual(
        await page.evaluate(() => window.fixtureHealthAttempts.filter((attempt) => attempt.state === "login")),
        [{ state: "login", acknowledged: false }, { state: "login", acknowledged: true }],
        "signed-out health retries and reaches native while animation frames are suspended",
      );
      await page.reload();
      await page.waitForFunction(
        () => window.fixtureDroppedStates.includes("login") &&
          window.fixtureReadyAttempts.some((attempt) => attempt.acknowledged),
      );
      await page
        .locator(".desktop-server-start .side-foot")
        .getByRole("button", { name: "Sign in", exact: true })
        .waitFor();
      await verifySignedOutAction(page, "Sign in");
      await page.waitForFunction(
        () =>
          window.spoolServers.entries.find((server) => server.id === "local")
            .status === "login",
      );
      assert.equal(
        await tabs
          .getByRole("button", {
            name: "本机开发, Connected · Sign-in required",
            exact: true,
          })
          .count(),
        1,
      );
      assert.deepEqual(
        await page.evaluate(() => window.fixtureDroppedStates),
        ["login"],
        "the signed-out status recovers even when its first notification is lost",
      );
      const reachableDotColor = await tabs
        .locator('.server-status-dot[data-state="login"]')
        .first()
        .evaluate((dot) => getComputedStyle(dot).backgroundColor);
      const channels = reachableDotColor.match(/\d+/g).map(Number);
      assert.ok(
        channels[2] > channels[0] && channels[2] > channels[1],
        "a reachable signed-out server has a blue dot",
      );
      assert.notEqual(reachableDotColor, signedInDotColor);
      // The native checker can detect an outage while a cached login page has
      // no socket. Its result updates the sidebar without being overwritten by
      // the page's last successful session check.
      for (const [status, action] of [["error", "Retry"], ["login", "Sign in"]]) {
        await page.evaluate((status) => {
          window.spoolServers = {
            ...window.spoolServers,
            entries: window.spoolServers.entries.map((server) =>
              server.id === "local" ? { ...server, status } : server,
            ),
          };
          window.dispatchEvent(new Event("spool:servers-changed"));
        }, status);
        await verifySignedOutAction(page, action);
        await tabs.getByRole("button", {
          name: `本机开发, ${status === "error" ? "Connection failed" : "Connected · Sign-in required"}`,
          exact: true,
        }).waitFor();
      }
      for (const state of ["error", "disconnected"])
        assert.notEqual(
          await tabs
            .locator(`.server-status-dot[data-state="${state}"]`)
            .first()
            .evaluate((dot) => getComputedStyle(dot).backgroundColor),
          reachableDotColor,
          "unreachable servers remain visually distinct",
        );
      assert.equal(
        await page.locator(".desktop-server-tabs").count(),
        1,
        "server switching remains in the sidebar before sign-in",
      );
      assert.equal(await page.locator(".desktop-login").count(), 0);
      await tabs
        .getByRole("button", {
          name: "自定义显示名称, Connection failed",
          exact: true,
        })
        .click();
      await page
        .getByRole("heading", { name: "自定义显示名称", exact: true })
        .waitFor();
      assert.equal(
        await page.locator('input[name="password"]').count(),
        0,
        "selecting an unsuccessful server does not open the sign-in page",
      );
      await verifySignedOutAction(page, "Retry");
      await page.evaluate(() => {
        window.spoolServers = {
          ...window.spoolServers,
          entries: window.spoolServers.entries.map((server) =>
            server.id === "custom-1234" ? { ...server, status: "login" } : server,
          ),
        };
        window.dispatchEvent(new Event("spool:servers-changed"));
      });
      await page
        .locator(".side-foot")
        .getByRole("button", { name: "Sign in", exact: true })
        .click();
      await page.locator('input[name="password"]').waitFor();
      assert.equal(await page.locator(".desktop-server-tabs").count(), 0);
      const loginHeader = page.locator(".desktop-login-header");
      const serverLabel = await loginHeader
        .getByText("自定义显示名称", { exact: true })
        .boundingBox();
      const loginSettings = await loginHeader
        .getByRole("link", { name: "Settings", exact: true })
        .boundingBox();
      const loginPanel = await page.locator(".login-panel").boundingBox();
      assert.ok(
        serverLabel.x >= loginPanel.x &&
          serverLabel.x + serverLabel.width <= loginSettings.x,
      );
      assert.ok(
        Math.abs(
          serverLabel.y +
            serverLabel.height / 2 -
            loginSettings.y -
            loginSettings.height / 2,
        ) < 2,
      );
      await page.screenshot({
        path: path.join(output, `${engine}-login-light.png`),
      });
      await page.emulateMedia({ colorScheme: "dark" });
      await page.screenshot({
        path: path.join(output, `${engine}-login-dark.png`),
      });
      await page.emulateMedia({ colorScheme: "light" });
      await page.locator('input[name="username"]').fill("server-login-draft");
      await loginHeader
        .getByRole("button", { name: "Back to list", exact: true })
        .click();
      await page
        .locator(".side-foot")
        .getByRole("button", { name: "Sign in", exact: true })
        .click();
      assert.equal(
        await page.locator('input[name="username"]').inputValue(),
        "server-login-draft",
      );
      await loginHeader
        .getByRole("button", { name: "Back to list", exact: true })
        .click();
      assert.equal(
        await page
          .getByRole("link", { name: "Add server", exact: true })
          .getAttribute("href"),
        "/__desktop/servers",
      );

      reachable = false;
      await page.reload();
      await page
        .locator(".side-foot")
        .getByRole("button", { name: "Retry", exact: true })
        .waitFor();
      await verifySignedOutAction(page, "Retry");
      const drag = await page.locator(".workspace > header").boundingBox();
      assert.ok(
        drag.height >= 54,
        "the disconnected server uses the normal window header",
      );
      assert.ok(
        await page.evaluate(() =>
          [200, 640, 1100].every((x) => {
            const element = document.elementFromPoint(x, 35);
            return (
              element &&
              getComputedStyle(element)
                .getPropertyValue("--app-region")
                .trim() === "drag"
            );
          }),
        ),
        "sidebar brand and main header provide a continuous drag area",
      );
      await page.screenshot({
        path: path.join(output, `${engine}-disconnected.png`),
      });
      await tabs
        .getByRole("button", {
          name: "自定义显示名称, Connection failed",
          exact: true,
        })
        .click();
      await page
        .getByRole("heading", { name: "自定义显示名称", exact: true })
        .waitFor();
      await page
        .locator(".side-foot")
        .getByRole("button", { name: "Retry", exact: true })
        .click();
      assert.equal(await page.evaluate(() => window.fixtureConnections), 1);
      await tabs
        .getByRole("button", {
          name: "Office workspace with a long name, Disconnected",
          exact: true,
        })
        .click();
      await verifySignedOutAction(page, "Connect");
      await page
        .locator(".side-foot")
        .getByRole("button", { name: "Connect", exact: true })
        .click();
      assert.equal(await page.evaluate(() => window.fixtureConnections), 2);
      assert.equal(await page.locator(".desktop-login").count(), 0);
      reachable = true;

      for (const status of [403, 404, 503]) {
        sessionStatus = status;
        await page.reload();
        await verifySignedOutAction(page, "Retry");
      }
      sessionStatus = 0;
      invalidSession = true;
      await page.reload();
      await verifySignedOutAction(page, "Retry");
      invalidSession = false;
      for (const invalid of [false, true]) {
        versionStatus = invalid ? 200 : 503;
        invalidVersion = invalid;
        await page.reload();
        await verifySignedOutAction(page, "Retry");
      }
      versionStatus = 200;
      invalidVersion = false;
      await page.reload();
      await verifySignedOutAction(page, "Sign in");

      await page.addInitScript(() => {
        const calls = [];
        const data = {
          server: {
            id: "",
            name: "",
            server_name: "",
            address: "",
            enabled: true,
            automatic: true,
            direct: "",
            tailscale: "",
            relay: "",
            mode: "direct",
          },
          status: "disconnected",
          selected: false,
          active: false,
        };
        window.fixtureCalls = calls;
        window.fixtureDialogData = data;
        if (sessionStorage.getItem("fixtureDialogBootstrap"))
          window.spoolServerDialog = structuredClone(data);
        window.mygo.call = async (method, ...args) => {
          calls.push([method, ...structuredClone(args)]);
          if (method === "DesktopServers.Save") {
            const previous = data.server;
            data.server = {
              ...args[0],
              id: "saved-1234",
              server_name: "服务器原名",
            };
            if (args[1]) {
              data.status = "connecting";
              data.active = true;
            } else if (
              !data.server.enabled ||
              previous.mode !== data.server.mode
            ) {
              data.status = "disconnected";
              data.active = false;
            }
            const saved = structuredClone(data);
            if (window.fixtureHoldSave)
              await new Promise((resolve) => {
                window.fixtureFinishSave = resolve;
              });
            return saved;
          } else if (method === "DesktopServers.Disconnect") {
            data.server.enabled = false;
            data.status = "disconnected";
            data.active = false;
          } else if (method === "DesktopServers.PairRelay") {
            return new Promise((resolve) => {
              window.fixtureFinishPairing = resolve;
            });
          } else if (method === "DesktopServers.TailscaleLogin") return true;
          else if (method === "DesktopServers.Delete") return true;
          else if (method === "DesktopServers.Ready") {
            window.fixtureFirstDialogView ??= {
              form: !!document.querySelector("form input"),
              theme: document.documentElement.dataset.theme,
              error: document.querySelector('[role="alert"]')?.textContent,
            };
            return true;
          } else if (
            method !== "DesktopServers.Snapshot" &&
            method !== "DesktopServers.Close"
          )
            throw new Error("Unexpected native call: " + method);
          if (method === "DesktopServers.Snapshot") {
            // The modal must remain hidden while configuration loads.
            await new Promise((resolve) => setTimeout(resolve, 150));
            if (sessionStorage.getItem("fixtureDialogInitializationFails"))
              throw new Error("Server settings could not be loaded.");
          }
          return structuredClone(data);
        };
      });
      await page.goto(origin + "/servers.html");
      await page.setViewportSize({ width: 560, height: 660 });
      const name = page.getByRole("textbox", {
        name: "Display name (optional)",
        exact: true,
      });
      await name.waitFor();
      await page.waitForFunction(() => window.fixtureFirstDialogView);
      assert.deepEqual(
        await page.evaluate(() => window.fixtureFirstDialogView),
        { form: true, theme: "light", error: undefined },
        "native reveal waits for the usable form and its theme",
      );
      await verifyTabSlide(
        page,
        ".server-connection-tabs",
        page.getByRole("tab", { name: "Tailscale", exact: true }),
      );
      await page.getByRole("tab", { name: "Tailscale", exact: true }).focus();
      await page.keyboard.press("End");
      assert.equal(
        await page
          .getByRole("tab", { name: "Spool Relay", exact: true })
          .getAttribute("aria-selected"),
        "true",
      );
      await page.keyboard.press("Home");
      assert.equal(
        await page
          .getByRole("tab", { name: "Public HTTPS", exact: true })
          .getAttribute("aria-selected"),
        "true",
      );
      assert.equal(await name.getAttribute("required"), null);
      assert.equal(
        await page
          .getByRole("button", { name: "Connect", exact: true })
          .isDisabled(),
        true,
      );
      for (const width of [460, 560]) {
        await page.setViewportSize({ width, height: 660 });
        for (const control of [
          name,
          page.getByRole("switch", {
            name: "Connect to this server",
            exact: true,
          }),
          page.getByRole("switch", {
            name: "Allow automatic connection switching",
            exact: true,
          }),
        ]) {
          const label = await control
            .locator(
              "xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' settings-row ')][1]",
            )
            .locator(".settings-row-heading")
            .boundingBox();
          const box = await control.boundingBox();
          assert.ok(
            box.x >= label.x + label.width,
            "each popup control is to the right of its label",
          );
          assert.ok(
            box.y + box.height / 2 >= label.y &&
              box.y + box.height / 2 <= label.y + label.height,
            "each popup control shares the label's row",
          );
          const row = await control
            .locator(
              "xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' settings-row ')][1]",
            )
            .boundingBox();
          assert.ok(
            row.height <= 56,
            "settings rows keep compact vertical spacing",
          );
        }
        const nameBox = await name.boundingBox();
        const help = page.getByText(
          "Leave blank to use the name from the server's settings.",
          { exact: true },
        );
        const helpBox = await help.boundingBox();
        assert.ok(
          helpBox.y >= nameBox.y + nameBox.height &&
            helpBox.x < nameBox.x &&
            helpBox.width > nameBox.width,
          "the name hint occupies its own line below the label and input",
        );
        assert.equal(
          await name.getAttribute("aria-describedby"),
          await help.getAttribute("id"),
          "the optional name is described by its hint",
        );
      }
      await page
        .getByRole("textbox", { name: "Public HTTPS address", exact: true })
        .fill("https://public.example.com");
      await page.getByRole("tab", { name: "Tailscale", exact: true }).click();
      await page
        .getByRole("textbox", { name: "Tailscale HTTPS address", exact: true })
        .fill("https://home.example.ts.net");
      await page.getByRole("button", { name: "Connect", exact: true }).click();
      await page.waitForFunction(() =>
        window.fixtureCalls.some((call) => call[0] === "DesktopServers.Save"),
      );
      let saved = await page.evaluate(() =>
        window.fixtureCalls
          .filter((call) => call[0] === "DesktopServers.Save")
          .at(-1),
      );
      assert.equal(saved[1].mode, "tailscale");
      assert.equal(saved[1].name, "");
      assert.equal(saved[1].direct, "https://public.example.com");
      assert.equal(saved[1].tailscale, "https://home.example.ts.net");
      assert.equal(saved[2], true);
      await page
        .getByRole("button", { name: "Disconnect", exact: true })
        .waitFor();
      assert.equal(
        await page
          .getByRole("button", { name: "Disconnect", exact: true })
          .isEnabled(),
        true,
        "a connecting route can be stopped",
      );
      await page
        .getByRole("heading", { name: "服务器原名 · Settings", exact: true })
        .waitFor();
      await name.fill("我的自定义名称");
      await page.getByRole("tab", { name: "Spool Relay", exact: true }).click();
      await page
        .getByRole("textbox", { name: "Pairing code", exact: true })
        .fill("spool-pair:fixture");
      await page
        .getByRole("tab", { name: "Public HTTPS", exact: true })
        .click();
      assert.equal(
        await page
          .getByRole("textbox", { name: "Public HTTPS address", exact: true })
          .inputValue(),
        "https://public.example.com",
      );
      await page
        .getByRole("button", { name: "Save settings", exact: true })
        .click();
      await page.waitForFunction(
        () =>
          window.fixtureCalls.filter(
            (call) => call[0] === "DesktopServers.Save",
          ).length === 2,
      );
      saved = await page.evaluate(() =>
        window.fixtureCalls
          .filter((call) => call[0] === "DesktopServers.Save")
          .at(-1),
      );
      assert.equal(
        saved[1].mode,
        "tailscale",
        "browsing a mode does not switch the active route",
      );
      assert.equal(saved[1].relay, "spool-pair:fixture");
      assert.equal(saved[1].name, "我的自定义名称");

      // All modes use a centered primary action and right-aligned tools.
      for (const width of [460, 560]) {
        await page.setViewportSize({ width, height: 660 });
        for (const mode of ["Public HTTPS", "Tailscale", "Spool Relay"]) {
          await page.getByRole("tab", { name: mode, exact: true }).click();
          const panel = page.getByRole("tabpanel");
          const primary = panel.locator(".server-connection-use");
          const box = await primary.boundingBox();
          const bounds = await panel.boundingBox();
          assert.ok(
            Math.abs(box.x + box.width / 2 - bounds.x - bounds.width / 2) < 1,
            "the connection action is centered",
          );
          assert.ok(box.width < 160, "the connection action stays compact");
          for (const tool of await panel
            .locator(".server-connection-tools button")
            .all()) {
            const toolBox = await tool.boundingBox();
            assert.ok(
              toolBox.x > box.x + box.width,
              "route tools sit to the right of the primary action",
            );
            assert.ok(
              Math.abs(
                toolBox.y + toolBox.height / 2 - box.y - box.height / 2,
              ) < 1,
              "route tools share the primary action's row",
            );
          }
          assert.equal(
            await panel.locator("p").count(),
            0,
            "details do not add permanent rows below the input",
          );
        }
      }
      await page.getByRole("tab", { name: "Tailscale", exact: true }).click();
      const information = page.getByRole("button", {
        name: "Information: Tailscale",
        exact: true,
      });
      await information.click();
      await page
        .getByRole("dialog", { name: "Information: Tailscale", exact: true })
        .waitFor();
      assert.equal(
        await page.getByRole("dialog").getByRole("status").textContent(),
        "Connecting…",
      );
      await page.keyboard.press("Escape");
      assert.equal(await page.getByRole("dialog").count(), 0);
      assert.equal(
        await page.evaluate(() =>
          window.fixtureCalls.some(
            ([method]) => method === "DesktopServers.Close",
          ),
        ),
        false,
        "Escape dismisses information before the editor",
      );
      await page
        .getByRole("textbox", { name: "Tailscale HTTPS address", exact: true })
        .fill("");
      assert.equal(
        await page
          .getByRole("button", { name: "Sign in to Tailscale", exact: true })
          .isDisabled(),
        true,
      );
      await page
        .getByRole("button", { name: "Disconnect", exact: true })
        .click();
      await page
        .getByRole("button", { name: "Connect", exact: true })
        .waitFor();
      assert.equal(
        await page
          .getByRole("button", { name: "Connect", exact: true })
          .isDisabled(),
        true,
      );
      assert.equal(
        await page
          .getByRole("textbox", {
            name: "Tailscale HTTPS address",
            exact: true,
          })
          .inputValue(),
        "",
        "disconnect preserves unsaved edits",
      );
      assert.equal(
        await page.evaluate(() => window.fixtureDialogData.server.tailscale),
        "https://home.example.ts.net",
        "disconnect uses persisted connection settings even when draft fields are blank",
      );
      assert.equal(
        await page.evaluate(() => window.fixtureDialogData.server.enabled),
        false,
      );

      for (const [status, active, action] of [
        ["connected", true, "Disconnect"],
        ["login", true, "Disconnect"],
        ["error", false, "Connect"],
        ["disconnected", false, "Connect"],
      ]) {
        await page.evaluate(
          ({ status, active }) => {
            Object.assign(window.fixtureDialogData, { status, active });
            window.fixtureDialogData.server.enabled = true;
            window.dispatchEvent(
              new CustomEvent("spool:server-dialog", {
                detail: structuredClone(window.fixtureDialogData),
              }),
            );
          },
          { status, active },
        );
        await page.getByRole("button", { name: action, exact: true }).waitFor();
        if (status === "connected" || status === "login")
          assert.equal(
            await page
              .locator(
                `.server-connection-tabs .server-status-dot[data-state="${status}"]`,
              )
              .evaluate((dot) => getComputedStyle(dot).backgroundColor),
            status === "connected" ? signedInDotColor : reachableDotColor,
            "connection settings distinguish signed-in and signed-out servers just like the sidebar",
          );
        await page
          .getByRole("tab", { name: "Public HTTPS", exact: true })
          .click();
        await page
          .getByRole("button", { name: "Connect", exact: true })
          .waitFor();
        assert.equal(
          await page.evaluate(() => window.fixtureDialogData.server.mode),
          "tailscale",
          "browsing another mode preserves the selected route",
        );
      }

      await page.getByRole("tab", { name: "Spool Relay", exact: true }).click();
      const qrInput = page.locator('input[type="file"]');
      await qrInput.setInputFiles({
        name: "pairing.png",
        mimeType: "image/png",
        buffer: pairingPNG,
      });
      await page.waitForFunction(
        (code) =>
          document.querySelector("#connection-panel-relay input").value ===
          code,
        pairingCode,
      );
      const invalidImage = await page.evaluate(() => {
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 64;
        return canvas.toDataURL().split(",")[1];
      });
      await qrInput.setInputFiles({
        name: "blank.png",
        mimeType: "image/png",
        buffer: Buffer.from(invalidImage, "base64"),
      });
      await page
        .getByRole("alert")
        .getByText("No Spool pairing QR code was found in this image.", {
          exact: true,
        })
        .waitFor();
      assert.equal(
        await page
          .getByRole("textbox", { name: "Pairing code", exact: true })
          .inputValue(),
        pairingCode,
        "an invalid QR image preserves the current pairing code",
      );
      await page
        .getByRole("alert")
        .getByRole("button", { name: "Dismiss", exact: true })
        .click();
      await page.getByRole("button", { name: "Connect", exact: true }).click();
      await page.waitForFunction(
        () => typeof window.fixtureFinishPairing === "function",
      );
      assert.equal(
        await page
          .getByRole("button", { name: "Disconnect", exact: true })
          .isEnabled(),
        true,
        "pairing can be cancelled while its approval is pending",
      );
      await page.getByRole("tab", { name: "Tailscale", exact: true }).click();
      assert.equal(
        await page
          .getByRole("button", { name: "Connect", exact: true })
          .isDisabled(),
        true,
        "another route cannot connect while pairing is pending",
      );
      await page.getByRole("tab", { name: "Spool Relay", exact: true }).click();
      await page
        .getByRole("button", { name: "Disconnect", exact: true })
        .click();
      await page
        .getByRole("button", { name: "Connect", exact: true })
        .waitFor();
      await page.evaluate(() =>
        window.fixtureFinishPairing({
          state: "approved",
          fingerprint: "fixture",
        }),
      );
      assert.equal(
        await page.locator(".server-pairing-status").count(),
        0,
        "a late pairing result cannot restore a cancelled operation",
      );
      await page.locator(".server-dialog-shell").evaluate((el) => {
        el.scrollTop = 0;
      });
      await page.screenshot({
        path: path.join(output, `${engine}-dialog-light.png`),
        animations: "disabled",
      });
      await page.emulateMedia({ colorScheme: "dark" });
      await page.waitForFunction(
        () => document.documentElement.dataset.theme === "dark",
      );
      await page.screenshot({
        path: path.join(output, `${engine}-dialog-dark.png`),
        animations: "disabled",
      });
      assert.ok(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
        "the editor fits its modal viewport",
      );
      await page
        .getByRole("button", { name: "Delete server", exact: true })
        .click();
      await page
        .getByRole("alert")
        .getByRole("button", { name: "Delete server", exact: true })
        .click();
      assert.ok(
        await page.evaluate(() =>
          window.fixtureCalls.some(
            (call) => call[0] === "DesktopServers.Delete",
          ),
        ),
      );
      await page.evaluate(() =>
        sessionStorage.setItem("fixtureDialogInitializationFails", "1"),
      );
      await page.reload();
      await page.getByRole("alert").waitFor();
      await page.waitForFunction(() => window.fixtureFirstDialogView);
      const failedView = await page.evaluate(
        () => window.fixtureFirstDialogView,
      );
      assert.equal(failedView.form, false);
      assert.ok(["light", "dark"].includes(failedView.theme));
      assert.match(failedView.error, /Server settings could not be loaded/);

      await page.evaluate(() => {
        sessionStorage.removeItem("fixtureDialogInitializationFails");
        sessionStorage.setItem("fixtureDialogBootstrap", "1");
      });
      await page.reload();
      await name.waitFor();
      await page.waitForFunction(() => window.fixtureFirstDialogView);
      assert.equal(
        await page.evaluate(() =>
          window.fixtureCalls.some(
            ([method]) => method === "DesktopServers.Snapshot",
          ),
        ),
        false,
        "the native preloaded snapshot avoids an initial configuration roundtrip",
      );
      await name.fill("Unsaved editor draft");
      await page.getByRole("tab", { name: "Spool Relay", exact: true }).click();
      const nativeCalls = await page.evaluate(() => window.fixtureCalls.length);
      await page.evaluate(() => {
        const next = structuredClone(window.fixtureDialogData);
        next.server = {
          ...next.server,
          id: "reopened-1234",
          name: "Reopened server",
          mode: "tailscale",
          tailscale: "https://reopened.example.ts.net",
        };
        window.fixtureDialogData.server = next.server;
        window.dispatchEvent(
          new CustomEvent("spool:server-dialog", { detail: next }),
        );
      });
      await page
        .getByRole("heading", {
          name: "Reopened server · Settings",
          exact: true,
        })
        .waitFor();
      assert.equal(await name.inputValue(), "Reopened server");
      assert.equal(
        await page
          .getByRole("tab", { name: "Tailscale", exact: true })
          .getAttribute("aria-selected"),
        "true",
      );
      await page.waitForFunction(
        (count) =>
          window.fixtureCalls
            .slice(count)
            .some(
              ([method, id]) =>
                method === "DesktopServers.Ready" && id === "reopened-1234",
            ),
        nativeCalls,
      );
      await page.evaluate(() => {
        window.fixtureHoldSave = true;
      });
      await page
        .getByRole("switch", {
          name: "Allow automatic connection switching",
          exact: true,
        })
        .click();
      await page.waitForFunction(
        () => typeof window.fixtureFinishSave === "function",
      );
      await page.evaluate(() => {
        const next = structuredClone(window.fixtureDialogData);
        next.server.id = "another-5678";
        next.server.name = "Another server";
        next.server.server_name = "New server metadata";
        window.fixtureDialogData.server = next.server;
        window.dispatchEvent(
          new CustomEvent("spool:server-dialog", { detail: next }),
        );
        window.fixtureFinishSave();
      });
      await page
        .getByRole("heading", {
          name: "Another server · Settings",
          exact: true,
        })
        .waitFor();
      assert.equal(
        await name.inputValue(),
        "Another server",
        "a late policy save cannot replace the newly opened server",
      );
      assert.equal(
        await name.getAttribute("placeholder"),
        "New server metadata",
        "the reopened editor keeps its own snapshot after an older save finishes",
      );
      assert.deepEqual(errors, []);
      reports.push({
        engine,
        sidebarNames: true,
        overflow: true,
        keyboard: true,
        disconnectedSidebar: true,
        explicitSignIn: true,
        loginServerName: true,
        optionalName: true,
        independentModes: true,
        modalManagement: true,
        nameHintBelow: true,
        readyBeforeReveal: true,
        cachedEditor: true,
        footerActions: true,
        centeredActions: true,
        fullTitleDragArea: true,
        consistentFailures: true,
        sharedShell: true,
        sharedSidebarWidth: true,
        trailingAdd: true,
        dragScrolling: true,
        animatedTabs: true,
        acknowledgedPaint: true,
        signedOutStatus: true,
        healthWithoutPaint: true,
        revealTrailingAdd: true,
        alignedScrolledTabs: true,
        sharedScrollHandoff: true,
        reducedMotion: true,
        compactSettings: true,
        connectionActions: true,
        connectionInformation: true,
        localQRDecoding: true,
        cancellablePairing: true,
        reachableSignedOutDot: true,
        responsiveStartup: true,
        responsivePanelPreferences: true,
      });
    } catch (error) {
      if (errors.length) console.error(`${engine} page errors:`, errors);
      console.error(`${engine} desktop state:`, await page.evaluate(() => ({
        selected: window.spoolServers?.selected,
        entries: window.spoolServers?.entries,
        health: window.fixtureHealthAttempts,
        paint: window.fixtureReadyAttempts,
        dropped: window.fixtureDroppedStates,
        suspended: window.requestAnimationFrame.toString(),
      })));
      await page.screenshot({
        path: path.join(output, `${engine}-failure.png`),
      });
      throw error;
    } finally {
      await context.close();
      await browser.close();
      browser = undefined;
    }
  }
  console.log(JSON.stringify(reports, null, 2));
} finally {
  await writeFile(
    path.join(output, "browser-report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
  await browser?.close();
  for (const close of cleanup.reverse()) await close();
}
