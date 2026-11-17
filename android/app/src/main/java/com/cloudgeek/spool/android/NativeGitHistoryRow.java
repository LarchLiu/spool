package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.ReplacementSpan;
import android.view.View;
import android.widget.LinearLayout;
import org.json.JSONArray;
import org.json.JSONObject;

/** Custom history row with inline tag badges and an outgoing-commit icon. */
final class NativeGitHistoryRow extends LinearLayout {
    NativeGitHistoryRow(NativeUi ui, JSONObject commit, Runnable click) {
        super(ui.context); setOrientation(VERTICAL); setPadding(ui.dp(12), ui.dp(7), ui.dp(12), ui.dp(7));
        setBackground(ui.states(0, 5, 0, ui.selected, 0, ui.hover)); ui.focusOnPress(this); ui.buttonKeys(this);
        setOnClickListener(view -> click.run());
        String subject = commit.optString("subject"), id = commit.optString("id");
        NativeTextView heading = heading(ui, commit, subject);
        heading.setMaxLines(2); heading.setEllipsize(android.text.TextUtils.TruncateAt.END);
        heading.setOnClickListener(view -> performClick()); heading.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO); addView(heading, new LayoutParams(-1, -2));
        String stamp = commit.optLong("time") == 0 ? "" : " · " + java.text.DateFormat.getDateInstance(java.text.DateFormat.SHORT).format(new java.util.Date(commit.optLong("time") * 1000));
        NativeTextView metadata = new NativeTextView(ui.context); ui.typography(metadata, 10, ui.muted, 400); ui.fixedLineHeight(metadata, 1.6f);
        String detail = id.substring(0, Math.min(7, id.length())) + " · " + commit.optString("author") + stamp;
        metadata.setText(detail); metadata.setSingleLine(true); metadata.setEllipsize(android.text.TextUtils.TruncateAt.END);
        metadata.setOnClickListener(view -> performClick()); metadata.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO); addView(metadata, new LayoutParams(-1, -2));
        setContentDescription(heading.getContentDescription() + " · " + detail); setTooltipText(heading.getContentDescription());
        setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button");
            }
        });
    }
    static NativeTextView heading(NativeUi ui, JSONObject commit, String prefix) {
        SpannableStringBuilder title = new SpannableStringBuilder(prefix); java.util.List<String> description = new java.util.ArrayList<>();
        if (!prefix.isEmpty()) description.add(prefix);
        if (commit.optBoolean("unpushed")) { badge(title, new Badge(ui, null)); description.add(ui.t("Not pushed")); }
        JSONArray tags = commit.optJSONArray("tags");
        if (tags != null) for (int i = 0; i < tags.length(); i++) {
            String tag = tags.optString(i); badge(title, new Badge(ui, tag)); description.add(tag);
        }
        NativeTextView heading = new NativeTextView(ui.context); ui.typography(heading, 12, ui.text, 500); ui.fixedLineHeight(heading, 1.6f);
        heading.setText(title); heading.setContentDescription(String.join(" · ", description)); return heading;
    }
    private static void badge(SpannableStringBuilder text, Badge badge) {
        if (text.length() > 0) text.append(" "); int start = text.length(); text.append(badge.label == null ? "↑" : badge.label);
        text.setSpan(badge, start, text.length(), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
    }
    private static final class Badge extends ReplacementSpan {
        final NativeUi ui; final String label;
        Badge(NativeUi ui, String label) { this.ui = ui; this.label = label; }
        private Paint ink(Paint source) { Paint ink = new Paint(source); ink.setTextSize(ui.dp(10)); ink.setColor(ui.muted); return ink; }
        @Override public int getSize(Paint paint, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) {
            return Math.round((label == null ? ui.dp(11) : ink(paint).measureText(label)) + ui.dp(6));
        }
        @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int y, int bottom, Paint paint) {
            Paint ink = ink(paint); float width = getSize(paint, text, start, end, null), height = ui.dp(15);
            float center = y + (paint.ascent() + paint.descent()) / 2, at = center - height / 2;
            ink.setStyle(Paint.Style.STROKE); ink.setStrokeWidth(ui.dp(1)); ink.setColor(ui.borderStrong);
            canvas.drawRoundRect(x, at, x + width, at + height, ui.dp(3), ui.dp(3), ink);
            if (label == null) {
                android.graphics.drawable.Drawable icon = NativeIcons.drawable(ui.context, "arrow_up", ui.muted, ui.dp(11));
                int save = canvas.save(); canvas.translate(x + ui.dp(3), center - ui.dp(11) / 2f); icon.draw(canvas); canvas.restoreToCount(save);
            } else { ink.setStyle(Paint.Style.FILL); ink.setColor(ui.muted); canvas.drawText(label, x + ui.dp(3), center - (ink.ascent() + ink.descent()) / 2, ink); }
        }
    }
}
