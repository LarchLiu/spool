package com.cloudgeek.spool.android;

import android.view.View;
import android.view.MotionEvent;
import android.view.ViewTreeObserver;
import android.animation.ValueAnimator;
import java.util.function.Consumer;

/** Web history follow/height-delta anchoring, without moving focus into the log. */
final class NativeHistoryScroll implements ViewTreeObserver.OnPreDrawListener {
    static final class State {
        String key = "";
        boolean following = true;
        int top;
        Anchor anchor;
        void select(String next) { if (!key.equals(next)) { key = next; following = true; top = 0; anchor = null; } }
    }
    static final class Anchor {
        final int height, top;
        final View row;
        final int relative;
        Anchor(int height, int top) { this(height, top, null, 0); }
        Anchor(int height, int top, View row, int relative) { this.height = height; this.top = top; this.row = row; this.relative = relative; }
    }
    private final View view;
    private final View content;
    private final State state;
    private final float threshold;
    private boolean pending, restoring, disposed;
    private ViewTreeObserver observer;
    private final View.OnLayoutChangeListener resized;
    private Consumer<Boolean> following = value -> {};
    private ValueAnimator animation;
    private long anchorRestores, anchorCorrections;
    private int maxAnchorCorrection;

    NativeHistoryScroll(View view, View content, State state, float threshold) {
        this.view = view; this.content = content; this.state = state; this.threshold = threshold;
        resized = (v, l, t, r, b, ol, ot, or, ob) -> {
            if (pending || disposed) return;
            if (state.following) bottom(); else updatePosition();
        };
        view.setOnScrollChangeListener((v, x, y, oldX, oldY) -> {
            if (pending || restoring || disposed) return;
            updatePosition();
        });
        view.setOnTouchListener((v, event) -> { if (event.getActionMasked() == MotionEvent.ACTION_DOWN) cancelAnimation(); return false; });
        content.addOnLayoutChangeListener(resized); view.addOnLayoutChangeListener(resized);
    }

    void onFollowing(Consumer<Boolean> listener) { following = listener; following.accept(state.following); }

    private void updatePosition() {
        state.top = view.getScrollY();
        state.following = content.getHeight() - state.top - view.getHeight() < threshold;
        following.accept(state.following);
    }

    void latest() {
        if (disposed) return;
        cancelAnimation(); state.anchor = null; state.following = true; following.accept(true);
        int from = view.getScrollY(), target = Math.max(0, content.getHeight() - view.getHeight());
        if (from == target || !ValueAnimator.areAnimatorsEnabled()) { scroll(target); return; }
        animation = ValueAnimator.ofInt(from, target);
        animation.setDuration(250);
        animation.setInterpolator(new android.view.animation.DecelerateInterpolator());
        animation.addUpdateListener(value -> {
            int top = (int) value.getAnimatedValue();
            if (!disposed && top != view.getScrollY()) scroll(top);
        });
        animation.start();
    }

    private void cancelAnimation() { if (animation != null) { animation.cancel(); animation = null; } }
    void suspend() { cancelAnimation(); }

    Anchor anchor() {
        if (content instanceof android.view.ViewGroup) {
            android.view.ViewGroup group = (android.view.ViewGroup) content;
            for (int i = 0; i < group.getChildCount(); i++) {
                View child = group.getChildAt(i);
                if (child instanceof NativeTranscriptViewport.Row && child.getBottom() > view.getScrollY())
                    return new Anchor(content.getHeight(), view.getScrollY(), child, view.getScrollY() - child.getTop());
            }
        }
        return new Anchor(content.getHeight(), view.getScrollY());
    }

    static int restore(int height, int top, boolean following, Anchor anchor) {
        return following ? height : anchor == null ? top : anchor.top + height - anchor.height;
    }

    void changing(Anchor next) {
        cancelAnimation();
        pending = true;
        if (next != null && state.anchor == null) state.anchor = next;
    }

    void changed() {
        if (disposed) return;
        if (observer == null) {
            observer = view.getViewTreeObserver(); observer.addOnPreDrawListener(this);
        }
        view.invalidate();
    }

    private void scroll(int top) {
        restoring = true; view.scrollTo(0, top); restoring = false; updatePosition();
    }
    private void bottom() { scroll(Math.max(0, content.getHeight() - view.getHeight())); }

    @Override public boolean onPreDraw() {
        remove();
        if (!disposed) {
            Anchor anchor = state.anchor;
            int top = !state.following && anchor != null && anchor.row != null && anchor.row.getParent() == content
                    ? anchor.row.getTop() + anchor.relative : restore(content.getHeight(), state.top, state.following, anchor);
            if (!state.following && anchor != null) {
                anchorRestores++;
                int correction = Math.abs(top - view.getScrollY());
                if (correction > 0) anchorCorrections++;
                maxAnchorCorrection = Math.max(maxAnchorCorrection, correction);
            }
            scroll(top);
            state.anchor = null;
        }
        pending = false;
        return true;
    }

    org.json.JSONObject metrics() {
        org.json.JSONObject result = new org.json.JSONObject();
        try { result.put("restores", anchorRestores).put("corrections", anchorCorrections).put("maxCorrectionPixels", maxAnchorCorrection); }
        catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return result;
    }

    private void remove() {
        if (observer != null && observer.isAlive()) observer.removeOnPreDrawListener(this);
        ViewTreeObserver attached = view.getViewTreeObserver();
        if (attached != observer && attached.isAlive()) attached.removeOnPreDrawListener(this);
        observer = null;
    }

    void dispose() {
        disposed = true; cancelAnimation(); remove(); following = value -> {};
        view.setOnScrollChangeListener((View.OnScrollChangeListener) null); view.setOnTouchListener(null);
        content.removeOnLayoutChangeListener(resized); view.removeOnLayoutChangeListener(resized);
    }
}
