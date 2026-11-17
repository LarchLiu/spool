package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;

/** Shared file-row selection and pointer feedback. */
class NativeTreeRow extends NativeMiddleTruncate {
    private final NativeUi ui;
    private final float density;
    private final Paint rowPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private boolean selected;

    NativeTreeRow(NativeUi ui) {
        super(ui.context);
        this.ui = ui;
        density = getResources().getDisplayMetrics().density;
    }

    void setTreeState(boolean selected) {
        this.selected = selected;
        setSelected(selected); invalidate();
    }

    protected void drawLeading(Canvas canvas) {}

    @Override protected void onDraw(Canvas canvas) {
        float margin = 2 * density, radius = 6 * density;
        setMarkerBackground(selected ? ui.selected : isPressed() || isHovered() ? ui.hover : ui.elevated);
        if (selected || isPressed() || isHovered()) {
            rowPaint.setColor(selected ? ui.selected : ui.hover);
            canvas.drawRoundRect(margin, 0, getWidth() - margin, getHeight(), radius, radius, rowPaint);
        }
        drawLeading(canvas);
        super.onDraw(canvas);
    }
}
