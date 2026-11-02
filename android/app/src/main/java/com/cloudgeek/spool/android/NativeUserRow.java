package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.ImageView;
import java.util.function.Consumer;
import java.util.List;
import java.util.ArrayList;
import org.json.JSONArray;
import org.json.JSONObject;

/** Web user-row: content and footer together, then the 28px operator avatar. */
final class NativeUserRow extends ViewGroup {
    private final NativeUi ui;
    private ImageView avatar;
    final LinearLayout content;
    final NativeMessage message;
    NativeAttachmentCards attachments;
    private final View attachmentGap;
    NativeUserRow(NativeUi ui, JSONObject operator, String text, long timestamp,
            NativeAttachmentCards attachments, Consumer<String> open) {
        super(ui.context);
        this.ui = ui;
        this.attachments = attachments;
        attachmentGap = ui.space(9);
        setClipChildren(false);
        content = ui.column();
        content.setClipChildren(false);
        content.setGravity(Gravity.END);
        addView(content, new LayoutParams(-2, -2));
        if (attachments != null) {
            content.addView(attachments, new LinearLayout.LayoutParams(-2, -2));
            content.addView(attachmentGap);
        }
        message = new NativeMessage(ui, text, true, operator == null ? "" : NativeAvatars.name(operator),
                timestamp, ui.chinese, open);
        message.maximumFraction = 1;
        content.addView(message, new LinearLayout.LayoutParams(-2, -2));
        bindProfile(operator);
    }

    void bind(JSONObject operator, String text, long timestamp, List<NativeAttachmentCards.Item> files) {
        message.bindSource(text); message.footer.bindTimestamp(timestamp); bindProfile(operator);
        List<View> children = new ArrayList<>();
        if (files.isEmpty()) {
            if (attachments != null) attachments.retire();
            attachments = null;
        } else {
            if (attachments == null) attachments = new NativeAttachmentCards(ui, true);
            attachments.bind(files); children.add(attachments); children.add(attachmentGap);
        }
        children.add(message); NativeKeyedViews.sync(content, children);
    }

    void bindProfile(JSONObject operator) {
        String name = operator == null ? "" : NativeAvatars.name(operator);
        message.footer.bindAuthor(name);
        setContentDescription(name);
        if (operator == null) {
            if (avatar != null) { removeView(avatar); avatar = null; }
        } else if (avatar == null) {
            avatar = ui.avatar(operator, 28);
            addView(avatar, new LayoutParams(ui.dp(28), ui.dp(28)));
        } else if (ui.avatars != null) ui.avatars.bind(avatar, ui, operator, 28);
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int available = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED ? ui.dp(680) : MeasureSpec.getSize(widthSpec);
        int avatarWidth = avatar == null ? 0 : ui.dp(38);
        int maximum = Math.max(0, Math.min(ui.dp(680), Math.min(Math.round(available * .9f), available - avatarWidth)));
        content.measure(MeasureSpec.makeMeasureSpec(maximum, MeasureSpec.AT_MOST), heightSpec);
        if (avatar != null) avatar.measure(MeasureSpec.makeMeasureSpec(ui.dp(28), MeasureSpec.EXACTLY),
                MeasureSpec.makeMeasureSpec(ui.dp(28), MeasureSpec.EXACTLY));
        setMeasuredDimension(resolveSize(content.getMeasuredWidth() + avatarWidth, widthSpec),
                resolveSize(Math.max(content.getMeasuredHeight(), avatar == null ? 0 : ui.dp(34)), heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int edge = getWidth() - (avatar == null ? 0 : ui.dp(38));
        content.layout(edge - content.getMeasuredWidth(), 0, edge, content.getMeasuredHeight());
        if (avatar != null) avatar.layout(getWidth() - ui.dp(28), ui.dp(6), getWidth(), ui.dp(34));
    }

    static boolean hasContent(String text, JSONArray files, JSONArray parts, int start, int end) {
        if (!text.trim().isEmpty() || files.length() > 0) return true;
        for (int i = Math.max(0, start); i < Math.min(parts.length(), end); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && part.optString("role").equals("user") && part.optString("kind").equals("image")
                    && !part.optString("image_url").isEmpty()) return true;
        }
        return false;
    }

    static String text(JSONArray parts, int start, int end) {
        StringBuilder text = new StringBuilder();
        boolean first = true;
        for (int i = Math.max(0, start); i < Math.min(parts.length(), end); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part == null || !part.optString("role").equals("user") || !part.optString("kind").equals("text")) continue;
            if (!first) text.append('\n');
            text.append(part.optString("text"));
            first = false;
        }
        return text.toString();
    }
}
