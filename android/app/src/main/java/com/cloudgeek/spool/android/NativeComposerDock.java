package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.FrameLayout;
import android.widget.ImageView;

/** Absolute composer-relative action: it consumes neither history nor footer layout space. */
final class NativeComposerDock extends FrameLayout {
    final NativeSurface latest;
    final ImageView arrow;
    private final View footer;
    private final float density;

    NativeComposerDock(NativeUi ui, View page, View footer, Runnable click) {
        super(ui.context);
        this.footer = footer;
        density = getResources().getDisplayMetrics().density;
        setClipChildren(false); setClipToPadding(false);
        addView(page, new LayoutParams(-1, -1));
        latest = new NativeSurface(ui, ui.elevated, ui.line, 999, 0, 3, 10) {
            @Override public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(info); info.setClassName("android.widget.Button");
            }
        };
        latest.setGravity(Gravity.CENTER);
        latest.setContentDescription(ui.t("Scroll to latest"));
        latest.setFocusable(true); ui.buttonKeys(latest);
        latest.setOnClickListener(view -> click.run());
        arrow = new ImageView(ui.context);
        arrow.setImageDrawable(NativeIcons.drawable(ui.context, "arrow-down", ui.text, ui.dp(18)));
        arrow.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        latest.addView(arrow, new android.widget.LinearLayout.LayoutParams(ui.dp(18), ui.dp(18)));
        addView(latest, new LayoutParams(ui.dp(35), ui.dp(35)));
        latest.setVisibility(GONE);
    }

    void following(boolean value) {
        int visibility = value ? GONE : VISIBLE;
        if (latest.getVisibility() != visibility) latest.setVisibility(visibility);
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        int footerTop = footer.getTop();
        for (android.view.ViewParent parent = footer.getParent(); parent instanceof View && parent != this; parent = parent.getParent())
            footerTop += ((View) parent).getTop();
        int x = Math.round((getWidth() - 35 * density) / 2f), y = footerTop - Math.round(45 * density);
        latest.layout(x, y, Math.round((getWidth() + 35 * density) / 2f), footerTop - Math.round(10 * density));
    }
}
