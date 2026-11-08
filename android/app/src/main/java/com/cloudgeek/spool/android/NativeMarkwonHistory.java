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
import androidx.recyclerview.widget.DiffUtil;
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
import java.util.ArrayDeque;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.function.Consumer;
import org.commonmark.node.Node;
import org.commonmark.node.FencedCodeBlock;
import org.commonmark.node.IndentedCodeBlock;
import org.json.JSONObject;

/** Chat history with the official Markwon block adapter inside one native RecyclerView. */
final class NativeMarkwonHistory extends RecyclerView {
    private final NativeUi ui;
    private final NativeHistoryScroll.State state;
    private final LinearLayoutManager layout;
    private final ConcatAdapter adapters;
    private final Markwon markwon;
    private final android.os.Handler main = new android.os.Handler(android.os.Looper.getMainLooper());
    private final PrecomputedTextCompat.Params textMetrics;
    private final ExecutorService parser = Executors.newSingleThreadExecutor();
    private Map<String, Segment> current = new LinkedHashMap<>(), next;
    private Consumer<Boolean> following = value -> {};
    private boolean disposed, updating, restoring, movingToLatest, explicitScroll;
    private java.lang.ref.WeakReference<View> disclosureHeading;
    private int disclosureTop;
    private final List<RecyclerView.Adapter<?>> adapterOrder = new ArrayList<>();
    private final Map<RecyclerView.Adapter<?>, Segment> owners = new IdentityHashMap<>();
    private final Map<View, RowAnchor> rowAnchors = new java.util.WeakHashMap<>();
    private int rowCount;
    private long nextBlockId, reusedBlocks, reconciledSegments;
    private long creates, binds, markdownBinds, chromeBinds, recycles, bindNanos, maxBindNanos, parseNanos, parseErrors;
    private long chromeNanos, markdownNanos, chromeReuses, chromeTransfers;
    boolean reuseActivityRows = true;
    long activityInflations;
    private long prepareNanos, preparedBlocks, precomputedBlocks, precomputeSkips, precomputeFailures, precomputeFallbacks;
    private static final int CHROME_HEADING = 0x10000000, CHROME_FOOTER = 0x10000001,
            CHROME_OTHER = 0x10000002, CHROME_ACTIVITY = 0x10000003;

    private final class Segment {
        final String key;
        final RecyclerView.Adapter<?> adapter;
        String source;
        Future<?> parsing;
        TextEntry textEntry;
        boolean pending;
        long version;
        int position;
        List<NativeMarkdownBlocks.Block> blocks = Collections.emptyList();
        Segment(String key, RecyclerView.Adapter<?> adapter) { this.key = key; this.adapter = adapter; }
        void cancel() { version++; if (parsing != null) parsing.cancel(true); pending = false; }
    }

    /** Bound row identity avoids walking ConcatAdapter's segments on every scroll frame. */
    private final class RowAnchor {
        final Segment segment;
        final Node node;
        RowAnchor(Segment segment, Node node) { this.segment = segment; this.node = node; }
        int item() { return node == null ? 0 : segment.textEntry.positions.getOrDefault(node, -1); }
        long id() { return node == null ? 0 : segment.textEntry.ids.getOrDefault(node, -1L); }
        boolean current() { return segment != null && current.get(segment.key) == segment; }
    }

