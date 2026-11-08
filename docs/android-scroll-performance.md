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
`--pause-seconds 3` lengthens the pause between flings (default 1.25 seconds). A
7000px/s fling decelerates for about two seconds, so the default cadence never
lets the scroller rest; pauses of three seconds or more emulate reading between
gestures, which is the only time offscreen row preparation can run.

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

## Offscreen Row Preparation And Incremental Rebinds

The remaining upward-traversal cost after the origin fixes was first-time row
construction itself: about 1.4s of view building across 12 flings with a 191ms
maximum for one row, plus roughly 360ms of first measure, all executed inside
the fling's pre-draw pass, followed by a placeholder-to-real height correction
that cancelled the frame. Two architectural changes move that work off the
gesture path.

### Preparing rows between frames

`NativeKeyedViews` now separates committing a reconcile pass from attaching it:
`commit()` performs the same reuse/create bookkeeping as `end()` but leaves the
views detached, and `attach()` reconciles them into the parent later. A
`NativeTranscriptViewport.Row` uses this to `prepare()` its document while
unmounted: the keyed views are built into a throwaway `LinearLayout` stage that
is never added to a window, measured there with the row's own `EXACTLY` width
and `UNSPECIFIED` height, removed again, and kept in the row's warm-cache slot
with the measured height as the row's retained height. Because the stage is
detached, attach-driven work (syntax highlighting, tool-detail requests, precise
diff layout, summary ticks, origin observers) does not start for offscreen rows.
Entering the viewport then attaches an already-built, already-sized document, so
the existing known-height in-place layout path draws without cancelling the
frame. A `bind()` that arrives while a row is prepared marks it stale; the mount
refreshes it through the normal keyed path instead of attaching stale views.

Preparation is scheduled by the viewport itself through a main-thread `Handler`,
never inside measure, layout or draw. Every pre-draw pass samples `scrollY`; a
change re-arms a 48ms quiet window (three missed frames), so the chain only runs
once the scroller has actually stopped; a fling's decelerating tail still
produces a frame every 16ms until then. Frames that also restored the reading anchor do not define the
direction of travel. Each turn prepares one row, then re-arms with no delay
until the bounded candidate set is exhausted: the nearest unbuilt rows beyond the
overscan band, up to three on the preferred side and three on the other side
when the preferred side is exhausted or already warm. Rows above the retainable
size (more than 512 parts, or a built document over the view/character limits)
are never prepared and are marked so they are not rebuilt. Prepared rows share
the existing warm-cache budget and eviction, so memory does not grow; the real
height is applied in the quiet window with the same anchor restoration a
correction frame uses. `NativeHistoryScroll` gained two read-only queries
(`animating()`, `restores()`); its listeners and behavior are unchanged.

The debug dump's `viewport` object gained `prepares`, `prepareMs`,
`maxPrepareMs`, `preparedRows`, `preparedMounts`, `stalePrepares`,
`heightMisses`, `preparedEvictions`, `preparedSkips` and `preparedCorrections`.
`heightMisses` counts prepared rows whose attached measure disagreed with the
detached one; it is the load-bearing check of this design and should stay at 0.

### Rebinding only changed turns

`NativeChatState` merges now report what changed. `mergeParts` already restores
the old reference of every unchanged part, so a turn is unchanged when its parts
are identical by reference and its fields, ignoring the rebased part ranges, are
equivalent. `eventTracked`, `reconcileTracked` and `prependTracked` return the
merged chat together with the changed turn ids, a reorder flag and an unchanged
flag; the previous entry points delegate to them and produce the same JSON.
`renderChat` still reconciles every row's position and part count, but only
rebinds rows whose turn is in the change set (or which were never bound).
Untouched mounted rows no longer run the two whole-part deep comparisons in
`NativeTurn.bind` and `NativeActivityList.bind` on every SSE frame or poll, and
untouched unmounted rows keep their closures. An unchanged frame keeps the
current state object, as an unchanged poll already did. Callers outside the
event path (profile refresh, send flow, history retry) still rebind every turn.

Robolectric API26/35 tests pin: prepared rows enter at their real height without
a cancelled frame and with `heightMisses == 0`; preparation attaches nothing and
waits for the scroller to be quiet; stale prepared rows are refreshed at mount;
prepared rows respect the cache bounds and are cleared on dispose; giant rows
are never prepared; the production Activity prepares between frames without
changing what is attached and a prepended page detaches nothing; tracked merges
equal the untracked ones for every `web-behavior.json` case while every unlisted
turn keeps identical part references; turn events rebind only their turn, an
identical snapshot rebinds nothing, and a full render still rebinds everything.

### Device A/B on the same build

The debug probe accepts `spool-performance prepare on|off`, so the same APK
measures both arms on the same chat (67 turns, 16,720 parts, API34 phone).
At the tool's default 1.25-second cadence the mechanism cannot engage: a
7000px/s fling decelerates for about two seconds, each gesture restarts the
scroller after about 3,050px, and the quiet window never opens (`prepareTurns`
stayed at 0 or 2 across 12 flings; both arms then measured 2.3-3.4% janky
frames that reflect cold mounts alone). With `--pause-seconds 3`, each arm ran in
a fresh process after full hydration, traversing older rows upward from the same
start position; the order was reversed for the second pair.

