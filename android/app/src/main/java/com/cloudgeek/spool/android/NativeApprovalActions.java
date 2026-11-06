package com.cloudgeek.spool.android;

import android.graphics.Outline;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;

/** Independent refusal plus a confirm button with a selection-only menu. */
final class NativeApprovalActions extends ViewGroup {
    private final NativeUi ui;
    private final List<JSONObject> options = new ArrayList<>();
    private JSONObject selected;
    private final Button deny, confirm, dropdown;
    private final View main;
    private final TextView detail;
    private final boolean unavailable;
    private NativePopover menu;
    private boolean wrapped;

    NativeApprovalActions(NativeUi ui, JSONObject request, TextView detail, Consumer<JSONObject> submit) {
        super(ui.context); this.ui = ui; this.detail = detail;
        unavailable = !request.optString("unavailable").isEmpty();
        JSONArray decisions = request.optJSONArray("decisions");
        if (decisions == null || decisions.length() == 0) {
            decisions = new JSONArray().put(choice("deny", "Deny", "deny")).put(choice("allow", request.optString("url").isEmpty() ? "Allow once" : "Confirm authorization", "approve"));
        }
        JSONObject refusal = null;
        for (int i = 0; i < decisions.length(); i++) { JSONObject value = decisions.optJSONObject(i); if (value != null && value.optString("id").equals("deny")) { refusal = value; break; } }
        if (refusal == null) for (int i = 0; i < decisions.length(); i++) { JSONObject value = decisions.optJSONObject(i); if (value != null && (value.optString("kind").equals("deny") || value.optString("kind").equals("cancel"))) { refusal = value; break; } }
        for (int i = 0; i < decisions.length(); i++) { JSONObject value = decisions.optJSONObject(i); if (value != null && value != refusal) options.add(value); }
        JSONObject refusalChoice = refusal;
        deny = refusal == null ? null : ui.button(ui.t(refusal.optString("label")), false, () -> submit.accept(refusalChoice));
        if (deny != null) {
            int fill = androidx.core.graphics.ColorUtils.blendARGB(ui.soft, ui.text, .20f);
            deny.setBackground(ui.states(fill, 9, ui.borderStrong, androidx.core.graphics.ColorUtils.blendARGB(ui.soft, ui.text, .24f), fill));
            size(deny); addView(deny);
        }
        selected = options.isEmpty() ? null : options.get(0);
        for (JSONObject option : options) if (contentRequired(option)) { selected = option; break; }
        for (JSONObject option : options) if (option.optString("id").equals("allow")) { selected = option; break; }
        confirm = selected == null ? null : ui.button("", true, () -> { if (selected != null && isEnabled()) submit.accept(selected); });
        dropdown = options.size() <= 1 ? null : ui.button("", true, this::open);
        if (confirm == null) main = null;
        else if (dropdown == null) { main = confirm; size(confirm); addView(main); }
        else {
            LinearLayout split = new LinearLayout(ui.context); split.setGravity(Gravity.CENTER_VERTICAL);
            split.setOutlineProvider(new android.view.ViewOutlineProvider() { @Override public void getOutline(View view, Outline outline) { outline.setRoundRect(0, 0, view.getWidth(), view.getHeight(), ui.dp(9)); } });
            split.setClipToOutline(true);
            size(confirm); size(dropdown);
            confirm.setBackground(splitBackground(false));
            dropdown.setBackground(splitBackground(true));
            ui.setButtonIcon(dropdown, "chevron-down"); dropdown.setPadding(ui.dp(10), ui.dp(9), ui.dp(10), ui.dp(9));
            dropdown.setContentDescription(ui.t("Choose an approval option"));
            split.addView(confirm, new LinearLayout.LayoutParams(-2, -2));
            View divider = new View(ui.context); divider.setBackgroundColor(0x40ffffff); split.addView(divider, new LinearLayout.LayoutParams(ui.dp(1), ui.dp(34)));
            split.addView(dropdown, new LinearLayout.LayoutParams(ui.dp(34), -2)); main = split; addView(main);
        }
        bind();
    }

