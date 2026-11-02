package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.ColorMatrix;
import android.graphics.ColorMatrixColorFilter;
import android.graphics.Typeface;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Native AttachmentCards tile; media identity determines image layout even while loading/failing. */
final class NativeAttachmentCard extends FrameLayout {
    ImageView image;
    View open;
    final NativeUi.IconButton remove;
    boolean imageFile;
    private final NativeUi ui;
    private LinearLayout body;
    private FrameLayout placeholder;
    private TextView excerptText, filename;
    private ImageView fileIcon, symbol;
    private Runnable preview;
    private boolean initialized;
    private Boolean codeFile;

    NativeAttachmentCard(NativeUi ui, String name, String mediaType, String excerpt, Bitmap bitmap, Runnable preview, Runnable removeFile) {
        super(ui.context);
        this.ui = ui;
        setBackground(ui.shape(ui.soft, 9, ui.borderStrong));
        setClipToOutline(true);
        setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        bind(name, mediaType, excerpt, preview);
        if (image != null) image.setImageBitmap(bitmap);
        addView(body, new LayoutParams(-1, -1));
        if (removeFile == null) remove = null;
        else {
            remove = ui.icon("close", 12, ui.t("Remove {name}", "name", name), removeFile);
            remove.setPadding(ui.dp(3), ui.dp(3), ui.dp(3), ui.dp(3));
            remove.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            remove.setBackground(ui.states(ui.elevated, 99, ui.borderStrong, ui.hover, ui.elevated));
            remove.setElevation(ui.dp(2));
            LayoutParams close = new LayoutParams(ui.dp(20), ui.dp(20), Gravity.TOP | Gravity.END);
            close.topMargin = ui.dp(4);
            close.setMarginEnd(ui.dp(4));
            addView(remove, close);
        }
    }

    private void createBody() {
        body = new LinearLayout(ui.context) {
            final android.graphics.Path clip = new android.graphics.Path();
            @Override protected void dispatchDraw(android.graphics.Canvas canvas) {
                clip.reset();
                clip.addRoundRect(0, 0, getWidth(), getHeight(), ui.dp(9), ui.dp(9), android.graphics.Path.Direction.CW);
                int checkpoint = canvas.save();
                canvas.clipPath(clip);
                super.dispatchDraw(canvas);
                canvas.restoreToCount(checkpoint);
            }
        };
        body.setOrientation(LinearLayout.VERTICAL);
        body.setClipChildren(true);
        if (imageFile) {
            placeholder = null; excerptText = null; filename = null; fileIcon = null; symbol = null;
            image = new NativeImageView(ui.context) {
                @Override protected void drawableStateChanged() {
                    super.drawableStateChanged();
                    if (isHovered() || isPressed()) setColorFilter(new ColorMatrixColorFilter(new ColorMatrix(new float[] {
                        .92f,0,0,0,0, 0,.92f,0,0,0, 0,0,.92f,0,0, 0,0,0,1,0
                    })));
                    else clearColorFilter();
                }
            };
            image.setScaleType(ImageView.ScaleType.CENTER_CROP);
            image.setDuplicateParentStateEnabled(true);
            body.addView(image, new LinearLayout.LayoutParams(-1, -1));
        } else {
            image = null;
            placeholder = new FrameLayout(ui.context);
            placeholder.setBackgroundColor(ui.elevated);
            placeholder.setClipChildren(true);
            excerptText = null; fileIcon = null;
            body.addView(placeholder, new LinearLayout.LayoutParams(-1, 0, 1));
            LinearLayout label = ui.row();
            label.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6));
            symbol = ui.decorativeIcon("file-text", 14);
            codeFile = false;
            symbol.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            label.addView(symbol, new LinearLayout.LayoutParams(ui.dp(14), ui.dp(14)));
            filename = ui.text("", 10, ui.text);
            filename.setSingleLine(true);
            filename.setEllipsize(android.text.TextUtils.TruncateAt.END);
            LinearLayout.LayoutParams title = new LinearLayout.LayoutParams(0, -2, 1);
            title.setMarginStart(ui.dp(6));
            label.addView(filename, title);
            body.addView(label, new LinearLayout.LayoutParams(-1, -2));
        }
        open = body;
        body.setOnClickListener(view -> { if (view == open && this.preview != null) this.preview.run(); });
    }

    void bind(String name, String mediaType, String excerpt, Runnable preview) {
        boolean nextImage = NativeFileData.image(mediaType);
        if (!initialized || imageFile != nextImage) {
            imageFile = nextImage;
            boolean mounted = initialized;
            if (mounted) {
                if (image != null) { image.setTag(null); image.setImageDrawable(null); }
                removeView(body);
            }
            createBody();
            if (mounted) addView(body, 0, new LayoutParams(-1, -1));
            initialized = true;
        }
        this.preview = preview;
        if (image != null) image.setContentDescription(name);
        else {
            filename.setText(name);
            boolean code = NativeFileData.code(name);
            if (codeFile != code) {
                LinearLayout label = (LinearLayout) symbol.getParent(); label.removeView(symbol);
                symbol = ui.decorativeIcon(code ? "file-code" : "file-text", 14);
                symbol.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
                label.addView(symbol, 0, new LinearLayout.LayoutParams(ui.dp(14), ui.dp(14))); codeFile = code;
            }
            if (excerpt == null || excerpt.isEmpty()) {
                if (fileIcon == null) {
                    placeholder.removeAllViews(); excerptText = null;
                    fileIcon = ui.decorativeIcon("file-text", 28);
                    fileIcon.setImageTintList(android.content.res.ColorStateList.valueOf(ui.subtle));
                    placeholder.addView(fileIcon, new LayoutParams(ui.dp(28), ui.dp(28), Gravity.CENTER));
                }
            } else {
                if (excerptText == null) {
                    placeholder.removeAllViews(); fileIcon = null;
                    excerptText = ui.text("", 7, ui.text);
                    excerptText.setTypeface(Typeface.MONOSPACE);
                    ui.fixedLineHeight(excerptText, 1.3f); excerptText.setGravity(Gravity.TOP);
                    excerptText.setPadding(ui.dp(8), ui.dp(8), ui.dp(8), ui.dp(8));
                    excerptText.setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
                    placeholder.addView(excerptText, new LayoutParams(-1, -1));
                }
                if (!android.text.TextUtils.equals(excerptText.getText(), excerpt)) excerptText.setText(excerpt);
            }
        }
        body.setContentDescription(preview == null ? name : imageFile ? ui.t("Preview {name}", "name", name) : ui.t("Download {name}", "name", name));
        body.setFocusable(preview != null); body.setClickable(preview != null);
        boolean compact = getResources().getConfiguration().screenWidthDp <= 400;
        setLayoutParams(new LinearLayout.LayoutParams(ui.dp(imageFile ? compact ? 110 : 120 : compact ? 140 : 160), ui.dp(compact ? 110 : 120)));
    }

    void retire() { preview = null; if (image != null) { image.setTag(null); image.setImageDrawable(null); } }
}
