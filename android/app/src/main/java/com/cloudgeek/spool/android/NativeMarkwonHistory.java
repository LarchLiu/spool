package com.cloudgeek.spool.android;

import android.text.Layout;
import android.text.Spanned;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.recyclerview.widget.ConcatAdapter;
import androidx.recyclerview.widget.LinearLayoutManager;
import androidx.recyclerview.widget.RecyclerView;
import androidx.core.text.PrecomputedTextCompat;
import androidx.core.widget.TextViewCompat;
import io.noties.markwon.AbstractMarkwonPlugin;
import io.noties.markwon.Markwon;
import io.noties.markwon.MarkwonConfiguration;
import io.noties.markwon.core.MarkwonTheme;
import io.noties.markwon.ext.strikethrough.StrikethroughPlugin;
import io.noties.markwon.ext.tables.TablePlugin;
import io.noties.markwon.ext.tasklist.TaskListPlugin;
import io.noties.markwon.recycler.MarkwonAdapter;
import io.noties.markwon.recycler.SimpleEntry;
import java.util.ArrayList;
import java.util.Collections;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.function.Consumer;
import org.commonmark.node.Node;
import org.json.JSONObject;

/** Opt-in trial: the official Markwon block adapter inside one native RecyclerView. */
final class NativeMarkwonHistory extends RecyclerView {
    private final NativeUi ui;
    private final NativeHistoryScroll.State state;
    private final LinearLayoutManager layout;
    private final ConcatAdapter adapters;
    private final Markwon markwon;
    private final PrecomputedTextCompat.Params textMetrics;
    private final ExecutorService parser = Executors.newSingleThreadExecutor();
    private Map<String, Segment> current = new LinkedHashMap<>(), next;
    private Consumer<Boolean> following = value -> {};
    private boolean disposed, updating;
    private long creates, binds, markdownBinds, chromeBinds, recycles, bindNanos, maxBindNanos, parseNanos, parseErrors;
    private long chromeNanos, markdownNanos, chromeReuses, chromeTransfers;
    private long prepareNanos, preparedBlocks, precomputedBlocks, precomputeSkips, precomputeFailures, precomputeFallbacks;
    private static final int CHROME_HEADING = 0x10000000, CHROME_FOOTER = 0x10000001, CHROME_OTHER = 0x10000002;

    private final class Segment {
        final String key;
        final RecyclerView.Adapter<?> adapter;
        String source;
        Future<?> parsing;
        TextEntry textEntry;
        boolean pending;
        Segment(String key, RecyclerView.Adapter<?> adapter) { this.key = key; this.adapter = adapter; }
        void cancel() { if (parsing != null) parsing.cancel(true); pending = false; }
    }

    private final class TextEntry extends SimpleEntry {
        Map<Node, Spanned> prepared = new IdentityHashMap<>();
        TextEntry() { super(R.layout.markwon_block, 0); }
        @Override public SimpleEntry.Holder createHolder(LayoutInflater inflater, ViewGroup parent) {
            SimpleEntry.Holder holder = super.createHolder(inflater, parent);
            TextView text = (TextView) holder.itemView;
            configureText(text);
            creates++;
            return holder;
        }
        @Override public void bindHolder(Markwon renderer, SimpleEntry.Holder holder, Node node) {
            long started = System.nanoTime();
            Spanned text = prepared.get(node);
            if (text == null) super.bindHolder(renderer, holder, node);
            else renderer.setParsedMarkdown((TextView) holder.itemView, text);
            long elapsed = System.nanoTime() - started;
            markdownBinds++; markdownNanos += elapsed; bound(elapsed);
        }
        @Override public void onViewRecycled(SimpleEntry.Holder holder) {
            // Release TextView watchers from the cached document before this holder changes blocks.
            ((TextView) holder.itemView).setText(""); recycles++;
            super.onViewRecycled(holder);
        }
        @Override public void clear() { super.clear(); prepared = new IdentityHashMap<>(); }
    }

