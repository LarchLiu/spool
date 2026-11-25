package com.cloudgeek.spool.android;

import android.text.Editable;
import android.text.TextWatcher;
import android.view.KeyEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Owns native slash completion without taking the EditText's focus or replacing its document. */
final class NativeComposerCommands {
    interface ClientMenu {
        NativeSlashCommand root();
        java.util.List<NativeSlashCommand> suggestions(String text);
        void select(NativeSlashCommand command);
        boolean canBack();
        boolean back();
        boolean commandsEnabled();
    }
    ClientMenu clientMenu;
    final TextView hint;
    final NativePopover menu;
    final java.util.List<NativeSlashCommandMenu> options = new java.util.ArrayList<>();
    NativeCommandNotice notice;
    TextView error;
    private LinearLayout errorRow;
    private final NativeUi ui;
    private final NativeUi.Composer composer;
    private boolean writable, posting, running, dismissed, hiding, localMenu;
    private int handledKey;
    private int activeIndex;
    private java.util.List<NativeSlashCommand> catalog = java.util.Collections.emptyList(), suggestions;
    private String catalogKey = "", selectionKey = "", renderedKey = "";
    private String catalogMessage = "";
    /** While a turn runs, the draft waits for its end or is steered into it; set by the composer's owner. */
    Runnable queue, steer;
    private String noticeText = "", errorText = "";

