package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.RectF;
import android.os.Bundle;
import android.util.SparseArray;
import android.view.GestureDetector;
import android.view.MotionEvent;
import android.view.ScaleGestureDetector;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.OverScroller;
import java.util.ArrayList;

/** Continuous, width-fitted PDF canvas. Only visible/adjacent pages retain bitmaps. */
final class NativePdfPages extends View {
    static final int PREFETCH_PAGE_BUDGET = 3;
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG);
    private final GestureDetector gestures;
    private final ScaleGestureDetector pinch;
    private final OverScroller fling;
    private final Runnable changed;
    private final float gap;
    private final SparseArray<Rendered> rendered = new SparseArray<>();
    private NativePdfDocument.Size[] sizes = new NativePdfDocument.Size[0];
    private float[] tops = new float[0];
    private float totalHeight, zoom = 1, x, y;

    private static final class Rendered {
        final NativePdfDocument.Page page;
        final int width;
        Rendered(NativePdfDocument.Page page, int width) { this.page = page; this.width = width; }
    }

    NativePdfPages(NativeUi ui, Runnable changed) {
        super(ui.context); this.changed = changed; gap = ui.dp(12);
        setBackgroundColor(ui.soft); setFocusable(true); setVerticalScrollBarEnabled(true);
        fling = new OverScroller(ui.context);
        gestures = new GestureDetector(ui.context, new GestureDetector.SimpleOnGestureListener() {
            @Override public boolean onDown(MotionEvent event) { fling.forceFinished(true); return true; }
            @Override public boolean onSingleTapUp(MotionEvent event) { performClick(); return true; }
            @Override public boolean onDoubleTap(MotionEvent event) {
                scale(zoom > 1 ? 1 : 2, event.getX(), event.getY()); return true;
            }
            @Override public boolean onScroll(MotionEvent start, MotionEvent end, float dx, float dy) {
                if (!pinch.isInProgress()) scrollByPixels(dx, dy);
                return true;
            }
            @Override public boolean onFling(MotionEvent start, MotionEvent end, float vx, float vy) {
                if (pinch.isInProgress()) return false;
                fling.fling(Math.round(x), Math.round(y), Math.round(-vx), Math.round(-vy), 0, Math.round(maxX()), 0, Math.round(maxY()));
                postInvalidateOnAnimation(); return true;
            }
        });
        pinch = new ScaleGestureDetector(ui.context, new ScaleGestureDetector.SimpleOnScaleGestureListener() {
            @Override public boolean onScale(ScaleGestureDetector detector) {
                scale(zoom * detector.getScaleFactor(), detector.getFocusX(), detector.getFocusY()); return true;
            }
        });
    }

    float zoom() { return zoom; }
    float scrollOffset() { return y; }
    int currentPage() { return sizes.length == 0 ? 0 : pageAt((y + gap * zoom + 1) / zoom); }
    int pageWidth() { return sizes.length == 0 ? 0 : sizes[currentPage()].width; }
    int pageHeight() { return sizes.length == 0 ? 0 : sizes[currentPage()].height; }
    int bitmapPixels() {
        Rendered value = rendered.get(currentPage());
        return value == null ? 0 : value.page.bitmap.getWidth() * value.page.bitmap.getHeight();
    }
    int cachedPages() { return rendered.size(); }
    boolean ready() { return rendered.get(currentPage()) != null; }
    int requestedWidth() { return Math.min(NativePdfDocument.MAX_EDGE, Math.max(1, Math.round((getWidth() - 2 * gap) * zoom))); }
    boolean needs(int index, int width) { Rendered value = rendered.get(index); return value == null || value.width < width; }
    void zoomBy(float factor) { scale(zoom * factor, getWidth() / 2f, getHeight() / 2f); }
    void fit() { scale(1, getWidth() / 2f, getHeight() / 2f); }

    void document(NativePdfDocument.Size[] value) {
        clear(); sizes = value; geometry(); changed.run();
    }

    void page(int index, NativePdfDocument.Page value, int width) {
        rendered.put(index, new Rendered(value, width)); invalidate();
    }

    void clear() {
        fling.forceFinished(true); rendered.clear(); sizes = new NativePdfDocument.Size[0]; tops = new float[0];
        zoom = 1; x = y = totalHeight = 0; invalidate();
    }

    void goTo(int index) {
        if (index < 0 || index >= sizes.length) return;
        fling.forceFinished(true); y = (tops[index] - gap) * zoom; moved();
    }

    void scrollByPixels(float dx, float dy) { x += dx; y += dy; moved(); }

    /** Retain all visible pages; prefetch neighbours only when fewer than three pages are visible. */
    int[] renderTargets() {
        if (sizes.length == 0 || getWidth() <= 0 || getHeight() <= 0) return new int[0];
        ArrayList<Integer> targets = new ArrayList<>();
        int first = pageAt(y / zoom), last = pageAt((y + getHeight()) / zoom);
        targets.add(currentPage());
        for (int i = first; i <= last; i++) if (i != currentPage()) targets.add(i);
        if (zoom == 1) {
            if (last + 1 < sizes.length && targets.size() < PREFETCH_PAGE_BUDGET) targets.add(last + 1);
            if (first > 0 && targets.size() < PREFETCH_PAGE_BUDGET) targets.add(first - 1);
        }
        int[] result = new int[targets.size()];
        for (int i = 0; i < result.length; i++) result[i] = targets.get(i);
        return result;
    }

    void retain(int[] targets) {
        for (int i = rendered.size() - 1; i >= 0; i--) {
            int index = rendered.keyAt(i); boolean keep = false;
            for (int target : targets) if (index == target) { keep = true; break; }
            // Let Android release bitmaps after pending hardware display lists stop using them.
            if (!keep) rendered.removeAt(i);
        }
        invalidate();
    }

    private void geometry() {
        tops = new float[sizes.length]; float top = gap, width = Math.max(1, getWidth() - 2 * gap);
        for (int i = 0; i < sizes.length; i++) {
            tops[i] = top; top += width * sizes[i].height / sizes[i].width + gap;
        }
        totalHeight = top; clamp(); invalidate();
    }

    private int pageAt(float offset) {
        int low = 0, high = tops.length - 1;
        while (low < high) {
            int mid = (low + high + 1) >>> 1;
            if (tops[mid] <= offset) low = mid; else high = mid - 1;
        }
        return low;
    }

    private float maxX() { return Math.max(0, getWidth() * (zoom - 1)); }
    private float maxY() {
        // A short final page can still align to the top when jumping to it.
        return sizes.length == 0 ? 0 : Math.max(0, Math.max(totalHeight * zoom - getHeight(), (tops[tops.length - 1] - gap) * zoom));
    }
    private void clamp() { x = Math.max(0, Math.min(maxX(), x)); y = Math.max(0, Math.min(maxY(), y)); }
    private void moved() { clamp(); invalidate(); awakenScrollBars(); changed.run(); }

    private void scale(float value, float focusX, float focusY) {
        fling.forceFinished(true);
        float next = Math.max(1, Math.min(4, value)), ratio = next / zoom;
        x = (x + focusX) * ratio - focusX; y = (y + focusY) * ratio - focusY;
        zoom = next; moved();
    }

    @Override protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) {
        int anchor = currentPage();
        // Anchor at the page's navigation position; the fixed gutter must not scale with width.
        float fraction = sizes.length == 0 || oldWidth <= 0 ? 0 : (y / zoom - (tops[anchor] - gap)) / Math.max(1, oldWidth - 2 * gap);
        geometry();
        if (sizes.length > 0 && oldWidth > 0) y = (tops[anchor] - gap + fraction * Math.max(1, width - 2 * gap)) * zoom;
        clamp(); if (width > 0 && height > 0) changed.run();
    }

    @Override public void computeScroll() {
        if (fling.computeScrollOffset()) { x = fling.getCurrX(); y = fling.getCurrY(); moved(); postInvalidateOnAnimation(); }
    }
    @Override public boolean canScrollVertically(int direction) { return direction < 0 ? y > 0 : y < maxY(); }
    @Override protected int computeVerticalScrollRange() { return Math.round(maxY() + getHeight()); }
    @Override protected int computeVerticalScrollOffset() { return Math.round(y); }
    @Override protected int computeVerticalScrollExtent() { return getHeight(); }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        if (sizes.length == 0) return;
        float left = gap * zoom - x, width = Math.max(1, getWidth() - 2 * gap) * zoom;
        for (int i = pageAt(y / zoom); i < sizes.length; i++) {
            float top = tops[i] * zoom - y;
            if (top >= getHeight()) break;
            RectF bounds = new RectF(left, top, left + width, top + width * sizes[i].height / sizes[i].width);
            paint.setColor(Color.WHITE); canvas.drawRect(bounds, paint);
            Rendered value = rendered.get(i);
            if (value != null) canvas.drawBitmap(value.page.bitmap, null, bounds, paint);
        }
    }

    @Override public boolean onTouchEvent(MotionEvent event) {
        if (getParent() != null) getParent().requestDisallowInterceptTouchEvent(true);
        pinch.onTouchEvent(event); gestures.onTouchEvent(event); return true;
    }
    @Override public boolean performClick() { super.performClick(); return true; }
    @Override public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info); info.setScrollable(true);
        if (canScrollVertically(1)) info.addAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_FORWARD);
        if (canScrollVertically(-1)) info.addAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_BACKWARD);
    }
    @Override public boolean performAccessibilityAction(int action, Bundle arguments) {
        if (action == AccessibilityNodeInfo.ACTION_SCROLL_FORWARD && canScrollVertically(1)) { scrollByPixels(0, getHeight() * .8f); return true; }
        if (action == AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD && canScrollVertically(-1)) { scrollByPixels(0, -getHeight() * .8f); return true; }
        return super.performAccessibilityAction(action, arguments);
    }
}
