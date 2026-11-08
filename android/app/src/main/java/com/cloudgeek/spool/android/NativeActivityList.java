package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.text.SpannableStringBuilder;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Consumer;

/** Native ToolActivityList with Web-keyed groups/items and conditional detail lifetimes. */
final class NativeActivityList extends LinearLayout {
    interface UserInput { void bind(NativeUserRow row, int start, int end); }
    static final class State {
        boolean open, live, initialized;
        int count = -1;
        final Set<Integer> groups = new HashSet<>();
        final Set<String> compounds = new HashSet<>();
        final Set<String> items = new HashSet<>();
        final Map<Integer, Boolean> imageGroups = new HashMap<>();
        void update(boolean nextLive, int nextCount) {
            if (!initialized || nextLive != live) open = nextLive;
            if (nextCount != count || nextLive != live) {
                groups.clear();
                if (nextLive) for (int i = 0; i < nextCount; i++) groups.add(i);
            }
            initialized = true; live = nextLive; count = nextCount;
        }
    }
    private final class Item {
        final LinearLayout view = ui.column();
        final NativeDisclosureRow row;
        final String id;
        NativeToolDetail detail;
        NativeActivityModel.Activity current;
        String previewSource, previewLabel;
        CharSequence previewTitle;
        Item(NativeActivityModel.Activity activity, String id) {
            this.id = id;
            row = new NativeDisclosureRow(ui, "", NativeActivityModel.icon(activity.kind), "", false, false, false, () -> {});
            view.addView(row, new LayoutParams(-1, -2));
            row.setOnClickListener(button -> {
                if (current.noDetails) return;
                if (!state.items.add(id)) state.items.remove(id);
                bind(current);
            });
            bind(activity);
        }
        void bind(NativeActivityModel.Activity activity) {
            current = activity;
            CharSequence title = activity.label;
            org.json.JSONObject summary = activity.use == null ? null : activity.use.optJSONObject("tool");
            String preview = summary == null ? activity.use == null ? "" : activity.use.optString("text") : summary.optString("preview");
            if (activity.kind.equals("thinking") && !preview.trim().isEmpty()) {
                if (!preview.equals(previewSource) || !activity.label.equals(previewLabel)) {
                    long started = System.nanoTime();
                    SpannableStringBuilder label = new SpannableStringBuilder(activity.label + " \u00b7 ");
                    label.append(NativeMarkdown.preview(ui, preview));
                    if (ui.toolDetails != null) ui.toolDetails.rendered("thinking_preview", System.nanoTime()-started);
                    previewSource = preview; previewLabel = activity.label; previewTitle = label;
                }
                title = previewTitle;
            } else { previewSource = previewLabel = null; previewTitle = null; }
            row.bind(title, NativeActivityModel.icon(activity.kind), NativeActivityModel.changeStats(java.util.Collections.singletonList(activity)));
            row.setDisclosureEnabled(!activity.noDetails);
            row.setExpanded(!activity.noDetails && state.items.contains(id));
            if (!activity.noDetails && state.items.contains(id)) {
                if (detail == null) {
                    detail = new NativeToolDetail(ui, activity, openLink);
                    LayoutParams params = new LayoutParams(-1, -2);
                    params.setMarginStart(ui.dp(20)); params.topMargin = ui.dp(3); params.bottomMargin = ui.dp(8);
                    view.addView(detail, params);
                } else detail.bind(activity);
            } else if (detail != null) {
                view.removeView(detail); detail = null;
            }
        }
    }
    private final class Group {
        final LinearLayout view = ui.column(), nested;
        final NativeDisclosureRow heading;
        final int index;
        Map<String, Item> items = new HashMap<>();
        NativeActivityModel.Group current;
        Group(NativeActivityModel.Group group, int index) {
            this.index = index;
            nested = new LinearLayout(ui.context) {
                final Paint border = new Paint();
                @Override protected void dispatchDraw(Canvas canvas) {
                    border.setColor(ui.line); canvas.drawRect(0, 0, ui.dp(1), getHeight(), border);
                    super.dispatchDraw(canvas);
                }
            };
            nested.setOrientation(VERTICAL); nested.setPadding(ui.dp(9), 0, 0, 0);
            heading = new NativeDisclosureRow(ui, "", NativeActivityModel.icon(group.kind), "", false, true, false, () -> {});
            heading.setOnClickListener(button -> {
                if (current.compound) {
                    String key = compoundKey(current, index);
                    if (!state.compounds.add(key)) state.compounds.remove(key);
                } else if (!state.groups.add(index)) state.groups.remove(index);
                bind(current);
            });
            view.addView(heading, new LayoutParams(-1, -2)); bind(group);
        }
        void bind(NativeActivityModel.Group group) {
            current = group;
            heading.bind(group.label, NativeActivityModel.icon(group.kind), NativeActivityModel.changeStats(group.activities));
            boolean expanded = group.compound ? state.compounds.contains(compoundKey(group, index)) : state.groups.contains(index); heading.setExpanded(expanded);
            if (expanded) {
                List<View> views = new ArrayList<>(); Map<String, Item> next = new HashMap<>();
                for (int i = 0; i < group.activities.size(); i++) {
                    String key = index + ":" + i; NativeActivityModel.Activity activity = group.activities.get(i);
                    Item item = items.get(key); if (item == null) item = new Item(activity, key); else item.bind(activity);
                    views.add(item.view); next.put(key, item);
                }
                NativeKeyedViews.sync(nested, views); items = next;
                if (nested.getParent() == null) {
                    LayoutParams params = new LayoutParams(-1, -2); params.setMarginStart(ui.dp(20));
                    params.topMargin = ui.dp(2); params.bottomMargin = ui.dp(7); view.addView(nested, params);
                }
            } else {
                view.removeView(nested); nested.removeAllViews(); items.clear();
            }
        }
    }
    private static final class Progress {
        final LinearLayout view;
        String source;
        Progress(LinearLayout view, String source) { this.view = view; this.source = source; }
    }
    private final class User {
        final NativeUserRow view = new NativeUserRow(ui, null, "", 0, null, openLink);
        NativeActivityModel.Activity current;
        void bind(NativeActivityModel.Activity activity) {
            current = activity;
            if (userInput != null) userInput.bind(view, activity.inputStart, activity.inputStart + activity.input.size());
            else {
                org.json.JSONArray input = new org.json.JSONArray(activity.input);
                view.bind(null, NativeUserRow.text(input, 0, input.length()), 0, java.util.Collections.emptyList());
            }
        }
    }
    private final class Images {
        final LinearLayout view = ui.column();
        final NativeDisclosureRow heading;
        final NativeViewedImages thumbnails = new NativeViewedImages(ui, imageActions);
        NativeActivityModel.Group current;
        Images(NativeActivityModel.Group group, int index) {
            heading = new NativeDisclosureRow(ui, "", "image", "", false, true, false, () -> {});
            heading.setOnClickListener(v -> {
                state.imageGroups.put(index, !state.imageGroups.getOrDefault(index, state.live));
                bind(current, index);
            });
            view.addView(heading, new LayoutParams(-1, -2)); bind(group, index);
        }
        void bind(NativeActivityModel.Group group, int index) {
            current = group; heading.bind(group.label, "image", "");
            boolean open = state.imageGroups.getOrDefault(index, state.live); heading.setExpanded(open);
            if (open) {
                thumbnails.bind(group);
                if (thumbnails.getParent() == null) {
                    LayoutParams params = new LayoutParams(-1, -2); params.setMarginStart(ui.dp(20));
                    params.topMargin = ui.dp(4); params.bottomMargin = ui.dp(8); view.addView(thumbnails, params);
                }
            } else { view.removeView(thumbnails); }
        }
    }
    private final NativeUi ui;
    private State state;
    private final Consumer<String> openLink;
    private NativeTranscriptImages imageActions;
    private UserInput userInput;
    private final LinearLayout list;
    private final View summaryGap, bottomGap;
    private final Map<Integer, View> gaps = new HashMap<>();
    private List<NativeActivityModel.Group> groups = new ArrayList<>();
    private Map<String, Object> entries = new HashMap<>();
    private NativeDisclosureRow toggle;
    private Object bound;
    private List<org.json.JSONObject> deferredParts;
    private String deferredBackend;
    private CharSequence currentSummary;
    TextView summaryLabel;

