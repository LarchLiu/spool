package com.cloudgeek.spool.android;

import android.text.Editable;
import android.text.TextWatcher;
import android.view.KeyEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Owns native slash completion without taking the EditText's focus or replacing its document. */
final class NativeComposerCommands {
    final TextView hint;
    final NativePopover menu;
    final NativeSlashCommandMenu option;
    NativeCommandNotice notice;
    TextView error;
    private final NativeUi ui;
    private final NativeUi.Composer composer;
    private boolean writable, posting, running, dismissed, hiding;
    private int handledKey;
    private String noticeText = "", errorText = "";

    NativeComposerCommands(NativeUi ui, NativeUi.Composer composer, Runnable submit) {
        this.ui = ui; this.composer = composer;
        hint = ui.text(ui.t("/goal <condition> · /goal to view · /goal clear to remove"), 11, ui.subtle);
        ui.normalFallbackLineSpacing(hint);
        hint.setPadding(ui.dp(19), 0, ui.dp(19), ui.dp(8)); hint.setVisibility(View.GONE);
        composer.box.addView(hint, composer.box.getChildCount() - 1, new LinearLayout.LayoutParams(-1, -2));
        menu = NativePopover.commands(ui);
        option = new NativeSlashCommandMenu(ui, this::select);
        menu.content.setContentDescription(ui.t("Chat commands"));
        menu.content.addView(option, new LinearLayout.LayoutParams(-1, -2));
        menu.dismissed = () -> { if (!hiding) dismissed = true; };
        composer.input.setFilters(new android.text.InputFilter[] {new android.text.InputFilter.LengthFilter(131072)});
        composer.input.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence text, int start, int count, int after) {}
            public void onTextChanged(CharSequence text, int start, int before, int count) { dismissed = false; refresh(); }
            public void afterTextChanged(Editable text) {}
        });
        composer.input.setOnFocusChangeListener((view, focus) -> refresh());
        composer.input.setOnKeyListener((view, key, event) -> {
            if (event.getAction() == KeyEvent.ACTION_UP && handledKey == key) { handledKey = 0; return true; }
            if (event.getAction() != KeyEvent.ACTION_DOWN || composing()) return false;
            boolean complete = menu.isShowing() && (key == KeyEvent.KEYCODE_TAB || key == KeyEvent.KEYCODE_ENTER
                    && !event.isMetaPressed() && !event.isAltPressed() && !event.isShiftPressed());
            boolean navigation = menu.isShowing() && (key == KeyEvent.KEYCODE_DPAD_DOWN || key == KeyEvent.KEYCODE_DPAD_UP);
            boolean escape = menu.isShowing() && key == KeyEvent.KEYCODE_ESCAPE;
            boolean send = key == KeyEvent.KEYCODE_ENTER && (event.isMetaPressed() || event.isAltPressed());
            if (!complete && !navigation && !escape && !send) return false;
            handledKey = key;
            if (event.getRepeatCount() != 0) return true;
            if (complete) select();
            else if (escape) { dismissed = true; hide(); }
            else if (send && writable && !posting && !running) submit.run();
            return true;
        });
        composer.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
            public void onViewAttachedToWindow(View view) { composer.post(NativeComposerCommands.this::refresh); }
            public void onViewDetachedFromWindow(View view) { hide(); }
        });
    }

    void bind(boolean writable, boolean posting, boolean running) {
        this.writable = writable; this.posting = posting; this.running = running; refresh();
    }

    private boolean composing() { return android.view.inputmethod.BaseInputConnection.getComposingSpanStart(composer.input.getText()) >= 0; }

    private void refresh() {
        String text = composer.input.getText().toString();
        hint.setVisibility(NativeGoalCommand.parse(text) == null ? View.GONE : View.VISIBLE);
        boolean show = writable && composer.input.hasFocus() && !posting && !running && !dismissed && NativeGoalCommand.suggestion(text);
        if (show && composer.box.isAttachedToWindow() && composer.box.getWidth() > 0) {
            if (!menu.isShowing()) menu.show(composer.box, true);
        } else hide();
    }

    void select() {
        composer.input.setText("/goal "); composer.input.setSelection(composer.input.length());
        dismissed = false; hide(); composer.input.requestFocus();
    }

    private void hide() {
        hiding = true;
        try { menu.dismiss(); } finally { hiding = false; }
    }

    void notice(String value, Runnable dismiss) {
        if (noticeText.equals(value)) return;
        noticeText = value;
        if (notice != null) { composer.removeView(notice); notice = null; }
        if (!value.isEmpty()) {
            notice = new NativeCommandNotice(ui, value, dismiss);
            LinearLayout.LayoutParams position = new LinearLayout.LayoutParams(-1, -2); position.bottomMargin = ui.dp(10);
            composer.addView(notice, error == null ? 0 : 1, position);
        }
    }

    void error(String value) {
        if (errorText.equals(value)) return;
        errorText = value;
        if (error != null) { composer.removeView(error); error = null; }
        if (!value.isEmpty()) {
            error = ui.text(value, 12, ui.error); ui.fixedLineHeight(error, 1.7f);
            error.setPadding(ui.dp(15), ui.dp(12), ui.dp(15), ui.dp(12));
            error.setBackground(ui.shape(ui.errorBg, 10, ui.errorBorder));
            error.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_ASSERTIVE);
            LinearLayout.LayoutParams position = new LinearLayout.LayoutParams(-1, -2); position.topMargin = position.bottomMargin = ui.dp(10);
            composer.addView(error, 0, position);
        }
    }
}