    private void configureText(TextView text) {
        text.setTextColor(ui.text); text.setLinkTextColor(ui.primary);
        text.setTextLocale(java.util.Locale.forLanguageTag(ui.locale));
        text.setTypeface(NativeTypefaces.weight(400));
        text.setLineSpacing(text.getTextSize() * .6f, 1);
        text.setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
        text.setHyphenationFrequency(Layout.HYPHENATION_FREQUENCY_NONE);
    }

    /** Its document origin stays local when RecyclerView offsets the item during scrolling. */
    static final class ChromeRow extends LinearLayout {
        ChromeRow(NativeUi ui) { super(ui.context); setOrientation(VERTICAL); }
    }

    private final class ChromeHolder extends RecyclerView.ViewHolder {
        final NativeKeyedViews children;
        ChromeAdapter owner;
        long revision;
        ChromeHolder(LinearLayout view) { super(view); children = new NativeKeyedViews(view); }
    }
    private final class ChromeAdapter extends RecyclerView.Adapter<ChromeHolder> {
        Consumer<NativeKeyedViews> render;
        final int type;
        long revision;
        final Map<Integer, Boolean> expanded = new LinkedHashMap<>();
        ChromeAdapter(int type) { this.type = type; setHasStableIds(true); }
        @Override public int getItemCount() { return 1; }
        @Override public long getItemId(int position) { return 0; }
        @Override public int getItemViewType(int position) { return type; }
        @Override public ChromeHolder onCreateViewHolder(ViewGroup parent, int type) {
            LinearLayout column = new ChromeRow(ui);
            column.setLayoutParams(new RecyclerView.LayoutParams(-1, -2)); creates++;
            return new ChromeHolder(column);
        }
        @Override public void onBindViewHolder(ChromeHolder holder, int position) {
            if (holder.owner == this && holder.revision == revision) { chromeReuses++; return; }
            long started = System.nanoTime();
            boolean transferred = holder.owner != this;
            if (transferred && holder.owner != null) holder.owner.rememberExpanded(holder);
            if (transferred) clearSelection(holder.itemView);
            holder.children.begin(); render.accept(holder.children); holder.children.end();
            if (transferred) {
                List<NativeUserRow> rows = new ArrayList<>(); userRows(holder.itemView, rows);
                for (int i = 0; i < rows.size(); i++) rows.get(i).message.restoreExpanded(expanded.getOrDefault(i, false));
                chromeTransfers++;
            }
            holder.owner = this; holder.revision = revision;
            long elapsed = System.nanoTime() - started;
            chromeBinds++; chromeNanos += elapsed; bound(elapsed);
        }
        @Override public void onViewRecycled(ChromeHolder holder) {
            rememberExpanded(holder); clearSelection(holder.itemView); recycles++;
            // Detached children stop their own requests/timers. Keep this bounded holder's
            // widgets so the next footer/user binding can update them rather than inflate again.
        }
        private void rememberExpanded(ChromeHolder holder) {
            List<NativeUserRow> rows = new ArrayList<>(); userRows(holder.itemView, rows);
            expanded.clear();
            for (int i = 0; i < rows.size(); i++) expanded.put(i, rows.get(i).message.content.expanded);
        }
    }

