package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.MotionEvent;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.List;
import org.json.JSONObject;

/** Compact shared operator avatars and the same full-name tooltip as the Web chat list. */
final class NativeChatOperators extends FrameLayout {
    final NativePopover popup;
    final List<JSONObject> people;
    private boolean hovered;
    private boolean active = true;
    private final Runnable close;

    NativeChatOperators(NativeUi ui, List<JSONObject> people) {
        super(ui.context); this.people = people;
        NativeAvatarStack stack = new NativeAvatarStack(ui, people, true);
        addView(stack, new FrameLayout.LayoutParams(stack.getLayoutParams().width, ui.dp(16)));
        setLayoutParams(new LinearLayout.LayoutParams(stack.getLayoutParams().width, ui.dp(16)));
        setFocusable(true); setBackground(ui.states(0, 20, 0, 0, 0));
        StringBuilder names = new StringBuilder();
        for (JSONObject person : people) {
            if (names.length() > 0) names.append(", ");
            names.append(NativeAvatars.name(person));
        }
        setContentDescription(names.toString());
        popup = new NativePopover(ui, 240, 12, 7, 16);
        close = () -> { if (!hovered && !hasFocus()) popup.dismiss(); };
        for (JSONObject person : people) {
            LinearLayout row = ui.row(); row.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6)); row.addView(ui.avatar(person, 28), new LinearLayout.LayoutParams(ui.dp(28), ui.dp(28)));
            TextView name = ui.text(NativeAvatars.name(person), 13, ui.text); ui.fixedLineHeight(name, 15f / 13); ui.preciseText(name);
            LinearLayout.LayoutParams copy = new LinearLayout.LayoutParams(0, -2, 1); copy.setMarginStart(ui.dp(9)); row.addView(name, copy); popup.content.addView(row, new LinearLayout.LayoutParams(-1, -2));
        }
        popup.content.setContentDescription(getContentDescription());
        popup.content.setOnHoverListener((view, event) -> { if (event.getActionMasked() == MotionEvent.ACTION_HOVER_ENTER) { hovered = true; removeCallbacks(close); } else if (event.getActionMasked() == MotionEvent.ACTION_HOVER_EXIT) { hovered = false; postDelayed(close, 150); } return false; });
        setOnClickListener(view -> show());
        setOnFocusChangeListener((view, focus) -> { if (focus) show(); else postDelayed(close, 150); });
        setOnHoverListener((view, event) -> { if (event.getActionMasked() == MotionEvent.ACTION_HOVER_ENTER) { hovered = true; show(); } else if (event.getActionMasked() == MotionEvent.ACTION_HOVER_EXIT) { hovered = false; postDelayed(close, 150); } return false; });
    }
    private void show() { removeCallbacks(close); if (active && !popup.isShowing()) popup.showOperators(this); }
    void deactivate() { active = false; removeCallbacks(close); popup.dismiss(); }
    @Override protected void onDetachedFromWindow() { deactivate(); super.onDetachedFromWindow(); }
}
