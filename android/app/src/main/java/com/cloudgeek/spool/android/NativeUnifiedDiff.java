package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.Typeface;
import android.view.View;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** One custom-painted unified column; only the rows in the viewport are drawn. */
final class NativeUnifiedDiff extends View {
    static final class Row {
        final String text; final char kind; final int old, next;
        Row(String text, char kind, int old, int next) { this.text = text; this.kind = kind; this.old = old; this.next = next; }
    }
    final List<Row> rows = new ArrayList<>();
    private final NativeUi ui;
    private final NativeDiffPalette palette;
    private final Paint text = new Paint(Paint.ANTI_ALIAS_FLAG), fill = new Paint();
    private final Rect clip = new Rect();
    private final int lineHeight, gutter, contentWidth;
    NativeUnifiedDiff(NativeUi ui, String patch) {
        super(ui.context); this.ui = ui; palette = new NativeDiffPalette(ui.dark);
        text.setTypeface(Typeface.MONOSPACE); text.setTextSize(11 * getResources().getDisplayMetrics().scaledDensity);
        text.setSubpixelText(true); text.setLinearText(true); lineHeight = ui.dp(20); gutter = Math.round(text.measureText("0") * 12);
        Pattern pattern = Pattern.compile("^@@ -(\\d+)(?:,\\d+)? \\+(\\d+)(?:,\\d+)? @@");
        int old = 0, next = 0; boolean hunk = false; float width = 0;
        for (String value : patch.replaceFirst("\\n$", "").split("\n", -1)) {
            Matcher match = pattern.matcher(value); char kind = 'm'; String body = value; int before = 0, after = 0;
            if (match.find()) {
                if (!rows.isEmpty()) rows.add(new Row("⋯", 'm', 0, 0));
                old = Integer.parseInt(match.group(1)); next = Integer.parseInt(match.group(2)); hunk = true;
            }
            else if (value.startsWith("diff --git ")) hunk = false;
            else if (hunk && !value.isEmpty()) {
                char prefix = value.charAt(0);
                if (prefix == '+' || prefix == '-' || prefix == ' ') {
                    kind = prefix; body = value.substring(1);
                    if (prefix != '+') before = old++;
                    if (prefix != '-') after = next++;
                }
            }
            if (kind != 'm') { rows.add(new Row(body, kind, before, after)); width = Math.max(width, NativeCodeTabs.width(text, body, body.length())); }
        }
        contentWidth = Math.round(width) + gutter + ui.dp(12);
        StringBuilder accessible = new StringBuilder(ui.t("Git diff"));
        for (Row row : rows) accessible.append('\n').append(row.kind == 'm' ? ' ' : row.kind).append(row.text);
        setContentDescription(accessible);
    }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        setMeasuredDimension(resolveSize(contentWidth, widthSpec), resolveSize(ui.dp(32) + rows.size() * lineHeight, heightSpec));
    }
    @Override protected void onDraw(Canvas canvas) {
        canvas.getClipBounds(clip); int first = Math.max(0, (clip.top - ui.dp(8)) / lineHeight);
        for (int i = first; i < rows.size(); i++) {
            int top = ui.dp(8) + i * lineHeight; if (top > clip.bottom) break;
            Row row = rows.get(i); fill.setColor(row.kind == '+' ? palette.additionBg : row.kind == '-' ? palette.deletionBg : row.kind == 'm' ? ui.soft : ui.elevated);
            canvas.drawRect(0, top, getWidth(), top + lineHeight, fill);
            float baseline = top + (lineHeight - text.ascent() - text.descent()) / 2;
            float ch = text.measureText("0"); text.setColor(ui.muted);
            if (row.old > 0) canvas.drawText(String.valueOf(row.old), ch, baseline, text);
            if (row.next > 0) canvas.drawText(String.valueOf(row.next), ch * 5, baseline, text);
            if (row.kind == '+' || row.kind == '-') canvas.drawText(String.valueOf(row.kind), ch * 10, baseline, text);
            text.setColor(row.kind == 'm' ? ui.muted : ui.text); NativeCodeTabs.draw(canvas, text, row.text, gutter, baseline);
        }
    }
}
