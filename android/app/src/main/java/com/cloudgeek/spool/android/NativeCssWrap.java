package com.cloudgeek.spool.android;

import android.graphics.Paint;
import android.graphics.Rect;
import android.icu.text.BreakIterator;
import android.text.GetChars;
import android.text.Layout;
import android.text.NoCopySpan;
import android.text.Spannable;
import android.text.Spanned;
import android.text.SpannedString;
import android.text.TextPaint;
import android.text.TextUtils;
import android.text.method.TransformationMethod;
import android.text.style.MetricAffectingSpan;
import android.text.style.ReplacementSpan;
import android.text.style.UpdateLayout;
import android.view.View;
import android.widget.TextView;
import java.util.BitSet;

/** CSS normal-whitespace word wrapping, retaining native text/offsets, spans and source data. */
final class NativeCssWrap implements TransformationMethod {
    private static final int PRECISION = 1024;
    private static final class Refresh implements UpdateLayout, NoCopySpan {}
    private final TextView owner;
    private final float density;
    private final Refresh refresh = new Refresh();
    private BitSet breaks = new BitSet();
    private Display display;
    private String previous = "";
    private float previousWidth = -1;
    private boolean supported;
    private boolean sourceAction;

    private NativeCssWrap(TextView owner) {
        this.owner = owner; density = owner.getResources().getDisplayMetrics().density;
    }

    static void install(TextView view) { view.setTransformationMethod(new NativeCssWrap(view)); }

    static int width(TextView view, float physicalWidth) {
        if (!(view.getTransformationMethod() instanceof NativeCssWrap)) return (int) Math.ceil(physicalWidth);
        NativeCssWrap wrap = (NativeCssWrap) view.getTransformationMethod();
        wrap.plan(Math.max(0, physicalWidth / wrap.density));
        // Native frames/padding are integral, while CSS's content width is fractional.
        int width = (int) Math.ceil(physicalWidth);
        return wrap.supported && wrap.display != null ? Math.max(width, (int) Math.ceil(Layout.getDesiredWidth(wrap.display, view.getPaint()))) : width;
    }

    static boolean active(TextView view) {
        return view.getTransformationMethod() instanceof NativeCssWrap && !((NativeCssWrap) view.getTransformationMethod()).breaks.isEmpty();
    }

    static <T> T source(TextView view, java.util.function.Supplier<T> action) {
        if (!active(view)) return action.get();
        NativeCssWrap wrap = (NativeCssWrap) view.getTransformationMethod();
        boolean previous = wrap.sourceAction; wrap.sourceAction = true;
        try { return action.get(); } finally { wrap.sourceAction = previous; }
    }

    @Override public CharSequence getTransformation(CharSequence source, View view) {
        previousWidth = -1;
        display = new Display(source == null ? "" : source);
        return display;
    }

    @Override public void onFocusChanged(View view, CharSequence source, boolean focused, int direction, Rect previous) {}

    private void plan(float width) {
        CharSequence source = owner.getText();
        String key = source.toString() + "\n" + metricsKey(source);
        if (width == previousWidth && key.equals(previous)) return;
        previous = key; previousWidth = width;
        BitSet next = new BitSet();
        supported = width > 0 && source.length() <= 16384 && simpleBreaks(source);
        if (supported) {
            int lineStart = 0, lastSpace = -1;
            for (int i = 0; i <= source.length(); i++) {
                if (i < source.length() && source.charAt(i) != ' ' && source.charAt(i) != '\n') continue;
                if (advance(source, lineStart, i, owner.getPaint(), density) > width) {
                    if (lastSpace < lineStart || advance(source, lastSpace + 1, i, owner.getPaint(), density) > width) { supported = false; break; }
                    next.set(lastSpace); lineStart = lastSpace + 1;
                }
                if (i == source.length() || source.charAt(i) == '\n') { lineStart = i + 1; lastSpace = -1; }
                else lastSpace = i;
            }
        }
        if (!supported) next.clear();
        if (next.equals(breaks)) return;
        breaks = next;
        if (source instanceof Spannable && source.length() > 0)
            ((Spannable) source).setSpan(refresh, 0, source.length(), Spanned.SPAN_INCLUSIVE_INCLUSIVE);
    }

    private String metricsKey(CharSequence source) {
        StringBuilder key = new StringBuilder(paintKey(owner.getPaint()));
        key.append('/').append(owner.getTextLocale());
        if (source instanceof Spanned) {
            Spanned spans = (Spanned) source;
            for (MetricAffectingSpan span : spans.getSpans(0, spans.length(), MetricAffectingSpan.class)) {
                TextPaint paint = new TextPaint(owner.getPaint()); span.updateMeasureState(paint);
                key.append('/').append(span.getClass().getName()).append(':').append(spans.getSpanStart(span)).append(':').append(spans.getSpanEnd(span)).append(':').append(paintKey(paint));
            }
        }
        return key.toString();
    }

