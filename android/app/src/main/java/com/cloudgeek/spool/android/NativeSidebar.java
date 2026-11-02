package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONObject;

/** Reusable native Web-mobile sidebar, with session state owned separately from its overlay. */
final class NativeSidebar extends LinearLayout {
    final LinearLayout brand, navigation, footer;
    final ScrollView scroll;
    final List<NativeProjectRow> projectRows = new ArrayList<>();
    final List<NativeChatLink> chatRows = new ArrayList<>();
    final List<View> headings = new ArrayList<>(), moreButtons = new ArrayList<>();
    final NativeSidebarState state;
    private final NativeUi ui;
    private final Consumer<String> select, create;
    private final Runnable avatar, settings, logout;
    private final Function<String, JSONObject> person;
    private final Paint border = new Paint();
    private JSONArray projects = new JSONArray(), chats = new JSONArray();
    private JSONObject user = new JSONObject();
    private String project = "", chat = "", backend = "", signature = "";
    private boolean posting, active = true;
    private int epoch;

    NativeSidebar(NativeUi ui, NativeSidebarState state, Function<String, JSONObject> person, Runnable close, Consumer<String> select, Consumer<String> create, Runnable avatar, Runnable settings, Runnable logout) {
        super(ui.context); this.ui = ui; this.state = state; this.person = person; this.select = select; this.create = create; this.avatar = avatar; this.settings = settings; this.logout = logout;
        setOrientation(VERTICAL); setPadding(0, 0, ui.dp(1), 0); setBackgroundColor(ui.sidebar); setWillNotDraw(false);
        brand = ui.row(); brand.setPadding(ui.dp(18), 0, ui.dp(18), 0); brand.addView(ui.logo(32));
        TextView name = ui.text("Spool", 17, ui.text); ui.typography(name, 17, ui.text, 750); ui.fixedLineHeight(name, 20f / 17); name.setLetterSpacing(-.5f / 17);
        LayoutParams label = new LayoutParams(0, -2, 1); label.setMarginStart(ui.dp(10)); brand.addView(name, label); brand.addView(ui.icon("close", 18, ui.t("Close sidebar"), () -> { if (active) close.run(); })); addView(brand, new LayoutParams(-1, ui.dp(49))); addView(ui.divider());
        scroll = new ScrollView(ui.context); scroll.setVerticalScrollBarEnabled(false); scroll.setContentDescription(ui.t("Workspace"));
        navigation = ui.column(); navigation.setPadding(ui.dp(4), 0, ui.dp(4), ui.dp(24)); scroll.addView(navigation, new ScrollView.LayoutParams(-1, -2)); addView(scroll, new LayoutParams(-1, 0, 1));
        scroll.setOnScrollChangeListener((view, x, y, oldX, oldY) -> { if (active) state.scrollY = y; });
        addView(ui.divider()); footer = ui.row(); footer.setPadding(ui.dp(10), ui.dp(14), ui.dp(10), ui.dp(14)); addView(footer, new LayoutParams(-1, ui.dp(63)));
    }

