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
    private static final class Item {
        final NativeUi ui;
        NativeActivityList owner;
        boolean prepared;
        final LinearLayout view;
        final NativeDisclosureRow row;
        String id;
        NativeToolDetail detail;
        NativeActivityModel.Activity current;
        String previewSource, previewLabel;
        CharSequence previewTitle;
        Item(NativeActivityList owner, NativeActivityModel.Activity activity, String id, boolean prepared) {
            this(owner, activity, id, prepared, null);
        }
        Item(NativeActivityList owner, NativeActivityModel.Activity activity, String id, boolean prepared, LinearLayout container) {
            this.owner = owner; this.ui = owner.ui; this.prepared = prepared;
            this.id = id; view = container == null ? ui.column() : container;
            row = new NativeDisclosureRow(ui, "", NativeActivityModel.icon(activity.kind), "", false, false, false, () -> {});
            view.addView(row, new LayoutParams(-1, -2));
            row.setOnClickListener(button -> {
                if (current.noDetails) return;
                if (!this.owner.state.items.add(this.id)) this.owner.state.items.remove(this.id);
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
                    previewTitle = owner.previewTitle(activity.label, preview);
                    previewSource = preview; previewLabel = activity.label;
                }
                title = previewTitle;
            } else { previewSource = previewLabel = null; previewTitle = null; }
            row.bind(title, NativeActivityModel.icon(activity.kind), prepared ? owner.itemStatistics(activity)
                    : NativeActivityModel.changeStats(java.util.Collections.singletonList(activity)), prepared || owner.rowsChanged == null);
            row.setDisclosureEnabled(!activity.noDetails);
            row.setExpanded(!activity.noDetails && owner.state.items.contains(id));
            if (!activity.noDetails && owner.state.items.contains(id)) {
                if (detail == null) {
                    detail = new NativeToolDetail(ui, activity, owner.openLink);
                    LayoutParams params = new LayoutParams(-1, -2);
                    params.setMarginStart(ui.dp(20)); params.topMargin = ui.dp(3); params.bottomMargin = ui.dp(8);
                    view.addView(detail, params);
                } else detail.bind(activity);
            } else if (detail != null) {
                view.removeView(detail); detail = null;
            }
        }
        void bind(NativeActivityList nextOwner, NativeActivityModel.Activity activity, String nextId, boolean prepared) {
            if (owner != nextOwner || !id.equals(nextId)) {
                if (detail != null) { view.removeView(detail); detail = null; }
                id = nextId; owner = nextOwner;
            }
            this.prepared = prepared;
            bind(activity);
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
                    Item item = items.get(key); if (item == null) item = new Item(NativeActivityList.this, activity, key, false); else item.bind(activity);
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
        int index;
        Images(NativeActivityModel.Group group, int index) {
            heading = new NativeDisclosureRow(ui, "", "image", "", false, true, false, () -> {});
            heading.setOnClickListener(v -> {
                state.imageGroups.put(this.index, !state.imageGroups.getOrDefault(this.index, state.live));
                bind(current, this.index);
            });
            view.addView(heading, new LayoutParams(-1, -2)); bind(group, index);
        }
        void bind(NativeActivityModel.Group group, int index) {
            this.index = index; current = group; heading.bind(group.label, "image", "");
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
    private Runnable rowsChanged;
    private NativeTurnPresentation virtualPresentation;
    private final java.util.LinkedHashMap<String, CharSequence> previews = new java.util.LinkedHashMap<>(16, .75f, true);
    private int previewCharacters;
    private final Map<Object, String> statistics = new java.util.IdentityHashMap<>();

    private String itemStatistics(NativeActivityModel.Activity activity) {
        return statistics.computeIfAbsent(activity, ignored -> NativeActivityModel.changeStats(java.util.Collections.singletonList(activity)));
    }

    private String groupStatistics(NativeActivityModel.Group group) {
        return statistics.computeIfAbsent(group, ignored -> NativeActivityModel.changeStats(group.activities));
    }

    private CharSequence previewTitle(String label, String source) {
        String key = label + "\n" + source;
        CharSequence cached = previews.get(key); if (cached != null) return cached;
        long started = System.nanoTime();
        SpannableStringBuilder title = new SpannableStringBuilder(label + " \u00b7 ");
        title.append(NativeMarkdown.preview(ui, source));
        if (ui.toolDetails != null) ui.toolDetails.rendered("thinking_preview", System.nanoTime() - started);
        int size = key.length() + title.length();
        if (size <= 32768) {
            previews.put(key, title); previewCharacters += size;
            while (previews.size() > 64 || previewCharacters > 131072) {
                java.util.Map.Entry<String, CharSequence> oldest = previews.entrySet().iterator().next();
                previewCharacters -= oldest.getKey().length() + oldest.getValue().length(); previews.remove(oldest.getKey());
            }
        }
        return title;
    }

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
        this(ui, parts, backend, summary, live, state, openLink, images, null);
    }

    NativeActivityList(NativeUi ui, List<org.json.JSONObject> parts, String backend, CharSequence summary,
            boolean live, State state, Consumer<String> openLink, NativeTranscriptImages images, Runnable rowsChanged) {
        this(ui, state, openLink, images);
        this.rowsChanged = rowsChanged;
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
        previews.clear(); previewCharacters = 0; statistics.clear();
        groups = new ArrayList<>(); entries.clear(); gaps.clear(); list.removeAllViews();
    }

    void bindParts(List<org.json.JSONObject> parts, String backend, CharSequence summary, boolean live) {
        deferredParts = parts; deferredBackend = backend;
        if (rowsChanged != null) {
            // Recycler history owns each expanded body row. This widget only paints its summary.
            groups = parts.isEmpty() ? java.util.Collections.emptyList() : java.util.Collections.singletonList(new NativeActivityModel.Group());
            state.update(live, state.count); update(); setSummary(summary); return;
        }
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

    void rowsChanged(Runnable changed) { rowsChanged = changed; }

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
                    if (rowsChanged != null) { update(); rowsChanged.run(); return; }
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
        if (rowsChanged == null && !groups.isEmpty() && state.open) { buildGroups(); views.add(list); }
        else { list.removeAllViews(); entries.clear(); gaps.clear(); }
        NativeKeyedViews.sync(this, views);
    }

    /** A lightweight row factory; it never constructs the complete expanded View tree. */
    static NativeActivityList virtualRows(NativeUi ui, State state, Consumer<String> open,
            NativeTranscriptImages images, Runnable changed) {
        NativeActivityList result = new NativeActivityList(ui, state, open, images);
        result.rowsChanged = changed; return result;
    }

    void bindVirtual(NativeTurnPresentation presentation, UserInput input) {
        userInput = input;
        if (virtualPresentation == presentation) return;
        virtualPresentation = presentation;
        statistics.clear();
        groups = NativeActivityModel.groups(ui, NativeActivityModel.fromParts(ui, presentation.process, presentation.turn.optString("backend")));
        state.update(presentation.running && !presentation.answered, groups.size());
    }

    /** Deterministic debug setup: expanded groups/images and one detail beside the middle image group. */
    int probeExpansion() {
        state.open = true; state.groups.clear(); state.compounds.clear(); state.items.clear(); state.imageGroups.clear();
        List<Integer> images = new ArrayList<>();
        for (int i = 0; i < groups.size(); i++) {
            NativeActivityModel.Group group = groups.get(i);
            state.groups.add(i);
            if (group.compound) state.compounds.add(compoundKey(group, i));
            if (group.kind.equals("image")) { state.imageGroups.put(i, true); images.add(i); }
        }
        int anchor = images.isEmpty() ? groups.size() / 2 : images.get(images.size() / 2);
        for (int i = anchor - 1; i >= 0; i--) {
            NativeActivityModel.Group group = groups.get(i);
            if (group.kind.equals("user") || group.kind.equals("message") || group.kind.equals("image")) continue;
            for (int j = group.activities.size() - 1; j >= 0; j--) if (!group.activities.get(j).noDetails) {
                state.items.add(i + ":" + j); return anchor;
            }
        }
        return anchor;
    }

    private static final class VirtualRow extends LinearLayout {
        Object value;
        boolean nested;
        final Paint border = new Paint();
        final NativeUi ui;
        VirtualRow(NativeUi ui) { super(ui.context); this.ui = ui; setOrientation(VERTICAL); }
        @Override protected void dispatchDraw(Canvas canvas) {
            if (nested) { border.setColor(ui.line); canvas.drawRect(0, 0, ui.dp(1), getHeight(), border); }
            super.dispatchDraw(canvas);
        }
    }

    private VirtualRow virtualRow(NativeMarkwonHistory history, NativeKeyedViews children, String kind, int gap) {
        // Tool widgets have an explicit owner that is rebound with their state and actions.
        // User/image/progress widgets still close over their factory and retain that lifetime.
        Object signature = history.reuseActivityRows && (kind.equals("item") || kind.equals("group")) ? kind : this;
        VirtualRow row = (VirtualRow) children.use("activity-" + kind, signature, () -> {
            history.activityInflations++; return new VirtualRow(ui);
        });
        row.setPadding(0, ui.dp(gap), 0, 0); return row;
    }

    void appendVirtualRows(NativeMarkwonHistory history, String prefix, boolean refresh) {
        if (!state.open) return;
        for (int index = 0; index < groups.size(); index++) {
            NativeActivityModel.Group group = groups.get(index); int at = index, gap = index == 0 ? 0 : 2;
            String key = prefix + "/group:" + index;
            if (group.kind.equals("user")) {
                history.activity(key, "user", refresh, children -> {
                    VirtualRow row = virtualRow(history, children, "user", gap);
                    if (row.value == null) {
                        User user = new User(); row.value = user;
                        LayoutParams params = new LayoutParams(-1, -2); params.topMargin = ui.dp(10); params.bottomMargin = ui.dp(10);
                        row.addView(user.view, params);
                    }
                    ((User) row.value).bind(group.activities.get(0));
                });
            } else if (group.kind.equals("message")) {
                history.activity(key, "message", refresh, children -> {
                    VirtualRow row = virtualRow(history, children, "message", gap);
                    String source = group.activities.get(0).detail;
                    if (row.value == null) {
                        LinearLayout markdown = NativeMarkdown.progress(ui, source, openLink);
                        markdown.setPadding(ui.dp(5), ui.dp(6), ui.dp(5), ui.dp(10));
                        row.value = new Progress(markdown, source); row.addView(markdown, new LayoutParams(-1, -2));
                    } else {
                        Progress progress = (Progress) row.value;
                        if (!progress.source.equals(source)) { NativeMarkdown.bind(progress.view, source); progress.source = source; }
                    }
                });
            } else if (group.kind.equals("image")) {
                history.activity(key, "image", refresh, children -> {
                    VirtualRow row = virtualRow(history, children, history.reuseActivityRows ? "image" : "image:" + at, gap);
                    if (row.value == null) { Images images = new Images(group, at); row.value = images; row.addView(images.view, new LayoutParams(-1, -2)); }
                    else ((Images) row.value).bind(group, at);
                });
            } else if (group.activities.size() == 1 && !group.compound) {
                appendVirtualItem(history, key, refresh, group.activities.get(0), at + ":0", gap, false, false, false);
            } else {
                history.activity(key, "group", refresh, children -> {
                    VirtualRow row = virtualRow(history, children, "group", gap);
                    if (row.value == null) {
                        NativeDisclosureRow heading = new NativeDisclosureRow(ui, "", NativeActivityModel.icon(group.kind), "", false, true, false, () -> {});
                        row.value = heading; row.addView(heading, new LayoutParams(-1, -2));
                    }
                    NativeDisclosureRow heading = (NativeDisclosureRow) row.value;
                    heading.bind(group.label, NativeActivityModel.icon(group.kind), history.reuseActivityRows ? groupStatistics(group) : NativeActivityModel.changeStats(group.activities),
                            history.reuseActivityRows);
                    heading.setExpanded(group.compound ? state.compounds.contains(compoundKey(group, at)) : state.groups.contains(at));
                    heading.setOnClickListener(view -> {
                        if (group.compound) { String id = compoundKey(group, at); if (!state.compounds.add(id)) state.compounds.remove(id); }
                        else if (!state.groups.add(at)) state.groups.remove(at);
                        rowsChanged.run();
                    });
                });
                boolean expanded = group.compound ? state.compounds.contains(compoundKey(group, at)) : state.groups.contains(at);
                if (expanded) for (int i = 0; i < group.activities.size(); i++)
                    appendVirtualItem(history, key + "/item:" + i, refresh, group.activities.get(i), at + ":" + i,
                            0, true, i == 0, i == group.activities.size() - 1);
            }
        }
        if (!groups.isEmpty()) history.activity(prefix + "/end", "gap", false, children -> children.use("gap", "gap", () -> ui.space(10)));
    }

    private void appendVirtualItem(NativeMarkwonHistory history, String key, boolean refresh, NativeActivityModel.Activity activity,
            String id, int gap, boolean nested, boolean first, boolean last) {
        history.activity(key, "item", refresh, children -> {
            VirtualRow row = virtualRow(history, children, "item", gap);
            row.nested = nested;
            LayoutParams params = new LayoutParams(-1, -2); params.setMarginStart(nested ? ui.dp(20) : 0);
            if (nested) row.setPadding(ui.dp(9), first ? ui.dp(2) : 0, 0, last ? ui.dp(7) : 0);
            row.setLayoutParams(params);
            if (row.value == null) {
                Item item = new Item(this, activity, id, history.reuseActivityRows, history.reuseActivityRows ? row : null);
                row.value = item;
                if (item.view != row) row.addView(item.view, new LayoutParams(-1, -2));
            } else ((Item) row.value).bind(this, activity, id, history.reuseActivityRows);
        });
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
                if (item == null) item = new Item(this, group.activities.get(0), key, false); else item.bind(group.activities.get(0));
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
