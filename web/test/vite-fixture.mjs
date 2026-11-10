import { rm } from "node:fs/promises";
import { join } from "node:path";
import { createServer } from "vite";
import { createScratch } from "../../tools/test-scratch.mjs";

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
