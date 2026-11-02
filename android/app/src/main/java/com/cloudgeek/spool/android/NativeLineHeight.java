package com.cloudgeek.spool.android;

import android.graphics.Paint;
import android.text.TextPaint;
import android.text.style.LineHeightSpan;

/** Cumulative CSS line boxes; UpdateLayout also reflows existing editable DynamicLayouts. */
final class NativeLineHeight implements LineHeightSpan.WithDensity, android.text.style.UpdateLayout {
    private final float height;
    private final float cssDensity;
    private final Paint cssPaint;
    private final Paint strutPaint;
    private final float normalAscent;
    private float originPhase = Float.NaN, roundingY;
    private int intrinsicBaseline;

    NativeLineHeight(float height) { this(height, 0); }

    NativeLineHeight(float height, float cssDensity) {
        this(height, cssDensity, null);
    }

    NativeLineHeight(float height, float cssDensity, Paint strutPaint) {
        this(height, cssDensity, strutPaint, Float.NaN);
    }

    NativeLineHeight(float height, float cssDensity, Paint strutPaint, float normalAscent) {
        this.height = Math.max(1, height); this.cssDensity = cssDensity;
        this.strutPaint = strutPaint;
        this.normalAscent = normalAscent;
        cssPaint = cssDensity > 0 ? new Paint() : null;
    }

    boolean origin(float physicalY, float roundingY) {
        if (cssDensity <= 0) return false;
        float phase = physicalY - (float) Math.floor(physicalY / cssDensity) * cssDensity;
        if (originPhase == phase && this.roundingY == roundingY) return false;
        originPhase = phase; this.roundingY = roundingY;
        return true;
    }

    int intrinsicBaseline() { return intrinsicBaseline; }

    @Override public void chooseHeight(CharSequence text, int start, int end, int spanStart, int top, Paint.FontMetricsInt metrics) {
        apply(spanStart, top, metrics, metrics.ascent, metrics.descent);
    }

    @Override public void chooseHeight(CharSequence text, int start, int end, int spanStart, int top, Paint.FontMetricsInt metrics, TextPaint paint) {
        if (cssDensity > 0) {
            // CSS's primary-font strut is independent of a fallback run's ink/weight metrics.
            cssPaint.set(strutPaint == null ? paint : strutPaint); cssPaint.setTextSize(cssPaint.getTextSize() / cssDensity);
            Paint.FontMetrics font = cssPaint.getFontMetrics();
            // Blink's explicit line-height uses integer CSS font metrics and floors the upper half-leading.
            float ascent = Math.round(-font.ascent), descent = Math.round(font.descent);
            float baseline = (Float.isNaN(normalAscent) ? ascent + (float) Math.floor((height / cssDensity - ascent - descent) / 2) : normalAscent) * cssDensity;
            intrinsicBaseline = Math.round(baseline);
            if (!Float.isNaN(originPhase)) {
                int lineTop = top - spanStart, index = Math.round(lineTop / height);
                // Snap the cumulative CSS paint baseline, not the line's independently rounded local offset.
                baseline = Math.round((originPhase + roundingY + index * height + baseline) / cssDensity) * cssDensity - originPhase;
                applyBaseline(spanStart, top, metrics, Math.round(baseline) - lineTop);
            } else applyBaseline(spanStart, top, metrics, intrinsicBaseline);
        } else {
            Paint.FontMetrics font = paint.getFontMetrics();
            apply(spanStart, top, metrics, font.ascent, font.descent);
        }
    }

    private void apply(int spanStart, int top, Paint.FontMetricsInt metrics, float ascent, float descent) {
        int baseline = Math.round((height - (descent - ascent)) / 2 - ascent);
        applyBaseline(spanStart, top, metrics, baseline);
    }

    private void applyBaseline(int spanStart, int top, Paint.FontMetricsInt metrics, int baseline) {
        int index = Math.round((top - spanStart) / height);
        int line = Math.max(1, Math.round((index + 1) * height) - (top - spanStart));
        metrics.ascent = -baseline;
        metrics.descent = line - baseline;
        metrics.top = metrics.ascent;
        metrics.bottom = metrics.descent;
        metrics.leading = 0;
    }
}
