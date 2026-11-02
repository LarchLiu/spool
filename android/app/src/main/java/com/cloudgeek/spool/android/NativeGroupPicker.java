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
    final LinearLayout content, heading, memberRows, actions;
    final EditText name;
    final TextView count, error;
    final Button submit;
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
        content = ui.column(); content.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        heading = ui.row(); heading.setPadding(ui.dp(16), ui.dp(12), ui.dp(16), ui.dp(12));
        TextView title = ui.bold(ui.t(group == null ? "New group chat" : "Invite people"), 16); ui.typography(title, 16, ui.text, 700); title.setPadding(0, 0, 0, ui.dp(4));
        heading.addView(title, new LinearLayout.LayoutParams(0, -2, 1)); heading.addView(ui.icon("close", 18, ui.t("Close group chat"), this::dismiss)); content.addView(heading);
        heading.setFocusableInTouchMode(true);
        LinearLayout naming = new LinearLayout(ui.context) { @Override protected void dispatchDraw(android.graphics.Canvas canvas) { super.dispatchDraw(canvas); ui.focusRing(canvas, name, 8); } }; naming.setOrientation(LinearLayout.VERTICAL); naming.setClipChildren(false); naming.setClipToPadding(false); naming.setPadding(ui.dp(20), ui.dp(4), ui.dp(20), ui.dp(16));
        naming.addView(ui.text(ui.t("Group name"), 12, ui.text)); naming.addView(ui.space(8));
        name = ui.field(ui.t("Group chat"), false); ui.typography(name, 12, ui.text, 400); name.setPadding(ui.dp(11), ui.dp(10), ui.dp(11), ui.dp(10)); name.setBackground(ui.shape(ui.input, 8, ui.line)); name.setFilters(new android.text.InputFilter[] {new android.text.InputFilter.LengthFilter(80)}); name.setOnFocusChangeListener((view, focused) -> naming.invalidate());
        naming.addView(name); naming.setVisibility(group == null ? View.VISIBLE : View.GONE); content.addView(naming);
        TextView notice = ui.text(ui.t("Only selected members can see this group. No history is saved; the group closes when everyone disconnects."), 16, ui.text); ui.fixedLineHeight(notice, 19f / 16);
        LinearLayout.LayoutParams note = new LinearLayout.LayoutParams(-1, -2); note.topMargin = note.bottomMargin = ui.dp(16); content.addView(notice, note);
        LinearLayout members = ui.column(); TextView legend = ui.text(ui.t("Select online people"), 11, ui.muted); members.addView(legend); members.addView(ui.space(10)); memberRows = ui.column(); members.addView(memberRows);
        ScrollView memberScroll = new ScrollView(ui.context) { @Override protected void onMeasure(int width, int height) { super.onMeasure(width, MeasureSpec.makeMeasureSpec(ui.dp(280), MeasureSpec.AT_MOST)); } }; memberScroll.setVerticalScrollBarEnabled(false); memberScroll.addView(members);
        // The paragraph and fieldset's 16px vertical margins collapse in the Web form.
        LinearLayout.LayoutParams field = new LinearLayout.LayoutParams(-1, -2); field.setMargins(ui.dp(20), 0, ui.dp(20), ui.dp(16)); content.addView(memberScroll, field);
        error = ui.text("", 11, ui.error); ui.fixedLineHeight(error, 1.6f); error.setPadding(ui.dp(20), ui.dp(10), ui.dp(20), ui.dp(10)); error.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); error.setVisibility(View.GONE); content.addView(error);
        content.addView(ui.divider()); actions = ui.row(); actions.setPadding(ui.dp(20), ui.dp(14), ui.dp(20), ui.dp(14));
        count = ui.text("", 11, ui.muted); count.getPaint().setLinearText(true); count.getPaint().setSubpixelText(true); actions.addView(count);
        submit = ui.button(ui.t(group == null ? "Create group" : "Invite people"), true, this::send); submit.setPadding(ui.dp(17), ui.dp(13), ui.dp(17), ui.dp(13));
        submit.setGravity(Gravity.START | Gravity.CENTER_VERTICAL);
        LinearLayout.LayoutParams button = new LinearLayout.LayoutParams(0, -2, 1); button.gravity = Gravity.TOP; button.setMarginStart(ui.dp(12)); button.topMargin = ui.dp(28); button.bottomMargin = ui.dp(18); actions.addView(submit, button); content.addView(actions);
        ScrollView scroll = new ScrollView(ui.context) { @Override protected void onMeasure(int width, int height) { int maximum = getResources().getDisplayMetrics().heightPixels - ui.dp(32); super.onMeasure(width, MeasureSpec.makeMeasureSpec(Math.min(maximum, MeasureSpec.getSize(height) > 0 ? MeasureSpec.getSize(height) : maximum), MeasureSpec.AT_MOST)); } };
        scroll.setVerticalScrollBarEnabled(false); scroll.addView(content);
        modal = new NativeModalFrame(ui, NativeModalFrame.Profile.GROUP, scroll, null); setContentView(modal);
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
        if (available.length() == 0) { TextView empty = ui.text(ui.t("No other people online."), 12, ui.muted); empty.setPadding(0, ui.dp(12), 0, ui.dp(12)); memberRows.addView(empty); }
        for (int i = 0; i < available.length(); i++) {
            JSONObject person = available.optJSONObject(i); String id = person.optString("id");
            LinearLayout row = ui.row(); row.setPadding(0, ui.dp(9), 0, ui.dp(9));
            NativeCheckbox check = new NativeCheckbox(ui); check.setChecked(selected.contains(id)); check.setEnabled(!busy && (selected.contains(id) || selected.size() < limit)); check.setContentDescription(NativeAvatars.name(person));
            row.addView(check, new LinearLayout.LayoutParams(ui.dp(20), ui.dp(20)));
            LinearLayout.LayoutParams avatar = new LinearLayout.LayoutParams(ui.dp(26), ui.dp(26)); avatar.setMarginStart(ui.dp(10)); row.addView(ui.avatar(person, 26), avatar);
            LinearLayout.LayoutParams label = new LinearLayout.LayoutParams(0, -2, 1); label.setMarginStart(ui.dp(10)); row.addView(ui.text(NativeAvatars.name(person), 13, ui.text), label);
            check.setOnCheckedChangeListener((view, checked) -> { if (checked) selected.add(id); else selected.remove(id); rows(); }); row.setOnClickListener(view -> { if (check.isEnabled()) check.toggle(); }); memberRows.addView(row);
        }
        controls();
    }
    private void controls() {
        name.setEnabled(!busy); submit.setEnabled(!busy && !selected.isEmpty()); submit.setText(ui.t(busy ? "Working…" : target.isEmpty() ? "Create group" : "Invite people"));
        String label = ui.t("{count} selected", "count", selected.size()); count.setText(label);
        java.text.BreakIterator breaks = java.text.BreakIterator.getLineInstance(ui.chinese ? java.util.Locale.CHINESE : ui.locale.equals("ja") ? java.util.Locale.JAPANESE : java.util.Locale.ENGLISH);
        breaks.setText(label); float minimum = 0;
        for (int start = breaks.first(), end = breaks.next(); end != java.text.BreakIterator.DONE; start = end, end = breaks.next()) minimum = Math.max(minimum, count.getPaint().measureText(label.substring(start, end).trim()));
        count.getLayoutParams().width = (int) Math.ceil(minimum); count.requestLayout();
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
