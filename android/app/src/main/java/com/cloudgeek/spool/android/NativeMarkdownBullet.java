package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;

/** Browser list symbols are geometry, not the smaller font-dependent U+2022 glyph. */
final class NativeMarkdownBullet extends View {
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final float size, lineHeight;
    final String style;

    NativeMarkdownBullet(NativeUi ui, float size, float lineHeight, String style, int color) {
        super(ui.context);
        this.size = size; this.lineHeight = lineHeight; this.style = style;
        paint.setColor(color);
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
    }

    @Override protected void onMeasure(int width, int height) {
        setMeasuredDimension(MeasureSpec.getSize(width), Math.round(lineHeight));
    }

    @Override protected void onDraw(Canvas canvas) {
        float x = getWidth() - size * .95f, y = lineHeight / 2;
        float radius = size / 7;
        if (style.equals("circle")) {
            paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(size / 14);
            canvas.drawCircle(x, y, radius + size / 28, paint);
        } else {
            paint.setStyle(Paint.Style.FILL);
            if (style.equals("square")) canvas.drawRect(x - radius, y - radius, x + radius, y + radius, paint);
            else canvas.drawCircle(x, y, radius, paint);
        }
    }
}
