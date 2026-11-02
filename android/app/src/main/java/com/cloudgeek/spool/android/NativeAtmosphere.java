package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Bitmap;
import android.graphics.ColorSpace;
import android.graphics.ColorFilter;
import android.graphics.Matrix;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.PixelFormat;
import android.graphics.RadialGradient;
import android.graphics.Rect;
import android.graphics.Shader;
import android.graphics.drawable.Drawable;

/** Original Web CSS elliptical gradients, repeating grid and login orbit layers. */
final class NativeAtmosphere extends Drawable {
    private final NativeUi ui;
    private final boolean story;
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private Shader gradient;
    private final Path outer = new Path(), inner = new Path(), border = new Path();
    private final Paint bitmapPaint = new Paint(Paint.DITHER_FLAG);
    private Bitmap composedStory;

    NativeAtmosphere(NativeUi ui, boolean story) { this.ui = ui; this.story = story; }

    @Override protected void onBoundsChange(Rect bounds) {
        float width = bounds.width(), height = bounds.height();
        if (composedStory != null) { composedStory.recycle(); composedStory = null; }
        if (width <= 0 || height <= 0) { gradient = null; return; }
        float x = width * (story ? .8f : .6f), y = height * (story ? .8f : .44f);
        float end = story ? .6f : .58f;
        float rx = Math.max(x, width - x) * (float) Math.sqrt(2) * end;
        float ry = Math.max(y, height - y) * (float) Math.sqrt(2) * end;
        int color = story ? (ui.dark ? 0x453b4b35 : 0x65d8e2ca) : ui.atmosphere;
        gradient = new RadialGradient(0, 0, 1, color, color & 0x00ffffff, Shader.TileMode.CLAMP);
        Matrix transform = new Matrix(); transform.setScale(rx, ry); transform.postTranslate(x, y);
        gradient.setLocalMatrix(transform);
        float cx = width - ui.dp(91), cy = height + ui.dp(29);
        ring(outer, cx, cy, 171, 271); ring(inner, cx, cy, 171, 221); ring(border, cx, cy, 170, 171);
        paint.setDither(true);
        if (story && (long) bounds.width() * bounds.height() <= 2_000_000) {
            // Keep translucent CSS layers in float precision until their final opaque blit.
            composedStory = Bitmap.createBitmap(bounds.width(), bounds.height(), Bitmap.Config.RGBA_F16,
                    false, ColorSpace.get(ColorSpace.Named.SRGB));
            composedStory.setDensity(Bitmap.DENSITY_NONE);
            Canvas layers = new Canvas(composedStory); drawSurface(layers); drawOrbit(layers);
        }
    }

    private void ring(Path path, float x, float y, float inside, float outside) {
        float density = ui.context.getResources().getDisplayMetrics().density;
        path.reset(); path.setFillType(Path.FillType.EVEN_ODD);
        path.addCircle(x, y, outside * density, Path.Direction.CW);
        path.addCircle(x, y, inside * density, Path.Direction.CCW);
    }

    @Override public void draw(Canvas canvas) {
        if (composedStory != null) { canvas.drawBitmap(composedStory, 0, 0, bitmapPaint); return; }
        drawSurface(canvas);
        if (story) drawOrbit(canvas);
    }

    void drawSurface(Canvas canvas) {
        float width = getBounds().width(), height = getBounds().height();
        if (gradient == null) return;
        canvas.drawColor(story ? (ui.dark ? ui.sidebar : ui.soft) : ui.elevated);
        if (story) {
            paint.setStyle(Paint.Style.FILL);
            paint.setColor(ui.dark ? 0x08717b64 : 0x0dc7d1b4);
            // The last CSS gradient pixel ramps from transparent to the grid color.
            for (float y = height - ui.dp(48); y >= 0; y -= ui.dp(48))
                canvas.drawRect(0, y, width, y + ui.dp(1), paint);
        }
        paint.setColor(0xffffffff); paint.setShader(gradient);
        canvas.drawRect(0, 0, width, height, paint); paint.setShader(null);
    }

    private void drawOrbit(Canvas canvas) {
        // Both outer shadows exclude the border box, but overlap in the inner annulus.
        paint.setStyle(Paint.Style.FILL);
        paint.setColor(0x06b7c6a3); canvas.drawPath(outer, paint);
        paint.setColor(0x0ab7c6a3); canvas.drawPath(inner, paint);
        paint.setColor(0x44b7c6a3); canvas.drawPath(border, paint);
    }

    @Override public void setAlpha(int alpha) {}
    @Override public void setColorFilter(ColorFilter filter) {}
    @Override public int getOpacity() { return PixelFormat.OPAQUE; }
}
