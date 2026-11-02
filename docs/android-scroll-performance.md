# Android Scroll Diagnostics

Run from the repository root. Keep a fully hydrated long chat in the foreground;
do not navigate or open Settings while a measurement is running.

```sh
sh android/tools/run.sh python3 android/tools/device-performance.py \
  --serial DEVICE_SERIAL --rounds 12

sh android/tools/run.sh python3 android/tools/device-performance.py \
  --serial DEVICE_SERIAL --rounds 8 --direction up --method-sampling-us 2000 \
  --output build/artifacts/android-native/device-scroll-methods.json
```

The first command measures alternating native flings without a method sampler.
Use `--direction up` / `down` to traverse new rows rather than repeatedly visiting
one small interval. `--allow-hydrating` explicitly opts into measuring initial
history hydration; settled-history measurements reject it by default.
`--wait-history-seconds 180` waits for hydration on the same focused chat before
issuing any gesture (maximum 300 seconds); navigation/focus changes still abort.

## Scope And Safety

- The debug-only `spool-performance` Activity dump has version 2 and a random
  per-Activity token, not a chat/session/authentication identifier. Only exactly
  one foreground, focused, visible chat is accepted. Flings bind to that token
  and selection epoch; background Activities cannot be scrolled by this probe.
- No touchscreen injection, backend writes, screenshots, messages, IDs, tool
  bodies, or credentials are collected. Counters include retained/mounted row
  counts, aggregate mount/burst times, cancelled draw passes, anchor corrections,
  and process heap estimates. These are cumulative; compare before/after counts
  and note that maximum values include earlier work on that Activity.
- `gfxinfo` and heap metrics are process-wide, not exclusive to the selected
  Activity. They do not measure finger-to-photon latency, identify every visual
  position jump, or establish Web/native raster parity. A timeout is not evidence
  of an obsolete APK, an OOM, or a specific root cause.
- ART method sampling adds overhead. Use its call stacks for attribution, not
  as the uninstrumented frame-rate baseline. Inclusive times overlap and must
  not be summed. The summary reports both thread CPU and wall time, plus trace
  buffer overflow; an overflowed trace is incomplete.
- Profile stop can return before ART finishes writing its file. The collector
  now waits for two identical, structurally aligned v3 dual-clock copies within
  a bounded retry window. Missing headers, partial records and growing files
  are not accepted as evidence. App-method immediate-callee CPU summaries help
  distinguish an expensive native call from an expensive app method's own work;
  recursive branches and inclusive times must not be summed as exclusive time.
- Device exception: Android `system_server` refuses app-private and external
  storage output contexts for `am profile`. The sampler therefore owns a unique
  `/data/local/tmp/spool-scroll-<uuid>.trace` on the phone. It stops the profiler
  and deletes that exact file in `finally`; it never clears other device files.
  Host raw traces stay inside the wrapper's owned `build/tmp/` run and disappear
  at exit. Only the content-free JSON summary is retained. Device disconnection
  can prevent cleanup; report its exact owned path rather than broad deletion.

## Shared Font Variants

A physical-device sampled trace identified repeated
`Typeface.nativeCreateFromTypefaceWithVariation` while mounting transcript rows.
Calling `TextView.setFontVariationSettings` for every new label rebuilt native
fallback chains on the tested OEM firmware. `NativeTypefaces` now bakes the same
requested variation into an immutable, bounded shared Typeface cache, retaining
the original static-font CSS weight fallback and OEM/system fallback family.

The cache is keyed by requested weight, not text or message identity, and contains
at most 32 faces across UI mounts. Text sizes, locales, CJK spans, line boxes,
colors, and selectable document lifetimes remain per-view. API26/35 tests compare
English/Chinese/Japanese advances and ascent/descent with the previous per-view
font path, as well as shared object identity. A TextView's variation-settings
string need not be populated when its immutable Typeface already contains the
axis; geometry and actual font metrics are the checks, not that metadata string.

This removes a measured source of redundant font construction. It does not by
itself prove that whole-turn mounting, very large expanded activity groups,
placeholder-height corrections, or physical scrolling are fully smooth.

## Bounded Warm Documents

Transcript rows still detach outside the visible/overscan window. A small warm
cache now retains their detached keyed documents so reversing direction does
not rebuild the same Markdown, selectable text, metadata and icon views. A
remount rebinds the latest row data before attaching; stale stream or profile
data must not be rendered from the cache. Detachment still stops the existing
tool-body requests, timers and view observers.

