# Spool Android Native Workspace

Android 8.0+ native views following the Web client's mobile layout and palette.
The phone does not run Spool Server or local agents, and does not load a remote
HTML document or bundle React in a WebView.

Tool bodies are now fetched only for expanded details, with matching bounded
pages/caches on Web and Android. See [lazy tool protocol and performance
verification](../docs/tool-details.md). The updated Go server is required;
building the Android client alone does not slim an older server's payloads.

For content-free, foreground-targeted ADB fling/frame/method measurements, see
[Android scroll diagnostics](../docs/android-scroll-performance.md). Method
sampling is for hotspot attribution, not an uninstrumented frame-rate baseline.

## Interface And Transport

- Native sign-in, project/recent-chat drawer, transcript and composer.
- Agent/model and reasoning-effort selection from the server catalog; permission
  modes come from the authorized project's catalog. Bypass modes require confirmation.
- Live chat snapshots/turns over SSE; navigation, profiles, presence and people
  messages over a native WebSocket. Chat SSE and REST polling pause in the
  background; the workspace WebSocket stays connected, reports visibility and
  retains presence. Returning reuses a healthy socket; stale/failed connections
  recover through the existing watchdog/retry policy. Android may suspend network
  access or reclaim the process in the background. Failed writes are not
  automatically retried.
- Foreground return verifies a retained workspace socket with a protocol
  ping/pong bounded to three seconds. A failed probe reconnects immediately;
  focus callbacks retain an in-progress handshake, and background retry backoff
  resets on return. Socket recovery/probes run independently of ordinary API
  requests. During a transient disconnect, the header keeps the last avatar
  list until the server's next presence snapshot; messaging waits for that
  confirmation. Logout, user/server changes clear the retained list.
- Native document picker and bounded multipart uploads (6 files, 4 MiB each,
  16 MiB total); native project-file browsing and text/image/PDF previews.
- Native [Word/Excel previews](../docs/office-preview.md) for `.docx`/`.xlsx`,
  parsed by the Go server. Word shows paragraphs, images and tables; Excel has
  worksheet selection, 50-row pages and horizontal scrolling. Update the server
  together with the APK. Legacy `.doc`/`.xls` files remain download-only.
- Both mobile sidebars fill the available screen width. The file sidebar follows
  the latest session turn's directory, including registered worktrees. When
  multiple checkouts are available, a dropdown after the directory name switches
  the file tree, previews and downloads; a new native turn resumes following its
  directory. A single checkout has no dropdown. The header's information button
  shows the selectable path, checkout type, branch
  or detached commit. Metadata refreshes on chat updates, focus/foreground return,
  manual refresh and every ten seconds while the file panel is open. Polling
  pauses in the background. Switching chats or directory paths clears the old
  tree, search and file selection; late responses cannot repopulate them.
- Referenced external files use the authenticated session preview/image/download
  APIs and show an external-file label. The server's project setting and chat
  authorization still decide access on every request; external parent directories
  are never expanded. New chats without a session use the project file APIs.
  File image caches distinguish directory scopes, so switching worktrees cannot
  reuse an image from the previous checkout. Source stays selectable while
  native highlighting loads or fails, and horizontal gestures scroll long lines.
- The palette icon immediately before the sidebar close button opens device
  appearance/language preferences. Theme tiles and the language listbox remain
  local and reusable; choices update the dialog and workspace immediately
  without reconnecting. Opening either settings dialog retains the sidebar; theme
  and language updates preserve its expanded state and scroll position. Each server tab's footer shows its
  own profile, Settings, and sign-in/sign-out actions. Settings contains only
  the selected server's name, enabled state, network entries and desktop action.
  Explanations and connection diagnostics open from title information icons.
  Normal
  workspace headers contain only chat/project information and workspace actions:
  no server origin, connection bar, latency, or node state.

```text
Native Android views
    -> gomobile API / SSE / WebSocket boundary
    -> Go HTTPS client using the selected connection mode
       -> Public HTTPS: system DNS and TCP, without an embedded node
       -> Tailscale: pinned peer IP and tsnet's TCP netstack
    -> selected server's HTTPS listener
```

