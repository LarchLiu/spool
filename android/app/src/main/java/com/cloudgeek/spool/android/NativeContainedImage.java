package com.cloudgeek.spool.android;

/** Web intrinsic image sizing: contain within available width, never enlarge a small raster. */
final class NativeContainedImage extends NativeImageView {
    private final NativeUi ui;
    private final boolean modal;
    NativeContainedImage(NativeUi ui, boolean modal) {
        super(ui.context);
        this.ui = ui; this.modal = modal;
        setScaleType(ScaleType.FIT_CENTER);
    }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int maximum = modal ? Math.max(1, Math.round(getResources().getDisplayMetrics().heightPixels * .92f) - ui.dp(58)) : Integer.MAX_VALUE;
        if (MeasureSpec.getMode(heightSpec) != MeasureSpec.UNSPECIFIED)
            maximum = Math.min(maximum, MeasureSpec.getSize(heightSpec));
        int width = getDrawable() == null ? 0 : getDrawable().getIntrinsicWidth();
        int height = getDrawable() == null ? 0 : getDrawable().getIntrinsicHeight();
        if (width <= 0 || height <= 0) { setMeasuredDimension(0, 0); return; }
        int available = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED ? width : MeasureSpec.getSize(widthSpec);
        float scale = Math.min(1, Math.min((float) available / width, (float) maximum / height));
        setMeasuredDimension(Math.round(width * scale), Math.round(height * scale));
    }
}
