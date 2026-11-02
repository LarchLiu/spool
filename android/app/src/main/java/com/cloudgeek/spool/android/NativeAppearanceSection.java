package com.cloudgeek.spool.android;

import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.BiConsumer;

/** The same theme fieldset and language choice used by Web AppearanceDialog. */
final class NativeAppearanceSection extends LinearLayout {
    final LinearLayout themes;
    final NativeSelect language;

    NativeAppearanceSection(NativeUi ui, String theme, String selectedLanguage,
            BiConsumer<String, String> change) {
        super(ui.context);
        setOrientation(VERTICAL);
        addView(legend(ui, ui.t("Theme")));
        addView(ui.space(10));
        themes = ui.row();
        themes.setBaselineAligned(false);
        String[] values = {"light", "dark", "system"};
        String[] labels = {ui.t("Light"), ui.t("Dark"), ui.t("Follow system")};
        String[] icons = {"sun", "moon", "monitor"};
        for (int i = 0; i < values.length; i++) {
            String value = values[i];
            LayoutParams params = new LayoutParams(0, -1, 1);
            if (i > 0) params.setMarginStart(ui.dp(9));
            themes.addView(ui.themeOption(labels[i], icons[i], theme.equals(value),
                    () -> change.accept(value, selectedLanguage)), params);
        }
        addView(themes);
        addView(ui.space(22));
        addView(legend(ui, ui.t("Language")));
        addView(ui.space(10));
        language = new NativeSelect(ui, ui.t("Language"), selectedLanguage,
                new String[] {"system", "zh-CN", "en", "ja"},
                new String[] {ui.t("Follow system"), "简体中文", "English", "日本語"},
                true, value -> change.accept(theme, value));
        addView(language, new LayoutParams(-1, -2));
    }

    private TextView legend(NativeUi ui, String title) {
        TextView text = ui.text(title, 12, ui.text);
        ui.typography(text, 12, ui.text, 500);
        ui.fixedLineHeight(text, 1.2f);
        return text;
    }

    void saving(boolean saving) {
        for (int i = 0; i < themes.getChildCount(); i++) {
            themes.getChildAt(i).setEnabled(!saving);
            themes.getChildAt(i).setAlpha(saving ? .45f : 1);
        }
        language.setEnabled(!saving);
    }
}
