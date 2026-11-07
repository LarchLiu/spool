package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Insets;
import android.graphics.PixelFormat;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.StateListDrawable;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.Window;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.WindowManager;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import java.util.concurrent.Executor;
import org.json.JSONObject;

/** Full-window native viewer using the Web connect/disconnect and availability APIs. */
final class NativeDesktopDialog extends Dialog {
    private final NativeUi ui;
    private final android.content.Context labels;
    private final Handler main = new Handler(Looper.getMainLooper());
    final NativeDesktopView desktop;
    private final LinearLayout shell, heading, dock, toolbarControls;
    private final TextView status;
    private final NativeUi.IconButton keyboard, pan, minus, fit, plus, shortcut, alt, shift, fullscreenButton, toolbarToggle;
    private final java.util.List<NativeUi.IconButton> keys = new java.util.ArrayList<>();
    final NativeDesktopConnection connection;
    private volatile boolean closed, ended, acceptingVideo;
    private boolean readOnly, fullscreen, toolbarCollapsed;
    private final Runnable firstFrameTimeout = () -> failed("Could not decode desktop video");

    NativeDesktopDialog(NativeUi ui, NativeDesktopConnection.Transport transport, Executor io, Runnable dismissed) {
        this(ui, transport, io, dismissed, "");
    }

