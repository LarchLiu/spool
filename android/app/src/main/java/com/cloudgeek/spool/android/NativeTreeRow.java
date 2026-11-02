package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;

/** Shared Pierre row ink, with CSS-density focus outlines rather than rounded dp strokes. */
class NativeTreeRow extends NativeMiddleTruncate {
    private final NativeUi ui;
    private final float density;
    private final Paint rowPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private boolean selected, focused;

    NativeTreeRow(NativeUi ui) {
        super(ui.context);
        this.ui = ui;
        density = getResources().getDisplayMetrics().density;
    }

    void setTreeState(boolean selected, boolean focused) {
        this.selected = selected; this.focused = focused;
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
        if (focused) {
            float half = density / 2;
            rowPaint.setStyle(Paint.Style.STROKE); rowPaint.setStrokeWidth(density); rowPaint.setColor(0xff009fff);
            canvas.drawRoundRect(margin + half, half, getWidth() - margin - half, getHeight() - half,
                    radius - half, radius - half, rowPaint);
            rowPaint.setStyle(Paint.Style.FILL);
        }
    }
}