The cache has simultaneous limits: 12 rows, 768 views, 262,144 displayed text
characters and 2,048 transcript parts. A single row with more than 160 views,
65,536 displayed characters or 512 parts is not retained. Eviction, row removal
and viewport disposal clear the corresponding document references. These are
UI reuse limits, not transcript pagination or tool-body API limits; no messages
are removed from the model. Explicit selection/focus continues to pin the
attached row separately, rather than putting its live document into the cache.

After mounting, the row measures at its actual width. If its height matches the
retained placeholder, it lays out in place and can draw in the current pass;
the viewport no longer cancels the entire scroll frame merely because a known
height row reattached. Actual height changes still request parent layout and
row-relative anchor restoration before drawing. Tests exercise document identity,
offscreen updates, cache bounds, giant-row exclusion and unchanged-height draw
passes on API26/35. First-time rendering of an uncached large row remains a
separate potential hotspot; these tests do not prove every such row is smooth.

## Stable Text-Origin Checks

A subsequent warm-row method sample identified `NativeTextView.alignOrigin` as
the largest remaining app-side CPU path. Even unchanged frames called the
platform TextView layout/padding APIs before checking the document's existing
CSS baseline phase; a cached row alone does not avoid those queries or a
baseline-phase change's span/layout work.

The pre-draw fast path now compares the metric-span identity, ancestor document
top and fractional rounding offset first. An unchanged document skips the
native layout/padding queries. Real input/geometry changes and attachment invalidate the fast path;
new text/metric spans, changed fractional offsets and even direct ancestor
`offsetTopAndBottom` calls are still observed. Document coordinates deliberately
exclude scroll translations, as before. The existing CSS baseline, underline,
selection and ancestor-move tests remain unchanged; an additional API26/35 test
counts zero editor-padding queries over 120 unchanged pre-draw passes and
checks invalidation on real changes. This is not a different baseline rule or
an omission of the CSS snapping needed for Web alignment.

A parent-only remeasure/layout of the same text geometry now also retains this
cache. Previously, every `onLayout`, including `changed=false`, cleared it:
an API26/35 test counted 120 padding queries for 120 otherwise unchanged parent
layout passes. The updated path counts zero for those passes while retaining
the same selectable document and native Layout object. The view's own layout
requests, a replaced native Layout, changed bounds/ancestor origin, new text
metrics and vertical gravity changes still invalidate or recheck the origin.
Vertical gravity is explicit because TextView can change it without requesting
a new layout. Padding, gravity, max-line and text changes are exercised separately.

CSS origin observation also has its own listener identity, rather than overriding
TextView's `onPreDraw`. The framework already implements that method as a
one-shot callback for text/cursor visibility and selection handling. Overriding
it without its normal lifecycle left the platform's `mPreDrawRegistered` state
set indefinitely; simply calling `super` on every CSS frame would unnecessarily
repeat the platform's one-shot work. An independent listener allows the inherited
callback to finish/unregister while CSS observation continues until detachment.
API26/35 tests inspect that platform state (test-only reflection), count one CSS
callback per frame, retain selection/document identity and exercise detach/reattach
without losing or duplicating the listener.

These additional changes were subsequently cover-installed and measured in the
device follow-up at the end of this document. Their operation-count and lifecycle
checks are not a claim of a particular device frame-rate improvement; the physical
results in the next section belong to the earlier installed package.

## Fling-Safe Height Corrections

A settled-history, one-direction device run still reproduced stutter on entering
previously unmeasured rows: 43 height corrections / deferred draws, 13.07% janky
frames and a 113ms 99th percentile. This is distinct from waiting for history
pages or tool-detail API responses.

The framework `ScrollView` advances a fling using an absolute scroller coordinate.
When a virtual row above the reader grows, the row anchor corrects `scrollY`, but
the next fling frame replaces that correction with the old absolute coordinate.
An API26/35 regression reproduces a 2,000px snap-back in that case. The production
history now uses AndroidX `NestedScrollView`, whose fling advances by the delta
between consecutive scroller samples. The same correction therefore persists
without restarting the fling or changing its velocity. Additional tests compare
corrected/reference trajectories in both directions, with positive and negative
height changes. They exercise the actual native scroller, not a mocked formula.

