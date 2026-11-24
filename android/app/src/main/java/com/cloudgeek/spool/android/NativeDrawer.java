package com.cloudgeek.spool.android;

import android.animation.ValueAnimator;
import android.animation.Animator;
import android.animation.AnimatorListenerAdapter;
import android.view.Gravity;
import android.view.ViewGroup;
import android.view.animation.PathInterpolator;
import android.widget.FrameLayout;
import android.widget.LinearLayout;

/** Full-screen mobile sidebars slide over the workspace without a dimming layer. */
final class NativeDrawer extends NativeSwipeLayout {
    final LinearLayout sheet;
    private final boolean right;
    private ValueAnimator animation;
    private float hidden = 1;
    private float dragOrigin, dragDistance;
    private boolean dismissing, dragging, opening;

    NativeDrawer(NativeUi ui, boolean right) {
        this(ui, right, null);
    }

    NativeDrawer(NativeUi ui, boolean right, Runnable close) {
        super(ui);
        this.right = right;
        swipes(direction -> !dismissing && direction == (right ? 1 : -1),
                new SwipeListener() {
                    @Override public void drag(int direction, float distance) { NativeDrawer.this.drag(distance, false); }
                    @Override public void release(int direction, boolean commit) {
                        if (!commit) show();
                        else if (close != null) close.run();
                        else dismiss();
                    }
                });
        sheet = new LinearLayout(ui.context) {
            // The blank sheet consumes taps, but its pressed state belongs only
            // to that background; navigation rows handle their own feedback.
            @Override protected void dispatchSetPressed(boolean pressed) {}
        };
        sheet.setOrientation(LinearLayout.VERTICAL);
        sheet.setBackgroundColor(right ? ui.elevated : ui.sidebar);
        sheet.setClickable(true);
        sheet.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        addView(sheet, new FrameLayout.LayoutParams(ui.dp(right ? 360 : 298), -1, right ? Gravity.END : Gravity.START));
    }

    void show() {
        show(true);
    }

    void show(boolean animate) {
        if (dismissing) return;
        dragging = false;
        if (animate) animateTo(0, null);
        else { stopAnimation(); hidden = 0; positionSheet(); }
    }

    /** Drag distances stay in pixels, including before the new drawer's first layout. */
    void drag(float distance, boolean opening) {
        if (dismissing) return;
        if (!dragging) {
            stopAnimation();
            dragging = true;
            this.opening = opening;
            dragOrigin = opening ? 1 : hidden;
            hidden = dragOrigin;
        }
        dragDistance = distance;
        positionSheet();
    }

    void dismiss() {
        if (dismissing) return;
        dragging = false;
        dismissing = true;
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
        Runnable remove = () -> {
            if (getParent() instanceof ViewGroup) ((ViewGroup) getParent()).removeView(this);
        };
        animateTo(1, remove);
    }

    private void animateTo(float target, Runnable complete) {
        stopAnimation();
        if (!ValueAnimator.areAnimatorsEnabled() || hidden == target) {
            hidden = target; positionSheet();
            if (complete != null) complete.run();
            return;
        }
        ValueAnimator next = ValueAnimator.ofFloat(hidden, target);
        animation = next;
        next.setDuration(200);
        next.setInterpolator(new PathInterpolator(.25f, .1f, .25f, 1));
        next.addUpdateListener(value -> { hidden = (float) value.getAnimatedValue(); positionSheet(); });
        next.addListener(new AnimatorListenerAdapter() {
            @Override public void onAnimationEnd(Animator animator) {
                if (animation != animator) return;
                animation = null;
                if (complete != null) complete.run();
            }
        });
        next.start();
    }

    private void positionSheet() {
        int width = sheet.getMeasuredWidth();
        if (width == 0 && getParent() instanceof ViewGroup) {
            ViewGroup parent = (ViewGroup) getParent();
            width = Math.max(0, parent.getWidth() - parent.getPaddingLeft() - parent.getPaddingRight()
                    - getPaddingLeft() - getPaddingRight());
        }
        if (dragging && width > 0) hidden = Math.max(0, Math.min(1, dragOrigin + (opening ? -1 : 1) * dragDistance / width));
        sheet.setTranslationX((right ? 1 : -1) * width * hidden);
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec);
        int available = Math.max(0, width - getPaddingLeft() - getPaddingRight());
        sheet.getLayoutParams().width = available;
        super.onMeasure(widthSpec, heightSpec);
        positionSheet();
    }

    private void stopAnimation() {
        if (animation == null) return;
        ValueAnimator previous = animation; animation = null;
        previous.removeAllUpdateListeners(); previous.removeAllListeners(); previous.cancel();
    }

    @Override protected void onDetachedFromWindow() {
        stopAnimation();
        super.onDetachedFromWindow();
    }
}
