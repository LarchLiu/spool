package com.cloudgeek.spool.android;

/** CSS-pixel counterpart of Web selectPopupLayout, independent of Android window state. */
final class NativeSelectLayout {
    final float left, top, width, maximum;
    final boolean above;

    NativeSelectLayout(float left, float top, float right, float bottom, float anchorWidth,
            float viewX, float viewY, float viewWidth, float viewHeight, int count) {
        this(left, top, right, bottom, anchorWidth, viewX, viewY, viewWidth, viewHeight, count, 180);
    }

    NativeSelectLayout(float left, float top, float right, float bottom, float anchorWidth,
            float viewX, float viewY, float viewWidth, float viewHeight, int count, float minimumWidth) {
        this(left, top, right, bottom, anchorWidth, viewX, viewY, viewWidth, viewHeight, count, minimumWidth, 36);
    }

    NativeSelectLayout(float left, float top, float right, float bottom, float anchorWidth,
            float viewX, float viewY, float viewWidth, float viewHeight, int count, float minimumWidth, int rowHeight) {
        this(left, top, right, bottom, anchorWidth, viewX, viewY, viewWidth, viewHeight, count, minimumWidth, rowHeight, 0);
    }

    NativeSelectLayout(float left, float top, float right, float bottom, float anchorWidth,
            float viewX, float viewY, float viewWidth, float viewHeight, int count, float minimumWidth, int rowHeight, float preferredHeight) {
        width = Math.min(Math.max(minimumWidth, anchorWidth), Math.max(0, viewWidth - 16));
        float below = Math.max(0, viewY + viewHeight - 8 - bottom - 4);
        float before = Math.max(0, top - 4 - viewY - 8);
        float desired = preferredHeight > 0 ? preferredHeight : Math.min(280, count * rowHeight + 10);
        above = !(below >= Math.min(desired, 180) || below >= before);
        this.left = Math.max(viewX + 8, Math.min(left, viewX + viewWidth - 8 - width));
        this.top = above ? top - 4 : bottom + 4;
        maximum = Math.min(desired, above ? before : below);
    }
}
