package com.cloudgeek.spool.android;

import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import java.util.function.Consumer;

/** Nonmodal native floating-window shell using Web's viewport fit/move/eight-edge resize rules. */
class NativeFloatingWindow extends FrameLayout {
    static final class Bounds {
        final double x, y, width, height;
        Bounds(double x, double y, double width, double height) { this.x = x; this.y = y; this.width = width; this.height = height; }
    }
    static double clamp(double value, double minimum, double maximum) { return Math.max(minimum, Math.min(maximum, value)); }
    static Bounds fit(Bounds previous, double width, double height) {
        double gap = Math.min(12, Math.min(width / 4, height / 4)), right = width - gap, bottom = height - gap;
        double w = clamp(previous == null ? 600 : previous.width, Math.min(300, width - gap * 2), width - gap * 2);
        double h = clamp(previous == null ? 520 : previous.height, Math.min(360, height - gap * 2), height - gap * 2);
        return new Bounds(clamp(previous == null ? right - w : previous.x, gap, right - w), clamp(previous == null ? bottom - h : previous.y, gap, bottom - h), w, h);
    }
    static Bounds transform(Bounds previous, double dx, double dy, double width, double height, String edge) {
        Bounds fitted = fit(previous, width, height);
        if (edge.equals("move")) return fit(new Bounds(fitted.x + dx, fitted.y + dy, fitted.width, fitted.height), width, height);
        double gap = Math.min(12, Math.min(width / 4, height / 4)), x = fitted.x, y = fitted.y, w = fitted.width, h = fitted.height;
        double minW = Math.min(300, width - gap * 2), minH = Math.min(360, height - gap * 2);
        if (edge.contains("e")) w = clamp(w + dx, minW, width - gap - x);
        if (edge.contains("s")) h = clamp(h + dy, minH, height - gap - y);
        if (edge.contains("w")) { x = clamp(x + dx, gap, fitted.x + fitted.width - minW); w = fitted.x + fitted.width - x; }
        if (edge.contains("n")) { y = clamp(y + dy, gap, fitted.y + fitted.height - minH); h = fitted.y + fitted.height - y; }
        return new Bounds(x, y, w, h);
    }
    final LinearLayout sheet;
    final NativeUi ui;
    Bounds bounds;
    private final Consumer<Bounds> change;
    final View[] edges = new View[8];
    private final String[] names = {"n","e","s","w","ne","se","sw","nw"};

    NativeFloatingWindow(NativeUi ui, Bounds bounds, Consumer<Bounds> change) {
        super(ui.context); this.ui = ui; this.bounds = bounds; this.change = change;
        setClipChildren(false);
        sheet = new NativeSurface(ui, ui.elevated, ui.borderStrong, 18, 0, 12, 44, 0x33000000);
        sheet.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        sheet.setClickable(true); addView(sheet);
        for (int i = 0; i < names.length; i++) { View edge = new View(ui.context) { @Override public boolean performClick() { super.performClick(); return true; } }; addView(edge); edges[i] = edge; gesture(edge, names[i]); }
    }
    void gesture(View view, String operation) {
        view.setFocusable(operation.length() == 1 || operation.equals("move"));
        view.setContentDescription(ui.t(operation.equals("move") ? "Move chat window (arrow keys)" : "Resize chat window (arrow keys)"));
        view.setOnTouchListener(new OnTouchListener() {
            float x, y; Bounds initial;
            public boolean onTouch(View target, MotionEvent event) {
                switch (event.getActionMasked()) {
                    case MotionEvent.ACTION_DOWN: x = event.getRawX(); y = event.getRawY(); initial = bounds; target.getParent().requestDisallowInterceptTouchEvent(true); return true;
                    case MotionEvent.ACTION_MOVE: if (initial != null) update(transform(initial, (event.getRawX() - x) / density(), (event.getRawY() - y) / density(), getWidth() / density(), getHeight() / density(), operation)); return true;
                    case MotionEvent.ACTION_UP: target.performClick(); initial = null; return true;
                    case MotionEvent.ACTION_CANCEL: initial = null; return true;
                    default: return true;
                }
            }
        });
        view.setOnKeyListener((target, key, event) -> {
            if (event.getAction() != KeyEvent.ACTION_DOWN) return false;
            int step = event.isShiftPressed() ? 40 : 10, dx = key == KeyEvent.KEYCODE_DPAD_LEFT ? -step : key == KeyEvent.KEYCODE_DPAD_RIGHT ? step : 0;
            int dy = key == KeyEvent.KEYCODE_DPAD_UP ? -step : key == KeyEvent.KEYCODE_DPAD_DOWN ? step : 0;
            if (dx == 0 && dy == 0) return false;
            update(transform(bounds, dx, dy, getWidth() / density(), getHeight() / density(), operation)); return true;
        });
    }
    private float density() { return getResources().getDisplayMetrics().density; }
    void update(Bounds next) { bounds = next; change.accept(next); requestLayout(); }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec), height = MeasureSpec.getSize(heightSpec); setMeasuredDimension(width, height);
        bounds = fit(bounds, width / density(), height / density()); change.accept(bounds);
        sheet.measure(MeasureSpec.makeMeasureSpec(ui.dp((float) bounds.width), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(ui.dp((float) bounds.height), MeasureSpec.EXACTLY));
        for (int i = 0; i < names.length; i++) {
            String edge = names[i]; int w = edge.length() > 1 ? ui.dp(12) : edge.equals("n") || edge.equals("s") ? sheet.getMeasuredWidth() - ui.dp(26) : ui.dp(6);
            int h = edge.length() > 1 ? ui.dp(12) : edge.equals("e") || edge.equals("w") ? sheet.getMeasuredHeight() - ui.dp(26) : ui.dp(6);
            edges[i].measure(MeasureSpec.makeMeasureSpec(w, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(h, MeasureSpec.EXACTLY));
        }
    }
    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        int x = ui.dp((float) bounds.x), y = ui.dp((float) bounds.y), w = sheet.getMeasuredWidth(), h = sheet.getMeasuredHeight();
        sheet.layout(x, y, x + w, y + h);
        x += ui.dp(1); y += ui.dp(1); w -= ui.dp(2); h -= ui.dp(2);
        for (int i = 0; i < names.length; i++) { String edge = names[i];
            int left = edge.contains("w") ? x : edge.contains("e") ? x + w - edges[i].getMeasuredWidth() : x + ui.dp(12);
            int top = edge.contains("n") ? y : edge.contains("s") ? y + h - edges[i].getMeasuredHeight() : y + ui.dp(12);
            edges[i].layout(left, top, left + edges[i].getMeasuredWidth(), top + edges[i].getMeasuredHeight());
        }
    }
}