    NativeComposerCommands(NativeUi ui, NativeUi.Composer composer, Runnable submit) {
        this.ui = ui; this.composer = composer;
        hint = ui.text(ui.t("/goal <condition> · /goal to view · /goal clear to remove"), 11, ui.subtle);
        ui.normalFallbackLineSpacing(hint);
        hint.setPadding(ui.dp(19), 0, ui.dp(19), ui.dp(8)); hint.setVisibility(View.GONE);
        composer.box.addView(hint, composer.box.getChildCount() - 1, new LinearLayout.LayoutParams(-1, -2));
        menu = NativePopover.commands(ui);
        menu.content.setContentDescription(ui.t("Chat commands"));
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
            boolean complete = menu.isShowing() && !options.isEmpty() && (key == KeyEvent.KEYCODE_DPAD_RIGHT && suggestions != null && suggestions.get(activeIndex).local
                    || key == KeyEvent.KEYCODE_TAB || key == KeyEvent.KEYCODE_ENTER
                    && !event.isMetaPressed() && !event.isAltPressed() && !event.isShiftPressed());
            boolean navigation = menu.isShowing() && !options.isEmpty() && (key == KeyEvent.KEYCODE_DPAD_DOWN || key == KeyEvent.KEYCODE_DPAD_UP);
            boolean escape = menu.isShowing() && key == KeyEvent.KEYCODE_ESCAPE;
            boolean back = menu.isShowing() && key == KeyEvent.KEYCODE_DPAD_LEFT && clientMenu != null && clientMenu.canBack();
            // Enter sends and Shift+Enter breaks the line. A touch keyboard has no
            // Shift+Enter, so its Enter keeps the line break and the button sends.
            boolean hardware = (event.getFlags() & KeyEvent.FLAG_SOFT_KEYBOARD) == 0
                    && event.getDeviceId() != android.view.KeyCharacterMap.VIRTUAL_KEYBOARD;
            boolean direct = event.isMetaPressed() || event.isCtrlPressed();
            boolean send = key == KeyEvent.KEYCODE_ENTER && !event.isShiftPressed()
                    && (direct || event.isAltPressed() || hardware);
            if (!complete && !navigation && !escape && !back && !send) return false;
            handledKey = key;
            if (event.getRepeatCount() != 0) return true;
            if (complete) select();
            else if (navigation) {
                activeIndex = (activeIndex + (key == KeyEvent.KEYCODE_DPAD_DOWN ? 1 : options.size() - 1)) % options.size();
                for (int i = 0; i < options.size(); i++) options.get(i).setActive(i == activeIndex);
                menu.reveal(options.get(activeIndex));
            } else if (back) { clientMenu.back(); }
            else if (escape) { dismissed = true; hide(); }
            else if (send && writable && !posting) {
                // The key never stops a running turn; that is the button's job.
                if (!running) submit.run();
                else if (direct && steer != null) steer.run();
                else if (queue != null) queue.run();
            }
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

    void catalog(String backend, org.json.JSONArray commands) {
        catalog(backend, commands, "");
    }

    void catalog(String backend, org.json.JSONArray commands, String message) {
        String key = backend + "\n" + commands + "\n" + message;
        if (key.equals(catalogKey)) return;
        catalogKey = key; catalogMessage = message; catalog = NativeSlashCommand.catalog(commands); refresh();
    }

    private boolean composing() { return android.view.inputmethod.BaseInputConnection.getComposingSpanStart(composer.input.getText()) >= 0; }

    void refresh() {
        String text = composer.input.getText().toString();
        java.util.List<NativeSlashCommand> commands = catalog;
        if (clientMenu != null) {
            commands = new java.util.ArrayList<>(); commands.add(clientMenu.root());
            for (NativeSlashCommand item : catalog) if (!item.name.equals("mcp")) commands.add(item);
        }
        suggestions = clientMenu == null ? null : clientMenu.suggestions(text);
        localMenu = suggestions != null;
        if (suggestions == null) suggestions = NativeSlashCommand.suggestions(text, commands);
        if (!localMenu && clientMenu != null && !clientMenu.commandsEnabled()) suggestions = null;
        StringBuilder state = new StringBuilder();
        if (suggestions != null) for (NativeSlashCommand item : suggestions)
            state.append(item.name).append(item.description).append(item.enabled).append('\n');
        String key = catalogKey + "\n" + text + "\n" + state;
        if (!selectionKey.equals(key)) { selectionKey = key; activeIndex = 0; renderedKey = ""; }
        hint.setVisibility(NativeGoalCommand.parse(text) == null ? View.GONE : View.VISIBLE);
        boolean show = writable && composer.input.hasFocus() && !posting && !running && !dismissed && suggestions != null;
        if (show && composer.box.isAttachedToWindow() && composer.box.getWidth() > 0) {
            if (!renderedKey.equals(key)) { renderedKey = key; renderOptions(); }
            if (!menu.isShowing()) menu.show(composer.box, true);
        } else hide();
    }

    private void renderOptions() {
        options.clear(); menu.content.removeAllViews(); boolean skills = false;
        if (suggestions.isEmpty() || !localMenu && catalog.isEmpty()) {
            TextView empty = ui.text(catalogMessage.isEmpty() ? ui.t("No matching agent commands") : catalogMessage, 12, ui.subtle);
            ui.normalFallbackLineSpacing(empty); empty.setPadding(ui.dp(12), ui.dp(8), ui.dp(12), ui.dp(8));
            empty.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.topMargin = params.bottomMargin = ui.dp(12);
            menu.content.addView(empty, params);
        }
        for (NativeSlashCommand command : suggestions) {
            if (command.skill() && !skills) {
                TextView group = ui.text(ui.t("Skills"), 12, ui.muted); ui.normalFallbackLineSpacing(group);
                group.setPadding(ui.dp(7), ui.dp(12), ui.dp(7), ui.dp(5)); menu.content.addView(group); skills = true;
            }
            NativeSlashCommandMenu row = new NativeSlashCommandMenu(ui, command, options.size() == activeIndex, () -> select(command));
            options.add(row); menu.content.addView(row, new LinearLayout.LayoutParams(-1, -2));
        }
        menu.reveal(options.isEmpty() ? menu.content : options.get(activeIndex));
    }

    /** Whether the open list belongs to the client menu rather than the agent's slash commands. */
    boolean clientMenuShowing() { return localMenu && menu.isShowing(); }

    void select() {
        if (suggestions != null && activeIndex < suggestions.size()) select(suggestions.get(activeIndex));
    }

    private void select(NativeSlashCommand command) {
        if (!command.enabled) return;
        if (command.local && clientMenu != null) { clientMenu.select(command); refresh(); composer.input.requestFocus(); return; }
        composer.input.setText("/" + command.name + " "); composer.input.setSelection(composer.input.length());
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

    void error(String value, Runnable dismiss) {
        if (errorText.equals(value)) return;
        errorText = value;
        if (errorRow != null) { composer.removeView(errorRow); errorRow = null; error = null; }
        if (!value.isEmpty()) {
            errorRow = ui.row(); errorRow.setGravity(android.view.Gravity.TOP);
            errorRow.setPadding(ui.dp(15), ui.dp(12), ui.dp(15), ui.dp(12));
            errorRow.setBackground(ui.shape(ui.errorBg, 10, ui.errorBorder));
            error = ui.text(value, 12, ui.error); ui.fixedLineHeight(error, 1.7f);
            error.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_ASSERTIVE);
            errorRow.addView(error, new LinearLayout.LayoutParams(0, -2, 1));
            NativeUi.IconButton close = ui.icon("close", 15, ui.t("Dismiss"), dismiss);
            close.setTranslationX(ui.dp(12)); close.setTranslationY(-ui.dp(6));
            LinearLayout.LayoutParams button = new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32));
            button.leftMargin = ui.dp(10); errorRow.addView(close, button);
            LinearLayout.LayoutParams position = new LinearLayout.LayoutParams(-1, -2); position.topMargin = position.bottomMargin = ui.dp(10);
            composer.addView(errorRow, 0, position);
        }
    }
}
