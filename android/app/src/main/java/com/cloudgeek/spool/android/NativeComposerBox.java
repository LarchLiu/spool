package com.cloudgeek.spool.android;

import android.view.View;
import android.widget.LinearLayout;

/** CSS border/content flow keeps fractional density until each cumulative edge is laid out. */
final class NativeComposerBox extends NativeSurface {
    private final float density;

    NativeComposerBox(NativeUi ui) {
        super(ui, ui.elevated, ui.borderStrong, 19, 0, 8, 32);
        useAnalyticBorder();
        density = getResources().getDisplayMetrics().density;
        setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
    }

    private float height(View child) {
        if (child instanceof NativeAutosizeInput) return ((NativeAutosizeInput) child).cssHeight();
        if (child instanceof NativeComposerControls) return 49 * density;
        return child.getMeasuredHeight();
    }

    private int contentWidth(int width) {
        return Math.max(0, Math.round(Math.round(width / density) * density - 2 * density));
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        super.onMeasure(widthSpec, heightSpec);
        int width = getMeasuredWidth();
        int contentWidth = contentWidth(width);
        float cursor = density;
        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i);
            if (child.getVisibility() == GONE) continue;
            LinearLayout.LayoutParams params = (LinearLayout.LayoutParams) child.getLayoutParams();
            int childWidth = params.width >= 0 ? params.width : Math.max(0, contentWidth - params.leftMargin - params.rightMargin);
            int childHeight = params.height >= 0 ? MeasureSpec.makeMeasureSpec(params.height, MeasureSpec.EXACTLY)
                    : MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
            child.measure(MeasureSpec.makeMeasureSpec(childWidth, params.width == -2 ? MeasureSpec.AT_MOST : MeasureSpec.EXACTLY), childHeight);
            cursor += params.topMargin + height(child) + params.bottomMargin;
        }
        setMeasuredDimension(width, resolveSize(Math.round(cursor + density), heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        View wrapper = (View) getParent();
        int x = wrapper.getLeft() + getLeft(), y = wrapper.getTop() + getTop();
        float paintX = Math.round(x / density) * density, paintY = Math.round(y / density) * density;
        float cursor = density;
        int contentLeft = Math.round(paintX + density) - x;
        int contentWidth = contentWidth(getWidth());
        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i);
            if (child.getVisibility() == GONE) continue;
            LinearLayout.LayoutParams params = (LinearLayout.LayoutParams) child.getLayoutParams();
            cursor += params.topMargin;
            int childTop = Math.round(paintY + cursor) - y;
            cursor += height(child);
            int childBottom = Math.round(paintY + cursor) - y;
            int childWidth = params.width == -1 ? Math.max(0, contentWidth - params.leftMargin - params.rightMargin) : child.getMeasuredWidth();
            child.measure(MeasureSpec.makeMeasureSpec(childWidth, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(Math.max(0, childBottom - childTop), MeasureSpec.EXACTLY));
            child.layout(contentLeft + params.leftMargin, childTop, contentLeft + params.leftMargin + childWidth, childBottom);
            cursor += params.bottomMargin;
        }
    }
}
