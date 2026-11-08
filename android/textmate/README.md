# Android TextMate Adapter

This Java library uses the original installed Web TextMate grammars and Pierre
themes. It runs natively; it does not bundle Shiki JavaScript, a JS engine, WASM
or a WebView. Code grammars (Go, TypeScript, etc.) are independent of the three
UI locales: Chinese, English and Japanese.

## Pinned Inputs

- Eclipse TM4E core 0.14.1, distributed as
  `org.netbeans.external:org.eclipse.tm4e.core-0.14.1:RELEASE300`.
  Binary SHA-256: `d78182d6362657a745236d1a085ac2ee11a958f9c3354d8faa87e36563e16174`.
  Source: <https://github.com/eclipse-tm4e/tm4e/tree/0.14.1>.
- Oniguruma 6.9.10 from
  <https://codeload.github.com/kkos/oniguruma/tar.gz/refs/tags/v6.9.10>.
  Archive SHA-256: `ad92309d0d13eebc27f6592e875f3efbfa3dda2bf6da5952e00f0a2120c921a8`.
- Gson 2.13.1 and SnakeYAML Engine 2.9 are the TM4E readers. The app loads only
  the JSON reader; YAML is retained for TM4E binary compatibility.
- `sync-web-syntax.mjs` exports the installed Web packages, not downloaded
  replacement grammars. Their versions are recorded in `syntax/manifest.json`.

## Exact Changes To TM4E

`android/tools/prepare-textmate.py` verifies the upstream binary checksum,
retains upstream resources, removes invalidated JAR signatures, and makes only
these binary changes:

1. Substitute `java.lang.System`/`System.Logger` constant-pool references with
   `TextMatePlatform` and its Android-compatible logging interfaces. The shim
   delegates array copies, time and line separators to the real `System` and
   logging to `java.util.logging`. Tokenizer and theme rules are unchanged.
2. Remove `OnigString`, `OnigRegExp`, `OnigResult` and their nested classes.
   The checked-in Java sources replace the same binary APIs with an explicit
   native Oniguruma boundary and correct UTF-8/UTF-16 offset maps, including
   supplementary characters. The original Joni/Jcodings backend is not shipped.
3. Redirect `CharSequence.isEmpty()` and `StringBuilder.isEmpty()` to the same
   `length() == 0` operation in `TextMatePlatform`. These newer JDK methods are
   absent on API 26 and are not rewritten by core desugaring. Only the invoke
   opcode/reference changes; interface-call padding becomes NOPs, preserving
   branch offsets, exception ranges and stack maps. No grammar semantics change.
4. Escape the literal closing brace in `RegexSource`'s capture-substitution
   helper. The JDK accepts the unescaped brace, but Android ICU throws a
   `PatternSyntaxException` during class initialization, disabling every source
   and diff highlighter. The escaped expression has the same capture semantics;
   no Oniguruma grammar patterns are rewritten.

The transform has deterministic ZIP ordering/timestamps. The replacement Java
files document their upstream EPL/MIT attribution. These source modifications
are distributed under EPL-2.0 alongside this recipe; the rest of the app is not
part of the modified TM4E library. See `android/licenses/TM4E.txt`,
`android/licenses/EPL-2.0.txt` and `android/THIRD_PARTY_NOTICES`.

`src/main/cpp/syntax.c` is a small Spool JNI adapter, statically linked with
unmodified Oniguruma C sources. Each compile has a registry-scoped owner. Every
search frees its match parameters, capture buffer and region, including failure
paths. Closing the owner frees all compiled patterns; synchronized handle access
prevents use-after-free. There are no finalizers or Cleaner APIs. Match and total
search retry limits are both 1,000,000; pathological expressions fall back to
plain display rather than blocking indefinitely. A retry limit is not a strict
wall-clock deadline for every possible grammar.

## Build And Verify

Run from the repository root, using the standard scratch/cache wrapper:

```sh
sh android/tools/run.sh bun android/tools/sync-web-syntax.mjs --check
sh android/tools/run.sh python3 android/tools/build-syntax-native.py --host --android
sh android/tools/check-java.sh
sh android/tools/run.sh sh android/tools/gradle.sh :app:assembleDebug :app:testDebugUnitTest :app:lintDebug
sh android/tools/run.sh python3 android/tools/check-syntax-package.py --report build/artifacts/android-native/syntax-package.json
```

The native build requires Python 3.12+, CMake 3.22+ and the configured Android
NDK. It reuses `build/cache/android-syntax` and writes ordinary host/ABI outputs
under `build/android/syntax`. The host build uses JDK JNI headers, or the NDK's
VM-neutral `jni.h` with `-idirafter` when Android Studio's JBR omits headers.
Host/Android builds share a file lock. No upstream test programs are built and
no system native libraries are required at runtime.

Source and native code widgets schedule syntax computation on `codeWorker`;
nested Markwon code prepares tokens on its parser worker. The shared engine
serializes both tokenization and cleanup with its existing monitor.
Grammars are registered lazily, retaining visited-language injection history,
and compiled handles are released when changing the current language/theme or
clearing the session. Each document/hunk side resets multiline state. Lines
longer than 1,000 UTF-16 units stay plain without advancing that state, matching
the Web. Detachment cancels tasks; view/session epochs reject late results.
Source keeps its original selectable text and tab spans. Diff colors update
together with precise rows; neither renderer creates one View per token.
Markdown fences resolve language names/aliases from the grammar manifest, while
file previews keep the exact filename mapping. Rebinding a code widget cancels
its previous subscription and removes only syntax-owned spans, retaining
selection and tab stops when the code bytes have not changed.

Conformance uses actual Web token outputs, checking every UTF-16 unit's color
and font flags in 29 fixtures, both themes. It includes embedded grammars,
surrogate pairs/CJK, tabs, CRLF, multiline state, unknown filenames and long-line
limits. JNI tests also cover ownership, invalid patterns and bounded retries.
These are broad regression fixtures, not exhaustive proof for every grammar.
Web currently defaults to its JavaScript regex engine; native Oniguruma can
differ on untested patterns. Warnings about absent optional embedded grammars
are possible, just as those bundles are not globally registered on Web.
The package check separately parses the shipped DEX method tables to reject
unbackported API-26-incompatible JDK calls and accidentally packaged Joni classes.
It verifies exact grammar/notice bytes, both JNI ABIs and 16 KiB ELF alignment.
Robolectric renderer/lifecycle tests do not replace actual Android ABI/device,
font-rasterization, keyboard or safe-area verification.
