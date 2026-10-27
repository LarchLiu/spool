import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign, verify } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test from "node:test";
import { signRelease } from "./sign-release.mjs";
import { installerName, releaseTargets } from "./release-assets.mjs";

const key = generateKeyPairSync("ed25519");
const pub = key.publicKey.export({ format: "der", type: "spki" }).subarray(-32);
const seed = key.privateKey.export({ format: "der", type: "pkcs8" }).subarray(-32);

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "spool-secure-release-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = join(root, "assets");
  await mkdir(directory);
  const notes = join(root, "notes.md");
  await writeFile(notes, "## Fixes\n\n- Secure updates\n");
  const env = {
    RELEASE_TAG: "v1.2.3", GITHUB_REPOSITORY: "owner/spool", RELEASE_NOTES_FILE: notes,
    MYGO_UPDATER_PUBLIC_KEY: pub.toString("base64"),
    MYGO_UPDATER_PRIVATE_KEY: Buffer.concat([seed, pub]).toString("base64"),
  };
  for (const target of releaseTargets) {
    await writeFile(join(directory, `spool-1.2.3-${target}.tar.gz`), `archive-${target}`);
    await writeFile(join(directory, installerName("1.2.3", target)), "installer");
  }
  return { root, directory, env };
}

test("only the final signing stage needs the key and signs both archive and metadata", async (t) => {
  const { directory, env } = await fixture(t);
  await assert.rejects(signRelease(directory, { ...env, MYGO_UPDATER_PRIVATE_KEY: "" }), /Configure/);
  assert.equal((await readdir(directory)).length, 12);
  await signRelease(directory, env);
  assert.equal((await readdir(directory)).length, 25);
  for (const target of releaseTargets) {
    const data = await readFile(join(directory, `update-${target}.json`));
    const signature = await readFile(join(directory, `update-${target}.json.sig`));
    assert.ok(verify(null, data, key.publicKey, signature));
    assert.equal(JSON.parse(data).target, target);
  }
  const installer = await readFile(join(directory, "install.sh"), "utf8");
  assert.ok(!installer.includes("@PUBLIC_KEY_DER@"));
  assert.ok(!installer.includes(env.MYGO_UPDATER_PRIVATE_KEY));
  assert.match(installer, /openssl pkeyutl -verify/);
});

test("signing rejects injected build output before producing signed metadata", async (t) => {
  const { directory, env } = await fixture(t);
  await writeFile(join(directory, "unexpected-secret"), "not a package");
  await assert.rejects(signRelease(directory, env), /asset set/);
  assert.ok(!(await readdir(directory)).some((name) => name.startsWith("update-")));
});

