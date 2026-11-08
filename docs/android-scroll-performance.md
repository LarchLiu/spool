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

## Markwon Recycler Trial

A debug-only opt-in now renders assistant answers with Markwon 4.6.2's official
`MarkwonAdapter` / `SimpleEntry`, composed with the existing native user rows,
activity disclosures, approvals, actions and message footers using AndroidX
`ConcatAdapter`. A single `RecyclerView` is the scrolling surface. Markdown
blocks are recycled across answers rather than mounting a whole turn inside a
`NestedScrollView`. The GFM tables, task-list and strikethrough plugins are
enabled; tables use Markwon's span renderer. File citations retain their native
file action through a parser/visitor extension and all links use the existing
message-link router. Images do not introduce an automatic remote image loader.

The trial uses the existing CommonMark 0.24.0 classes. The obsolete
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
holder. The trial uses a synchronous TextSetter with the public AndroidX
`TextViewCompat.setPrecomputedText` API instead. See the [official implementation warning](https://github.com/noties/Markwon/blob/master/markwon-core/src/main/java/io/noties/markwon/PrecomputedTextSetterCompat.java).

Enable the trial on a debug APK, preserving app data:

```sh
sh android/tools/run.sh adb -s DEVICE_SERIAL shell am force-stop com.cloudgeek.spool.android.dev
sh android/tools/run.sh adb -s DEVICE_SERIAL shell am start \
  -n com.cloudgeek.spool.android.dev/com.cloudgeek.spool.android.MainActivity \
  --ez spool_markwon true
```

The setting survives app restarts in the debug package. Repeat the commands with
`--ez spool_markwon false` to select the existing renderer. The intent option is
accepted only when the package is debuggable. The existing renderer remains the
release default while this trial is evaluated.

The performance dump identifies `renderer: "markwon-recycler"` and reports
block count, created/bound/recycled holders, bind times, parse time, parse errors
and `renderPending`. Settled-history measurements wait for both network history
and background Markdown parsing. RecyclerView scroll range/offset are native
estimates based on visible item sizes; they are not directly comparable with
the previous renderer's exact document pixel coordinates.

This is a performance and interaction trial. Markwon's typography, table
wrapping and code-block presentation are not the existing Web raster contract;
the current trial does not add code syntax highlighting to the Markwon blocks.
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
