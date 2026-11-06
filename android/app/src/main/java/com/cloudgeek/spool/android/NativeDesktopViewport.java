package com.cloudgeek.spool.android;

/** Same fit-relative zoom and moving pinch midpoint equations as web/src/voxl-zoom.ts. */
final class NativeDesktopViewport {
    double zoom = 1, x, y, width, height, desktopWidth = 1, desktopHeight = 1;
    double fit() { return Math.min(width / desktopWidth, height / desktopHeight); }
    double scale() { return fit() * zoom; }
    void resize(double w, double h, double dw, double dh) {
        double before = fit();
        width = w; height = h; desktopWidth = dw; desktopHeight = dh;
        if (before > 0) { x *= fit() / before; y *= fit() / before; }
        constrain();
    }
    void zoomAt(double value, double fromX, double fromY, double toX, double toY) {
        double next = Math.max(1, Math.min(5, value)), ratio = next / zoom;
        x = toX - width / 2 - (fromX - width / 2 - x) * ratio;
        y = toY - height / 2 - (fromY - height / 2 - y) * ratio;
        zoom = next; constrain();
    }
    void pan(double dx, double dy) { x += dx; y += dy; constrain(); }
    void reset() { zoom = 1; x = y = 0; }
    double remoteX(double local) { return Math.max(0, Math.min(desktopWidth - 1, (local - width / 2 - x) / scale() + desktopWidth / 2)); }
    double remoteY(double local) { return Math.max(0, Math.min(desktopHeight - 1, (local - height / 2 - y) / scale() + desktopHeight / 2)); }
    boolean contains(double px, double py) {
        return Math.abs(px - width / 2 - x) <= desktopWidth * scale() / 2
                && Math.abs(py - height / 2 - y) <= desktopHeight * scale() / 2;
    }
    private void constrain() {
        double limitX = Math.max(0, (desktopWidth * scale() - width) / 2);
        double limitY = Math.max(0, (desktopHeight * scale() - height) / 2);
        x = Math.max(-limitX, Math.min(limitX, x)); y = Math.max(-limitY, Math.min(limitY, y));
    }
}
