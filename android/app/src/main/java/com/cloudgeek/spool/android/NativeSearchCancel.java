package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.drawable.BitmapDrawable;
import android.graphics.drawable.StateListDrawable;
import android.widget.ImageView;

/** Chromium's original four-state UA search-cancel image, not a redrawn X. */
final class NativeSearchCancel extends ImageView {
    NativeSearchCancel(NativeUi ui) {
        super(ui.context);
        BitmapDrawable normal = original(ui, ui.dark
                ? R.drawable.web_search_cancel_dark : R.drawable.web_search_cancel_light);
        BitmapDrawable pressed = original(ui, ui.dark
                ? R.drawable.web_search_cancel_dark_pressed : R.drawable.web_search_cancel_light_pressed);
        normal.setFilterBitmap(true); pressed.setFilterBitmap(true);
        StateListDrawable states = new StateListDrawable();
        states.addState(new int[] {android.R.attr.state_pressed}, pressed);
        states.addState(new int[0], normal);
        setImageDrawable(states); setScaleType(ScaleType.FIT_XY);
        setClickable(true); setFocusable(false);
    }

    private static BitmapDrawable original(NativeUi ui, int resource) {
        BitmapFactory.Options options = new BitmapFactory.Options(); options.inScaled = false;
        Bitmap bitmap = BitmapFactory.decodeResource(ui.context.getResources(), resource, options);
        // Only the explicit CSS-size ImageView scales the original 28px bitmap.
        bitmap.setDensity(Bitmap.DENSITY_NONE);
        return new BitmapDrawable(ui.context.getResources(), bitmap);
    }

    @Override public CharSequence getAccessibilityClassName() { return android.widget.Button.class.getName(); }
}
