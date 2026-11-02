package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.os.Build;
import android.view.Gravity;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** Native PermissionPicker body; discovery and transport stay with its owner. */
final class NativePermissionMenu extends FrameLayout implements NativeMarkdownFlowBox {
    final NativePopover popover;
    final NativeMarkdownFlow flow;
    private final NativeUi ui;
    private final String backend;
    private final Consumer<String> choose;
    private final Runnable retry;
    private JSONObject catalog;
    private String selected;
    private boolean loading, failed, confirmBypass;

    NativePermissionMenu(NativeUi ui, String backend, String selected, JSONObject catalog,
            boolean loading, boolean failed, Consumer<String> choose, Runnable retry) {
        super(ui.context);
        this.ui = ui; this.backend = backend; this.selected = selected; this.catalog = catalog;
        this.loading = loading; this.failed = failed; this.choose = choose; this.retry = retry;
        popover = NativePopover.permission(ui);
        flow = new NativeMarkdownFlow(ui);
        flow.collapseTop = flow.collapseBottom = false;
        addView(flow, new FrameLayout.LayoutParams(-1, -2));
        popover.content.addView(this, new LinearLayout.LayoutParams(-1, -2));
        popover.dismissed = () -> { confirmBypass = false; };
        setContentDescription(ui.t("Permissions"));
        render();
    }

    static String label(NativeUi ui, String backend, String id) {
        switch (id) {
            case "ask": return ui.t("Ask for approval");
            case "manual": return ui.t("Manual");
            case "auto": return ui.t(backend.equals("codex") ? "Approve for me" : "Auto");
            case "acceptEdits": return ui.t("Accept edits");
            case "plan": return ui.t("Plan");
            case "dontAsk": return ui.t("Don't ask");
            case "bypassPermissions": return ui.t("Bypass permissions");
            default: return ui.t("Permissions");
        }
    }

    static String description(NativeUi ui, String id) {
        switch (id) {
            case "ask": return ui.t("You approve requests outside the sandbox.");
            case "manual": return ui.t("Confirm operations that require permission.");
            case "auto": return ui.t("The agent reviews approval requests automatically.");
            case "acceptEdits": return ui.t("Allow file edits; ask for other protected operations.");
            case "plan": return ui.t("Plan before making changes.");
            case "dontAsk": return ui.t("Deny operations that are not already permitted.");
            case "bypassPermissions": return ui.t("Skip permission checks. Only use in trusted projects.");
            default: return ui.t("Permissions");
        }
    }

    void bind(String selected, JSONObject catalog, boolean loading, boolean failed) {
        this.selected = selected; this.catalog = catalog; this.loading = loading; this.failed = failed;
        render();
    }

    void show(View anchor) { confirmBypass = false; render(); popover.show(anchor, true); }

    private TextView text(String value, int size, int color) {
        TextView view = ui.text(value, size, color);
        ui.normalFallbackLineSpacing(view);
        ui.preciseText(view, true); return view;
    }

