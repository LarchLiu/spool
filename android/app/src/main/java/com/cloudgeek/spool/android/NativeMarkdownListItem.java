package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;

/** List markers sit outside the li content box, as CSS list-style-position: outside does. */
final class NativeMarkdownListItem extends ViewGroup implements NativeMarkdownFlowBox {
    private final NativeMarkdownFlow content;
    private final float markerWidth;
    private View marker;
    private int taskSize, taskTop, taskInset;
    private float roundingY;
    String markerType;

    NativeMarkdownListItem(NativeUi ui, NativeMarkdownFlow content, float markerWidth) {
        super(ui.context);
        this.content = content; this.markerWidth = markerWidth;
        setClipChildren(false); addView(content);
    }

    void marker(View marker) {
        if (this.marker == marker) return;
        if (this.marker != null) removeView(this.marker);
        this.marker = marker; taskSize = taskInset = taskTop = 0;
        if (marker != null) addView(marker);
    }
    void task(View marker, int size, int inset) {
        marker(marker); taskSize = size; taskInset = inset;
    }
    void refreshMarker() { if (marker != null) marker.invalidate(); }

    @Override protected void onMeasure(int width, int height) {
        int available = Math.max(0, MeasureSpec.getSize(width) - taskInset);
        content.measure(MeasureSpec.makeMeasureSpec(available, MeasureSpec.getMode(width)), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
        if (taskSize > 0 && content.getChildCount() > 0 && content.getChildAt(0) instanceof TextView)
            taskTop = ((TextView) content.getChildAt(0)).getBaseline() - taskSize;
        if (marker != null) {
            int markerPixels = taskSize > 0 ? taskSize : Math.round(markerWidth);
            if (marker instanceof TextView) {
                marker.measure(MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
                markerPixels = Math.max(markerPixels, marker.getMeasuredWidth());
            }
            marker.measure(MeasureSpec.makeMeasureSpec(markerPixels, MeasureSpec.EXACTLY),
                    MeasureSpec.makeMeasureSpec(taskSize, taskSize > 0 ? MeasureSpec.EXACTLY : MeasureSpec.UNSPECIFIED));
        }
        setMeasuredDimension(resolveSize(content.getMeasuredWidth() + taskInset, width), content.getMeasuredHeight());
    }

    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        content.layout(taskInset, 0, taskInset + content.getMeasuredWidth(), content.getMeasuredHeight());
        content.layoutRounding();
        if (marker != null) {
            int x = taskSize > 0 ? 0 : -marker.getMeasuredWidth();
            NativeUi.cssTextRounding(marker, roundingY);
            marker.layout(x, taskTop, x + marker.getMeasuredWidth(), taskTop + marker.getMeasuredHeight());
            NativeUi.alignCssText(marker);
        }
    }

    @Override public float cssHeight() { return content.cssHeight(); }
    @Override public float collapsedBefore() { return content.collapsedBefore(); }
    @Override public float collapsedAfter() { return content.collapsedAfter(); }
    @Override public void roundingOffset(float x, float y) { roundingY = y; content.roundingOffset(x, y); }
    @Override public void layoutRounding() {
        content.layoutRounding();
        if (marker != null) { NativeUi.cssTextRounding(marker, roundingY); NativeUi.alignCssText(marker); }
    }
}
