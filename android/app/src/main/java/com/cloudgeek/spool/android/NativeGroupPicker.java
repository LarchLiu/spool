package com.cloudgeek.spool.android;

import android.app.Dialog;
import android.view.Gravity;
import android.view.View;
import android.view.Window;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.function.Consumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** Reusable Web PeopleGroupPicker, with session-scoped async submit and live available-member pruning. */
final class NativeGroupPicker extends Dialog {
    interface Completion { void done(JSONObject group, String code); }
    interface Submit { void submit(String name, List<String> members, Completion completion); }
    final LinearLayout content, heading, body, memberRows, actions;
    final EditText name;
    final TextView count, error, notice;
    final Button submit;
    final ScrollView scroll;
    final Set<String> selected = new LinkedHashSet<>();
    final String target;
    private final NativeUi ui;
    private final Submit request;
    private final Consumer<JSONObject> done;
    private JSONArray available = new JSONArray();
    private int limit, epoch;
    private boolean busy, dismissed;
    final NativeModalFrame modal;

    NativeGroupPicker(NativeUi ui, JSONObject group, Submit request, Consumer<JSONObject> done) {
        super(ui.context); this.ui = ui; this.request = request; this.done = done;
        target = group == null ? "" : group.optString("id"); limit = 32 - (group == null ? 1 : group.optJSONArray("members").length());
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        int padding = ui.context.getResources().getDisplayMetrics().widthPixels <= ui.dp(400) ? 20 : 24;
        content = ui.column(); content.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        heading = ui.row(); heading.setPadding(ui.dp(padding), ui.dp(20), ui.dp(padding), ui.dp(16));
        TextView title = ui.bold(ui.t(group == null ? "New group chat" : "Invite people"), 16); ui.fixedLineHeight(title, 1.5f);
        heading.addView(title, new LinearLayout.LayoutParams(0, -2, 1)); heading.addView(ui.icon("close", 18, ui.t("Close group chat"), this::dismiss)); content.addView(heading);
        heading.setFocusableInTouchMode(true);
        body = ui.column(); body.setPadding(ui.dp(padding), 0, ui.dp(padding), ui.dp(20)); body.setClipChildren(false);
        LinearLayout naming = new LinearLayout(ui.context) { @Override protected void dispatchDraw(android.graphics.Canvas canvas) { super.dispatchDraw(canvas); ui.focusRing(canvas, name, 9); } }; naming.setOrientation(LinearLayout.VERTICAL); naming.setClipChildren(false); naming.setClipToPadding(false); naming.setPadding(0, 0, 0, ui.dp(12));
        TextView nameLabel = ui.text(ui.t("Group name"), 12, ui.text); ui.typography(nameLabel, 12, ui.text, 500); ui.fixedLineHeight(nameLabel, 1.5f); naming.addView(nameLabel); naming.addView(ui.space(8));
        int inputSize = padding == 20 ? 16 : 14;
        name = ui.field(ui.t("Group chat"), false); ui.typography(name, inputSize, ui.text, 400); ui.fixedLineHeight(name, 1.5f); name.setMinHeight(ui.dp(inputSize * 1.5f + 20)); name.setPadding(ui.dp(13), ui.dp(10), ui.dp(13), ui.dp(10)); name.setBackground(ui.shape(ui.input, 9, ui.line)); name.setFilters(new android.text.InputFilter[] {new android.text.InputFilter.LengthFilter(80)}); name.setOnFocusChangeListener((view, focused) -> naming.invalidate());
        naming.addView(name); naming.setVisibility(group == null ? View.VISIBLE : View.GONE); body.addView(naming);
        notice = ui.text(ui.t("Only selected members can see this group. No history is saved; the group closes when everyone disconnects."), 12, ui.muted); ui.fixedLineHeight(notice, 1.7f);
        LinearLayout.LayoutParams note = new LinearLayout.LayoutParams(-1, -2); note.bottomMargin = ui.dp(20); body.addView(notice, note);
        LinearLayout members = ui.column(); TextView legend = ui.text(ui.t("Select online people"), 12, ui.text); ui.typography(legend, 12, ui.text, 500); ui.fixedLineHeight(legend, 1.5f); members.addView(legend); members.addView(ui.space(8)); memberRows = ui.column(); members.addView(memberRows);
        ScrollView memberScroll = new ScrollView(ui.context) { @Override protected void onMeasure(int width, int height) { super.onMeasure(width, MeasureSpec.makeMeasureSpec(ui.dp(280), MeasureSpec.AT_MOST)); } }; memberScroll.setVerticalScrollBarEnabled(false); memberScroll.addView(members); body.addView(memberScroll);
        error = ui.text("", 12, ui.error); ui.fixedLineHeight(error, 1.6f); error.setPadding(ui.dp(14), ui.dp(11), ui.dp(14), ui.dp(11)); error.setBackground(ui.shape(ui.errorBg, 10, ui.errorBorder)); error.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); error.setVisibility(View.GONE);
        LinearLayout.LayoutParams errorParams = new LinearLayout.LayoutParams(-1, -2); errorParams.topMargin = ui.dp(12); body.addView(error, errorParams);
        scroll = new ScrollView(ui.context); scroll.setVerticalScrollBarEnabled(false); scroll.addView(body); content.addView(scroll, new LinearLayout.LayoutParams(-1, -2, 1));
        content.addView(ui.divider()); actions = ui.row(); actions.setPadding(ui.dp(padding), ui.dp(16), ui.dp(padding), ui.dp(16));
        count = ui.text("", 12, ui.muted); count.setSingleLine(true); count.getPaint().setLinearText(true); count.getPaint().setSubpixelText(true); actions.addView(count); actions.addView(ui.space(0), new LinearLayout.LayoutParams(0, 0, 1));
        submit = ui.button(ui.t(group == null ? "Create group" : "Invite people"), true, this::send); ui.typography(submit, 12, 0xffffffff, 500); ui.fixedLineHeight(submit, 20f / 12); submit.setSingleLine(true); submit.setMinWidth(ui.dp(112)); submit.setMinHeight(ui.dp(38)); submit.setPadding(ui.dp(18), ui.dp(9), ui.dp(18), ui.dp(9));
        submit.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams button = new LinearLayout.LayoutParams(-2, -2); button.setMarginStart(ui.dp(12)); actions.addView(submit, button); content.addView(actions);
        modal = new NativeModalFrame(ui, NativeModalFrame.Profile.GROUP, content, null); setContentView(modal);
        setCanceledOnTouchOutside(false);
        NativeModalFrame.prepare(this, ui, modal.profile);
        setOnKeyListener((dialog, code, event) -> { if (code != android.view.KeyEvent.KEYCODE_ESCAPE) return false; if (event.getAction() == android.view.KeyEvent.ACTION_DOWN) cancel(); return true; });
        controls();
    }
    void bind(JSONArray people, JSONObject group) {
        available = people; if (group != null) limit = 32 - group.optJSONArray("members").length();
        Set<String> allowed = new LinkedHashSet<>(); for (int i = 0; i < people.length(); i++) allowed.add(people.optJSONObject(i).optString("id")); selected.retainAll(allowed); rows();
    }
    private void rows() {
        memberRows.removeAllViews();
        if (available.length() == 0) {
            LinearLayout empty = ui.column(); empty.setGravity(Gravity.CENTER); empty.setMinimumHeight(ui.dp(104)); empty.setPadding(ui.dp(16), ui.dp(16), ui.dp(16), ui.dp(16));
            android.graphics.drawable.GradientDrawable background = ui.shape(ui.input, 10, 0); background.setStroke(ui.dp(1), ui.line, ui.dp(3), ui.dp(3)); empty.setBackground(background);
            android.widget.ImageView icon = ui.decorativeIcon("people", 24); icon.setColorFilter(ui.subtle); empty.addView(icon, new LinearLayout.LayoutParams(ui.dp(24), ui.dp(24))); empty.addView(ui.space(10));
            TextView label = ui.text(ui.t("No other people online."), 12, ui.muted); ui.fixedLineHeight(label, 1.6f); label.setGravity(Gravity.CENTER); empty.addView(label); memberRows.addView(empty, new LinearLayout.LayoutParams(-1, -2));
        }
        for (int i = 0; i < available.length(); i++) {
            JSONObject person = available.optJSONObject(i); String id = person.optString("id");
            LinearLayout row = ui.row(); row.setPadding(ui.dp(11), ui.dp(9), ui.dp(11), ui.dp(9)); row.setMinimumHeight(ui.dp(44));
            NativeCheckbox check = new NativeCheckbox(ui); check.setChecked(selected.contains(id)); check.setEnabled(!busy && (selected.contains(id) || selected.size() < limit)); check.setContentDescription(NativeAvatars.name(person));
            check.setButtonDrawable(new android.graphics.drawable.InsetDrawable(NativeCheckbox.glyph(ui, check::isChecked, check::isEnabled), 0) {
                @Override public int getIntrinsicWidth() { return ui.dp(16); }
                @Override public int getIntrinsicHeight() { return ui.dp(16); }
            });
            row.addView(check, new LinearLayout.LayoutParams(ui.dp(16), ui.dp(16)));
            int fill = selected.contains(id) ? androidx.core.graphics.ColorUtils.blendARGB(ui.elevated, ui.primary, .08f) : 0;
            int border = selected.contains(id) ? androidx.core.graphics.ColorUtils.setAlphaComponent(ui.primary, 64) : 0;
            row.setBackground(ui.states(fill, 9, border, ui.controlActive, fill)); row.setAlpha(check.isEnabled() ? 1 : .55f);
            LinearLayout.LayoutParams avatar = new LinearLayout.LayoutParams(ui.dp(26), ui.dp(26)); avatar.setMarginStart(ui.dp(10)); row.addView(ui.avatar(person, 26), avatar);
            LinearLayout.LayoutParams label = new LinearLayout.LayoutParams(0, -2, 1); label.setMarginStart(ui.dp(10)); row.addView(ui.text(NativeAvatars.name(person), 13, ui.text), label);
            check.setOnCheckedChangeListener((view, checked) -> { if (checked) selected.add(id); else selected.remove(id); rows(); }); row.setOnClickListener(view -> { if (check.isEnabled()) check.toggle(); }); memberRows.addView(row);
        }
        controls();
    }
    private void controls() {
        name.setEnabled(!busy); submit.setEnabled(!busy && !selected.isEmpty()); submit.setText(ui.t(busy ? "Working…" : target.isEmpty() ? "Create group" : "Invite people"));
        count.setText(ui.t("{count} selected", "count", selected.size()));
    }
    private void send() {
        if (busy || selected.isEmpty()) return;
        busy = true; error.setVisibility(View.GONE); rows(); int requestEpoch = ++epoch;
        request.submit(name.getText().toString(), new ArrayList<>(selected), (group, code) -> {
            if (dismissed || requestEpoch != epoch) return;
            busy = false;
            if (code == null || code.isEmpty()) { done.accept(group); return; }
            error.setText(ui.t(NativePeopleModel.errorKey(code, true))); error.setVisibility(View.VISIBLE); rows();
        });
    }
    @Override public void show() { ui.dismissMenus(); dismissed = false; super.show(); NativeModalFrame.shown(this, target.isEmpty() ? name : null); }
    @Override public void dismiss() { dismissed = true; epoch++; super.dismiss(); }
}
