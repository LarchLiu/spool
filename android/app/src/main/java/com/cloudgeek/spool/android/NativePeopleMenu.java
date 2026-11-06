package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONObject;

/** Original mobile online-list: inbox, room/create actions, live groups and current presence. */
final class NativePeopleMenu {
    final NativePopover popover;
    final LinearLayout content;
    final List<View> options = new ArrayList<>();
    final List<View> rows = new ArrayList<>();
    private final NativeUi ui;
    private final Runnable inbox, create;
    private final Consumer<String> select;

    NativePeopleMenu(NativeUi ui, Runnable inbox, Consumer<String> select, Runnable create) {
        this.ui = ui; this.inbox = inbox; this.select = select; this.create = create;
        popover = new NativePopover(ui, 360, 12, 0, 24); content = popover.content;
    }

    private TextView label(String title, int size, int color) {
        TextView text = ui.text(title, size, color); ui.fixedLineHeight(text, 1.1f); ui.preciseText(text); return text;
    }
    private TextView badge(int count) {
        TextView text = label(NativePeopleModel.badge(count), 9, 0xffffffff); ui.typography(text, 9, 0xffffffff, 600);
        text.setMinWidth(ui.dp(17)); text.setPadding(ui.dp(4), 0, ui.dp(4), 0); text.setGravity(Gravity.CENTER); text.setBackground(ui.shape(ui.primary, 10, 0));
        return text;
    }
    private void item(LinearLayout row, View child, int gap) {
        android.view.ViewGroup.LayoutParams previous = child.getLayoutParams();
        LinearLayout.LayoutParams at = new LinearLayout.LayoutParams(previous == null ? -2 : previous.width, previous == null ? -2 : previous.height); at.setMarginStart(ui.dp(row.getChildCount() == 0 ? 0 : gap)); row.addView(child, at);
    }
    private LinearLayout button(String title, View symbol, int unread, String tag, Runnable action) {
        TextView text = label(title, 10, ui.text);
        LinearLayout button = new LinearLayout(ui.context) {
            @Override protected void onMeasure(int width, int height) {
                int extras = 0;
                for (int i = 0; i < getChildCount(); i++) if (getChildAt(i) != text) {
                    View child = getChildAt(i); int fixed = child.getLayoutParams().width;
                    child.measure(MeasureSpec.makeMeasureSpec(Math.max(0, fixed), fixed >= 0 ? MeasureSpec.EXACTLY : MeasureSpec.UNSPECIFIED), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED)); extras += child.getMeasuredWidth() + ui.dp(7);
                }
                text.setMaxWidth(MeasureSpec.getMode(width) == MeasureSpec.UNSPECIFIED ? Integer.MAX_VALUE : Math.max(1, MeasureSpec.getSize(width) - extras));
                super.onMeasure(width, height);
            }
        };
        button.setGravity(Gravity.CENTER_VERTICAL); button.setPadding(0, ui.dp(3), 0, ui.dp(3));
        if (symbol != null) item(button, symbol, 7); item(button, text, 7);
        if (unread > 0) { TextView badge = badge(unread); LinearLayout.LayoutParams at = new LinearLayout.LayoutParams(-2, ui.dp(17)); at.setMarginStart(ui.dp(7)); button.addView(badge, at); }
        action(button, tag, title, action); return button;
    }
    private void action(View view, String tag, String label, Runnable action) {
        view.setTag(tag); ui.focusOnPress(view); view.setContentDescription(label);
        view.setBackground(ui.states(0, 4, 0, ui.controlHover, 0));
        view.setOnClickListener(target -> { popover.dismiss(); action.run(); });
        view.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View target, android.view.accessibility.AccessibilityNodeInfo info) { super.onInitializeAccessibilityNodeInfo(target, info); info.setClassName("android.widget.Button"); }
        });
        view.setOnKeyListener((target, key, event) -> {
            if (event.getAction() != KeyEvent.ACTION_DOWN) return false;
            if (key == KeyEvent.KEYCODE_ESCAPE || key == KeyEvent.KEYCODE_TAB) { popover.dismiss(); return true; }
            int count = options.size(), index = options.indexOf(target), next = key == KeyEvent.KEYCODE_DPAD_DOWN ? (index + 1) % count : key == KeyEvent.KEYCODE_DPAD_UP ? (index - 1 + count) % count : key == KeyEvent.KEYCODE_MOVE_HOME ? 0 : key == KeyEvent.KEYCODE_MOVE_END ? count - 1 : -1;
            if (next < 0) return false; options.get(next).requestFocus(); return true;
        });
        options.add(view);
    }
    private LinearLayout section(boolean divider) {
        LinearLayout wrapper = ui.column(), row = ui.column(); row.setPadding(0, ui.dp(9), 0, ui.dp(9)); wrapper.addView(row, new LinearLayout.LayoutParams(-1, -2));
        if (divider) wrapper.addView(ui.divider());
        LinearLayout.LayoutParams at = new LinearLayout.LayoutParams(-1, -2); if (divider) at.bottomMargin = ui.dp(3);
        content.addView(wrapper, at); rows.add(wrapper); return row;
    }

    void bind(String self, JSONArray online, NativePeopleModel model, Function<String, JSONObject> person) {
        String focused = ""; for (View option : options) if (option.hasFocus()) focused = String.valueOf(option.getTag());
        content.removeAllViews(); options.clear(); rows.clear();
        section(true).addView(button(ui.t("People chat"), null, model.unreadCount(), "inbox", inbox), new LinearLayout.LayoutParams(-2, -2));
        LinearLayout room = section(true);
        room.addView(button(ui.t("Online group chat"), ui.decorativeIcon("people", 16), model.unread.getOrDefault(NativePeopleModel.ROOM, 0), NativePeopleModel.ROOM, () -> select.accept(NativePeopleModel.ROOM)), new LinearLayout.LayoutParams(-1, -2));
        LinearLayout add = ui.row(); add.setPadding(0, ui.dp(6), 0, ui.dp(6)); item(add, ui.decorativeIcon("plus", 14), 6); item(add, label(ui.t("New group chat"), 10, ui.muted), 6); action(add, "new", ui.t("New group chat"), create);
        LinearLayout.LayoutParams gap = new LinearLayout.LayoutParams(-1, -2); gap.topMargin = ui.dp(10); room.addView(add, gap);
        for (int i = 0; i < model.groups.length(); i++) {
            JSONObject group = model.groups.optJSONObject(i); String id = group.optString("id"); List<JSONObject> members = new ArrayList<>(); JSONArray ids = group.optJSONArray("members"); if (ids != null) for (int j = 0; j < ids.length(); j++) members.add(person.apply(ids.optString(j)));
            section(true).addView(button(NativeAvatars.name(person.apply(id)), NativeAvatarStack.group(ui, members), model.unread.getOrDefault(id, 0), id, () -> select.accept(id)), new LinearLayout.LayoutParams(-1, -2));
        }
        for (int i = 0; i < online.length(); i++) {
            JSONObject presence = online.optJSONObject(i); String id = presence.optString("id"); JSONObject identity = person.apply(id); boolean own = id.equals(self);
            LinearLayout row = ui.row(); row.setPadding(0, ui.dp(9), 0, ui.dp(9)); item(row, ui.avatar(identity, 26), 10);
            View dot = new View(ui.context); dot.setBackground(ui.shape(presence.optString("state").equals("online") ? 0xff7a9b64 : 0xffc1b487, 999, 0)); LinearLayout.LayoutParams point = new LinearLayout.LayoutParams(ui.dp(5), ui.dp(5)); point.setMarginStart(ui.dp(10)); row.addView(dot, point);
            View name = own ? label(NativeAvatars.name(identity), 10, ui.text) : button(NativeAvatars.name(identity), null, model.unread.getOrDefault(id, 0), id, () -> select.accept(id));
            if (!own) { item((LinearLayout) name, ui.decorativeIcon("message", 13), 7); name.setContentDescription(ui.t("Chat with {name}", "name", NativeAvatars.name(identity))); }
            LinearLayout copy = ui.row(); copy.addView(name, new LinearLayout.LayoutParams(-2, -2));
            LinearLayout.LayoutParams title = new LinearLayout.LayoutParams(0, -2, 1); title.setMarginStart(ui.dp(10)); row.addView(copy, title);
            item(row, label(ui.t(presence.optString("state").equals("online") ? "Online" : "Away"), 9, ui.subtle), 10);
            content.addView(row, new LinearLayout.LayoutParams(-1, -2)); rows.add(row);
        }
        if (online.length() == 0) content.addView(label(ui.t("Updating presence…"), 9, ui.subtle));
        for (View option : options) if (focused.equals(String.valueOf(option.getTag()))) option.requestFocus();
    }
    void show(View anchor) { popover.showOnline(anchor); }
    boolean isShowing() { return popover.isShowing(); }
    void dismiss() { popover.dismiss(); }
}
