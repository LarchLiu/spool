# Chat screenshots

On macOS, Windows, and Linux Desktop, click the scan icon beside the attachment button to hide Spool
and immediately start capturing. Its small arrow opens a single **Show Spool**
action. Clicking that action immediately starts capturing while keeping the main
window visible. This applies to the current capture only; the scan icon always
hides Spool on the next capture. Both actions let you select any visible
application window or drag a region, including Spool when it is visible.

Move the pointer over an application window to highlight it. **Click** to select
its visible bounds, including its title bar, then edit or confirm the selection.
The hover label shows the window name followed by its width and height.
This selects one window; it does not combine all windows belonging to an
application or scroll offscreen content.

**Drag** to select a rectangular region instead. After releasing the mouse, drag
any of its four edges or four corners to resize it. The editor follows Sessio's
window/region selection and annotation workflow:

- The editor toolbar appears only after selecting a window or region. **Esc**
  also cancels before a selection is made.
- Hover over a toolbar icon to see its name; keyboard focus also shows the label.
- No tool is selected initially. Dragging inside the selection moves the crop.
- **Rectangle** draws an outline inside the selection.
- **Line** draws a straight line.
- **Mosaic** pixelates a dragged area.
- **Undo**, or **Cmd/Ctrl+Z**, removes the last annotation.
- **Move selection** lets you drag the crop without changing its size.
- The checkmark (**Capture**), or **Enter**, saves the edited PNG to the chat draft.
- Clicking an active tool again deselects it.
- **Select again** clears the selection, tool, and annotations. Clicking or dragging
  outside the selection also starts a new selection. **Esc** cancels.

Resizing or moving the crop preserves annotations at their original image
coordinates and clips them to the crop. A failed save leaves the editor open
with its selection and annotations intact for retry. Each connected
display has its own selection surface; a rectangular selection stays within the
display where the drag starts. A window spanning displays is clipped to the
display where it is selected, as in Sessio's display-based editor.

The selection surface uses a frozen image, so its controls never appear in a
region capture. On macOS, a borderless native panel covers the entire display,
including the menu bar, without an opening animation. It stays hidden until the
background image and selection controls are laid out. Native window enumeration
provides the front-to-back hover targets. Hidden editor pages load in parallel
with screen capture. Keeping Spool visible starts capture immediately; hiding it
allows 250 ms for the desktop compositor to remove the hidden window. Connected
displays capture concurrently, and Go reads PNG dimensions without decoding the
source pixels a second time. Preview and PNG export share a single renderer and
use the frozen source, so the result includes the annotations
without the toolbar, selection frame, or later changes to other apps. The
selection outline, resize handles, rectangles, and lines use the same theme color
as the chat controls, including in dark mode. Retina pixels and negative display
coordinates are preserved. The PNG appears as a normal draft
attachment for preview, removal, and sending. Changing the account, chat,
project, or draft while selecting discards the result. Screenshots follow the
existing attachment limits: 4 MB per image, six files and 16 MB total per message.

Spool restores the main window after completion, cancellation, failure, or the
two-minute timeout. Returning to the same editable draft also focuses its message
input, so typing can continue immediately. On macOS, both visibility actions require screen
recording permission. If macOS requests it, allow Spool in
**System Settings → Privacy & Security → Screen & System Audio Recording** (the
label varies by macOS version). Restart Spool if macOS asks you to, then retry.
When permission is missing, Spool opens the relevant System Settings page with
a guide showing the app to authorize. Closing that guide reports the permission
requirement in the chat; changing the workspace or quitting cancels the pending
guide. Permission checks and guidance run off the UI thread before capture starts.
Accessibility permission is not required. The screenshot is captured on the
computer running the desktop client, including when the chat connects to a
remote Spool server. Only the chosen image is uploaded when the message is sent.
Full display images stay inside the owned native selection windows and memory;
temporary capture files are private and removed after reading. The clipboard is
never read or replaced by this feature.

Windows uses native GDI display capture and front-to-back window enumeration.
Monitor rectangles and capture pixels use physical coordinates; each monitor's
window targets are converted separately into the editor's display coordinates.
This preserves negative origins and different monitor DPI. The borderless,
square-cornered editor covers the taskbar and becomes the foreground window;
confirming or cancelling restores Spool and the chat input. Screen capture is
unavailable on a locked/secure desktop, and protected content may be blank.

