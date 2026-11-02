package com.cloudgeek.spool.android;

import android.app.Activity;
import android.os.Build;
import android.view.View;
import android.view.WindowInsets;
import android.widget.FrameLayout;

/** Shared production window shell: content remains clear of system bars and the live IME. */
final class NativeWindow {
    static FrameLayout content(Activity activity, NativeUi ui) {
        activity.getWindow().setStatusBarColor(ui.surface);
        activity.getWindow().setNavigationBarColor(ui.surface);
        activity.getWindow().getDecorView().setSystemUiVisibility(ui.dark ? 0
                : View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR);
        if (Build.VERSION.SDK_INT >= 30) activity.getWindow().setDecorFitsSystemWindows(false);
        FrameLayout root = new FrameLayout(activity);
        root.setBackgroundColor(ui.surface);
        root.setOnApplyWindowInsetsListener((view, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.ime() | WindowInsets.Type.displayCutout());
                view.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            } else {
                int left = insets.getSystemWindowInsetLeft(), top = insets.getSystemWindowInsetTop();
                int right = insets.getSystemWindowInsetRight(), bottom = insets.getSystemWindowInsetBottom();
                if (Build.VERSION.SDK_INT >= 28 && insets.getDisplayCutout() != null) {
                    android.view.DisplayCutout cutout = insets.getDisplayCutout();
                    left = Math.max(left, cutout.getSafeInsetLeft()); top = Math.max(top, cutout.getSafeInsetTop());
                    right = Math.max(right, cutout.getSafeInsetRight()); bottom = Math.max(bottom, cutout.getSafeInsetBottom());
                }
                view.setPadding(left, top, right, bottom);
            }
            return insets;
        });
        return root;
    }
}
