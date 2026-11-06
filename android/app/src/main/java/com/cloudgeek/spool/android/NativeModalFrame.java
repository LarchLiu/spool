package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.graphics.Rect;
import android.graphics.RectF;
import android.graphics.drawable.ColorDrawable;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;
import android.widget.FrameLayout;
import android.widget.LinearLayout;

/** Centered CSS dialog border box with independent shadow room and current-window size limits. */
final class NativeModalFrame extends FrameLayout {
    enum Profile {
        APPEARANCE(430, 38, 40, 1, 1, 18, 20, 100, 0x44000000, 7f / 15),
        AVATAR(460, 38, 40, 1, 1, 18, 20, 100, 0x44000000, 7f / 15),
        GROUP(460, 32, 32, 1, 1, 18, 20, 100, 0x44000000, 7f / 15),
        IMAGE(1080, 0, 0, .94f, .92f, 14, 0, 0, 0, .72f);
        final int width, reserveX, reserveY, radius, offset, blur, shadow;
        final float widthRatio, heightRatio, dim;
        Profile(int width, int reserveX, int reserveY, float widthRatio, float heightRatio,
                int radius, int offset, int blur, int shadow, float dim) {
            this.width = width; this.reserveX = reserveX; this.reserveY = reserveY;
            this.widthRatio = widthRatio; this.heightRatio = heightRatio;
            this.radius = radius; this.offset = offset; this.blur = blur; this.shadow = shadow; this.dim = dim;
        }
        NativeSurface surface(NativeUi ui) { return new NativeSurface(ui, ui.elevated, ui.borderStrong, radius, 0, offset, blur, shadow); }
    }

    final NativeSurface surface;
    final RectF panel = new RectF();
    final Profile profile;
    private final NativeUi ui;
    Runnable outside;
    private boolean outsideGesture;
    private float borderWidth;

    NativeModalFrame(NativeUi ui, Profile profile, View body, Runnable outside) {
        super(ui.context);
        this.ui = ui; this.profile = profile; this.outside = outside;
        surface = body instanceof NativeSurface ? (NativeSurface) body : profile.surface(ui);
        if (surface != body) surface.addView(body, new LinearLayout.LayoutParams(-1, -2));
        setClipChildren(false); setClipToPadding(false);
        addView(surface, new FrameLayout.LayoutParams(-2, -2));
    }

    static void prepare(Dialog dialog, NativeUi ui, Profile profile) {
        Window window = dialog.getWindow();
        if (window == null) return;
        window.setBackgroundDrawable(new ColorDrawable(0));
        window.setGravity(Gravity.CENTER);
        window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        WindowManager.LayoutParams attributes = window.getAttributes();
        attributes.flags |= WindowManager.LayoutParams.FLAG_DIM_BEHIND;
        attributes.dimAmount = profile.dim; attributes.windowAnimations = 0;
        if (android.os.Build.VERSION.SDK_INT >= 31 && profile == Profile.IMAGE) {
            attributes.flags |= WindowManager.LayoutParams.FLAG_BLUR_BEHIND;
            attributes.setBlurBehindRadius(ui.dp(6));
        }
        window.setAttributes(attributes);
    }

    static void shown(Dialog dialog, View focus) {
        Window window = dialog.getWindow();
        if (window == null) return;
        window.getDecorView().setPadding(0, 0, 0, 0);
        window.getDecorView().setElevation(0);
        window.setLayout(WindowManager.LayoutParams.MATCH_PARENT, WindowManager.LayoutParams.WRAP_CONTENT);
        if (focus != null) {
            focus.setFocusableInTouchMode(true);
            focus.requestFocus();
        }
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec), available = MeasureSpec.getSize(heightSpec);
        if (width <= 0) width = getResources().getDisplayMetrics().widthPixels;
        if (available <= 0) available = getResources().getDisplayMetrics().heightPixels;
        float density = getResources().getDisplayMetrics().density;
        borderWidth = Math.max(1, Math.min(profile.width * density, Math.min(width - profile.reserveX * density, width * profile.widthRatio)));
        int panelWidth = Math.round(borderWidth);
        int maximumHeight = Math.max(1, Math.min(available - ui.dp(profile.reserveY), Math.round(available * profile.heightRatio)));
        surface.measure(MeasureSpec.makeMeasureSpec(panelWidth, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(maximumHeight, MeasureSpec.AT_MOST));
        Rect shadow = surface.shadowOutsets();
        // Symmetric room keeps the border box centered even when its shadow is offset downward.
        int room = Math.max(shadow.top, shadow.bottom);
        int desired = Math.min(available, surface.getMeasuredHeight() + 2 * room);
        setMeasuredDimension(resolveSize(width, widthSpec), resolveSize(desired, heightSpec));
    }

    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        float x = (getWidth() - borderWidth) / 2f, y = (getHeight() - surface.getMeasuredHeight()) / 2f;
        panel.set(x, y, x + borderWidth, y + surface.getMeasuredHeight());
        int left = (int) x, top = (int) y;
        surface.layout(left, top, left + surface.getMeasuredWidth(), top + surface.getMeasuredHeight());
        // Keep CSS geometry fractional without scaling native children or their glyphs.
        surface.setCssBorderBox(borderWidth, surface.getMeasuredHeight(), x, y);
        surface.setTranslationX(x - left); surface.setTranslationY(y - top);
    }

    private boolean inPanel(float x, float y) { return x >= panel.left && x <= panel.right && y >= panel.top && y <= panel.bottom; }

    @Override public boolean onInterceptTouchEvent(MotionEvent event) {
        if (event.getActionMasked() == MotionEvent.ACTION_DOWN) outsideGesture = !inPanel(event.getX(), event.getY());
        return outsideGesture || super.onInterceptTouchEvent(event);
    }

    @Override public boolean onTouchEvent(MotionEvent event) {
        if (!outsideGesture) return super.onTouchEvent(event);
        if (event.getActionMasked() == MotionEvent.ACTION_UP) {
            outsideGesture = false;
            if (!inPanel(event.getX(), event.getY())) performClick();
        } else if (event.getActionMasked() == MotionEvent.ACTION_CANCEL) outsideGesture = false;
        return true;
    }

    @Override public boolean performClick() {
        boolean handled = super.performClick();
        if (outside == null) return handled;
        outside.run();
        return true;
    }
}
