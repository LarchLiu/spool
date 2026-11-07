package com.cloudgeek.spool.android;

import android.graphics.Rect;
import android.graphics.drawable.ColorDrawable;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.PopupWindow;
import android.widget.ScrollView;
import android.widget.TextView;

/** Reusable anchored, scrollable Web-style menu with keyboard/outside dismissal. */
final class NativePopover {
    static NativePopover model(NativeUi ui) { return new NativePopover(ui, 290, 17, 1, 46); }
    // Content padding includes the Web border, which NativeSurface paints inside its bounds.
    static NativePopover permission(NativeUi ui) {
        NativePopover menu = new NativePopover(ui, 320, 13, 9, 48);
        menu.permissionMode = true;
        return menu;
    }
    static NativePopover commands(NativeUi ui) {
        NativePopover menu = new NativePopover(ui, 390, 12, 6, 0);
        menu.commandMode = true;
        menu.restyle(12, ui.borderStrong, 8, 32, ui.shadow);
        menu.window.setFocusable(false);
        menu.window.setInputMethodMode(PopupWindow.INPUT_METHOD_NEEDED);
        return menu;
    }
    static NativePopover workspace(NativeUi ui) {
        NativePopover popup = new NativePopover(ui, 420, 10, 13, 16);
        popup.workspaceMode = true;
        popup.restyle(10, ui.line, 8, 30, 0x22000000);
        return popup;
    }

    final LinearLayout content;
    Runnable dismissed;
    private final NativeUi ui;
    private final PopupWindow window;
    private final ScrollView scroll;
    private final NativePopupFrame frame;
    private final int requestedWidth;
    private final int viewportReserve;
    private java.lang.ref.WeakReference<View> anchor;
    private boolean above;
    private boolean selectMode;
    private int selectMinimumWidth = 180;
    private boolean onlineMode;
    private boolean operatorMode;
    private boolean permissionMode;
    private boolean commandMode;
    private boolean workspaceMode;
    private String search = "";
    private long searchAt;
    private android.view.ViewTreeObserver observer;
    private final android.view.ViewTreeObserver.OnGlobalLayoutListener layoutObserver = () -> scheduleResize();
    private final android.view.ViewTreeObserver.OnScrollChangedListener scrollObserver = () -> { if (operatorMode) dismiss(); else scheduleResize(); };
    private final Runnable resize = this::relayout;
    private final View.OnAttachStateChangeListener attachmentObserver = new View.OnAttachStateChangeListener() {
        @Override public void onViewAttachedToWindow(View view) {}
        @Override public void onViewDetachedFromWindow(View view) { dismiss(); }
    };

    NativePopover(NativeUi ui, int width, int radius, int padding) {
        this(ui, width, radius, padding, 32);
    }

    NativePopover(NativeUi ui, int width, int radius, int padding, int viewportReserve) {
        this.ui = ui;
        requestedWidth = ui.dp(width);
        this.viewportReserve = ui.dp(viewportReserve);
        content = ui.padded(padding, padding);
        scroll = new ScrollView(ui.context);
        scroll.setFillViewport(false);
        scroll.setVerticalScrollBarEnabled(false);
        scroll.setBackground(null);
        scroll.addView(content, new ScrollView.LayoutParams(-1, -2));
        frame = new NativePopupFrame(ui, scroll, radius, ui.borderStrong, 12, 40, ui.shadow);
        frame.escape = () -> {
            View target = this.anchor == null ? null : this.anchor.get();
            dismiss();
            if (target != null && target.isAttachedToWindow()) target.requestFocus();
        };
        window = new PopupWindow(frame, ui.dp(width), -2, true);
        window.setBackgroundDrawable(new ColorDrawable(0x00000000));
        window.setOutsideTouchable(true);
        window.setElevation(0);
        window.setTouchInterceptor((view, event) -> {
            if (event.getActionMasked() == android.view.MotionEvent.ACTION_OUTSIDE
                    || event.getActionMasked() == android.view.MotionEvent.ACTION_DOWN && !frame.hitsPanel(event.getX(), event.getY())) {
                dismiss(); return true;
            }
            return false;
        });
        window.setInputMethodMode(PopupWindow.INPUT_METHOD_NOT_NEEDED);
        window.setOnDismissListener(() -> {
            if (ui.activePopover == this) ui.activePopover = null;
            scroll.removeCallbacks(resize);
            unobserve();
            View target = this.anchor == null ? null : this.anchor.get();
            if (target instanceof NativeUi.ChoiceButton) target.setActivated(false);
            if (selectMode && target != null && target.isAttachedToWindow()) target.requestFocus();
            if (dismissed != null) dismissed.run();
        });
        content.addOnLayoutChangeListener((view, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom) -> {
            if (!window.isShowing() || bottom - top == oldBottom - oldTop) return;
            scroll.removeCallbacks(resize);
            scroll.post(resize);
        });
    }

