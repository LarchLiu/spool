package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.TimeZone;
import org.json.JSONArray;
import org.json.JSONObject;

/** The same operator, request summary and decision time shown by Web TurnActions. */
final class NativeTurnActions extends LinearLayout {
    private final NativeUi ui;
    private Object bound;

    NativeTurnActions(NativeUi ui) {
        super(ui.context); this.ui = ui;
        setOrientation(VERTICAL);
    }

    void bind(JSONArray values) {
        JSONArray actions = values == null ? new JSONArray() : values;
        JSONArray inputs = new JSONArray().put(actions);
        for (int i = 0; i < actions.length(); i++) {
            JSONObject action = actions.optJSONObject(i);
            if (action != null && ui.people != null) inputs.put(ui.people.apply(action.optString("user_id")));
        }
        if (NativeChatState.equivalent(bound, inputs)) return;
        bound = NativeChatState.snapshot(inputs);
        removeAllViews();
        setVisibility(actions.length() == 0 ? GONE : VISIBLE);
        for (int i = 0; i < actions.length(); i++) {
            JSONObject action = actions.optJSONObject(i);
            if (action == null) continue;
            String id = action.optString("user_id"), kind = action.optString("kind");
            JSONObject person = ui.people == null ? null : ui.people.apply(id);
            String name = person == null ? id : person.optString("nickname", id);
            String key = kind.equals("stop") ? "Stopped by {name}" : kind.equals("approve") ? "Approved by {name}" : kind.equals("respond") ? "Answered by {name}" : kind.equals("cancel") ? "Cancelled by {name}" : "Denied by {name}";
            String label = ui.t(key, "name", name), summary = action.optString("summary");
            LinearLayout row = new LinearLayout(ui.context); row.setGravity(Gravity.CENTER_VERTICAL);
            View avatar = ui.avatar(person, 18); row.addView(avatar, new LinearLayout.LayoutParams(ui.dp(18), ui.dp(18)));
            String description = label + (summary.isEmpty() ? "" : " · " + summary);
            NativeMessageTime formatted = NativeMessageTime.format(action.optLong("created_at"), ui.locale, System.currentTimeMillis(), TimeZone.getDefault());
            TextView text = ui.text(description + (formatted == null ? "" : "  " + formatted.label.replace(' ', '\u00a0')), 11, ui.muted);
            text.setFontFeatureSettings("tnum");
            text.setContentDescription(description + (formatted == null ? "" : " " + formatted.title));
            ui.fixedLineHeight(text, 1.5f);
            LinearLayout.LayoutParams content = new LinearLayout.LayoutParams(0, -2, 1); content.setMarginStart(ui.dp(7));
            row.addView(text, content);
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.topMargin = ui.dp(6);
            addView(row, params);
        }
    }
}
