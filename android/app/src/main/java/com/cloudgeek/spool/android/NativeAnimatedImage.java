package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.PixelFormat;
import android.graphics.drawable.Animatable;
import android.graphics.drawable.Drawable;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import java.util.concurrent.Executor;

/** Independent, visibility-controlled playback; at most one frame decode is in flight per image. */
final class NativeAnimatedImage extends Drawable implements Animatable {
    private final Executor worker;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Object lock = new Object();
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG);
    private NativeImageSequence sequence;
    private final int edge, width, height;
    private Bitmap first, current;
    private NativeImageSequence.Renderer renderer;
    private final Runnable tick = this::advance;
    private int index, iterations;
    private volatile int rendererEpoch;
    private volatile int epoch;
    private boolean running, ended, preparing, disposed;
    private long deadline, remaining;

    NativeAnimatedImage(NativeImageSequence sequence, Bitmap first, int edge, float density, Executor worker) {
        this.sequence = sequence; this.first = this.current = first; this.edge = edge; this.worker = worker;
        width = Math.round(first.getWidth() * density); height = Math.round(first.getHeight() * density);
        remaining = sequence.frames.get(0).delay;
    }
    @Override public void start() {
        if (running || ended || disposed) return;
        running = true;
        if (!preparing) schedule(Math.max(1, remaining));
    }
    @Override public void stop() {
        if (!running) return;
        running = false; remaining = Math.max(1, deadline - SystemClock.uptimeMillis()); main.removeCallbacks(tick);
    }
    @Override public boolean isRunning() { return running; }
    private void schedule(long delay) { deadline = SystemClock.uptimeMillis() + delay; main.postAtTime(tick, deadline); }
    private void advance() {
        if (!running || preparing || disposed) return;
        int next = index + 1;
        if (next == sequence.frames.size()) {
            if (sequence.loops != 0 && iterations + 1 >= sequence.loops) { running = false; ended = true; return; }
            next = 0;
        }
        int frame = next, token = epoch; NativeImageSequence source = sequence; preparing = true;
        worker.execute(() -> {
            Bitmap result = null; Exception error = null;
            try {
                synchronized (lock) {
                    if (token != epoch) return;
                    if (renderer == null || rendererEpoch != token) {
                        if (renderer != null) renderer.close();
                        renderer = source.renderer(edge);
                        rendererEpoch = token;
                        // Displayed frame zero is cached; initialize its disposal state before advancing.
                        if (frame != 0) renderer.render(0);
                    }
                    renderer.render(frame); result = renderer.snapshot();
                }
            } catch (Exception failure) { error = failure; }
            Bitmap decoded = result; Exception failure = error;
            main.post(() -> {
                if (token != epoch || disposed) { if (decoded != null) decoded.recycle(); return; }
                preparing = false;
                if (failure != null) { running = false; ended = true; releaseRenderer(token); return; }
                // Do not recycle a displayed bitmap: the hardware renderer may still own its display list.
                current = decoded; if (frame == 0) iterations++; index = frame; remaining = source.frames.get(frame).delay;
                invalidateSelf(); if (running) schedule(remaining);
            });
        });
    }
    private void releaseRenderer(int token) {
        worker.execute(() -> { synchronized (lock) { if (renderer != null && rendererEpoch <= token) { renderer.close(); renderer = null; } } });
    }
    void release(boolean permanently) {
        if (disposed) return;
        stop(); int previous = epoch++; preparing = false; index = iterations = 0; ended = false;
        current = first; if (sequence != null) remaining = sequence.frames.get(0).delay;
        if (permanently) { disposed = true; current = first = null; sequence = null; }
        releaseRenderer(previous); invalidateSelf();
    }
    @Override public void draw(Canvas canvas) { if (current != null) canvas.drawBitmap(current, null, getBounds(), paint); }
    @Override public int getIntrinsicWidth() { return width; }
    @Override public int getIntrinsicHeight() { return height; }
    @Override public void setAlpha(int alpha) { paint.setAlpha(alpha); invalidateSelf(); }
    @Override public void setColorFilter(ColorFilter filter) { paint.setColorFilter(filter); invalidateSelf(); }
    @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
}