    NativeDesktopDialog(NativeUi ui, NativeDesktopConnection.Transport transport, Executor io, Runnable dismissed, String serverName) {
        super(ui.context);
        this.ui = ui;
        Configuration config = new Configuration(ui.context.getResources().getConfiguration());
        config.setLocale(Locale.forLanguageTag(ui.locale));
        labels = ui.context.createConfigurationContext(config);
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        shell = ui.column();
        shell.setBackgroundColor(ui.surface);
        shell.setOnApplyWindowInsetsListener((view, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                Insets safe = insets.getInsets((fullscreen ? 0 : WindowInsets.Type.systemBars()) | WindowInsets.Type.displayCutout() | WindowInsets.Type.ime());
                view.setPadding(safe.left, safe.top, safe.right, safe.bottom);
            } else {
                int top = fullscreen ? 0 : insets.getSystemWindowInsetTop();
                int left = insets.getSystemWindowInsetLeft(), right = insets.getSystemWindowInsetRight();
                if (Build.VERSION.SDK_INT >= 28 && insets.getDisplayCutout() != null) {
                    top = Math.max(top, insets.getDisplayCutout().getSafeInsetTop());
                    left = Math.max(left, insets.getDisplayCutout().getSafeInsetLeft());
                    right = Math.max(right, insets.getDisplayCutout().getSafeInsetRight());
                }
                view.setPadding(left, top, right, insets.getSystemWindowInsetBottom());
            }
            return insets;
        });
        heading = ui.row(); heading.setPadding(ui.dp(12), ui.dp(6), ui.dp(12), ui.dp(6));
        TextView title = ui.bold(serverName.isEmpty() ? label(R.string.desktop_title) : serverName + " · " + label(R.string.desktop_title), 16);
        heading.addView(title, new LinearLayout.LayoutParams(0, -2, 1));
        shell.addView(heading);
        status = ui.text(label(R.string.desktop_connecting), 12, ui.muted);
        status.setPadding(ui.dp(12), ui.dp(5), ui.dp(12), ui.dp(5));
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        shell.addView(status);
        desktop = new NativeDesktopView(ui, this::send, reason -> main.post(() -> failed(reason)), this::updateControls,
                () -> {
                    main.removeCallbacks(firstFrameTimeout);
                    status.setText(label(R.string.desktop_read_only)); status.setVisibility(readOnly ? View.VISIBLE : View.GONE);
                });
        FrameLayout viewer = new FrameLayout(ui.context);
        viewer.setBackgroundColor(0xff101010);
        viewer.addView(desktop, new FrameLayout.LayoutParams(-1, -1));
        shell.addView(viewer, new LinearLayout.LayoutParams(-1, 0, 1));
        dock = ui.row();
        dock.setClickable(true);
        dock.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        toolbarControls = ui.row();
        dock.addView(toolbarControls, new LinearLayout.LayoutParams(0, -2, 1));
        HorizontalScrollView tools = new HorizontalScrollView(ui.context);
        tools.setHorizontalScrollBarEnabled(false);
        LinearLayout buttons = ui.row();
        keyboard = button(buttons, "keyboard", label(R.string.desktop_keyboard), desktop::keyboard);
        pan = button(buttons, "pan", label(R.string.desktop_pan), () -> desktop.panMode(!desktop.panMode()));
        minus = button(buttons, "minus", label(R.string.desktop_zoom_out), () -> desktop.zoom(1 / 1.25));
        fit = button(buttons, "fit", label(R.string.desktop_fit), desktop::fit);
        plus = button(buttons, "plus", label(R.string.desktop_zoom_in), () -> desktop.zoom(1.25));
        addKey(buttons, "escape", "Esc", 0xff1b); addKey(buttons, "tab", "Tab", 0xff09);
        shortcut = button(buttons, "chevron_up", label(R.string.desktop_control), () -> desktop.modifier(desktop.shortcut()));
        alt = button(buttons, "alt", "Alt", () -> desktop.modifier(0xffe9));
        shift = button(buttons, "shift", "Shift", () -> desktop.modifier(0xffe1));
        addKey(buttons, "back", label(R.string.desktop_left), 0xff51);
        addKey(buttons, "send", label(R.string.desktop_up), 0xff52);
        addKey(buttons, "arrow_down", label(R.string.desktop_down), 0xff54);
        addKey(buttons, "arrow_right", label(R.string.desktop_right), 0xff53);
        tools.addView(buttons);
        toolbarControls.addView(tools, new LinearLayout.LayoutParams(0, -2, 1));
        fullscreenButton = button(toolbarControls, "fullscreen", label(R.string.desktop_fullscreen), () -> setFullscreen(!fullscreen));
        button(toolbarControls, "log_out", label(R.string.desktop_disconnect), this::dismiss);
        toolbarToggle = button(dock, "chevron_down", label(R.string.desktop_toolbar_collapse), () -> setToolbarCollapsed(!toolbarCollapsed));
        FrameLayout.LayoutParams placement = new FrameLayout.LayoutParams(-1, -2, Gravity.BOTTOM | Gravity.END);
        placement.setMargins(ui.dp(8), ui.dp(8), ui.dp(8), ui.dp(8));
        viewer.addView(dock, placement);
        setContentView(shell);
        setCanceledOnTouchOutside(false);
        Window window = getWindow();
        if (window != null) {
            window.setBackgroundDrawableResource(android.R.color.transparent);
            window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
            window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
            if (Build.VERSION.SDK_INT >= 30) window.setDecorFitsSystemWindows(false);
            window.setStatusBarColor(ui.surface); window.setNavigationBarColor(ui.surface);
            window.getDecorView().setSystemUiVisibility(ui.dark ? 0 : View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR);
        }
        connection = new NativeDesktopConnection(transport, io, main, new NativeDesktopConnection.Events() {
            public void message(byte[] data, boolean binary) {
                if (closed || ended) return;
                if (binary) { if (acceptingVideo) desktop.binary(data); return; }
                try {
                    if (data.length > 65536) throw new IllegalArgumentException("Desktop message too large");
                    JSONObject message = new JSONObject(new String(data, StandardCharsets.UTF_8));
                    switch (message.optString("t")) {
                        case "ping": send("po," + message.getLong("s")); break;
                        case "hello":
                            acceptingVideo = false;
                            NativeDesktopProtocol.Layout layout = new NativeDesktopProtocol.Layout(message);
                            main.post(() -> {
                                if (closed || ended) return;
                                readOnly = layout.readOnly;
                                status.setVisibility(View.VISIBLE); status.setText(label(R.string.desktop_connecting));
                                desktop.configure(layout); acceptingVideo = true;
                                main.removeCallbacks(firstFrameTimeout); main.postDelayed(firstFrameTimeout, 20000);
                            });
                            break;
                        case "close": main.post(() -> failed("Desktop connection ended")); break;
                        default: break;
                    }
                } catch (Exception error) { main.post(() -> failed("Could not decode desktop video")); }
            }
            public void failed(String reason) { NativeDesktopDialog.this.failed(reason); }
        });
        setOnDismissListener(dialog -> {
            closed = true; acceptingVideo = false;
            desktop.close(); connection.close(); main.removeCallbacksAndMessages(null); dismissed.run();
        });
        updateControls();
    }

    String label(int id) { return labels.getString(id); }
    private NativeUi.IconButton button(LinearLayout parent, String kind, String text, Runnable run) {
        NativeUi.IconButton button = ui.icon(kind, 20, text, run);
        button.setLayoutParams(new LinearLayout.LayoutParams(ui.dp(44), ui.dp(44)));
        iconKind(button, kind);
        describe(button, text);
        button.setImageTintList(new ColorStateList(new int[][]{new int[]{android.R.attr.state_selected}, new int[0]}, new int[]{0xffffc7a8, 0xfff5f5f5}));
        StateListDrawable background = new StateListDrawable();
        background.addState(new int[]{-android.R.attr.state_enabled}, ui.shape(0, 8, 0));
        background.addState(new int[]{android.R.attr.state_pressed}, ui.shape(0x40ffffff, 8, 0));
        background.addState(new int[]{android.R.attr.state_selected}, ui.shape(0x26ffffff, 8, 0));
        background.addState(new int[]{android.R.attr.state_focused}, ui.shape(0x26ffffff, 8, 0));
        background.addState(new int[]{android.R.attr.state_hovered}, ui.shape(0x26ffffff, 8, 0));
        background.addState(new int[0], ui.shape(0, 8, 0));
        button.setBackground(background);
        button.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View host, AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(host, info); info.setClassName("android.widget.Button");
                if (host == pan || host == shortcut || host == alt || host == shift || host == fullscreenButton) {
                    info.setCheckable(true); info.setChecked(host.isSelected());
                }
            }
        });
        parent.addView(button); return button;
    }
    private void iconKind(NativeUi.IconButton button, String kind) {
        if (!(button.getDrawable() instanceof ToolbarIcon icon) || !icon.kind.equals(kind)) {
            button.setKind(kind); button.setImageDrawable(new ToolbarIcon(kind));
        }
        button.setPadding(ui.dp(12), ui.dp(12), ui.dp(12), ui.dp(12));
    }
    /** Thin glyph outlines keep transparent controls readable over light and dark desktop pixels. */
    private final class ToolbarIcon extends Drawable {
        final String kind;
        final Drawable glyph, outline;
        final int size = ui.dp(20);
        final float edge = Math.max(1, ui.dp(.65f));
        ToolbarIcon(String kind) {
            this.kind = kind;
            glyph = NativeIcons.drawable(ui.context, kind, 0xfff5f5f5, size);
            outline = NativeIcons.drawable(ui.context, kind, 0xcc000000, size);
        }
        @Override public void draw(Canvas canvas) {
            glyph.setBounds(getBounds()); outline.setBounds(getBounds());
            for (int y = -1; y <= 1; y++) for (int x = -1; x <= 1; x++) {
                if (x == 0 && y == 0) continue;
                int saved = canvas.save(); canvas.translate(x * edge, y * edge);
                outline.draw(canvas); canvas.restoreToCount(saved);
            }
            glyph.draw(canvas);
        }
        @Override public int getIntrinsicWidth() { return size; }
        @Override public int getIntrinsicHeight() { return size; }
        @Override public void setTintList(ColorStateList tint) { glyph.setTintList(tint); invalidateSelf(); }
        @Override public boolean isStateful() { return glyph.isStateful(); }
        @Override protected boolean onStateChange(int[] state) {
            boolean changed = glyph.setState(state); if (changed) invalidateSelf(); return changed;
        }
        @Override public void setAlpha(int alpha) { glyph.setAlpha(alpha); outline.setAlpha(alpha); invalidateSelf(); }
        @Override public void setColorFilter(ColorFilter filter) { glyph.setColorFilter(filter); invalidateSelf(); }
        @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
    }
    private void describe(View view, String text) { view.setContentDescription(text); view.setTooltipText(text); }
    private void addKey(LinearLayout parent, String kind, String text, int symbol) { keys.add(button(parent, kind, text, () -> desktop.tap(symbol))); }
    private void send(String command) { if (!closed && !ended && connection != null) connection.send(command); }
    private void updateControls() {
        if (keyboard == null) return;
        boolean control = desktop.controllable();
        pan.setEnabled(desktop.ready());
        keyboard.setEnabled(control);
        for (NativeUi.IconButton key : keys) key.setEnabled(control);
        shortcut.setEnabled(control); alt.setEnabled(control); shift.setEnabled(control);
        shortcut.setSelected(desktop.held(desktop.shortcut()));
        alt.setSelected(desktop.held(0xffe9)); shift.setSelected(desktop.held(0xffe1));
        iconKind(shortcut, desktop.shortcut() == 0xffeb ? "command" : "chevron_up");
        describe(shortcut, label(desktop.shortcut() == 0xffeb ? R.string.desktop_command : R.string.desktop_control));
        pan.setSelected(desktop.panMode());
        minus.setEnabled(desktop.panMode() && desktop.viewport.zoom > 1);
        plus.setEnabled(desktop.panMode() && desktop.viewport.zoom < 5);
        fit.setEnabled(desktop.panMode()); describe(fit, label(R.string.desktop_fit) + " (" + Math.round(desktop.viewport.zoom * 100) + "%)");
    }
    boolean fullscreen() { return fullscreen; }
    boolean toolbarCollapsed() { return toolbarCollapsed; }
    private void setToolbarCollapsed(boolean value) {
        if (closed || toolbarCollapsed == value) return;
        desktop.releaseAll(); toolbarCollapsed = value;
        toolbarControls.setVisibility(value ? View.GONE : View.VISIBLE);
        FrameLayout.LayoutParams placement = (FrameLayout.LayoutParams) dock.getLayoutParams();
        placement.width = value ? FrameLayout.LayoutParams.WRAP_CONTENT : FrameLayout.LayoutParams.MATCH_PARENT;
        dock.setLayoutParams(placement);
        iconKind(toolbarToggle, value ? "chevron_up" : "chevron_down");
        describe(toolbarToggle, label(value ? R.string.desktop_toolbar_expand : R.string.desktop_toolbar_collapse));
        desktop.requestFocus();
    }
    void setFullscreen(boolean value) {
        if (closed || fullscreen == value) return;
        desktop.releaseAll(); fullscreen = value;
        heading.setVisibility(value ? View.GONE : View.VISIBLE);
        fullscreenButton.setSelected(value); iconKind(fullscreenButton, value ? "fullscreen_exit" : "fullscreen");
        describe(fullscreenButton, label(value ? R.string.desktop_fullscreen_exit : R.string.desktop_fullscreen));
        applyFullscreen(); shell.requestApplyInsets(); desktop.requestFocus();
    }
    private void applyFullscreen() {
        Window window = getWindow();
        if (window == null) return;
        shell.setBackgroundColor(fullscreen ? 0xff101010 : ui.surface);
        if (Build.VERSION.SDK_INT >= 28) {
            WindowManager.LayoutParams params = window.getAttributes();
            params.layoutInDisplayCutoutMode = fullscreen ? WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
                    : WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_DEFAULT;
            window.setAttributes(params);
        }
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController controller = window.getInsetsController();
            if (controller != null) {
                if (fullscreen) {
                    controller.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
                    controller.hide(WindowInsets.Type.systemBars());
                } else controller.show(WindowInsets.Type.systemBars());
            }
        } else {
            if (fullscreen) window.addFlags(WindowManager.LayoutParams.FLAG_FULLSCREEN);
            else window.clearFlags(WindowManager.LayoutParams.FLAG_FULLSCREEN);
            int appearance = ui.dark ? 0 : View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
            window.getDecorView().setSystemUiVisibility(appearance | (fullscreen ? View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    | View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                    | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION : 0));
        }
    }
    private void failed(String reason) {
        if (closed || ended) return;
        acceptingVideo = false; desktop.close(); connection.close();
        main.removeCallbacks(firstFrameTimeout);
        ended = true;
        status.setVisibility(View.VISIBLE); status.setTextColor(ui.error);
        status.setText(label(reason.equals("Desktop connection ended") ? R.string.desktop_ended
                : reason.equals("Could not connect to desktop") ? R.string.desktop_unavailable : R.string.desktop_video_error));
        updateControls();
    }
    @Override public void show() {
        ui.dismissMenus(); super.show();
        Window window = getWindow();
        if (window != null) {
            window.getDecorView().setPadding(0, 0, 0, 0);
            window.setLayout(WindowManager.LayoutParams.MATCH_PARENT, WindowManager.LayoutParams.MATCH_PARENT);
        }
        applyFullscreen();
        desktop.requestFocus(); connection.start();
    }
}
