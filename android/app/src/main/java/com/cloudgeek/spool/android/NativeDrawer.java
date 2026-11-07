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
final class NativeDrawer extends FrameLayout {
    final LinearLayout sheet;
    private final boolean right;
    private ValueAnimator animation;
    private float hidden = 1;
    private boolean dismissing;

    NativeDrawer(NativeUi ui, boolean right) {
        super(ui.context);
        this.right = right;
        sheet = ui.column();
        sheet.setBackgroundColor(right ? ui.elevated : ui.sidebar);
        sheet.setClickable(true);
        sheet.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        addView(sheet, new FrameLayout.LayoutParams(ui.dp(right ? 360 : 298), -1, right ? Gravity.END : Gravity.START));
    }

    void show() {
        if (dismissing) return;
        animateTo(0, null);
    }

    void dismiss() {
        if (dismissing) return;
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
        sheet.setTranslationX((right ? 1 : -1) * sheet.getMeasuredWidth() * 1.05f * hidden);
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
