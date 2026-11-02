package com.cloudgeek.spool.android;

import android.graphics.drawable.GradientDrawable;
import android.view.Gravity;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.text.DateFormat;
import java.util.Date;
import java.util.Locale;
import org.json.JSONObject;

/** Plain-text people message: original 28px author avatar, asymmetric bubble and local time. */
final class NativePeopleMessage extends LinearLayout {
    final TextView text, time;
    final LinearLayout entry, bubble;
    private final NativeUi ui;
    @android.annotation.SuppressLint("InlinedApi") // The constant is inlined into TextView's API-23 setter.
    NativePeopleMessage(NativeUi ui, JSONObject message, JSONObject author, boolean own) {
        super(ui.context); this.ui = ui;
        setGravity(Gravity.TOP); setContentDescription(NativeAvatars.name(author));
        entry = ui.column(); entry.setGravity(own ? Gravity.END : Gravity.START);
        bubble = ui.column(); bubble.setPadding(ui.dp(10), ui.dp(8), ui.dp(10), ui.dp(8));
        GradientDrawable background = ui.shape(own ? new NativePeoplePalette(ui.dark).own : ui.soft, 12, 0);
        float small = ui.dp(3), large = ui.dp(12); background.setCornerRadii(new float[] {large,large,large,large,own?small:large,own?small:large,own?large:small,own?large:small}); bubble.setBackground(background);
        text = ui.text(message.optString("text"), 13, ui.text); ui.fixedLineHeight(text, 1.65f); ui.preciseText(text); text.setTextIsSelectable(true);
        text.setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
        bubble.addView(text, new LayoutParams(-2, -2)); entry.addView(bubble, new LayoutParams(-2, -2));
        Locale locale = ui.locale.equals("zh-CN") ? Locale.SIMPLIFIED_CHINESE : ui.locale.equals("ja") ? Locale.JAPANESE : Locale.US;
        DateFormat format = DateFormat.getTimeInstance(DateFormat.SHORT, locale);
        if (format instanceof java.text.SimpleDateFormat) { java.text.SimpleDateFormat simple = (java.text.SimpleDateFormat) format; simple.applyPattern(simple.toPattern().replaceAll("(?<![hHKk])[hHKk](?![hHKk])", "$0$0")); }
        time = ui.text(format.format(new Date(message.optLong("sent_at"))), 9, ui.muted); ui.fixedLineHeight(time, 10f / 9); ui.preciseText(time); time.setPadding(ui.dp(2), 0, ui.dp(2), 0);
        LayoutParams stamp = new LayoutParams(-2, -2); stamp.topMargin = ui.dp(4); entry.addView(time, stamp);
        android.widget.ImageView avatar = ui.avatar(author, 28); LayoutParams photo = new LayoutParams(ui.dp(28), ui.dp(28)); photo.topMargin = ui.dp(2);
        if (own) { addView(entry); photo.setMarginStart(ui.dp(6)); addView(avatar, photo); }
        else { photo.setMarginEnd(ui.dp(6)); addView(avatar, photo); addView(entry); }
    }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int available = MeasureSpec.getSize(widthSpec);
        text.setMaxWidth(Math.max(1, available - ui.dp(28 + 6 + 20)));
        super.onMeasure(widthSpec, heightSpec);
    }
}