    NativeActivityList(NativeUi ui, List<NativeActivityModel.Activity> activities, CharSequence summary,
            boolean live, State state, Consumer<String> openLink) {
        this(ui, state, openLink);
        bind(activities, summary, live);
    }

    NativeActivityList(NativeUi ui, List<org.json.JSONObject> parts, String backend, CharSequence summary,
            boolean live, State state, Consumer<String> openLink) {
        this(ui, parts, backend, summary, live, state, openLink, null);
    }

    NativeActivityList(NativeUi ui, List<org.json.JSONObject> parts, String backend, CharSequence summary,
            boolean live, State state, Consumer<String> openLink, NativeTranscriptImages images) {
        this(ui, state, openLink, images);
        bindParts(parts, backend, summary, live);
    }

    private NativeActivityList(NativeUi ui, State state, Consumer<String> openLink) {
        this(ui, state, openLink, null);
    }

    private NativeActivityList(NativeUi ui, State state, Consumer<String> openLink, NativeTranscriptImages images) {
        super(ui.context); this.ui = ui; this.state = state; this.openLink = openLink;
        imageActions = images;
        setOrientation(VERTICAL); list = ui.column(); summaryGap = ui.space(12); bottomGap = ui.space(10);
    }

    /** Reuse the native summary widgets, with a different turn's own disclosure state and actions. */
    void rebindForTurn(State next, NativeTranscriptImages images) {
        if (state == next) return;
        state = next; imageActions = images;
        bound = null; deferredParts = null; deferredBackend = null; userInput = null;
        groups = new ArrayList<>(); entries.clear(); gaps.clear(); list.removeAllViews();
    }

