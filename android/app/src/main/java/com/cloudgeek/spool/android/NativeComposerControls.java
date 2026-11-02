package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewGroup;

/** The composer's two CSS flex groups, including proportional shrink on narrow windows. */
final class NativeComposerControls extends ViewGroup {
    private final NativeUi ui;
    private final View add, send;
    private final NativeUi.ChoiceButton permission, model;
    private boolean phone = true;

    NativeComposerControls(NativeUi ui, View add, NativeUi.ChoiceButton permission,
            NativeUi.ChoiceButton model, View send) {
        super(ui.context);
        this.ui = ui; this.add = add; this.permission = permission; this.model = model; this.send = send;
        addView(add); addView(permission); addView(model); addView(send);
        setPadding(ui.dp(10), 0, ui.dp(11), ui.dp(9));
    }

    void setViewportWidth(int width) {
        phone = width <= ui.dp(600);
        permission.setViewportWidth(width); model.setViewportWidth(width);
        setPadding(ui.dp(phone ? 10 : 17), 0, ui.dp(11), ui.dp(9));
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec);
        int available = Math.max(0, width - getPaddingLeft() - getPaddingRight());
        int buttonSpec = MeasureSpec.makeMeasureSpec(ui.dp(32), MeasureSpec.EXACTLY);
        int modelHeight = MeasureSpec.makeMeasureSpec(ui.dp(30), MeasureSpec.EXACTLY);
        int natural = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
        add.measure(buttonSpec, buttonSpec); send.measure(buttonSpec, buttonSpec);
        permission.measure(natural, buttonSpec);
        model.maximumLabel = phone ? 100 : 155;
        model.measure(natural, modelHeight);
        float tools = add.getMeasuredWidth() + ui.dp(6) + permission.getMeasuredWidth();
        int actionGap = ui.dp(phone ? 8 : 10);
        float actions = model.getMeasuredWidth() + actionGap + send.getMeasuredWidth();
        float deficit = tools + ui.dp(phone ? 5 : 10) + actions - available;
        // Both groups have min-width:0 and flex-shrink:1; their bases determine the shrink ratio.
        float actionWidth = actions - Math.max(0, deficit) * actions / (tools + actions);
        if (phone) actionWidth = Math.min(actionWidth, available * (permission.viewportWidth <= ui.dp(400) ? .72f : .57f));
        int modelWidth = Math.max(0, Math.round(actionWidth) - actionGap - send.getMeasuredWidth());
        model.measure(MeasureSpec.makeMeasureSpec(modelWidth, MeasureSpec.EXACTLY), modelHeight);
        setMeasuredDimension(resolveSize(width, widthSpec), resolveSize(ui.dp(49), heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int contentHeight = getHeight() - getPaddingTop() - getPaddingBottom();
        place(add, getPaddingLeft(), contentHeight);
        place(permission, add.getRight() + ui.dp(6), contentHeight);
        place(send, getWidth() - getPaddingRight() - send.getMeasuredWidth(), contentHeight);
        place(model, send.getLeft() - ui.dp(phone ? 8 : 10) - model.getMeasuredWidth(), contentHeight);
    }

    private void place(View child, int left, int contentHeight) {
        int top = getPaddingTop() + Math.round((contentHeight - child.getMeasuredHeight()) / 2f);
        child.layout(left, top, left + child.getMeasuredWidth(), top + child.getMeasuredHeight());
    }

    @Override protected LayoutParams generateDefaultLayoutParams() { return new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT); }
}