    private void scheduleResize() {
        scroll.removeCallbacks(resize);
        if (window.isShowing()) scroll.post(resize);
    }

    private void unobserve() {
        View target = anchor == null ? null : anchor.get();
        if (target != null) target.removeOnAttachStateChangeListener(attachmentObserver);
        if (observer != null && observer.isAlive()) {
            observer.removeOnGlobalLayoutListener(layoutObserver);
            observer.removeOnScrollChangedListener(scrollObserver);
        }
        observer = null;
    }

    void selectOption(String title, boolean selected, Runnable click) {
        selectOption(title, "", selected, click);
    }

    void selectOption(String title, String description, boolean selected, Runnable click) {
        LinearLayout row = ui.row();
        row.setMinimumHeight(ui.dp(36));
        row.setPadding(ui.dp(8), ui.dp(8), ui.dp(8), ui.dp(8));
        row.setBackground(ui.states(selected ? ui.controlHover : 0x00000000, 6, 0, ui.controlHover, 0x00000000,
                selected ? ui.controlHover : (ui.controlHover & 0x00ffffff) | 0x80000000));
        TextView label = ui.text(title, 12, ui.text);
        ui.fixedLineHeight(label, 1.2f);
        if (description.isEmpty()) {
            label.setSingleLine(true);
            label.setEllipsize(android.text.TextUtils.TruncateAt.END);
            row.addView(label, new LinearLayout.LayoutParams(0, -2, 1));
        } else {
            LinearLayout copy = ui.column(); copy.addView(label);
            copy.addView(ui.text(description, 11, ui.muted));
            row.addView(copy, new LinearLayout.LayoutParams(0, -2, 1));
        }
        if (selected) {
            android.widget.ImageView check = ui.decorativeIcon("check", 14);
            check.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(ui.dp(14), ui.dp(14));
            params.setMarginStart(ui.dp(8));
            row.addView(check, params);
        }
        row.setSelected(selected);
        ui.focusOnPress(row);
        row.setContentDescription(title);
        row.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info);
                info.setClassName("android.widget.Button");
                info.setSelected(selected);
            }
        });
        row.setOnClickListener(view -> click.run());
        row.setOnKeyListener((view, code, event) -> {
            if (event.getAction() != android.view.KeyEvent.ACTION_DOWN) return false;
            if (code == android.view.KeyEvent.KEYCODE_ESCAPE || code == android.view.KeyEvent.KEYCODE_TAB) {
                dismiss();
                return true;
            }
            int count = content.getChildCount(), index = content.indexOfChild(row), next = -1;
            if (code == android.view.KeyEvent.KEYCODE_DPAD_DOWN) next = (index + 1) % count;
            else if (code == android.view.KeyEvent.KEYCODE_DPAD_UP) next = (index - 1 + count) % count;
            else if (code == android.view.KeyEvent.KEYCODE_MOVE_HOME) next = 0;
            else if (code == android.view.KeyEvent.KEYCODE_MOVE_END) next = count - 1;
            else if (!event.isCtrlPressed() && !event.isAltPressed() && !event.isMetaPressed()) {
                int character = event.getUnicodeChar();
                if (character > 32 && !Character.isISOControl(character)) {
                    String text = new String(Character.toChars(character)).toLowerCase(java.util.Locale.ROOT);
                    search = event.getEventTime() - searchAt > 700 ? text : search + text;
                    searchAt = event.getEventTime();
                    String query = search.codePoints().allMatch(ch -> ch == text.codePointAt(0)) ? text : search;
                    for (int offset = 1; offset <= count; offset++) {
                        int candidate = (index + offset) % count;
                        if (content.getChildAt(candidate).getContentDescription().toString().toLowerCase(java.util.Locale.ROOT).startsWith(query)) {
                            next = candidate;
                            break;
                        }
                    }
                }
            }
            if (next < 0) return false;
            content.getChildAt(next).requestFocus();
            return true;
        });
        content.addView(row, new LinearLayout.LayoutParams(-1, -2));
    }

    void showSelect(View anchor, int initial) {
        showSelect(anchor, initial, 180);
    }

    void showSelect(View anchor, int initial, int minimumWidth) {
        selectMode = true;
        selectMinimumWidth = minimumWidth;
        restyle(10, ui.borderStrong, 8, 28, 0x33000000);
        show(anchor, false);
        if (initial >= 0 && initial < content.getChildCount()) content.getChildAt(initial).requestFocus();
    }

    void showOnline(View anchor) {
        onlineMode = true;
        content.setPadding(ui.dp(16), ui.dp(13), ui.dp(16), ui.dp(13));
        restyle(12, ui.line, 8, 30, ui.shadow);
        show(anchor, false);
    }

    void showOperators(View anchor) {
        operatorMode = true;
        content.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7));
        restyle(12, ui.line, 8, 30, ui.shadow);
        show(anchor, false);
    }

    void show(View anchor, boolean above) {
        ui.dismissMenus();
        ui.activePopover = this;
        this.anchor = new java.lang.ref.WeakReference<>(anchor);
        this.above = above;
        relayout();
        if (window.isShowing()) {
            if (anchor instanceof NativeUi.ChoiceButton) anchor.setActivated(true);
            unobserve();
            observer = anchor.getViewTreeObserver();
            observer.addOnGlobalLayoutListener(layoutObserver);
            observer.addOnScrollChangedListener(scrollObserver);
            anchor.addOnAttachStateChangeListener(attachmentObserver);
        }
    }

    private void restyle(int radius, int border, float shadowY, float blur, int color) {
        if (window.isShowing()) dismiss();
        frame.restyle(radius, border, shadowY, blur, color);
    }

    View panel() { return frame.surface; }

    private void place(View anchor, Rect viewport, int x, int y, int width, int height) {
        if (viewport.isEmpty() || width <= 0 || height <= 0) { dismiss(); return; }
        Rect bounds = frame.place(viewport, x, y, width, height);
        int[] screen = new int[2], inWindow = new int[2];
        anchor.getLocationOnScreen(screen); anchor.getLocationInWindow(inWindow);
        int windowX = bounds.left - screen[0] + inWindow[0], windowY = bounds.top - screen[1] + inWindow[1];
        if (window.isShowing()) window.update(windowX, windowY, bounds.width(), bounds.height());
        else {
            window.setWidth(bounds.width()); window.setHeight(bounds.height());
            window.showAtLocation(anchor, Gravity.TOP | Gravity.LEFT, windowX, windowY);
        }
    }

    private void relayout() {
        View anchor = this.anchor == null ? null : this.anchor.get();
        if (anchor == null || !anchor.isAttachedToWindow()) { dismiss(); return; }
        Rect viewport = new Rect();
        anchor.getWindowVisibleDisplayFrame(viewport);
        int width = Math.max(ui.dp(1), Math.min(requestedWidth, viewport.width() - viewportReserve));
        int[] position = new int[2];
        anchor.getLocationOnScreen(position);
        if (workspaceMode) {
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(Math.max(1, viewport.height() - ui.dp(16)), content.getMeasuredHeight());
            int x = Math.max(viewport.left + ui.dp(8), Math.min(position[0], viewport.right - width - ui.dp(8)));
            int y = Math.max(viewport.top + ui.dp(8), Math.min(position[1] + anchor.getHeight() + ui.dp(8), viewport.bottom - height - ui.dp(8)));
            place(anchor, viewport, x, y, width, height);
            return;
        }
        if (commandMode) {
            width = Math.max(ui.dp(1), Math.min(requestedWidth, anchor.getWidth() - ui.dp(2)));
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(viewport.height(), content.getMeasuredHeight());
            place(anchor, viewport, position[0] + ui.dp(1), position[1] + ui.dp(1) - height - ui.dp(8), width, height);
            return;
        }
        if (permissionMode) {
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(Math.min(ui.dp(580), Math.round(viewport.height() * .65f)), content.getMeasuredHeight());
            place(anchor, viewport, position[0] - ui.dp(35), position[1] - height - ui.dp(12), width, height);
            return;
        }
        if (operatorMode) {
            width = Math.max(ui.dp(1), Math.min(ui.dp(240), viewport.width() - ui.dp(16)));
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(Math.min(ui.dp(320), viewport.height() - ui.dp(16)), content.getMeasuredHeight());
            int x = Math.max(viewport.left + ui.dp(8), Math.min(position[0], viewport.right - width - ui.dp(8)));
            int y = position[1] - height - ui.dp(6);
            if (y < viewport.top + ui.dp(8)) y = position[1] + anchor.getHeight() + ui.dp(6);
            y = Math.max(viewport.top + ui.dp(8), Math.min(y, viewport.bottom - height - ui.dp(8)));
            place(anchor, viewport, x, y, width, height);
            return;
        }
        if (onlineMode) {
            int maximumWidth = Math.min(ui.dp(360), Math.max(ui.dp(1), viewport.width() - ui.dp(24)));
            content.measure(View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            width = Math.min(maximumWidth, Math.max(ui.dp(210), content.getMeasuredWidth()));
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(Math.min(ui.dp(520), Math.round(viewport.height() * .7f)), content.getMeasuredHeight());
            int x = position[0] + anchor.getWidth() - width;
            x = Math.max(viewport.left + ui.dp(12), Math.min(x, viewport.right - width - ui.dp(12)));
            int y = position[1] + anchor.getHeight() + ui.dp(8);
            place(anchor, viewport, x, y, width, height);
            return;
        }
        if (selectMode) {
            if (!anchor.isEnabled() || position[1] + anchor.getHeight() <= viewport.top || position[1] >= viewport.bottom) { dismiss(); return; }
            float density = ui.context.getResources().getDisplayMetrics().density;
            NativeSelectLayout layout = new NativeSelectLayout(position[0] / density, position[1] / density,
                    (position[0] + anchor.getWidth()) / density, (position[1] + anchor.getHeight()) / density,
                    anchor.getWidth() / density, viewport.left / density, viewport.top / density,
                    viewport.width() / density, viewport.height() / density, content.getChildCount(), selectMinimumWidth);
            width = ui.dp(layout.width);
            content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int height = Math.min(ui.dp(layout.maximum), content.getMeasuredHeight());
            if (height <= 0) { dismiss(); return; }
            int x = ui.dp(layout.left), y = ui.dp(layout.top) - (layout.above ? height : 0);
            place(anchor, viewport, x, y, width, height);
            return;
        }
        int maximum = Math.min(ui.dp(540), Math.max(ui.dp(120),
                (above ? position[1] - viewport.top : viewport.bottom - position[1] - anchor.getHeight()) - ui.dp(26)));
        // Measure intrinsic content, not the scroll root constrained by the old PopupWindow size.
        content.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        int height = Math.min(maximum, content.getMeasuredHeight());
        int x = Math.max(viewport.left + ui.dp(16), Math.min(position[0] + anchor.getWidth() - width,
                viewport.right - width - ui.dp(16)));
        int y = above ? position[1] - height - ui.dp(13) : position[1] + anchor.getHeight() + ui.dp(6);
        y = Math.max(viewport.top + ui.dp(8), y);
        place(anchor, viewport, x, y, width, height);
    }

    void dismiss() {
        if (ui.activePopover == this) ui.activePopover = null;
        scroll.removeCallbacks(resize);
        window.dismiss();
    }
    boolean isShowing() { return window.isShowing(); }
}