    private final class TextEntry extends SimpleEntry {
        Map<Node, Spanned> prepared = new IdentityHashMap<>();
        Map<Node, Long> ids = new IdentityHashMap<>();
        Map<Node, Integer> positions = new IdentityHashMap<>();
        Segment segment;
        TextEntry() { super(R.layout.markwon_block, 0); }
        @Override public long id(Node node) { return ids.get(node); }
        @Override public SimpleEntry.Holder createHolder(LayoutInflater inflater, ViewGroup parent) {
            SimpleEntry.Holder holder = super.createHolder(inflater, parent);
            TextView text = (TextView) holder.itemView;
            configureText(text);
            creates++;
            return holder;
        }
        @Override public void bindHolder(Markwon renderer, SimpleEntry.Holder holder, Node node) {
            long started = System.nanoTime();
            rowAnchors.put(holder.itemView, new RowAnchor(segment, node));
            Spanned text = prepared.get(node);
            if (text == null) super.bindHolder(renderer, holder, node);
            else renderer.setParsedMarkdown((TextView) holder.itemView, text);
            long elapsed = System.nanoTime() - started;
            markdownBinds++; markdownNanos += elapsed; bound(elapsed);
        }
        @Override public void onViewRecycled(SimpleEntry.Holder holder) {
            // Release TextView watchers from the cached document before this holder changes blocks.
            rowAnchors.remove(holder.itemView); ((TextView) holder.itemView).setText(""); recycles++;
            super.onViewRecycled(holder);
        }
        @Override public void clear() {
            super.clear(); prepared = new IdentityHashMap<>(); ids = new IdentityHashMap<>();
            positions = new IdentityHashMap<>();
        }
    }

