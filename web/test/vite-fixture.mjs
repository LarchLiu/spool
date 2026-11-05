import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

// A test Vite server with different options invalidates the development
// server's optimized modules if both use node_modules/.vite. Each fixture
// owns its cache and closes Vite before removing it.
export async function createTestViteServer(t, options) {
  const base = fileURLToPath(new URL("../../build/tmp/", import.meta.url));
  await mkdir(base, { recursive: true });
  const scratch = await mkdtemp(join(base, "vite-test."));
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