The transcript viewport, row cache and reading-anchor behavior remain unchanged;
the latter two depend only on View position/layout APIs and support either
container. History hydration, streaming, selection and composer tests use the
production container. This remains native UI, not a browser or WebView.

The cover-installed fix was measured on the same long chat (65 turns, over
16,000 transcript parts), without method sampling:

| Run | Janky frames | P99 | New height corrections / deferred draws |
| --- | --- | --- | --- |
| Traverse older unmeasured rows | 67 / 1,328 (5.05%) | 97ms | 18 / 18 |
| Alternate through this nearby interval | 126 / 1,528 (8.25%) | 48ms | 2 / 2 |
| Repeat that warmed interval | 110 / 1,577 (6.98%) | 48ms | 0 / 0 |

These are not a controlled before/after speed ratio: starting positions, rows
visited and cache state differ, and live stream updates continued. The warmed
run reused 24/24 mounted documents; native allocated memory was about 81.4 to
82.2MiB. Those numbers are not total process PSS or proof against future OOMs.
The sampled follow-up still identifies CSS text-origin checks, transcript
mount/layout and live-event reconciliation as remaining main-thread work.
An earlier sampled attempt produced an incomplete trace and is not evidence;
the later valid sample is attribution-only, not a frame-rate baseline.

Raw content-free measurements are in `build/artifacts/android-native/` as
`device-scroll-origin-{fastpath,repeat,cold}.json` (framework container),
`device-scroll-delta-{cold,repeat,warm}.json` (delta container), and
`device-scroll-delta-methods.json` (sampled). The retained verification manifest
records the installed APK hash, test results, deltas and measurement caveats.
Residual first-time layout and frame jank remain; neither these checks nor
gfxinfo establish perfectly smooth finger scrolling or full Web raster parity.

## Text-Origin Lifecycle Device Follow-Up

The user-authorized follow-up first measured the existing installed package,
then cover-installed the parent-relayout cache / separate TextView-listener fix
without clearing app data. The phone runs API34. The new installed APK SHA256 is
`2c47f81837f7cce73195dd4e5fa9b98cc7e5bec9921ae1ad69fec30ba490dd6d`.
The same long chat was fully hydrated before issuing gestures (66 turns and
over 16,400 transcript parts); live updates continued during all runs.

| New-package run | Janky frames | P99 | New height corrections / deferred draws |
| --- | --- | --- | --- |
| Initial 16 alternating flings | 80 / 2,222 (3.60%) | 73ms | 1 / 1 |
| Repeat 16 flings through the same warmed interval | 84 / 2,254 (3.73%) | 69ms | 0 / 0 |
| Traverse older rows with 12 upward flings | 81 / 1,293 (6.26%) | 97ms | 23 / 23 |

The warm repeat reused 16/16 mounted documents; the older traversal reused
14/37. Both still contain visible-frame-time outliers. The earlier-package
baseline in this follow-up recorded 8.31% / 44ms on alternating flings and
8.36% / 81ms on older traversal. Those are **not controlled before/after ratios**:
reinstallation changed process lifetime and document-cache state, the starting
regions and number/size of mounted rows differ, and live data changed. In
particular, the new package's lower jank percentage does not establish improved
tail latency; its measured P99 remains high.

No process crash or OOM was reproduced during these bounded tests, not a guarantee
against future OOM. The initial new-package warm method sample was complete and
not overflowed, but still attributed about 2,127ms of 7,516ms sampled main-thread
CPU to `NativeTextView.alignOrigin`. Sampling adds substantial overhead and this
is attribution only, not a speed ratio against the earlier package. First-time
mount/Markdown layout and text-origin work remain optimization targets.

A final additional ordinary-scroll measurement did not receive an Activity
probe and was aborted without a performance result. It is excluded from the
table. A subsequent read returned the same foreground Activity token, selection
epoch and process PID (24718), now responsive. This does not diagnose why that
attempt had no response, and that interruption is not counted as a passed test.

One later short sampled attempt returned a file without ART's header terminator.
It produced no retained performance report, was not used for conclusions, and
its owned raw device/host files were removed. The bounded post-stop completion
check above was added afterward, with tests for delayed headers, incomplete
records, still-growing files and timeout rejection.