    private static final class CodeHolder extends MarkwonAdapter.Holder {
        final NativeCodeBlock code;
        CodeHolder(View view, NativeCodeBlock code) { super(view); this.code = code; }
    }
    private final class CodeEntry extends MarkwonAdapter.Entry<Node, CodeHolder> {
        final TextEntry textEntry;
        CodeEntry(TextEntry textEntry) { this.textEntry = textEntry; }
        @Override public long id(Node node) { return textEntry.ids.get(node); }
        @Override public CodeHolder createHolder(LayoutInflater inflater, ViewGroup parent) {
            // Use the same code widget as file Markdown previews, with a row-local origin.
            ChromeRow row = new ChromeRow(ui);
            row.setLayoutParams(new RecyclerView.LayoutParams(-1, -2));
            row.setPadding(0, ui.dp(4), 0, ui.dp(8));
            NativeCodeBlock code = new NativeCodeBlock(ui, ui.text, 14);
            row.addView(code, new LinearLayout.LayoutParams(-1, -2)); creates++;
            return new CodeHolder(row, code);
        }
        @Override public void bindHolder(Markwon renderer, CodeHolder holder, Node node) {
            long started = System.nanoTime();
            rowAnchors.put(holder.itemView, new RowAnchor(textEntry.segment, node));
            holder.code.bind(node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getInfo() : "", codeLiteral(node));
            long elapsed = System.nanoTime() - started;
            markdownBinds++; markdownNanos += elapsed; bound(elapsed);
        }
        @Override public void onViewRecycled(CodeHolder holder) {
            // This widget owns its text buffer; unlike Markwon's shared prepared text it
            // cannot retain a holder through a document cache. Keep the bounded pool's
            // layout/tokens until the next bind, which replaces changed source/language.
            rowAnchors.remove(holder.itemView);
            // Legacy intrinsic paragraph rounding depends on a fresh layout (also below).
            if (android.os.Build.VERSION.SDK_INT < 28) holder.code.bind("", "");
            clearSelection(holder.itemView); holder.code.resetFeedback(); holder.code.scroll.scrollTo(0, 0); recycles++;
        }
    }
    private static boolean codeBlock(Node node) { return node instanceof FencedCodeBlock || node instanceof IndentedCodeBlock; }
    private static String codeLiteral(Node node) {
        return node instanceof FencedCodeBlock ? ((FencedCodeBlock) node).getLiteral() : ((IndentedCodeBlock) node).getLiteral();
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
            rowAnchors.put(holder.itemView, new RowAnchor(owners.get(this), null));
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
            rowAnchors.remove(holder.itemView);
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
        if (view.isFocused()) view.clearFocus();
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) clearSelection(((ViewGroup) view).getChildAt(i));
    }

    NativeMarkwonHistory(NativeUi ui, NativeHistoryScroll.State state, Consumer<String> open) {
        super(ui.context); this.ui = ui; this.state = state;
        layout = new LinearLayoutManager(ui.context);
        // Welcome and short conversations start at the top; restorePosition follows long histories.
        layout.setStackFromEnd(false); layout.setInitialPrefetchItemCount(4);
        setLayoutManager(layout); setItemAnimator(null); setHasFixedSize(true);
        setItemViewCacheSize(8); getRecycledViewPool().setMaxRecycledViews(0, 24);
        getRecycledViewPool().setMaxRecycledViews(CHROME_HEADING, 8);
        getRecycledViewPool().setMaxRecycledViews(CHROME_FOOTER, 8);
        getRecycledViewPool().setMaxRecycledViews(CHROME_OTHER, 2);
        for (int type = CHROME_ACTIVITY; type < CHROME_ACTIVITY + 6; type++)
            getRecycledViewPool().setMaxRecycledViews(type, type == CHROME_ACTIVITY + 4 ? 32 : 8);
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
                .usePlugin(new NativeMarkwonLinks(open))
                .usePlugin(new AbstractMarkwonPlugin() {
                    @Override public void configureConfiguration(MarkwonConfiguration.Builder builder) {
                        builder.linkResolver((view, link) -> open.accept(link));
                        // Nested fences inside lists/quotes still use Markwon spans. Their
                        // tokens are prepared with the containing block on the parser worker.
                        builder.syntaxHighlight((language, source) -> {
                            if (ui.syntax == null || android.os.Looper.myLooper() == android.os.Looper.getMainLooper()) return source;
                            try {
                                android.text.SpannableString text = new android.text.SpannableString(source);
                                NativeSyntax.apply(text, ui.syntax.computeCode(language, source, ui.dark, ui.syntax.epoch()), ui.text);
                                return text;
                            } catch (java.util.concurrent.CancellationException cancelled) { throw cancelled; }
                            catch (Exception | LinkageError error) {
                                android.util.Log.w("SpoolSyntax", "Markdown highlighting failed", error); return source;
                            }
                        });
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
                if (disposed || updating || restoring || dy == 0) return;
                if (movingToLatest) return;
                disclosureHeading = null;
                boolean wasFollowing = state.following;
                state.following = nearBottom();
                remember();
                if (wasFollowing != state.following) following.accept(state.following);
            }
            @Override public void onScrollStateChanged(RecyclerView view, int value) {
                if (disposed) return;
                if (value == SCROLL_STATE_DRAGGING) { movingToLatest = false; disclosureHeading = null; }
                if (value == SCROLL_STATE_IDLE) { movingToLatest = false; state.top = computeVerticalScrollOffset(); }
                if (!restoring) remember();
            }
        });
    }

    private void bound(long elapsed) { binds++; bindNanos += elapsed; maxBindNanos = Math.max(maxBindNanos, elapsed); }
    void onFollowing(Consumer<Boolean> listener) { following = listener; following.accept(state.following); }

    void begin() { next = new LinkedHashMap<>(); }

    void chrome(String key, boolean refresh, Consumer<NativeKeyedViews> render) {
        chrome(key, key.endsWith("/heading") ? CHROME_HEADING : key.endsWith("/footer") ? CHROME_FOOTER : CHROME_OTHER, refresh, render);
    }

    void activity(String key, String kind, boolean refresh, Consumer<NativeKeyedViews> render) {
        int type;
        switch (kind) {
            case "user": type = 0; break;
            case "message": type = 1; break;
            case "image": type = 2; break;
            case "group": type = 3; break;
            case "item": type = 4; break;
            default: type = 5;
        }
        chrome(key, CHROME_ACTIVITY + type, refresh, render);
    }

    private void chrome(String key, int type, boolean refresh, Consumer<NativeKeyedViews> render) {
        Segment segment = current.get(key);
        if (segment == null || !(segment.adapter instanceof NativeMarkwonHistory.ChromeAdapter) || ((ChromeAdapter) segment.adapter).type != type)
            segment = new Segment(key, new ChromeAdapter(type));
        ChromeAdapter adapter = (ChromeAdapter) segment.adapter;
        boolean changed = adapter.render == null || refresh;
        if (changed) { adapter.render = render; adapter.revision++; adapter.notifyItemChanged(0); }
        next.put(key, segment);
    }

    void markdown(String key, String source) {
        Segment segment = current.get(key);
        if (segment == null || !(segment.adapter instanceof MarkwonAdapter)) {
            TextEntry entry = new TextEntry();
            CodeEntry code = new CodeEntry(entry);
            segment = new Segment(key, MarkwonAdapter.builder(entry)
                    .include(FencedCodeBlock.class, code).include(IndentedCodeBlock.class, code).build()); segment.textEntry = entry; entry.segment = segment;
        }
        next.put(key, segment);
        if (source.equals(segment.source)) return;
        segment.cancel(); segment.source = source; segment.pending = true;
        Segment target = segment; long version = segment.version;
        List<NativeMarkdownBlocks.Block> previous = segment.blocks;
        segment.parsing = parser.submit(() -> {
            long started = System.nanoTime();
            try {
                Node document = markwon.parse(source);
                long elapsed = System.nanoTime() - started;
                List<Node> nodes = io.noties.markwon.MarkwonReducer.directChildren().reduce(document);
                Map<String, ArrayDeque<NativeMarkdownBlocks.Block>> available = new LinkedHashMap<>();
                for (NativeMarkdownBlocks.Block block : previous)
                    available.computeIfAbsent(block.key, ignored -> new ArrayDeque<>()).add(block);
                List<NativeMarkdownBlocks.Block> blocks = new ArrayList<>();
                long preparing = System.nanoTime(); int precomputed = 0, skipped = 0, failures = 0, reused = 0;
                for (Node node : nodes) {
                    if (Thread.currentThread().isInterrupted()) return;
                    String signature = NativeMarkdownBlocks.key(node, source);
                    ArrayDeque<NativeMarkdownBlocks.Block> matches = available.get(signature);
                    if (matches != null && !matches.isEmpty()) { blocks.add(matches.removeFirst()); reused++; continue; }
                    Spanned text = codeBlock(node) ? new android.text.SpannableString(codeLiteral(node)) : markwon.render(node);
                    if (codeBlock(node)) skipped++;
                    else if (text.getSpans(0, text.length(), android.text.style.ReplacementSpan.class).length == 0) {
                        try { text = PrecomputedTextCompat.create(text, textMetrics); precomputed++; }
                        catch (RuntimeException error) { failures++; }
                    } else skipped++;
                    blocks.add(new NativeMarkdownBlocks.Block(nextBlockId++, signature, node, text));
                }
                // A retained top-level node must not retain every old document via parent/sibling links.
                for (Node node : nodes) node.unlink();
                long prepareElapsed = System.nanoTime() - preparing;
                int measured = precomputed, replacements = skipped, failed = failures, retained = reused;
                DiffUtil.DiffResult diff = diff(previous, blocks);
                main.post(() -> {
                    if (!current(target, version)) return;
                    prepareNanos += prepareElapsed; preparedBlocks += blocks.size() - retained; reusedBlocks += retained;
                    precomputedBlocks += measured; precomputeSkips += replacements; precomputeFailures += failed;
                    parsed(target, version, blocks, diff, elapsed, false);
                });
            } catch (RuntimeException | LinkageError error) {
                org.commonmark.node.Paragraph paragraph = new org.commonmark.node.Paragraph();
                paragraph.appendChild(new org.commonmark.node.Text(source));
                List<NativeMarkdownBlocks.Block> blocks = Collections.singletonList(new NativeMarkdownBlocks.Block(
                        nextBlockId++, source, paragraph, new android.text.SpannableString(source)));
                DiffUtil.DiffResult diff = diff(previous, blocks);
                main.post(() -> parsed(target, version, blocks, diff, System.nanoTime() - started, true));
            }
        });
    }

    private static DiffUtil.DiffResult diff(List<NativeMarkdownBlocks.Block> previous, List<NativeMarkdownBlocks.Block> next) {
        return DiffUtil.calculateDiff(new DiffUtil.Callback() {
            @Override public int getOldListSize() { return previous.size(); }
            @Override public int getNewListSize() { return next.size(); }
            @Override public boolean areItemsTheSame(int old, int value) { return previous.get(old).id == next.get(value).id; }
            @Override public boolean areContentsTheSame(int old, int value) { return previous.get(old) == next.get(value); }
        });
    }

    private boolean current(Segment target, long version) { return !disposed && current.get(target.key) == target && version == target.version; }

    private void parsed(Segment target, long version, List<NativeMarkdownBlocks.Block> blocks,
            DiffUtil.DiffResult diff, long elapsed, boolean failed) {
        if (!current(target, version)) return;
        target.pending = false; parseNanos += elapsed; if (failed) parseErrors++;
        MarkwonAdapter adapter = (MarkwonAdapter) target.adapter;
        List<Node> nodes = new ArrayList<>();
        for (NativeMarkdownBlocks.Block block : blocks) nodes.add(block.node);
        adapter.setParsedMarkdown(markwon, nodes);
        for (int i = 0; i < blocks.size(); i++) {
            NativeMarkdownBlocks.Block block = blocks.get(i);
            target.textEntry.prepared.put(block.node, block.text); target.textEntry.ids.put(block.node, block.id);
            target.textEntry.positions.put(block.node, i);
        }
        target.blocks = blocks;
        diff.dispatchUpdatesTo(adapter); reindex(); settlePosition();
    }

    void end() {
        updating = true;
        try {
            Set<RecyclerView.Adapter<?>> wanted = Collections.newSetFromMap(new IdentityHashMap<>());
            for (Segment segment : next.values()) wanted.add(segment.adapter);
            Set<RecyclerView.Adapter<?>> present = Collections.newSetFromMap(new IdentityHashMap<>());
            present.addAll(adapterOrder);
            for (Segment segment : current.values()) if (!wanted.contains(segment.adapter)) segment.cancel();
            for (int i = adapterOrder.size() - 1; i >= 0; i--) if (!wanted.contains(adapterOrder.get(i))) {
                RecyclerView.Adapter<?> removed = adapterOrder.remove(i); present.remove(removed); adapters.removeAdapter(removed);
            }
            int position = 0;
            for (Segment segment : next.values()) {
                reconciledSegments++;
                if (position >= adapterOrder.size() || adapterOrder.get(position) != segment.adapter) {
                    int old = present.contains(segment.adapter) ? adapterOrder.indexOf(segment.adapter) : -1;
                    if (old >= 0) { adapterOrder.remove(old); adapters.removeAdapter(segment.adapter); }
                    adapterOrder.add(position, segment.adapter); adapters.addAdapter(position, segment.adapter); present.add(segment.adapter);
                }
                position++;
            }
            current = next; next = null; reindex();
        } finally { updating = false; }
        settlePosition();
    }

    private void reindex() {
        int position = 0; owners.clear();
        for (Segment segment : current.values()) {
            segment.position = position; position += segment.adapter.getItemCount(); owners.put(segment.adapter, segment);
        }
        rowCount = position;
    }

    // Coalesce updates into RecyclerView's next layout. Restore before that layout chooses its
    // reading anchor, including layouts caused only by a disclosure or delayed tool body.
    private void settlePosition() { requestLayout(); }

    private void restorePosition() {
        if (state.following) { if (!movingToLatest && adapters.getItemCount() > 0) layout.scrollToPosition(adapters.getItemCount() - 1); return; }
        Segment segment = current.get(state.recyclerKey);
        if (segment == null || segment.adapter.getItemCount() == 0) return;
        int item = Math.min(state.recyclerItem, segment.adapter.getItemCount() - 1);
        if (state.recyclerId >= 0) for (int i = 0; i < segment.adapter.getItemCount(); i++)
            if (segment.adapter.getItemId(i) == state.recyclerId) { item = i; break; }
        layout.scrollToPositionWithOffset(segment.position + item, state.recyclerOffset);
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        restoring = true;
        try {
            boolean requested = explicitScroll; explicitScroll = false;
            if (!requested) restorePosition();
            super.onLayout(changed, left, top, right, bottom);
            if (requested) { state.following = nearBottom(); following.accept(state.following); }
            View heading = disclosureHeading == null ? null : disclosureHeading.get();
            View item = heading == null ? null : findContainingItemView(heading);
            if (!state.following && item != null) {
                int delta = relativeTop(heading, this) - disclosureTop;
                if (delta != 0) scrollBy(0, delta);
            }
            if (!movingToLatest) remember();
        } finally { restoring = false; }
    }

    @Override public void scrollToPosition(int position) {
        disclosureHeading = null; movingToLatest = false; explicitScroll = true;
        super.scrollToPosition(position);
    }

    boolean probePosition(int position) {
        if (position < 0 || position >= blocks()) return false;
        stopScroll(); disclosureHeading = null; movingToLatest = false; explicitScroll = true;
        state.following = false; following.accept(false);
        layout.scrollToPositionWithOffset(position, 0); return true;
    }

    private boolean nearBottom() {
        if (rowCount == 0) return true;
        View tail = layout.getChildAt(layout.getChildCount() - 1);
        RowAnchor anchor = rowAnchors.get(tail);
        return anchor != null && anchor.current() && anchor.item() >= 0
                && anchor.segment.position + anchor.item() == rowCount - 1
                && layout.getDecoratedBottom(tail) - (getHeight() - getPaddingBottom()) < ui.dp(90);
    }

    private void remember() {
        for (int i = 0; i < layout.getChildCount(); i++) {
            View item = layout.getChildAt(i);
            if (layout.getDecoratedBottom(item) > getPaddingTop() && layout.getDecoratedTop(item) < getHeight() - getPaddingBottom()) {
                remember(item); return;
            }
        }
    }
    private void remember(View item) {
        RowAnchor anchor = rowAnchors.get(item);
        if (anchor == null || !anchor.current()) return;
        int position = anchor.item();
        if (position < 0) return;
        state.recyclerKey = anchor.segment.key; state.recyclerItem = position; state.recyclerId = anchor.id();
        // The recycler restores the logical row/id/offset, not the scrollbar's estimate.
        // Keep that estimate for renderer handoff at idle/disposal instead of every frame.
        state.recyclerOffset = layout.getDecoratedTop(item) - getPaddingTop();
    }
    private static int relativeTop(View child, View parent) {
        int top = 0;
        for (View view = child; view != parent && view != null; view = view.getParent() instanceof View ? (View) view.getParent() : null)
            top += view.getTop();
        return top;
    }

    void preservePosition(View heading) {
        state.following = false; movingToLatest = false;
        View item = findContainingItemView(heading);
        if (item != null) {
            remember(item); disclosureHeading = new java.lang.ref.WeakReference<>(heading); disclosureTop = relativeTop(heading, this);
        } else remember();
        following.accept(false); requestLayout();
    }
    @Override public boolean fling(int velocityX, int velocityY) {
        disclosureHeading = null; movingToLatest = false; return super.fling(velocityX, velocityY);
    }
    @Override public boolean onInterceptTouchEvent(android.view.MotionEvent event) {
        if (event.getActionMasked() == android.view.MotionEvent.ACTION_DOWN) { disclosureHeading = null; movingToLatest = false; }
        return super.onInterceptTouchEvent(event);
    }
    void latest() {
        if (disposed || adapters.getItemCount() == 0) return;
        disclosureHeading = null; state.following = true; following.accept(true); movingToLatest = true;
        smoothScrollToPosition(adapters.getItemCount() - 1);
    }
    int contentHeight() { return computeVerticalScrollRange(); }
    int contentOffset() { return computeVerticalScrollOffset(); }
    int blocks() { return rowCount; }
    int positionOf(String key) { Segment segment = current.get(key); return segment == null ? -1 : segment.position; }
    int pending() { int count = 0; for (Segment segment : current.values()) if (segment.pending) count++; return count; }

    /** Explicit idle-only verification; native content never leaves the process. */
    JSONObject probeVisibleContent(String salt) {
        try {
            java.security.MessageDigest digest = java.security.MessageDigest.getInstance("SHA-256");
            digestValue(digest, salt); int rows = 0;
            for (int i = 0; i < getChildCount(); i++) {
                View row = getChildAt(i);
                if (row.getBottom() <= getPaddingTop() || row.getTop() >= getHeight() - getPaddingBottom()) continue;
                digestValue(digest, Integer.toString(getChildAdapterPosition(row)));
                digestView(digest, row); rows++;
            }
            StringBuilder hex = new StringBuilder();
            for (byte value : digest.digest()) hex.append(Character.forDigit((value >>> 4) & 15, 16)).append(Character.forDigit(value & 15, 16));
            return new JSONObject().put("rows", rows).put("sha256", hex.toString());
        } catch (java.security.NoSuchAlgorithmException | org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
    }
    private static void digestValue(java.security.MessageDigest digest, CharSequence value) {
        byte[] bytes = (value == null ? "" : value.toString()).getBytes(java.nio.charset.StandardCharsets.UTF_8);
        digest.update(java.nio.ByteBuffer.allocate(4).putInt(bytes.length).array()); digest.update(bytes);
    }
    private static void digestView(java.security.MessageDigest digest, View view) {
        if (view.getVisibility() != VISIBLE) return;
        digestValue(digest, view.getClass().getName());
        digestValue(digest, view.getLeft() + "/" + view.getTop() + "/" + view.getRight() + "/" + view.getBottom());
        digestValue(digest, view.getContentDescription());
        if (view instanceof TextView) digestValue(digest, ((TextView) view).getText());
        if (view instanceof NativeImageView) {
            // NativeImages tags each request with its current source key.
            digestValue(digest, view.getTag() instanceof String ? (String) view.getTag() : "");
            digestValue(digest, ((NativeImageView) view).getDrawable() == null ? "empty" : "loaded");
        }
        if (view instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) view;
            for (int i = 0; i < group.getChildCount(); i++) digestView(digest, group.getChildAt(i));
        }
    }
    JSONObject metrics() {
        JSONObject result = new JSONObject();
        int first = layout.findFirstVisibleItemPosition();
        View firstView = layout.findViewByPosition(first);
        try { result.put("renderer", "markwon-recycler").put("blocks", blocks()).put("pendingParses", pending())
                .put("firstVisible", first).put("lastVisible", layout.findLastVisibleItemPosition())
                .put("canScrollUp", canScrollVertically(-1)).put("canScrollDown", canScrollVertically(1))
                .put("firstTop", firstView == null ? 0 : layout.getDecoratedTop(firstView) - getPaddingTop())
                .put("activityRowReuse", reuseActivityRows).put("activityInflations", activityInflations)
                .put("creates", creates).put("binds", binds).put("markdownBinds", markdownBinds).put("chromeBinds", chromeBinds)
                .put("recycles", recycles).put("bindMs", bindNanos / 1_000_000d).put("maxBindMs", maxBindNanos / 1_000_000d)
                .put("chromeBindMs", chromeNanos / 1_000_000d).put("markdownBindMs", markdownNanos / 1_000_000d)
                .put("chromeReuses", chromeReuses).put("chromeTransfers", chromeTransfers)
                .put("timeCacheHits", NativeMessageTime.cacheHits()).put("timeFormats", NativeMessageTime.formats())
                .put("timeFormatterCreates", NativeMessageTime.formatterCreates())
                .put("reusedBlocks", reusedBlocks).put("reconciledSegments", reconciledSegments)
                .put("prepareMs", prepareNanos / 1_000_000d).put("preparedBlocks", preparedBlocks).put("precomputedBlocks", precomputedBlocks)
                .put("precomputeSkips", precomputeSkips).put("precomputeFailures", precomputeFailures).put("precomputeFallbacks", precomputeFallbacks)
                .put("parseMs", parseNanos / 1_000_000d).put("parseErrors", parseErrors); }
        catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return result;
    }
    void dispose() {
        if (disposed) return;
        remember(); state.top = computeVerticalScrollOffset(); disposed = true; stopScroll(); parser.shutdownNow(); main.removeCallbacksAndMessages(null);
        for (Segment segment : current.values()) segment.cancel();
        setAdapter(null); getRecycledViewPool().clear(); current.clear(); adapterOrder.clear(); owners.clear(); rowAnchors.clear(); rowCount = 0; disclosureHeading = null; following = value -> {};
    }
}
