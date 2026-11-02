package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.PixelFormat;
import android.graphics.drawable.Drawable;
import android.widget.CheckBox;
import java.util.function.BooleanSupplier;

/** Browser-sized checkbox: 13px glyph and the original 4px/3px input margins. */
final class NativeCheckbox extends CheckBox {
    NativeCheckbox(NativeUi ui) {
        super(ui.context);
        setBackground(null);
        setButtonTintList(null);
        setMinWidth(0); setMinimumWidth(0); setMinHeight(0); setMinimumHeight(0);
        setPadding(0, 0, 0, 0);
        setButtonDrawable(new android.graphics.drawable.InsetDrawable(glyph(ui, this::isChecked, this::isEnabled), ui.dp(4), 0, ui.dp(3), 0));
    }

    static Drawable glyph(NativeUi ui, BooleanSupplier checked, BooleanSupplier enabled) {
        return new Drawable() {
            final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
            final Path tick = new Path();
            @Override public int getIntrinsicWidth() { return ui.dp(13); }
            @Override public int getIntrinsicHeight() { return ui.dp(13); }
            @Override public boolean isStateful() { return true; }
            @Override protected boolean onStateChange(int[] states) { invalidateSelf(); return true; }
            @Override public void draw(Canvas canvas) {
                int save = canvas.save(); canvas.translate(getBounds().left, getBounds().top);
                canvas.scale(getBounds().width() / 13f, getBounds().height() / 13f);
                boolean active = enabled.getAsBoolean(), selected = checked.getAsBoolean();
                int fill = active ? selected ? ui.primary : ui.dark ? 0xff3b3b3b : 0xffffffff
                        : selected ? ui.dark ? 0xff757575 : 0xffd1d1d1 : ui.dark ? 0xff3b3b3b : 0xfff8f8f8;
                int edge = active ? selected ? ui.primary : ui.dark ? 0xff858585 : 0xff767676
                        : selected ? fill : ui.dark ? 0xff626262 : 0xffd1d1d1;
                // A previous checkmark draw must not change the next border's corner join.
                paint.setStrokeCap(Paint.Cap.BUTT); paint.setStrokeJoin(Paint.Join.MITER);
                paint.setStyle(Paint.Style.FILL); paint.setColor(fill); canvas.drawRoundRect(0, 0, 13, 13, 2, 2, paint);
                paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(1); paint.setColor(edge);
                canvas.drawRoundRect(.5f, .5f, 12.5f, 12.5f, 1.5f, 1.5f, paint);
                if (selected) {
                    paint.setColor(ui.dark ? 0xff3b3b3b : active ? 0xffffffff : 0xffededed); paint.setStrokeWidth(1.7f); paint.setStrokeCap(Paint.Cap.ROUND); paint.setStrokeJoin(Paint.Join.ROUND);
                    tick.reset(); tick.moveTo(3, 6.5f); tick.lineTo(5.5f, 9); tick.lineTo(10, 3.5f); canvas.drawPath(tick, paint);
                }
                canvas.restoreToCount(save);
            }
            @Override public void setAlpha(int alpha) {}
            @Override public void setColorFilter(ColorFilter filter) {}
            @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
        };
    }
}
