package com.cloudgeek.spool.android;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.LinearGradient;
import android.graphics.Matrix;
import android.graphics.Paint;
import android.graphics.Shader;
import android.text.Layout;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.StaticLayout;
import android.text.TextDirectionHeuristics;
import android.text.TextPaint;
import java.util.List;

/** Pierre-style nonselectable filename segments: clip whole runs, never replace source characters. */
class NativeMiddleTruncate extends NativeTextView {
    static final class Segment {
        final CharSequence text;
        final boolean fromEnd;
        final float x, width, natural, markerWidth;
        final boolean markerVisible;
        final StaticLayout layout;
        Segment(CharSequence text, boolean fromEnd, float x, float width, float natural, float markerWidth,
                boolean markerVisible, StaticLayout layout) {
            this.text = text; this.fromEnd = fromEnd; this.x = x; this.width = width; this.natural = natural;
            this.markerWidth = markerWidth; this.markerVisible = markerVisible; this.layout = layout;
        }
    }

    private final class Marker {
        boolean visible;
        float alpha;
        ValueAnimator animation;
        void show(boolean next) {
            if (next == visible) return;
            if (animation != null) { animation.cancel(); animation = null; }
            visible = next; alpha = next ? 1 : 0;
            if (next && isAttachedToWindow() && isShown() && ValueAnimator.areAnimatorsEnabled()) {
                alpha = 0;
                animation = ValueAnimator.ofFloat(0, 1); animation.setDuration(100);
                animation.setInterpolator(NativeCssTiming.EASE_IN_OUT);
                animation.addUpdateListener(value -> { alpha = (float) value.getAnimatedValue(); invalidate(); });
                animation.start();
            }
        }
        void reset() {
            if (animation != null) animation.cancel();
            animation = null; visible = false; alpha = 0;
        }
    }

    private List<Segment> segments;
    private StaticLayout ellipsis;
    private final Marker[] markers = {new Marker(), new Marker()};
    private final Paint cover = new Paint();
    private final Matrix fadeMatrix = new Matrix();
    private final Shader[] fades = new Shader[2];
    private int markerBackground;
    private int structure;

    NativeMiddleTruncate(Context context) {
        super(context);
        getPaint().setLinearText(true); getPaint().setSubpixelText(true);
        setMarkerBackground(0);
    }

    void setMarkerBackground(int color) {
        if (markerBackground == color && fades[0] != null) return;
        markerBackground = color;
        fades[0] = new LinearGradient(0, 0, 1, 0, color & 0xffffff, color, Shader.TileMode.CLAMP);
        fades[1] = new LinearGradient(0, 0, 1, 0, color, color & 0xffffff, Shader.TileMode.CLAMP);
    }

    private static boolean boundarySpace(char c) {
        return c >= 9 && c <= 13 || c == 32 || c == 0xa0 || c == 0x1680 || c >= 0x2000 && c <= 0x200a
                || c == 0x2028 || c == 0x2029 || c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff;
    }

    private static boolean boundary(CharSequence text, int index) {
        return index > 0 && boundarySpace(text.charAt(index - 1)) || index < text.length() && boundarySpace(text.charAt(index));
    }

    static int extensionSplit(CharSequence text) {
        if (text.length() < 4) return text.length();
        int extension = text.toString().lastIndexOf('.') + 1;
        if (extension >= 1 && text.length() - extension <= 10) return extension;
        int center = (text.length() + 1) / 2;
        if (!boundary(text, center)) return center;
        for (int offset = 1; offset < text.length(); offset++) {
            int before = center - offset, after = center + offset;
            if (before > 0 && !boundary(text, before)) return before;
            if (after < text.length() && !boundary(text, after)) return after;
        }
        return center;
    }

