package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.drawable.GradientDrawable;
import android.widget.SeekBar;

/** Web's 22px range track, 24px thumb and tick geometry, with native keyboard/a11y handling. */
final class NativeReasoningSlider extends SeekBar {
    private final NativeUi ui;
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Path track = new Path();

    NativeReasoningSlider(NativeUi ui, int levels, int selected) {
        super(ui.context);
        this.ui = ui;
        setPadding(ui.dp(12), 0, ui.dp(12), 0);
        GradientDrawable thumb = ui.shape(0x00000000, 999, 0);
        thumb.setSize(ui.dp(24), ui.dp(24));
        setThumb(thumb);
        setThumbOffset(ui.dp(12));
        setSplitTrack(false);
        setMax(Math.max(0, levels - 1));
        setProgress(selected);
        setEnabled(levels >= 2);
        setContentDescription(ui.t("Reasoning level"));
        setBackground(null);
    }

    @Override protected synchronized void onDraw(Canvas canvas) {
        float y = getHeight() / 2f, height = ui.dp(22), radius = ui.dp(15);
        float fraction = getMax() == 0 ? 1 : (float) getProgress() / getMax();
        paint.setColor(ui.track);
        paint.setAlpha(isEnabled() ? 255 : 128);
        canvas.drawRoundRect(0, y - height / 2, getWidth(), y + height / 2, radius, radius, paint);
        track.reset();
        track.addRoundRect(0, y - height / 2, getWidth(), y + height / 2, radius, radius, Path.Direction.CW);
        int checkpoint = canvas.save();
        canvas.clipPath(track);
        paint.setColor(0xff369bea);
        paint.setAlpha(isEnabled() ? 255 : 128);
        canvas.drawRect(0, y - height / 2, getWidth() * fraction, y + height / 2, paint);
        canvas.restoreToCount(checkpoint);
        paint.setColor(ui.elevated);
        paint.setAlpha(isEnabled() ? 166 : 83);
        for (int i = 0; i <= getMax(); i++) {
            if (i == getProgress()) continue;
            float x = ui.dp(12) + (getWidth() - ui.dp(24)) * (getMax() == 0 ? 0 : (float) i / getMax());
            canvas.drawCircle(x, y, ui.dp(1.5f), paint);
        }
        float x = ui.dp(12) + (getWidth() - ui.dp(24)) * (getMax() == 0 ? 0 : fraction);
        paint.setColor(ui.thumb);
        paint.setAlpha(isEnabled() ? 255 : 128);
        paint.setShadowLayer(ui.dp(4), 0, ui.dp(1), 0x331e321e);
        canvas.drawCircle(x, y, ui.dp(12), paint);
        paint.clearShadowLayer();
        paint.setStyle(Paint.Style.STROKE);
        paint.setStrokeWidth(ui.dp(1));
        paint.setColor(ui.borderStrong);
        canvas.drawCircle(x, y, ui.dp(11.5f), paint);
        paint.setStyle(Paint.Style.FILL);
        paint.setAlpha(255);
    }
}
