package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.view.GestureDetector;
import android.view.MotionEvent;
import android.view.ScaleGestureDetector;
import android.view.View;

/** Native vector display and modal pan/zoom; scrolling history never runs JavaScript. */
final class NativeMermaidImage extends View {
    private final NativeUi ui;
    private final boolean expanded;
    private NativeMermaidCache.Image image;
    private float scale, fit, x, y;
    private final ScaleGestureDetector pinch;
    private final GestureDetector drag;

    NativeMermaidImage(NativeUi ui, boolean expanded) {
        super(ui.context); this.ui = ui; this.expanded = expanded;
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        pinch = new ScaleGestureDetector(ui.context, new ScaleGestureDetector.SimpleOnScaleGestureListener() {
            @Override public boolean onScale(ScaleGestureDetector detector) {
                resize(scale * detector.getScaleFactor(), detector.getFocusX(), detector.getFocusY()); return true;
            }
        });
        drag = new GestureDetector(ui.context, new GestureDetector.SimpleOnGestureListener() {
            @Override public boolean onDown(MotionEvent event) { return true; }
            @Override public boolean onScroll(MotionEvent first, MotionEvent next, float dx, float dy) {
                if (!pinch.isInProgress()) { x -= dx; y -= dy; invalidate(); }
                return true;
            }
            @Override public boolean onSingleTapUp(MotionEvent event) { performClick(); return true; }
            @Override public boolean onDoubleTap(MotionEvent event) { zoom("fit"); return true; }
        });
    }

    void bind(NativeMermaidCache.Image value) {
        image = value;
        setContentDescription(value == null ? null : value.accessibilityLabel);
        setImportantForAccessibility(value == null ? IMPORTANT_FOR_ACCESSIBILITY_NO : IMPORTANT_FOR_ACCESSIBILITY_YES);
        fit(); invalidate();
    }

    @Override public CharSequence getAccessibilityClassName() { return android.widget.ImageView.class.getName(); }

    int inlineHeight(int width) {
        if (image == null) return 0;
        float factor = Math.min(ui.context.getResources().getDisplayMetrics().density, Math.max(1, width - ui.dp(32)) / image.width);
        return Math.min(ui.dp(20000), (int) Math.ceil(image.height * factor) + ui.dp(28));
    }

    void zoom(String action) {
        if (image == null || !expanded) return;
        if (action.equals("fit")) fit();
        else resize(scale * (action.equals("in") ? 1.25f : .8f), getWidth() / 2f, getHeight() / 2f);
        invalidate();
    }

    private void fit() {
        if (image == null || getWidth() == 0 || getHeight() == 0) return;
        fit = Math.min(Math.max(1, getWidth() - ui.dp(32)) / image.width, Math.max(1, getHeight() - ui.dp(28)) / image.height);
        scale = fit; x = (getWidth() - image.width * scale) / 2; y = (getHeight() - image.height * scale) / 2;
    }

    private void resize(float value, float centerX, float centerY) {
        if (image == null || scale <= 0) return;
        value = Math.max(fit * .2f, Math.min(fit * 16, value));
        x = centerX - (centerX - x) * value / scale; y = centerY - (centerY - y) * value / scale; scale = value; invalidate();
    }

    @Override protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) { fit(); }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        if (image == null) return;
        int save = canvas.save();
        if (expanded) { canvas.translate(x, y); canvas.scale(scale, scale); }
        else {
            float factor = Math.min(ui.context.getResources().getDisplayMetrics().density, Math.max(1, getWidth() - ui.dp(32)) / image.width);
            canvas.translate((getWidth() - image.width * factor) / 2, ui.dp(12)); canvas.scale(factor, factor);
        }
        canvas.drawPicture(image.picture); canvas.restoreToCount(save);
    }

    @Override public boolean onTouchEvent(MotionEvent event) {
        if (!expanded || image == null) return super.onTouchEvent(event);
        if (event.getActionMasked() == MotionEvent.ACTION_DOWN) getParent().requestDisallowInterceptTouchEvent(true);
        pinch.onTouchEvent(event); drag.onTouchEvent(event);
        if (event.getActionMasked() == MotionEvent.ACTION_UP || event.getActionMasked() == MotionEvent.ACTION_CANCEL)
            getParent().requestDisallowInterceptTouchEvent(false);
        return true;
    }

    @Override public boolean performClick() { super.performClick(); return true; }
}