    private void render() {
        flow.clear();
        LinearLayout heading = ui.row(); heading.setPadding(ui.dp(9), ui.dp(9), ui.dp(9), ui.dp(9));
        TextView title = text(ui.t("Permissions"), 12, ui.text); ui.typography(title, 12, ui.text, 700);
        ui.normalFallbackLineSpacing(title);
        heading.addView(title, new LinearLayout.LayoutParams(0, -2, 1));
        heading.addView(text(backend.equals("codex") ? "Codex" : "Claude", 10, ui.subtle));
        heading.setTag("heading"); flow.add(heading, 0, 0);
        if (loading) note(ui.t("Checking project permissions…"), "loading");
        if (failed) {
            Failure status = new Failure(); status.setTag("failed");
            FrameLayout inset = new FrameLayout(ui.context); inset.setPadding(ui.dp(9), 0, ui.dp(9), 0);
            inset.addView(status, new FrameLayout.LayoutParams(-1, -2)); flow.add(inset, ui.dp(7), ui.dp(7));
        }
        JSONArray modes = catalog == null ? null : catalog.optJSONArray("modes");
        if (modes != null) for (int i = 0; i < modes.length(); i++) {
            JSONObject mode = modes.optJSONObject(i); if (mode == null) continue;
            String id = mode.optString("id"), reason = mode.optString("reason");
            Option row = new Option(id, reason.isEmpty() ? description(ui, id) : ui.t(reason), mode.optBoolean("available"));
            row.setTag("option:" + id); flow.add(row, 0, 0);
        }
        if (confirmBypass) {
            NativeMarkdownFlow confirmation = new NativeMarkdownFlow(ui);
            confirmation.collapseTop = confirmation.collapseBottom = false;
            confirmation.inset(ui.dp(6), ui.dp(6), ui.dp(6), ui.dp(6));
            confirmation.setBackground(ui.shape(ui.errorBg, 8, ui.errorBorder));
            confirmation.setTag("confirmation");
            TextView warning = text(description(ui, "bypassPermissions"), 11, ui.muted);
            FrameLayout warningInset = new FrameLayout(ui.context); warningInset.setPadding(ui.dp(9), 0, ui.dp(9), 0);
            warningInset.addView(warning, new FrameLayout.LayoutParams(-1, -2));
            warning.setTag("warning"); confirmation.add(warningInset, ui.dp(7), ui.dp(7));
            LinearLayout actions = ui.row(); actions.setGravity(Gravity.END | Gravity.CENTER_VERTICAL);
            actions.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
            TextView cancel = action(ui.t("Cancel"), false, () -> { confirmBypass = false; render(); });
            cancel.setTag("cancel"); actions.addView(cancel);
            TextView enable = action(ui.t("Enable bypass"), true, () -> select("bypassPermissions"));
            enable.setTag("enable");
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, -2); params.leftMargin = ui.dp(8);
            actions.addView(enable, params); confirmation.add(actions, 0, 0);
            flow.add(confirmation, ui.dp(8), 0);
            if (Build.VERSION.SDK_INT >= 28) confirmation.setAccessibilityPaneTitle(ui.t("Bypass permissions"));
            confirmation.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        }
        if (backend.equals("codex")) {
            TextView scope = text(ui.t("Project sandbox · network access requires approval"), 10, ui.muted);
            ui.fixedLineHeight(scope, 1.5f);
            scope.setPadding(0, ui.dp(10), 0, 0);
            scope.setBackground(new android.graphics.drawable.Drawable() {
                final Paint paint = new Paint();
                @Override public void draw(Canvas canvas) { paint.setColor(ui.borderStrong); canvas.drawRect(0, 0, getBounds().width(), ui.dp(1), paint); }
                @Override public void setAlpha(int alpha) { paint.setAlpha(alpha); }
                @Override public void setColorFilter(android.graphics.ColorFilter filter) { paint.setColorFilter(filter); }
                @Override public int getOpacity() { return android.graphics.PixelFormat.TRANSLUCENT; }
            });
            FrameLayout inset = new FrameLayout(ui.context); inset.setPadding(ui.dp(9), 0, ui.dp(9), 0);
            inset.addView(scope, new FrameLayout.LayoutParams(-1, -2)); inset.setTag("scope");
            flow.add(inset, ui.dp(7), ui.dp(7));
        }
        requestLayout();
    }

    private void note(String value, String tag) {
        TextView note = text(value, 11, ui.muted); note.setTag(tag);
        FrameLayout inset = new FrameLayout(ui.context); inset.setPadding(ui.dp(9), 0, ui.dp(9), 0);
        inset.addView(note, new FrameLayout.LayoutParams(-1, -2)); flow.add(inset, ui.dp(7), ui.dp(7));
    }

    private TextView action(String title, boolean danger, Runnable click) {
        TextView button = text(title, 10, danger ? ui.error : ui.muted);
        ui.preciseText(button);
        button.setPadding(ui.dp(6), ui.dp(1), ui.dp(6), ui.dp(1));
        button.setBackground(ui.shape(0, 0, 0));
        button.setFocusable(true); button.setFocusableInTouchMode(true); button.setOnClickListener(view -> click.run());
        ui.buttonKeys(button);
        button.setAccessibilityDelegate(new AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName(Button.class.getName());
            }
        });
        return button;
    }

    private void select(String id) {
        JSONArray modes = catalog == null ? null : catalog.optJSONArray("modes");
        if (loading || modes == null) return;
        for (int i = 0; i < modes.length(); i++) {
            JSONObject mode = modes.optJSONObject(i);
            if (mode != null && id.equals(mode.optString("id")) && mode.optBoolean("available")) {
                choose.accept(id); popover.dismiss(); return;
            }
        }
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        flow.measure(widthSpec, MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
        setMeasuredDimension(flow.getMeasuredWidth(), resolveSize(flow.getMeasuredHeight(), heightSpec));
    }
    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) { flow.layout(0, 0, flow.getMeasuredWidth(), flow.getMeasuredHeight()); }
    @Override public float cssHeight() { return flow.cssHeight(); }
    @Override public void roundingOffset(float x, float y) { flow.roundingOffset(x, y); }

    /** Reserve an inline control in the text layout, but keep Retry a real native focusable view. */
    private final class Failure extends FrameLayout {
        final TextView message, button;
        final int start;
        Failure() {
            super(ui.context);
            String status = ui.t("Permission discovery is unavailable"), caption = ui.t("Retry");
            message = text("", 11, ui.muted); message.setContentDescription(status);
            button = action(caption, false, retry); button.setTag("retry");
            ui.typography(button, 11, ui.muted, 400);
            ui.normalFallbackLineSpacing(button);
            button.setPadding(ui.dp(8), ui.dp(3), ui.dp(8), ui.dp(3));
            button.setBackground(new android.graphics.drawable.Drawable() {
                final Paint paint = new Paint();
                @Override public void draw(Canvas canvas) {
                    paint.setColor(ui.dark ? 0xff3b3b3b : 0xffefefef);
                    canvas.drawRect(getBounds(), paint);
                    paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(ui.dp(2));
                    paint.setColor(ui.dark ? 0xff858585 : 0xff767676);
                    canvas.drawRect(ui.dp(1), ui.dp(1), getBounds().right - ui.dp(1), getBounds().bottom - ui.dp(1), paint);
                    paint.setStyle(Paint.Style.FILL);
                }
                @Override public void setAlpha(int alpha) { paint.setAlpha(alpha); }
                @Override public void setColorFilter(android.graphics.ColorFilter filter) { paint.setColorFilter(filter); }
                @Override public int getOpacity() { return android.graphics.PixelFormat.OPAQUE; }
            });
            start = status.length() + 1;
            android.text.SpannableString contents = new android.text.SpannableString(status + " " + caption);
            contents.setSpan(new android.text.style.ReplacementSpan() {
                @Override public int getSize(Paint paint, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) {
                    if (metrics != null) {
                        metrics.top = metrics.ascent = -button.getBaseline();
                        metrics.bottom = metrics.descent = button.getMeasuredHeight() - button.getBaseline();
                    }
                    return button.getMeasuredWidth();
                }
                @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int y, int bottom, Paint paint) {}
            }, start, contents.length(), android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            message.setText(contents); addView(message); addView(button);
        }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            int width = MeasureSpec.getSize(widthSpec), copyWidth = Math.max(0, width - getPaddingLeft() - getPaddingRight());
            button.measure(MeasureSpec.makeMeasureSpec(copyWidth, MeasureSpec.AT_MOST), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            message.measure(MeasureSpec.makeMeasureSpec(copyWidth, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            setMeasuredDimension(width, resolveSize(message.getMeasuredHeight(), heightSpec));
        }
        @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
            message.layout(getPaddingLeft(), 0, getPaddingLeft() + message.getMeasuredWidth(), message.getMeasuredHeight());
            android.text.Layout layout = message.getLayout(); int line = layout.getLineForOffset(start);
            int x = getPaddingLeft() + Math.round(layout.getPrimaryHorizontal(start));
            int y = message.getBaseline() - layout.getLineBaseline(0) + layout.getLineBaseline(line) - button.getBaseline();
            button.layout(x, y, x + button.getMeasuredWidth(), y + button.getMeasuredHeight());
        }
    }

    final class Option extends FrameLayout implements NativeMarkdownFlowBox {
        final TextView title, detail;
        final ImageView check;
        final String id;
        private float height, titleHeight;
        Option(String id, String description, boolean available) {
            super(ui.context); this.id = id;
            title = text(label(ui, backend, id), 12, id.equals("bypassPermissions") ? ui.primary : ui.text);
            ui.typography(title, 12, id.equals("bypassPermissions") ? ui.primary : ui.text, 500);
            ui.normalFallbackLineSpacing(title);
            detail = text(description, 10, ui.muted); ui.fixedLineHeight(detail, 1.5f);
            addView(title); addView(detail);
            check = selected.equals(id) ? ui.decorativeIcon("check", 15) : null;
            if (check != null) { check.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text)); addView(check); }
            setSelected(selected.equals(id)); setEnabled(available); setAlpha(available ? 1 : .5f);
            setBackground(ui.states(isSelected() ? ui.soft : 0, 7, 0, ui.soft, isSelected() ? ui.soft : 0));
            setFocusable(true); setFocusableInTouchMode(true); setContentDescription(title.getText() + ", " + detail.getText());
            ui.buttonKeys(this);
            setOnClickListener(view -> {
                if (!isEnabled()) return;
                if (id.equals("bypassPermissions") && !selected.equals(id)) {
                    if (!confirmBypass) { confirmBypass = true; render(); }
                } else select(id);
            });
            setAccessibilityDelegate(new AccessibilityDelegate() {
                @Override public void onInitializeAccessibilityNodeInfo(View view, AccessibilityNodeInfo info) {
                    super.onInitializeAccessibilityNodeInfo(view, info);
                    info.setClassName(Button.class.getName()); info.setCheckable(true); info.setChecked(isSelected());
                }
            });
            for (int i = 0; i < getChildCount(); i++) getChildAt(i).setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            int width = MeasureSpec.getSize(widthSpec), copyWidth = Math.max(0, width - ui.dp(18) - (check == null ? 0 : ui.dp(25)));
            int copy = MeasureSpec.makeMeasureSpec(copyWidth, MeasureSpec.EXACTLY), natural = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
            title.measure(copy, natural); detail.measure(copy, natural);
            titleHeight = NativeUi.cssTextHeight(title);
            height = titleHeight + ui.dp(3) + NativeUi.cssTextHeight(detail) + ui.dp(18);
            if (check != null) check.measure(MeasureSpec.makeMeasureSpec(ui.dp(15), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(ui.dp(15), MeasureSpec.EXACTLY));
            setMeasuredDimension(width, resolveSize(Math.round(height), heightSpec));
        }
        @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
            int x = ui.dp(9), y = ui.dp(9);
            title.layout(x, y, x + title.getMeasuredWidth(), y + title.getMeasuredHeight());
            int detailY = Math.round(y + titleHeight + ui.dp(3));
            detail.layout(x, detailY, x + detail.getMeasuredWidth(), detailY + detail.getMeasuredHeight());
            if (check != null) {
                int left = getWidth() - ui.dp(24), top = (getHeight() - ui.dp(15)) / 2;
                check.layout(left, top, left + ui.dp(15), top + ui.dp(15));
            }
        }
        @Override public float cssHeight() { return height; }
    }
}