test("installer verifies both signatures before replacing an app and preserves user data", async (t) => {
  // macOS provides LibreSSL on some runners. Use Homebrew's OpenSSL 3 when
  // present; the actual Linux installer intentionally fails closed without it.
  const bin = execFileSync("sh", ["-c", 'command -v openssl'], { encoding: "utf8" }).trim();
  let openssl = bin;
  if (!execFileSync(openssl, ["version"], { encoding: "utf8" }).startsWith("OpenSSL 3.")) {
    for (const path of ["/opt/homebrew/opt/openssl@3/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"]) {
      const result = spawnSync(path, ["version"], { encoding: "utf8" });
      if (result.status === 0 && result.stdout.startsWith("OpenSSL 3.")) { openssl = path; break; }
    }
  }
  assert.match(execFileSync(openssl, ["version"], { encoding: "utf8" }), /^OpenSSL 3\./, "installer tests require OpenSSL 3");
  const { root, directory, env } = await fixture(t);
  const payload = join(root, "payload");
  await mkdir(payload);
  await writeFile(join(payload, "spool"), "#!/bin/sh\necho new-app\n", { mode: 0o755 });
  const archive = join(directory, "spool-1.2.3-linux-amd64.tar.gz");
  execFileSync("tar", ["-czf", archive, "-C", payload, "spool"]);
  await signRelease(directory, env);
  const home = join(root, "home");
  const app = join(home, ".local", "spool.app");
  await mkdir(app, { recursive: true });
  await mkdir(join(home, ".spool"));
  await writeFile(join(home, ".spool", "settings"), "keep-data");
  await writeFile(join(app, "spool"), "old-app");
  const mocks = join(root, "bin");
  await mkdir(mocks);
  for (const [name, script] of Object.entries({
    uname: '#!/bin/sh\nif [ "$1" = -s ]; then echo Linux; else echo x86_64; fi\n',
    id: "#!/bin/sh\necho 1000\n",
    openssl: `#!/bin/sh\nexec '${openssl}' "$@"\n`,
  })) {
    await writeFile(join(mocks, name), script, { mode: 0o755 });
  }
  const run = () => spawnSync("sh", [join(directory, "install.sh"), archive], {
    env: { ...process.env, HOME: home, XDG_DATA_HOME: join(home, ".local/share"), PATH: mocks + delimiter + process.env.PATH }, encoding: "utf8",
  });
  const manifestPath = join(directory, "update-linux-amd64.json");
  const signaturePath = `${manifestPath}.sig`;
  const original = await readFile(manifestPath);
  const originalSignature = await readFile(signaturePath);
  await rm(signaturePath);
  let result = run();
  assert.notEqual(result.status, 0);
  assert.equal(await readFile(join(app, "spool"), "utf8"), "old-app");
  await writeFile(signaturePath, originalSignature);
  await writeFile(manifestPath, original.toString().replaceAll("1.2.3", "99.0.0"));
  result = run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /invalid manifest signature/);
  assert.equal(await readFile(join(app, "spool"), "utf8"), "old-app");
  await writeFile(manifestPath, original);
  const content = await readFile(archive);
  const tampered = Buffer.from(content); tampered[0] ^= 1;
  await writeFile(archive, tampered);
  result = run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /invalid archive signature/);
  assert.equal(await readFile(join(app, "spool"), "utf8"), "old-app");
  // Even a genuinely signed archive must not escape the staging directory or
  // introduce links. Exercise extraction separately from signature rejection.
  for (const kind of ["traversal", "symlink"]) {
    execFileSync("python3", ["-c", `
import io, sys, tarfile
with tarfile.open(sys.argv[1], 'w:gz') as archive:
    member = tarfile.TarInfo('../escaped' if sys.argv[2] == 'traversal' else 'spool')
    if sys.argv[2] == 'symlink':
        member.type, member.linkname = tarfile.SYMTYPE, '/bin/sh'
        archive.addfile(member)
    else:
        member.size = 3
        archive.addfile(member, io.BytesIO(b'bad'))
`, archive, kind]);
    const unsafe = await readFile(archive);
    const manifest = { ...JSON.parse(original), size: unsafe.length,
      signature: sign(null, createHash("sha256").update(unsafe).digest(), key.privateKey).toString("base64") };
    const data = Buffer.from(JSON.stringify(manifest));
    await writeFile(manifestPath, data);
    await writeFile(signaturePath, sign(null, data, key.privateKey));
    result = run();
    assert.notEqual(result.status, 0, `${kind} was accepted`);
    assert.match(result.stderr, /unsafe entry in archive/);
    assert.equal(await readFile(join(app, "spool"), "utf8"), "old-app");
    await assert.rejects(readFile(join(home, ".local", "escaped")), { code: "ENOENT" });
  }
  await writeFile(manifestPath, original);
  await writeFile(signaturePath, originalSignature);
  await writeFile(archive, content);
  result = run();
  assert.equal(result.status, 0, result.stderr + result.stdout);
  assert.match(await readFile(join(app, "spool"), "utf8"), /new-app/);
  assert.equal(await readFile(join(home, ".spool", "settings"), "utf8"), "keep-data");
});

test("workflow pins actions, keeps private keys out of builds and uploads assets once", async () => {
  const workflow = await readFile(new URL("../.github/workflows/release.yml", import.meta.url), "utf8");
  for (const use of workflow.matchAll(/uses: (\S+)/g)) assert.match(use[1], /^actions\/[\w-]+@[a-f0-9]{40}$/);
  const [beforePublish, publish] = workflow.split("\n  publish:\n");
  assert.doesNotMatch(beforePublish, /secrets\.MYGO_UPDATER_PRIVATE_KEY/);
  assert.equal((publish.match(/secrets\.MYGO_UPDATER_PRIVATE_KEY/g) ?? []).length, 1);
  assert.match(beforePublish, /go test -race -v -timeout=10m/);
  assert.equal((publish.match(/gh release upload/g) ?? []).length, 1);
  assert.match(publish, /needs: \[prepare, build\]/);
  assert.doesNotMatch(publish, /sha256sum --check/);
});