| 3s pauses, 12 upward flings | Prepare on #1 | Prepare off #1 | Prepare off #2 | Prepare on #2 |
| --- | --- | --- | --- | --- |
| Janky frames | 89 / 3,356 (2.65%) | 364 / 2,667 (13.65%) | 201 / 2,844 (7.07%) | 142 / 3,334 (4.26%) |
| P50 / P90 / P95 / P99 | 10 / 14 / 18 / 57ms | 17 / 40 / 44 / 57ms | 17 / 29 / 40 / 53ms | 10 / 17 / 23 / 57ms |
| Mounts: cold / warm / prepared | 0 / 26 / 15 | 12 / 8 / 3 | 12 / 8 / 3 | 0 / 25 / 15 |
| Cancelled frames / anchor corrections | 0 / 15 | 12 / 12 | 12 / 12 | 0 / 15 |
| Mount time (max per row) | 422ms (180ms) | 1,750ms (227ms) | 1,710ms (255ms) | 400ms (175ms) |
| Prepares, time in pauses (max) | 30, 3,152ms (243ms) | 0 | 0 | 29, 3,078ms (242ms) |
| `heightMisses` | 0 | - | - | 0 |

Both arms are deterministic enough to reproduce their trajectories exactly
(identical start and end `scrollY`). The "on" arms' janky-frame totals include
the frames delayed by preparation itself during the pauses, when nothing is
moving; the gesture-time tail is better read from P90/P95 and from the zero
cold mounts and cancelled frames. The two "off" arms differ in total janky
share (13.65% versus 7.07%) with the same mount counts, so that total carries
run-to-run noise; P50 and P95 are stable. The 15 anchor corrections in the "on"
arms happen in the pauses when a prepared row above the reader takes its real
height; the reading position does not move. `heightMisses` stayed at 0, so the
detached stage measured exactly like the attached rows for this chat's content.

Residual cost is unchanged by design: a prepared row still costs 100-250ms of
main-thread time when it is built, a fling that never pauses still mounts cold,
and the first CSS baseline snap remains on the mount path. Raw content-free
reports are `build/artifacts/android-scroll-prefetch/pause3-{on,off}-up*.json`;
the 1.25-second-cadence runs are `up.json`, `alternate.json` and `ab*.json`.

## Default Markwon Recycler History

All build types now render assistant answers by default with Markwon 4.6.2's official
`MarkwonAdapter` / `SimpleEntry`, composed with the existing native user rows,
activity disclosures, approvals, actions and message footers using AndroidX
`ConcatAdapter`. A single `RecyclerView` is the scrolling surface. Markdown
blocks are recycled across answers rather than mounting a whole turn inside a
`NestedScrollView`. The GFM tables, task-list and strikethrough plugins are
enabled; blocks containing tables use the existing selectable native table
renderer inside a recycled block holder. File citations retain their native
file action through a parser/visitor extension and all links use the existing
message-link router. Images do not introduce an automatic remote image loader.

The renderer uses the existing CommonMark 0.24.0 classes. The obsolete
`com.atlassian.commonmark` transitive artifacts are excluded from every Markwon
dependency to prevent duplicate `org.commonmark` classes in the APK. The
API26/35 GFM tests exercise the resulting binary combination, including ordered
lists, tables, task lists, strikethrough and file citations.

