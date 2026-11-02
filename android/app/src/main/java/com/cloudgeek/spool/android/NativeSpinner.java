package com.cloudgeek.spool.android;

import android.animation.ObjectAnimator;
import android.animation.ValueAnimator;
import android.view.View;
import android.view.animation.LinearInterpolator;
import android.widget.ImageView;

/** Original Web LoaderCircle with its 800ms spin, scoped to a visible attached view. */
final class NativeSpinner extends ImageView {
    private ObjectAnimator spin;
    private final boolean task;
    private final android.graphics.Paint ring;

    NativeSpinner(NativeUi ui, int size, String label) {
        this(ui, size, label, false);
    }

    NativeSpinner(NativeUi ui, int size, String label, boolean task) {
        super(ui.context);
        this.task = task;
        ring = new android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG); ring.setStyle(android.graphics.Paint.Style.STROKE); ring.setStrokeWidth(ui.dp(3) / 2f); ring.setColor(ui.muted);
        if (!task) setImageDrawable(NativeIcons.drawable(ui.context, "spinner", ui.muted, ui.dp(size)));
        setContentDescription(label);
        setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
    }

    private void update() {
        if (isAttachedToWindow() && isShown() && getWindowVisibility() == VISIBLE && ValueAnimator.areAnimatorsEnabled()) {
            if (spin != null) return;
            spin = ObjectAnimator.ofFloat(this, View.ROTATION, 0, 360);
            spin.setDuration(task ? 1100 : 800);
            spin.setRepeatCount(ValueAnimator.INFINITE);
            spin.setInterpolator(new LinearInterpolator());
            spin.start();
        } else stop();
    }

    private void stop() {
        if (spin != null) { spin.cancel(); spin = null; }
        setRotation(0);
    }

    @Override protected void onDraw(android.graphics.Canvas canvas) {
        if (!task) { super.onDraw(canvas); return; }
        float inset = ring.getStrokeWidth() / 2; canvas.drawArc(inset, inset, getWidth() - inset, getHeight() - inset, 45, 270, false, ring);
    }

    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); update(); }
    @Override protected void onDetachedFromWindow() { stop(); super.onDetachedFromWindow(); }
    @Override protected void onVisibilityChanged(View changed, int visibility) { super.onVisibilityChanged(changed, visibility); update(); }
    @Override protected void onWindowVisibilityChanged(int visibility) { super.onWindowVisibilityChanged(visibility); update(); }
}