Linux X11 uses native XCB capture and EWMH window metadata, including title-bar
frame extents. Each editor covers its monitor, including desktop panels. It
requires the normal X11 runtime library `libxcb.so.1`; no screenshot command-line
utility is required.

Linux Wayland (including Spool running through XWayland) uses the standard
`org.freedesktop.portal.Screenshot` service, so native Wayland applications can
also be captured.
The system first asks the user to select/authorize a screenshot; that PNG opens
with its area selected in the same Spool editor for resizing, annotations,
mosaic, and attachment delivery. Small selections have padding around the image
so the toolbar fits, while export initially includes only the selected source
pixels. The image keeps its original resolution even when the editor fits it
into a smaller window. Cancelling the system picker quietly returns to Spool.
Timeout/shutdown closes a pending portal request. The portal owns its source
file; Spool reads it without changing or deleting it. A working desktop portal
and screenshot backend (such as the desktop's GNOME/KDE portal backend) are
required. Wayland does not provide arbitrary global window enumeration or
positioning, so its system selection step cannot match the direct macOS/X11
window-hover overlay. The system picker determines available monitors/windows
and may allow selection across outputs. Hide/Show Spool still controls whether
the main window is visible when that picker starts.

Web/PWA and Android retain their existing file/image attachment entry point;
they do not show the desktop screenshot button. Their system screenshot and
paste/file-selection workflows remain available.

For changes to the feature, run the screenshot unit tests and the browser
selection/attachment smoke checks through `tools/run.sh`. The desktop
`--smoke-probe attachments` probe also checks actual WebView navigation for the
primary button and the **Show Spool** action using native mouse down/up events
(including focus changes while holding the mouse), the owned native screenshot panel,
exact display/window/content bounds, and native-to-composer image delivery. Synthetic display/window capture fixtures
exercise main-window visibility, unrestricted window selection, edited PNG export,
caller authorization, startup cancellation, and restoration through the production
capture lifecycle. Browser checks cover the annotation tools, undo, resize/move,
deferred capture loading, save retry, and matching Retina preview/export pixels.
The native probe writes capture-start and selection-ready timings beside its
report; these isolate app overhead using synthetic pixels, rather than measuring
the OS screen-capture operation. These
checks do not read actual desktop pixels or request Screen Recording access.
Browser smoke checks use `SPOOL_NODE_MODULES` when Playwright is installed outside
`web/`.

The composer browser checks cover desktop capability hints for macOS, Windows,
and Linux as well as the ordinary browser. Unit tests cover native pixel byte
orders/row padding, opaque desktop pixels, mixed-DPI per-monitor window targets,
and system-picked image fitting/export coordinates. On an owned Linux virtual
desktop, the optional native fixtures can also be run without capturing a real
user's screen:

```sh
sh tools/run.sh screenshot-linux-native sh -eu -c '
  SPOOL_SCREENSHOT_XVFB=1 SPOOL_SCREENSHOT_PORTAL_FIXTURE=1 \
    dbus-run-session -- xvfb-run -a -s "-screen 0 320x240x24 -nolisten tcp" \
    go test ./internal/screencapture
'
```

These fixtures create their own X11 window and D-Bus portal service, verify
window closure/stale metadata, source pixels, early portal responses, user
cancellation, and pending-request cleanup. They also capture two display regions
concurrently, checking per-display target clipping, negative editor origins,
window stacking, frame extents, and UTF-8 titles against actual XCB replies.

To run the current capture package on an authenticated Linux x64 box without
installing Go there, sign in once with `bunx --yes @simular-ai/box login`, then run:

```sh
sh tools/run.sh screenshot-linux-box bun tools/screenshot-linux-box.mjs main
```

This compiles locally and transfers only the test binary and its runner through
the official box CLI. The box needs Xorg with its dummy driver, `xauth`, and
`dbus-run-session`. The runner verifies the binary checksum, starts a private
authenticated virtual display with TCP disabled and a separate D-Bus session,
and removes its processes and temporary files after the test. It never uses the
box's existing desktop display. The latest log and report are retained under
`build/artifacts/screenshot-linux-box/`.

The desktop and capture packages
also cross-compile for Windows/Linux amd64 and arm64. Cross-compilation and
browser mocks do not replace native Windows/Linux or physical multi-monitor
validation.
