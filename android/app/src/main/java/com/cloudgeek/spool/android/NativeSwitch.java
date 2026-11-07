package com.cloudgeek.spool.android;

import android.content.res.ColorStateList;
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
        setTrackTintList(new ColorStateList(new int[][] {
                new int[] {-android.R.attr.state_enabled}, new int[] {android.R.attr.state_checked}, new int[0]
        }, new int[] {ui.borderStrong, ui.primary, ui.track}));
    }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        setAlpha(enabled ? 1 : .45f);
    }
}
