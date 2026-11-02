package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Path;
import android.text.Layout;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.ClickableSpan;
import android.text.style.LineBackgroundSpan;
import android.text.style.MetricAffectingSpan;
import android.text.style.ReplacementSpan;
import android.view.View;
import android.widget.TextView;
import java.util.function.Consumer;
import java.lang.ref.WeakReference;

/** CSS link ink is independent of Android's font-positioned native underline. */
final class NativeMarkdownLink extends ClickableSpan implements LineBackgroundSpan {
    private final WeakReference<TextView> view;
    private final int color;
    private final float density;
    private final String target;
    private final Consumer<String> open;
    private final Paint ink = new Paint(Paint.ANTI_ALIAS_FLAG);

    NativeMarkdownLink(TextView owner, int color, String target, Consumer<String> open) {
        view = new WeakReference<>(owner); this.color = color; this.target = target; this.open = open;
        density = owner.getResources().getDisplayMetrics().density;
    }

    @Override public void onClick(View view) { open.accept(target); }
    @Override public void updateDrawState(TextPaint paint) { paint.setColor(color); paint.setUnderlineText(false); }

    private float thickness(TextView owner) { return Math.max(1, (float) Math.floor(owner.getTextSize() / density / 10)) * density; }

    private float top(TextView owner, float baseline) {
        float y = NativeUi.cssTextOrigin(owner);
        return Math.round((baseline + y) / density + 3) * density - y;
    }

    @Override public void drawBackground(Canvas canvas, Paint paint, int left, int right, int top,
            int baseline, int bottom, CharSequence value, int start, int end, int line) {
        TextView owner = view.get();
        if (!(value instanceof Spanned) || owner == null || owner.getLayout() == null) return;
        Spanned text = (Spanned) value;
        start = Math.max(start, text.getSpanStart(this)); end = Math.min(end, text.getSpanEnd(this));
        while (end > start && value.charAt(end - 1) == '\n') end--;
        if (end <= start) return;
        Layout layout = owner.getLayout();
        float y = top(owner, baseline), thickness = thickness(owner);
        Path selected = selection(layout, start, end, line);
        if (selected.isEmpty()) return;
        android.graphics.RectF bounds = new android.graphics.RectF(); selected.computeBounds(bounds, true);
        int save = canvas.save(); canvas.clipPath(selected);
        for (ReplacementSpan replacement : text.getSpans(start, end, ReplacementSpan.class)) {
            Path box = new Path(); layout.getSelectionPath(Math.max(start, text.getSpanStart(replacement)), Math.min(end, text.getSpanEnd(replacement)), box);
            canvas.clipOutPath(box);
        }
        skipInk(canvas, owner, text, start, end, line, baseline, y);
        ink.setColor(color);
        canvas.drawRect(bounds.left, y, bounds.right, y + thickness, ink);
        canvas.restoreToCount(save);
    }

    private static Path selection(Layout layout, int start, int end, int line) {
        Path selected = new Path(); layout.getSelectionPath(start, end, selected);
        float width = layout.getLineWidth(line);
        float left = layout.getParagraphDirection(line) < 0 ? layout.getLineRight(line) - width : layout.getLineLeft(line);
        // A selection crossing a soft wrap extends to the paragraph edge, not the final glyph/space.
        Path band = new Path(); band.addRect(left, layout.getLineTop(line), left + width, layout.getLineBottom(line), Path.Direction.CW);
        selected.op(band, Path.Op.INTERSECT);
        return selected;
    }

    private void skipInk(Canvas canvas, TextView owner, Spanned text, int start, int end, int line, int baseline, float y) {
        float rawThickness = Math.max(1, owner.getTextSize() / density / 10) * density;
        // Blink ignores intersections shallower than half a CSS pixel on either side.
        float upper = y + density / 2, lower = y + rawThickness - density / 2;
        if (lower <= upper) return;
        for (int cursor = start; cursor < end;) {
            int next = text.nextSpanTransition(cursor, end, MetricAffectingSpan.class);
            boolean cjk = cjkAt(text, cursor);
            for (int boundary = cursor + Character.charCount(Character.codePointAt(text, cursor)); boundary < next;) {
                int after = boundary + Character.charCount(Character.codePointAt(text, boundary));
                if (cjkAt(text, boundary) != cjk) { next = boundary; break; }
                boundary = after;
            }
            TextPaint paint = new TextPaint(owner.getPaint()); boolean replaced = false;
            for (MetricAffectingSpan span : text.getSpans(cursor, next, MetricAffectingSpan.class)) {
                if (span instanceof ReplacementSpan) replaced = true;
                else span.updateMeasureState(paint);
            }
            if (!replaced && !cjk) {
                Path glyphs = new Path(), stripe = new Path();
                android.graphics.RectF run = new android.graphics.RectF();
                selection(owner.getLayout(), cursor, next, line).computeBounds(run, true);
                float x = run.left;
                paint.getTextPath(text.toString(), cursor, next, x, baseline, glyphs);
                stripe.addRect(-owner.getWidth() * 2f, upper, owner.getWidth() * 2f, lower, Path.Direction.CW);
                if (glyphs.op(stripe, Path.Op.INTERSECT)) {
                    float[] points = glyphs.approximate(.01f * density);
                    float min = Float.POSITIVE_INFINITY, max = Float.NEGATIVE_INFINITY, fraction = -1;
                    for (int i = 0; i < points.length; i += 3) {
                        if (points[i] == fraction && min <= max) { canvas.clipOutRect(min - rawThickness, y - density, max + rawThickness, y + rawThickness + density); min = Float.POSITIVE_INFINITY; max = Float.NEGATIVE_INFINITY; }
                        min = Math.min(min, points[i + 1]); max = Math.max(max, points[i + 1]); fraction = points[i];
                    }
                    if (min <= max) canvas.clipOutRect(min - rawThickness, y - density, max + rawThickness, y + rawThickness + density);
                }
            }
            cursor = next;
        }
    }

    private static boolean cjkAt(CharSequence text, int at) {
        Character.UnicodeScript script = Character.UnicodeScript.of(Character.codePointAt(text, at));
        return script == Character.UnicodeScript.HAN || script == Character.UnicodeScript.HIRAGANA
                || script == Character.UnicodeScript.KATAKANA || script == Character.UnicodeScript.HANGUL;
    }

    void code(Canvas canvas, float left, float right, float baseline) {
        TextView owner = view.get(); if (owner == null) return;
        ink.setColor(color); float y = top(owner, baseline);
        canvas.drawRect(left, y, right, y + thickness(owner), ink);
    }

    int color() { return color; }
}
