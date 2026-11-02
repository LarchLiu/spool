package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.BlurMaskFilter;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.PorterDuff;
import android.graphics.PorterDuffColorFilter;
import android.graphics.PorterDuffXfermode;
import android.graphics.RectF;
import android.graphics.Rect;
import android.widget.LinearLayout;

/** Rounded CSS border box with a cached, theme-colored shadow, not platform elevation. */
class NativeSurface extends LinearLayout {
    private final float radius, stroke, offsetX, offsetY, sigma, density;
    private final int fill, border;
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint shadowPaint = new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG);
    private final Paint clipPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF outer = new RectF(), inner = new RectF(), center = new RectF();
    private NativeRoundedBox analyticBorder;
    private Bitmap shadow;
    private final int spread;
    private float borderWidth = -1, borderHeight = -1, originX, originY;

    void useAnalyticBorder() {
        if (analyticBorder == null) analyticBorder = new NativeRoundedBox();
        invalidate();
    }

    /** Blink snaps border/background bounds to its CSS pixel grid before display scaling. */
    void setCssBorderBox(float width, float height, float x, float y) {
        if (!Float.isFinite(width) || !Float.isFinite(height) || !Float.isFinite(x) || !Float.isFinite(y) || width < 0 || height < 0)
            throw new IllegalArgumentException("A surface border box must have finite coordinates and nonnegative dimensions");
        borderWidth = width; borderHeight = height;
        originX = x; originY = y;
        updateBounds();
    }

    Rect shadowOutsets() {
        if (sigma <= 0) return new Rect();
        return new Rect((int) Math.ceil(Math.max(0, spread - offsetX)), (int) Math.ceil(Math.max(0, spread - offsetY)),
                (int) Math.ceil(Math.max(0, spread + offsetX)), (int) Math.ceil(Math.max(0, spread + offsetY)));
    }

    NativeSurface(NativeUi ui, int fill, int border, float radius, float shadowX, float shadowY, float blur) {
        this(ui, fill, border, radius, shadowX, shadowY, blur, ui.shadow);
    }

    NativeSurface(NativeUi ui, int fill, int border, float radius, float shadowX, float shadowY, float blur, int shadowColor) {
        super(ui.context);
        density = getResources().getDisplayMetrics().density;
        this.radius = radius * density;
        stroke = border == 0 ? 0 : density;
        this.fill = fill; this.border = border;
        offsetX = shadowX * density; offsetY = shadowY * density;
        sigma = blur * density / 2;
        spread = (int) Math.ceil(sigma * 3) + 2;
        shadowPaint.setColorFilter(new PorterDuffColorFilter(shadowColor, PorterDuff.Mode.SRC_IN));
        clipPaint.setXfermode(new PorterDuffXfermode(PorterDuff.Mode.DST_IN));
        setOrientation(VERTICAL);
        setWillNotDraw(false);
    }

    @Override protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) {
        super.onSizeChanged(width, height, oldWidth, oldHeight);
        updateBounds();
    }

    private void updateBounds() {
        float left = borderWidth < 0 ? 0 : Math.round(originX / density) * density - originX;
        float top = borderHeight < 0 ? 0 : Math.round(originY / density) * density - originY;
        float right = borderWidth < 0 ? getWidth() : Math.round((originX + borderWidth) / density) * density - originX;
        float bottom = borderHeight < 0 ? getHeight() : Math.round((originY + borderHeight) / density) * density - originY;
        if (outer.left == left && outer.top == top && outer.right == right && outer.bottom == bottom) return;
        if (outer.width() != right - left || outer.height() != bottom - top) shadow = null;
        outer.set(left, top, right, bottom);
        inner.set(left + stroke, top + stroke, Math.max(left + stroke, right - stroke), Math.max(top + stroke, bottom - stroke));
        center.set(outer); center.inset(stroke / 2, stroke / 2);
        invalidate();
    }

    private void prepareShadow() {
        if (shadow != null || outer.width() <= 0 || outer.height() <= 0 || sigma <= 0) return;
        shadow = Bitmap.createBitmap((int) Math.ceil(outer.width()) + spread * 2, (int) Math.ceil(outer.height()) + spread * 2, Bitmap.Config.ARGB_8888);
        shadow.setDensity(Bitmap.DENSITY_NONE);
        Canvas canvas = new Canvas(shadow);
        Paint mask = new Paint(Paint.ANTI_ALIAS_FLAG);
        mask.setColor(0xff000000);
        // CSS blur = 2 sigma; Android's legacy blur radius maps to sigma = .57735r + .5.
        mask.setMaskFilter(new BlurMaskFilter(Math.max(.01f, (sigma - .5f) / .57735f), BlurMaskFilter.Blur.NORMAL));
        canvas.drawRoundRect(spread, spread, spread + outer.width(), spread + outer.height(), radius, radius, mask);
        // CSS outer shadows exclude the unshifted padding box, including its antialiased fringe.
        mask.setMaskFilter(null);
        mask.setXfermode(new PorterDuffXfermode(PorterDuff.Mode.DST_OUT));
        canvas.drawRoundRect(spread + stroke - offsetX, spread + stroke - offsetY,
                spread + outer.width() - stroke - offsetX, spread + outer.height() - stroke - offsetY,
                Math.max(0, radius - stroke), Math.max(0, radius - stroke), mask);
    }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        prepareShadow();
        if (shadow != null) canvas.drawBitmap(shadow, outer.left + offsetX - spread, outer.top + offsetY - spread, shadowPaint);
        if (analyticBorder != null) {
            analyticBorder.prepare(outer, radius, stroke, fill, border);
            analyticBorder.draw(canvas);
            return;
        }
        // Blink paints the background at the border center, then the solid centered stroke.
        // Nested outer/inner fills give a different coverage blend along both rounded edges.
        paint.setColor(fill);
        float rounding = Math.max(0, radius - stroke / 2);
        canvas.drawRoundRect(center, rounding, rounding, paint);
        if (border != 0) {
            paint.setColor(border); paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(stroke);
            canvas.drawRoundRect(center, rounding, rounding, paint);
            paint.setStyle(Paint.Style.FILL);
        }
    }

    @Override protected void dispatchDraw(Canvas canvas) {
        if (getChildCount() == 0) return;
        // clipPath can leave hard edges on older renderers. Mask only the children,
        // leaving the rounded border and its overflowing CSS shadow untouched.
        int save = canvas.saveLayer(0, 0, getWidth(), getHeight(), null);
        super.dispatchDraw(canvas);
        int mask = canvas.saveLayer(0, 0, getWidth(), getHeight(), clipPaint);
        paint.setColor(0xff000000);
        canvas.drawRoundRect(inner, Math.max(0, radius - stroke), Math.max(0, radius - stroke), paint);
        canvas.restoreToCount(mask);
        canvas.restoreToCount(save);
    }

    @Override protected void onDetachedFromWindow() {
        shadow = null;
        if (analyticBorder != null) analyticBorder.clear();
        super.onDetachedFromWindow();
    }
}
