package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.view.Gravity;
import android.view.View;
import android.view.Window;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/** Shared Web modal shell: safe viewport bounds, fixed heading/actions, scrollable body. */
class NativeDialog extends Dialog {
    final NativeUi ui;
    final LinearLayout body, actions;
    final TextView error;
    final ScrollView scroll;
    final int padding;
    final NativeModalFrame modal;
    private final NativeUi.IconButton close;
    private boolean locked;

    NativeDialog(NativeUi ui, String title, String closeLabel) {
        super(ui.context);
        this.ui = ui;
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        int width = ui.context.getResources().getDisplayMetrics().widthPixels;
        padding = width <= ui.dp(400) ? 20 : 24;
        LinearLayout shell = new LinearLayout(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                int viewport = ui.context.getResources().getDisplayMetrics().heightPixels - ui.dp(40);
                int available = MeasureSpec.getSize(heightSpec);
                int maximum = available > 0 ? Math.min(available, viewport) : viewport;
                super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(maximum, MeasureSpec.AT_MOST));
            }
        };
        shell.setOrientation(LinearLayout.VERTICAL);
        shell.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        LinearLayout heading = ui.row();
        heading.setPadding(ui.dp(padding), ui.dp(padding), ui.dp(padding), ui.dp(padding));
        heading.addView(ui.bold(title, 18), new LinearLayout.LayoutParams(0, -2, 1));
        close = ui.icon("close", 19, closeLabel, this::dismiss);
        LinearLayout.LayoutParams closeParams = new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32));
        closeParams.setMarginStart(ui.dp(12));
        heading.addView(close, closeParams);
        shell.addView(heading);
        body = ui.padded(padding, 0);
        scroll = new ScrollView(ui.context);
        scroll.setVerticalScrollBarEnabled(false);
        scroll.addView(body);
        shell.addView(scroll, new LinearLayout.LayoutParams(-1, -2, 1));
        error = ui.text("", 12, ui.error);
        error.setVisibility(View.GONE);
        error.setPadding(ui.dp(padding), ui.dp(12), ui.dp(padding), ui.dp(12));
        error.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        shell.addView(error);
        shell.addView(ui.divider());
        actions = ui.row();
        actions.setGravity(Gravity.END | Gravity.CENTER_VERTICAL);
        actions.setPadding(ui.dp(padding), ui.dp(16), ui.dp(padding), ui.dp(16));
        shell.addView(actions);
        modal = new NativeModalFrame(ui, NativeModalFrame.Profile.AVATAR, shell, () -> { if (!locked) cancel(); });
        setContentView(modal);
        NativeModalFrame.prepare(this, ui, modal.profile);
        setOnKeyListener((dialog, code, event) -> {
            if (code != android.view.KeyEvent.KEYCODE_ESCAPE) return false;
            if (event.getAction() == android.view.KeyEvent.ACTION_DOWN && !locked) cancel();
            return true;
        });
    }

    @Override public void show() {
        ui.dismissMenus();
        super.show();
        NativeModalFrame.shown(this, close);
    }

    void lock(boolean locked) {
        this.locked = locked;
        setCancelable(!locked);
        setCanceledOnTouchOutside(!locked);
        close.setEnabled(!locked);
    }
}