Parsing and native text precomputation run on an owned single-thread worker.
Changed or removed answers and
disposed pages reject stale results. Unchanged sources retain their official
adapter and node/span cache; hydration inserts earlier adapters while retaining
the native reading position. The background worker stops when the page is
disposed. Prepared data is published before its nodes; text binding remains
synchronous. Upstream explicitly warns
that `PrecomputedTextSetterCompat` must not be used directly with
`markwon-recycler`, because its asynchronous `setText` can overwrite a recycled
holder. The renderer uses a synchronous TextSetter with the public AndroidX
`TextViewCompat.setPrecomputedText` API instead. See the [official implementation warning](https://github.com/noties/Markwon/blob/master/markwon-core/src/main/java/io/noties/markwon/PrecomputedTextSetterCompat.java).

Markwon is the default without an intent or saved preference. To restore it
after a debug-only comparison, preserving app data:

```sh
sh android/tools/run.sh adb -s DEVICE_SERIAL shell am force-stop com.cloudgeek.spool.android.dev
sh android/tools/run.sh adb -s DEVICE_SERIAL shell am start \
  -n com.cloudgeek.spool.android.dev/com.cloudgeek.spool.android.MainActivity \
  --ez spool_markwon true
```

The setting survives app restarts in the debug package. Repeat the commands with
`--ez spool_markwon false` to select the legacy renderer for comparison. Debug
packages default to Markwon when the preference is absent and retain an explicit
saved override. Release always uses Markwon and ignores both the saved override
and intent extra. The old `rendering-trial` preference file/key is retained for
debug compatibility; the runtime field is named `useMarkwonHistory`.

The `spool-performance` dump and its ADB scrolling/setup controls remain guarded
by `FLAG_DEBUGGABLE`, independently of the production renderer selection.

The performance dump identifies `renderer: "markwon-recycler"` and reports
block count, created/bound/recycled holders, bind times, parse time, parse errors
and `renderPending`. Settled-history measurements wait for both network history
and background Markdown parsing. RecyclerView scroll range/offset are native
estimates based on visible item sizes; they are not directly comparable with
the previous renderer's exact document pixel coordinates.

Top-level fenced and indented code blocks use `NativeCodeBlock`, shared with
native Markdown previews: language label, copy feedback, selectable original
text, tab spacing and horizontal scrolling. Its TextMate work starts on
attachment, cancels on detach/rebind, and rejects results for an old language or
document. Code within quotes/lists receives syntax spans during background
Markwon preparation. The shared engine serializes tokenization and cleanup.
The TM4E adapter explicitly escapes the closing brace in its Java capture helper
for Android ICU; the unescaped form previously disabled source/diff syntax at
class initialization even though host-JVM tests passed. APK checks reject that
old constant, and the release device smoke checks actual colored spans in both
the answer code widget and file source preview.

Markwon's ordinary Markdown typography and table wrapping still differ from the
existing Web raster contract; whole-document pixel parity is not claimed.
User bubbles and expanded tool/activity details retain their existing native
renderers. Content-free device measurements and the verification summary live
in `build/artifacts/android-scroll-debug/`.

### Completed long-session retest

The initial Markwon trial checkpoint APK SHA256 was
`df2989d8d5609b951cf5b1f2993191561c1746d8269db4599cfc55d3e4a268cd`.
The user selected the same API34-device chat with 36 turns and 2,382 parts. All
history pages and background parses completed before the three runs; the list
contained 247 native/Markdown blocks and all runs stayed on the same Activity
and selection epoch.

| Native ADB fling run | Janky frames | P95 | P99 |
| --- | --- | --- | --- |
| 24 upward, 350ms pauses | 114 / 980 (11.63%) | 32ms | 77ms |
| 24 downward, 350ms pauses | 109 / 1,008 (10.81%) | 34ms | 73ms |
| 32 alternating, 250ms pauses | 60 / 1,208 (4.97%) | 22ms | 27ms |

The alternating run stays in one nearby warmed interval. Each run created zero
new holders and performed zero additional Markdown parsing; traversal still
spent about 1.9 seconds binding existing holders, including native user/activity
and footer subtrees. An additional non-overflowed ART sample attributes remaining
work to those native bindings and official Markdown TextView binding. Its frame
times are not an uninstrumented baseline, and bridge/overload inclusive method
totals overlap.

The tail is shorter than the earlier 150–200ms native samples on this chat, but
starting positions and cache states differ. At that checkpoint the approximately 11% traversal
jank remains a limitation; these numbers do not establish a controlled speed
ratio or completely smooth scrolling.

Two integration corrections are in the final APK: Recycler native user/tool
rows are local CSS text-origin boundaries, so offsetting an item does not
re-snap its text; `renderMarkwonChat` schedules the history loader on every
render, so hydration continues past the initial pages. An API26/35 production
Activity test loads successive pages after successive paints until `has_more`
is false and preserves the composer document/focus. The measurement tool also
accepts `--min-parts 1000 --expect-renderer markwon-recycler` and aborts before
gesturing when the wrong chat/renderer is selected.

The latest 40 Markwon/history/origin tests and 14 Python probe tests pass;
the preceding 94-test focused run, APK build and lint also passed. A broader
1,036-test run retained five failures: one activity-classification fixture
mismatch and four native-library classloader conflicts. The native syntax
groups pass when run separately. The full suite was not repeated after the
final origin/history corrections. Exact logs, report paths and limitations are
recorded in `build/artifacts/android-scroll-debug/verification.json`.

### Native holder reuse and prepared text follow-up

The user-authorized continuation addresses the sampled native row construction,
date formatting, folded tool snapshots and Markdown TextView work. The installed
APK SHA256 is now
`d1534f5250d7a03d634f9f0e20bb1be0fb80b7c32c829cd9409b9272ca9d6daa`.

Native headings, footers and other UI have separate shared view types and
bounded recycled pools (8 heading, 8 footer, 2 other holders; 24 Markdown
holders and the existing 8-item view cache). A recycled native holder retains
its keyed widgets; generic user/response keys allow a new turn to reuse the
bubble, footer and activity-summary widgets. Transferring ownership clears
selection/copy feedback and switches to the destination turn's own disclosure
state, deferred tool parts and image actions. Returning to a message restores
its user expansion state. An unchanged owner/revision skips binding. Removed
or disposed pages clear retained holders with the RecyclerView.

Folded completed Markwon turns retain their latest deferred tool data without
deep-copying it for an invisible comparison; expanded and live paths still
detect changes. New turns no longer execute their constructor binding again
immediately in `renderTurn`. `NativeMessageTime` reuses bounded, per-thread
formatters (16) and formatted results (128). The result keys include timestamp,
locale, a cloned time zone and today/same-year classification; day, year and
custom-zone changes cannot reuse an incorrect display value.

The background worker now renders each Markdown node to styled text and
precomputes native metrics using `PrecomputedTextCompat`. The official adapter
still owns block binding/recycling and binds synchronously through the public
AndroidX setter. Nodes with ReplacementSpans, including the tested tables, keep
native measurement; incompatible font/density changes fall back to a copied
styled document with the current widget metrics. No background task writes to
a recycled TextView. Stale/removed/disposed results are still rejected. The
current chat prepared 175 blocks: 170 precomputed and 5 native-measurement
exceptions, with zero precompute errors or binding fallbacks.

All runs below used the same fully loaded 36-turn, 2,382-part chat and 247 blocks.
Traversal used 24 flings at 350ms intervals; alternating used 32 at 250ms.

| Run | Janky frames | P95 | P99 | Total binding time |
| --- | --- | --- | --- | --- |
| Upward, final build | 32 / 1,148 (2.79%) | 18ms | 36ms | 577ms |
| Downward, final build | 33 / 1,174 (2.81%) | 17ms | 32ms | 499ms |
| Alternating, final build | 8 / 1,239 (0.65%) | 17ms | 19ms | 21ms |
| Upward, independent repeat | 35 / 1,149 (3.05%) | 19ms | 34ms | 549ms |
| Downward, independent repeat | 32 / 1,180 (2.71%) | 17ms | 34ms | 520ms |

For comparison, the initial trial above had 10.81–11.63% traversal jank, 73–77ms
P99 and about 1.9 seconds binding per traversal. Starting rows, cache warmth and
frame totals vary, so these are repeated device observations rather than a
controlled speed ratio. The final non-overflowed method sample contains no
samples of `NativeMessageFooter`, `NativeTurn` or `NativeActivityList`
constructors and no `NativeChatState.snapshot` samples. That is attribution
evidence for the removed hot paths, not proof those functions never execute.
Remaining work is predominantly native text binding/measurement and rendering.
The 2.7–3.1% traversal jank still means this is not a claim of zero missed frames.

The latest focused suite passes all 126 tests, with APK build and lint passing.
It checks actual user/footer/summary object reuse, per-turn expansion, old/new
tool data, selection and copy isolation, date/year/zone/cache bounds,
background result races, unchanged answer caching, and equal native text line
starts/ends/baselines/heights at mdpi/420dpi/xxhdpi on API26/35. Prior full-suite
failures remain documented above; the full suite was not re-run for this
continuation. Current reports are `hotspots-precomputed-{up,down,alternate}.json`,
`hotspots-repeat-{up,down}.json` and `hotspots-precomputed-methods.json`, plus
`hotspots-tests.log` and the updated verification manifest.


## Expanded Tools And Shared Turn Presentation

A subsequent user report reproduced a large pause when scrolling upward into
an expanded tool process containing image previews. The focused Android 34
phone had 19 turns and 3,247 transcript parts, with history hydration complete.
A no-sampler alternating run recorded 25/525 janky frames (4.76%), P99 150ms,
15 slow bitmap uploads and zero new Markdown parses/binds. A separate complete,
non-overflowed ART sample recorded one 600.6ms native chrome binding and about
942–950 mounted Views when the expanded process entered the viewport. That
sample attributes the main-thread burst to constructing the whole activity
subtree. The instrumented frame times are not a performance baseline.

The five review corrections and the extra measured hotspot are now handled:

- Disclosure position is captured relative to its containing recycler item and
  restored during actual layout, including child-only and delayed body layouts.
  User gestures release that anchor; explicit list jumps are respected. Manual
  disclosure pauses following, while ordinary scrolling retains the existing
  90dp bottom-follow threshold.
- Each Markdown block has a stable identity and a retained styled document.
  Background parsing fingerprints source and resolved references, renders and
  precomputes changed blocks, and calculates a diff. Unchanged blocks keep their
  selection. Rendering boundaries, table alignment, task state and resolved
  references participate in identity checks. Retained blocks are unlinked from
  old document parents/siblings. Main-thread delivery is independent of View
  attachment and rejects removed, superseded or disposed requests.
- Image Markdown, bare URLs and file citations use the existing native open
  router. Code, escaped URLs and nested link labels retain literal boundaries.
- Segment reconciliation retains adapter order and ownership. An unchanged
  pass visits each segment once without copying the adapter list per segment.
- Both renderer paths consume `NativeTurnPresentation`: initial input and files,
  chronological process, final answer, actions, approval and normalized error.
  Changed-turn updates reuse every untouched presentation. Turn-local part
  indices remain valid after prepending history. Approval/error UI follows the
  answer and actions in either renderer.
- Markwon expanded processes emit independent user, progress, image, group and
  tool rows. Only visible/nearby rows create native widgets. Distinct row types
  have distinct recycled pools, preventing alternating tool/group/message rows
  from repeatedly rebuilding each other's children. Tool ownership updates
  both details and click IDs. Details retain their existing detach/cancellation
  lifecycle. Thinking preview reuse is bounded to 64 entries and 131,072 source
  plus display characters per factory, excluding entries over 32,768 characters.

The targeted suite passes 142 tests on API26/35, including the original four
review reproductions, cross-block references, insertion/deletion/reordering,
400 expanded tools, mixed-type holder reuse, delayed tool body lifetimes, image
preview ownership, and production Activity steering/files/status/composer flow.
The 500-turn presentation test reads only the changed turn's two parts on an
incremental update. The 14 Python probe tests, debug APK build and lint pass.
An additional broad behavior check still encounters the previously documented
fixture mismatch: `rg -n foo src` is classified as `read`, while the fixture
expects `search`. The classifier, activity model, test and fixture all match
HEAD; this is recorded separately rather than included in the passing suite.

An intermediate row-splitting build reduced the observed mounted View peak to
205 and P99 to 38ms for 8 upward flings, but still had 87/428 janky frames
(20.33%) while traversing new rows. Its nearby alternating run recorded 33/655
(5.04%) and P99 31ms. The follow-up ART sample identified repeated mixed-row
construction and thinking-preview parsing, motivating the separate pools and
bounded preview reuse described above. The sample did not overflow, but its
large instrumentation overhead precludes comparing its frame times.

The refined build was cover-installed and measured without ART sampling. The
same chat still had 19 turns / 3,247 parts; the reader opened additional groups,
so it now contained 752 recycler entries rather than the intermediate 432.
Each fling used a 0.5s pause; upward/downward runs had eight flings and the
alternating run had ten.

| Refined build | Janky frames | P95 | P99 | Sampled maximum Views | Total chrome bind time |
| --- | --- | --- | --- | --- | --- |
| Upward traversal | 66 / 475 (13.89%) | 29ms | 36ms | 204 | 464ms |
| Downward traversal | 66 / 476 (13.87%) | 29ms | 34ms | 204 | 526ms |
| Nearby alternating | 38 / 666 (5.71%) | 22ms | 28ms | 143 | 7.9ms |

All three runs performed zero additional Markdown parses, turn-presentation
builds or segment reconciliations. The alternating run created zero holders and
bound native chrome four times. The large tail is shorter than the initial
150ms P99 alternating observation, while sustained traversal still drops about
13.9% of frames; this is not a claim of consistently smooth rendering.

Reports and the installed APK hash are retained under
`build/artifacts/android-chat-scroll-fix/`. These observations do not establish
a controlled speed ratio: viewport position, rows traversed, cache warmth and
frame totals vary. Expanded rows no longer mount a complete turn's activity
View tree, but individual large progress blocks, tool bodies and image groups
still use their existing native content widgets. Prepared Markdown remains
retained per loaded answer, so total history memory is not bounded by the
holder pool. Very large single blocks/image groups and an overall parsed-data
budget remain separate scalability work.


### Avoiding Duplicate Disclosure Text Layout

The follow-up focused on the remaining dropped-frame percentage while scrolling
through expanded tools. Before this change, a same-chat upward run (19 turns,
3,247 parts, 752 entries, eight flings with 0.5s pauses) still measured 51/484
janky frames (10.54%), P95 27ms and P99 42ms. A separate 10,000us ART sample did
not overflow. Its main-thread call stacks attributed 277ms CPU to native text
updates, with 197ms in `TextView.checkForRelayout`; the sample is attribution,
not the no-sampler baseline.

`NativeDisclosureRow` measures its title and change statistics with AT_MOST and
positions them explicitly. They nevertheless inherited FrameLayout's default
MATCH_PARENT layout parameters. Android's fixed-width TextView update path
therefore reshaped each new title synchronously using the previous holder's
width; the row's subsequent measurement then reshaped it again. The forced
label remeasure in the binder had been compensating for the same mismatch.
Titles and statistics now declare WRAP_CONTENT, allowing TextView to discard
its stale layout and wait for the row's real width. Unchanged text keeps its
existing layout. The measured label sizes, typography, ellipsis and positions
remain the same.

API26/35 tests compare complete pixels, title width/baseline/ellipsis and row
height with the old parameter path after changing both content and width. They
cover summary/group/item rows, mdpi/420dpi/xxhdpi, styled mixed CJK/English text
and changing/removing/recreating statistics. The update test asserts that
binding creates no premature text layout and that unchanged bindings preserve
the current layout object. The 162-test regression set, 14 probe tests, APK build
and lint pass. Attribution, APK hash and verification are retained in
`build/artifacts/android-chat-scroll-fix/row-layout-verification.json`.

The cover-installed text-layout build did **not** demonstrate a reduced overall
dropped-frame percentage. The reopened chat had 411 entries; upward traversal
recorded 67/460 janky frames (14.57%), P95 31ms and P99 46ms; downward traversal
recorded 49/501 (9.78%), P95 26ms and P99 32ms. Text-setting time in the upward
run fell to 83ms across 243 calls (previously 250ms across 136), but the expanded
state and traversed row counts differed, so these are not a controlled speedup.
A complete 10,000us ART sample still found repeated disclosure/TextView
construction when pooled containers crossed turn owners, and repeated change
statistics work in tool binding.

### Reusing Tool Widgets Across Turns

Tool item and group holders now retain their inner widgets across turn owners.
An item explicitly rebinds its factory, tool ID and state; changing either
owner or ID removes the old detail widget before binding the new tool. Clicks
read the current owner. Group headings replace their current click callback.
The factory caches change statistics for its prepared presentation and clears
them when the presentation changes. User, image and progress widgets retain
their existing owner-specific lifetime because their callbacks and image
requests have different ownership requirements.

The content-free debug probe can align a recycler row at the viewport top and
temporarily disable this tool reuse path for an in-process A/B comparison. Both
controls require the exact focused Activity token and selection epoch. Reports
include first/last visible row positions, top offset and inner activity-widget
inflations. The runner rejects changed transcript/disclosure row counts, warms
the same interval if requested, resets before measurement, lets the final fling
settle, and restores the original reuse mode when it exits. For example:

```sh
PYTHONDONTWRITEBYTECODE=1 sh android/tools/run.sh python3 android/tools/device-performance.py \
  --serial DEVICE --expect-renderer markwon-recycler --min-parts 1000 \
  --start-position 240 --direction up --rounds 8 --warmup-rounds 8 \
  --pause-seconds 0.5 --activity-reuse on \
  --output build/artifacts/android-chat-scroll-fix/controlled-reuse-on.json
```

Use an identical start position and expanded state with `--activity-reuse off`.
Row positions identify layout slots only; reports do not include message text,
tool arguments, image URLs or chat IDs. Frame comparisons must use runs without
ART sampling; sampling is reserved for attribution.

The first fixed-interval comparison of cross-turn widget reuse alone did not
reduce jank: off was 57/694 (8.21%) and on was 59/698 (8.45%), both traversing
row 220 to row 56 in the same 369-row expansion state. This narrows its benefit
to less construction/binding work, rather than establishing a frame-rate gain.

At the reader's request, setup is now automatic as well. `--chat-title` selects
one exact matching title from the focused Activity's existing navigation data;
duplicates, missing titles and a replaced Activity are rejected. The runner
waits for complete hydration and retries a failed history read at most three
times through the same native retry action. `--expand-tools` closes other turns,
opens the turn with the most process parts, opens all its groups/images and one
tool detail before its middle image group, then positions the viewport thirty
rows below that image group. This fixture has 19 turns, 3,247 parts, 887 entries,
388 groups, four image groups and one open detail; its start is row 638.

```sh
PYTHONDONTWRITEBYTECODE=1 sh android/tools/run.sh python3 android/tools/device-performance.py \
  --serial DEVICE --chat-title 'EXACT TITLE' --expand-tools --wait-history-seconds 180 \
  --expect-renderer markwon-recycler --min-parts 1000 --direction up \
  --rounds 8 --warmup-rounds 8 --pause-seconds 0.5 --activity-reuse on \
  --output build/artifacts/android-chat-scroll-fix/standard-on.json
```

Subsequent comparisons use `--start-position 638` with the same row count and
toggle `--activity-reuse`. They do not repeat setup or change the expansion
state. A fling's final distance can still vary with scheduling; reports retain
both endpoint row positions. These are warmed scrolling measurements, not a
claim about first-open latency or every possible tool body.

An intermediate whole-label shaping cache showed an aggregate 5.76% off versus
4.55% on in three rounds per mode. After fixing watcher retention and reusing
image headings, two further rounds were 6.31% off versus 5.80% on, with overlapping
individual results. These measurements do not establish a consistent 21% gain.
Another experiment moved tool-row measurement into binding and prefetched two
extra adjacent entries. It regressed to 8.35% on versus 4.44% off and was removed.

The retained widget changes flatten each virtual tool item by one layout
container, reuse image headings within their turn with the current group index,
and clear focus only on the focused descendant. The long-label ellipsis cache
was also removed: its controlled run was 5.29% on versus 4.13% off, with only
eight cache hits in 239 tool binds. None of these cache or extra-prefetch
experiments are part of the final implementation.

The remaining text change avoids copying CJK labels into an empty span wrapper
when the current locale supplies no custom fallback face. Android 9 and later
can retain the original String and its direct BoringLayout drawing path.
Existing style spans remain intact, and a locale change still removes old
custom font spans. Android 8 keeps its former path because native pixel tests
found a mixed-direction rendering difference. Density, ellipsis, baseline,
complex emoji and CJK/RTL parity are covered by native raster tests.

Three balanced fixed-interval runs per mode, with no method sampler, measured:

| Mode | Run 1 | Run 2 | Run 3 | Combined |
| --- | --- | --- | --- | --- |
| Off | 40/702 (5.70%) | 63/682 (9.24%) | 31/726 (4.27%) | 134/2110 (6.35%) |
| On | 37/709 (5.22%) | 25/722 (3.46%) | 48/697 (6.89%) | 110/2128 (5.17%) |

The combined comparison improved, but individual results overlap and do not
establish a stable 4% target. The debug switch includes widget reuse, layout
flattening, statistics reuse and plain-label fallback together; this is not an
isolated attribution to font wrapping.

### Full Traversal and Content Verification

`--traversal bottom-to-top`, `top-to-bottom`, and `round-trip` start at the
appropriate native scroll boundary and continue issuing 7000px/s flings until
`canScrollVertically` reports the other boundary. The round trip goes from
bottom to top and back to bottom. Unlike a fixed fling count, this checks that
the complete range was traversed, including partially visible edge rows.

`--verify-position ROW` can be repeated. Outside the measured frame interval,
the runner aligns those rows before and after traversal and compares salted
SHA-256 digests of visible text, accessibility descriptions, image source keys,
image-loaded state and widget coordinates. The random per-Activity salt and
the digest leave message bodies and image sources inside the app. A mismatch
fails the run while preserving its numeric report for diagnosis. These checks
verify the chosen anchors, not every pixel of every possible tool body.

The final device validation uses three groups, each containing a full upward
traversal, full downward traversal and complete round trip, with images and
the selected tool detail expanded. It uses the same 887-row fixture and no ART
sampler. Reports are `full-{1,2,3}-{up,down,roundtrip}.json` in the current
artifact directory.

```sh
PYTHONDONTWRITEBYTECODE=1 sh android/tools/run.sh python3 android/tools/device-performance.py \
  --serial DEVICE --traversal bottom-to-top --activity-reuse on \
  --verify-position 608 --verify-position 638 --pause-seconds 0.5 \
  --min-parts 1000 --expect-renderer markwon-recycler \
  --output build/artifacts/android-chat-scroll-fix/full-up.json
```

Use `top-to-bottom` and `round-trip` for the other full traversals. For rapid
reversals near the image/detail anchor, replace `--traversal` with
`--start-position 620 --direction alternate --rounds 24`. Position verification
is outside the measured frame interval in both modes.


Final three-group results on API34 / 22041211AC (no ART sampler):

| Scenario | Group 1 | Group 2 | Group 3 | Combined |
| --- | --- | --- | --- | --- |
| Bottom to top | 111/3196 (3.47%) | 68/3566 (1.91%) | 61/3563 (1.71%) | 240/10325 (2.32%) |
| Top to bottom | 74/3542 (2.09%) | 67/3554 (1.89%) | 56/3567 (1.57%) | 197/10663 (1.85%) |
| Bottom/top/bottom | 113/7078 (1.60%) | 117/7103 (1.65%) | 131/7103 (1.84%) | 361/21284 (1.70%) |
| Rapid reversals near tools/images | 40/1785 (2.24%) | 62/1807 (3.43%) | 40/1823 (2.19%) | 142/5415 (2.62%) |

All full traversals reached both intended native boundaries. All 24 selected
anchor content/geometry comparisons passed; row/turn/part counts stayed fixed.
No additional turn presentation builds, Markdown parsing or segment
reconciliation occurred during measurement. The sampled mounted view count
peaked at 211. Full traversal P99 ranged from 24–31ms; rapid reversal P99 was
27–40ms. The full traversals and rapid reversals are below the requested
approximately 4% janky-frame threshold. The separate dense-tool single-direction
interval remains 5.17% and is explicitly not hidden by the whole-chat average.

The final build passed 198 targeted Android tests, 18 Python probe tests,
assembleDebug and lintDebug. Device reports, exact frame counts and the APK
SHA-256 are retained in `build/artifacts/android-chat-scroll-fix/standard-results.md`
and `cross-turn-verification.json`.


### Production Default Verification

Markwon is now the default for every build. The debug preference defaults to
true and retains explicit old/new comparisons; release ignores the preference
and `spool_markwon` intent entirely. The separate performance dump remains
restricted to debuggable packages. The former runtime field `markwonTrial` is
now `useMarkwonHistory`.

The change passed 395 Activity/Markwon/probe regression tests on API26/35, the
production R8/resource-shrunk `assembleRelease`, `lintRelease`, debug and
verification lints, and compilation of the debug device instrumentation.
The ARM64 release package check preserved all 263 syntax assets and JNI names,
verified 16KB native alignment and found no checked unresolved API26 methods.

A non-debuggable `.releaseverify` variant was installed on API34 / 22041211AC.
It starts the actual MainActivity with a synthetic chat, no Internet permission,
and both the saved legacy preference and intent override set to false. It
confirmed the default Markwon renderer, Markdown and code-block text, and a
committed frame; `spool-performance` returned no probe data. The owned package
was removed after the check. This variant inherits release optimization and
resource shrinking but retains the Activity members used by fixture injection;
production has neither that fixture code nor those additional keep rules.
Detailed results are in `build/artifacts/android-markwon-release/verification.json`
and `device-smoke.json`. This verifies release startup/linkage/rendering, not
another end-to-end benchmark of the user's authenticated production installation.

## Authorization history and recycled Markdown layout (2026-11-08)

The authorization-history scenario contains 9 turns, 88 transcript parts and
46 virtual rows, with one tool process expanded. On the physical 22041211AC
(API34, 120 Hz), its initial fixed-interval reversals measured 5.12% janky frames
and its full bottom/top/bottom traversal measured 4.76%.

Sampled attribution found two avoidable costs. Footer transfers reconstructed
authorization rows, including avatars, text widgets and line-height setup.
`NativeTurnActions` now rebinds those widgets and retains at most four detached
rows per container. Empty turns keep a hidden audit container so a later footer
transfer can reuse it. Changed operator metadata, timestamps and decisions still
update; stale asynchronous avatar decodes cannot overwrite a different operator.
An idle `NativeMessageFooter` also skips redundant copy-feedback resets.

Ordinary Markdown recycling cleared a full-width `TextView`, synchronously
constructing an empty text layout before rebinding and measuring the next block.
`NativeMarkwonTextView` uses a wrap-content layout parameter to defer that work,
then measures at the full available row width. Cached text is still released on
recycle, background precomputation remains active, and Markdown retains its
original full-width geometry. This does not introduce a per-conversation view
cache or share selection state between holders.

The final APK was installed and tested without method sampling. Each full
traversal reached both native boundaries, with four flings per direction; each
reversal run used 12 alternating flings from row 32:

| Group | Bottom → top → bottom | P99 | Reversals | P99 |
| --- | ---: | ---: | ---: | ---: |
| 1 | 30/791 (3.79%) | 44 ms | 33/1941 (1.70%) | 25 ms |
| 2 | 26/804 (3.23%) | 32 ms | 41/1934 (2.12%) | 28 ms |
| 3 | 25/801 (3.12%) | 42 ms | 73/1847 (3.95%) | 38 ms |

Weighted totals are **3.38%** for full traversal and **2.57%** for reversals.
All 12 content/geometry comparisons passed: rows 0/16/32 on each full traversal
and row 32 on each reversal run. Each measurement checked the focused Activity,
selection epoch, part/row counts and scroll boundaries. Two setup attempts were
aborted before obtaining valid measurements and are excluded. The same chat was
re-entered before the resumed third group; its scenario was checked again.

The change passed 91 targeted regressions, including API26/34/35 cases for audit
row reuse, avatar ownership and deferred Markdown layout. Existing native text
geometry, links, selection, streaming, short-chat positioning, scroll-anchor and
copy-feedback checks passed. Debug/release lint, debug assembly and R8-shrunk
release assembly passed; release keep rules preserve the XML view constructor.
Physical performance numbers here are from the debug package and still include
occasional slow frames.

The final build hash, individual counts, method-sampling limits and excluded
intermediate runs are recorded in
`build/artifacts/android-audit-code-scroll/audit-report.md` and
`audit-verification.json`. Only `audit-layout-round-trip-1/2/3.json` and
`audit-layout-reversals-1/2/3.json` represent this final APK. Earlier row-reuse-only
runs and the discarded `code-final-*` width experiment are not final results.

## Markdown link touch handling (2026-11-08)

Selectable Markdown TextViews install Android's `ArrowKeyMovementMethod` during
inflation. Markwon only supplies its default link movement method when no
movement method exists, leaving file links and URLs styled correctly but inert
to a normal tap. `NativeMarkwonHistory.configureText` now explicitly installs
`LinkMovementMethod` on each empty Markdown view, matching `NativeMarkdown`.
Deferred layout and background precomputation remain in place.

The regression first reproduced a real down/up gesture that dispatched no file
action. API26/34/35 tests now dispatch touch events through the RecyclerView for
file links, URLs, bare URLs, image links and citations, including recycled rows
and changed reference targets. They also check that dragging from a link scrolls
without navigating and that selected text can still be copied. Activity-level
touch tests verify the browser intent and the selected chat's file preview with
its requested line. Earlier tests that invoked span callbacks directly did not
cover this failure.

All 84 related tests, debug/release lint and debug/release builds passed. The
installed debug APK was checked on API34: tapping the previously inert URL
opened Chrome, and tapping an existing file link opened the file sidebar with
the Markdown source loaded. Build hashes and results are retained in
`build/artifacts/android-markdown-link-touch/verification.json`.

## Table interaction and width review (2026-11-08)

Three additional defects were reproduced with native drawing and touch tests on
API26/34/35. Markwon's table spans move visible cell text into private layouts,
leaving nonbreaking-space placeholders in the TextView document. As a result,
ordinary link handling cannot reach cell links and copying selected table text
returns placeholders. A 48-column table at a 360px phone width also threw
`IllegalArgumentException: Layout: -1 < 0` while drawing because equal-width
columns became narrower than their padding. The parse fallback cannot catch an
exception raised later during drawing.

The RecyclerView now sends a top-level block containing any table, including a
table inside a quote or list, to a recycled native Markdown holder. It binds the
already parsed AST and a source slice local to that block. Resolved references
outside the slice remain in the AST; binding does not parse Markdown again or
retain the whole answer's source. Other blocks keep the Markwon path. Native
table cells provide real selectable text, normal link touch handling and
horizontal scrolling for columns that do not fit. Recycling clears selection,
copy feedback and horizontal offsets before the holder changes owners.

The 121 related regressions passed, including 21 new API26/34/35 cases for actual
cell-link touches, copying cell text, wide-table drawing/scrolling, resolved and
bare URLs, unchanged streamed tables, nested tables, reading anchors and
recycling. Debug/release lint completed without errors; debug, R8-shrunk release
and isolated release-verification APKs built successfully. The non-debuggable,
R8-shrunk release smoke also passed on the API34 22041211AC phone over Wi-Fi
ADB: native table cells rendered as selectable text, answer code and file source
contained syntax-color spans, and a frame committed. Release ignored the legacy
renderer overrides and blocked the debug performance entry point. The isolated
verification package was removed after the check.

A bounded device scroll regression used the current foreground chat with 36
turns, 2,382 parts and 445 rows after the standard tool/image expansion. The
earlier exact chat title was not uniquely available in the current list. From
row 220, 12 alternating flings at 1.25-second intervals, without method sampling,
rendered 1,980 frames with 46 janky frames (2.32%); P50 was 9ms, P95 17ms and P99
30ms. Content/geometry digests matched before/after at rows 0 and 220; parse
errors stayed at zero. This one interval is not a repeat of the earlier
three-group full traversal benchmark or a performance claim about all chats.
Tables are recycled as blocks, not as individual rows, so a single very large
table can still incur work when its cells are first created and measured.
Reports are retained in `build/artifacts/android-markwon-review/`.

### Table entry stalls in the contract chat (2026-11-08)

The user reopened `梳理合同数据与录入时点` and reported pauses when tables
entered the viewport. This is a different fixture from the 36-turn interval
above: it has 19 turns, 3,247 transcript parts, 191 rows and 16 tables containing
9–39 cells each. Its pre-fix full bottom/top/bottom traversal measured 100/6,574
janky frames (1.52%), P95 19ms and P99 44ms. The low average concealed individual
binding stalls: the process had observed a Markdown bind of 108.64ms. A first
attempt with a small table cache still recorded a 95.14ms table bind and did not
establish a frame-rate improvement.

Native table documents now have a bounded cache (16 blocks, 384 cells, 65,536
source characters). While the RecyclerView is attached, visible and idle, the
nearest eligible plain table prepares one cell at a time on the UI thread, with
a 2ms work budget and a yield between slices. Preparation pauses during scrolling,
pending adapter updates and Markdown parsing. This moves selectable-cell creation
out of the first visible bind without creating Android views on a worker thread.
The final binding uses the same parsed AST, resolved links and cell text buffers.
Changed/removed blocks discard their cached documents; disposal releases all
preparation and cached views. Very large tables beyond the cache budget and
tables nested in quotes/lists retain the normal on-demand binding path.

Unchanged table identity skips AST reconciliation. Column intrinsic widths are
retained until cell bindings change; allocated widths are recomputed when the
available width changes. Unchanged rows keep their measured cell geometry, and
one table layout no longer invokes every row's cell layout twice. Existing
fractional-width, CSS baseline, selection/copy and reference-update tests remain
part of the regression set.

The final 140 tests passed on the configured API26/34/35 combinations, with no
failures, errors or skips. Debug/release lint had zero errors, and debug,
R8-shrunk release and isolated release-verification APKs built successfully.
New tests cover retained cell documents, idle preparation with actual link
touches and source copying, bounded cache size, stale-reference replacement and
cache disposal.

Three final physical-device groups each performed a complete bottom/top/bottom
traversal followed by 12 alternating flings from row 20 near the largest tables.
All used the same 191-row disclosure state and 1.25s fling intervals without
method sampling. Position verification and settling allowed idle preparation
before the measurement; this does not measure immediate scrolling on a cold
chat entry.

| Group | Full traversal jank | Table-area reversal jank | P95 (both) | P99 traversal / table area |
| --- | --- | --- | --- | --- |
| 1 | 100/6,757 (1.48%) | 21/2,029 (1.03%) | 17ms | 31ms / 25ms |
| 2 | 103/6,812 (1.51%) | 13/2,093 (0.62%) | 17ms | 28ms / 26ms |
| 3 | 96/6,837 (1.40%) | 21/2,002 (1.05%) | 17ms | 26ms / 26ms |

All 15 visible-content/geometry checks matched, every full traversal reached
both native boundaries and parse errors stayed at zero. All 16 tables were
prepared; maximum table binding was 1.92ms across these groups. Weighted jank
was 354/26,530 (1.33%). The full-traversal jank average remained near the baseline;
the clear change is the smaller table-binding spike and P99, rather than a
demonstrated large reduction in average jank. The final non-debuggable R8 release
device smoke also passed, including selectable cells and answer/file syntax
highlighting; its isolated package was removed. Reports and per-group data are in
`build/artifacts/android-markwon-review/table-performance-report.md` and
`table-final-summary.json`. The debug app was restored to the 39-cell table.
