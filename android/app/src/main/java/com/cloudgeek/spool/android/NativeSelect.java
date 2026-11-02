package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;

/** Reusable CustomSelect trigger with the Web listbox dimensions, disabled state and focus return. */
final class NativeSelect extends LinearLayout {
    private final NativeUi ui;
    private final String[] values, labels;
    private final Consumer<String> change;
    private final TextView selected;
    private String value;
    private NativePopover menu;

    NativeSelect(NativeUi ui, String label, String value, String[] values, String[] labels,
            boolean languageChoice, Consumer<String> change) {
        super(ui.context);
        if (values.length != labels.length) throw new IllegalArgumentException("Mismatched options");
        this.ui = ui;
        this.values = values.clone();
        this.labels = labels.clone();
        this.change = change;
        setGravity(Gravity.CENTER_VERTICAL);
        int vertical = languageChoice ? 11 : 9;
        setPadding(ui.dp(12), ui.dp(vertical), ui.dp(12), ui.dp(vertical));
        setMinimumHeight(ui.dp(34));
        setBackground(ui.states(ui.input, 9, ui.line, ui.input, ui.input));
        selected = ui.text("", 12, ui.text);
        ui.typography(selected, 12, ui.text, 500);
        ui.fixedLineHeight(selected, 1.2f);
        selected.setSingleLine(true);
        selected.setEllipsize(android.text.TextUtils.TruncateAt.END);
        addView(selected, new LayoutParams(0, -2, 1));
        LayoutParams chevron = new LayoutParams(ui.dp(14), ui.dp(14));
        chevron.setMarginStart(ui.dp(12));
        android.widget.ImageView icon = ui.decorativeIcon("chevron-down", 14);
        icon.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
        addView(icon, chevron);
        setFocusable(true);
        setFocusableInTouchMode(true);
        setContentDescription(label);
        setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info);
                info.setClassName("android.widget.Spinner");
                info.setText(selected.getText());
                info.setCanOpenPopup(true);
            }
        });
        bind(value);
        setOnClickListener(view -> {
            if (menu != null && menu.isShowing()) menu.dismiss();
            else open(false);
        });
        setOnKeyListener((view, key, event) -> {
            if (event.getAction() != KeyEvent.ACTION_DOWN || (key != KeyEvent.KEYCODE_DPAD_DOWN && key != KeyEvent.KEYCODE_DPAD_UP)) return false;
            open(key == KeyEvent.KEYCODE_DPAD_UP);
            return true;
        });
    }

    void bind(String value) {
        this.value = value;
        String label = value;
        for (int i = 0; i < values.length; i++) if (values[i].equals(value)) label = labels[i];
        selected.setText(label);
    }

    void open(boolean last) {
        if (!isEnabled() || values.length == 0) return;
        menu = new NativePopover(ui, 180, 10, 5);
        int initial = last ? values.length - 1 : 0;
        for (int i = 0; i < values.length; i++) {
            String option = values[i];
            boolean current = option.equals(value);
            if (current) initial = i;
            menu.selectOption(labels[i], current, () -> {
                menu.dismiss();
                requestFocus();
                if (!option.equals(value)) change.accept(option);
            });
        }
        menu.showSelect(this, initial);
    }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        setAlpha(enabled ? 1 : .45f);
        if (!enabled && menu != null) menu.dismiss();
    }
}
