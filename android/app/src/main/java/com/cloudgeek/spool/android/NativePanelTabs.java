package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.KeyEvent;
import android.widget.LinearLayout;
import java.util.LinkedHashMap;
import java.util.Map;

/** Icon/text panel tabs on a thin baseline, with an underline for the selected item. */
final class NativePanelTabs extends LinearLayout {
    private final NativeUi ui;
    private final Paint line = new Paint();
    private final Map<String, NativePanelAction> actions = new LinkedHashMap<>();
    private final boolean underline;
    NativePanelTabs(NativeUi ui, String label) {
        this(ui, label, true);
    }
    NativePanelTabs(NativeUi ui, String label, boolean underline) {
        super(ui.context); this.ui = ui; setOrientation(HORIZONTAL); setWillNotDraw(false);
        this.underline = underline;
        setPadding(ui.dp(10), ui.dp(underline ? 0 : 6), ui.dp(10), ui.dp(underline ? 0 : 6)); setContentDescription(label);
    }
    void add(String key, String label, String icon, Runnable choose) {
        NativePanelAction action = new NativePanelAction(ui, label, choose); action.tab = true; action.underline = underline;
        if (!underline) {
            action.setGravity(android.view.Gravity.CENTER);
            android.graphics.drawable.StateListDrawable background = new android.graphics.drawable.StateListDrawable();
            background.addState(new int[]{android.R.attr.state_pressed}, ui.shape(ui.controlActive, 5, 0));
            background.addState(new int[]{android.R.attr.state_selected}, ui.shape(ui.selected, 5, 0));
            background.addState(new int[]{android.R.attr.state_hovered}, ui.shape(ui.hover, 5, 0));
            background.addState(new int[0], ui.shape(0, 5, 0));
            action.setBackground(background);
        }
        if (!icon.isEmpty()) {
            action.setCompoundDrawables(NativeIcons.drawable(ui.context, icon, ui.muted, ui.dp(14)), null, null, null);
            action.setCompoundDrawablePadding(ui.dp(6));
        }
        action.setOnKeyListener((view, code, event) -> {
            if (event.getAction() != KeyEvent.ACTION_DOWN || !event.hasNoModifiers()) return false;
            java.util.List<NativePanelAction> buttons = new java.util.ArrayList<>(actions.values());
            int index = buttons.indexOf(action), next = code == KeyEvent.KEYCODE_DPAD_RIGHT ? (index + 1) % buttons.size()
                : code == KeyEvent.KEYCODE_DPAD_LEFT ? Math.floorMod(index - 1, buttons.size())
                : code == KeyEvent.KEYCODE_MOVE_HOME ? 0 : code == KeyEvent.KEYCODE_MOVE_END ? buttons.size() - 1 : -1;
            if (next < 0) { if (code == KeyEvent.KEYCODE_SPACE) { action.performClick(); return true; } return false; }
            buttons.get(next).requestFocus(); buttons.get(next).performClick(); return true;
        });
        actions.put(key, action); LayoutParams params = new LayoutParams(underline ? -2 : 0, ui.dp(underline ? 40 : 30), underline ? 0 : 1); params.setMarginEnd(ui.dp(underline ? 6 : 4)); addView(action, params);
    }
    void select(String key) {
        for (Map.Entry<String, NativePanelAction> entry : actions.entrySet()) {
            boolean selected = key.equals(entry.getKey()); NativePanelAction action = entry.getValue();
            action.setSelected(selected); ui.typography(action, 12, selected ? ui.text : ui.muted, selected ? 600 : 400);
            action.setCompoundDrawableTintList(android.content.res.ColorStateList.valueOf(selected ? ui.text : ui.muted));
        }
    }
    NativePanelAction action(String key) { return actions.get(key); }
    @Override protected void onDraw(Canvas canvas) { super.onDraw(canvas); line.setColor(ui.line); canvas.drawRect(0, getHeight() - ui.dp(1), getWidth(), getHeight(), line); }
}
