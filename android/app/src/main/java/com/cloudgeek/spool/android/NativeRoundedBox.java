package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.RectF;

/** Cached analytic border coverage; only the four corners need two-dimensional bitmaps. */
final class NativeRoundedBox {
    private final Rect[] bounds = new Rect[9];
    private final Bitmap[] backgrounds = new Bitmap[9], borders = new Bitmap[9];
    private final Paint paint = new Paint();
    private final RectF previous = new RectF();
    private float previousRadius = -1;
    private float previousStroke;
    private int previousFill, previousBorder;

    NativeRoundedBox() { for (int i = 0; i < bounds.length; i++) bounds[i] = new Rect(); }

    void prepare(RectF outer, float radius, float stroke, int fill, int border) {
        radius = Math.min(radius, Math.min(outer.width(), outer.height()) / 2);
        if (previous.equals(outer) && previousRadius == radius && previousStroke == stroke
                && previousFill == fill && previousBorder == border) return;
        clear();
        previous.set(outer); previousRadius = radius;
        previousStroke = stroke; previousFill = fill; previousBorder = border;
        if (outer.isEmpty()) return;
        int middleX = (int) Math.floor(outer.centerX()), middleY = (int) Math.floor(outer.centerY());
        int left = (int) Math.floor(outer.left - .5f), top = (int) Math.floor(outer.top - .5f);
        int right = (int) Math.ceil(outer.right + .5f), bottom = (int) Math.ceil(outer.bottom + .5f);
        int leftEnd = Math.min(middleX, (int) Math.ceil(outer.left + radius));
        int rightStart = Math.max(middleX, (int) Math.floor(outer.right - radius));
        int topEnd = Math.min(middleY, (int) Math.ceil(outer.top + radius));
        int bottomStart = Math.max(middleY, (int) Math.floor(outer.bottom - radius));
        int[] xs = {left, leftEnd, rightStart, right}, ys = {top, topEnd, bottomStart, bottom};
        for (int row = 0; row < 3; row++) for (int column = 0; column < 3; column++)
            bounds[row * 3 + column].set(xs[column], ys[row], xs[column + 1], ys[row + 1]);
        for (int i = 0; i < bounds.length; i++) {
            Rect box = bounds[i];
            if (box.isEmpty()) continue;
            int width = i % 3 == 1 ? 1 : box.width(), height = i / 3 == 1 ? 1 : box.height();
            int[] backgroundPixels = new int[width * height], borderPixels = new int[width * height];
            for (int y = 0; y < height; y++) for (int x = 0; x < width; x++) {
                float px = box.left + x + .5f, py = box.top + y + .5f;
                float background = coverage(outer, radius, stroke / 2, px, py);
                float edge = border == 0 ? 0 : Math.max(0, coverage(outer, radius, 0, px, py)
                        - coverage(outer, radius, stroke, px, py));
                backgroundPixels[y * width + x] = covered(fill, background);
                borderPixels[y * width + x] = covered(border, edge);
            }
            backgrounds[i] = Bitmap.createBitmap(backgroundPixels, width, height, Bitmap.Config.ARGB_8888);
            borders[i] = Bitmap.createBitmap(borderPixels, width, height, Bitmap.Config.ARGB_8888);
            backgrounds[i].setDensity(Bitmap.DENSITY_NONE); borders[i].setDensity(Bitmap.DENSITY_NONE);
        }
    }

    // First-order distance to the implicit circular edge matches analytic GPU round-rect AA.
    // Straight edges retain exact signed distance; no supersampling or image scaling is used.
    private static float coverage(RectF outer, float radius, float inset, float x, float y) {
        float r = Math.max(0, radius - inset);
        float dx = Math.abs(x - outer.centerX()) - Math.max(0, outer.width() / 2 - inset - r);
        float dy = Math.abs(y - outer.centerY()) - Math.max(0, outer.height() / 2 - inset - r);
        float qx = Math.max(dx, 0), qy = Math.max(dy, 0);
        float length = (float) Math.hypot(qx, qy);
        float distance = dx <= 0 || dy <= 0 ? Math.max(dx, dy) - r
                : (length * length - r * r) / (2 * length);
        return Math.max(0, Math.min(1, .5f - distance));
    }

    private static int covered(int color, float coverage) { return color & 0xffffff | Math.round((color >>> 24) * coverage) << 24; }

    void draw(Canvas canvas) {
        for (int i = 0; i < bounds.length; i++) if (backgrounds[i] != null) {
            canvas.drawBitmap(backgrounds[i], null, bounds[i], paint);
            canvas.drawBitmap(borders[i], null, bounds[i], paint);
        }
    }

    void clear() {
        // RenderThread may still reference a previous frame's bitmap; let its owner release it.
        for (int i = 0; i < backgrounds.length; i++) { backgrounds[i] = null; borders[i] = null; }
        previousRadius = -1;
    }
}
