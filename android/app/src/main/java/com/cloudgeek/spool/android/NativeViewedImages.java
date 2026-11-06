package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.TextView;

/** Left-aligned, wrapping native thumbnails for a single adjacent image activity group. */
final class NativeViewedImages extends ViewGroup {
    private final NativeUi ui;
    private final NativeTranscriptImages images;
    private Object bound;

    NativeViewedImages(NativeUi ui, NativeTranscriptImages images) { super(ui.context); this.ui = ui; this.images = images; }

    void bind(NativeActivityModel.Group group) {
        org.json.JSONArray identity = new org.json.JSONArray();
        for (NativeActivityModel.Activity item : group.activities) identity.put(new org.json.JSONArray().put(new org.json.JSONArray(item.images)).put(new org.json.JSONArray(item.imageURLs)));
        if (NativeChatState.equivalent(bound, identity)) return;
        bound = NativeChatState.snapshot(identity);
        retire(); removeAllViews();
        for (NativeActivityModel.Activity item : group.activities) {
            int count = item.imageURLs.isEmpty() ? item.images.size() : item.imageURLs.size();
            for (int i = 0; i < count; i++) {
                String path = i < item.images.size() ? item.images.get(i) : "";
                String source = i < item.imageURLs.size() ? item.imageURLs.get(i) : "";
                String name = path.isEmpty() ? "image" : path.replace('\\', '/').substring(path.replace('\\', '/').lastIndexOf('/') + 1);
                FrameLayout tile = new FrameLayout(ui.context);
                tile.setBackground(ui.shape(ui.soft, 6, ui.borderStrong)); tile.setClipToOutline(true);
                tile.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
                ImageView image = new NativeImageView(ui.context); image.setScaleType(ImageView.ScaleType.FIT_CENTER);
                image.setContentDescription(name); tile.addView(image, new FrameLayout.LayoutParams(-1, -1));
                TextView fallback = ui.text(name, 10, ui.subtle); fallback.setGravity(android.view.Gravity.CENTER);
                fallback.setMaxLines(3); fallback.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
                fallback.setContentDescription(ui.t("Image unavailable: {name}", "name", name));
                fallback.setVisibility(GONE); tile.addView(fallback, new FrameLayout.LayoutParams(-1, -1));
                tile.setFocusable(true); tile.setContentDescription(ui.t("Preview {name}", "name", name));
                ui.buttonKeys(tile);
                tile.setOnClickListener(v -> { if (images != null && isAttachedToWindow()) images.preview(path, source, name); });
                addView(tile, new LayoutParams(ui.dp(80), ui.dp(80)));
                if (images == null) fallback.setVisibility(VISIBLE);
                else images.load(image, path, source, ui.dp(160), error -> {
                    image.setVisibility(error == null ? VISIBLE : GONE); fallback.setVisibility(error == null ? GONE : VISIBLE);
                });
            }
        }
    }

    void retire() {
        for (int i = 0; i < getChildCount(); i++) {
            FrameLayout tile = (FrameLayout) getChildAt(i);
            ImageView image = (ImageView) tile.getChildAt(0); image.setTag(null); image.setImageDrawable(null);
        }
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int available = Math.max(1, MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED ? ui.dp(680) : MeasureSpec.getSize(widthSpec));
        int size = Math.min(ui.dp(80), available), gap = ui.dp(8), columns = Math.max(1, (available + gap) / (size + gap));
        for (int i = 0; i < getChildCount(); i++) getChildAt(i).measure(MeasureSpec.makeMeasureSpec(size, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(size, MeasureSpec.EXACTLY));
        int rows = (getChildCount() + columns - 1) / columns;
        setMeasuredDimension(resolveSize(Math.max(0, Math.min(columns, getChildCount()) * (size + gap) - gap), widthSpec), resolveSize(rows == 0 ? 0 : rows * (size + gap) - gap, heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int x = 0, y = 0;
        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i); int size = child.getMeasuredWidth();
            if (x > 0 && x + size > getWidth()) { x = 0; y += size + ui.dp(8); }
            child.layout(x, y, x + size, y + size); x += size + ui.dp(8);
        }
    }
}
