package com.cloudgeek.spool.android;

import android.content.res.ColorStateList;
import android.graphics.drawable.GradientDrawable;
import android.widget.Switch;

/** Native switch for a setting whose label is laid out separately on its left. */
final class NativeSwitch extends Switch {
    NativeSwitch(NativeUi ui, String label) {
        super(ui.context);
        setText(""); setShowText(false); setSplitTrack(false);
        setContentDescription(label);
        setBackground(null);
        setSwitchMinWidth(ui.dp(40)); setSwitchPadding(0);
        setMinHeight(ui.dp(36)); setMinimumHeight(ui.dp(36));
        setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        setThumbTintList(ColorStateList.valueOf(0xffffffff));
        // The platform track has its own opacity, which washes out the tint.
        GradientDrawable track = ui.shape(ui.track, 10, 0);
        track.setSize(ui.dp(34), ui.dp(20));
        setTrackDrawable(track);
        setTrackTintList(new ColorStateList(new int[][] {
                new int[] {android.R.attr.state_checked}, new int[] {-android.R.attr.state_enabled}, new int[0]
        }, new int[] {ui.primary, ui.borderStrong, ui.track}));
    }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        setAlpha(enabled ? 1 : .45f);
    }
}