    private static String paintKey(Paint paint) {
        return paint.getTextSize() + "/" + paint.getTextScaleX() + "/" + paint.getTextSkewX() + "/" + paint.getLetterSpacing()
                + "/" + (android.os.Build.VERSION.SDK_INT >= 29 ? paint.getWordSpacing() : 0)
                + "/" + paint.getTypeface() + "/" + paint.getFontFeatureSettings() + "/" + paint.getFlags();
    }

    private boolean simpleBreaks(CharSequence source) {
        if (source instanceof Spanned && ((Spanned) source).getSpans(0, source.length(), ReplacementSpan.class).length > 0) return false;
        for (int i = 0; i < source.length(); i++) {
            byte direction = Character.getDirectionality(source.charAt(i));
            if (direction == Character.DIRECTIONALITY_RIGHT_TO_LEFT || direction == Character.DIRECTIONALITY_RIGHT_TO_LEFT_ARABIC) return false;
        }
        BreakIterator iterator = BreakIterator.getLineInstance(owner.getTextLocale()); iterator.setText(source.toString());
        for (int end = iterator.first(); end != BreakIterator.DONE; end = iterator.next()) {
            if (end > 0 && end < source.length() && source.charAt(end - 1) != ' ' && source.charAt(end - 1) != '\n') return false;
        }
        return true;
    }

    /** Measure CSS floats at a large normalization scale before Minikin's fixed-point truncation. */
    static float advance(CharSequence source, int start, int end, TextPaint original, float density) {
        float width = 0;
        Spanned spans = source instanceof Spanned ? (Spanned) source : null;
        for (int from = start; from < end;) {
            int to = spans == null ? end : spans.nextSpanTransition(from, end, MetricAffectingSpan.class);
            TextPaint paint = new TextPaint(original);
            if (spans != null) for (MetricAffectingSpan span : spans.getSpans(from, to, MetricAffectingSpan.class)) span.updateMeasureState(paint);
            // Blink FontDescription::EffectiveFontSize uses the font cache's 1/100px precision.
            float size = (float) Math.floor(paint.getTextSize() / density * 100) / 100;
            paint.setTextSize(size * PRECISION);
            if (android.os.Build.VERSION.SDK_INT >= 29) paint.setWordSpacing(paint.getWordSpacing() / density * PRECISION);
            float advance = paint.getRunAdvance(source, from, to, from, to, false, to) / PRECISION;
            // Each style item's ShapeResult::SnappedWidth owns a separate CSS LayoutUnit.
            width += (float) Math.ceil(advance * 64) / 64;
            from = to;
        }
        return width;
    }

    /** Only display spaces at chosen soft breaks change; every source offset/span stays valid. */
    private final class Display implements Spanned, GetChars {
        private final CharSequence source;
        Display(CharSequence source) { this.source = source; }
        @Override public int length() { return source.length(); }
        @Override public char charAt(int index) { return !sourceAction && breaks.get(index) && source.charAt(index) == ' ' ? '\n' : source.charAt(index); }
        @Override public void getChars(int start, int end, char[] destination, int offset) {
            TextUtils.getChars(source, start, end, destination, offset);
            for (int at = sourceAction ? -1 : breaks.nextSetBit(start); at >= 0 && at < end; at = breaks.nextSetBit(at + 1))
                if (source.charAt(at) == ' ') destination[offset + at - start] = '\n';
        }
        @Override public String toString() { char[] chars = new char[length()]; getChars(0, length(), chars, 0); return new String(chars); }
        @Override public CharSequence subSequence(int start, int end) { return new SpannedString(this).subSequence(start, end); }
        @Override public <T> T[] getSpans(int start, int end, Class<T> kind) {
            if (source instanceof Spanned) return ((Spanned) source).getSpans(start, end, kind);
            @SuppressWarnings("unchecked") T[] empty = (T[]) java.lang.reflect.Array.newInstance(kind, 0);
            return empty;
        }
        @Override public int getSpanStart(Object span) { return source instanceof Spanned ? ((Spanned) source).getSpanStart(span) : -1; }
        @Override public int getSpanEnd(Object span) { return source instanceof Spanned ? ((Spanned) source).getSpanEnd(span) : -1; }
        @Override public int getSpanFlags(Object span) { return source instanceof Spanned ? ((Spanned) source).getSpanFlags(span) : 0; }
        @Override public int nextSpanTransition(int start, int limit, Class kind) { return source instanceof Spanned ? ((Spanned) source).nextSpanTransition(start, limit, kind) : limit; }
    }
}
