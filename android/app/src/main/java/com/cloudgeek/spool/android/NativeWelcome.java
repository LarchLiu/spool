package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Web mobile empty state, including centered flex-wrap notes rather than overflowing labels. */
final class NativeWelcome extends LinearLayout {
    final ImageView mark;
    final TextView eyebrow, title, description;
    final ViewGroup notes;
    final TextView project, local;

    NativeWelcome(NativeUi ui, String titleText, String summary, String projectText, String note) {
        super(ui.context);
        setOrientation(VERTICAL); setGravity(Gravity.CENTER_HORIZONTAL);
        setPadding(0, ui.dp(getResources().getConfiguration().screenHeightDp * .12f), 0, ui.dp(35));
        mark = ui.logo(64); addView(mark); addView(ui.space(35));
        eyebrow = ui.eyebrow(ui.t("A little space for big ideas"), 8, .19f);
        eyebrow.setGravity(Gravity.CENTER); ui.fixedLineHeight(eyebrow, 9f / 8); ui.preciseText(eyebrow);
        addView(eyebrow, new LayoutParams(-2, -2)); addView(ui.space(20));
        title = ui.bold(titleText, 24); ui.typography(title, 24, ui.text, 650);
        ui.fixedLineHeight(title, 28f / 24); title.setLetterSpacing(-1f / 24); title.setGravity(Gravity.CENTER);
        addView(title, new LayoutParams(-1, -2)); addView(ui.space(18));
        description = ui.text(summary, 12, ui.muted); ui.fixedLineHeight(description, 1.9f); description.setGravity(Gravity.CENTER);
        addView(description, new LayoutParams(-1, -2)); addView(ui.space(30));
        project = ui.iconLabel(projectText, "folder", 8, 14); local = ui.iconLabel(note, "command", 8, 13);
        for (TextView text : new TextView[] {project, local}) {
            ui.preciseText(text); ui.fixedLineHeight(text, 1.2f); text.setGravity(Gravity.CENTER_VERTICAL);
        }
        notes = new ViewGroup(ui.context) {
            final int[] rowWidths = new int[2], rowHeights = new int[2]; int rowCount;
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                int available = MeasureSpec.getSize(widthSpec); java.util.Arrays.fill(rowWidths, 0); java.util.Arrays.fill(rowHeights, 0); rowCount = 1;
                for (int i = 0; i < getChildCount(); i++) {
                    View child = getChildAt(i); child.measure(MeasureSpec.makeMeasureSpec(available, MeasureSpec.AT_MOST), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
                    int gap = rowWidths[rowCount - 1] == 0 ? 0 : ui.dp(14);
                    if (gap > 0 && rowWidths[rowCount - 1] + gap + child.getMeasuredWidth() > available) { rowCount++; gap = 0; }
                    rowWidths[rowCount - 1] += gap + child.getMeasuredWidth(); rowHeights[rowCount - 1] = Math.max(rowHeights[rowCount - 1], child.getMeasuredHeight());
                }
                int height = ui.dp(14) * (rowCount - 1); for (int i = 0; i < rowCount; i++) height += rowHeights[i];
                setMeasuredDimension(available, resolveSize(height, heightSpec));
            }
            @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
                int row = 0, used = 0, y = 0;
                for (int i = 0; i < getChildCount(); i++) {
                    View child = getChildAt(i); int gap = used == 0 ? 0 : ui.dp(14);
                    if (used + gap + child.getMeasuredWidth() > rowWidths[row]) { y += rowHeights[row++] + ui.dp(14); used = 0; gap = 0; }
                    int x = (getWidth() - rowWidths[row]) / 2 + used + gap;
                    child.layout(x, y, x + child.getMeasuredWidth(), y + rowHeights[row]); used += gap + child.getMeasuredWidth();
                }
            }
        };
        notes.addView(project); notes.addView(local); addView(notes, new LayoutParams(-1, -2));
    }
}
