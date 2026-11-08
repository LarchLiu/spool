package com.cloudgeek.spool.android;

import android.graphics.drawable.Drawable;
import android.graphics.drawable.StateListDrawable;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.KeyEvent;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.Map;

/** Reusable inset tabs on one rounded track. Callers own selection and content. */
class NativeTabs extends FrameLayout {
    enum Size { REGULAR, COMPACT }
    final LinearLayout tabs;
    final NativeSurface track;
    final HorizontalScrollView scroll;
    private final NativeUi ui;
    private final Size size;
    private final Map<String, Button> buttons = new LinkedHashMap<>();
    private String selection;
    private boolean revealSelection = true;

    NativeTabs(NativeUi ui, String label) {
        this(ui, label, Size.REGULAR);
    }

    NativeTabs(NativeUi ui, String label, Size size) {
        super(ui.context); this.ui = ui; this.size = size;
        track = new NativeSurface(ui, ui.soft, ui.line, 8, 0, 0, 0);
        addView(track, new FrameLayout.LayoutParams(-1, -2));
        scroll = new HorizontalScrollView(ui.context);
        scroll.setHorizontalScrollBarEnabled(false); scroll.setFillViewport(true);
        scroll.setOverScrollMode(OVER_SCROLL_NEVER); scroll.setContentDescription(label);
        track.addView(scroll, new LinearLayout.LayoutParams(-1, -2));
        tabs = ui.row(); tabs.setGravity(Gravity.CENTER_VERTICAL);
        tabs.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        scroll.addView(tabs, new HorizontalScrollView.LayoutParams(-2, -2));
    }

    Button addTab(String key, String label, Runnable choose) {
        Button tab = ui.button(label, false, choose);
        tab.setTag(key); tab.setContentDescription(label); tab.setMinimumWidth(ui.dp(size == Size.COMPACT ? 64 : 80));
        if (size == Size.COMPACT) tab.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6));
        tab.setMaxLines(1); tab.setHorizontallyScrolling(false);
        tab.setMaxWidth(ui.dp(200)); tab.setEllipsize(TextUtils.TruncateAt.END);
        style(tab, key.equals(selection));
        tab.setOnKeyListener((view, code, event) -> {
            int direction = code == KeyEvent.KEYCODE_DPAD_LEFT ? -1 : code == KeyEvent.KEYCODE_DPAD_RIGHT ? 1 : 0;
            if (direction == 0 || !event.hasNoModifiers()) return false;
            if (event.getAction() == KeyEvent.ACTION_DOWN && event.getRepeatCount() == 0) {
                ArrayList<Button> order = new ArrayList<>(buttons.values());
                int index = order.indexOf(tab), next = Math.floorMod(index + direction, order.size());
                Button target = order.get(next);
                target.requestFocus(); target.performClick();
            }
            return true;
        });
        buttons.put(key, tab);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, ui.dp(size == Size.COMPACT ? 32 : 40), 1);
        params.setMarginEnd(ui.dp(4));
        tabs.addView(tab, params);
        return tab;
    }

    NativeUi.IconButton addAction(String icon, String label, Runnable click) {
        NativeUi.IconButton button = ui.icon(icon, 18, label, click);
        button.setBackground(buttonBackground(false));
        int height = ui.dp(size == Size.COMPACT ? 32 : 40);
        tabs.addView(button, new LinearLayout.LayoutParams(height, height));
        return button;
    }

    Button button(String key) { return buttons.get(key); }

    void select(String key) {
        if (key.equals(selection)) return;
        selection = key;
        for (Map.Entry<String, Button> entry : buttons.entrySet()) style(entry.getValue(), key.equals(entry.getKey()));
        revealSelection = true; requestLayout();
    }

    private void style(Button tab, boolean selected) {
        tab.setSelected(selected); tab.setBackground(buttonBackground(selected));
        ui.typography(tab, size == Size.COMPACT ? 11 : 12, selected && ui.dark ? ui.thumb : selected || !ui.dark ? ui.text : ui.muted, selected ? 600 : 400);
    }

    private Drawable buttonBackground(boolean selected) {
        int fill = selected ? ui.dark ? ui.sidebar : ui.elevated : 0x00000000;
        int border = selected && ui.dark ? ui.borderStrong : 0;
        StateListDrawable background = new StateListDrawable();
        background.addState(new int[] {-android.R.attr.state_enabled}, ui.shape(ui.controlActive, 4, border));
        background.addState(new int[] {android.R.attr.state_pressed}, ui.shape(selected ? fill : ui.hover, 4, border));
        background.addState(new int[] {android.R.attr.state_hovered}, ui.shape(selected ? fill : ui.controlHover, 4, border));
        background.addState(new int[] {android.R.attr.state_focused}, ui.shape(selected ? fill : ui.hover, 4, ui.dark ? ui.borderStrong : ui.primary));
        background.addState(new int[0], ui.shape(fill, 4, border));
        return background;
    }

    @Override protected void onSizeChanged(int width, int height, int previousWidth, int previousHeight) {
        super.onSizeChanged(width, height, previousWidth, previousHeight);
        if (width != previousWidth) revealSelection = true;
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        if (!revealSelection) return;
        Button selected = buttons.get(selection);
        int viewport = scroll.getWidth();
        if (selected == null || viewport <= 0) return;
        int target = scroll.getScrollX();
        if (selected.getLeft() < target) target = selected.getLeft();
        else if (selected.getRight() > target + viewport) target = selected.getRight() - viewport;
        scroll.scrollTo(Math.max(0, Math.min(target, tabs.getWidth() - viewport)), 0);
        revealSelection = false;
    }
}
