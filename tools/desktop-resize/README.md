# macOS window resize diagnostic

Run from the repository root:

```sh
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs
```

The separate **Spool Resize Diagnostic** window uses Desktop's native window
configuration, synthetic text, and disposable app data. Drag its outer left and
right edges in both directions, then close the window. It also exits after about
thirty minutes. The wrapper stops owned processes and removes the temporary app.

The latest recording is `build/artifacts/desktop-resize/events.jsonl`. It contains
window dimensions, live-resize start/end notifications, main-thread dispatch
latency, and mouse coordinates during live resizing. It does not open Spool's
database, load conversations, or contact agent providers.

Run comparisons sequentially; each mode replaces its recording. Cookie polling
uses `events-cookies-main.jsonl` or `events-cookies-worker.jsonl`:

```sh
# The same window with a main WebView, without the native UI compositor.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --content direct

# The built Spool chat frontend with 60 synthetic turns, code and tables.
# Prepare the current checkout's frontend with `make web` when needed.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --spool-ui

# Reproduce Desktop's synchronous UI-thread cookie lookup, then compare
# the same native API called from a worker. No cookie values are recorded.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --spool-ui --cookie-poll main
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --spool-ui --cookie-poll worker

# Native UI without WebKit.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --content native

# Native UI with the system title bar and default traffic-light placement.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --content native --standard-titlebar

# Verify programmatic horizontal/vertical dimensions and orderly shutdown.
sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs --automatic
```

`--automatic` checks native API sizes. It does not reproduce AppKit's interactive
resize tracking and cannot establish whether a manual drag remains responsive.

Run the actual Desktop notification path with temporary app data, isolated
accounts, and a deliberately stalled cookie lookup:

```sh
sh tools/run.sh desktop-resize-check bun tools/desktop-resize/smoke.mjs
sh tools/run.sh desktop-resize-check bun tools/desktop-resize/smoke.mjs --race
```

This verifies that native dispatch continues during the lookup, automatic
notifications and their click targets still work, and the app quits. It also
checks that an idle desktop does not poll cookies, session-change hints preserve
the chat document and native privilege boundary, and a delayed old-session read
cannot deliver notifications after an account switch. Reports belong to
`build/artifacts/desktop-resize-smoke/`.