    private static boolean collapsingSpace(char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'; }

    private static CharSequence collapse(CharSequence text) {
        SpannableStringBuilder result = new SpannableStringBuilder();
        int[] offsets = new int[text.length() + 1];
        for (int i = 0; i < text.length();) {
            offsets[i] = result.length();
            if (!collapsingSpace(text.charAt(i))) { result.append(text.charAt(i)); offsets[++i] = result.length(); continue; }
            int start = i;
            while (i < text.length() && collapsingSpace(text.charAt(i))) i++;
            if (result.length() > 0 && i < text.length()) result.append(' ');
            for (int j = start + 1; j <= i; j++) offsets[j] = result.length();
        }
        if (text.toString().contentEquals(result)) return text;
        if (text instanceof Spanned) {
            Spanned source = (Spanned) text;
            for (Object span : source.getSpans(0, source.length(), Object.class)) {
                int start = offsets[source.getSpanStart(span)], end = offsets[source.getSpanEnd(span)];
                if (start < end && !(span instanceof android.text.NoCopySpan)) result.setSpan(span, start, end, source.getSpanFlags(span));
            }
        }
        return result;
    }

    private StaticLayout textLayout(CharSequence text, float width, boolean fromEnd) {
        return StaticLayout.Builder.obtain(text, 0, text.length(), new TextPaint(getPaint()), Math.max(1, (int) Math.ceil(width)))
                .setIncludePad(false).setTextDirection(fromEnd ? TextDirectionHeuristics.FIRSTSTRONG_LTR : TextDirectionHeuristics.LTR)
                .setHyphenationFrequency(Layout.HYPHENATION_FREQUENCY_NONE).build();
    }

    private float advance(CharSequence text) {
        float density = getResources().getDisplayMetrics().density;
        return Math.round(Layout.getDesiredWidth(text, getPaint()) / density * 64) / 64f * density;
    }

    private Segment segment(CharSequence text, boolean fromEnd, float x, float width, float natural) {
        CharSequence display = collapse(text);
        float raw = Layout.getDesiredWidth(display, getPaint());
        // Layout is deliberately wider than the run; clipping belongs to the CSS segment, not line breaking.
        StaticLayout layout = textLayout(display, raw + getTextSize(), fromEnd);
        boolean overflow = natural > width && textLayout(display, width, fromEnd).getLineCount() > 1;
        return new Segment(text, fromEnd, x, width, natural, advance("\u2026"), overflow, layout);
    }

    List<Segment> overflowSegments() {
        if (segments != null) return segments;
        CharSequence source = getText();
        float available = Math.max(0, getWidth() - getCompoundPaddingLeft() - getCompoundPaddingRight());
        if (source.length() < 5) {
            float natural = advance(collapse(source));
            segments = List.of(segment(source, true, 0, Math.min(available, natural), natural));
        } else {
            int split = extensionSplit(source);
            CharSequence first = source.subSequence(0, split), last = source.subSequence(split, source.length());
            float a = advance(collapse(first)), b = advance(collapse(last)), firstWidth = a, lastWidth = b;
            if (a + b > available) {
                double weight = a * 999999d + b;
                firstWidth = (float) Math.max(0, a - (a + b - available) * (a * 999999d / weight));
                float density = getResources().getDisplayMetrics().density;
                firstWidth = Math.round(firstWidth / density * 64) / 64f * density;
                lastWidth = Math.max(0, available - firstWidth);
            }
            segments = List.of(segment(first, false, 0, firstWidth, a), segment(last, true, firstWidth, lastWidth, b));
        }
        if (structure != segments.size()) { for (Marker marker : markers) marker.reset(); structure = segments.size(); }
        for (int i = 0; i < segments.size(); i++) markers[i].show(segments.get(i).markerVisible);
        ellipsis = textLayout("\u2026", advance("\u2026") + getTextSize(), false);
        return segments;
    }

    private void drawLayout(Canvas canvas, StaticLayout layout, float x, int color) {
        layout.getPaint().setColor(color);
        int save = canvas.save();
        canvas.translate(x - layout.getLineLeft(0), getBaseline() - layout.getLineBaseline(0));
        layout.draw(canvas); canvas.restoreToCount(save);
    }

    @Override protected void onDraw(Canvas canvas) {
        List<Segment> current = overflowSegments();
        if (getText().length() == 0 || current.size() == 1 && current.get(0).natural <= current.get(0).width
                && getText().toString().contentEquals(current.get(0).layout.getText())) {
            super.onDraw(canvas); return;
        }
        float density = getResources().getDisplayMetrics().density;
        for (int i = 0; i < current.size(); i++) {
            Segment segment = current.get(i);
            if (segment.width <= 0) continue;
            float left = getCompoundPaddingLeft() + segment.x, right = left + segment.width;
            int save = canvas.save(); canvas.clipRect(left, 0, right, getHeight());
            drawLayout(canvas, segment.layout, left + (segment.fromEnd ? segment.width - segment.natural : 0), getCurrentTextColor());
            float alpha = markers[i].alpha;
            if (alpha > 0) {
                float markerLeft = segment.fromEnd ? left : right - segment.markerWidth, markerRight = markerLeft + segment.markerWidth;
                int background = markerBackground & 0xffffff | Math.round((markerBackground >>> 24) * alpha) << 24;
                cover.setShader(null); cover.setColor(background); canvas.drawRect(markerLeft, 0, markerRight, getHeight(), cover);
                cover.setAlpha(Math.round(255 * alpha));
                fadeMatrix.setScale(2 * density, 1); fadeMatrix.postTranslate(markerLeft - 2 * density, 0);
                fades[0].setLocalMatrix(fadeMatrix); cover.setShader(fades[0]);
                canvas.drawRect(markerLeft - 2 * density, 0, markerLeft, getHeight(), cover);
                fadeMatrix.setScale(2 * density, 1); fadeMatrix.postTranslate(markerRight, 0);
                fades[1].setLocalMatrix(fadeMatrix); cover.setShader(fades[1]);
                canvas.drawRect(markerRight, 0, markerRight + 2 * density, getHeight(), cover); cover.setShader(null);
                int text = getCurrentTextColor();
                int color = text & 0xffffff | Math.round((text >>> 24) * alpha * (current.size() == 1 ? .5f : .8f)) << 24;
                drawLayout(canvas, ellipsis, markerLeft, color);
            }
            canvas.restoreToCount(save);
        }
    }

    @Override protected void onTextChanged(CharSequence text, int start, int before, int count) {
        segments = null; super.onTextChanged(text, start, before, count);
    }
    @Override protected void onMeasure(int width, int height) { super.onMeasure(width, height); segments = null; }
    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); segments = null; for (Marker marker : markers) marker.reset(); }
    @Override protected void onDetachedFromWindow() { for (Marker marker : markers) marker.reset(); segments = null; super.onDetachedFromWindow(); }
}
