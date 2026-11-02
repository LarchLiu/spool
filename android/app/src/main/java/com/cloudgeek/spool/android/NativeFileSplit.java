package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import java.util.function.DoubleConsumer;

/** Native FilePreviewSplit with the same limits, 8px grip, drag, keyboard and accessibility actions. */
final class NativeFileSplit extends ViewGroup {
    final View tree, handle;
    View preview;
    private final NativeUi ui;
    private final DoubleConsumer change;
    double preferred;
    private float startY;
    private double startFraction;
    private boolean dragging;
    private final android.view.GestureDetector gestures;

    static double minimum(double height) {
        double available = height - 8;
        return !Double.isFinite(available) || available <= 0 ? .15 : Math.max(.15, Math.min(120, available / 2) / available);
    }
    static double maximum(double height) {
        double available = height - 8;
        return !Double.isFinite(available) || available <= 0 ? .8 : Math.min(.8, 1 - Math.min(160, available / 2) / available);
    }
    static double fraction(double value, double height) { return Math.max(minimum(height), Math.min(maximum(height), Double.isFinite(value) ? value : .32)); }

    NativeFileSplit(NativeUi ui, View tree, double preferred, DoubleConsumer change) {
        super(ui.context);
        this.ui = ui; this.tree = tree; this.preferred = preferred; this.change = change;
        addView(tree);
        gestures = new android.view.GestureDetector(ui.context, new android.view.GestureDetector.SimpleOnGestureListener() {
            @Override public boolean onDown(MotionEvent event) { return true; }
            @Override public boolean onDoubleTap(MotionEvent event) { update(.32); return true; }
        });
        handle = new View(ui.context) {
            final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
            @Override protected void onDraw(Canvas canvas) {
                paint.setColor(dragging || isFocused() || isHovered() ? ui.muted : ui.line);
                canvas.drawRect(0, ui.dp(3), getWidth(), ui.dp(dragging || isFocused() || isHovered() ? 5 : 4), paint);
                paint.setColor(ui.borderStrong);
                canvas.drawRoundRect(getWidth() / 2f - ui.dp(14), ui.dp(2), getWidth() / 2f + ui.dp(14), ui.dp(5), ui.dp(2), ui.dp(2), paint);
            }
            @Override public boolean onTouchEvent(MotionEvent event) {
                gestures.onTouchEvent(event);
                switch (event.getActionMasked()) {
                    case MotionEvent.ACTION_DOWN:
                        getParent().requestDisallowInterceptTouchEvent(true);
                        startY = event.getRawY(); startFraction = current(); dragging = true; invalidate(); return true;
                    case MotionEvent.ACTION_MOVE:
                        if (dragging) update(startFraction + (event.getRawY() - startY) / Math.max(1, NativeFileSplit.this.getHeight() - ui.dp(8)));
                        return true;
                    case MotionEvent.ACTION_UP:
                        if (dragging && Math.abs(event.getRawY() - startY) < ui.dp(4)) performClick();
                    case MotionEvent.ACTION_CANCEL:
                        dragging = false; invalidate(); return true;
                    default: return true;
                }
            }
            @Override public boolean performClick() { super.performClick(); return true; }
        };
        handle.setContentDescription(ui.t("Resize file preview"));
        handle.setFocusable(true);
        handle.setOnKeyListener((view, key, event) -> {
            if (event.getAction() != KeyEvent.ACTION_DOWN) return false;
            double height = heightDP();
            if (key == KeyEvent.KEYCODE_DPAD_UP || key == KeyEvent.KEYCODE_DPAD_DOWN) update(current() + (key == KeyEvent.KEYCODE_DPAD_DOWN ? 20 : -20) / Math.max(1, height - 8));
            else if (key == KeyEvent.KEYCODE_MOVE_HOME) update(minimum(height));
            else if (key == KeyEvent.KEYCODE_MOVE_END) update(maximum(height));
            else return false;
            return true;
        });
        handle.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info);
                info.setClassName("android.widget.SeekBar");
                info.setRangeInfo(android.view.accessibility.AccessibilityNodeInfo.RangeInfo.obtain(android.view.accessibility.AccessibilityNodeInfo.RangeInfo.RANGE_TYPE_FLOAT, (float) minimum(heightDP()) * 100, (float) maximum(heightDP()) * 100, (float) current() * 100));
                info.addAction(android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SET_PROGRESS);
                info.addAction(android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_FORWARD);
                info.addAction(android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_BACKWARD);
            }
            @Override public boolean performAccessibilityAction(View host, int action, android.os.Bundle arguments) {
                if (action == android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SET_PROGRESS.getId() && arguments != null) { update(arguments.getFloat(android.view.accessibility.AccessibilityNodeInfo.ACTION_ARGUMENT_PROGRESS_VALUE) / 100d); return true; }
                if (action == android.view.accessibility.AccessibilityNodeInfo.ACTION_SCROLL_FORWARD || action == android.view.accessibility.AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD) {
                    update(current() + (action == android.view.accessibility.AccessibilityNodeInfo.ACTION_SCROLL_FORWARD ? 20 : -20) / Math.max(1, heightDP() - 8)); return true;
                }
                return super.performAccessibilityAction(host, action, arguments);
            }
        });
        addView(handle);
        handle.setVisibility(GONE);
    }

    private double heightDP() { return getHeight() / ui.context.getResources().getDisplayMetrics().density; }
    double current() { return fraction(preferred, heightDP()); }
    void update(double value) { preferred = fraction(value, heightDP()); change.accept(preferred); requestLayout(); handle.invalidate(); }
    void preview(View view) {
        if (preview != null) removeView(preview);
        preview = view;
        handle.setVisibility(view == null ? GONE : VISIBLE);
        if (view != null) addView(view);
        requestLayout();
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec), height = MeasureSpec.getSize(heightSpec);
        setMeasuredDimension(width, height);
        int treeHeight = preview == null ? height : Math.round((float) (Math.max(0, height - ui.dp(8)) * fraction(preferred, height / getResources().getDisplayMetrics().density)));
        tree.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(treeHeight, MeasureSpec.EXACTLY));
        if (preview != null) {
            handle.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(ui.dp(8), MeasureSpec.EXACTLY));
            preview.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(Math.max(0, height - treeHeight - ui.dp(8)), MeasureSpec.EXACTLY));
        }
    }
    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        tree.layout(0, 0, getWidth(), tree.getMeasuredHeight());
        if (preview != null) {
            handle.layout(0, tree.getBottom(), getWidth(), tree.getBottom() + ui.dp(8));
            preview.layout(0, handle.getBottom(), getWidth(), getHeight());
        }
    }
}
