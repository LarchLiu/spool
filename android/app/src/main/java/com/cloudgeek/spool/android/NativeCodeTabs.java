package com.cloudgeek.spool.android;

import android.graphics.Paint;
import android.graphics.Canvas;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.TabStopSpan;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;

/** CSS tab-size: 2 for selectable monospace text, without changing its copied bytes. */
final class NativeCodeTabs {
    private NativeCodeTabs() {}

    static void setText(TextView view, String text) {
        setText(view, text, view.getTextSize());
    }

    static void setText(TextView view, String text, float containerTextSize) {
        if (text.indexOf('\t') < 0) { view.setText(text); return; }
        Paint paint = view.getPaint();
        float[] widths = new float[1]; paint.getTextWidths(" ", widths);
        float interval = widths[0] * 2 * containerTextSize / view.getTextSize();
        if (interval <= 0) { view.setText(text); return; }
        SpannableStringBuilder source = new SpannableStringBuilder(text);
        List<Integer> stops = new ArrayList<>();
        int paragraph = 0, segment = 0;
        float advance = 0;
        for (int i = 0; i <= text.length(); i++) {
            if (i == text.length() || text.charAt(i) == '\n') {
                int end = i < text.length() ? i + 1 : i;
                // Paragraph-local stops avoid rounding from another line changing this line's tabs.
                for (int stop : stops) source.setSpan(new TabStopSpan.Standard(stop), paragraph, end, Spanned.SPAN_PARAGRAPH);
                stops.clear(); paragraph = segment = end; advance = 0;
            } else if (text.charAt(i) == '\t') {
                if (i > segment) advance += paint.getRunAdvance(text, segment, i, segment, i, false, i);
                advance = nextStop(advance, interval);
                stops.add(Math.round(advance)); segment = i + 1;
            }
        }
        view.setText(source);
    }

    private static float nextStop(float advance, float interval) {
        float next = ((float) Math.floor(advance / interval + .00001f) + 1) * interval;
        // CSS advances to a later stop when the remaining gap is less than half a space.
        return next - advance < interval / 4 - .00001f ? next + interval : next;
    }

    static float width(Paint paint, String text, int end) {
        return render(null, paint, text, end, 0, 0);
    }

    static void draw(Canvas canvas, Paint paint, String text, float x, float baseline) {
        render(canvas, paint, text, text.length(), x, baseline);
    }

    static void drawRange(Canvas canvas, Paint paint, String text, int from, int end, float x, float baseline) {
        float[] widths = new float[1]; paint.getTextWidths(" ", widths); float interval = widths[0] * 2;
        float advance = 0; int segment = 0;
        for (int i = 0; i <= text.length(); i++) if (i == text.length() || text.charAt(i) == '\t') {
            int start = Math.max(segment, from), limit = Math.min(i, end);
            if (start < limit) {
                float prefix = paint.getRunAdvance(text, segment, i, segment, i, false, start);
                canvas.drawTextRun(text, start, limit, segment, i, x + advance + prefix, baseline, false, paint);
            }
            if (i >= end) return;
            if (i > segment) advance += paint.getRunAdvance(text, segment, i, segment, i, false, i);
            if (interval > 0) advance = nextStop(advance, interval);
            segment = i + 1;
        }
    }

    private static float render(Canvas canvas, Paint paint, String text, int end, float x, float baseline) {
        float[] widths = new float[1]; paint.getTextWidths(" ", widths); float interval = widths[0] * 2;
        float advance = 0; int segment = 0;
        for (int i = 0; i <= end; i++) if (i == end || text.charAt(i) == '\t') {
            if (i > segment) {
                if (canvas != null) canvas.drawText(text, segment, i, x + advance, baseline, paint);
                advance += paint.getRunAdvance(text, segment, i, segment, i, false, i);
            }
            if (i < end && interval > 0) advance = nextStop(advance, interval);
            segment = i + 1;
        }
        return advance;
    }
}
