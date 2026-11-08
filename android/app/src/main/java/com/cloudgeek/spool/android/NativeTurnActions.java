package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.TimeZone;
import org.json.JSONArray;
import org.json.JSONObject;

/** The same operator, request summary and decision time shown by Web TurnActions. */
final class NativeTurnActions extends LinearLayout {
    private final NativeUi ui;
    private Object bound;
    // Footer holders move between turns, including turns without any audit records.
    // Keep only a few detached rows; never retain a whole previous turn's view tree.
    private final ArrayDeque<Row> recycled = new ArrayDeque<>();

    NativeTurnActions(NativeUi ui) {
        super(ui.context); this.ui = ui;
        setOrientation(VERTICAL);
    }

    void bind(JSONArray values) {
        JSONArray actions = values == null ? new JSONArray() : values;
        JSONArray inputs = new JSONArray().put(actions);
        List<JSONObject> people = new ArrayList<>(actions.length());
        for (int i = 0; i < actions.length(); i++) {
            JSONObject action = actions.optJSONObject(i);
            JSONObject person = action == null || ui.people == null ? null : ui.people.apply(action.optString("user_id"));
            people.add(person); inputs.put(person);
        }
        if (NativeChatState.equivalent(bound, inputs)) return;
        bound = NativeChatState.snapshot(inputs);
        int count = 0;
        for (int i = 0; i < actions.length(); i++) {
            JSONObject action = actions.optJSONObject(i);
            if (action == null) continue;
            Row row = count < getChildCount() ? (Row) getChildAt(count) : recycled.pollFirst();
            if (row == null) row = new Row();
            row.bind(action, people.get(i));
            if (row.getParent() == null) {
                LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.topMargin = ui.dp(6);
                addView(row, params);
            }
            count++;
        }
        while (getChildCount() > count) {
            Row row = (Row) getChildAt(getChildCount() - 1);
            removeViewAt(getChildCount() - 1);
            if (recycled.size() < 4) recycled.addFirst(row);
        }
        int visibility = count == 0 ? GONE : VISIBLE;
        if (getVisibility() != visibility) setVisibility(visibility);
    }

    private final class Row extends LinearLayout {
        final ImageView avatar;
        final TextView text;
        Row() {
            super(ui.context); setGravity(Gravity.CENTER_VERTICAL);
            avatar = NativeAvatars.placeholder(ui, 18);
            addView(avatar, new LinearLayout.LayoutParams(ui.dp(18), ui.dp(18)));
            text = ui.text("", 11, ui.muted);
            text.setFontFeatureSettings("tnum"); ui.fixedLineHeight(text, 1.5f);
            LinearLayout.LayoutParams content = new LinearLayout.LayoutParams(0, -2, 1); content.setMarginStart(ui.dp(7));
            addView(text, content);
        }

        void bind(JSONObject action, JSONObject person) {
            String id = action.optString("user_id"), kind = action.optString("kind");
            String name = person == null ? id : person.optString("nickname", id);
            String key = kind.equals("stop") ? "Stopped by {name}" : kind.equals("approve") ? "Approved by {name}" : kind.equals("respond") ? "Answered by {name}" : kind.equals("cancel") ? "Cancelled by {name}" : "Denied by {name}";
            String label = ui.t(key, "name", name), summary = action.optString("summary");
            if (person != null && ui.avatars != null) ui.avatars.bind(avatar, ui, person, 18);
            else if (avatar.getTag() != null || avatar.getDrawable() != null) {
                // Invalidate any asynchronous decode belonging to the previous operator.
                avatar.setTag(null); avatar.setImageDrawable(null);
            }
            String description = label + (summary.isEmpty() ? "" : " · " + summary);
            NativeMessageTime formatted = NativeMessageTime.format(action.optLong("created_at"), ui.locale, System.currentTimeMillis(), TimeZone.getDefault());
            String value = description + (formatted == null ? "" : "  " + formatted.label.replace(' ', '\u00a0'));
            if (!value.contentEquals(text.getText())) text.setText(value);
            String accessible = description + (formatted == null ? "" : " " + formatted.title);
            if (!accessible.equals(text.getContentDescription())) text.setContentDescription(accessible);
        }
    }
}
