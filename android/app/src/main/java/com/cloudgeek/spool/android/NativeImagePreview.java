package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.view.Gravity;
import android.view.View;
import android.view.Window;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.lang.ref.WeakReference;
import java.util.function.Consumer;

/** Native ImagePreview: Web toolbar, contained intrinsic image, bounded viewport and modal backdrop. */
final class NativeImagePreview extends Dialog {
    final Content content;
    private final NativeUi ui;
    final NativeModalFrame modal;

    NativeImagePreview(NativeUi ui, String name, Runnable download) {
        super(ui.context);
        this.ui = ui;
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        content = new Content(ui, name, download, this::dismiss);
        modal = new NativeModalFrame(ui, NativeModalFrame.Profile.IMAGE, content, this::cancel);
        setContentView(modal);
        setCanceledOnTouchOutside(true);
        NativeModalFrame.prepare(this, ui, modal.profile);
        setOnKeyListener((dialog, code, event) -> { if (code != android.view.KeyEvent.KEYCODE_ESCAPE) return false; if (event.getAction() == android.view.KeyEvent.ACTION_DOWN) cancel(); return true; });
    }

    @Override public void show() {
        ui.dismissMenus();
        super.show();
        NativeModalFrame.shown(this, content.close);
    }

    Consumer<Exception> completion() {
        WeakReference<NativeImagePreview> owner = new WeakReference<>(this);
        return error -> {
            NativeImagePreview current = owner.get();
            if (current != null && current.isShowing()) current.content.loaded(error);
        };
    }

    static final class Content extends NativeSurface {
        final ImageView image;
        final NativeUi.IconButton close, download;
        final TextView status;
        final LinearLayout toolbar;
        private final NativeUi ui;
        Content(NativeUi ui, String name, Runnable save, Runnable dismiss) {
            super(ui, ui.elevated, ui.borderStrong, 14, 0, 0, 0, 0);
            this.ui = ui;
            setOrientation(VERTICAL);
            setGravity(Gravity.CENTER_HORIZONTAL);
            setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
            setContentDescription(ui.t("Preview {name}", "name", name));
            setOnClickListener(view -> dismiss.run());
            toolbar = ui.row();
            toolbar.setPadding(ui.dp(12), ui.dp(9), ui.dp(12), ui.dp(9));
            toolbar.setOnClickListener(view -> {});
            TextView title = ui.text(name, 12, ui.text);
            title.setSingleLine(true);
            title.setEllipsize(android.text.TextUtils.TruncateAt.END);
            toolbar.addView(title, new LayoutParams(0, -2, 1));
            download = ui.icon("download", 18, ui.t("Download {name}", "name", name), () -> { if (save != null) save.run(); });
            download.setEnabled(save != null);
            download.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            LayoutParams downloadParams = new LayoutParams(ui.dp(32), ui.dp(32));
            downloadParams.setMarginStart(ui.dp(7));
            toolbar.addView(download, downloadParams);
            close = ui.icon("close", 20, ui.t("Close preview"), dismiss);
            LayoutParams closeParams = new LayoutParams(ui.dp(32), ui.dp(32));
            closeParams.setMarginStart(ui.dp(7));
            toolbar.addView(close, closeParams);
            addView(toolbar, new LayoutParams(-1, -2));
            status = ui.text(ui.t("Loading file…"), 12, ui.muted);
            status.setPadding(ui.dp(12), ui.dp(12), ui.dp(12), ui.dp(16));
            status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
            addView(status);
            image = new NativeContainedImage(ui, true);
            image.setContentDescription(name);
            image.setOnClickListener(view -> {});
            image.setVisibility(GONE);
            addView(image, new LayoutParams(-2, -2));
        }

        void loaded(Exception error) {
            boolean success = error == null && image.getDrawable() != null;
            image.setVisibility(success ? VISIBLE : GONE);
            status.setVisibility(success ? GONE : VISIBLE);
            if (!success) { status.setText(ui.t("This file cannot be previewed as an image")); status.setTextColor(ui.error); }
            requestLayout();
        }

    }
}
