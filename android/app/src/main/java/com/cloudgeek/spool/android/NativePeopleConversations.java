package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONObject;

/** Web's 72px people/group rail, shared by all native people-chat conversations. */
final class NativePeopleConversations extends LinearLayout {
    final LinearLayout heading, items;
    final ScrollView scroll;
    private final NativeUi ui;
    private final Consumer<String> select;
    private final Paint border = new Paint();

    NativePeopleConversations(NativeUi ui, Consumer<String> select, Runnable createGroup) {
        super(ui.context); this.ui = ui; this.select = select;
        setOrientation(VERTICAL); setBackgroundColor(ui.sidebar); setPadding(0, 0, ui.dp(1), 0); setWillNotDraw(false);
        setContentDescription(ui.t("People & groups"));
        heading = ui.row(); heading.setGravity(Gravity.CENTER); heading.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        heading.addView(ui.icon("plus", 15, ui.t("New group chat"), createGroup)); addView(heading, new LayoutParams(-1, ui.dp(40)));
        items = ui.column(); items.setPadding(ui.dp(4), ui.dp(2), ui.dp(4), ui.dp(8));
        scroll = new ScrollView(ui.context); scroll.setVerticalScrollBarEnabled(false); scroll.addView(items);
        scroll.setContentDescription(ui.t("Choose conversation")); addView(scroll, new LayoutParams(-1, 0, 1));
    }

    void bind(String user, NativePeopleModel model, JSONArray online, boolean connected, Function<String, JSONObject> person) {
        String focused = "";
        for (int i = 0; i < items.getChildCount(); i++) if (items.getChildAt(i).hasFocus()) focused = String.valueOf(items.getChildAt(i).getTag());
        items.removeAllViews();
        NativePeoplePalette palette = new NativePeoplePalette(ui.dark);
        List<String> ids = model.conversations(user, online);
        for (String id : ids) {
            JSONObject identity = person.apply(id); boolean group = NativePeopleModel.group(id), selected = model.active.equals(id);
            String state = connected ? NativePeopleModel.status(online, id) : "disconnected";
            String label = ui.t(!connected ? "Reconnecting…" : state.equals("online") ? "Online" : state.equals("away") ? "Away" : "Offline");
            FrameLayout row = new FrameLayout(ui.context);
            row.setClipToPadding(false);
            row.setTag(id); row.setFocusable(true); row.setSelected(selected); row.setContentDescription(NativeAvatars.name(identity) + (group ? "" : " (" + label + ")"));
            row.setBackground(ui.states(selected ? palette.selection : 0, 9, selected ? palette.border : 0, ui.controlHover, 0));
            row.setPadding(ui.dp(4), ui.dp(9), ui.dp(4), ui.dp(9));
            LinearLayout content = ui.column(); content.setGravity(Gravity.CENTER_HORIZONTAL);
            if (group) {
                List<JSONObject> members = new ArrayList<>(); JSONObject info = model.groupInfo(id);
                JSONArray memberIDs = info == null ? null : info.optJSONArray("members");
                if (memberIDs != null) for (int i = 0; i < memberIDs.length(); i++) members.add(person.apply(memberIDs.optString(i)));
                content.addView(NativeAvatarStack.group(ui, members, 30));
            } else {
                FrameLayout avatar = new FrameLayout(ui.context); avatar.setClipChildren(false);
                avatar.addView(ui.avatar(identity, 30), new FrameLayout.LayoutParams(ui.dp(30), ui.dp(30)));
                View dot = new View(ui.context); dot.setBackground(ui.shape(state.equals("online") ? 0xff7a9b64 : state.equals("away") ? 0xffc1b487 : ui.subtle, 999, 0));
                FrameLayout ring = new FrameLayout(ui.context); ring.setBackground(ui.shape(ui.sidebar, 999, 0));
                ring.addView(dot, new FrameLayout.LayoutParams(ui.dp(6), ui.dp(6), Gravity.CENTER));
                FrameLayout.LayoutParams position = new FrameLayout.LayoutParams(ui.dp(10), ui.dp(10), Gravity.TOP | Gravity.END); position.topMargin = -ui.dp(1); position.rightMargin = -ui.dp(1); avatar.addView(ring, position);
                content.addView(avatar, new LayoutParams(ui.dp(30), ui.dp(30)));
            }
            TextView name = ui.text(NativeAvatars.name(identity), 10, selected ? ui.primary : ui.text); ui.fixedLineHeight(name, 1.1f);
            name.setSingleLine(true); name.setEllipsize(android.text.TextUtils.TruncateAt.END); name.setGravity(Gravity.CENTER);
            LayoutParams copy = new LayoutParams(-1, -2); copy.topMargin = ui.dp(5); content.addView(name, copy);
            row.addView(content, new FrameLayout.LayoutParams(-1, -2));
            int unread = model.unread.getOrDefault(id, 0);
            if (unread > 0) { TextView badge = ui.text(NativePeopleModel.badge(unread), 9, 0xffffffff); ui.typography(badge, 9, 0xffffffff, 600); badge.setGravity(Gravity.CENTER); badge.setMinWidth(ui.dp(17)); badge.setPadding(ui.dp(4), 0, ui.dp(4), 0); badge.setBackground(ui.shape(ui.primary, 10, 0)); FrameLayout.LayoutParams at = new FrameLayout.LayoutParams(-2, ui.dp(17)); at.leftMargin = -ui.dp(1); at.topMargin = -ui.dp(6); row.addView(badge, at); }
            row.setOnClickListener(view -> select.accept(id));
            row.setOnKeyListener((view, key, event) -> {
                if (event.getAction() != KeyEvent.ACTION_DOWN) return false;
                int count = items.getChildCount(), index = items.indexOfChild(row), next = key == KeyEvent.KEYCODE_DPAD_DOWN ? (index + 1) % count : key == KeyEvent.KEYCODE_DPAD_UP ? (index - 1 + count) % count : key == KeyEvent.KEYCODE_MOVE_HOME ? 0 : key == KeyEvent.KEYCODE_MOVE_END ? count - 1 : -1;
                if (next < 0) return false; items.getChildAt(next).requestFocus(); return true;
            });
            row.setAccessibilityDelegate(new View.AccessibilityDelegate() {
                @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) { super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button"); info.setSelected(selected); }
            });
            LayoutParams position = new LayoutParams(-1, -2); if (items.getChildCount() > 0) position.topMargin = ui.dp(3); items.addView(row, position);
            if (focused.equals(id)) row.requestFocus();
        }
    }
    @Override protected void onDraw(Canvas canvas) { super.onDraw(canvas); border.setColor(ui.line); canvas.drawRect(getWidth() - ui.dp(1), 0, getWidth(), getHeight(), border); }
}
