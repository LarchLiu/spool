package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;

/** Native block flow: collapse adjacent CSS margins and round cumulative positions once. */
class NativeMarkdownFlow extends LinearLayout implements NativeMarkdownFlowBox {
    static final class Item {
        final View view;
        final float before, after;
        float y;
        Item(View view, float before, float after) { this.view = view; this.before = before; this.after = after; }
    }
    private final List<Item> items = new ArrayList<>();
    boolean collapseTop = true, collapseBottom = true, zeroFirst, zeroLast;
    private float left, top, right, bottom, height, escapedBefore, escapedAfter;
    private float roundingX, roundingY;
    private boolean roundingDirty;
    private final Paint border = new Paint();
    private float borderWidth;

    NativeMarkdownFlow(NativeUi ui) {
        super(ui.context);
        setOrientation(VERTICAL);
        setClipChildren(false);
        setClipToPadding(false);
    }

    void add(View child, float before, float after) {
        items.add(new Item(child, before, after));
        addView(child, new LinearLayout.LayoutParams(-1, -2));
    }

    void clear() { items.clear(); removeAllViews(); }

    void replace(List<Item> next) {
        List<View> views = new ArrayList<>();
        for (Item item : next) {
            if (item.view.getParent() == null) item.view.setLayoutParams(new LinearLayout.LayoutParams(-1, -2));
            views.add(item.view);
        }
        NativeKeyedViews.sync(this, views);
        items.clear(); items.addAll(next); requestLayout();
    }

    void inset(float left, float top, float right, float bottom) {
        if (this.left == left && this.top == top && this.right == right && this.bottom == bottom) return;
        this.left = left; this.top = top; this.right = right; this.bottom = bottom;
        requestLayout();
    }

    void leftBorder(float width, int color) {
        borderWidth = width; border.setColor(color); setWillNotDraw(false);
    }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        if (borderWidth > 0) canvas.drawRect(0, 0, borderWidth, getHeight(), border);
    }

    static float contentHeight(View view) {
        if (view instanceof NativeMarkdownFlowBox) return ((NativeMarkdownFlowBox) view).cssHeight();
        if (view instanceof TextView) return NativeUi.cssTextHeight((TextView) view);
        return view.getMeasuredHeight();
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int available = Math.max(0, MeasureSpec.getSize(widthSpec) - Math.round(left + right));
        int mode = MeasureSpec.getMode(widthSpec);
        int widest = 0;
        for (Item item : items) {
            item.view.measure(MeasureSpec.makeMeasureSpec(available, mode), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            widest = Math.max(widest, item.view.getMeasuredWidth());
        }
        int width = resolveSize(widest + Math.round(left + right), widthSpec);
        if (mode != MeasureSpec.EXACTLY) for (Item item : items)
            item.view.measure(MeasureSpec.makeMeasureSpec(Math.max(0, width - Math.round(left + right)), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
        float y = top, previous = 0;
        escapedBefore = escapedAfter = 0;
        for (int i = 0; i < items.size(); i++) {
            Item item = items.get(i);
            float before = i == 0 && zeroFirst ? 0 : item.before;
            float after = i == items.size() - 1 && zeroLast ? 0 : item.after;
            if (item.view instanceof NativeMarkdownFlowBox) {
                before = Math.max(before, ((NativeMarkdownFlowBox) item.view).collapsedBefore());
                after = Math.max(after, ((NativeMarkdownFlowBox) item.view).collapsedAfter());
            }
            if (i == 0 && collapseTop) escapedBefore = before;
            else y += Math.max(previous, before);
            item.y = y;
            y += contentHeight(item.view);
            previous = after;
        }
        if (collapseBottom) escapedAfter = previous;
        else y += previous;
        height = y + bottom;
        setMeasuredDimension(width, resolveSize(Math.round(height), heightSpec));
    }

    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        layoutItems();
    }

    private void layoutItems() {
        roundingDirty = false;
        for (Item item : items) {
            int x = Math.round(left + roundingX), y = Math.round(item.y + roundingY);
            if (item.view instanceof NativeMarkdownFlowBox)
                ((NativeMarkdownFlowBox) item.view).roundingOffset(left + roundingX - x, item.y + roundingY - y);
            NativeUi.cssTextRounding(item.view, item.y + roundingY - y);
            item.view.layout(x, y, x + item.view.getMeasuredWidth(), y + item.view.getMeasuredHeight());
            if (item.view instanceof NativeMarkdownFlowBox) ((NativeMarkdownFlowBox) item.view).layoutRounding();
            NativeUi.alignCssText(item.view);
        }
    }

    @Override public void roundingOffset(float x, float y) {
        if (roundingX != x || roundingY != y) { roundingX = x; roundingY = y; roundingDirty = true; forceLayout(); }
    }

    @Override public void layoutRounding() { if (roundingDirty) layoutItems(); }

    @Override public float cssHeight() { return height; }
    @Override public float collapsedBefore() { return escapedBefore; }
    @Override public float collapsedAfter() { return escapedAfter; }
}

interface NativeMarkdownFlowBox {
    float cssHeight();
    default float collapsedBefore() { return 0; }
    default float collapsedAfter() { return 0; }
    default void roundingOffset(float x, float y) {}
    default void layoutRounding() {}
}