    void bindParts(List<org.json.JSONObject> parts, String backend, CharSequence summary, boolean live) {
        deferredParts = parts; deferredBackend = backend;
        if (live || state.open || parts.isEmpty()) {
            bind(NativeActivityModel.fromParts(ui, parts, backend), summary, live);
            return;
        }
        // A folded completed turn only paints its summary. Tool JSON/regex/patch work is demand-driven.
        NativeActivityModel.Group placeholder = new NativeActivityModel.Group();
        placeholder.kind = "other"; placeholder.label = "";
        groups = java.util.Collections.singletonList(placeholder); bound = null;
        state.update(false, state.count); update(); setSummary(summary);
    }

    void bind(List<NativeActivityModel.Activity> activities, CharSequence summary, boolean live) {
        org.json.JSONArray input = new org.json.JSONArray().put(live);
        for (NativeActivityModel.Activity activity : activities) input.put(new org.json.JSONArray().put(activity.kind)
                .put(activity.label).put(activity.detail).put(activity.stats).put(activity.use).put(activity.result).put(activity.noDetails).put(activity.inputStart).put(new org.json.JSONArray(activity.input)));
        if (!NativeChatState.equivalent(bound, input)) {
            bound = NativeChatState.snapshot(input); groups = NativeActivityModel.groups(ui, activities); state.update(live, groups.size());
            update();
        }
        setSummary(summary);
    }

    void userInput(UserInput input) {
        userInput = input;
        for (Object entry : entries.values()) if (entry instanceof User) { User user = (User) entry; user.bind(user.current); }
    }

    void setSummary(CharSequence value) {
        currentSummary = value;
        if (toggle == null) summaryLabel.setText(value);
        else toggle.bind(value, "", "");
    }

    private void update() {
        List<View> views = new ArrayList<>();
        if (groups.isEmpty()) {
            if (summaryLabel == null || toggle != null) summaryLabel = ui.text("", 13, ui.muted);
            toggle = null; views.add(summaryLabel);
        } else {
            if (toggle == null) {
                toggle = new NativeDisclosureRow(ui, "", "", "", true, false, state.open, () -> {});
                toggle.setOnClickListener(button -> {
                    state.open = !state.open;
                    if (state.open && deferredParts != null) bindParts(deferredParts, deferredBackend, currentSummary, false);
                    // The body can be unchanged when reopening a just-completed turn.
                    update();
                });
                summaryLabel = toggle.label;
                toggle.setLayoutParams(new LayoutParams(-1, -2));
            }
            toggle.setExpanded(state.open); views.add(toggle);
        }
        views.add(summaryGap);
        if (!groups.isEmpty() && state.open) { buildGroups(); views.add(list); }
        else { list.removeAllViews(); entries.clear(); gaps.clear(); }
        NativeKeyedViews.sync(this, views);
    }

