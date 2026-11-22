import { rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { createScratch } from "../../tools/test-scratch.mjs";

// Browser fixtures have no native OnWillNavigate guard. A routed 204 still
// cancels pending fetches in WebKit, so replace only the native notification
// boundary. runtime.test.mjs and the real desktop smoke cover its delivery.
export function mockDesktopNavigation() {
  const runtime = fileURLToPath(new URL("../src/runtime.ts", import.meta.url));
  const id = "\0spool-test-desktop-navigation";
  return {
    name: "spool-test-desktop-navigation",
    enforce: "pre",
    async resolveId(source, importer) {
      if (!/^\.\/runtime(?:\.[jt]s)?$/.test(source)) return;
      const resolved = await this.resolve(source, importer, { skipSelf: true });
      if (resolved?.id === runtime) return id;
    },
    load(source) {
      if (source !== id) return;
      return `export * from ${JSON.stringify(runtime)};
export async function notifyDesktopSessionChanged() {}
export async function notifyDesktopViewChanged() {}
export function notifyDesktopServerState() {}
export function reportDesktopServerState() { return () => {}; }
export function notifyDesktopServerReady(state, selection) { window.dispatchEvent(new CustomEvent('spool:server-ready-ack', {detail: {state, selection}})); }
export function notifyDesktopSidebarWidth() {}
export function notifyDesktopServerName() {}`;
    },
  };
}

// A test Vite server with different options invalidates the development
// server's optimized modules if both use node_modules/.vite. Each fixture
// owns its cache and closes Vite before removing it. Honor the owning run's
// TMPDIR so its cleanup also covers interrupted fixture teardown.
export async function createTestViteServer(t, options) {
  const scratch = await createScratch("vite-test");
  let server;
  try {
    server = await createServer({
      ...options,
      cacheDir: join(scratch, "cache"),
    });
  } catch (error) {
    await rm(scratch, { recursive: true, force: true });
    throw error;
  }
  t.after(async () => {
    try {
      await server.close();
    } finally {
      await rm(scratch, { recursive: true, force: true });
    }
  });
  return server;
}
