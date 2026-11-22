package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.Rect;
import android.graphics.Region;
import android.graphics.RegionIterator;
import android.text.Layout;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.MetricAffectingSpan;
import android.widget.TextView;

/** Paint-only code decoration: native text keeps its wrapping, selection and copied bytes. */
final class NativeInlineCodeBackground {
    private final int color;
    private final float radius;

    NativeInlineCodeBackground(NativeUi ui) { color = ui.soft; radius = ui.dp(4); }

    static void draw(TextView view, Canvas canvas) {
        Layout layout = view.getLayout();
        if (layout == null || !(layout.getText() instanceof Spanned)) return;
        Spanned text = (Spanned) layout.getText();
        NativeInlineCodeBackground[] spans = text.getSpans(0, text.length(), NativeInlineCodeBackground.class);
        if (spans.length == 0) return;
        int save = canvas.save();
        canvas.translate(view.getTotalPaddingLeft(), view.getTotalPaddingTop());
        Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG);
        TextPaint code = new TextPaint();
        Path selection = new Path();
        Region region = new Region();
        Rect bounds = new Rect();
        Region clip = new Region(0, 0, layout.getWidth(), layout.getHeight());
        try {
            for (NativeInlineCodeBackground span : spans) {
                int start = text.getSpanStart(span), end = text.getSpanEnd(span);
                if (start < 0 || end <= start) continue;
                fill.setColor(span.color);
                for (int line = layout.getLineForOffset(start); line <= layout.getLineForOffset(end - 1); line++) {
                    int from = Math.max(start, layout.getLineStart(line)), to = Math.min(end, layout.getLineEnd(line));
                    if (to > from && text.charAt(to - 1) == '\n') to--;
                    if (to <= from) continue;
                    code.set(view.getPaint());
                    for (MetricAffectingSpan style : text.getSpans(from, Math.min(from + 1, to), MetricAffectingSpan.class))
                        style.updateMeasureState(code);
                    float padding = code.getTextSize() * .15f;
                    float top = layout.getLineBaseline(line) + code.ascent() - padding;
                    float bottom = layout.getLineBaseline(line) + code.descent() + padding;
                    // Selection geometry already accounts for wrapping, list indents and
                    // bidi runs. Only its horizontal extents belong to the decoration;
                    // the font metrics determine height, independently of line spacing.
                    selection.reset(); layout.getSelectionPath(from, to, selection);
                    region.setPath(selection, clip);
                    RegionIterator fragments = new RegionIterator(region);
                    while (fragments.next(bounds)) {
                        // Selection extends to the viewport edge at a soft wrap;
                        // the background ends with this line's actual text.
                        float left = Math.max(bounds.left, layout.getLineLeft(line));
                        float right = Math.min(bounds.right, layout.getLineRight(line));
                        if (right > left) canvas.drawRoundRect(left, top, right, bottom, span.radius, span.radius, fill);
                    }
                }
            }
        } finally { canvas.restoreToCount(save); }
    }
}
