package com.cloudgeek.spool.android;

import android.content.Context;
import android.graphics.Rect;
import android.graphics.Bitmap;
import android.graphics.drawable.Drawable;
import android.view.ViewTreeObserver;
import android.widget.ImageView;

/** Image lifecycle shared by attachment cards, file images and modal previews. */
class NativeImageView extends ImageView {
    private final Rect visible = new Rect();
    private final ViewTreeObserver.OnPreDrawListener visibility = () -> { syncAnimation(); return true; };
    NativeImageView(Context context) { super(context); }
    @Override public void setImageBitmap(Bitmap bitmap) {
        // ImageView clears its drawable internally before dispatching setImageDrawable().
        if (getDrawable() instanceof NativeAnimatedImage) ((NativeAnimatedImage) getDrawable()).release(true);
        super.setImageBitmap(bitmap);
    }
    @Override public void setImageDrawable(Drawable drawable) {
        Drawable previous = getDrawable();
        if (previous != drawable && previous instanceof NativeAnimatedImage) ((NativeAnimatedImage) previous).release(true);
        super.setImageDrawable(drawable); syncAnimation();
    }
    private void syncAnimation() {
        Drawable drawable = getDrawable();
        if (drawable instanceof NativeAnimatedImage) {
            NativeAnimatedImage animation = (NativeAnimatedImage) drawable;
            if (isAttachedToWindow() && isShown() && getWindowVisibility() == VISIBLE && getGlobalVisibleRect(visible)) animation.start();
            else animation.stop();
        }
    }
    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); getViewTreeObserver().addOnPreDrawListener(visibility); syncAnimation(); }
    @Override protected void onDetachedFromWindow() {
        getViewTreeObserver().removeOnPreDrawListener(visibility);
        if (getDrawable() instanceof NativeAnimatedImage) ((NativeAnimatedImage) getDrawable()).release(false);
        super.onDetachedFromWindow();
    }
    @Override protected void onWindowVisibilityChanged(int visibility) { super.onWindowVisibilityChanged(visibility); syncAnimation(); }
    @Override public void onVisibilityAggregated(boolean visible) { super.onVisibilityAggregated(visible); syncAnimation(); }
}
