package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.TextView;
import java.util.List;
import org.json.JSONObject;

/** Same overlap, order, border, and three-person cap as Web AvatarStack. */
final class NativeAvatarStack extends FrameLayout {
    enum Style { NORMAL, COMPACT, GROUP }

    NativeAvatarStack(NativeUi ui, List<JSONObject> people, boolean compact) {
        this(ui, people, compact ? Style.COMPACT : Style.NORMAL);
    }

    NativeAvatarStack(NativeUi ui, List<JSONObject> people, Style style) {
        super(ui.context);
        boolean compact = style == Style.COMPACT, group = style == Style.GROUP;
        int visible = Math.min(3, people.size()), size = compact ? 16 : group ? 20 : 28;
        int remaining = people.size() - visible, step = compact ? 6 : group ? 8 : 19;
        int width = visible == 0 ? 0 : size + (visible - 1) * step;
        if (remaining > 0) {
            TextView more = ui.text("+" + remaining, compact || group ? 8 : 9, ui.muted);
            if (compact) { ui.typography(more, 8, ui.muted, 600); ui.fixedLineHeight(more, 1); ui.preciseText(more); }
            more.setGravity(Gravity.CENTER);
            more.setBackground(ui.shape(ui.soft, 999, compact ? ui.sidebar : ui.elevated));
            int moreSize = compact ? Math.max(20, Math.round(more.getPaint().getRunAdvance(more.getText(), 0, more.length(), 0, more.length(), false, more.length()) / getResources().getDisplayMetrics().density) + 8) : group ? 18 : size;
            int start = width + (group ? 1 : compact ? -3 : -9);
            FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(moreSize), ui.dp(group ? 18 : size), Gravity.CENTER_VERTICAL);
            params.leftMargin = ui.dp(start);
            addView(more, params);
            width = start + moreSize;
        }
        for (int i = visible - 1; i >= 0; i--) {
            FrameLayout border = new FrameLayout(ui.context);
            border.setBackground(ui.shape(compact ? ui.sidebar : ui.elevated, 999, 0));
            int inset = compact ? (visible == 1 ? 0 : 1) : group ? 1 : 2;
            ImageView avatar = ui.avatar(people.get(i), size - 2 * inset);
            border.addView(avatar, new FrameLayout.LayoutParams(ui.dp(size - 2 * inset), ui.dp(size - 2 * inset), Gravity.CENTER));
            FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(size), ui.dp(size));
            params.leftMargin = ui.dp(i * step);
            addView(border, params);
        }
        setLayoutParams(new android.widget.LinearLayout.LayoutParams(ui.dp(width), ui.dp(size)));
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
    }

    static FrameLayout group(NativeUi ui, List<JSONObject> people) {
        return group(ui, people, 34);
    }

    static FrameLayout group(NativeUi ui, List<JSONObject> people, int fallbackSize) {
        FrameLayout wrapper = new FrameLayout(ui.context);
        if (people.isEmpty()) {
            wrapper.setBackground(ui.shape(ui.soft, fallbackSize == 30 ? 10 : 11, 0));
            android.view.View icon = ui.decorativeIcon("people", 18);
            if (icon instanceof ImageView) ((ImageView) icon).setColorFilter(ui.primary);
            wrapper.addView(icon, new FrameLayout.LayoutParams(ui.dp(18), ui.dp(18), Gravity.CENTER));
            wrapper.setLayoutParams(new android.widget.LinearLayout.LayoutParams(ui.dp(fallbackSize), ui.dp(fallbackSize)));
        } else {
            NativeAvatarStack stack = new NativeAvatarStack(ui, people, Style.GROUP);
            wrapper.addView(stack, new FrameLayout.LayoutParams(stack.getLayoutParams().width, ui.dp(20), Gravity.CENTER));
            wrapper.setLayoutParams(new android.widget.LinearLayout.LayoutParams(stack.getLayoutParams().width, ui.dp(30)));
        }
        wrapper.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        return wrapper;
    }
}
