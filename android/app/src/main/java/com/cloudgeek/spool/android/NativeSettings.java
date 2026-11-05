package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.graphics.Color;
import android.view.Window;
import android.view.ViewTreeObserver;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.function.BiConsumer;

/** Web AppearanceDialog shell with an extension slot for Android-only tsnet settings. */
final class NativeSettings extends Dialog {
    Content content;
    private NativeUi ui;
    NativeModalFrame modal;
    private ScrollView scroll;
    private final String version;
    private final BiConsumer<String, String> change;
    private final Runnable close;

    NativeSettings(NativeUi ui, String theme, String language, String version,
            BiConsumer<String, String> change, Runnable close) {
        super(ui.context);
        this.version = version;
        this.change = change;
        this.close = close;
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        bind(ui, theme, language, body -> {});
        setCanceledOnTouchOutside(true);
        setOnCancelListener(dialog -> close.run());
        setOnKeyListener((dialog, code, event) -> {
            if (code != android.view.KeyEvent.KEYCODE_ESCAPE) return false;
            if (event.getAction() == android.view.KeyEvent.ACTION_DOWN) cancel();
            return true;
        });
    }

    /** Rebuild themed content without removing/re-adding the dialog's window. */
    void bind(NativeUi ui, String theme, String language, java.util.function.Consumer<LinearLayout> additions) {
        this.ui = ui;
        int top = scroll == null ? 0 : scroll.getScrollY();
        content = new Content(ui, theme, language, version, change, close);
        content.setBackgroundColor(Color.TRANSPARENT);
        additions.accept(content.additions);
        scroll = new ScrollView(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                int available = MeasureSpec.getSize(heightSpec);
                int maximum = getResources().getDisplayMetrics().heightPixels - ui.dp(40);
                super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(available > 0 ? Math.min(available, maximum) : maximum, MeasureSpec.AT_MOST));
            }
        };
        scroll.setVerticalScrollBarEnabled(false);
        scroll.addView(content);
        modal = new NativeModalFrame(ui, NativeModalFrame.Profile.APPEARANCE, scroll, this::cancel);
        setContentView(modal);
        NativeModalFrame.prepare(this, ui, modal.profile);
        ScrollView current = scroll;
        if (top > 0) current.getViewTreeObserver().addOnPreDrawListener(new ViewTreeObserver.OnPreDrawListener() {
            @Override public boolean onPreDraw() {
                current.getViewTreeObserver().removeOnPreDrawListener(this);
                current.scrollTo(0, top);
                return true;
            }
        });
    }

    @Override public void show() {
        ui.dismissMenus();
        super.show();
        NativeModalFrame.shown(this, content.close);
    }

    static final class Content extends LinearLayout {
        final LinearLayout heading, additions;
        final NativeAppearanceSection appearance;
        final NativeUi.IconButton close;
        final TextView version;
        Content(NativeUi ui, String theme, String language, String currentVersion,
                BiConsumer<String, String> change, Runnable dismiss) {
            super(ui.context);
            setOrientation(VERTICAL);
            setPadding(ui.dp(25), ui.dp(25), ui.dp(25), ui.dp(25));
            setBackground(ui.shape(ui.elevated, 18, ui.borderStrong));
            heading = ui.row();
            TextView title = ui.bold(ui.t("Appearance and language"), 18);
            ui.fixedLineHeight(title, 7f / 6);
            heading.addView(title, new LayoutParams(0, -2, 1));
            close = ui.icon("close", 19, ui.t("Close settings"), dismiss);
            LayoutParams icon = new LayoutParams(ui.dp(32), ui.dp(32));
            icon.setMarginStart(ui.dp(12));
            heading.addView(close, icon);
            addView(heading);
            addView(ui.space(24));
            appearance = new NativeAppearanceSection(ui, theme, language, change);
            addView(appearance, new LayoutParams(-1, -2));
            additions = ui.column();
            addView(additions, new LayoutParams(-1, -2));
            version = ui.text(ui.t("Current version: {version}", "version", currentVersion), 11, ui.muted);
            ui.fixedLineHeight(version, 1.8f);
            LayoutParams versionParams = new LayoutParams(-1, -2);
            versionParams.topMargin = ui.dp(22);
            addView(version, versionParams);
        }
    }
}
