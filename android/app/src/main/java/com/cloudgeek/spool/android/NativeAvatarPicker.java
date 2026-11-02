package com.cloudgeek.spool.android;

import android.graphics.drawable.GradientDrawable;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.method.LinkMovementMethod;
import android.text.style.ClickableSpan;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.List;
import java.util.function.Consumer;
import org.json.JSONObject;

/** Native counterpart of Web AvatarDialog; images always come from the Go renderer. */
final class NativeAvatarPicker extends NativeDialog {
    interface Save { void save(JSONObject avatar, Consumer<String> completion); }
    private final JSONObject person;
    private final Save save;
    private final Consumer<String> link;
    private final String initialStyle, initialSeed;
    private final Button saveButton, cancel;
    private String style, seed;
    private int page;
    private boolean saving;

    NativeAvatarPicker(NativeUi ui, JSONObject person, Save save, Consumer<String> link) {
        super(ui, ui.label("Choose your avatar", "选择你的头像"), ui.label("Close avatar picker", "关闭头像选择器"));
        this.person = person;
        this.save = save;
        this.link = link;
        JSONObject config = NativeAvatars.config(person);
        initialStyle = style = config.optString("style");
        initialSeed = seed = config.optString("seed");
        cancel = ui.quietButton(ui.label("Cancel", "取消"), this::dismiss);
        actions.addView(cancel);
        saveButton = ui.button(ui.label("Save avatar", "保存头像"), true, this::save);
        saveButton.setPadding(ui.dp(18), ui.dp(10), ui.dp(18), ui.dp(10));
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, -2);
        params.setMarginStart(ui.dp(16));
        actions.addView(saveButton, params);
        render();
    }

    private void space(int height) { body.addView(ui.space(height)); }

    private View avatar(String style, String seed, int size) {
        return ui.avatar(NativeAvatars.withAvatar(person, style, seed), size);
    }

    private void choose(String nextStyle, String nextSeed) {
        style = nextStyle;
        seed = nextSeed;
        render();
    }

    private void selection(View view, boolean selected, String label, Runnable click) {
        view.setSelected(selected);
        view.setEnabled(!saving);
        view.setAlpha(saving ? .5f : 1);
        view.setFocusable(true);
        view.setContentDescription(label);
        if (android.os.Build.VERSION.SDK_INT >= 30)
            view.setStateDescription(selected ? ui.label("Selected", "已选择") : ui.label("Not selected", "未选择"));
        view.setOnClickListener(v -> click.run());
    }

    private void render() {
        body.removeAllViews();
        LinearLayout preview = ui.row();
        preview.setPadding(ui.dp(17), ui.dp(17), ui.dp(17), ui.dp(17));
        GradientDrawable gradient = new GradientDrawable(GradientDrawable.Orientation.TL_BR, new int[] {ui.soft, ui.elevated});
        gradient.setCornerRadius(ui.dp(14));
        gradient.setStroke(ui.dp(1), ui.line);
        preview.setBackground(gradient);
        preview.addView(avatar(style, seed, 80));
        LinearLayout description = ui.column();
        TextView name = ui.bold(NativeAvatars.name(person), 16);
        ui.typography(name, 16, ui.text, 700);
        description.addView(name);
        TextView hint = ui.text(seed.isEmpty() ? ui.label("Generated from your name", "根据你的名字生成") : ui.label("Picked by you", "由你选择"), 11, ui.muted);
        LinearLayout.LayoutParams hintParams = new LinearLayout.LayoutParams(-1, -2);
        hintParams.topMargin = ui.dp(7);
        description.addView(hint, hintParams);
        LinearLayout.LayoutParams descriptionParams = new LinearLayout.LayoutParams(0, -2, 1);
        descriptionParams.setMarginStart(ui.dp(18));
        preview.addView(description, descriptionParams);
        body.addView(preview);
        space(24);
        TextView legend = ui.text(ui.label("Avatar style", "头像风格"), 12, ui.text);
        ui.typography(legend, 12, ui.text, 500);
        body.addView(legend);
        space(10);
        for (int r = 0; r < 2; r++) {
            LinearLayout row = ui.row();
            row.setGravity(Gravity.TOP);
            row.setBaselineAligned(false);
            for (int c = 0; c < 4; c++) {
                int index = r * 4 + c;
                String value = NativeAvatarCatalog.STYLES[index];
                String label = NativeAvatarCatalog.LABELS[index];
                LinearLayout option = ui.padded(4, 0);
                option.setPadding(ui.dp(5), ui.dp(13), ui.dp(5), ui.dp(10));
                option.setGravity(Gravity.CENTER_HORIZONTAL);
                boolean selected = value.equals(style);
                option.setBackground(ui.states(selected ? ui.soft : ui.input, 10, selected ? ui.primary : ui.line, ui.hover, ui.input));
                option.addView(avatar(value, "", 38));
                TextView text = ui.text(label, 11, ui.text);
                text.setGravity(Gravity.CENTER);
                LinearLayout.LayoutParams labelParams = new LinearLayout.LayoutParams(-1, -2);
                labelParams.topMargin = ui.dp(8);
                option.addView(text, labelParams);
                selection(option, selected, label, () -> choose(value, seed));
                LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(0, -1, 1);
                if (c > 0) params.setMarginStart(ui.dp(8));
                row.addView(option, params);
            }
            body.addView(row);
            if (r == 0) space(8);
        }
        space(16);
        LinearLayout automatic = ui.row();
        automatic.setPadding(ui.dp(11), ui.dp(11), ui.dp(11), ui.dp(11));
        automatic.setBackground(ui.states(ui.input, 10, seed.isEmpty() ? ui.primary : ui.line, ui.hover, ui.input));
        automatic.addView(avatar(style, "", 36));
        LinearLayout automaticCopy = ui.column();
        automaticCopy.addView(ui.text(ui.label("Use my name", "使用我的名字"), 12, ui.text));
        TextView automaticHint = ui.text(ui.label("Updates automatically when your name changes", "名字更改时自动更新"), 10, ui.muted);
        LinearLayout.LayoutParams automaticHintParams = new LinearLayout.LayoutParams(-1, -2);
        automaticHintParams.topMargin = ui.dp(4);
        automaticCopy.addView(automaticHint, automaticHintParams);
        LinearLayout.LayoutParams copyParams = new LinearLayout.LayoutParams(0, -2, 1);
        copyParams.setMarginStart(ui.dp(10));
        automatic.addView(automaticCopy, copyParams);
        if (seed.isEmpty()) automatic.addView(ui.decorativeIcon("check", 16));
        selection(automatic, seed.isEmpty(), ui.label("Use my name", "使用我的名字"), () -> choose(style, ""));
        body.addView(automatic);
        space(20);
        LinearLayout gallery = ui.row();
        gallery.addView(ui.text(ui.label("Or pick one you like", "或选择喜欢的头像"), 12, ui.text), new LinearLayout.LayoutParams(0, -2, 1));
        Button more = ui.quietButton(ui.label("More avatars", "更多头像"), () -> { page++; render(); });
        ui.typography(more, 11, ui.muted, 400);
        more.setPadding(ui.dp(6), ui.dp(6), 0, ui.dp(6));
        ui.setButtonIcon(more, "refresh", 13, false);
        more.setEnabled(!saving);
        gallery.addView(more);
        body.addView(gallery);
        space(10);
        int columns = ui.context.getResources().getDisplayMetrics().widthPixels <= ui.dp(400) ? 4 : 6;
        List<String> choices = NativeAvatars.choices(NativeAvatars.name(person), page);
        for (int r = 0; r < 12 / columns; r++) {
            LinearLayout row = ui.row();
            for (int c = 0; c < columns; c++) {
                int index = r * columns + c;
                String candidate = choices.get(index);
                boolean selected = seed.equals(candidate);
                FrameLayout option = new FrameLayout(ui.context);
                option.setPadding(0, ui.dp(8), 0, ui.dp(8));
                option.setBackground(ui.states(selected ? ui.soft : ui.input, 12, selected ? ui.primary : ui.line, ui.hover, ui.input));
                option.addView(avatar(style, candidate, 48), new FrameLayout.LayoutParams(ui.dp(48), ui.dp(48), Gravity.CENTER));
                if (selected) {
                    View check = ui.decorativeIcon("check", 13);
                    check.setBackground(ui.shape(ui.soft, 999, 0));
                    FrameLayout.LayoutParams checkParams = new FrameLayout.LayoutParams(ui.dp(13), ui.dp(13), Gravity.BOTTOM | Gravity.END);
                    checkParams.setMarginEnd(ui.dp(2));
                    option.addView(check, checkParams);
                }
                selection(option, selected, ui.label("Avatar ", "头像 ") + (page * 12L + index + 1), () -> choose(style, candidate));
                LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(0, ui.dp(66), 1);
                if (c > 0) params.setMarginStart(ui.dp(7));
                row.addView(option, params);
            }
            body.addView(row);
            if (r < 12 / columns - 1) space(7);
        }
        space(18);
        TextView credit = ui.text("", 10, ui.muted);
        ui.lineHeight(credit, 1.8f);
        SpannableStringBuilder text = new SpannableStringBuilder(
                ui.t("Generated on your device. Your name is never sent to DiceBear.") + " ");
        appendLink(text, "DiceBear", "https://www.dicebear.com/");
        text.append("\n");
        String[] credits = NativeAvatarCatalog.CREDITS[NativeAvatarCatalog.index(style)];
        for (int i = 0; i < credits.length; i += 2) {
            if (i > 0) text.append(" / ");
            appendLink(text, credits[i], credits[i + 1]);
        }
        credit.setText(text);
        credit.setMovementMethod(LinkMovementMethod.getInstance());
        credit.setLinkTextColor(ui.muted);
        body.addView(credit);
        space(18);
        saveButton.setText(saving ? ui.label("Working…", "处理中…") : ui.label("Save avatar", "保存头像"));
        saveButton.setEnabled(!saving && (!style.equals(initialStyle) || !seed.equals(initialSeed)));
        cancel.setEnabled(!saving);
        lock(saving);
    }

    private void appendLink(SpannableStringBuilder text, String title, String target) {
        int start = text.length();
        text.append(title);
        text.setSpan(new ClickableSpan() {
            @Override public void onClick(View widget) { link.accept(target); }
        }, start, text.length(), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
    }

    private void save() {
        if (saving) return;
        saving = true;
        error.setVisibility(View.GONE);
        render();
        save.save(NativeAvatars.options(style, seed), message -> {
            if (!isShowing()) return;
            saving = false;
            if (message == null) dismiss();
            else {
                render();
                error.setText(message);
                error.setVisibility(View.VISIBLE);
            }
        });
    }
}
