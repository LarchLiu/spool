package com.cloudgeek.spool.android;

import android.view.View;
import android.view.WindowManager;
import android.view.inputmethod.EditorInfo;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;

/** Chat management UI; mutations and navigation remain owned by the Activity/API. */
final class NativeChatMenu {
    interface Actions {
        boolean canManage();
        boolean pinned();
        void togglePin();
        void rename(String title, Consumer<Exception> done);
        void archive(Consumer<Exception> done);
        void delete(Consumer<Exception> done);
    }

    final NativePopover popup;
    ChatDialog dialog;
    private final NativeUi ui;
    private final String title;
    private final Actions actions;
    private final Consumer<Exception> failed;

    NativeChatMenu(NativeUi ui, String title, Actions actions, Consumer<Exception> failed) {
        this.ui = ui; this.title = title; this.actions = actions; this.failed = failed;
        popup = NativePopover.actions(ui);
        if (actions.canManage()) option("Rename", "pencil", false, this::rename);
        option(actions.pinned() ? "Unpin chat" : "Pin chat", actions.pinned() ? "pin-off" : "pin", false, actions::togglePin);
        if (actions.canManage()) {
            View separator = ui.divider();
            separator.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
            LinearLayout.LayoutParams spacing = new LinearLayout.LayoutParams(-1, ui.dp(1));
            spacing.topMargin = spacing.bottomMargin = ui.dp(4);
            popup.content.addView(separator, spacing);
            option("Archive", "archive", false, () -> actions.archive(error -> { if (error != null) failed.accept(error); }));
            option("Delete", "trash", true, this::delete);
        }
    }

    private void option(String text, String icon, boolean danger, Runnable click) {
        popup.actionOption(ui.t(text), icon, danger, true, click);
    }

    void show(View anchor) { popup.showActions(anchor); }

    private void rename() {
        dialog = new ChatDialog(false);
        dialog.show();
        dialog.name.requestFocus();
        dialog.name.selectAll();
        dialog.getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_VISIBLE
                | WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
    }

    private void delete() {
        dialog = new ChatDialog(true);
        dialog.show();
        dialog.cancel.setFocusableInTouchMode(true);
        dialog.cancel.requestFocus();
    }

    void dismiss() {
        popup.dismiss();
        if (dialog != null) dialog.dismiss();
    }

    final class ChatDialog extends NativeDialog {
        final EditText name;
        final Button cancel, submit;
        private final boolean deleting;
        private boolean saving;

        ChatDialog(boolean deleting) {
            super(NativeChatMenu.this.ui, NativeChatMenu.this.ui.t(deleting ? "Delete chat" : "Rename chat"), NativeChatMenu.this.ui.t("Cancel"));
            this.deleting = deleting;
            if (deleting) {
                name = null;
                TextView selected = ui.bold(title, 14);
                selected.setMaxLines(3);
                selected.setEllipsize(android.text.TextUtils.TruncateAt.END);
                body.addView(selected); body.addView(ui.space(12));
                TextView explanation = ui.text(ui.t("This permanently deletes this chat and its subagent history. This action cannot be undone."), 13, ui.muted);
                ui.normalFallbackLineSpacing(explanation);
                body.addView(explanation);
            } else {
                body.addView(ui.text(ui.t("Chat title"), 12, ui.text)); body.addView(ui.space(8));
                name = ui.field(ui.t("Chat title"), false);
                name.setText(title);
                name.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);
                name.setImeOptions(EditorInfo.IME_ACTION_DONE);
                name.setOnEditorActionListener((view, action, event) -> {
                    if (action != EditorInfo.IME_ACTION_DONE) return false;
                    save(); return true;
                });
                body.addView(name, new LinearLayout.LayoutParams(-1, -2));
            }
            body.addView(ui.space(20));
            cancel = ui.quietButton(ui.t("Cancel"), this::dismiss);
            submit = ui.button(ui.t(deleting ? "Delete" : "Save"), !deleting, this::save);
            if (deleting) {
                submit.setTextColor(ui.error);
                submit.setBackground(ui.states(ui.errorBg, 9, ui.errorBorder, ui.controlActive, ui.errorBg));
            }
            for (Button button : new Button[] {cancel, submit}) {
                button.setMinHeight(ui.dp(48)); button.setMaxLines(2);
                actions.addView(button, new LinearLayout.LayoutParams(0, -2, 1));
            }
            ((LinearLayout.LayoutParams) submit.getLayoutParams()).setMarginStart(ui.dp(8));
        }

        private void save() {
            if (saving) return;
            String value = name == null ? "" : name.getText().toString().replaceAll("[\\p{Z}\\s]+", " ").trim();
            if (!deleting && (value.isEmpty() || value.codePointCount(0, value.length()) > 200
                    || value.codePoints().anyMatch(Character::isISOControl))) {
                name.setError(ui.t("Enter a chat title of 1–200 characters"));
                return;
            }
            saving = true;
            lock(true); cancel.setEnabled(false); submit.setEnabled(false);
            if (name != null) name.setEnabled(false);
            submit.setText(ui.t(deleting ? "Deleting…" : "Saving…"));
            error.setVisibility(View.GONE);
            Consumer<Exception> done = failure -> {
                if (failure == null) { dismiss(); return; }
                saving = false;
                lock(false); cancel.setEnabled(true); submit.setEnabled(true);
                if (name != null) name.setEnabled(true);
                submit.setText(ui.t(deleting ? "Delete" : "Save"));
                error.setText(failure.getMessage()); error.setVisibility(View.VISIBLE);
                if (!isShowing()) failed.accept(failure);
            };
            if (deleting) NativeChatMenu.this.actions.delete(done);
            else NativeChatMenu.this.actions.rename(value, done);
        }
    }
}