The retried six-fling trace passed that completion check without overflow. Its
immediate-callee summary attributed about 1,345ms of `alignOrigin`'s 1,424ms
inclusive main-thread CPU to `SpannableString.setSpan`, versus about 9ms to the
ancestor-origin walk. This identifies span-triggered text layout as a more
specific remaining target than another ancestor-coordinate cache. This sample
visited a different interval from the first warm sample and does not establish
an improvement ratio. The trace collector and branch summary run on the host;
they do not change the installed APK.

The package passes 677 Android tests, lint and assembly; the diagnostic Python
suite passes 13 tests. Fresh API35 message/Markdown/composer fixtures pass 9,284
Web geometry/semantic comparisons. The newly expanded API26 gate exposes 104
message-bubble width/position differences; the Markdown and composer gates pass
on that API. This is an outstanding visual-compatibility gap, not a demonstrated
regression from this optimization, and is not waived by the passing Java tests.
The overflow editor's final clipped glyph pixels also still need raster review.
Neither the phone scroll measurements nor those fixture comparisons establish
full 1:1 whole-app visual parity.

Content-free reports use `device-scroll-current-*` for the pre-install baseline
and `device-scroll-origin-lifecycle-*` for the new package. The verification
manifest retains the earlier checkpoint separately from the new one.

## Virtual Rows As CSS Origin Boundaries

The same Codex session was re-measured on the API34 phone after the store parsed
it as 67 turns and 16,720 parts. Every turn exceeds the 200,000-byte page budget,
so hydration is 67 single-turn pages; the largest turn holds 780 parts.

The debug dump gained attribution counters: `textOrigin` reports CSS baseline
re-snaps (`aligns`, `freshAligns` for a document's first alignment, `alignMs`,
and the reason a repeated snap saw a different `topChanges`/`roundingChanges`/
padding), plus `setText` counts; the viewport reports `measureMs` and in-place
layouts separately from mount time. Counters are process-wide and cumulative.

Two causes accounted for nearly all repeated re-snaps:

1. `cssBlockOrigin` summed ancestor tops up to the window. A placeholder-to-real
   height correction above the reader therefore changed the origin of every
   mounted text view below it: 720 of 922 re-snaps in 12 upward flings. The walk
   now stops at `NativeTranscriptViewport.Row`. Text inside a virtual row snaps
   against the row's own top, so sibling rows cannot invalidate it. This is a
   deliberate deviation from a whole-document origin: a row's fractional document
   position no longer contributes to its baseline phase, a sub-device-pixel
   difference. The existing mdpi/420dpi/xxhdpi origin, scroll and lifecycle
   tests are unchanged and pass.
2. A row released during a pre-draw pass still received that pass's remaining
   callbacks, because `ViewTreeObserver` dispatches a snapshot. The detached
   ancestor chain produced a different origin, so each warm document rebuilt its
   layouts once while detached and once more on remount: 584 of 609 re-snaps in a
   16-fling alternating run with one correction. `alignOrigin` now returns for a
   view that is not attached to a window; reattachment still re-checks in
   `onLayout`. `NativeViewportOriginTest` pins both behaviors on API26/35.

Painting a frame while only offscreen rows changed height was also tried and
rejected: moving the relayout and anchor correction to the next frame made the
upward traversal worse (86 janky frames, 5.70%, 109ms P99).

| Run on this chat | Janky frames | P50 | P90 | P99 | Re-snaps | Re-snap time |
| --- | --- | --- | --- | --- | --- | --- |
| Upward, before | 58 / 1,511 (3.84%) | 14ms | 21ms | 61ms | 922 | 1,590ms |
| Upward, after | 34 / 1,569 (2.17%) | 10ms | 18ms | 65ms | 219 | 365ms |
| Alternating, before | 87 / 2,238 (3.89%) | 15ms | 19ms | 65ms | 609 | 1,201ms |
| Alternating, after | 35 / 2,420 (1.45%) | 9ms | 14ms | 38ms | 25 | 47ms |

The "before" re-snap columns come from an instrumented build of the previous
code; its frame statistics match the uninstrumented baseline within run-to-run
noise. The 219 and 25 remaining re-snaps are each document's first alignment.
Corrections and deferred draws per run are unchanged (14 upward, 1 alternating).
Remaining upward cost is first-time mounting: about 1.4s of view construction
across 12 flings with a 191ms maximum for one row, plus roughly 360ms of measure.
Raw reports are `build/artifacts/android-scroll-fix/{baseline,instr,fix4}-*.json`.
The server-side page shape (one turn per page for this session) is unchanged.
