package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.graphics.Typeface;
import android.view.Gravity;
import android.view.View;
import android.view.Window;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/** Large offline diagram viewport with fit, zoom, drag and pinch controls. */
final class NativeMermaidPreview extends Dialog {
    final NativeMermaid diagram;
    final TextView source, status;
    final ScrollView fallback;
    final NativeUi.IconButton zoomOut, fit, zoomIn, close;
    private final NativeUi ui;
    private NativeSyntax.Binding syntax;
    private String current;

    NativeMermaidPreview(NativeUi ui) {
        super(ui.context); this.ui = ui;
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        NativeSurface content = new NativeSurface(ui, ui.input, ui.borderStrong, 14, 0, 0, 0, 0) {
            @Override protected void onMeasure(int width, int height) {
                int desired = Math.round(ui.context.getResources().getDisplayMetrics().heightPixels * .92f);
                int available = MeasureSpec.getSize(height);
                super.onMeasure(width, MeasureSpec.makeMeasureSpec(available > 0 ? Math.min(desired, available) : desired, MeasureSpec.EXACTLY));
            }
        };
        content.setOrientation(LinearLayout.VERTICAL);
        content.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        content.setContentDescription(ui.t("Enlarge diagram"));
        LinearLayout toolbar = ui.row(); toolbar.setPadding(ui.dp(12), ui.dp(9), ui.dp(12), ui.dp(9));
        toolbar.addView(ui.text("Mermaid", 12, ui.text), new LinearLayout.LayoutParams(0, -2, 1));
        FrameLayout viewport = new FrameLayout(ui.context);
        source = ui.text("", 12, ui.text); source.setTypeface(Typeface.MONOSPACE);
        source.setTextIsSelectable(true); source.setPadding(ui.dp(16), ui.dp(12), ui.dp(16), ui.dp(16));
        HorizontalScrollView horizontal = new HorizontalScrollView(ui.context);
        horizontal.addView(source, new FrameLayout.LayoutParams(-2, -2));
        fallback = new ScrollView(ui.context); fallback.addView(horizontal); fallback.setVisibility(View.GONE);
        status = ui.text(ui.t("Rendering diagram…"), 13, ui.muted); status.setGravity(Gravity.CENTER);
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        diagram = new NativeMermaid(ui, true, (state, height) -> display(state));
        viewport.addView(diagram, new FrameLayout.LayoutParams(-1, -1));
        viewport.addView(fallback, new FrameLayout.LayoutParams(-1, -1));
        viewport.addView(status, new FrameLayout.LayoutParams(-1, -1));
        zoomOut = ui.icon("minus", 18, ui.t("Zoom out"), () -> diagram.zoom("out"));
        fit = ui.icon("fit", 18, ui.t("Fit diagram"), () -> diagram.zoom("fit"));
        zoomIn = ui.icon("plus", 18, ui.t("Zoom in"), () -> diagram.zoom("in"));
        close = ui.icon("close", 20, ui.t("Close preview"), this::dismiss);
        for (NativeUi.IconButton button : new NativeUi.IconButton[] {zoomOut, fit, zoomIn, close}) {
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32)); params.setMarginStart(ui.dp(7));
            toolbar.addView(button, params);
        }
        content.addView(toolbar, new LinearLayout.LayoutParams(-1, -2));
        content.addView(viewport, new LinearLayout.LayoutParams(-1, 0, 1));
        NativeModalFrame modal = new NativeModalFrame(ui, NativeModalFrame.Profile.IMAGE, content, this::cancel);
        setContentView(modal); setCanceledOnTouchOutside(true);
        NativeModalFrame.prepare(this, ui, modal.profile);
        setOnKeyListener((dialog, code, event) -> {
            if (code != android.view.KeyEvent.KEYCODE_ESCAPE) return false;
            if (event.getAction() == android.view.KeyEvent.ACTION_DOWN) cancel();
            return true;
        });
        display(NativeMermaid.State.LOADING);
    }

    private void display(NativeMermaid.State state) {
        fallback.setVisibility(state == NativeMermaid.State.ERROR ? View.VISIBLE : View.GONE);
        status.setVisibility(state == NativeMermaid.State.LOADING ? View.VISIBLE : View.GONE);
        for (NativeUi.IconButton button : new NativeUi.IconButton[] {zoomOut, fit, zoomIn})
            button.setEnabled(state == NativeMermaid.State.READY);
    }

    void bind(String text) {
        if (text.equals(current)) return;
        current = text;
        if (syntax != null) syntax.close();
        NativeCodeTabs.setText(source, text, source.getTextSize());
        if (ui.syntax != null) syntax = ui.syntax.bindCode(source, "mermaid", text, ui.dark);
        diagram.bind(text);
    }

    @Override public void dismiss() {
        if (syntax != null) { syntax.close(); syntax = null; }
        super.dismiss();
    }

    @Override public void show() {
        ui.dismissMenus(); super.show(); NativeModalFrame.shown(this, close);
    }
}