    static JSONObject choice(String id, String label, String kind) {
        try { return new JSONObject().put("id", id).put("label", label).put("kind", kind); }
        catch (org.json.JSONException invalid) { throw new IllegalArgumentException(invalid); }
    }
    static boolean contentRequired(JSONObject choice) { return choice.optString("kind").equals("approve") || choice.optString("kind").equals("submit"); }
    private void size(Button button) { button.setPadding(ui.dp(17), ui.dp(9), ui.dp(17), ui.dp(9)); button.setMinimumHeight(ui.dp(34)); }
    private android.graphics.drawable.Drawable splitBackground(boolean end) {
        android.graphics.drawable.StateListDrawable states = new android.graphics.drawable.StateListDrawable();
        float radius = ui.dp(9);
        float[] corners = end ? new float[] {0,0,radius,radius,radius,radius,0,0} : new float[] {radius,radius,0,0,0,0,radius,radius};
        for (int i=0; i<4; i++) {
            android.graphics.drawable.GradientDrawable shape = ui.shape(i==1 || i==2 ? ui.primaryHover : ui.primary, 0, 0);
            shape.setCornerRadii(corners);
            states.addState(i==0 ? new int[] {-android.R.attr.state_enabled} : i==1 ? new int[] {android.R.attr.state_pressed} : i==2 ? new int[] {android.R.attr.state_hovered} : new int[0], shape);
        }
        return states;
    }
    private void bind() {
        if (confirm != null) { confirm.setText(ui.t(selected.optString("label"))); confirm.setEnabled(isEnabled() && (!unavailable || !contentRequired(selected))); }
        String description = selected == null ? "" : selected.optString("description");
        detail.setText(ui.t(description)); detail.setVisibility(description.isEmpty() ? GONE : VISIBLE);
        requestLayout();
    }
    private void open() {
        if (!isEnabled() || dropdown == null) return;
        menu = new NativePopover(ui, 300, 10, 5);
        int initial = 0;
        for (int i = 0; i < options.size(); i++) {
            JSONObject option = options.get(i);
            if (option == selected) initial = i;
            menu.selectOption(ui.t(option.optString("label")), ui.t(option.optString("description")), option == selected, () -> {
                menu.dismiss(); if (!isEnabled()) return;
                selected = option; bind(); dropdown.requestFocus();
            });
            View row = menu.content.getChildAt(menu.content.getChildCount() - 1);
            row.setEnabled(!unavailable || !contentRequired(option));
            if (!row.isEnabled()) row.setAlpha(.45f);
        }
        menu.showSelect(dropdown, initial);
    }
    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled); if (deny != null) deny.setEnabled(enabled); if (dropdown != null) dropdown.setEnabled(enabled);
        if (!enabled && menu != null) menu.dismiss(); bind();
    }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec), gap = ui.dp(12), flexible = MeasureSpec.makeMeasureSpec(width, MeasureSpec.AT_MOST), height = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
        if (deny != null) deny.measure(flexible, height);
        if (confirm != null) confirm.setMaxWidth(Math.max(0, width - (dropdown == null ? 0 : ui.dp(35))));
        if (main != null) main.measure(flexible, height);
        int dw = deny == null ? 0 : deny.getMeasuredWidth(), mw = main == null ? 0 : main.getMeasuredWidth();
        wrapped = deny != null && main != null && dw + mw + gap > width;
        int dh = deny == null ? 0 : deny.getMeasuredHeight(), mh = main == null ? 0 : main.getMeasuredHeight();
        setMeasuredDimension(width, wrapped ? dh + gap + mh : Math.max(dh, mh));
    }
    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        int width = r-l, height = b-t, gap = ui.dp(12), mw = main == null ? 0 : main.getMeasuredWidth();
        if (deny != null) {
            int left = wrapped ? width-deny.getMeasuredWidth() : width-mw-(main == null ? 0 : gap)-deny.getMeasuredWidth();
            int top = wrapped ? 0 : (height-deny.getMeasuredHeight())/2;
            deny.layout(left, top, left+deny.getMeasuredWidth(), top+deny.getMeasuredHeight());
        }
        if (main != null) { int top = wrapped ? height-main.getMeasuredHeight() : (height-main.getMeasuredHeight())/2; main.layout(width-mw, top, width, top+main.getMeasuredHeight()); }
    }
}