Use **+** at the end of the sidebar server tabs to add a named server. Tabs
include disconnected/disabled servers so their settings and sign-in remain
accessible. The selected tab's footer Settings edits only that server, including
its name and whether it may connect. Each server can have a **Public HTTPS**
address, a **Tailscale (tsnet)** address, or both. Click **Connect** on
the desired entry. The choice is saved separately for each server and restored
on restart. Both entries share one card, with separate Connect/Disconnect actions
and a Tailscale sign-in icon at the right of its title. Delete server is at the
bottom of the selected server's Settings, including when it is the last server.
Public HTTPS accepts the same origin
as the PWA (for example `https://chat.example.com`, with an optional port and no
path). Its reverse proxy must support WebSocket and unbuffered SSE, as described
in the [Web/PWA setup](../README.md#webpwa-and-https).

Enabled servers retain independent connections, sessions and embedded nodes.
Each server has exactly one live network entry. A network handoff closes the old
client/node before opening the replacement; if the replacement fails, recovery
tries the next configured entry without keeping two routes alive.
Tabs below the sidebar logo row share a rounded background track with small
rounded active segments. They select the workspace to display without closing
other servers. Each connected, signed-in server keeps a workspace WebSocket while
the app is in the foreground, including inactive tabs. Navigation, agent and profile
pushes update only that server's memory cache; inactive tabs also use a batched
REST fallback while their stream is unavailable. Their subscriptions pause when
the app leaves the foreground and resume with fresh snapshots when it returns.
Only the displayed server owns chat SSE and the desktop viewer. No background
service or notification behavior is added.
Selecting a tab transfers ownership of its existing workspace subscription without
closing either server's WebSocket. Visibility controls change in place, and decoded
frames in flight still update the cache belonging to that live server/socket.
The window, sidebar and tab strip stay mounted; hidden chat transcripts are built
only when a conversation is opened. True transport teardown runs away from the UI
thread. Tab selection also retains the bounded local avatar cache.
Startup and server selection open the sidebar. The current server's footer shows
a monitor icon before Settings when authorized remote desktop is available.
Availability snapshots and changes arrive on the workspace WebSocket, scoped to
that authenticated server; closing or replacing the socket clears the entry.
The footer retains a per-server display-only account (name and avatar) across
disconnect, startup, reconnect and retry. Its action connects/retries instead of
asking that user to sign in again. This cache carries no session token, CSRF or
permissions and never authenticates workspace requests. Explicit logout or an
authentication rejection clears it; network errors and failed/malformed session
reads retain it. Once a starting tsnet route becomes HTTPS-ready, `/me` restores
the shared login and reloads workspace metadata independently of network status.
Network status dots reflect connectivity independently of Spool authentication.
The tsnet information popup contains only node, path, latency and HTTPS details;
Spool login and session recovery are handled separately in the footer.
Health/session polling updates the connecting page and existing settings controls
in place. An open information popup remains anchored and refreshes its contents
through retries and session recovery. The Disconnect label includes the same
connection-status dot as the current server tab.
Sidebar tabs show a colored dot before each server name: green for connected,
yellow for network setup or Tailscale node authorization, red for a network error and gray for disconnected
or disabled. Explanations and Tailscale path/latency/HTTPS diagnostics open from
the information icons beside their titles and update while the popup is visible.
Theme/language preferences are separate, under the palette icon immediately
before the sidebar close button. The per-server
**Allow automatic connection switching** option permits trying the other network
only after the selected connection fails. Waiting for Tailscale login or device
approval retains the explicit choice. A healthy selected route stays
connected without opening a second route for probes. Routing never
selects another server. Manual network
selection is also available. Both routes in one server profile share one private
Spool session, validated with `/me` after each handoff. Different profiles retain
independent logins, even when their addresses match. Cookies stay in Go and are
projected only onto the active transport's exact configured HTTPS origin.
One-time migration prefers the selected route's existing login, then the other
configured route. Logout persists an empty shared session so old route-specific
cookies cannot restore it. A retired transport cannot overwrite newer session
state. Tailscale node enrollment remains independent of the shared Spool login.
Server enablement and automatic network switching use native switches on the right.

Each server tab retains its own projects, recent chats, profile directory,
selection, expanded rows, pagination, scroll positions, model choices and unsent
drafts in memory. Switching tabs shows that server's cached workspace immediately,
then refreshes it in place. Reconnecting or handing off between public HTTPS and
Tailscale keeps the existing workspace visible while the network/session recovers.
Only a definitive endpoint failure clears fetched navigation; temporary network
and session-check failures preserve it. Logout, confirmed session expiry, account
changes and server deletion invalidate the affected cache. Workspace caches are
never persisted and do not authenticate APIs or enable actions before session
validation. Desktop availability and permissions are fetched from the selected
server, and changing servers or network entries closes an existing desktop
viewer. In-flight writes are revoked, never replayed on another connection.

Existing single-server settings migrate to one server named Spool and retain
its original node/session directories. There is no WebView, JavaScript bridge or
loopback CONNECT proxy. Each network entry enforces its exact HTTPS origin,
validates TLS, rejects redirects, and carries Origin/CSRF requirements. Only the
secure Spool cookie is retained in private, non-backed-up storage.

In Tailscale mode, the server address must match the selected node's full DNS name. Each new TCP
connection waits up to 10 seconds for that peer to appear in the embedded node's
Running status, then dials only its Tailscale IP through the userspace netstack.
The original HTTPS hostname remains the TLS/SNI and HTTP origin identity. There
is no system DNS or system TCP fallback, including when an Android VPN supplies
Fake-IP DNS answers or the peer disappears during connection setup.

The native layer publishes Android ConnectivityManager interface snapshots to
Go before starting tsnet and on network changes, then wakes tsnet's network
monitor so the engine can rebind/reprobe after link changes, avoiding restricted Android
netlink enumeration. Tailscale enrollment uses its official external browser
login, separately from Spool's username/password login.

A separate foreground supervisor checks the exact-origin
HTTPS `/healthz` endpoint even when Settings is closed. It recovers connectivity
without discarding the selected chat, draft, node identity or Spool cookie. See
the health/recovery policy under Diagnostics And Scope below.
In Tailscale mode it also checks backend state; in Public HTTPS mode network
recovery refreshes ordinary TCP connections without starting or resetting a node.

## Shared Native Components

`NativeUi`, `NativeTextView`, `NativeMiddleTruncate`, `NativeCssTiming`, `NativeLogin`, `NativeWelcome`, `NativeAutosizeInput`,
`NativeComposerCommands`, `NativeSlashCommandMenu`, `NativeCommandNotice`,
`NativeAtmosphere`, `NativeLineHeight`, `NativeSurface`, `NativePopupFrame`, `NativeModalFrame`,
`NativeDrawer`, `NativePopover`, `NativeDialog`,
`NativeAvatarPicker`, `NativeMessage`, `NativeMessageFooter`, `NativeMessageTime`,
`NativeTurn`, `NativeKeyedViews`, `NativeActivityList`, `NativeToolDetail`, `NativeDisclosureRow`, `NativeComposerDock`, `NativeMarkdown`, `NativeMarkdownFlow`, `NativeMarkdownListItem`,
`NativeMarkdownBullet`, `NativeMarkdownTable`, `NativeCssWrap`,
`NativeUserRow`, `NativeAvatarStack`, `NativeAgentModelMenu`, `NativePermissionMenu`,
`NativeReasoningSlider`,
`NativePatchView`, `NativeAttachmentCard`, `NativeAttachmentCards`,
`NativeImagePreview`, `NativeSettings`, `NativeAppearanceSection`,
`NativeThemeOption`, `NativeSelect`, `NativeConnectionSettings`,
`NativeFileBrowser`, `NativeFileHeader`, `NativeSearchField`, `NativeSearchCancel`,
`NativeTreeRow`, `NativeFilePreview`, `NativeFileSource`, `NativeFileSplit`,
`NativeContainedImage`, `NativeImageView`, `NativeAnimatedImage`, `NativeSpinner`, `NativeFloatingWindow`,
`NativePeopleConversations`, `NativePeopleMessage`, `NativePeoplePanel`,
`NativePeopleMenu`, `NativeGroupPicker`, and `NativeCheckbox` are reusable
native views. `NativeSidebar`, `NativeProjectRow`, `NativeChatLink`, and
`NativeChatOperators` share navigation layout and identity rendering, while
`NativeSidebarState` keeps expansion, pagination and scroll position across
drawer mounts. These are programmatically composed native views, not XML layouts
or remote HTML surfaces.
CommonMark/GFM is rendered as native paragraphs, headings, lists,
blockquotes, horizontally scrollable tables and code blocks. Message HTML is
skipped and remote Markdown images are explicit links, never automatic requests.
Markdown block flow collapses adjacent/nested CSS margins and rounds cumulative
positions, including fractional offsets through nested quotes and lists. List
markers stay outside the item box; ordered markers never wrap, zero starts are
preserved, and unordered nesting uses disc/circle/square symbols instead of a
font-dependent bullet. Tight-list/heading/table whitespace collapses while
paragraph source line breaks remain visible. Code boxes use the outer pre's
line-height strut and border-inclusive padding; horizontal rules keep their
one-pixel height. Tables share column widths, wrap at the available width,
collapse border strokes and retain fractional row positions. Default table
headers center; explicit left/center/right alignment remains unchanged.
Disabled task inputs share `NativeCheckbox` ink and expose native checkable,
checked and disabled accessibility states rather than hard-coded English labels.

`NativeGfmLinks` also recognizes literal HTTP(S), `www` and email links, retains
balanced URL parentheses, and excludes terminal punctuation. It reads original
CommonMark source spans so escaping and entity handling do not silently change
the displayed link or its destination. Explicit/nested emphasized labels do not
gain overlapping automatic click spans; code and summary previews never gain
links. Unicode URI characters are encoded without decoding existing path/query
escapes. `sync-web-markdown-links.mjs --check` compares 42 text/range/destination
vectors generated by executing the actual Web MarkdownMessage, including local
files, images-as-links, nested emphasis, raw HTML, invalid protocols and encoded
URLs. Native tests exercise both themes on API26/35, including click callbacks
and primary-colored native clickable spans. This is not glyph-raster parity.

User-clicked handoffs support the Web's HTTP(S), mailto, IRC(S) and XMPP schemes
through a separate `NativeExternalLink` policy; the strict Go/API origin policy
is unchanged. Control characters, malformed UTF-8 escapes, credential-bearing
URLs, executable protocols and arbitrary Intent/file schemes never reach another
app. Control characters and credential-bearing URLs are intentionally stricter
than an ordinary Web href and are identified separately in the reference
vectors; malformed UTF-8 also stays inert as in the Web renderer. Project-local
links still use authenticated native file APIs, and Markdown images never load
on their own. File-preview Markdown uses the same link components and policy.
The native-only confirmation dialog has been removed: the Web's real external
anchors open immediately rather than adding another in-app modal. Generated
reference vectors now also retain each actual anchor's `href`, `target` and `rel`.
API26/35 Activity tests invoke production Markdown spans, check `_blank` plus
`noopener noreferrer` against the Web renderer, and verify exactly one matching
`ACTION_VIEW` / `CATEGORY_BROWSABLE` handoff with no dialog or Go request. Rendering
alone never dispatches an Intent. Stopped/destroyed and revoked workspaces cannot
launch; foreground read-only/posting states keep links usable without replacing
the draft or selection. Tailscale login retains its separate pre-authentication
browser route and the original HTTPS policy. These tests shadow Intents rather
than opening real external apps; physical app availability remains unverified.

At the earlier link checkpoint, the suite executed 419 Android tests in 56 suites: 417 pass and the two
API26/35 exact table-line-content tests still fail; neither failure is skipped.
Both APKs and lint complete via `--continue` (0 lint errors, 66 warnings per build
type), while the combined build correctly exits with failure; 36 host tests pass.
Three additional English/Chinese/
Japanese rendered-link cases extend the existing layout gate to 28,658 values,
with its original <=1px limit. `compare-markdown-links.mjs` produces an unscaled
Web/native diagnostic contact sheet from the complete reference images; it is
never a raster-gate input. Visual inspection still shows differences in underline
baseline placement and text rasterization, so neither the link semantic gate nor these
new geometry cases establish CSS text-decoration or whole-app pixel parity.

`NativeMarkdownLink` now separates CSS decoration ink from the platform font's
default underline, while retaining the actual ClickableSpan, selectable text,
native Layout, line wrapping and clipboard text. The original CSS's 3px offset
is snapped in workspace CSS coordinates; solid `auto` thickness uses the
observed Blink rule (font size / 10, floored with a 1px minimum). Inline code links
draw the decoration over their own background and inherit the primary link
color rather than falling back to ordinary text color. Native glyph outlines
provide local skip-ink gaps for intersecting non-CJK runs; CJK runs are excluded
as with the default Web `auto` mode. This is not a claim of exact complex-script,
fallback-font, skip-ink-path or hardware rendering equality.
Wrapped decorations intersect the native selection path with the actual line's
text/space extent, instead of underlining the empty remainder of the paragraph.
Skip-ink run origins use that same per-line geometry rather than the next line's
leading caret position. Text remains an ordinary selectable native TextView.

`probe-link-decoration.mjs` renders the actual Web MarkdownMessage with the
original CSS and byte-matched test fonts at DPR1/2.625/3 in both themes. Its
17 rows cover normal/bold/italic links, adjacency, multiline wrapping, email,
Chinese/Japanese, inline code and three heading sizes. It records DOM baselines,
actual painted text baselines, font ascent/descent and CDP paint/clip operations.
New rows cover a hard break and a wrapping unspaced word. The actual Web renderer
emits both `<br>` and a newline for the hard break; paragraph `pre-wrap` preserves
the intervening empty line. Native paragraphs now preserve that visible spacing
and clipboard text too; normal-whitespace headings/lists and previews retain
their respective single-break/flattened behavior. The calculations were
cross-checked against Chromium 156.0.8078.74's `text_decoration_thickness.cc`,
`decoration_line_painter.cc` and `text_painter.cc`; production does not embed or
execute that code. API26/35 production-view tests render the same fixtures and
retain the actual document/selection through relayout. The helper's margins and
insets accumulate fractional density rather than rounding each gap separately.

Markdown paragraphs, headings, ordered markers and table text now use the
reusable `NativeUi.cssLineHeight` strut mode. It rounds ascent/descent in CSS
coordinates and floors the upper half-leading before density conversion, rather
than rounding a physical-pixel baseline computed from floating font metrics.
Used font size and line-height retain Blink's 1/64 CSS-pixel LayoutUnits before
cumulative physical rounding. This removes the former body/heading/table
fractional-height drift without changing the editor's independently audited
line boxes. The calculation was checked against `ComputedLineHeightAsFixed` and
the actual Web DOM, not inferred from screenshots.
Editable inputs and ordinary controls retain their previously verified metric
path. Checkboxes now align their bottom with the corrected text baseline; the
old compensating 1dp offset is removed. `web-line-metrics.json` is generated by
the actual Web probe, covering Chinese/English/Japanese body text and the three
fractional heading sizes, measured table rows and wrapped-link extents.
API26/35 tests check 36 struts across three densities, including exact measured
heights, and retain styled text/selection through relayout. Eighteen native
wrapped-link scenes compare actual Web line extents at <=1 physical pixel,
exercise 48 native link clicks and retain document/selection while narrowing
the paragraph. Table regressions use actual Web row heights, not a handwritten
unquantized formula. This reference is desktop
macOS Chrome in a mobile viewport with byte-matched Android test fonts; it does
not establish Android Chrome/OEM font-metric or glyph-raster equality.

Positioned Markdown flow text now passes its fractional layout remainder into
the existing `NativeLineHeight` span. Real native Layout baselines snap in CSS
document coordinates before physical rounding, rather than adding a separately
rounded baseline to an already rounded View top. The same Spannable, selection,
native click handling and View frame survive origin updates; no replacement text
painter or transform is used. `getBaseline()` retains the logical CSS strut for
inline checkbox alignment, while native Layout exposes the snapped paint
baseline. Nested rounding changes propagate even if integer frames stay equal.
An attach/detach-managed pre-draw listener handles ancestor layout moves and
converges without repeated layout cancellation. Scroll offsets are excluded from
both glyph and underline paint origins: scrolling translates the already-painted
document rather than snapping it again. This opt-in origin path covers supplied
flow/ordered-marker contexts and collapsed-table cells. Code/pre struts, ordinary
controls, editable inputs and untracked parent contexts are not claimed as fully aligned.

`text-origin-reference.mjs` executes the actual Web renderer at three densities
for 45 fractional-inset/ancestor-position scenes. API26/35 tests compare 180
painted baselines (maximum 0.5 physical pixel), exercise 90 native link clicks,
and keep document/selection identity through layout moves. A separate native
ScrollView/host-container test moves glyphs and underline together by 1/2/3
physical pixels without reflow or selection loss. That is a native software-draw
invariant, not an Android Chrome compositor or hardware-device parity claim.

Collapsed tables now pass that same fractional origin through their horizontal
scroll container and each row to the existing native cell text. Cell height keeps
the CSS 7px padding plus half of each collapsed border in floating-point units;
row edges round cumulatively instead of losing fractional padding on every row.
Short cells use the logical centered offset beside wrapped text. The correction
updates the existing LineHeightSpan and native Layout, not glyph transforms or a
replacement text painter. `table-origin-reference.mjs`, called by the Web probe,
records actual MarkdownMessage DOM boxes and CDP text paint for 108 scenes:
English/Chinese/Japanese, wrapped/short cells, DPR1/2.625/3, fractional insets and
ancestor moves. API26/35 tests compare 3,456 painted baselines (maximum 0.5px),
cell origins/heights (maximum 0.8828125px), keep every document/selection, and
exercise 1,296 native link clicks. In the original single-line subset, 1,032
baseline failures over 1px disappear and maximum error drops from 5.5px to 0.5px.
These are baseline/vertical-geometry checks, not a full table raster pass. Visual
inspection of the English wrapped fixture initially caught different per-line
text despite matching line counts and baselines. The oracle records actual
per-character DOM Range lines and native tests require their contents to match
exactly. Native cells also include their omitted half collapsed borders in the
horizontal content inset, fixing the 420dpi wrap. Before the lossless wrapping
correction described below, DPR1/3 still produced 54 mismatched lines per API
across the repeated inset/ancestor cases, and the strengthened test failed.
`probe-table-wrap.mjs` isolates actual CSS/font advances: the borderline phrase
measures 208.054504 CSS px in Web, over the 208px content width, but native Paint
measures 207.898438px at mdpi and 623.84375px at DPR3 against 624px content width.
Native therefore fits the extra word. The correction does not scale text,
change source characters, or relax the line-content assertion.
See `table-origin-wrapping-check.txt` and `web-table-wrap-probe.json`.
The probe also retains character Range widths from the actual rendered table
and independent Canvas measurements at 12.875/12.88/13px. A separate
`NativeShapingPrecisionTest` records 36 samples per API (three densities, three
sizes and four public paint-option combinations), requiring distinct native
ASCII caret positions and consistent run totals. Disabling hinting does not
change the discrepancy; dropping explicit features does not help, and disabling
linear text makes advances substantially worse. None is adopted as a fix. These
diagnostics do not replace the exact wrapping assertion or prove a complete
engine/OEM cause. See `shaping-precision-{native,web}-check.txt` and
`native-shaping-precision-api26/35.json`.
Table border/horizontal-layout/full font raster remain incomplete. The separate
CJK synthesis correction described later removes invented weight/slant for
audited static faces; it does not make these full images byte-identical.

The isolated `check-link-decoration.mjs --offset-thickness-only` gate verifies
456 values: CSS-grid-snapped baseline-relative offset and exact thickness in
228 painted-line cases, with zero error. It does NOT audit absolute text position
or glyph width. Default mode now additionally compares wrapped-line extents,
actual painted baselines, row origin and row height. Origin snapping reduces
failures from 20 to 4 in the same 228 cases: API35 h1/h2 widths at DPR3 in both
themes remain over the original 1px limit. Maximum painted-bound error is
1.3591px, baseline0.5px,
horizontal1.3591px, row origin0.5px and height0.421875px. `--raster` still FAILS
the unchanged one-channel-unit limit (596,499 of 29,187,000 full-frame pixels,
maxima210/165
light/dark). These complete images are never cropped or rescaled for the gate.
The native/Web mdpi scenes and regenerated link contact sheet were visually
inspected; heading tracking, untracked origin contexts and glyph-raster errors
remain follow-up work, not waived by the isolated decoration pass. The larger existing
28,658-value block/control geometry gate continues to pass independently.
Native diagnostics now also record font metrics, unstyled Paint advances and
actual Layout caret positions. API35's heading Layout removes exactly one full
letter-spacing unit across the two line edges; API26 does not. This explains
the remaining API35 heading-width discrepancy without introducing font scaling,
invisible document characters, replacement text drawing or relaxed tolerances.
`NativeTrackingTest` also keeps a separate API35 `PrecomputedText` control:
cached shaping matches direct untrimmed native glyph paint, but intermediate
ASCII caret positions collapse to the run end at all three densities, including
selectable DynamicLayout. Production text retains distinct caret boundaries and
the original document through resize. This control therefore does not justify
using precomputation as the heading fix; matching total width/paint alone would
break native selection and link hit testing. See `native-tracking-probe.json`.

`NativeCodeTabs` shares Web's two-character tab grid between selectable source,
Markdown code blocks and the viewport-only diff canvas. Paragraph-local native
tab spans retain original tab characters for selection/copy; canvas sizing,
emphasis and painting use the same stop calculation. Markdown preserves the
different inherited tab grids of messages, tool details and file previews,
including CSS's half-space minimum advance. Single-line measurement retains
wide tab-only indentation instead of clipping it to raw glyph widths.
`NativeSyntax` shares asynchronous coloring and lifecycle guards between source
and diff renderers; `NativeSyntaxEngine` owns the native TextMate registry.
Interface languages remain Chinese, English and Japanese (plus Follow system).
Code grammars such as Go/TypeScript are not additional interface languages.

Lucide/LobeHub/Pierre icons use the original Web SVG geometry converted to Android
vectors, not approximate redraws. `WebPalette` is generated from Web CSS,
`NativeDiffPalette` uses the pinned Pierre themes and CSS Lab mixes, and the
logo is copied byte-for-byte. Regenerate shared assets after changing Web styles
or icon dependencies:

```sh
sh android/tools/run.sh bun android/tools/sync-web-ui.mjs
sh android/tools/run.sh bun android/tools/sync-web-ui.mjs --check
```

The agent/model menu keeps both catalogs available during a chat, resets effort
using the same backend-specific defaults as Web, and resizes in place when the
selected model adds/removes the reasoning slider. Switching agents applies to
the next send; the server performs conversion/forking without changing the
original chat. Permission discovery remains scoped to the authorized project.

Tool history pairs tool inputs/results, groups adjacent actions, and preserves
user disclosures across live snapshots. Thinking previews parse complete native
Markdown before clipping. File diffs support numbered unified/Git patches and
unnumbered apply_patch input, moves, multi-file changes, empty additions and
unavailable deleted contents. Incomplete/unsupported patches fall back to their
complete selectable source. Large patches draw only visible rows instead of
creating a native view per line. Original input/output remains expandable.
`NativeDiffLayout` follows Pierre's content-similarity pairing for mismatched
change blocks, including its 4,096-comparison limit and .5-per-pair improvement
threshold; pairing never crosses context lines. `NativeWordDiff` reproduces
jsdiff's word-with-space tokenizer, Myers path tie-breaking and Pierre's
word-alt joins, preserving disjoint edits instead of shading unchanged middle
text. UTF-16 ranges retain original text, Unicode and tabs. The Web's 1,000-code-unit
intra-line limit is shared. Highlight ink follows the inline font box rather than
painting the entire 20px row. Expensive exact layout runs in the activity's code
worker after attachment, updates both panes atomically without changing their
size, and cancels on detachment; epoch checks reject completed stale jobs.
Source/diff syntax uses the same installed Web TextMate grammars and normalized
Pierre light/dark themes, interpreted by pinned TM4E 0.14.1 and native Oniguruma
6.9.10. No JavaScript engine, WASM, keyword-color approximation, Joni or Sora
runtime is packaged. `NativeSyntax` applies colors/font flags without changing
selected text or tab spans, and colors both precise diff panes atomically.
Pierre's whitespace-token merging is retained, including its exception for
underline/strike ink. Each hunk side resets multiline state and uses the proper
old/new filename on rename. Registration stays lazy; native compiled patterns
are explicitly released when switching language/theme, clearing the session or
closing the activity. Match and search retry limits bound pathological patterns;
failures retain plain source rather than hiding a file or losing diff alignment.
The Android adapter handles unavailable System.Logger, API-26-incompatible JDK
isEmpty calls and UTF-8/UTF-16 surrogate offsets. See `textmate/README.md` for the
exact transform, pinned checksums, licenses and reproducible build recipe.

```sh
sh android/tools/run.sh bun android/tools/sync-web-syntax.mjs
sh android/tools/run.sh bun android/tools/sync-web-syntax.mjs --check
sh android/tools/run.sh python3 android/tools/check-syntax-package.py --report build/artifacts/android-native/syntax-package.json
```

The pure JVM/native conformance check compares every UTF-16 unit's foreground and
font flags with 29 actual-Web fixtures in both themes. It covers embedded
languages, supplementary characters/CJK, CRLF, tabs, multiline state, unknown
filenames and the 1,000-code-unit limit. Renderer/lifecycle tests cover API 26/35,
selection/tab preservation, unchanged dimensions, detach/reattach, session clear,
stale completion, exceptions, canvas colors and paint restoration. Native and
browser source/diff screenshots additionally compare all visible character
colors/styles and horizontal positions at three widths in both themes. Grammar
exports take about 1.6 MiB compressed in the APK; they do not add UI locale choices.
This is not exhaustive grammar or full-transcript pixel parity: Web defaults to
its JavaScript regex backend, native Oniguruma can differ on untested patterns,
and optional unloaded embedded grammars can produce warnings.

The native mobile file sidebar lazily loads authorized project directories,
reuses list rows and the original Pierre standard file icons/resolver, and
searches only already-loaded entries, as Web does. Closing search restores its
previous expansion state. Selecting a transcript/Markdown file link reveals the
server-returned canonical path and retains its target line. The inline preview
shares native Markdown and image components, selectable non-wrapping source,
sticky line numbers, download, error/retry and truncation notices. Its persistent 32%
tree split follows Web's limits and supports drag, double-tap reset, keyboard
and TalkBack range actions. Project changes, refresh, logout and deactivation
invalidate stale responses; queued revoked-session reads are skipped. There is
no project-file AlertDialog or arbitrary URL loader. HTML files have highlighted
native source only, not rendered HTML/CSS.

People chat uses the original nonmodal floating layout, 72px conversation rail,
28px message avatars, asymmetric bubbles, unread badges, presence dots and
autosizing composer. Moving and all eight resize edges follow Web's viewport
limits; keyboard moves use 10px or Shift+40px steps. The header opens the same
online menu as Web mobile, including inbox, public room, group creation, private
groups and individual contacts. Group creation/invitation uses the shared native
modal and checkbox rather than system AlertDialogs. Presence/group/profile
events update open surfaces without rebuilding unchanged transcript text.
The floating shell also reuses `NativeSurface`: its 18dp corners, 1dp border and
fixed black `0 12px 44px #0003` shadow follow Web in both themes, instead of using
Android elevation or the composer's theme-dependent shadow color. The cached mask
is invalidated on resize; child backgrounds cannot square off the rounded border.

The session-only people inbox retains 300 messages and independent drafts,
deduplicates echoes, counts incoming-only unread messages and validates 2,000
Unicode code points without trimming the user's saved draft. Send errors and
late acknowledgements remain scoped to their conversation/request; late results
cannot erase a newer draft. Disconnects/timeouts report unknown delivery and
never replay a message or group operation automatically. Requests capture the
authenticated Go WebSocket/connection epoch, not a later mutable session. Closed
group pickers ignore late completions, and member choices prune as people leave.
IME composition cannot send or close a chat accidentally, and finishing
composition refreshes the send action without requiring another edit.

The project/recent drawer sorts conversations by newest activity without changing
server arrays. Recent and per-project lists independently show five records,
append ten at a time, and collapse back to five. Projects expand in five-item
pages; selecting an off-page project/chat reveals it, while metadata pushes do
not reopen a project the user collapsed. New-chat controls respect posting and
project permissions. Rows show the same local-session monitor, 1.1s task spinner,
agent vector or compact participant-avatar stack as Web. Avatar taps open a
separate full-name/member tooltip, never navigate to the chat. Live profile
updates rebuild affected identities; identical navigation retains anchors and
focus, and metadata refresh restores scroll. Closing/deactivating the drawer
dismisses tooltips and fences its stale action callbacks. Logout clears its
session-owned state. Footer avatar, name, settings and sign-out spacing match
Web mobile, including the narrow ellipsis behavior.

Draft and transcript attachments use the same MIME-based card layout, excerpt
rules, icon geometry and narrow-screen dimensions as Web. Transcript cards wrap
at the user-message width; draft cards scroll horizontally. `NativeImages`
coalesces reads and keeps a bounded 12 MiB, auth-scoped image cache. It captures
the selected Go transport before dispatch, skips queued requests after logout,
ignores arbitrary preview URLs, bounds image pixels before decoding, and applies
EXIF orientation in memory with pinned AndroidX ExifInterface, not older system
EXIF parsers. Images load only through authorized Go/tsnet file
APIs. The native preview uses Web's contained image sizing, toolbar and backdrop.
GIF and animated WebP play natively, including on API 26: `NativeImageSequence`
validates/demultiplexes their containers and uses Android's built-in raster codecs,
not another native dependency, browser or network loader. Playback composites
cropped frames with alpha/blend/disposal, respects finite/infinite repeat counts
and clamps 0-10ms frame delays as browsers do. Each visible image has its own
cursor and at most one worker decode in flight; it does not predecode all frames.
The original pixel/response limits and a 10,000-frame metadata limit apply.
Encoded animation data counts against the shared cache budget. Visibility and
viewport clipping pause playback; detaching releases frame surfaces, reattaching
restarts playback, and rebind/logout invalidates old completions. Activity teardown
drains queued surface cleanup instead of discarding those tasks. Decode time can
slow playback on a busy device; exact device-level scheduling is not claimed.

Synthetic GIF/WebP fixtures cover global/local palettes, transparent/cropped
frames, clear/restore disposal, VP8L and lossy VP8+ALPH, finite/infinite repeats
and minimum delays. Actual Chrome WebCodecs supplies all 27 complete composited
frames; API 26/35 tests require exact alpha and RGB differences no greater than
one channel value over two loops. Browser screenshot sampling separately observes
minimum-delay playback (WebCodecs exposes raw durations, not img's clamping).
Native/browser contact sheets and playback samples stay in the artifact directory.
The metadata fixture also preserves an ICC profile while following actual
Chrome animated-WebP orientation (its img canvas does not apply EXIF rotation).
The original EXIF handling for still images remains unchanged. A separate
ImageBitmap/actual-img poster oracle guards against conflating raw frame and
display orientation semantics.
The tests also cover independent cached instances, background/scroll visibility,
detach/reattach races, stale logout/rebind results, malformed/oversized containers,
bounded frame metadata and high-density intrinsic sizing. The browser check uses
Playwright, pngjs and local Chrome; `SPOOL_NODE_MODULES` can point to a tool runtime
containing those packages, as for the other Web-reference probes. Regenerate/test with:

```sh
# Use a Python environment with Pillow only to regenerate the small synthetic fixtures.
sh android/tools/run.sh python3 android/tools/image-animation-fixtures.py
sh android/tools/run.sh bun android/tools/check-image-animation.mjs
# Add --write only when intentionally updating actual-browser golden frames.
```

History files and the image-preview download action open Android's document
destination picker. Bytes stay in bounded app-session memory until the user
explicitly selects a `content:` destination; there is no silent Downloads-folder
write, storage permission, external image loader or direct-network fallback.
The Go response envelope preserves non-UTF-8 attachment bytes with base64 instead
of allowing JSON string encoding to replace them.

Behavioral fixtures evaluate the actual Web TypeScript in memory, covering
model defaults, tool classification/pairing/stats, strict patch parsing,
attachment preparation/URLs, language resolution and select-popup placement.
They also cover file-tree expansion/search/icon rules, preview kinds, relative
file links and split limits against the actual Pierre/Web implementations.
People-chat fixtures additionally exercise 14 actual-Web inbox transitions and
50 original floating-window fit/move/resize cases.
Sidebar fixtures evaluate the same extracted Web sorting, pagination and
selected-item reveal functions used by the shipped Web sidebar, rather than a
second handwritten expected state machine.
Regenerate after changing those Web modules:

```sh
sh android/tools/run.sh bun android/tools/sync-web-behavior.mjs
sh android/tools/run.sh bun android/tools/sync-web-behavior.mjs --check
sh android/tools/run.sh bun android/tools/sync-web-diff.mjs
sh android/tools/run.sh bun android/tools/sync-web-diff.mjs --check
sh android/tools/run.sh bun test android/tools/css-lab.test.mjs
```

Diff fixtures execute the installed Pierre parser, iterator and actual internal
decoration function (exported for the probe without changing its implementation).
The 263 word-alt pairs and 70 split layouts cover repeated words/punctuation,
separate edits, extended Latin, CJK, emoji, exact ECMAScript whitespace, CRLF,
tabs, length limits, alignment thresholds and the 4,096-comparison boundary.
Native conformance/lifecycle tests run on API 26 and 35. The browser's extra
`--screens=diff-layout` fixture measures original inline highlights at all three
widths in both themes; geometry uses the same test-only mono font as native.

`render-web-reference.mjs` renders original React/CSS
fixtures in a local headless Chrome (requires Playwright; `SPOOL_NODE_MODULES`
can point to its installed module directory). After that and the native layout
tests, `sh android/tools/run.sh bun android/tools/check-ui-geometry.mjs` checks
login, welcome, composer, model-menu, attachment, image-preview, appearance, file-sidebar, private/group
chat, group-picker, online-menu, navigation-sidebar, transcript-row, Markdown-block,
code-tab, diff-highlight and syntax-character values at all three widths in both
themes. This includes all eight floating-window hit regions and the 320dp wrapped
chat composer, plus navigation text, agent/operator icons, task/local indicators
and independent project/chat hit targets. Model-menu/attachment boxes match exactly; the remaining boxes
allow at most one mdpi pixel for fractional CSS rounding.
Thirteen actual-Web Markdown scenes cover all six heading levels, nested quotes,
tight/loose/three-level lists, zero/large ordered markers, mixed task lists,
code/rules, plain/wrapped tables and Chinese/Japanese blocks. Element boxes,
list-marker styles and table alignment compare with production native views.
Separate API-26/35 tests cover 100-block cumulative rounding, selection retention,
wide-marker clipping, disabled task semantics and 21-row tables at mdpi/xxhdpi.
These checks do not prove every CommonMark/GFM edge case or glyph-raster parity.
Syntax foreground/font flags match exactly; character positions allow the same
one-pixel fractional rounding, including the original tab grid.
Code fixtures additionally compare 1,200 tab positions against actual Web/Pierre
source, message Markdown, tool details, file previews and diffs, with original
contents preserved. Native checks include a selectable 2,000-line tabbed source,
scrollable single-line indentation at mdpi/xhdpi, and Canvas ink equivalence.
The login/workspace pass adds 3,288 measured values. It uses the real Web
`AutosizeTextarea`, not a plain one-row textarea, and covers empty, one/three/six
lines, overflowing content and width-dependent wrapping. `NativeAutosizeInput`
keeps native selection and composing spans, uses CSS's complete last line box,
and caps growth at six rows. A focused native input/scroll test exercises growth,
shrinkage and IME composition without resetting a scrolled history. Login and
welcome labels use the shared Chinese/English/Japanese catalogs; all three
language menus are explicitly regression-locked to those locales plus Follow
system. The multilingual tests check text and bounded wrapping, not CJK font
raster parity.

The editable-input pass also checks the text itself, not only its outer box.
`NativeAutosizeInput` applies the reusable cumulative `NativeLineHeight` span to
the existing editable document and hint, with `UpdateLayout` notifications for
real `DynamicLayout` reflow. Fractional glyph advances match the browser's line
breaks; complete first/last line leading and Web's rounded scroll-height sizing
remove the former 3px first-baseline offset and per-line drift. Empty input uses
native vertical gravity to align its real caret with the hint, without inserting
hidden text or drawing a replacement cursor. The shared `Message {agent}` key
also localizes the production Japanese placeholder instead of concatenating an
English prefix.
Run `render-web-reference.mjs --screens=editor` and `check-editor-text.mjs` through
`run.sh` to check 2,056 values across 40 fixtures, 320/360/412/900dp and API26/35.
The browser probe reads actual textarea user-agent-shadow text nodes/ranges and
used fonts through CDP, rather than recreating the input in a styled mirror.
English, Chinese and Japanese hints/text, one/three/six/eight lines and wrapping
compare at <=1px with exact text/line counts; maximum baseline difference is
0.4375px and advance difference 0.3125px. Separate native tests retain document
identity, styled spans, composing spans, selection, accessibility hint and native
commit/delete behavior across font breakpoints at mdpi/420dpi/xxhdpi. The probe
uses the already byte-verified test reference fonts, not APK-bundled fonts, and
does not prove glyph-raster, OEM-font or physical-IME parity.

The reusable `NativeSurface` draws the composer's 19dp rounded border box and
theme-colored CSS `0 8px 32px` shadow without Android elevation. Its blurred mask
is cached until the box resizes; descendants clip to the inner rounded border,
while the workspace allows the exterior shadow to overflow without adding layout
space. This removes the dark triangular wedges caused by the former clipped
platform shadow at the two bottom corners. Native tests cover both themes,
320/360/412dp, short/tall composers, and child clipping on API 26/35 at mdpi/xxhdpi.
Additional production-composer tests paint a contrasting controls background and
grow/shrink the input while resizing through 220/360/900dp on API 26/35 at
mdpi/420dpi/xxhdpi. Both bottom corners keep their rounded child clip, intact
border and symmetric exterior shadow. Child content now uses an antialiased
round-rect alpha mask instead of `clipPath`'s hard raster edge; its temporary
layers exclude the separately painted border and overflowing shadow. This avoids
jagged inner corners when controls or other descendants have a background.
API 26/35 regressions compare every bottom-corner channel with a directly painted
antialiased inner curve (<=1 unit) at mdpi/420dpi/xxhdpi while growing and shrinking
the same surface. The current corner regressions and both APK builds pass; the
two strict table-wrapping executions fail independently as described earlier.
The corner-focused exterior composer-shadow audit passes 501,864 pixels.
The latest bottom-corner-focused recheck passes all 22 `NativeSurfaceTest` and
`NativeComposerLayoutTest` executions, the 2,992-value composed layout gate and
the same 501,864-pixel shadow gate. Both APK builds and both lint tasks succeed
(0 errors, 66 warnings per variant). See `composer-corner-current-*.txt` in
`build/artifacts/android-native/`. This recheck does not install either APK or
refresh the older physical-device screenshots.
The full surface audit still reports rounded-border antialiasing differences
(maximum 14 light / 11 dark channel units). Existing device screenshots predate
this fix, and no phone installation was retried. These synthetic tests do not
prove hardware raster behavior or complete pixel parity.

The border now follows actual Blink paint commands, not an inferred pair of
nested solid fills: fill the half-border-inset round rect, then stroke that same
rect with the CSS border width. The cached outer shadow excludes the unshifted
inner padding box, including for a transparent surface. `probe-css-paint.mjs`
captures original-CSS DevTools paint commands and their independent software
replay at DPR1/2.625/3 in both themes. The existing DPR1/3 border-only native/replay audit passes all
3,412,800 pixels at <=1 channel unit on API26/35 (light is byte-exact). This is
additional command-path evidence, NOT a replacement for the original screenshot
gate: Chrome's own replay differs from its screenshot, and native-to-screenshot
composer differences increased from the previous nested-fill maximum 7/6 to
14/11. Full replay including dense shadows also fails at 48 pixels, by 2 units.
Neither observation proves unavoidable errors or actual hardware equality.
Optional `--profile=modal-image` / `modal-appearance` trace real dialog layers;
those layer replays are not compared as if they were full composited screenshots.
The modal probe additionally composes each actual layer replay at its recorded
translation, without rescaling, into an independent full-viewport diagnostic.
It rejects non-translation transforms rather than approximating them. The
360px image-modal control still differs from the original screenshot (maximum
50/20 at DPR1, 56/20 at DPR2.625 and 39/15 at DPR3, light/dark). Recorded layer
translation alone therefore does not establish a native rendering fix; neither
this control nor individual layer images replace the original screenshot gates.
`compare-composer-corners.mjs` juxtaposes raw Web/native composer crops without
rescaling; all pixel gates continue to compare complete, uncropped fixtures.
The current-source recheck after extracting the shared text component retains
all20 passing surface executions, the2,992-value composer layout gate and the
501,864-pixel exterior-shadow pass. The full-surface gate still fails at the
unchanged threshold. Current mdpi comparison and actual420dpi empty-composer
Web/native full frames were visually inspected; no install or device test was
retried. See `file-font-build-check.txt`, `file-font-regression-check.txt` and
`file-font-composer-surface-check.txt` in `build/artifacts/android-native/`.
The subsequent file-row focus pass retains the same corner and composer checks;
its current evidence is `file-focus-build-check.txt`,
`file-focus-final-test-check.txt`, `file-focus-regression-check.txt` and
`file-focus-composer-surface-check.txt`.

The production composer now also uses `NativeSurface.setCssBorderBox` after
layout, snapping paint in workspace CSS coordinates without moving or scaling
the real editor or controls. At 420dpi, its previous integer View edges were
32/913px instead of Web's 31.5/913.5px, and its bottom was 428px instead of
427.875px. Independent full-viewport production-composer fixtures now verify
all four painted edges and radius on API26/35 at DPR1/2.625/3. Additional tests
move the composer and its workspace padding without replacing the Editable,
losing selection or applying child transforms. The bottom-border regression
samples the CSS stroke center rather than assuming the last integer View row
is fully covered; the full-image <=1 channel-unit gates remain unchanged.
`check-css-paint.mjs --composer` audits these production fixtures separately
from the earlier independent surface fixtures. At DPR2.625 the screenshot
maximum improves from 26/15 to 14/8 light/dark; failing pixels drop from
3,672/3,634 to 2,025/2,111 on API26 and 2,031/2,112 on API35.
All paint-coordinate errors are now zero,
but full screenshot and shadow-inclusive replay still fail (the latter has
81 pixels differing by 2 units across all densities/APIs). Existing DPR1/3
composer error counts and maxima are unchanged. The added DPR2.625 reference
uses a 240px-high viewport, integral at 21/8 scaling; neither image is cropped or rescaled for
the gate. `compare-composer-corners.mjs --density=2.625` creates an explicitly
diagnostic Web/before/after close-up. This is not a hardware or whole-app pass.

The latest composer border uses reusable `NativeRoundedBox` analytic coverage
instead of Canvas software round-rect tessellation. Only the four corners store
two-dimensional pixels; flat edge/center tiles retain constant coverage without
a full-surface bitmap. Background and border blend separately, matching the
original CSS paint order. Geometry/color/stroke changes invalidate the bounded
cache, and detachment releases references without recycling RenderThread-owned
bitmaps. Other `NativeSurface` users retain their existing painter until audited.
`probe-css-paint.mjs --write-border-fixtures` retains unmodified full-viewport CSS
screenshots plus source/image hashes. API26/35 compare every channel of all six
border-only frames at DPR1/2.625/3: 5,794,200 pixels pass the unchanged one-unit
limit. The independent, original shadow-inclusive composer gate still FAILS:
maximum2 with14 pixels outside tolerance across both APIs/densities/themes,
down from maximum14. The three-width/two-height full-surface gate also remains
failing at maximum2. No crop, supersampling, text scaling or threshold change is
used to pass a gate, and physical-device rendering is still unverified.

The full 420dpi composer is now checked with real text and controls, not only
the isolated surface. `NativeComposerBox` reuses `NativeSurface` and accumulates
the CSS border, editor and controls heights before rounding native layout
edges. Its content width follows the CSS-snapped border rather than subtracting
two independently rounded physical border widths. `NativeAutosizeInput` rounds
the real line-count/scroll-height size in CSS pixels before device scaling;
the header and separate divider also round their combined 50px border box.
This fixes cumulative 1-2px input/control/history offsets without transforming
glyphs, replacing the editor or weakening any geometry/pixel limit.

Visual inspection also caught early model-name ellipsis: converting the
remaining physical label width back to integer dp discarded another pixel.
Choice buttons now retain the actual physical label room, use a ceiled
single-line glyph advance, and keep the original CSS max-width. Full model
names remain visible where Web shows them; constrained narrow-window names
still ellipsize. `render-web-reference.mjs --screens=composer-layout` renders
actual Web/AutosizeTextarea scenes; `check-composer-layout.mjs` compares 2,992
geometry/semantic values on API26/35, both themes, all three locales and live
empty/six-line/overflow/keyboard/narrow/wide/restored transitions. All pass
<=1 physical pixel with exact text, visibility and actual ellipsis state.
The two new native executions render 44 full production-view fixtures and
preserve the Editable/selection during resize. These are synthetic window
sizes, not physical IME events, full-image pixel parity or device evidence.

`NativeComposerControls` reproduces the
two CSS flex groups instead of using a weighted spacer: narrow windows shrink
the model control proportionally and retain the send button's right inset.
The same input instance adopts Web's <=600dp phone and wider-window padding and
typography without replacing text, selection or IME composition.
`NativeComposerCommands` owns the exact `/`, `/g`, `/go`, `/goa`, `/goal`
completion prefixes, the original Crosshair icon and Tab shortcut, localized
goal hint, inline errors and selectable/180dp-scroll-capped result notice.
Its popup retains editor/IME focus, follows the live composer bounds and closes
on blur, detach, posting/running/read-only state, outside dismissal or Escape.
Tab/Enter select the command; arrows stay in the menu; Shift+Enter remains a
newline; Meta/Alt+Enter sends only when writable and idle, never stops a turn.
IME composition cannot complete a command or send accidentally.
The shared normal-line fallback uses the configured CJK font metrics, preserving
the original Chinese/Japanese menu/detail/hint heights instead of hard-coding
locale offsets. Text labels keep their intrinsic CSS grid width, not a weighted
full-width column. A CDP font audit rejects uncontrolled host fonts in the Web
reference, including the result's mono/CJK fallback.

Existing-chat `/goal` view/clear actions use the existing Go transport's
`POST /api/chats/{id}/goal`, not a message turn; a new local chat reports No goal
set without creating a chat or issuing a request. Goal objectives stay on the
normal messages endpoint. Attachment/code-point validation, backend mismatch,
late selection/draft replies, inline failures and session expiry are guarded.
Queued generic API work now checks its captured generation/session before
dispatch, so revoked-session commands do not issue a later write.
Activity tests run the actual send/API/callback code with only the gomobile
client boundary shadowed; they are not real tsnet/server integration tests.

Ordinary submission captures an immutable draft and immediately shows the
optimistic user message/attachments, while retaining the same writable editor
for the next draft. `NativeChatState` ports the Web reconciliation and running-turn
rules; its fixtures are generated from the actual Web state helpers.
`NativeSubmission` tracks the captured selection, adopted chat ID and one deferred
stop request. A stop during submission waits for the returned server turn ID,
never sends a local pending ID, and deduplicates repeated clicks. Acknowledgement
scrolls the transcript without moving focus away from the editor.
`NativeUi.IconButton` shares the send/stop/spinner geometry (20/14/18dp), retaining
the original filled-and-stroked stop icon and rotating only the spinner glyph
over 800ms. The Web side uses the reusable `ComposerSendButton`.

`MainActivitySubmissionTest` runs the production Activity request/callback path on
API26/35, including real input-connection edits during posting, attachments,
new-chat/fork adoption, pending/direct stops, errors and revoked queued requests.
The send-button reference gate checks 9,504 geometry, semantic, color and sampled
rotation values against the actual Web component at three widths, both themes
and all three UI locales. It does not prove icon raster or device frame timing.
Failed sends now follow Web's existing rule: restore the exact submitted text
(including whitespace) and attachments, replacing any text typed while posting.
An existing/adopted chat retains its current live state until the authoritative
recovery GET finishes; it is not first rolled back to a pre-send snapshot. Only
failed creation of a still-local chat clears the optimistic transcript outright.
Recovery errors leave the original send error and newer live state visible;
selection/session guards prevent stale callbacks from changing another chat.
The captured submission also supplies local bytes to any retained optimistic
attachment cards until their turn is replaced, even after the POST settles or
the restored draft attachment is removed. API26/35 regressions cover these
paths, actual SSE callbacks, invalid acknowledgements and new-chat/upload
failure. Broader provider/server races and hardware/IME behavior remain unproven.

The actual sidebar can open another existing conversation while posting, as on
Web; new-chat creation remains disabled until the submission settles. The target
editor stays read-only until its ID-matched chat loads for an authorized project
and authenticated user. Loading failures appear inline only for that selection.
Submission callbacks follow Web's user/chat identity rule, so leaving and
returning before acknowledgement updates the current draft, never a detached
draft instance. Hydration reconciles newer acknowledgements and updates existing
Views without replacing a focused editor, selection or composing span. Clicking
the already selected chat simply closes the drawer. Fourteen API26/35 production
sidebar/Activity cases cover these routes, stale replies/errors, background
first-chat adoption, readiness and pending new-chat controls. These are local
transport-boundary tests, not device navigation or actual server integration.

Composer draft lifetime now follows the actual Web selection effect rather
than retaining a separate unsent draft for every agent chat. Switching chats,
explicitly creating another new chat (even in the same project), removing the
selected project or revoking the session discards the old text/attachment list
and pending picker ownership. Selecting the already active row, opening/closing
Settings, or adopting the first-send/fork server ID preserves the current draft.
The immutable pending submission still owns its submitted bytes; returning to
the original chat before a failed send restores the submitted text/files there,
as on Web. People/inbox per-peer drafts are a separate unchanged feature.

Prepared document attachments check the captured user session, project, chat
selection and explicit-new-draft version before adding to the current draft.
Navigating away and returning to the same selection before preparation finishes
uses its current draft, not the abandoned Draft instance. A fresh new-chat draft
in the same project does not receive the old preparation result. Canceled or
revoked native picker results drop their ownership immediately.
Eight new API26/35 Activity tests (16 executions) cover these rules using the
production sidebar, real composer/cards and in-memory ContentResolver reads;
existing send/adoption tests also retain newly typed text/files through an
acknowledgement and restore submitted attachments after return-and-failure.
`check-web-composer-selection.mjs` executes the actual Web reset block (8 paths)
and actual asynchronous `addFiles` function (7 guards), with stubbed preparation,
without changing Web production sources. This is not mounted React, provider
permissions, physical-picker or real-server/device verification.

`NativeChatRefresh<T>` supplies the Web running-chat terminal-frame fallback:
read once immediately, then every second while any turn is running. It uses the
existing native Go API channel, not a WebView or a connection-status banner.
The shared Web `hasRunningTurn` helper and generated state fixtures lock the
predicate, which is deliberately different from the last active/stoppable turn.
Snapshot timestamps/turn identities reconcile through `NativeChatState`; matching
navigation rows update as well. An unchanged or older snapshot retains the actual
native message Views, input document/selection and scroll rather than rebuilding
them every second. Stop acknowledgements also update their matching sidebar row
even if another conversation is now selected.

The reusable refresher preserves the one-second cadence independently of slow
reads, so responses may arrive out of order just as on Web. Binding the same
scope does not start extra timers. Navigation, terminal state, session changes,
backgrounding and destruction revoke both queued reads and late callbacks;
foregrounding restarts immediately when still running. Locale changes rebind
the captured request context. Ordinary read failures stay quiet, while a current
401 expires the session. Six pure scheduler tests and eighteen API26/35 Activity
cases cover cadence, overlapping/out-of-order reads, terminal SSE/REST races,
sidebar state, focus/composition, pause/resume, revocation, malformed responses
and locale changes. `native-refresh-{light,dark}-{running,completed}.png` retains
synthetic production-view frames with inert avatar loading, not phone or Web
pixel-parity evidence. Physical network scheduling and OEM lifecycle behavior
remain unverified.

`NativeWorkspaceData` now matches the audited Web workspace-loading rules over the existing Go
transport: `/navigation` and `/agents` complete as one batch; a newer navigation
push fences the whole delayed REST batch. Only one batch/profile read is pending
per live session. A fifteen-second fallback reads while the workspace socket is
not ready, and missing participant/operator/action/presence identities trigger
one debounced fifty-millisecond `/profiles` lookup, never one lookup per avatar.
Socket retries no longer launch extra navigation/profile batches between those
fallback ticks. Background workspace reads use a separate three-thread executor, so
three slow data requests cannot occupy the send/stop executor.
`NativeProfiles` fences HTTP results on every accepted push/save/reset, including
directory-membership changes at the same profile revision. Session, foreground
and locale scopes revoke queued reads and late replies; an already running Go
request can still finish on the transport before its result is discarded.

`NativeNavigation` preserves SSE-newer metadata only for still-listed IDs while
applying current control permissions. Its initial/project-removal selection,
removed-current-chat clearing and explicit-new-chat retention follow Web.
Catalog refreshes no longer discard a model/effort choice. The Activity also
updates the current header metadata without rebuilding transcript selection.
`workspace-data-reference.mjs` executes the actual Web effect/profile cache for
seven navigation sequences, six asynchronous loading races and three missing-ID
vectors. All 30 new API26/35 coordinator/production-Activity executions pass,
including same-revision pushes, save acknowledgements, deferred reads, batch
failure, logout, language rebinding, authoritative permission revocation, exact
bind-relative fallback timing during socket retries and send/stop worker isolation.
At the workspace-loading checkpoint, the full suite was 457 tests/60 suites:
453 pass, 4 retained failures,
0 errors/skips. The retained failures are the two strict table-wrapping and two
file-search geometry executions (maximum1.125px, unchanged1px limit). The later
search-header correction below resolves those two geometry failures, not the
independent full search raster gate.
Both APKs build; both lint variants report0 errors/68 warnings. See
`workspace-data-{build,java,regression,independent}-check.txt` in the artifact
directory. This does not establish real-server/tsnet/device parity. The later
history checkpoint supersedes the paging/scroll-anchor gaps from this pass;
the incremental-Markdown pass now retains answer blocks/documents, but changed
tool/user subtrees and HTML rendering remain different. Avatar image API loading is removed by the later
local-generation change described below.
The later workspace lifecycle checkpoint below supersedes this pass's native
2.5s socket retry and disconnect/resume behavior.

Chat initialization now uses reusable `NativeChatEvents` over the same Go
subscription/request bindings. Opening a conversation subscribes immediately;
the SSE snapshot initializes its project/model/permission UI without a preceding
history GET. Only failed initialization starts one coalesced REST fallback.
After initialization, stream failure checks authentication rather than fetching
history again. Missing/forbidden chats remain selected with the actual HTTP
error inline, instead of silently switching to a new chat. The separate
running-turn one-second fallback remains unchanged, as it does on Web.

New SSE snapshots revoke queued/in-flight fallback results, including late401
responses. Navigation, logout, backgrounding and locale rebinding revoke old
frames/reads/timers, even after returning to the same chat ID. Unchanged bind or
send acknowledgement does not reopen the stream. Unchanged/older snapshots
retain transcript Views and native editor selection/composition. Changed
snapshots still use the existing transcript renderer. Already issued Go HTTP
requests can finish before being discarded; this does not claim transport-level
AbortController cancellation or identical hidden-page behavior.

`NativeChatState` now applies snapshot, metadata and turn events to the selected
chat and sidebar using the actual Web callbacks' separate rules. This includes
nullish metadata defaults, permission revocation, early metadata, pending-turn
removal and retained terminal turns. `chat-events-reference.mjs` executes the
actual Web subscriber and extracts the actual `main.tsx` callbacks, not a second
handwritten oracle:7 asynchronous subscription sequences/52 states and12 event
application cases. A blocked-worker host test exercises real in-flight races;
API26/35 tests run the production Activity, replacing only the gomobile boundary.
All33 new executions pass, including22 Activity executions. The installed
Chrome156 loopback probe receives two SSE snapshots without any fallback GET;
its default reconnect interval is3032.3ms. Native uses a3000ms retry timer for
the current server, which emits no retry/last-event-ID override. This is not a
real-server/tsnet/authentication/device integration or performance claim.

The SSE checkpoint suite is500 tests/65 suites:498 pass,2 retained strict table
wrapping failures (54 mismatched lines/API),0 errors/skips. Web346 tests and
host45 tests pass; both APKs build, contain the new coordinator and pass package
audits. Lint is0errors/70warnings per variant. The default28,658-value geometry
gate passes; independent full search/composer raster and absolute heading-width
gates still fail without changed thresholds. See
`chat-initialization-{focused,full,java,web,web-reference,web-application,geometry,package}-check.txt`,
`web-chat-events-browser.json` and `chat-initialization-results.json` in the
artifact directory. No APK is installed. The workspace checkpoint below
supersedes these counts and package identities.

Workspace lifecycle now uses reusable `NativeWorkspaceEvents` over the existing
Go/tsnet WebSocket. Its backoff matches Web:500ms doubling through30s, plus
0-20% jitter, with attempts reset only by `ready`. Every message including native
heartbeat frames rearms the45s watchdog. Connection loss immediately coalesces
an auth check, clears presence/header/menu/panel state and rejects pending
people/group requests without replaying them or discarding the draft. A healthy
focus/network resume sends `refresh` without extra workspace GETs; a disconnected
resume bypasses backoff and coalesces navigation/agents/profiles reads without
moving their independent15s fallback deadline. Backgrounding stops native
streams rather than retaining Web's hidden-page socket.

The Go binding now returns a cancelable workspace handle before dialing, so
backgrounding/rebinding can cancel a blocked TLS/WebSocket handshake. Concurrent
close/attach/send is synchronized; canceled streams do not report spurious
failure. Policy close1008 expires authentication instead of retrying forever;
ordinary close and failed handshakes retain failure status. Native hello/pong,
HTTPS origin/cookies/CSRF, interface publishing and API transport remain intact.
Queued opens/sends and posted old frames/auth/send failures are fenced across
locale/session/connection/background changes. Already issued REST auth/data
requests may finish before their revoked results are discarded.

`workspace-events-reference.mjs` executes actual `workspace-stream.ts` and the
actual `main.tsx` connection/unauthenticated callbacks with integer browser
timers:21 sequences/234 states, three random/jitter vectors. Six host tests
include this complete trace matrix, coalesced auth, early frames and revocation.
Production Activity tests replace only generated Go boundaries and exercise
both API26/35, including real focus/network callbacks, presence/header/panel,
pending operations, exact retry/watchdog deadlines and stale posted failures.
The production resume-age clock includes deep sleep; an API26/35 regression
advances elapsed time by45s without advancing Handler uptime and requires an
immediate reconnect rather than refreshing the stale socket.
Separate Go race/vet checks cover real loopback TLS/WebSocket handshakes,
cancellation, concurrent close,1008/ordinary close and native ping/pong. These
layers do not establish real-server/tsnet/device/IME or whole-app1:1 parity.

The workspace checkpoint suite is526 tests/67 suites:524 pass,2 retained strict table
wrapping failures (54 mismatched lines/API),0 errors/skips. All44 workspace
controller/Activity/data executions pass, including20 new Activity executions.
Web346 and host51 tests pass. Both APKs build and ship the coordinator, with
package/source/notice/API26/ABI/original search-image audits passing; lint remains
0errors/70warnings per variant. Default28,658-value geometry passes. Independent
strict full search/composer raster and four absolute heading-width cases still
fail at unchanged limits. See `workspace-reconnect-results.json`,
`workspace-reconnect-{full,focused,go,aar,java,web,package,geometry}-check.txt`
and `workspace-reconnect-independent-check.json` in the artifact directory.
No APK is installed and the goal remains incomplete. The later history
checkpoint supersedes these counts and package identities.

```sh
sh android/tools/run.sh bun android/tools/workspace-events-reference.mjs --check
sh android/gradlew :app:testDebugUnitTest --tests '*NativeWorkspaceEventsTest' --tests '*MainActivityWorkspaceEventsTest' --tests '*MainActivityWorkspaceDataTest'
```

History hydration now uses reusable `NativeHistoryLoader`, `NativeAfterPaint`
and `NativeHistoryScroll`. Each page waits for an actual draw and a posted UI
turn before issuing one read on the background workspace executor, independently
of Send/Stop. Pages load sequentially without scroll-to-top or a manual load
button. Errors latch until the text-only retry is activated. Wrong-chat,
missing-next-cursor and repeated-cursor pages fail rather than loop. Navigation,
logout, locale/cursor changes and backgrounding revoke scheduled/queued/posted
work; unchanged renders coalesce. Backgrounding preserves the failure latch.
An already issued Go REST request may finish before its revoked result is
discarded; transport-level HTTP abort is not claimed.

`NativeChatState.prepend` follows actual Web turn/native-ID alias merge,
deduplication, file order and transcript/input-range rebasing. Live metadata,
access authority, optimistic/running turns and longer partial output remain
current. Later SSE/status snapshots retain hydrated older turns/cursors using
the same Web reconciliation. The obsolete separate `olderPages` state is gone.
Before prepend, native captures the current content height and scroll top;
after layout it restores their height delta when reading earlier output and
follows the bottom only within Web's strict90CSSpx threshold. Content/viewport
resize follows the same policy without moving editor focus. A pending anchor
survives theme/View recreation before layout. Scroll arithmetic retains
fractional-density thresholds; native physical positions round only at layout.

`NativeHistoryNotice` shares the original status/error/retry captions in all
three UI languages, muted colors, centered alignment and24px bottom gap. Web's
global mobile `.quiet {font-size:0}` accidentally hid the text-only history retry;
a narrow `.history-older.quiet` rule now restores10px text without changing other
mobile icon controls. Native normal-height labels use real primary/fallback font
metrics rounded in CSS coordinates before density scaling, not hardcoded English
or CJK heights. Existing explicit Markdown struts retain their previous path.

`history-reference.mjs` executes actual Web fetch/scroll and prepend/reconcile
functions for9 merge,11 fetch/cancel and18 scroll cases. Eight host loader tests
include a genuinely blocked worker and stale success/auth-failure revocation.
Production API26/35 Activity regressions cover frame sequencing, real API path
and locale, precise reading-anchor retention, composer selection/composition,
invalid pages/retry, current versus stale401, live SSE during/after hydration,
background failure retention and workspace recreation. Native observer tests
verify post-draw cancellation, content/viewport resize and the fractional follow
boundary. Separate actual Chrome156 probes execute the original `main.tsx`
history status JSX/CSS with byte-matched Android test fonts over150 scenes;
both native APIs match all scene geometry at the unchanged1physical-pixel limit.
Chrome's actual frame-yield, abort-before-paint and real DOM scroll clamping are
also checked. These checks do not prove OEM font/glyph raster, real IME gestures,
enrolled tsnet/server or whole-application visual parity. Changed transcript
Views still rebuilt at this earlier history checkpoint; the keyed transcript
checkpoint below supersedes that limitation for unchanged message subtrees.

The history-checkpoint full suite is568 tests/71 suites:566 pass,2 retained strict table
wrapping failures (54 mismatched lines/API),0 errors/skips. All42 new history
executions pass. The300 native/Web status scenes compare1,980 geometry values
with maximum0.9921875physical-pixel error at the unchanged1px limit. Host59 and
Web347 tests/type-check pass; the Web production build succeeds. Both current
APKs build, ship all four history components and pass source/notice/API26/ABI/
original search-image audits. Lint is0errors/71warnings per variant; the added
warning is the programmatically constructed history View's tooling constructor.
The independent default28,658-value geometry gate passes. Existing full search,
composer and absolute heading-width gates still fail. The new strict unscaled
history-status full-frame raster check also fails27,468 of1,152,000pixels at the
unchanged1channel-unit limit (maximum118); passing status geometry does not waive
text-raster differences. Light/dark Web/native status contact sheets were
visually inspected. See `history-results.json`, `history-independent-check.json`
and `history-{full,focused,java,web,web-build,browser,package,geometry}-check.txt`
in the artifact directory. No installation or goal-completion claim is made.

```sh
sh android/tools/run.sh bun android/tools/history-reference.mjs --check
sh android/tools/run.sh bun android/tools/history-browser-reference.mjs
sh android/gradlew :app:testDebugUnitTest --tests '*History*Test'
```

## Incremental Tool Activity

`NativeTurn` now retains its `NativeActivityList` through tool/result/thought
and metadata updates. The list reconciles the original Web group/item keys:
progress uses its first part ID and group index, single items use the group/item
index, and multi-item groups keep independently keyed nested items. A singleton
becoming a group intentionally remounts its item, just as Web does. Live/count
changes reset the same group disclosure sets; live changes reset the main
disclosure. User item choices survive those resets.

`NativeToolDetail` independently binds input and output sections, retaining
unchanged source Views/documents, raw scroll containers and nested Markdown
blocks. Raw text growth updates the existing TextView. A changed diff source
remounts its keyed diff boundary; unrelated output/input updates leave the other
boundary intact. Switching thought/tool source modes resets that boundary's
raw disclosure even when the source string is identical. Reusable disclosure
rows rebind title/icon/statistics in place and remeasure intrinsic label widths,
preventing a growing thought preview from keeping an obsolete ellipsis width.

Conditional lifetimes follow actual Web DOM, rather than hiding every subtree:
closing a diff's raw-source disclosure keeps its selectable source mounted;
closing its parent item/group/activity unmounts the descendants. Reopening
recreates those descendants and resets raw-source state, while preserving the
parent's item/group choices. Mounted source state does not retain retired full
tool outputs in the cross-render disclosure state.

`activity-update-reference.mjs` executes the original Web ToolActivityList,
ActivityDetail and ToolPatch through9 sequences/84 snapshots, tracking98 node
roles per snapshot with0 API requests. API26/35 tests replay every snapshot at
mdpi/420dpi/xxhdpi and both themes, checking presence/identity, labels, source
modes/text, disclosure state and selection offsets. Android's per-View default
focus caret is not counted as the browser's explicitly selected global owner.
Complete incremental native frames must be byte-identical to fresh native
frames with equivalent mounted/disclosure state; this detects stale layout and
paint but does not establish Web raster parity. A separate40-update stress test
requires unchanged input/output documents, selection and120px raw-output scroll
to survive with0 detach, then requires parent collapse to actually unmount.
Production Activity tests also cover result arrival/growth and final-answer
collapse with only the Go request boundary shadowed.

The current full run is618 tests/76 suites, all passing with0 failures/errors/
skips, including6 new API26/35 executions. Each API checks504 snapshots,
49,392 node comparisons/3,588 retained nodes,354 selection cases and504 exact
complete incremental-versus-fresh frames. Host59 and Web347 tests/type-check/
production build pass, as do Go avatar/transport tests. Both APKs build and ship
the new bind methods; package/API26/ABI/notice/original-search-resource audits
pass. Lint has0 errors/76 warnings per variant, including the new programmatic
`NativeToolDetail` constructor warning. The final generated Web oracle bytes
match the full-suite resource exactly. The existing28,658-value geometry gate
passes. Independent strict failures remain unchanged: search101,513pixels/
max211, composer20pixels/max2,4/228 absolute heading/link cases and history
notices27,468pixels/max118. No tolerance is relaxed. Native and actual-Web
group-growth/diff-key full screenshots were visually inspected; the DOM oracle's
screenshots use host fonts and are diagnostic, not pixel-parity evidence.
See `activity-reuse-results.json`, `activity-reuse-independent-check.json` and
`activity-reuse-{full,focused,selected,host,web,web-reference,sync,geometry}-check.txt`
in `build/artifacts/android-native/`.

Changed user prompts/attachment sets still rebuild their corresponding inner
subtrees. Further inline selection boundaries, physical-device/IME behavior,
incoming-chat deep links, rendered HTML, background/cancellation policy and
strict raster/heading differences remain open. No APK is installed, no real
account/session data is accessed, and this is not a whole-app1:1 claim.

```sh
sh android/tools/run.sh bun android/tools/activity-update-reference.mjs
sh android/gradlew :app:testDebugUnitTest --tests '*NativeActivityUpdateTest' --tests '*MainActivityTranscriptTest'
```

## Incremental Markdown Answers

Streaming answer updates now bind the existing `NativeMessage`, footer and
Markdown root instead of replacing the answer subtree. The renderer retains
per-tag sibling keys, matching ReactMarkdown's generated keys, and reconciles
paragraphs, headings, blockquotes, list rows/markers, code scroll containers and
table rows/cells without creating a second native view tree. Unchanged blocks
keep their original selectable documents and spans. Changed text updates the
retained TextView; formatting or link-destination changes with identical visible
text update spans on the same document. Reference-definition destinations are
included in signatures even when defined outside the affected paragraph.

Flow/table child models update in place and remove retired bindings, preserving
the existing fractional margin, baseline, column and wrapping calculations.
Task checkboxes keep their View identity while checked/accessibility state
updates. Reusing them exposed a four-pixel repeat-draw difference: the previous
checkmark's round Paint join leaked into the next border. Each glyph draw now
resets border cap/join before painting; the original first-frame geometry and
colors remain unchanged.

Copy always reads the latest Markdown source while keeping the existing footer
and feedback state. As in the actual Web effect, copying again in the same
feedback state or rebinding source does not restart its two-second deadline;
changing between success and failure starts a new deadline. Native copy and
failure labels remain localized. React can move an otherwise retained element
past an unkeyed newline when inserting a different block type. The renderer
tracks those placements and clears the affected native selection, matching the
observed Chrome selection loss without detaching the retained native Views.

`markdown-update-reference.mjs` executes original TurnResponse/MarkdownMessage
components in Chrome. Its13 source-update sequences/41 snapshots cover append,
insert/remove/type changes, nested quotes, lists/tasks, table row/alignment
updates, unfinished code fences, external reference definitions and inline
bold/italic/strike/code/link switches, including Chinese/Japanese fallback runs.
It records actual node identity, selected
offsets, check states, footer retention and copied source, plus the original
same-state copy deadline. Native tests replay every snapshot at three densities
on API26/35 and additionally compare complete incremental native Markdown
frames byte-for-byte with fresh native renders. That control detects stale
styles/geometry/paint, but is explicitly not a Web glyph-raster gate. A separate
40-update/81-paragraph test requires every View and all80 unchanged documents
to survive with zero detach, followed by complete removal/rebinding. Actual
native link movement must dispatch only the newly resolved reference URL.

At this earlier checkpoint tool/activity groups and changed user prompts/
attachment sets still rebuilt their corresponding inner subtrees. The tool
activity pass above now reconciles mounted groups/items/source sections.
Inline-DOM selection cases beyond the
audited vectors, physical-device performance/selection/IME, and complete Web
glyph/border/shadow parity remain unproven. This is not a whole-app1:1 claim.

That earlier full run is612 tests/75 suites, all passing with0failures/errors/
skips, including8 new API26/35 update executions. Each API replays123 snapshots,
357 node comparisons/210 retained target nodes and123 byte-exact complete
incremental-versus-fresh native frames. Host59 and Web347 tests/type-check and
Web production build pass. Both APKs build, ship the incremental methods and
pass package/API26/ABI/license/original-search-resource audits; lint has0errors/
75warnings per variant. The original28,658-value geometry gate still passes.
Independent strict gates remain failing without relaxed limits: search101,513
pixels/max211, composer20pixels/max2,4/228 absolute heading/link geometry cases
and history notices27,468pixels/max118. Final table/inline-format native frames
were visually inspected. See `markdown-reuse-results.json`,
`markdown-reuse-independent-check.json` and `markdown-reuse-{full,selected,host,
web,web-reference,sync,geometry}-check.txt` in `build/artifacts/android-native/`.
No APK is installed and transport/local-avatar policy is unchanged.

```sh
sh android/tools/run.sh bun android/tools/markdown-update-reference.mjs
sh android/gradlew :app:testDebugUnitTest --tests '*NativeMarkdownUpdateTest' --tests '*MainActivityTranscriptTest'
```

## Lossless Table Word Wrapping

`NativeCssWrap` corrects normal-whitespace table word breaking without replacing
the native TextView, source document, spans, selection, or font size. It measures
at a normalization scale of1024 to avoid Minikin's small-font fixed-point advance
loss, retains Blink's1/100px effective font size, and snaps each metric-style
run upward to a1/64px LayoutUnit. These rules were cross-checked against
Chromium156's `FontDescription::EffectiveFontSize`, font-cache precision and
`ShapeResult::SnappedWidth`; no Chromium engine is embedded in production.
Word-spacing access uses public API29+ methods behind version guards; older
Android retains its native default rather than invoking hidden methods.

A length-preserving native TransformationMethod displays a newline only at
selected source-space offsets. The original characters and all span/selection
offsets remain unchanged. Native copy/share actions temporarily read the source
through an exception-safe scope, then restore the display transformation; no
custom clipboard or text-selection implementation is substituted. Accessibility
nodes and events expose original text rather than live display-only breaks.
Width/style/source signatures avoid repeating the measurement plan on unchanged
layouts. RTL, internal non-whitespace break opportunities, atomic inline code,
overlong words and oversized text conservatively retain native wrapping.

`table-wrap-reference.mjs` executes actual Web MarkdownMessage/CSS in Chrome
with byte-matched test fonts. Its72 tables/288 cells cover320/360/412 widths,
DPR1/2.625/3, English critical-boundary/ordinary/bold/italic/mixed text, French
accents and Chinese/Japanese. API26/35 require identical line contents, retain
source/selection through resize, compare original column bounds at<=1physicalpx
and intrinsic advances at<=1/64CSSpx, and invoke actual final-line links and
platform copy/share. The existing108-scene origin test retains its original
strict assertions. Native/Web complete360x600 boundary and mixed-style frames
are retained without cropping/scaling; inspecting them does not establish
full table border, horizontal text or glyph-raster equality.

At the table-wrapping checkpoint, the full run was604 tests/74 suites, all passing with0failures/errors/skips,
including10 table executions. Each API compares483 lines,72 final-line links
and the original1,728 painted baselines/648 links. Wrapping mismatches drop from
54/API to0; maximum column-bound error is0.625physicalpx, baseline0.5px,
origin/height0.8828125px and intrinsic advance0.015625CSSpx, all within the
unchanged limits. Host59 and Web347 tests/type-check and Web production build
pass. Both APKs build, ship the new wrapping methods and pass package/API26/ABI/
license/original-search-resource checks; lint has0errors/75warnings each.
The default28,658-value geometry gate passes. Independent strict search,
composer, absolute-heading/link and history-notice gates still fail at their
original thresholds:101,513 search pixels/max211,20 composer pixels/max2,
4/228 absolute geometry cases and27,468 history pixels/max118. See
`table-wrap-results.json`, `table-wrap-independent-check.json` and
`table-wrap-{full,selected,host,web,web-regression,sync,geometry}-check.txt` in
`build/artifacts/android-native/`. No APK is installed and no whole-app1:1 or
goal-completion claim is made.

```sh
sh android/tools/run.sh bun android/tools/table-wrap-reference.mjs
sh android/gradlew :app:testDebugUnitTest --tests '*NativeTableWrapTest' --tests '*NativeTableOriginTest'
```

## Keyed Transcript And Latest

`NativeKeyedViews` reconciles flat native siblings by turn ID without detaching
unchanged documents during history insertion and unrelated updates. Stable string signatures snapshot mutable JSON, sort object
properties and use turn-relative content rather than absolute transcript offsets.
Older-history prepend, unrelated live output and profile pushes retain existing
user rows, attachment images, answer documents, selection, expanded user bubbles
and copy-feedback timers. Removed turns and changed approval IDs retire their
Views; retained approval callbacks verify current session, selection, chat, turn,
permission and pending request before posting. Stopped-turn errors are hidden as
on Web. The flat layout and original22px/38px gaps do not gain wrapper margins.

`NativeTurn.bind` also retains the response root and unchanged assistant answer
when status, timing, model or activity metadata changes. Message footer author
and timestamp updates do not recreate the copy button or text document.
`NativeUserRow.bindProfile` rebinds local avatar style/seed and author on the same
View; late local bitmap jobs cannot overwrite a newer identity or the bot logo.
An unchanged pending/cached avatar adds no generation job. A failed local decode
can retry on the same View. SVG/bitmap cache limits and the no-image-API policy
remain unchanged. Theme/language/Settings recreation remains a new workspace
surface, not a claim of preserving View identity across all app transitions.

`NativeComposerDock` overlays Web's35px circular latest button, centered10px
above the entire footer without taking history/composer layout space. Its18px
ArrowDown geometry is generated from the original Lucide package, not redrawn.
Colors,1px border and0/3/10px theme shadow use the shared native CSS surface.
Visibility follows the same strict90CSSpx boundary; click initiates a smooth
scroll without requesting focus or replacing editor selection/composition.
Touch, a new transcript layout, backgrounding and disposal cancel the owned
animation. Restoring a scroll position recomputes following after native bounds
clamping, so a shortened document reaching the bottom does not keep the button
incorrectly visible. The250ms native deceleration is not claimed to match
Chrome's compositor-specific smooth-scroll curve/timing.

`transcript-browser-reference.mjs` executes extracted original `main.tsx` turn
JSX, latest callback and scroll handler with the real React message/avatar/tool
components, original CSS and byte-matched test fonts. Chrome verifies keyed
streaming/history/profile DOM and selection retention, the original smooth
scroll call and actual89/90/91px thresholds; no avatar/API request is made.
It records108 latest-button geometry scenes across320/360/412 widths, three
densities, three languages, both themes and two footer/viewport sizes. Native
API26/35 compare those216 scenes at the unchanged1physical-pixel limit. These
are software-layout/selection tests, not real-device IME, glyph/shadow raster,
compositor animation, tsnet or whole-app1:1 evidence.

At this earlier checkpoint, changed answer Markdown and changed tool/activity
groups still replaced their specific native subtrees. The incremental Markdown
pass described above now retains answer/footer/Markdown block identities and
unchanged documents through source updates. The tool/activity pass now also
reconciles mounted groups/items/input/output sections. Editing a user prompt
or replacing its attachment set still replaces that user row; full inline
selection behavior beyond the audited vectors is not established.

At the keyed-transcript checkpoint, the full run was596 tests/73 suites:594 pass,2 retained strict table
wrapping failures (54 mismatched lines/API),0 errors/skips. All28 newly added
executions pass. The final selected66 executions are extracted from that full
run's XML rather than reported as another focused Gradle run. Latest-button
geometry compares1,728 values over216 native/Web scenes, with maximum0.875px
error at the unchanged1px limit. Host59 and Web347 tests/type-check and Web
production build pass. Both APKs build and pass package/API26/ABI/notice/original
search-image audits, including the original new ArrowDown resource. Lint has
0errors/75warnings per variant: the four additions are the dock's programmatic
View constructor and three warnings on the passive, false-returning scroll
touch listener/removal. The full28,658-value baseline geometry gate passes.
Independent strict search/composer/absolute-heading/history-notice gates retain
their previous failures; no threshold/reference is weakened or cropped/scaled.
Native light/dark latest surfaces and the Web light fixture were visually
inspected, not accepted as full-frame raster parity. See `transcript-results.json`,
`transcript-independent-check.json` and `transcript-{full,focused,java,web,
web-build,browser,package,geometry}-check.txt` in the artifact directory. No APK
is installed; no real-server/tsnet/device or goal-completion claim is made.

```sh
sh android/tools/run.sh bun android/tools/transcript-browser-reference.mjs
sh android/gradlew :app:testDebugUnitTest --tests '*Transcript*Test' --tests '*NativeAvatarsTest'
```

Remaining behavior differences confirmed by source audit:

- Background lifecycle: native stops streams while the Activity is stopped,
  while Web can retain a hidden-page socket. Foreground/focus/network refresh,
  presence clearing and connected-state retry policy now follow Web as described
  above; this is not an identical OS/background execution policy.
- Transcript rendering: answer Markdown, tool/activity groups/items and their
  source sections reconcile through live/history/profile updates. Changed user
  prompt/attachment sets still replace their specific inner subtree; full
  inline-DOM selection behavior beyond audited vectors is not established.
- Smooth-scroll animation uses native deceleration rather than matching the
  browser compositor's platform-dependent easing and duration exactly.
- Deep links: Web consumes the initial `?chat=` notification selection. Native
  currently has only its launcher intent filter and no equivalent incoming-chat
  selection handler.
- File preview: native renders Markdown but only displays HTML/CSS source;
  Web additionally supports sandboxed rendered HTML preview.

Avatar generation is now local and separate from the batched `/profiles`
directory read. Web restores its original DiceBear JS generator and shares
up to 256 data URIs/8 MiB across components and display sizes. Native's default
`NativeAvatars` source is the static `Spoolnet.renderAvatar(style, seed)` binding,
not a `Client` request. The same pure `spool/internal/avatar` renderer is compiled
into the AAR through the transport module's local root-module dependency.
It keeps a process-local 256-entry/8 MiB SVG LRU independent of image dimensions;
different display sizes no longer redownload SVGs or regenerate cached images.
The Java component keeps its separate 8 MiB bitmap LRU and coalesces pending
jobs by style, resolved seed and rendered edge (96px minimum, 512px maximum).
Those bitmap jobs run on the image worker and cannot update Views after cache
reset; session reset/Activity destruction clears that View-facing bitmap cache.
No image API, server origin/session, disk cache, ETag or JS runtime is needed
for local generation. Profile loading and saving selected style/seed still use
the authenticated API; this does not make profile metadata available offline.

```sh
sh android/tools/run.sh bun android/tools/workspace-data-reference.mjs --check
sh android/gradlew :app:testDebugUnitTest --tests '*NativeWorkspaceDataTest' --tests '*MainActivityWorkspaceDataTest'
```

```sh
sh android/tools/run.sh bun android/tools/render-web-reference.mjs --screens=commands
sh android/gradlew :app:testDebugUnitTest --tests '*NativeComposerCommandsTest' --tests '*MainActivityGoalTest'
sh android/tools/run.sh bun android/tools/check-composer-commands.mjs
sh android/tools/run.sh bun android/tools/check-composer-commands.mjs --native-prefix=native-api26
```

The command gate compares 2,898 geometry/semantic values per API (26 and 35),
using five fixtures at 320/360/412dp, both themes and all three locales. Geometry
keeps the <=1px limit; strings/colors remain exact. Native/browser menu and long
notice screenshots are inspectable, but full glyph/surface raster and physical
keyboard/IME behavior remain unverified.
`render-web-reference.mjs --screens=composer-surface` renders the original Web CSS
without text. `check-composer-surface.mjs --shadow-only` separately audits exterior
shadow pixels with a one-channel-unit limit. Omitting that flag keeps the strict
full-surface audit, including rounded-border antialiasing; its remaining raster
differences are reported, not hidden by a larger tolerance. Both tools use the
same `run.sh` wrapper and optional `SPOOL_NODE_MODULES` as the other browser probes.
`render-web-reference.mjs --screens=viewport` and `check-mobile-viewport.mjs`
exercise the same composed workspace and actual Web sidebars at 360x800,
360x560, 280x360, 220x500 and 900x500, in both themes. The gate checks 1,218
geometry/semantic values, including 48 real CSS/native drawer-motion samples on
API 26/29/35. Drawers measure the live safe window, preserve normalized animation
progress on resize, and cancel detached callbacks. Insets combine system bars,
IME and display cutouts by per-side maximum. These synthetic checks do not prove
physical keyboard, cutout, system gesture or hardware raster behavior.
The `floating-surface` reference scene and
`check-composer-surface.mjs --surface=floating --shadow-only` extend that exterior
pixel audit to the actual native floating shell, including viewport clipping.
Separate native assertions on API 26/35 at mdpi/xxhdpi retain all eight hit regions
and validate that resizing discards the old mask. Strict full-surface mode remains
available without
`--shadow-only`; a passing exterior audit would not prove its border-AA pixels.
The current floating audit still fails its one-unit limit in light mode: 201
exterior pixels per width differ by two channel units; dark exterior pixels pass.
Its full-surface border-AA differences also remain, so neither mode is described
as full floating-window raster parity.
`NativePopover` uses `NativePopupFrame` to reserve window space around the menu's
border box instead of relying on platform elevation. Model/permission menus keep
the Web `0 12px 40px` shadow; online/operator menus use `0 8px 30px`; selects use
`0 8px 28px #0003`. The frame does not change the menu's anchor coordinates or
width, clips the shadow only at the visible viewport, and treats shadow-room and
rounded-corner touches as outside clicks. Global-layout/scroll observers follow
the anchor; detachment dismisses and removes callbacks. Native API 26/35 tests at
mdpi/xxhdpi cover edge placement, focus restoration, keyboard navigation, profile
colors/radii, movement/cache reuse and resize/detach invalidation. Shared model
and permission factories retain their 46/48dp mobile width reserves; permission
content includes its 1dp border plus 8dp padding.
`NativePermissionMenu` mirrors the actual Web `PermissionPicker`: 500-weight
option titles, reason/description text, 15dp selected checks without the old
extra leading shields, disabled/selected/danger states, Codex sandbox note,
loading, inline native Retry, and one cancelable bypass-confirmation block.
The popup uses the original -35dp horizontal anchor offset, 12dp vertical gap
and min(580dp, 65% visible-height) scroll cap. Its owner rejects stale discovery
results by request/selection/backend identity and disables Send while rediscovery
is pending; it does not replace the Go API path. Bypass cannot be committed while
loading or without an available catalog entry. Escape restores focus; native
Space actions track press/release, while anchor expansion and bypass ink remain
visible on the original composer controls.
Eight shared synthetic states render the actual Web component and the production
native body at 320/360/412dp in both themes, including Chinese/Japanese confirmation
and Codex/Claude variants. API 26 and API 35 each pass 3,222 added
geometry/semantic/icon/color values using the existing <=1px geometry and exact
string/color limits. Legacy evidence remains separate and is checked with
`--screens=permissions --native-prefix=native-api26` rather than substituted by
the API-35 artifacts. `NativeCjkFallback` supports API 26/27's missing fallback
line-spacing API by applying the device's configured CJK face to CJK runs only.
It reads `/system/etc/fonts.xml`, honors locale, fallback-family order, weight,
TTC index and configured axes, and loads only from allowed system font roots.
No bundled/repacked font, hidden API, fixed CJK height or Latin-typeface change
is used. Config/font errors retain platform rendering; bounded parsing, path
containment and a small face cache limit resources. The modern platform path
still uses native fallback line spacing. Test injection points to Robolectric's
relocated system config/fonts and verifies those exact original resource bytes;
it does not supply a handwritten font/height oracle. API 26/35 tests also preserve
selection/styling, CJK combining/variation sequences and explicit fractional CSS
line heights. Unsupported OEM configurations, fonts and hardware remain unverified.

Shared `NativeTextView` also honors the Web root's `font-synthesis:none` for static
configured CJK families in the selected Chinese/Japanese UI locale. It uses the
existing bounded config/font loader on API26/35, applies CSS weight search order
(not nearest numeric weight), keeps available real bold/italic faces, and clears
fake weight/skew when those faces are absent. Strong/emphasis transitions split
the CJK runs without replacing Latin styles or explicit code/font-family spans;
a strong child of weight650 requests900, matching the actual Web probe. No
font is bundled, downloaded, raster-patched or scaled. Missing config and English
fallback contexts retain platform behavior. Configured `wght` variable families
defer to native shaping instead of being frozen to one named static instance;
the pre-existing API26 normal-line metric path remains available. Variable/OEM
font-synthesis equality and code/font-family fallback remain unproven. The former
private `NativeUi.CssText` implementation is extracted without changing its line,
origin, width or selection behavior, so specialized native controls can share it.
File-browser adapter rows now extend this reusable base as well.

The actual Web `FileSidebar`/Pierre renderer keeps filename weight400 during
search: its installed version never emits the attribute targeted by the unused
search-match semibold rule. Native rows therefore stay regular for Latin and CJK
instead of requesting600 for matches. `render-web-reference.mjs --screens=files-fonts`
records visible filenames and their actual fonts through CDP in12 scenes across
Chinese/Japanese, both themes and DPR1/2.625/3. Each scene exercises normal,
unmatched, matched and closed external search; the actual no-match projection
retains both rows (counts2/2/1/2), so that existing native behavior is preserved.
Six API26/35 test executions check those counts without file reads, real adapter
reuse across Latin/CJK/mixed names, unchanged text/accessibility labels, weight,
row height and absence of stale fallback spans or synthetic CJK styling.
Independent native real-face glyph controls now match byte-for-byte in72 cases,
versus24,358 pixels outside the unchanged one-channel-unit limit before sharing
the base. The intermediate shared-base run still caught24 wrong requested
weights before correcting the actual Web400 rule. These controls do not establish
full Web/native file-row raster equality,
English-locale CJK fallback or physical/OEM font behavior.

`NativeTreeRow` shares the file rows' selection/hover background, leading-icon
hook and focus outline while retaining `NativeTextView`'s real text/layout path.
The outline uses the actual CSS1px width, -1px offset and6px outer radius in
floating density coordinates, rather than rounding the stroke to an integer dp
or adding a fixed half physical pixel. Search-open rows show their logical focus
even while the external header input or another header button owns input focus.
Outside search, a tree focus ring disappears when the native list loses focus.
Filtering selects the first canonical matching leaf, newly loaded entries
refresh that candidate, normalized-identical queries preserve user navigation,
and hidden descendants resolve to a visible ancestor before the first row.

`file-focus-reference.mjs` reads real DOM focus attributes and pseudo-element
outline styles in12 browser scenes, covering13 open/query/clear/Escape/keyboard/
blur/reopen transitions each. `file-tree-reference.mjs --check` independently
executes the installed Web controller for22 nested/lazy-listing/collapse steps.
`NativeFileFocusTest` compares production controls and recycled adapter rows on
API26/35, including native focus ownership, visible paths, logical focus and
physical outline geometry. The initial run records132 state/outline differences
per API; all312 UI-state snapshots and44 lazy-tree steps now pass in four native
test executions, including reuse of the same real adapter row. These are
semantic/paint-command checks, not complete focus-edge raster,
touch/IME/accessibility, file-name ordering or hardware parity.

`NativeFileHeader` now owns the reusable title/search/actions composition.
Its flex remainder, leading border/inset and successive action gaps round
cumulative CSS lengths rather than accumulating independent rounding errors.
`NativeSearchField` splits its two border insets cumulatively too. The unchanged
actual-Web oracle covers54 scenes (three widths, three densities, both themes,
all three UI locales), with nine open/query/blur/clear/Escape/close transitions
each. All972 API26/35 snapshots pass the original1physical-pixel limit, now also
checking all three action boxes and localized labels. Search-only geometry had
maximum1.125px before correction; the expanded passing gate has maximum1px.
Production-header resizing preserves the same Editable, selection and composing
range instead of replacing the input or issuing file reads.

`NativeSearchCancel` uses Chromium's four original28x28 PNGs for light/dark and
normal/pressed states, not an approximate X or gradient. Bitmap decoding avoids
density resampling before the explicit8dp clear-control size is applied. Eight
API26/35 search executions cover original resource/state selection, native
editing/clear/focus, composition through resize and actual-header transitions.
`android/licenses/chromium-search-cancel.json` pins upstream paths, SHA256 and git
blob identities; Chromium's BSD license ships in both APK notice assets.
`check-search-resources.mjs` verifies those source identities, exact decoded
pixels against the installed Chrome100%/200% packs and both packaged APKs.
Run it through `android/tools/run.sh`, passing `--chrome-resources` and/or `--apk`
for those optional comparisons. It does not assert full-image or phone parity.

The independent `check-file-search.mjs` still FAILS its unchanged full-frame
one-channel-unit gate:101,513 of4,855,200 pixels exceed tolerance, with maximum
channel differences211light/196dark. Geometry assertions do not replace it.
Current420dpi220px Chinese light/dark native/Web frames were visually inspected;
their search border/text/caret and button pointer-state raster still differ.
There is no crop, rescale, threshold change, device installation or whole-page
1:1 claim. See `search-alignment-{focused,full,java,geometry,raster,package}-check.txt`,
`search-resources-check.json`, `search-resources-nativeverify-check.json` and
`search-alignment-results.json` in the artifact directory.

`NativeMiddleTruncate` replaces the file row's Android `END` ellipsize setting.
It adapts the installed Pierre `MiddleTruncate` rather than substituting Android
`MIDDLE`: minimumLength5, extension split after the last dot when the extension
is at most10 UTF-16 code units, whitespace-safe center fallback, end-priority
flex shrink and separately clipped whole native text runs. Short names use the
Web's leading clip; a trailing dot can retain an empty second segment. The
original name and path remain in the TextView/accessibility model, with no
replacement or hidden characters. CSS collapsing whitespace is display-only
and remaps existing font spans. The component uses native StaticLayout for the
nonselectable filename runs; Markdown, editable inputs and selectable transcript
text retain their existing rendering paths. Fractional glyph advances use the
same native linear/subpixel paint mode as other CSS-aligned native text.

Each segment paints the actual ellipsis at50%/80% text opacity with a2CSSpx
background fade and100ms ease-in-out entrance; hiding is immediate, and reuse/
detach cancels stale animations. `NativeCssTiming` solves the CSS cubic-bezier
directly: the independent native PathInterpolator control in this test runtime
differs from actual Chrome opacity samples by0.0019888. The original0.001 timing
assertion remains unchanged; no tolerance is increased. This does not establish
an unavoidable Android/device interpolation defect. Source and packaged notices
identify the Pierre adaptation and retain the original Apache2.0 license.

`render-web-reference.mjs --screens=files-overflow` reads the real FileSidebar/
Pierre DOM with the original CSS and pinned test fonts. It covers English,
Chinese and Japanese, both themes and DPR1/2.625/3 at220/360/412px viewports;
0/3/10/20/40px content caps are explicitly separate constrained-component
controls, not ordinary full-sidebar scenes. Finite marker animations are
finished only for steady-state captures; actual transition timing is measured
separately. The filenames cover extensions, dotfiles, trailing dots, long
extensions, spaces, CJK, emoji and combining characters. Native tests compare
real recycled adapter rows, unchanged names, segment text/direction/width,
clipped-run origins and steady marker visibility with the actual Web. Full
narrow-view native screenshots and `check-file-overflow.mjs` are independent
of these geometry/motion controls and do not crop or rescale either image.
The reference contains144 scenes with14 filenames each. The API26/35 geometry
checks cover4,032 adapter-row cases, with maximum0.541016 physical-pixel error
under the unchanged<=1px limit. Native motion checks use actual Web opacity
samples at0/25/50/75/100ms and separately exercise reuse/detach cancellation;
cached gradient shaders receive opacity once rather than allocating each frame
or multiplying it twice. The original direct-row baseline confirms native
`END` removed37 characters from the test filename before this implementation.
Full row/glyph/gradient raster, native search-input styling, OEM fonts and
physical touch/IME remain incomplete; matching this segmentation is not a
whole-sidebar1:1 claim.
Eight overflow-test executions pass; actual timing residual is0.000000106.
After the row-grid correction described next, the strict full-frame audit still
fails160,450 of4,855,200 pixels (maximum255), down from264,670 at the unchanged
one-channel limit. Glyph/emoji/header and other raster differences remain.
Current evidence includes `file-row-raster-check.txt`,
`native-file-overflow-{geometry,motion}-api26/35.json` and
`file-overflow-raster-check.json` in `build/artifacts/android-native/`.

`NativeFileOrder` replaces locale collation and the extra dotfile rule with
Pierre's ASCII-digit natural tokens, double-precision numeric comparison and
UTF-16/lowercase/original-case tie breaks. Sort keys are cached per file entry;
directory priority remains intact. `file-tree-reference.mjs --check` executes
the actual installed Web comparator for3,600 ordered pairs and two complete
trees/search sequences, including leading zeroes, punctuation, CJK, combining
marks, case collisions and numeric overflow. These references supplement the
existing22 lazy-tree focus steps instead of replacing them.

File rows now round cumulative30CSSpx boundaries, not every row height, while
retaining ListView scrolling and recycling. The header/divider/tree inset also
round their cumulative edges. Filename line boxes use Pierre's explicit30px
primary-font CSS strut, preventing fallback glyph metrics from moving CJK
baselines, and icons no longer have an extra1dp downward nudge.
`NativeFileRowsTest` compares1,512 actual mounted rows across54 genuine Web
viewport scenes on API26/35: all three locales, both themes, three densities
and220/360/412px widths. The oracle is actual DOM rectangles and CDP text paint
commands; native baselines are also captured from actual Canvas text drawing,
not only TextView getters. Maximum row-edge error improves4.25->0.5physical px,
baseline8.125->0.375px and icon6.625->0.375px, with the unchanged<=1px limit.
Supplemental native tests exercise2,000 rows, repeated scrolling/recycling,
viewport height changes and filtering; they are not independent Web raster
proof. Before/after evidence is retained in `file-row-baseline-api26/35.json`,
`file-row-paint-baseline-api26/35.json` and `native-file-rows-api26/35.json`.

The explicit CSS line-height strut now reads the TextView's primary font paint,
not a fallback run's different ink metrics. This keeps the previously audited
table/body baselines while rendering the real CJK face. Ordinary Latin-only
labels retain their existing buffer/layout path. Font/locale changes rebuild
fallback spans through existing setters; layout, selection and link operations
continue using the same native document after binding.

`probe-cjk-fonts.mjs` inspects the actual MarkdownMessage/original CSS with
byte-checked test fonts at DPR1/2.625/3 in both locales. DevTools confirms CJK
Regular for requested650/900/700 and italic styles where only Regular exists;
Latin Bold still uses the real Roboto-Bold face. An explicitly separate Web
synthetic-style control visibly changes paint. API26/35 native controls now
match direct real-face painting byte-for-byte in12 header scenes; before the
fix,18,859 pixels exceeded the same1-channel-unit threshold. Separate tests
preserve Latin/CJK styled spans and native selection, real available italic/
weight faces, font-family priority, variable-family deferral and XML/path safety.
The table oracle now injects the same relocated real fontconfig on both APIs;
all3,456 baselines and1,296 link clicks remain valid, while the same54 per-API
exact wrapping mismatches still fail. `check-cjk-synthesis.mjs` independently
compares complete Web/native scenes, including a clearly named missing-config
control. All 12 actual full-frame cases retain 108,656 pixels outside the unchanged
one-channel-unit tolerance (maximum 209); the separately named missing-config
control retains 115,910. Its failures are not waived by the direct-native-font
control or the passing baseline/geometry checks.
Neither this body audit nor the native
placement assertions establish physical window coordinates, every permission
response or whole-app raster parity. Browser-derived UA Retry border raster and
absolute glyph baselines also remain unproven. Web's mobile `.quiet` rule used
to hide the bypass-confirmation labels; a narrowly scoped 10px rule now keeps
these safety actions visible, and the actual-component references cover them.
`render-web-reference.mjs --screens=popup-surface` and
`check-composer-surface.mjs --surface=popup --shadow-only` compare four original
CSS profiles with the native frame at three widths and both themes. All
1,078,224 exterior pixels pass the one-channel-unit limit. The strict full-surface
gate still fails at rounded-edge antialiasing (maximum 14 light / 11 dark channel
units); its failure is retained, not converted to a full menu or device-parity
claim. Online/operator content geometry continues to use the actual menu view
inside the new shadow frame.

Settings, avatar and group dialogs share `NativeModalFrame` and `NativeSurface`
instead of Android's floating-window background or elevation. The frame keeps
the Web border box centered independently of the downward `0 20px 100px #0004`
shadow, uses an 18dp radius and `#0007` dim, and constrains content against the
current window rather than assuming full-screen height. Existing border-inclusive
content insets and mobile widths remain unchanged, including Chromium's 38px
dialog UA width reserve. Image preview uses its separate 14dp/94vw/92dvh profile,
72% backdrop and no CSS shadow. Android 12+ retains the existing blur-behind
request; the surface fixture uses a flat backdrop and does not prove actual
background blur, OEM support or older-Android blur parity.
Fractional CSS widths retain their centered, floating-point layout/hit rectangle;
`NativeSurface.setCssBorderBox` independently snaps border/background/descendant
clipping to the CSS pixel grid before density scaling. Native children, text,
icons and images keep unscaled integer layout; no view-wide scale transform is
used to disguise a border-width error. Close autofocus also works
in touch mode. Outside-rectangle clicks close settings/avatar (unless saving)
and image preview; group outside touches do not dismiss. Interrupted drags,
Escape, focus, avatar locking, tall-image aspect ratios, scroll bounds and
resized/wide windows are tested through actual production dialogs on API 26/35
at mdpi/xxhdpi. The image's intrinsic contain sizing now respects the incoming
height constraint after the toolbar, preventing overflow in an IME-sized window.

`render-web-reference.mjs --screens=modal-appearance-surface,modal-avatar-surface,modal-group-surface,modal-image-surface`
opens actual Chromium modal dialogs with the original CSS and a transparent
focused descendant, so an empty-dialog UA focus outline is not mistaken for a
surface difference. `check-composer-surface.mjs --surface=modal-appearance
--shadow-only` (or `modal-avatar`, `modal-group`, `modal-image`) audits all exterior
pixels outside the unchanged 1px AA fringe. Across the four profiles, three
widths and both themes, all 5,083,372 exterior pixels pass the existing <=1
channel-unit limit. Full-surface gates without `--shadow-only` still fail: the
three shadow-bearing profiles retain maximum 29 light / 16 dark differences at
rounded borders; the corrected image preview retains maximum 50 light / 20 dark
differences (149 light / 156 dark pixels per mdpi scene). Its prior 320/412dp
light-mode maximum was 90/93 units because border paint and layout snapping were
mixed. Neither exterior checks nor the
passing content geometry gate establish full modal/device pixel parity. Reports
remain in `build/artifacts/android-native/modal-*-{shadow,surface}-check.txt` and
the corresponding `*-surface-check.json` files; no tolerance was widened.

`probe-fractional-surfaces.mjs` records original-CSS modal geometry and compares
fractional, device-grid and CSS-grid Canvas controls at DPR 1/2.625/3. It saves
dense Web references; `NativeModalTest` independently renders the production
frame at mdpi/420dpi/xxhdpi. The new API26/35 tests retain exact fractional layout
extents, snapped paint/clip bounds, outside-click cancellation rather than content
dismissal, no glyph/image scaling, invalid-coordinate rejection and mask cache
reuse/invalidation/detach. The existing surface geometry precheck, one-unit pixel
threshold and one-physical-pixel exterior fringe are unchanged.
`check-composer-surface.mjs --surface=modal-image --density=3 --straight-only`
(or `--density=1`) passes all 175,488 combined straight-edge pixels across three
widths/both themes. This isolated mode is additional evidence, not a replacement
for full/exterior audits. Full DPR3 still fails rounded-edge raster (maximum 40
light/14 dark); its unchanged exterior audit fails 121 pixels at 412dp. All three
DPR2.625 modes still fail: full maximum 53-92 light/19-20 dark, 57,074 of 118,548
straight-edge pixels and 5,613 exterior pixels exceed one unit. The same-browser
Canvas controls also fail at DPR2.625; these results do not prove the exact
compositor/raster-scale cause, unavoidable differences or hardware parity. The
412dp browser screenshot's far-right viewport column is included, not cropped
or rescaled away. `fractional-browser-check.txt` and
`surface-fractional-controls.json` retain this diagnostic failure; dense native
reports are `modal-image-*-dpr*-check.txt` and
`modal-image-surface-check-dpr*.json`.

`probe-surface-raster.mjs` is a diagnostic control: it compares original CSS with
five same-Chrome software-Canvas border compositions at DPR 1 and 3, in both
themes. Even those same-browser controls differ at rounded edges; this helps
separate compositing/raster effects from layout errors, but does not establish
hardware/device pixel parity or waive the strict native full-surface audit.
The transcript pass adds 3,252 measured values from actual Web `UserMessage`,
`MessageFooter`, `TurnOperatorAvatar` and attachment components. Thirteen shared
cases cover short/wrapped text with and without an operator, all three UI
locales, paragraphs/emphasis, collapsed/expanded long messages, long authors,
attachment-only/attachment-plus-text rows, and assistant output. Paragraph line
counts must match exactly; boxes retain the one-pixel fractional-rounding rule.
`NativeUserRow` composes the same production content/footer/attachment views
used by the tests, with Web's 90% mobile cap and 28px avatar/10px gap/6px inset.
Wrapping text uses a ceiling for intrinsic fractional glyph advances so its last
character does not reflow after integer-width layout. Native paragraph height
includes every CSS line box; expansion adds the original 22px control and 4px
icon gap. User parts join with original whitespace instead of trimming display
or copied text. API-26 tests also exercise row geometry/expansion at mdpi/xxhdpi.
End-aligned user footers preserve Web's intrinsic-width behavior: a long author
plus full timestamp may extend left of the content column at 320dp. The reference
records that existing Web overflow rather than silently changing Web's layout.
The typography pass adds 332 actual paragraph line-offset values. Reusable
`NativeLineHeight` spans round cumulative CSS line positions, fixing a 0.5px
per-line baseline loss (11.5px after 24 lines) without replacing native Layout,
selection or styled spans. Tests on API 26/35 also preserve text selection while
changing line height and ensure spans do not accumulate.
Browser references now use the exact static Roboto and DroidSansMono files from
the installed, pinned Robolectric native runtime, not Android Studio's unrelated
variable Roboto. The same runtime's Noto CJK collection supplies its original
Simplified Chinese (index 2) and Japanese (index 0) faces to the browser, selected
by the fixture's language rather than the host Mac's fallback fonts. Repacking
the collection as standalone OTF files preserves the glyph/metric/shaping tables.
`prepare-ui-fonts.py` prepares these test-only files in
`build/cache/android-ui-fonts/`; no font is added to the APK. Hash checks compare
all 13 Latin/mono resources and the source CJK collection against the native
classpath, as well as the two repacked browser faces. Static Roboto's CSS weight
matching is shared by native typography, while the requested weight axis is
retained for variable system fonts. Real variable/OEM-font behavior and CJK
glyph rasterization remain device-verification work. Native text and input
locales follow the selected Chinese/English/Japanese UI language, as Web's
`usePresentation` sets the document language; this also controls font/line-break
fallback rather than inheriting an unrelated device locale.
Run `sh android/tools/run.sh bun android/tools/check-typography.mjs --quantized`
after native tests and Web references. The 112 checked advances cover four sizes
and seven CSS weights on API 26/35. This explicitly models Minikin's 1/256px
glyph-advance quantization; the residual is <=0.003907px. The unmodified raw
comparison (omit `--quantized`) still fails its 0.01px threshold: maximum raw
difference is 0.042969px. That strict diagnostic is retained separately, not
hidden with a wider tolerance. Relative line offsets and reference-font advances
are not absolute baseline, glyph-raster, complete-Markdown or whole-transcript
pixel-parity proof.
`NativeAtmosphere` follows Web's elliptical gradient geometry and clipped
overlapping login-orbit shadows. Background-only references are retained for
pixel diagnostics. They are deliberately separate from the box geometry gate:
run `check-ui-geometry.mjs --screens=background-login,background-workspace` to
check the sampled RGB/alpha channels with a one-channel-unit tolerance. This
strict background comparison is not yet a whole-app visual-parity guarantee.
The workspace's 2,646 samples pass at <=1 RGB channel unit; login-orbit edge
samples still fail that strict tolerance. Float-precision layer composition
removes most translucent-layer rounding drift but does not claim identical
browser/Android circle antialiasing. These remaining failures are retained in
`build/artifacts/android-native/background-check.txt` rather than hidden by a
larger tolerance.
The 320dp appearance fixture also verifies the wrapped "Follow system" label
and equal-height theme tiles. Coordinate agreement is not a claim of font
rasterization or whole-app pixel parity. The language menu keeps Web's option
order and labels, keyboard wrapping/type-ahead/Escape, focus return and disabled
state. Common native labels are generated from the same Chinese/Japanese catalogs.

Web generates data URIs with the original pinned `@dicebear/core` JS package.
Android generates SVGs on-device through `Spoolnet.renderAvatar`, backed by the
official `dicebear-go/v11` package pinned to `v11.0.0-rc.2` (prerelease) and the
original eight styles pinned to `styles/v10 v10.6.0`, then paints with AndroidSVG.
The shared pure Go renderer is compiled into the existing AAR; this call does
not create a tsnet node, require a server/session or send HTTP. No avatar JS
engine, WebView, external avatar URL or direct-network image loader is added.
Both sides preserve automatic/name-based seeds, picked seeds, retired-style
fallback, original art attribution and the same byte-exact SVG fixtures.
The native picker is available from the sidebar avatar and Settings; style
selection, automatic names, 12-choice pages, save/cancel, and recoverable errors
follow Web. The native bitmap cache is bounded to 8 MiB and renders at the
requested physical-pixel size, including high-density 80dp previews. Private
group icons compose their members' same local avatars rather than generating
an unrelated group identity. A separate process-local Go SVG LRU reuses generated
images across display sizes. The old server image endpoint is retained for
compatibility, but neither current client uses it.

The asset sync also generates 24 SVG golden vectors. Go tests require byte-level
parity with the current Web generator; native rendering tests consume the same
vectors. Native layout tests cover 320/360/412dp, both themes, and a high-density
composer. Screenshots are kept in `build/artifacts/android-native/`.
The local-generation change passes all 346 Web tests/build, Go transport race
tests/vet and the shared renderer race suite. A real Chrome reference run
decodes the original avatar-picker/sidebar/messages/people components in 24
width/theme frames with zero avatar image API requests; it does not mount an
authenticated live workspace. Native tests cover all 24 default-binding vectors
without a Client/session, pending-job sharing, revocation before/after rendering,
physical preview size and the 8 MiB bitmap budget. JVM binding tests shadow only
the native Go method; the actual Go bridge independently matches all SVG bytes.
Both arm64/x86_64 AAR and APK variants were rebuilt, not just Java stubs.
The local-avatar checkpoint was463 tests/61 suites:459 pass,4 known failures.
At the search-header checkpoint, the full suite was467 tests/61 suites:
465 pass,2 known failures (strict table wrapping,54 mismatched lines/API),
0 errors/skips. Search geometry passes; the independent full composer/search
raster and absolute heading-width gates remain separate failures. Lint is
0 errors/70 warnings in each variant; host Java36 tests and both package audits
pass. The later SSE initialization checkpoint above supersedes these counts and
package identities; see `chat-initialization-results.json`. Historical search
evidence is in `search-alignment-results.json`, and
`avatar-local-{web,go,focused,full,java,browser,regression}-check.txt`,
`avatar-image-audit.json` and `avatar-local-results.json` in the artifact directory.
The host Go benchmark is not an Android/JNI/SVG-raster or cold-start measurement.
No APK is installed and no real-server or physical performance parity is claimed.
Robolectric rendering is not a substitute for final font, keyboard, safe-area,
and animation checks on a real Android device.
`sh android/tools/probe-devices.sh` queries connected devices and installed AVDs
without installing the app or using persistent app data. It only reads an existing
adb server; when one is absent, it owns and stops an isolated-port server. Tool
state stays in the shared run wrapper's scratch directory.
Sidebar navigation, pagination, permission controls and stale-action guards also
run against the minimum API 26 framework in Robolectric; Android lint checks
production API compatibility separately.

## Server Setup

For Public HTTPS, use the [existing PWA reverse proxy](../README.md#webpwa-and-https)
and matching Public origin, enter that origin in the selected server's
**Public HTTPS** entry in the APK settings. No server or Android Tailscale enrollment is needed.

Desktop and the headless Go server can provide HTTPS using an embedded tsnet
node, without installing a system Tailscale daemon or configuring Serve.
In Desktop, open **Service > Tailscale (tsnet)**, enable it, optionally stage an
Auth Key, then start/restart the service. Without a key, use the displayed
Tailscale browser login button. Copy the assigned HTTPS address into the Android
app's selected server **Tailscale (tsnet)** entry; the APK does not need rebuilding.
Auth Keys are not bundled into the APK.

For the headless server:

```sh
./build/spool-server -tsnet -tsnet-hostname spool-server
```

Sign in using the login URL printed by the server and approve its node if
required. Enable **MagicDNS and HTTPS certificates** in the Tailscale admin
DNS settings. Spool prints the actual HTTPS URL and automatically saves it as
the Public origin. Persistent node state stays in `<data-dir>/tsnet/`.
The flags are saved; later launches reuse them and the enrolled node identity.
For unattended enrollment, inject `TS_AUTHKEY` via the process environment or a
secret manager, not a command-line argument. Disable with `-tsnet=false`.

Alternatively, install and sign in to system Tailscale on the server, and run
Spool with its normal loopback listener. Expose it **within the tailnet**, not
with Funnel:

```sh
tailscale serve --bg http://127.0.0.1:8787
```

For the Serve option, set Spool's **Public origin** to the exact HTTPS origin returned by Serve, for
example `https://spool.example.ts.net`, and apply the service settings. Tailnet
policy must permit the phone's embedded node to reach that server/port. A Spool
user and authorized project must already exist; Tailscale login does not replace
Spool login. The server must remain running and awake. The same APK works with
either option: enter the chosen server's HTTPS URL, without rebuilding the app.

## Build

Requirements:

- Go 1.27.3+, with the existing shared module cache.
- JDK 17+ (the scripts also detect Android Studio's bundled JDK on macOS).
- Android SDK Platform **36.1**, Build Tools **36.1.0**, and accepted SDK licenses.
- Android NDK **29.0.14206865**. Set `ANDROID_NDK_HOME` to use another compatible
  installed NDK. No NDK is installed implicitly by the build script.
- Python 3.12+ and CMake 3.22+ for the pinned native syntax library build.
- Network access on the first build for pinned Go, Gradle and Maven dependencies.

Set `ANDROID_HOME` if the SDK is not at `~/Library/Android/sdk`. The optional
`sh android/tools/install-ndk.sh` installs the fixed official NDK into that SDK
on macOS, checking its published checksum. It downloads about 1 GB, never
overwrites an existing NDK, and cleans its owned scratch directory. Allow at
least 8 GiB of free space for the download, extraction and subsequent builds;
the installer checks this before downloading.

Run from the repository root:

```sh
# ARM64 Go AAR, debug APK, Java tests, and Android lint.
sh android/tools/build.sh

# Go proxy and configuration tests. They bind disposable loopback ports.
sh android/tools/run.sh go -C android/transport test -race ./...
sh android/tools/run.sh go -C android/transport vet ./...

# Generate real Java bindings, compile resources/all sources, build the host JNI
# syntax library, and run architecture/transport-policy/native syntax JUnit tests.
# JNI headers come from a full JDK, or the NDK when Studio's JBR omits them.
# This is NOT a substitute for building the Android AAR/APK.
sh android/tools/check-java.sh
```

The Gradle wrapper is pinned to 8.13 with distribution SHA-256 verification;
AGP is 8.13.2. There is no AndroidX WebKit dependency. Go dependencies and checksums are
in `transport/go.mod` and `transport/go.sum`.

Outputs:

- `build/android/spoolnet.aar`
- `build/android/gradle/app/outputs/apk/debug/app-debug.apk`
- `build/android/gradle/app/reports/tests/testDebugUnitTest/index.html`
- `build/android/gradle/app/reports/lint-results-debug.html`

The debug application ID is `com.cloudgeek.spool.android.dev`. Its debug key is
created in `build/android/debug.keystore`, not the developer's Android home.
This debug build is not a signed production release.

For an optimized phone release APK:

```sh
sh android/tools/build.sh :app:assembleRelease :app:lintRelease
python3 android/tools/check-syntax-package.py \
  --apk build/android/gradle/app/outputs/apk/release/app-release-unsigned.apk \
  --abis arm64-v8a
```

Release packages ARM64 only, enables R8 code optimization and resource shrinking,
and strips native debug symbols using the pinned NDK. JNI entry points retain
their names through the app's rules and the gomobile AAR's consumer rules. Direct
release APKs compress native libraries to reduce download size; Android extracts
them during installation, which adds installation work and disk usage. App Bundle
delivery keeps native libraries uncompressed for direct loading. Debug and
nativeVerify also package ARM64 only, matching the current phone and Apple
Silicon development machine.

The release APK is unsigned until a production signing configuration is supplied.
R8 mappings are written to `build/android/gradle/app/outputs/mapping/release/`.

The Go transport and Android syntax libraries also default to ARM64 builds.

After generating the AAR, open `android/` in Android Studio, or use
`sh android/gradlew :app:assembleDebug`. Do not run a bare Gradle command if it
would bypass the repository's scratch/cache wrapper. Gradle and Go build caches
live under `build/cache/`; every command wrapper owns and cleans a separate
`build/tmp/android.XXXXXX/` run. `GOPATH` and `GOMODCACHE` are not changed.

## Device Verification

### Remote Desktop

After the host has enabled voxl and granted the signed-in user remote desktop
access, **Settings > Remote connection > Connect to desktop** opens a native
viewer. It uses the same `/api/desktop` connect, availability, revocation and
disconnect routes as Web, over the existing private-cookie HTTPS/Tailscale
transport. No new host management endpoint or browser runtime is introduced.

The viewer decodes voxl 0.3.3's independent H.264 Annex-B stripes with Android
`MediaCodec`, groups their outputs by frame ID, then acknowledges complete frames.
Tap clicks, drag holds the left button, long press right-clicks, and two fingers
scroll. **Pan and zoom** switches to local panning and pinch/toolbar zoom from
100% to 500%; it never changes the host's desktop resolution. The keyboard button
opens Android's IME, including committed Chinese/Japanese text; external keyboards
and Esc/Tab/modifier/arrow controls send voxl keysyms. Read-only views suppress
remote input. The floating toolbar and its collapsed handle have transparent
backgrounds, with outlined icons and translucent interaction feedback. Icons have
accessible labels and long-press tooltips. While expanded, its fullscreen and disconnect controls stay visible while the other
controls scroll horizontally. The collapse button reduces the toolbar to a single
expand button at the bottom right, freeing the rest of the desktop for viewing
and input. Expanding restores the controls and preserves fullscreen and zoom.
The fullscreen button toggles entering and exiting
fullscreen, which hides the heading and system bars; swiping from a screen edge
temporarily reveals the system bars. The toolbar stays above the Android keyboard and clear of
display cutouts. Closing the viewer, backgrounding, signing out or revocation closes
the view and its media resources. Reconnection requires another explicit action.

Desktop audio, microphone, clipboard synchronization and voxl's shared-folder
panel are not implemented in this native viewer. Project-file browsing remains
available through the existing workspace UI. Unsupported video dimensions or
decoder failures end the view with an error rather than accumulating frames.

Focused verification:

```sh
sh android/tools/run.sh go -C android/transport test -race ./...
sh android/gradlew :app:testDebugUnitTest --tests '*NativeDesktop*' --tests '*NativeArchitectureTest'
sh android/gradlew :app:assembleDebug :app:lintDebug

# ffmpeg/libx264 creates only synthetic images. The separate test package has no
# network permission, never starts tsnet, and is uninstalled by the smoke helper.
sh android/tools/run.sh python3 android/tools/prepare-desktop-fixture.py
sh android/gradlew :app:assembleNativeVerify
sh android/tools/run.sh python3 android/tools/device-desktop-smoke.py --serial DEVICE_SERIAL
```

The device probe checks two real H.264 decoders, decoded red/blue fixture pixels,
eight ordered frame acknowledgements, touch/IME commands, local zoom, read-only
mode, toolbar collapse/expand and its reduced footprint, fullscreen system-bar hiding, the fullscreen button restoring the window without
disconnecting, labelled icon controls, background cleanup and simulated revocation. It does not establish live
host/Tailscale performance, hardware keyboard behavior or physical IME coverage.
The phone must allow installation of the separate verification package.

For synthetic visual checks on a phone with an existing developer installation,
build `sh android/gradlew :app:assembleNativeVerify` and run:

```sh
sh android/tools/run.sh python3 android/tools/device-ui-smoke.py --serial DEVICE_SERIAL
```

This uses only the separate `com.cloudgeek.spool.android.nativeverify` package.
Its manifest removes network permissions and the production launcher; fixtures
never construct tsnet or access the existing `.dev` app's data. The helper refuses
to overwrite a preexisting verification package, captures only synthetic scenes,
and uninstalls its owned package on exit. Phone-side installation approval may be
required; it does not change security settings or bypass a rejected installation.
It only reuses an existing adb server. The IME probe requests focus/show/hide from
inside its own activity and retains bounds, not keyboard screenshots or suggestions;
it does not inject shell input or claim real gesture/composition coverage. Device
reports and screenshots go to `build/artifacts/android-native/device-ui/`.

Install `build/android/gradle/app/outputs/apk/debug/app-debug.apk` on a disposable
emulator or test device. Keep emulator data and profiles in an owned
`build/tmp/<run>/` directory; retain only useful screenshots/logs in
`build/artifacts/android-native/`. Do not use a real provider home/database.

1. Launch at 320/360/412 dp widths. Compare the native login story, form,
   workspace header, drawer, welcome panel, message bubbles and composer with
   Web mobile. Check large text, landscape, keyboard resizing, light and dark.
2. Open **Settings > Tailscale (tsnet)** and enter the server HTTPS origin. Tap
   **Connect**, use **Sign in to Tailscale**, and approve the node if needed.
   Check that diagnostics appear only in Settings, including while connecting.
3. Return to the native login form and sign in with an existing Spool account.
   Create/select a chat, pick a real model/effort and authorized permission mode,
   submit a message, observe incremental output, stop a running turn, and answer
   an approval request. Check read-only project/chat behavior.
4. Open another chat while a response is arriving. Returning to Settings or
   changing theme must preserve the draft; old callbacks must not replace the
   newly selected conversation. Load earlier history without losing live turns.
5. Select/remove/upload attachments. Browse project directories and preview text
   and images; protected paths stay server-authorized. External Markdown links
   open outside the app directly on click, without a native-only confirmation.
   Invalid protocols remain inert; displaying a Markdown image must not load it.
6. Use two disposable accounts to verify presence, online-group/private messages,
   acknowledgements and reconnect behavior. Unacknowledged sends are not retried
   automatically; the current draft remains available for checking/retrying
   until navigating to another chat or explicitly creating a fresh one.
7. Switch Wi-Fi/mobile data and background/foreground. Disconnect/reconnect and
   relaunch: the embedded node and unexpired Spool session are retained. Signing
   out revokes the server session and clears its retained cookie.
8. Try an invalid certificate, refused API request and unavailable server. They
   must fail closed, not bypass tsnet. A successful node probe must not conceal
   an independent HTTPS error. With a Fake-IP VPN active, disconnect/reconnect
   while the target peer is initially unavailable: HTTPS must wait for the exact
   peer, never dial the fake IP, and recover from transient EOFs without changing
   VPN settings. Disconnect during a pending check must cancel it promptly;
   stale results must not mark a newer connection as verified.

The automated Go tests cover exact-origin requests, cookie/CSRF handling,
redirect/TLS rejection, response limits, SSE framing/cancellation, WebSocket
handshakes/messages, private session persistence and multipart uploads. Java
checks compile the actual generated bindings and guard against reintroducing a
WebView or connection information in normal headers. They do not replace the
real-device visual and Tailscale enrollment checks above.

## Diagnostics And Scope

In Tailscale mode, Settings probes only the selected server node. A successful DISCO ping identifies
Direct, DERP (with region), or Peer Relay; the peer's home DERP alone is not proof
of relayed traffic. Unknown targets and failed samples never imply a path or
latency. Diagnostics pause outside Settings/in the background. Node reachability
is independent of HTTPS, project authorization and Spool authentication.

The foreground `ConnectionRecovery` supervisor performs one exact-origin HTTPS
`/healthz` GET (8-second limit). In Tailscale mode it also samples backend state
(3-second limit); Public HTTPS does not query an embedded node or run DISCO probes.
Healthy samples repeat 15 seconds after completion. Failures use 2/4/8/16/30-second
backoff; API transport errors can advance a healthy timer, and network changes
or returning to the foreground cause an immediate check. There is only one
health/recovery operation in flight. Backgrounding, disconnecting and selecting
another server revoke stale results and queued recovery work. With no usable
network, checks/resets wait for the network to return. Public HTTPS uses Android's
active system network, including a system VPN; Tailscale requires an underlying
non-VPN network for its embedded node.

A missing peer/path sample does not establish a disconnect. HTTPS success is
authoritative even when local status is temporarily unavailable. A single
transient HTTPS/status failure marks the sample as failed but does not discard
previous availability. Two consecutive failures mark it unavailable and request
a network refresh. At least three consecutive network/backend failures spanning
30 seconds trigger a refresh of ordinary connections in Public HTTPS mode, or a
bounded `WantRunning=false/true` cycle on the existing
embedded node; subsequent resets have 60/120/240/300-second cooldowns. Only a
successful health sample establishes recovery. The existing SSE/WebSocket retry
policies resume live data without replaying writes or clearing the workspace.

NeedsLogin requests the official interactive login once per login episode and
polls for completion; NeedsMachineAuth waits for approval. Certificate/hostname,
redirect and malformed health errors remain explicit endpoint failures. HTTPS
502/503/504 and TCP connection refusal are treated as server availability
problems: health is retried, but the embedded node is not reset for those errors.
API 401 still expires only the Spool session. Interface updates, disconnect and
node shutdown use a separate worker from HTTPS checks, so a slow check cannot
block network-change delivery or disconnect cancellation. Authenticated mutations
and unacknowledged sends are never automatically replayed. The older Go `Probe`
entry point retains its bounded three-attempt behavior for explicit callers;
Android's supervisor uses the single-attempt `Health` entry point.

The native client currently includes the primary workspace, people messaging,
text/Markdown and image/file-preview flows. It is not an embedded browser: complex
HTML, the Web client's full Markdown/plugin renderer, group administration,
background notifications/services and automatic APK
updates are not included. The desktop remains responsible for account/project/
agent/service administration. People-message history and unsent attachments stay
in app-session memory, not retained diagnostic reports.

New servers keep each network entry's private state under
`noBackupFilesDir/servers/<server-id>/{tsnet,https}/`. The migrated original server
retains `noBackupFilesDir/{tsnet,https}/`. Cookies are scoped to the exact origin and connection
mode; backup and device transfer are disabled. Preferences retain only the server
origin, connection mode and appearance/language choices. Disconnect closes
the selected server's clients/nodes but does not sign out of Spool; use **Sign out** to revoke the
session. Clearing Android app storage also forgets the node identity; remove the
old node in the Tailscale console when appropriate. Server-side agent tasks keep
running if Android stops the app.
