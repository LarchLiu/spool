package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewTreeObserver;

/** A cancellable post-draw turn, not a fixed delay that can run before first paint. */
final class NativeAfterPaint implements ViewTreeObserver.OnDrawListener, Runnable {
    private final View view;
    private final ViewTreeObserver observer;
    private Runnable task;
    private boolean posted;

    private NativeAfterPaint(View view, Runnable task) {
        this.view = view; this.task = task; observer = view.getViewTreeObserver();
        observer.addOnDrawListener(this); view.invalidate();
    }

    static Runnable schedule(View view, Runnable task) {
        NativeAfterPaint pending = new NativeAfterPaint(view, task);
        return pending::cancel;
    }

    @Override public void onDraw() { if (!posted) { posted = true; view.post(this); } }

    private void remove() {
        if (observer.isAlive()) observer.removeOnDrawListener(this);
        ViewTreeObserver attached = view.getViewTreeObserver();
        if (attached != observer && attached.isAlive()) attached.removeOnDrawListener(this);
    }

    private void cancel() { task = null; view.removeCallbacks(this); remove(); }

    @Override public void run() {
        remove(); Runnable next = task; task = null;
        if (next != null) next.run();
    }
}
