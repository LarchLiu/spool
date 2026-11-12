// Run through tools/run.sh. Uses disposable container storage and synthetic
// credentials; neither deployment volumes nor the host's app data are touched.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

requireRunTemp();
const [id, image, version] = process.argv.slice(2);
assert.ok(
  ["relay", "caddy"].includes(id) && image && version,
  "Usage: image-smoke.mjs <relay|caddy> <image> <version>",
);
const docker = (...args) =>
  execFileSync("docker", args, {
    encoding: "utf8",
    timeout: 30_000,
    stdio: ["ignore", "pipe", "pipe"],
  });
const containers = new Set();
const directories = new Set();
const containerName = () => {
  const name = "spool-relay-image-" + randomUUID();
  containers.add(name);
  return name;
};
function cleanup() {
  for (const name of containers) {
    try {
      docker("rm", "-f", name);
    } catch (error) {
      if (!String(error.stderr).includes("No such container")) throw error;
    }
    containers.delete(name);
  }
  for (const directory of directories) {
    rmSync(directory, { recursive: true, force: true });
    directories.delete(directory);
  }
}
for (const [signal, code] of [
  ["SIGINT", 130],
  ["SIGTERM", 143],
])
  process.once(signal, () => {
    try {
      cleanup();
    } finally {
      process.exit(code);
    }
  });
const credential = "synthetic-image-smoke-administrator-000001";
try {
  if (id === "relay") {
    const name = containerName();
    const data = mkdtempSync(join(process.env.TMPDIR, "relay-image-data-"));
    directories.add(data);
    // The container runs as UID 10001, which need not exist on the host. The
    // per-run parent remains private; this owned bind mount can be removed by
    // the host after the container exits, regardless of its files' owner.
    chmodSync(data, 0o777);
    docker(
      "run",
      "-d",
      "--name",
      name,
      "--read-only",
      "--cap-drop=ALL",
      "--security-opt=no-new-privileges:true",
      "--mount",
      `type=bind,source=${data},target=/data`,
      "-e",
      `SPOOL_RELAY_ADMIN_TOKEN=${credential}`,
      image,
      "-listen=0.0.0.0:28080",
      "-admin-listen=0.0.0.0:28081",
      "-proxy-listen=0.0.0.0:28082",
      "-data-dir=/data",
    );
    const waitForHealth = async () => {
      for (let i = 0; i < 100; i++) {
        try {
          docker(
            "exec",
            name,
            "wget",
            "-q",
            "-O",
            "/dev/null",
            "http://127.0.0.1:28080/healthz",
          );
          return;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 100));
        }
      }
      throw new Error("Relay container did not become healthy");
    };
    await waitForHealth();
    assert.equal(docker("exec", name, "id", "-u").trim(), "10001");
    const html = docker(
      "exec",
      name,
      "wget",
      "-qO-",
      "http://127.0.0.1:28081/",
    );
    assert.ok(
      html.includes(`v${version}`) && !html.includes("{{version}}"),
      "Image must embed the release version",
    );
    const request = (...args) =>
      docker(
        "exec",
        name,
        "wget",
        "-qO-",
        "--header",
        `Authorization: Bearer ${credential}`,
        ...args,
      );
    const issued = JSON.parse(
      request(
        "--header",
        "Content-Type: application/json",
        "--post-data",
        "{}",
        "http://127.0.0.1:28081/v1/registrations",
      ),
    );
    assert.match(issued.registration.name, /^[A-Za-z]{12}$/);
    assert.ok(issued.token);
    docker("restart", name);
    await waitForHealth();
    const list = request("http://127.0.0.1:28081/v1/registrations");
    assert.equal(
      JSON.parse(list).registrations[0].id,
      issued.registration.id,
      "Registration must survive container restart",
    );
    assert.equal(list.includes(issued.token), false);
  } else {
    assert.match(
      docker(
        "run",
        "--name",
        containerName(),
        "--entrypoint",
        "caddy",
        image,
        "list-modules",
      ),
      /dns\.providers\.cloudflare/,
    );
    for (const mode of ["individual", "wildcard"]) {
      const config = docker(
        "run",
        "--name",
        containerName(),
        "-e",
        "RELAY_PUBLIC_HOST=relay.example.com",
        "-e",
        "RELAY_ADMIN_HOST=admin.example.com",
        "-e",
        "CLOUDFLARE_API_TOKEN=synthetic-dns-token",
        "--entrypoint",
        "caddy",
        image,
        "adapt",
        "--config",
        `/etc/caddy/Caddyfile.${mode}`,
        "--adapter",
        "caddyfile",
      );
      assert.ok(
        JSON.parse(config).apps.http,
        "Caddy configuration must be usable",
      );
    }
  }
  console.log(`Verified ${id} container`);
} finally {
  cleanup();
}
