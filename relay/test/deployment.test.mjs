import { test, expect } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { delimiter, join, resolve } from "node:path";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

requireRunTemp();

test("Compose profiles select optional Caddy and keep DNS secrets out of Relay", () => {
  for (const enabled of [false, true]) {
    const result = Bun.spawnSync(["docker", "compose", "-f", resolve("relay/compose.yaml"), "config", "--format", "json"], {
      env: { ...process.env, COMPOSE_PROFILES: enabled ? "caddy" : "", SPOOL_RELAY_ADMIN_TOKEN: "synthetic-administrator-token-000000", CLOUDFLARE_API_TOKEN: "synthetic-dns-token", RELAY_TRAEFIK_RESOLVER: "spooldns", CADDY_CERTIFICATE_MODE: "wildcard", RELAY_BIND_ADDRESS: "0.0.0.0", RELAY_TRUSTED_PROXIES: "0.0.0.0/0", RELAY_WEB_DOMAIN: "custom.example.com" },
      stdout: "pipe", stderr: "pipe",
    });
    expect(result.exitCode).toBe(0);
    const config = JSON.parse(result.stdout.toString());
    expect(config.services.relay).toBeDefined();
    expect(!!config.services.caddy).toBe(enabled);
    expect(config.services.relay.environment.CLOUDFLARE_API_TOKEN).toBeUndefined();
    expect(config.services.relay.environment.RELAY_TRUSTED_PROXIES).toBeUndefined();
    expect(config.services.relay.environment.RELAY_CERTIFICATE_MODE).toBeUndefined();
    expect(config.services.relay.environment.RELAY_WEB_DOMAIN).toBeUndefined();
    expect(config.services.relay.environment.RELAY_TRAEFIK_RESOLVER).toBe("spooldns");
    expect(config.services.relay.ports.every(port => port.host_ip === "127.0.0.1")).toBe(true);
    if (enabled) {
      expect(config.services.caddy.environment.CADDY_CERTIFICATE_MODE).toBe("wildcard");
      expect(config.services.caddy.environment.CLOUDFLARE_API_TOKEN).toBe("synthetic-dns-token");
      expect(config.services.caddy.environment.RELAY_WEB_DOMAIN).toBeUndefined();
      expect(config.services.caddy.ports.map(port => port.target)).toEqual([80, 443]);
      expect(config.services.caddy.volumes.every(volume => volume.type === "volume")).toBe(true);
    }
  }
});

test("bundled Caddy selects its own certificate mode independently of Relay", () => {
  const scratch = mkdtempSync(join(process.env.TMPDIR, "caddy-start-"));
  try {
    writeFileSync(join(scratch, "caddy"), '#!/bin/sh\nprintf "%s\\n" "$@"\n', { mode: 0o700 });
    for (const mode of ["individual", "wildcard", "invalid"]) {
      for (const token of ["", "synthetic-dns-token"]) {
        const result = Bun.spawnSync(["sh", resolve("relay/deploy/caddy/start.sh")], {
          env: { ...process.env, PATH: scratch + delimiter + process.env.PATH, RELAY_PUBLIC_HOST: "relay.example.com", RELAY_ADMIN_HOST: "admin.example.com", CADDY_CERTIFICATE_MODE: mode, CLOUDFLARE_API_TOKEN: token },
          stdout: "pipe", stderr: "pipe",
        });
        const valid = mode === "individual" || mode === "wildcard" && !!token;
        expect(result.exitCode === 0).toBe(valid);
        if (valid) expect(result.stdout.toString().trim().split("\n")).toEqual(["run", "--config", "/etc/caddy/Caddyfile." + mode, "--adapter", "caddyfile"]);
      }
    }
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});