    void bind(JSONObject user, JSONArray projects, JSONArray chats, String project, String chat, String backend, boolean posting) {
        this.user = user; this.projects = projects; this.chats = chats; this.project = project; this.chat = chat; this.backend = backend; this.posting = posting;
        state.select(project, chat, projects, chats);
        render();
    }
    private void heading(String title) {
        TextView text = ui.text(ui.t(title), 13, ui.muted); ui.typography(text, 13, ui.muted, 500); ui.fixedLineHeight(text, 22f / 13);
        LayoutParams at = new LayoutParams(-1, ui.dp(22)); at.setMargins(ui.dp(8), 0, ui.dp(8), ui.dp(9)); navigation.addView(text, at); headings.add(text); if (android.os.Build.VERSION.SDK_INT >= 28) text.setAccessibilityHeading(true);
    }
    private void more(String title, boolean nested, String tag, Runnable action) {
        Button button = ui.quietButton(ui.t(title), () -> { if (active) action.run(); }); ui.typography(button, 13, ui.muted, 400); ui.preciseText(button); button.setGravity(Gravity.START | Gravity.CENTER_VERTICAL); button.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6)); button.setBackground(ui.states(0, 7, 0, ui.hover, 0)); button.setTag(tag);
        LayoutParams at = new LayoutParams(-2, ui.dp(27)); if (nested) at.setMarginStart(ui.dp(24)); navigation.addView(button, at); moreButtons.add(button);
    }
    private void empty(String key, boolean nested) {
        TextView text = ui.text(ui.t(key), 11, ui.subtle); ui.fixedLineHeight(text, 1.8f); ui.preciseText(text); text.setPadding(ui.dp(nested ? 32 : 8), ui.dp(2), ui.dp(8), ui.dp(2));
        // The paragraph's 11px margin collapses with the heading's 9px and next section's 25px.
        LayoutParams at = new LayoutParams(-1, -2); at.topMargin = ui.dp(nested ? 11 : 2); at.bottomMargin = ui.dp(nested ? 11 : 0); navigation.addView(text, at);
    }
    private void list(String project, boolean nested) {
        List<JSONObject> ordered = NativeSidebarState.sorted(chats, project); int limit = project == null ? state.recentLimit : state.limit(project);
        for (int i = 0; i < Math.min(ordered.size(), limit); i++) {
            JSONObject value = ordered.get(i); String id = value.optString("id");
            NativeChatLink row = new NativeChatLink(ui, value, nested, chat.equals(id), backend, person, () -> { if (!active) return; state.expanded.add(value.optString("project_id")); select.accept(id); }); row.open.setTag((nested ? "project:" : "recent:") + id); navigation.addView(row, new LayoutParams(-1, ui.dp(31))); chatRows.add(row);
        }
        if (ordered.isEmpty()) empty("No conversations yet.", nested);
        if (ordered.size() > 5) more(ordered.size() > limit ? "Show more" : "Show less", nested, "more:" + (project == null ? "recents" : project), () -> { state.moreChats(project, ordered.size()); render(); });
    }
    private void render() {
        if (!active) return;
        StringBuilder next = new StringBuilder().append(user).append(projects).append(chats).append(project).append('\0').append(chat).append('\0').append(backend).append(posting).append(state.projectLimit).append(state.recentLimit).append(state.expanded).append(state.chatLimits);
        for (int i = 0; i < chats.length(); i++) { JSONArray participants = chats.optJSONObject(i).optJSONArray("participants"); if (participants != null) for (int j = 0; j < participants.length(); j++) next.append(person.apply(participants.optJSONObject(j).optString("id"))); }
        if (signature.contentEquals(next)) return;
        signature = next.toString();
        String focus = ""; View focused = findFocus(); if (focused != null && focused.getTag() != null) focus = focused.getTag().toString();
        int y = state.scrollY, renderEpoch = ++epoch;
        navigation.removeAllViews(); projectRows.clear(); chatRows.clear(); moreButtons.clear(); headings.clear(); heading("Projects");
        for (int i = 0; i < Math.min(projects.length(), state.projectLimit); i++) {
            JSONObject value = projects.optJSONObject(i); String id = value.optString("id"); boolean open = state.expanded.contains(id);
            NativeProjectRow row = new NativeProjectRow(ui, () -> { if (!active) return; state.toggle(id); render(); }, () -> { if (active) create.accept(id); }); row.bind(value, open, posting); row.toggle.setTag("project:" + id); row.create.setTag("new:" + id);
            navigation.addView(row, new LayoutParams(-1, ui.dp(31))); projectRows.add(row); if (open) list(id, true); navigation.addView(ui.space(5));
        }
        if (projects.length() == 0) empty("No authorized projects", false);
        if (projects.length() > state.projectLimit) more("Show more", false, "more:projects", () -> { state.moreProjects(); render(); });
        navigation.addView(ui.space(25)); heading("Recents"); list(null, false); if (chats.length() == 0) navigation.addView(ui.space(11)); footer();
        View restore = focus.isEmpty() ? null : findViewWithTag(focus);
        scroll.post(() -> { if (!active || renderEpoch != epoch) return; if (restore != null) restore.requestFocus(); scroll.scrollTo(0, y); });
    }
    private void footer() {
        footer.removeAllViews(); FrameLayout photo = new FrameLayout(ui.context); photo.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1)); photo.setBackground(ui.shape(ui.soft, 99, ui.borderStrong)); photo.addView(ui.avatar(user, 32), new FrameLayout.LayoutParams(ui.dp(32), ui.dp(32), Gravity.CENTER)); photo.setFocusable(true); photo.setContentDescription(ui.t("Change avatar")); photo.setOnClickListener(view -> { if (active) avatar.run(); }); footer.addView(photo, new LayoutParams(ui.dp(34), ui.dp(34)));
        LinearLayout copy = ui.column(); TextView name = ui.text(NativeAvatars.name(user), 12, ui.text); ui.fixedLineHeight(name, 14f / 12); ui.preciseText(name); name.setSingleLine(true); name.setEllipsize(android.text.TextUtils.TruncateAt.END); copy.addView(name, new LayoutParams(-1, -2));
        TextView hint = ui.text(ui.t("Project chat visibility follows project settings"), 9, ui.muted); ui.fixedLineHeight(hint, 10f / 9); hint.setSingleLine(true); hint.setEllipsize(android.text.TextUtils.TruncateAt.END); LayoutParams detail = new LayoutParams(-1, -2); detail.topMargin = ui.dp(4); copy.addView(hint, detail);
        LayoutParams identity = new LayoutParams(0, -2, 1); identity.setMarginStart(ui.dp(10)); footer.addView(copy, identity);
        LayoutParams action = new LayoutParams(ui.dp(32), ui.dp(32)); action.setMarginStart(ui.dp(10)); footer.addView(ui.icon("settings", 16, ui.t("Appearance and language"), () -> { if (active) settings.run(); }), action);
        LayoutParams exit = new LayoutParams(ui.dp(32), ui.dp(32)); exit.setMarginStart(ui.dp(10)); footer.addView(ui.icon("log-out", 16, ui.t("Sign out"), () -> { if (active) logout.run(); }), exit);
    }
    void deactivate() { active = false; epoch++; for (NativeChatLink row : chatRows) if (row.operators != null) row.operators.deactivate(); }
    @Override protected void onDetachedFromWindow() { deactivate(); super.onDetachedFromWindow(); }
    @Override protected void onDraw(Canvas canvas) { super.onDraw(canvas); border.setColor(ui.line); canvas.drawRect(getWidth() - ui.dp(1), 0, getWidth(), getHeight(), border); }
}
