package com.cloudgeek.spool.android;

import android.graphics.Rect;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;

/** Window-sized shadow room around an independently positioned CSS menu border box. */
final class NativePopupFrame extends FrameLayout {
    NativeSurface surface;
    Runnable escape;
    final Rect panel = new Rect(), window = new Rect();
    private final NativeUi ui;
    private final View body;
    private float radius;

    NativePopupFrame(NativeUi ui, View body, int radius, int border, float shadowY, float blur, int shadowColor) {
        super(ui.context);
        this.ui = ui; this.body = body;
        setClipChildren(false); setClipToPadding(false);
        restyle(radius, border, shadowY, blur, shadowColor);
    }

    void restyle(int radius, int border, float shadowY, float blur, int shadowColor) {
        if (surface != null) { surface.removeView(body); removeView(surface); }
        this.radius = radius * getResources().getDisplayMetrics().density;
        surface = new NativeSurface(ui, ui.elevated, border, radius, 0, shadowY, blur, shadowColor);
        surface.addView(body, new LinearLayout.LayoutParams(-1, -1));
        addView(surface);
    }

    Rect place(Rect viewport, int x, int y, int width, int height) {
        if (viewport.isEmpty() || width <= 0 || height <= 0) throw new IllegalArgumentException("Empty popup bounds");
        width = Math.min(width, viewport.width()); height = Math.min(height, viewport.height());
        x = Math.max(viewport.left, Math.min(x, viewport.right - width));
        y = Math.max(viewport.top, Math.min(y, viewport.bottom - height));
        Rect halo = surface.shadowOutsets();
        window.set(Math.max(viewport.left, x - halo.left), Math.max(viewport.top, y - halo.top),
                Math.min(viewport.right, x + width + halo.right), Math.min(viewport.bottom, y + height + halo.bottom));
        panel.set(x - window.left, y - window.top, x + width - window.left, y + height - window.top);
        LayoutParams previous = (LayoutParams) surface.getLayoutParams();
        if (previous.width != width || previous.height != height || previous.leftMargin != panel.left || previous.topMargin != panel.top) {
            LayoutParams next = new LayoutParams(width, height);
            next.leftMargin = panel.left; next.topMargin = panel.top;
            surface.setLayoutParams(next);
        }
        return window;
    }

    boolean hitsPanel(float x, float y) {
        if (x < panel.left || x >= panel.right || y < panel.top || y >= panel.bottom) return false;
        float r = Math.min(radius, Math.min(panel.width(), panel.height()) / 2f);
        float centerX = Math.max(panel.left + r, Math.min(x, panel.right - r));
        float centerY = Math.max(panel.top + r, Math.min(y, panel.bottom - r));
        return (x - centerX) * (x - centerX) + (y - centerY) * (y - centerY) <= r * r;
    }

    @Override public boolean dispatchKeyEvent(android.view.KeyEvent event) {
        if (event.getKeyCode() == android.view.KeyEvent.KEYCODE_ESCAPE && escape != null) {
            if (event.getAction() == android.view.KeyEvent.ACTION_DOWN) escape.run();
            return true;
        }
        return super.dispatchKeyEvent(event);
    }
}
