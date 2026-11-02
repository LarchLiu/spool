package com.cloudgeek.spool.android;

import android.text.InputFilter;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/** LoginStory + login-panel, using the Web mobile dimensions and shared form controls. */
final class NativeLogin extends FrameLayout {
    final EditText username, password;
    final TextView error;
    final Button submit;
    final LinearLayout story, brand, panel, form;
    final TextView headline, eyebrow, heading, description, usernameLabel, passwordLabel, help;
    final NativeUi.IconButton preferences;

    NativeLogin(NativeUi ui, String errorText, boolean busy, Runnable login, Runnable settings) {
        super(ui.context);
        setBackgroundColor(ui.surface);
        ScrollView scroll = new ScrollView(ui.context);
        scroll.setFillViewport(true);
        scroll.setVerticalScrollBarEnabled(false);
        LinearLayout page = ui.column();
        story = ui.padded(28, 25);
        story.setBackground(ui.storyBackground());
        brand = ui.row();
        brand.addView(ui.logo(32));
        TextView word = ui.bold("Spool", 16);
        ui.typography(word, 16, ui.text, 750);
        word.setLetterSpacing(-.5f / 16);
        LinearLayout.LayoutParams wordParams = new LinearLayout.LayoutParams(-2, -2);
        wordParams.setMarginStart(ui.dp(10));
        brand.addView(word, wordParams);
        story.addView(brand);
        story.addView(new View(ui.context), new LinearLayout.LayoutParams(1, 0, 1));
        TextView title = ui.bold(ui.t("Ideas, brought to life."), 36);
        this.headline = title;
        ui.typography(title, 36, ui.text, 550);
        ui.lineHeight(title, 1.25f);
        title.setLetterSpacing(-1f / 36);
        LinearLayout.LayoutParams headline = new LinearLayout.LayoutParams(-1, -2);
        headline.topMargin = ui.dp(25);
        headline.bottomMargin = ui.dp(10);
        story.addView(title, headline);
        page.addView(story, new LinearLayout.LayoutParams(-1, ui.dp(270)));
        panel = ui.padded(28, 35);
        panel.setPadding(ui.dp(28), ui.dp(35), ui.dp(28), ui.dp(50));
        form = new LinearLayout(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                super.onMeasure(MeasureSpec.makeMeasureSpec(Math.min(ui.dp(380), MeasureSpec.getSize(widthSpec)), MeasureSpec.EXACTLY), heightSpec);
            }
        };
        form.setOrientation(LinearLayout.VERTICAL);
        form.addView(ui.space(8));
        eyebrow = ui.eyebrow(ui.t("Welcome back"), 9, .13f); form.addView(eyebrow);
        form.addView(ui.space(15));
        heading = ui.bold(ui.t("Sign in to your workspace"), 24); ui.fixedLineHeight(heading, 28f / 24); form.addView(heading);
        form.addView(ui.space(15));
        description = ui.text(ui.t("Use the account created by your desktop administrator."), 12, ui.muted); form.addView(description);
        form.addView(ui.space(28));
        usernameLabel = ui.text(ui.t("Username"), 11, ui.muted); form.addView(usernameLabel);
        form.addView(ui.space(8));
        username = ui.field("", false);
        username.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD);
        username.setAutofillHints(View.AUTOFILL_HINT_USERNAME);
        username.setFilters(new InputFilter[] {new InputFilter.LengthFilter(64)});
        username.setContentDescription(ui.t("Username"));
        ui.typography(username, 11, ui.text, 400);
        form.addView(username, new LinearLayout.LayoutParams(-1, ui.dp(39)));
        form.addView(ui.space(20));
        passwordLabel = ui.text(ui.t("Password"), 11, ui.muted); form.addView(passwordLabel);
        form.addView(ui.space(8));
        password = ui.field("", true);
        password.setAutofillHints(View.AUTOFILL_HINT_PASSWORD);
        password.setFilters(new InputFilter[] {new InputFilter.LengthFilter(72)});
        password.setContentDescription(ui.t("Password"));
        ui.typography(password, 11, ui.text, 400);
        form.addView(password, new LinearLayout.LayoutParams(-1, ui.dp(39)));
        error = ui.text(errorText, 12, ui.error);
        error.setPadding(ui.dp(14), ui.dp(11), ui.dp(14), ui.dp(11));
        error.setBackground(ui.shape(ui.errorBg, 10, ui.errorBorder));
        error.setVisibility(errorText.isEmpty() ? GONE : VISIBLE);
        form.addView(error);
        form.addView(ui.space(28));
        submit = ui.button(ui.t(busy ? "Signing in…" : "Enter workspace"), true, login);
        submit.setGravity(Gravity.START | Gravity.CENTER_VERTICAL);
        submit.setPadding(ui.dp(17), ui.dp(13), ui.dp(17), ui.dp(13));
        ui.setButtonIcon(submit, "send", 17, true);
        submit.setEnabled(!busy);
        form.addView(submit, new LinearLayout.LayoutParams(-1, ui.dp(43)));
        form.addView(ui.space(18));
        help = ui.text(ui.t("Need an account? Contact your desktop administrator."), 10, ui.subtle); form.addView(help);
        // The final inline small element participates in its parent's 19px line box.
        form.addView(ui.space(8));
        LinearLayout.LayoutParams formParams = new LinearLayout.LayoutParams(-1, -2);
        formParams.gravity = Gravity.CENTER_HORIZONTAL;
        panel.addView(form, formParams);
        page.addView(panel);
        scroll.addView(page);
        addView(scroll, new FrameLayout.LayoutParams(-1, -1));
        preferences = ui.icon("settings", 20, ui.t("Settings"), settings);
        FrameLayout.LayoutParams top = new FrameLayout.LayoutParams(ui.dp(32), ui.dp(32), Gravity.TOP | Gravity.END);
        top.topMargin = ui.dp(20);
        top.setMarginEnd(ui.dp(20));
        addView(preferences, top);
    }
}
