package com.cloudgeek.spool.android;

import android.text.Editable;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.List;
import java.util.function.Consumer;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONObject;

/** Native DirectMessagePanel: nonmodal Web floating shell, people rail, log and autosizing composer. */
final class NativePeoplePanel extends NativeFloatingWindow {
    final NativePeopleConversations conversations;
    final LinearLayout body, main, toolbar, log, notices, composer;
    final ScrollView history;
    final TextView status;
    final EditText editor;
    final FrameLayout send;
    final NativeUi.IconButton invite, close;
    private final NativeSpinner spinner;
    private final View arrow;
    private final NativePeopleModel model;
    private final Function<String, JSONObject> person;
    private final Consumer<String> draft;
    private final Runnable submit;
    private String user = "", selected = "", last = "";
    private String renderedLog = "";
    private JSONArray online = new JSONArray();
    private boolean connected, syncing;

    NativePeoplePanel(NativeUi ui, NativePeopleModel model, Function<String, JSONObject> person, Bounds bounds, Consumer<Bounds> resize,
            Consumer<String> select, Consumer<String> draft, Runnable submit, Runnable dismiss, Runnable createGroup, Runnable inviteGroup) {
        super(ui, bounds, resize); this.model = model; this.person = person; this.draft = draft; this.submit = submit;
        body = ui.row(); body.setGravity(Gravity.TOP); sheet.addView(body, new LinearLayout.LayoutParams(-1, -1));
        conversations = new NativePeopleConversations(ui, select, createGroup); body.addView(conversations, new LinearLayout.LayoutParams(ui.dp(72), -1));
        main = ui.column(); body.addView(main, new LinearLayout.LayoutParams(0, -1, 1));
        toolbar = ui.row(); toolbar.setPadding(ui.dp(14), ui.dp(6), ui.dp(10), ui.dp(6));
        status = ui.text("", 11, ui.muted); ui.fixedLineHeight(status, 1.2f); status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        toolbar.addView(status, new LinearLayout.LayoutParams(0, -1, 1)); status.setGravity(Gravity.CENTER_VERTICAL); gesture(status, "move");
        invite = ui.icon("user-plus", 18, ui.t("Invite people"), inviteGroup); toolbar.addView(invite);
        LinearLayout.LayoutParams gap = new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32)); gap.setMarginStart(ui.dp(10));
        close = ui.icon("close", 18, ui.t("Close private chat"), dismiss); toolbar.addView(close, gap);
        main.addView(toolbar, new LinearLayout.LayoutParams(-1, ui.dp(44))); main.addView(ui.divider());
        history = new ScrollView(ui.context); history.setFillViewport(true); history.setVerticalScrollBarEnabled(false); history.setContentDescription(ui.t("Private messages"));
        log = ui.column(); log.setPadding(ui.dp(10), ui.dp(12), ui.dp(10), ui.dp(12)); history.addView(log, new ScrollView.LayoutParams(-1, -2)); main.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
        notices = ui.column(); main.addView(notices); main.addView(ui.divider());
        composer = new LinearLayout(ui.context) { @Override protected void dispatchDraw(android.graphics.Canvas canvas) { super.dispatchDraw(canvas); ui.focusRing(canvas, editor, 9); } }; composer.setClipChildren(false); composer.setClipToPadding(false); composer.setGravity(Gravity.BOTTOM); composer.setPadding(ui.dp(10), ui.dp(10), ui.dp(10), ui.dp(10));
        editor = new EditText(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) { super.onMeasure(widthSpec, heightSpec); int rows = Math.max(1, Math.min(6, (int) ((NativePeoplePanel.this.bounds == null ? 520 : NativePeoplePanel.this.bounds.height) - 290) / 20)); setMeasuredDimension(getMeasuredWidth(), ui.dp(Math.min(rows, Math.max(1, getLineCount())) * 20 + 18)); }
            @Override public android.view.inputmethod.InputConnection onCreateInputConnection(EditorInfo attributes) {
                android.view.inputmethod.InputConnection connection = super.onCreateInputConnection(attributes);
                return connection == null ? null : new android.view.inputmethod.InputConnectionWrapper(connection, false) {
                    @Override public boolean setComposingText(CharSequence text, int cursor) { boolean result = super.setComposingText(text, cursor); controls(); return result; }
                    @Override public boolean setComposingRegion(int start, int end) { boolean result = super.setComposingRegion(start, end); controls(); return result; }
                    @Override public boolean finishComposingText() { boolean result = super.finishComposingText(); controls(); return result; }
                    @Override public boolean commitText(CharSequence text, int cursor) { boolean result = super.commitText(text, cursor); controls(); return result; }
                };
            }
        };
        ui.typography(editor, 13, ui.text, 400); editor.setGravity(Gravity.TOP | Gravity.START); editor.setSingleLine(false); editor.setInputType(android.text.InputType.TYPE_CLASS_TEXT | android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE | android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        ui.preciseText(editor); editor.setHorizontallyScrolling(false);
        editor.setPadding(ui.dp(11), ui.dp(9), ui.dp(11), ui.dp(9)); editor.setBackground(ui.shape(ui.input, 9, ui.line)); editor.setOnFocusChangeListener((view, focused) -> composer.invalidate());
        editor.setHintTextColor(ui.muted); editor.setImeOptions(EditorInfo.IME_ACTION_SEND); editor.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
        editor.setLineSpacing(ui.dp(20) - (editor.getPaint().getFontMetrics().descent - editor.getPaint().getFontMetrics().ascent), 1);
        composer.addView(editor, new LinearLayout.LayoutParams(0, -2, 1));
        send = new FrameLayout(ui.context); send.setBackground(ui.states(ui.primary, 10, 0, ui.primaryHover, ui.primary)); send.setFocusable(true); send.setOnClickListener(view -> send());
        arrow = ui.decorativeIcon("send", 17); ((android.widget.ImageView) arrow).setColorFilter(0xffffffff); send.addView(arrow, new FrameLayout.LayoutParams(ui.dp(17), ui.dp(17), Gravity.CENTER));
        spinner = new NativeSpinner(ui, 17, ui.t("Working…")); spinner.setColorFilter(0xffffffff); send.addView(spinner, new FrameLayout.LayoutParams(ui.dp(17), ui.dp(17), Gravity.CENTER)); spinner.setVisibility(GONE);
        LinearLayout.LayoutParams button = new LinearLayout.LayoutParams(ui.dp(38), ui.dp(38)); button.setMarginStart(ui.dp(6)); composer.addView(send, button); main.addView(composer);
        editor.addTextChangedListener(new TextWatcher() { public void beforeTextChanged(CharSequence s,int start,int count,int after) {} public void onTextChanged(CharSequence s,int start,int before,int count) {} public void afterTextChanged(Editable value) { if (!syncing) { draft.accept(value.toString()); controls(); } } });
        editor.setOnEditorActionListener((view, action, event) -> { if (action == EditorInfo.IME_ACTION_SEND) { send(); return true; } return false; });
        editor.setOnKeyListener((view, key, event) -> { if (event.getAction() == KeyEvent.ACTION_DOWN && key == KeyEvent.KEYCODE_ENTER && !event.isShiftPressed() && !composing()) { send(); return true; } if (key == KeyEvent.KEYCODE_ESCAPE && event.getAction() == KeyEvent.ACTION_DOWN && !composing()) { dismiss.run(); return true; } return false; });
    }

    private boolean composing() { return BaseInputConnection.getComposingSpanStart(editor.getText()) >= 0; }
    private boolean writable() { return connected && (NativePeopleModel.group(model.active) ? model.active.equals(NativePeopleModel.ROOM) || model.groupInfo(model.active) != null : !NativePeopleModel.status(online, model.active).isEmpty()) && !model.sending.containsKey(model.active) && !NativePeopleModel.tooLong(editor.getText().toString()); }
    private void send() { if (writable() && !composing() && !NativePeopleModel.trim(editor.getText().toString()).isEmpty()) submit.run(); }
    void bind(String user, JSONArray online, boolean connected) {
        this.user = user; this.online = online; this.connected = connected;
        boolean changed = !selected.equals(model.active); if (changed) { selected = model.active; last = ""; }
        setContentDescription(NativeAvatars.name(person.apply(model.active)));
        conversations.bind(user, model, online, connected, person);
        JSONObject group = model.groupInfo(model.active);
        invite.setVisibility(group == null ? GONE : VISIBLE); invite.setEnabled(connected && group != null && group.optJSONArray("members").length() < 32);
        String current = NativePeopleModel.status(online, model.active);
        status.setText(!connected ? ui.t("Reconnecting…") : model.active.equals(NativePeopleModel.ROOM) ? ui.t("{count} online", "count", online.length()) : NativePeopleModel.group(model.active) ? ui.t("{count} members", "count", group == null ? 0 : group.optJSONArray("members").length()) : ui.t(current.isEmpty() ? "Offline" : current.equals("online") ? "Online" : "Away"));
        close.setContentDescription(ui.t(NativePeopleModel.group(model.active) ? "Close group chat" : "Close private chat"));
        String hint = ui.t(NativePeopleModel.group(model.active) ? "Group message" : "Private message"); editor.setHint(hint); editor.setContentDescription(hint); send.setContentDescription(ui.t(NativePeopleModel.group(model.active) ? "Send group message" : "Send private message"));
        String text = model.draft(model.active);
        if (!editor.getText().toString().equals(text)) { syncing = true; editor.setText(text); editor.setSelection(text.length()); syncing = false; }
        List<JSONObject> messages = model.history(user); String newest = messages.isEmpty() ? "" : messages.get(messages.size() - 1).optString("id");
        StringBuilder signature = new StringBuilder(model.active);
        for (JSONObject message : messages) signature.append('\0').append(message).append('\0').append(person.apply(message.optString("from")));
        // Presence and send acknowledgements must not destroy the user's selected transcript text.
        if (!renderedLog.contentEquals(signature)) {
            renderedLog = signature.toString(); log.removeAllViews();
            if (messages.isEmpty()) { log.setGravity(Gravity.CENTER); TextView empty = ui.text(ui.t(model.active.equals(NativePeopleModel.ROOM) ? "Everyone online can read this group chat." : NativePeopleModel.group(model.active) ? "Only group members can read these messages. New members do not receive earlier messages." : "Say hello. Only the two of you can see these messages."), 12, ui.muted); ui.fixedLineHeight(empty, 1.8f); empty.setGravity(Gravity.CENTER); empty.setMaxWidth(ui.dp(260)); log.addView(empty); }
            else { log.setGravity(Gravity.TOP); for (JSONObject message : messages) { boolean own = message.optString("from").equals(user); NativePeopleMessage row = new NativePeopleMessage(ui, message, person.apply(message.optString("from")), own); LinearLayout.LayoutParams position = new LinearLayout.LayoutParams(-2, -2); position.gravity = own ? Gravity.END : Gravity.START; if (log.getChildCount() > 0) position.topMargin = ui.dp(12); log.addView(row, position); } }
        }
        if (changed || !last.equals(newest)) history.post(() -> history.fullScroll(View.FOCUS_DOWN)); last = newest;
        controls(); if (changed) editor.post(() -> { if (selected.equals(model.active)) editor.requestFocus(); });
    }
    private void notice(String key, boolean error) { TextView text = ui.text(ui.t(key), 11, error ? ui.error : ui.muted); ui.fixedLineHeight(text, 1.6f); text.setPadding(ui.dp(10), ui.dp(8), ui.dp(10), ui.dp(8)); text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE); notices.addView(text); }
    private void controls() {
        boolean pending = model.sending.containsKey(model.active); editor.setEnabled(!pending);
        spinner.setVisibility(pending ? VISIBLE : GONE); arrow.setVisibility(pending ? INVISIBLE : VISIBLE);
        send.setEnabled(writable() && !composing() && !NativePeopleModel.trim(editor.getText().toString()).isEmpty()); send.setAlpha(send.isEnabled() ? 1 : .45f);
        notices.removeAllViews();
        if (NativePeopleModel.group(model.active) && !model.active.equals(NativePeopleModel.ROOM) && model.groupInfo(model.active) == null) notice("This group is no longer available to you.", false);
        if (!connected) notice("Chat disconnected. Wait for reconnection before sending.", false);
        else if (!NativePeopleModel.group(model.active) && NativePeopleModel.status(online, model.active).isEmpty()) notice("This person is offline. Messages are not queued.", false);
        if (NativePeopleModel.tooLong(editor.getText().toString()) && !composing()) notice("Enter a message of up to 2000 characters.", true);
        else if (model.errors.containsKey(model.active)) notice(NativePeopleModel.errorKey(model.errors.get(model.active), false), true);
    }
}
