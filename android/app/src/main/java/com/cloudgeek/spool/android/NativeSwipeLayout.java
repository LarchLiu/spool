package com.cloudgeek.spool.android;

import android.view.MotionEvent;
import android.view.VelocityTracker;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import java.util.function.IntPredicate;

/** Horizontal page navigation that leaves scrolling and editing gestures with their children. */
class NativeSwipeLayout extends FrameLayout {
    interface SwipeListener {
        default void drag(int direction, float distance) {}
        void release(int direction, boolean commit);
    }

    private final int slop, distance, minVelocity, maxVelocity;
    private IntPredicate allowed = direction -> false;
    private SwipeListener swipe = (direction, commit) -> {};
    private VelocityTracker velocity;
    private float startX, startY;
    private int direction;
    private boolean tracking, swiping;

    NativeSwipeLayout(NativeUi ui) {
        super(ui.context);
        ViewConfiguration configuration = ViewConfiguration.get(ui.context);
        slop = configuration.getScaledTouchSlop();
        distance = Math.max(slop * 2, ui.dp(64));
        minVelocity = Math.max(configuration.getScaledMinimumFlingVelocity(), ui.dp(600));
        maxVelocity = configuration.getScaledMaximumFlingVelocity();
    }

    /** Directions are physical: -1 for a left swipe, +1 for a right swipe. */
    void swipes(IntPredicate allowed, SwipeListener swipe) {
        this.allowed = allowed;
        this.swipe = swipe;
    }

    @Override public boolean dispatchTouchEvent(MotionEvent event) {
        if (velocity != null) velocity.addMovement(event);
        boolean handled = super.dispatchTouchEvent(event);
        // The first intercepted MOVE only cancels the child, without reaching onTouchEvent.
        if (event.getActionMasked() == MotionEvent.ACTION_MOVE && tracking && swiping)
            swipe.drag(direction, Math.max(0, direction * (event.getX() - startX)));
        return handled;
    }

    private void start(MotionEvent event) {
        recycleVelocity();
        startX = event.getX(); startY = event.getY(); swiping = false;
        tracking = event.getPointerCount() == 1 && (allowed.test(-1) || allowed.test(1))
                && !ownsHorizontalGesture(this, startX, startY);
        if (tracking) { velocity = VelocityTracker.obtain(); velocity.addMovement(event); }
    }

    private void move(MotionEvent event) {
        if (!tracking || swiping) return;
        float dx = event.getX() - startX, dy = event.getY() - startY;
        if (Math.max(Math.abs(dx), Math.abs(dy)) <= slop) return;
        // Lock the initial direction so a vertical scroll cannot turn into page navigation.
        if (Math.abs(dx) <= Math.abs(dy) * 1.5f || !allowed.test(dx > 0 ? 1 : -1)) {
            tracking = false;
            return;
        }
        direction = dx > 0 ? 1 : -1;
        swiping = true;
        if (getParent() != null) getParent().requestDisallowInterceptTouchEvent(true);
    }

    @Override public boolean onInterceptTouchEvent(MotionEvent event) {
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_DOWN: start(event); break;
            case MotionEvent.ACTION_MOVE: move(event); break;
            case MotionEvent.ACTION_POINTER_DOWN:
            case MotionEvent.ACTION_CANCEL:
            case MotionEvent.ACTION_UP:
                if (!swiping) { tracking = false; recycleVelocity(); }
                break;
        }
        return swiping;
    }

    @Override public boolean onTouchEvent(MotionEvent event) {
        boolean handled = tracking || swiping;
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_MOVE: move(event); break;
            case MotionEvent.ACTION_POINTER_DOWN:
                if (tracking && swiping) swipe.release(direction, false);
                tracking = false; recycleVelocity();
                break;
            case MotionEvent.ACTION_UP:
                float dx = event.getX() - startX, dy = event.getY() - startY;
                if (tracking && swiping) {
                    float dragged = Math.max(0, direction * dx), speed = 0;
                    if (velocity != null) {
                        velocity.computeCurrentVelocity(1000, maxVelocity);
                        speed = direction * velocity.getXVelocity();
                    }
                    boolean commit = Math.abs(dx) > Math.abs(dy) * 1.5f && speed > -minVelocity
                            && (dragged >= distance || dragged > slop * 2 && speed >= minVelocity);
                    swipe.drag(direction, dragged);
                    swipe.release(direction, commit);
                } else if (!swiping && Math.abs(dx) <= slop && Math.abs(dy) <= slop) performClick();
                tracking = swiping = false;
                recycleVelocity();
                break;
            case MotionEvent.ACTION_CANCEL:
                if (tracking && swiping) swipe.release(direction, false);
                tracking = swiping = false; recycleVelocity();
                break;
        }
        return handled || super.onTouchEvent(event);
    }

    @Override public boolean performClick() {
        return super.performClick();
    }

    @Override public void requestDisallowInterceptTouchEvent(boolean disallow) {
        if (disallow && !swiping) { tracking = false; recycleVelocity(); }
        super.requestDisallowInterceptTouchEvent(disallow);
    }

    private void recycleVelocity() {
        if (velocity == null) return;
        velocity.recycle(); velocity = null;
    }

    @Override protected void onDetachedFromWindow() {
        tracking = swiping = false; recycleVelocity();
        super.onDetachedFromWindow();
    }

    private static boolean ownsHorizontalGesture(View view, float x, float y) {
        if (view instanceof EditText || view instanceof HorizontalScrollView
                || view.canScrollHorizontally(-1) || view.canScrollHorizontally(1)) return true;
        if (view instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) view;
            for (int i = group.getChildCount() - 1; i >= 0; i--) {
                View child = group.getChildAt(i);
                if (child.getVisibility() != VISIBLE) continue;
                float childX = x + view.getScrollX() - child.getX(), childY = y + view.getScrollY() - child.getY();
                if (childX >= 0 && childY >= 0 && childX < child.getWidth() && childY < child.getHeight())
                    return ownsHorizontalGesture(child, childX, childY);
            }
        }
        return false;
    }
}