    private void buildGroups() {
        List<View> views = new ArrayList<>(); Map<String, Object> next = new HashMap<>();
        for (int i = 0; i < groups.size(); i++) {
            NativeActivityModel.Group group = groups.get(i);
            if (i > 0) views.add(gaps.computeIfAbsent(i, ignored -> ui.space(2)));
            String key;
            if (group.kind.equals("user")) {
                NativeActivityModel.Activity activity = group.activities.get(0);
                key = "user:" + (activity.use == null ? "" : activity.use.optString("id")) + ":" + i;
                User entry = (User) entries.get(key);
                if (entry == null) {
                    entry = new User(); LayoutParams params = new LayoutParams(-1, -2);
                    params.topMargin = ui.dp(10); params.bottomMargin = ui.dp(10); entry.view.setLayoutParams(params);
                }
                entry.bind(activity); next.put(key, entry); views.add(entry.view);
            } else if (group.kind.equals("message")) {
                NativeActivityModel.Activity activity = group.activities.get(0);
                key = "message:" + (activity.use == null ? "" : activity.use.optString("id")) + ":" + i;
                Progress progress = (Progress) entries.get(key);
                if (progress == null) {
                    LinearLayout markdown = NativeMarkdown.progress(ui, activity.detail, openLink);
                    markdown.setPadding(ui.dp(5), ui.dp(6), ui.dp(5), ui.dp(10));
                    progress = new Progress(markdown, activity.detail);
                } else if (!progress.source.equals(activity.detail)) {
                    NativeMarkdown.bind(progress.view, activity.detail); progress.source = activity.detail;
                }
                next.put(key, progress); views.add(progress.view);
            } else if (group.kind.equals("image")) {
                key = "images:" + i; Images entry = (Images) entries.get(key);
                if (entry == null) entry = new Images(group, i); else entry.bind(group, i);
                next.put(key, entry); views.add(entry.view);
            } else if (group.activities.size() == 1 && !group.compound) {
                key = i + ":0"; Item item = (Item) entries.get(key);
                if (item == null) item = new Item(group.activities.get(0), key); else item.bind(group.activities.get(0));
                next.put(key, item); views.add(item.view);
            } else {
                key = group.compound ? "compound:" + compoundKey(group, i) : Integer.toString(i); Group entry = (Group) entries.get(key);
                if (entry == null) entry = new Group(group, i); else entry.bind(group);
                next.put(key, entry); views.add(entry.view);
            }
        }
        views.add(bottomGap); NativeKeyedViews.sync(list, views); entries = next;
        gaps.keySet().removeIf(index -> index >= groups.size());
    }

    private static String compoundKey(NativeActivityModel.Group group, int index) {
        org.json.JSONObject use = group.activities.get(0).use;
        String id = use == null ? "" : use.optString("id");
        org.json.JSONObject tool = use == null ? null : use.optJSONObject("tool");
        if (id.isEmpty() && tool != null) id = tool.optString("ref");
        return index + ":" + id;
    }

    static ScrollView raw(NativeUi ui, String source) {
        TextView text = ui.text(source, 12, ui.text);
        text.setTypeface(Typeface.MONOSPACE);
        ui.lineHeight(text, 1.6f);
        text.setTextIsSelectable(true);
        ScrollView viewport = new ScrollView(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(ui.dp(260), MeasureSpec.AT_MOST));
            }
        };
        viewport.setNestedScrollingEnabled(true);
        viewport.setOnTouchListener((view, event) -> {
            if (event.getActionMasked() == android.view.MotionEvent.ACTION_DOWN) view.getParent().requestDisallowInterceptTouchEvent(true);
            else if (event.getActionMasked() == android.view.MotionEvent.ACTION_UP || event.getActionMasked() == android.view.MotionEvent.ACTION_CANCEL)
                view.getParent().requestDisallowInterceptTouchEvent(false);
            return false;
        });
        viewport.addView(text, new ScrollView.LayoutParams(-1, -2));
        return viewport;
    }
}
