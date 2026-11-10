#!/usr/bin/env bun
// Build checksum-pinned Oniguruma/JNI sources for host and Android tests.
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  readdirSync,
  rmSync,
} from "node:fs";
import { join } from "node:path";
import { ROOT, SDK, cli, command } from "./common.mjs";
import { extractTarGz } from "../../tools/tar.mjs";
const CACHE = join(ROOT, "build/cache/android-syntax"),
  OUTPUT = join(ROOT, "build/android/syntax");
const HASH = "ad92309d0d13eebc27f6592e875f3efbfa3dda2bf6da5952e00f0a2120c921a8",
  NDK_VERSION = "29.0.14206865";
const { values: args } = cli({
  host: { type: "boolean" },
  android: { type: "boolean" },
  abis: { type: "string", multiple: true },
});
if (!args.host && !args.android)
  throw new Error("Specify --host and/or --android");
const abis = args.abis || ["arm64-v8a"];
if (abis.some((abi) => !["arm64-v8a", "x86_64"].includes(abi)))
  throw new Error("Invalid ABI");
mkdirSync(OUTPUT, { recursive: true });
// A kernel lock releases on signals/crashes; the owner marker never protects user data.
const { dlopen, FFIType } = await import("bun:ffi");
const libc = dlopen(
  process.platform === "darwin" ? "/usr/lib/libSystem.B.dylib" : "libc.so.6",
  { flock: { args: [FFIType.i32, FFIType.i32], returns: FFIType.i32 } },
);
const { openSync, closeSync } = await import("node:fs");
const fd = openSync(join(OUTPUT, ".build.lock"), "w");
if (libc.symbols.flock(fd, 2) !== 0)
  throw new Error("Could not lock syntax build");
try {
  mkdirSync(CACHE, { recursive: true });
  const archive = join(CACHE, "oniguruma-6.9.10.tar.gz");
  const hash = (path) =>
    createHash("sha256").update(readFileSync(path)).digest("hex");
  if (!existsSync(archive)) {
    const { requireRunTmp } = await import("./common.mjs");
    const temporary = join(requireRunTmp(), "oniguruma-6.9.10.tar.gz");
    command("curl", [
      "--fail",
      "--location",
      "--silent",
      "--show-error",
      "https://codeload.github.com/kkos/oniguruma/tar.gz/refs/tags/v6.9.10",
      "-o",
      temporary,
    ]);
    if (hash(temporary) !== HASH)
      throw new Error("Unexpected Oniguruma source archive");
    // Cross-filesystem moves from system temp must work too.
    writeFileSync(archive, readFileSync(temporary));
    rmSync(temporary);
  }
  if (hash(archive) !== HASH)
    throw new Error("Unexpected Oniguruma source archive");
  const marker = join(CACHE, "oniguruma-6.9.10/.spool-source-sha256");
  if (existsSync(marker) && readFileSync(marker, "utf8") !== HASH)
    throw new Error("Unexpected extracted Oniguruma source marker");
  if (!existsSync(marker)) {
    extractTarGz(readFileSync(archive), CACHE);
    writeFileSync(marker, HASH);
  }
  const ndk = process.env.ANDROID_NDK_HOME || join(SDK, "ndk", NDK_VERSION),
    java =
      process.env.JAVA_HOME ||
      "/Applications/Android Studio.app/Contents/jbr/Contents/Home";
  const needsHeaders = args.host && !existsSync(join(java, "include/jni.h"));
  if (
    (args.android || needsHeaders) &&
    !existsSync(join(ndk, "toolchains/llvm/prebuilt"))
  )
    throw new Error(
      `Missing NDK ${ndk}; install ${NDK_VERSION} or set ANDROID_NDK_HOME. Host checks can instead use a JDK with JNI headers.`,
    );
  function build(name, options) {
    const dir = join(OUTPUT, name + "-build");
    command(
      "cmake",
      [
        "-S",
        join(ROOT, "android/textmate/src/main/cpp"),
        "-B",
        dir,
        "-DCMAKE_BUILD_TYPE=Release",
        "-DONIG_SOURCE=" + join(CACHE, "oniguruma-6.9.10"),
        "-DCMAKE_LIBRARY_OUTPUT_DIRECTORY=" + join(OUTPUT, name),
        ...options,
      ],
      { stdio: "inherit" },
    );
    command(
      "cmake",
      ["--build", dir, "--target", "spool-textmate", "--parallel", "4"],
      { stdio: "inherit", timeout: 600000 },
    );
  }
  if (args.host) {
    const options = [
      "-DJAVA_HOME=" + java,
      "-DJNI_PLATFORM=" + (process.platform === "darwin" ? "darwin" : "linux"),
    ];
    if (needsHeaders)
      options.push(
        "-DJNI_NDK_INCLUDE=" +
          join(
            ndk,
            "toolchains/llvm/prebuilt",
            readdirSync(join(ndk, "toolchains/llvm/prebuilt"))[0],
            "sysroot/usr/include",
          ),
      );
    build("host", options);
  }
  if (args.android)
    for (const abi of abis)
      build("jni/" + abi, [
        "-DCMAKE_TOOLCHAIN_FILE=" +
          join(ndk, "build/cmake/android.toolchain.cmake"),
        "-DANDROID_ABI=" + abi,
        "-DANDROID_PLATFORM=android-26",
      ]);
} finally {
  libc.symbols.flock(fd, 8);
  closeSync(fd);
  libc.close();
}