    private static void userRows(View view, List<NativeUserRow> rows) {
        if (view instanceof NativeUserRow) { rows.add((NativeUserRow) view); return; }
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) userRows(((ViewGroup) view).getChildAt(i), rows);
    }
    private static void clearSelection(View view) {
        if (view instanceof NativeMessageFooter) ((NativeMessageFooter) view).resetFeedback();
        if (view instanceof TextView && ((TextView) view).getText() instanceof android.text.Spannable)
            android.text.Selection.removeSelection((android.text.Spannable) ((TextView) view).getText());
        view.clearFocus();
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) clearSelection(((ViewGroup) view).getChildAt(i));
    }

    NativeMarkwonHistory(NativeUi ui, NativeHistoryScroll.State state, Consumer<String> open) {
        super(ui.context); this.ui = ui; this.state = state;
        layout = new LinearLayoutManager(ui.context);
        layout.setStackFromEnd(true); layout.setInitialPrefetchItemCount(4);
        setLayoutManager(layout); setItemAnimator(null); setHasFixedSize(true);
        setItemViewCacheSize(8); getRecycledViewPool().setMaxRecycledViews(0, 24);
        getRecycledViewPool().setMaxRecycledViews(CHROME_HEADING, 8);
        getRecycledViewPool().setMaxRecycledViews(CHROME_FOOTER, 8);
        getRecycledViewPool().setMaxRecycledViews(CHROME_OTHER, 2);
        setVerticalScrollBarEnabled(false); setClipToPadding(false);
        setPadding(ui.dp(12), ui.dp(25), ui.dp(12), ui.dp(25));
        adapters = new ConcatAdapter(new ConcatAdapter.Config.Builder()
                .setIsolateViewTypes(false)
                .setStableIdMode(ConcatAdapter.Config.StableIdMode.ISOLATED_STABLE_IDS).build());
        setAdapter(adapters);
        markwon = Markwon.builder(ui.context)
                .textSetter((view, text, bufferType, complete) -> {
                    if (text instanceof PrecomputedTextCompat) {
                        try { TextViewCompat.setPrecomputedText(view, (PrecomputedTextCompat) text); }
                        catch (IllegalArgumentException metricsChanged) {
                            // A font-scale/configuration change can invalidate a prepared paint.
                            // Keep the current widget's metrics and synchronously lay out a copy.
                            precomputeFallbacks++; view.setText(new android.text.SpannableStringBuilder(text), bufferType);
                        }
                    } else view.setText(text, bufferType);
                    complete.run();
                })
                .usePlugin(TablePlugin.create(ui.context))
                .usePlugin(StrikethroughPlugin.create())
                .usePlugin(TaskListPlugin.create(ui.context))
                .usePlugin(new AbstractMarkwonPlugin() {
                    @Override public void configureParser(org.commonmark.parser.Parser.Builder builder) {
                        builder.customInlineContentParserFactory(new NativeCodexFileCitation.Factory());
                    }
                    @Override public void configureVisitor(io.noties.markwon.MarkwonVisitor.Builder builder) {
                        builder.on(NativeCodexFileCitation.class, (visitor, citation) -> {
                            for (Node parent = citation.getParent(); parent != null; parent = parent.getParent())
                                if (parent instanceof org.commonmark.node.Link || parent instanceof org.commonmark.node.Image) {
                                    visitor.builder().append(citation.source); return;
                                }
                            int start = visitor.length(); visitor.builder().append(citation.name);
                            visitor.setSpans(start, new android.text.style.URLSpan(citation.path.replace('\\', '/')) {
                                @Override public void onClick(View view) { open.accept(getURL()); }
                            });
                        });
                    }
                    @Override public void configureConfiguration(MarkwonConfiguration.Builder builder) {
                        builder.linkResolver((view, link) -> open.accept(link));
                    }
                    @Override public void configureTheme(MarkwonTheme.Builder builder) {
                        builder.linkColor(ui.primary).codeBackgroundColor(ui.soft).codeTextColor(ui.text)
                                .blockQuoteColor(ui.line);
                    }
                }).build();
        TextView prototype = (TextView) LayoutInflater.from(ui.context).inflate(R.layout.markwon_block, this, false);
        configureText(prototype); textMetrics = TextViewCompat.getTextMetricsParams(prototype);
        // Prepare data before publishing nodes. Never asynchronously setText on a holder:
        // upstream PrecomputedTextSetterCompat cannot safely track RecyclerView ownership.
        addOnScrollListener(new OnScrollListener() {
            @Override public void onScrolled(RecyclerView view, int dx, int dy) {
                if (disposed || updating || dy == 0) return;
                state.following = !canScrollVertically(1);
                remember(); following.accept(state.following);
            }
            @Override public void onScrollStateChanged(RecyclerView view, int value) { if (!disposed) remember(); }
        });
    }

    private void bound(long elapsed) { binds++; bindNanos += elapsed; maxBindNanos = Math.max(maxBindNanos, elapsed); }
    void onFollowing(Consumer<Boolean> listener) { following = listener; following.accept(state.following); }

    void begin() { next = new LinkedHashMap<>(); }

    void chrome(String key, boolean refresh, Consumer<NativeKeyedViews> render) {
        Segment segment = current.get(key);
        if (segment == null || !(segment.adapter instanceof NativeMarkwonHistory.ChromeAdapter))
            segment = new Segment(key, new ChromeAdapter(key.endsWith("/heading") ? CHROME_HEADING : key.endsWith("/footer") ? CHROME_FOOTER : CHROME_OTHER));
        ChromeAdapter adapter = (ChromeAdapter) segment.adapter;
        boolean changed = adapter.render == null || refresh;
        if (changed) { adapter.render = render; adapter.revision++; adapter.notifyItemChanged(0); }
        next.put(key, segment);
    }

    void markdown(String key, String source) {
        Segment segment = current.get(key);
        if (segment == null || !(segment.adapter instanceof MarkwonAdapter)) {
            TextEntry entry = new TextEntry();
            segment = new Segment(key, MarkwonAdapter.create(entry)); segment.textEntry = entry;
        }
        next.put(key, segment);
        if (source.equals(segment.source)) return;
        segment.cancel(); segment.source = source; segment.pending = true;
        Segment target = segment;
        segment.parsing = parser.submit(() -> {
            long started = System.nanoTime();
            try {
                Node document = markwon.parse(source);
                long elapsed = System.nanoTime() - started;
                List<Node> nodes = io.noties.markwon.MarkwonReducer.directChildren().reduce(document);
                Map<Node, Spanned> prepared = new IdentityHashMap<>();
                long preparing = System.nanoTime(); int precomputed = 0, skipped = 0, failures = 0;
                for (Node node : nodes) {
                    if (Thread.currentThread().isInterrupted()) return;
                    Spanned text = markwon.render(node);
                    // Tables and other replacements can need an attached TextView's width.
                    // Keep their native measuring path; only immutable text runs are precomputed.
                    if (text.getSpans(0, text.length(), android.text.style.ReplacementSpan.class).length == 0) {
                        try {
                            PrecomputedTextCompat measured = PrecomputedTextCompat.create(text, textMetrics);
                            text = measured;
                            precomputed++;
                        } catch (RuntimeException error) { failures++; }
                    } else skipped++;
                    prepared.put(node, text);
                }
                long prepareElapsed = System.nanoTime() - preparing;
                int measured = precomputed, replacements = skipped, failed = failures;
                post(() -> {
                    if (!current(target, source)) return;
                    prepareNanos += prepareElapsed; preparedBlocks += nodes.size(); precomputedBlocks += measured;
                    precomputeSkips += replacements; precomputeFailures += failed;
                    parsed(target, source, nodes, prepared, elapsed, false);
                });
            } catch (RuntimeException | LinkageError error) {
                org.commonmark.node.Document document = new org.commonmark.node.Document();
                org.commonmark.node.Paragraph paragraph = new org.commonmark.node.Paragraph();
                paragraph.appendChild(new org.commonmark.node.Text(source)); document.appendChild(paragraph);
                post(() -> parsed(target, source, io.noties.markwon.MarkwonReducer.directChildren().reduce(document), new IdentityHashMap<>(), System.nanoTime() - started, true));
            }
        });
    }

    private boolean current(Segment target, String source) { return !disposed && current.get(target.key) == target && source.equals(target.source); }

    private void parsed(Segment target, String source, List<Node> nodes, Map<Node, Spanned> prepared, long elapsed, boolean failed) {
        if (!current(target, source)) return;
        target.pending = false; parseNanos += elapsed; if (failed) parseErrors++;
        MarkwonAdapter adapter = (MarkwonAdapter) target.adapter;
        int before = adapter.getItemCount();
        adapter.setParsedMarkdown(markwon, nodes); target.textEntry.prepared = prepared;
        int after = adapter.getItemCount(), retained = Math.min(before, after);
        if (retained > 0) adapter.notifyItemRangeChanged(0, retained);
        if (after > before) adapter.notifyItemRangeInserted(before, after - before);
        else if (before > after) adapter.notifyItemRangeRemoved(after, before - after);
        settlePosition();
    }

    void end() {
        updating = true;
        try {
            Set<RecyclerView.Adapter<?>> wanted = Collections.newSetFromMap(new IdentityHashMap<>());
            for (Segment segment : next.values()) wanted.add(segment.adapter);
            for (Segment segment : current.values()) if (!wanted.contains(segment.adapter)) segment.cancel();
            for (RecyclerView.Adapter<?> adapter : new ArrayList<>(adapters.getAdapters()))
                if (!wanted.contains(adapter)) adapters.removeAdapter(adapter);
            int position = 0;
            for (Segment segment : next.values()) {
                List<? extends RecyclerView.Adapter<?>> attached = adapters.getAdapters();
                if (position >= attached.size() || attached.get(position) != segment.adapter) {
                    adapters.removeAdapter(segment.adapter); adapters.addAdapter(position, segment.adapter);
                }
                position++;
            }
            current = next; next = null;
        } finally { updating = false; }
        settlePosition();
    }

    private void settlePosition() {
        post(() -> {
            if (disposed) return;
            if (state.following) latest(false);
            else if (!state.recyclerKey.isEmpty() && getScrollState() == SCROLL_STATE_IDLE) {
                int position = 0;
                for (Segment segment : current.values()) {
                    if (segment.key.equals(state.recyclerKey)) {
                        layout.scrollToPositionWithOffset(position + Math.min(state.recyclerItem, Math.max(0, segment.adapter.getItemCount() - 1)), state.recyclerOffset);
                        break;
                    }
                    position += segment.adapter.getItemCount();
                }
            }
        });
    }

    private void remember() {
        int first = layout.findFirstVisibleItemPosition();
        View item = layout.findViewByPosition(first);
        if (item == null) return;
        int position = first;
        for (Segment segment : current.values()) {
            int count = segment.adapter.getItemCount();
            if (position < count) {
                state.recyclerKey = segment.key; state.recyclerItem = position;
                state.recyclerOffset = layout.getDecoratedTop(item) - getPaddingTop();
                state.top = computeVerticalScrollOffset(); return;
            }
            position -= count;
        }
    }

    void preservePosition(View heading) { state.following = false; remember(); following.accept(false); }
    void latest() { latest(true); }
    private void latest(boolean animate) {
        if (disposed || adapters.getItemCount() == 0) return;
        state.following = true; following.accept(true);
        if (animate) smoothScrollToPosition(adapters.getItemCount() - 1);
        else layout.scrollToPosition(adapters.getItemCount() - 1);
    }
    int contentHeight() { return computeVerticalScrollRange(); }
    int contentOffset() { return computeVerticalScrollOffset(); }
    int blocks() { return adapters.getItemCount(); }
    int pending() { int count = 0; for (Segment segment : current.values()) if (segment.pending) count++; return count; }
    JSONObject metrics() {
        JSONObject result = new JSONObject();
        try { result.put("renderer", "markwon-recycler").put("blocks", blocks()).put("pendingParses", pending())
                .put("creates", creates).put("binds", binds).put("markdownBinds", markdownBinds).put("chromeBinds", chromeBinds)
                .put("recycles", recycles).put("bindMs", bindNanos / 1_000_000d).put("maxBindMs", maxBindNanos / 1_000_000d)
                .put("chromeBindMs", chromeNanos / 1_000_000d).put("markdownBindMs", markdownNanos / 1_000_000d)
                .put("chromeReuses", chromeReuses).put("chromeTransfers", chromeTransfers)
                .put("timeCacheHits", NativeMessageTime.cacheHits()).put("timeFormats", NativeMessageTime.formats())
                .put("timeFormatterCreates", NativeMessageTime.formatterCreates())
                .put("prepareMs", prepareNanos / 1_000_000d).put("preparedBlocks", preparedBlocks).put("precomputedBlocks", precomputedBlocks)
                .put("precomputeSkips", precomputeSkips).put("precomputeFailures", precomputeFailures).put("precomputeFallbacks", precomputeFallbacks)
                .put("parseMs", parseNanos / 1_000_000d).put("parseErrors", parseErrors); }
        catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return result;
    }
    void dispose() {
        if (disposed) return;
        remember(); disposed = true; stopScroll(); parser.shutdownNow();
        for (Segment segment : current.values()) segment.cancel();
        setAdapter(null); getRecycledViewPool().clear(); current.clear(); following = value -> {};
    }
}
