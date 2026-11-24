package com.cloudgeek.spool.android;

import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import java.util.function.IntConsumer;
import java.util.function.IntPredicate;

/** Horizontal page navigation that leaves scrolling and editing gestures with their children. */
class NativeSwipeLayout extends FrameLayout {
    private final int slop, distance;
    private IntPredicate allowed = direction -> false;
    private IntConsumer swipe = direction -> {};
    private float startX, startY;
    private boolean tracking, swiping;

    NativeSwipeLayout(NativeUi ui) {
        super(ui.context);
        slop = ViewConfiguration.get(ui.context).getScaledTouchSlop();
        distance = Math.max(slop * 2, ui.dp(64));
    }

    /** Directions are physical: -1 for a left swipe, +1 for a right swipe. */
    void swipes(IntPredicate allowed, IntConsumer swipe) {
        this.allowed = allowed;
        this.swipe = swipe;
    }

    private void start(MotionEvent event) {
        startX = event.getX(); startY = event.getY(); swiping = false;
        tracking = event.getPointerCount() == 1 && (allowed.test(-1) || allowed.test(1))
                && !ownsHorizontalGesture(this, startX, startY);
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
        swiping = true;
        if (getParent() != null) getParent().requestDisallowInterceptTouchEvent(true);
    }

    @Override public boolean onInterceptTouchEvent(MotionEvent event) {
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_DOWN: start(event); break;
            case MotionEvent.ACTION_MOVE: move(event); break;
            case MotionEvent.ACTION_POINTER_DOWN:
            case MotionEvent.ACTION_CANCEL: tracking = false; break;
            case MotionEvent.ACTION_UP: if (!swiping) tracking = false; break;
        }
        return swiping;
    }

    @Override public boolean onTouchEvent(MotionEvent event) {
        boolean handled = tracking || swiping;
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_MOVE: move(event); break;
            case MotionEvent.ACTION_POINTER_DOWN: tracking = false; break;
            case MotionEvent.ACTION_UP:
                float dx = event.getX() - startX, dy = event.getY() - startY;
                int direction = dx > 0 ? 1 : -1;
                boolean commit = tracking && swiping && Math.abs(dx) >= distance
                        && Math.abs(dx) > Math.abs(dy) * 1.5f && allowed.test(direction);
                tracking = swiping = false;
                if (commit) swipe.accept(direction);
                else if (Math.abs(dx) <= slop && Math.abs(dy) <= slop) performClick();
                break;
            case MotionEvent.ACTION_CANCEL: tracking = swiping = false; break;
        }
        return handled || super.onTouchEvent(event);
    }

    @Override public boolean performClick() {
        return super.performClick();
    }

    @Override public void requestDisallowInterceptTouchEvent(boolean disallow) {
        if (disallow) tracking = false;
        super.requestDisallowInterceptTouchEvent(disallow);
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
