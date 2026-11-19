package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Web's compact slash-command row, including skill provenance and selection. */
final class NativeSlashCommandMenu extends LinearLayout {
    final TextView title, detail, source;
    final android.widget.ImageView symbol;
    final LinearLayout copy;
    private final NativeUi ui;

    NativeSlashCommandMenu(NativeUi ui, NativeSlashCommand command, boolean selected, Runnable select) {
        super(ui.context); this.ui = ui;
        setGravity(Gravity.CENTER_VERTICAL); setMinimumHeight(ui.dp(28));
        setPadding(ui.dp(8), ui.dp(5), ui.dp(8), ui.dp(5));
        symbol = ui.decorativeIcon(command.icon(), 14);
        symbol.setImageTintList(android.content.res.ColorStateList.valueOf(ui.muted));
        addView(symbol, new LayoutParams(ui.dp(14), ui.dp(14)));
        copy = ui.row(); copy.setGravity(Gravity.CENTER_VERTICAL); copy.setBaselineAligned(true);
        title = label(command.title(ui), 13, ui.text); detail = label(command.detail(ui), 13, ui.subtle);
        copy.addView(title, new LayoutParams(-2, -2));
        LayoutParams description = new LayoutParams(0, -2, 1); description.leftMargin = ui.dp(8); copy.addView(detail, description);
        LayoutParams content = new LayoutParams(0, -2, 1); content.leftMargin = ui.dp(9); addView(copy, content);
        source = label(command.source(ui), 12, ui.subtle);
        if (command.skill() && !source.getText().toString().isEmpty()) {
            LayoutParams provenance = new LayoutParams(-2, -2); provenance.leftMargin = ui.dp(9); addView(source, provenance);
        }
        setFocusable(true); setActive(selected);
        setContentDescription("/" + command.name + (command.argumentHint.isEmpty() ? "" : " " + command.argumentHint)
                + ": " + detail.getText() + (source.getText().length() == 0 ? "" : ", " + source.getText()));
        setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button"); info.setSelected(isSelected());
            }
        });
        setOnClickListener(view -> select.run()); ui.buttonKeys(this);
    }

    private TextView label(String value, int size, int color) {
        TextView label = ui.text(value, size, color); ui.preciseText(label); ui.normalFallbackLineSpacing(label);
        label.setSingleLine(true); label.setEllipsize(TextUtils.TruncateAt.END); return label;
    }

    void setActive(boolean active) {
        setSelected(active);
        int selected = android.graphics.Color.rgb(
                Math.round(android.graphics.Color.red(ui.text) * .09f + android.graphics.Color.red(ui.elevated) * .91f),
                Math.round(android.graphics.Color.green(ui.text) * .09f + android.graphics.Color.green(ui.elevated) * .91f),
                Math.round(android.graphics.Color.blue(ui.text) * .09f + android.graphics.Color.blue(ui.elevated) * .91f));
        setBackground(ui.shape(active ? selected : android.graphics.Color.TRANSPARENT, 16, 0));
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        if (MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED) {
            int available = Math.max(0, MeasureSpec.getSize(widthSpec) - getPaddingLeft() - getPaddingRight());
            source.setMaxWidth(Math.round(available * .3f));
            source.measure(MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            int copyWidth = available - ui.dp(23) - (source.getParent() == null ? 0 : source.getMeasuredWidth() + ui.dp(9));
            title.setMaxWidth(Math.max(0, copyWidth / 2));
        }
        super.onMeasure(widthSpec, heightSpec);
    }
}
