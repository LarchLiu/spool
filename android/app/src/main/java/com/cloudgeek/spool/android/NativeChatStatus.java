package com.cloudgeek.spool.android;

import android.animation.ValueAnimator;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.view.View;
import android.view.animation.LinearInterpolator;
import android.view.animation.PathInterpolator;
import android.widget.LinearLayout;

/** Web's nine-dot, 1.1s grid beat, animated only while attached and visible. */
final class NativeChatStatus extends View {
    private final Paint dot = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final PathInterpolator ease = new PathInterpolator(.42f, 0, .58f, 1);
    private final float density;
    private final int color;
    private ValueAnimator beat;
    private float phase;

    NativeChatStatus(NativeUi ui) {
        super(ui.context);
        density = getResources().getDisplayMetrics().density;
        color = ui.muted;
        LinearLayout.LayoutParams box = new LinearLayout.LayoutParams(ui.dp(12), ui.dp(12));
        box.setMarginStart(ui.dp(2)); box.topMargin = ui.dp(8);
        setLayoutParams(box);
        setContentDescription(ui.t("Working"));
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
        setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
    }

    private void update() {
        if (isAttachedToWindow() && isShown() && getWindowVisibility() == VISIBLE && ValueAnimator.areAnimatorsEnabled()) {
            if (beat != null) return;
            beat = ValueAnimator.ofFloat(0, 1);
            beat.setDuration(1100);
            beat.setRepeatCount(ValueAnimator.INFINITE);
            beat.setInterpolator(new LinearInterpolator());
            beat.addUpdateListener(value -> { phase = (float) value.getAnimatedValue(); invalidate(); });
            beat.start();
        } else stop();
    }

    private void stop() {
        if (beat != null) { beat.cancel(); beat = null; }
        invalidate();
    }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        dot.setColor(color);
        for (int index = 0; index < 9; index++) {
            float opacity = .65f;
            if (beat != null) {
                float time = (phase + Math.abs(index - 4) * 120f / 1100) % 1;
                opacity = time < .5f ? 1 - .7f * ease.getInterpolation(time * 2)
                        : .3f + .7f * ease.getInterpolation((time - .5f) * 2);
            }
            dot.setAlpha(Math.round(Color.alpha(color) * opacity));
            canvas.drawCircle((1.5f + index % 3 * 4.5f) * density,
                    (1.5f + index / 3 * 4.5f) * density, 1.5f * density, dot);
        }
    }

    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); update(); }
    @Override protected void onDetachedFromWindow() { stop(); super.onDetachedFromWindow(); }
    @Override protected void onVisibilityChanged(View changed, int visibility) { super.onVisibilityChanged(changed, visibility); update(); }
    @Override protected void onWindowVisibilityChanged(int visibility) { super.onWindowVisibilityChanged(visibility); update(); }
}
