package com.cloudgeek.spool.android;

import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.view.ViewTreeObserver;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/** Long histories retain data/heights, not thousands of offscreen text/layout/syntax trees. */
final class NativeTranscriptViewport implements ViewTreeObserver.OnPreDrawListener {
    static final class Row extends LinearLayout {
        private NativeKeyedViews children, cached;
        private Consumer<NativeKeyedViews> render;
        private int retainedHeight, measuredWidth;
        int partCount;
        private final Map<Integer, Boolean> expanded = new java.util.HashMap<>();
        // A prepared row keeps a built, measured, detached document in its warm-cache slot.
        private boolean prepared, stale, unpreparable;
        private int preparedHeight;

        Row(NativeUi ui) { super(ui.context); setOrientation(VERTICAL); retainedHeight = ui.dp(220); }

        void bind(Consumer<NativeKeyedViews> render) {
            this.render = render;
            if (children != null) update();
            // Data changed while the document waited offscreen; refresh it through the keyed path at mount.
            else if (cached != null) stale = true;
        }

        private void update() { children.begin(); render.accept(children); children.end(); }

        boolean mounted() { return children != null; }
        boolean bound() { return render != null; }
        boolean prepared() { return prepared; }
        boolean stale() { return stale; }
        int preparedHeight() { return preparedHeight; }

        void mount() {
            if (mounted() || render == null) return;
            children = cached == null ? new NativeKeyedViews(this) : cached;
            cached = null;
            // An unchanged parked document only needs attaching, including previously visible rows.
            if (children.staged() && !stale) children.attach(); else update();
            prepared = false; stale = false;
            for (int i = 0; i < getChildCount(); i++) if (getChildAt(i) instanceof NativeUserRow) {
                NativeMessage message = ((NativeUserRow) getChildAt(i)).message;
                if (expanded.containsKey(i)) message.restoreExpanded(expanded.get(i));
            }
            requestLayout();
        }

        /** Build and measure the document in a detached stage, so attach-driven work cannot start offscreen. */
        int prepare(LinearLayout stage) {
            if (children != null || cached != null || render == null || unpreparable || getWidth() <= 0) return -1;
            NativeKeyedViews views = new NativeKeyedViews(this);
            views.begin(); render.accept(views); views.commit();
            stage.removeAllViews();
            for (View view : views.stagedViews()) stage.addView(view);
            stage.measure(MeasureSpec.makeMeasureSpec(getWidth(), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            int height = Math.max(1, stage.getMeasuredHeight());
            stage.removeAllViews();
            cached = views; prepared = true; stale = false; preparedHeight = height; retainedHeight = height;
            return height;
        }

        void release(boolean retain) {
            if (!mounted()) return;
            retainedHeight = Math.max(1, getMeasuredHeight());
            for (int i = 0; i < getChildCount(); i++) if (getChildAt(i) instanceof NativeUserRow)
                expanded.put(i, ((NativeUserRow) getChildAt(i)).message.content.expanded);
            if (retain) children.detach(); else removeAllViews();
            cached = retain ? children : null; children = null;
        }

        boolean pinned() { return mounted() && (hasFocus() || selected(this)); }

        private static boolean selected(View view) {
            if (view instanceof TextView) {
                TextView text = (TextView) view;
                if (text.getSelectionStart() >= 0 && text.getSelectionEnd() >= 0 && text.getSelectionEnd() != text.getSelectionStart()) return true;
            }
            if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
                if (selected(((ViewGroup) view).getChildAt(i))) return true;
            return false;
        }

        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            int width = MeasureSpec.getSize(widthSpec);
            if (mounted()) {
                super.onMeasure(widthSpec, heightSpec); retainedHeight = getMeasuredHeight(); measuredWidth = width;
            } else {
                // Keep the last height through resize until this row is actually laid out again.
                if (measuredWidth == 0) measuredWidth = width;
                setMeasuredDimension(resolveSize(width, widthSpec), resolveSize(retainedHeight, heightSpec));
            }
        }
    }

    private final View scroll;
    private final LinearLayout content;
    private final NativeHistoryScroll behavior;
    private final NativeHistoryScroll.State state;
    private final ViewTreeObserver observer;
    private boolean disposed;
    private long mounts, releases, drawDeferrals, mountNanos, maxMountNanos, maxBurstNanos;
    private long measureNanos, maxMeasureNanos, inPlaceLayouts, inPlaceNanos;
    private static final int CACHE_ROWS = 12, CACHE_VIEWS = 768, CACHE_CHARACTERS = 262144, CACHE_PARTS = 2048;
    // Offscreen preparation runs between frames, only once the scroller has been quiet for a few frames,
    // and only for the nearest rows beyond the overscan band in the direction of travel.
    static final long PREPARE_QUIET_MS = 48;
    static final int PREPARE_AHEAD = 3;
    /** Debug-probe switch for same-build A/B measurements; production never turns it off. */
    static volatile boolean prepareEnabled = true;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Runnable chain = this::prepareTurn;
    private LinearLayout stage;
    private int lastScrollTop, direction, lastRowCount;
    private long lastChangeUptime, lastRestores;
    private long prepares, prepareNanos, maxPrepareNanos, preparedMounts, stalePrepares, heightMisses;
    private long preparedEvictions, preparedSkips, preparedCorrections, prepareTurns, prepareIdle;
    private static final class Cost {
        int views, characters, parts;
        void count(View view) {
            views++;
            if (view instanceof TextView) characters += ((TextView) view).length();
            if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount() && retainable(); i++)
                count(((ViewGroup) view).getChildAt(i));
        }
        void count(List<View> detached) { views++; for (View view : detached) { if (!retainable()) break; count(view); } }
        boolean retainable() { return views <= 160 && characters <= 65536 && parts <= 512; }
    }
    private final Map<Row, Cost> cache = new LinkedHashMap<>();
    private int cachedViews, cachedCharacters, cachedParts;
    private long cacheHits;

    NativeTranscriptViewport(View scroll, LinearLayout content, NativeHistoryScroll behavior, NativeHistoryScroll.State state) {
        this.scroll = scroll; this.content = content; this.behavior = behavior; this.state = state;
        observer = scroll.getViewTreeObserver(); observer.addOnPreDrawListener(this);
    }

    @Override public boolean onPreDraw() {
        if (disposed || scroll.getHeight() <= 0) return true;
        int top = state.following ? Math.max(0, content.getHeight() - scroll.getHeight()) : scroll.getScrollY();
        int overscan = scroll.getHeight(), bottom = top + scroll.getHeight();
        boolean changed = false;
        long burstNanos = 0;
        NativeHistoryScroll.Anchor anchor = behavior.anchor();
        java.util.Iterator<Map.Entry<Row, Cost>> stale = cache.entrySet().iterator();
        while (stale.hasNext()) {
            Map.Entry<Row, Cost> entry = stale.next();
            if (entry.getKey().getParent() != content) { discard(entry); stale.remove(); }
        }
        for (int i = 0; i < content.getChildCount(); i++) {
            View view = content.getChildAt(i); if (!(view instanceof Row)) continue;
            Row row = (Row) view;
            if (row.getBottom() >= top - overscan && row.getTop() <= bottom + overscan) {
                if (!row.mounted()) {
                    boolean wasPrepared = row.prepared(), wasStale = row.stale();
                    Cost restored = cache.remove(row);
                    if (restored != null) {
                        cachedViews -= restored.views; cachedCharacters -= restored.characters; cachedParts -= restored.parts;
                        if (wasPrepared) { preparedMounts++; if (wasStale) stalePrepares++; } else cacheHits++;
                    }
                    long started = System.nanoTime(); row.mount(); long elapsed = System.nanoTime() - started;
                    mounts++; mountNanos += elapsed; burstNanos += elapsed; maxMountNanos = Math.max(maxMountNanos, elapsed);
                    long measureStarted = System.nanoTime();
                    row.measure(View.MeasureSpec.makeMeasureSpec(row.getWidth(), View.MeasureSpec.EXACTLY),
                            View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                    long measured = System.nanoTime() - measureStarted;
                    measureNanos += measured; maxMeasureNanos = Math.max(maxMeasureNanos, measured);
                    // The detached stage must measure like the attached row; count any disagreement.
                    if (wasPrepared && row.preparedHeight() > 0 && row.getMeasuredHeight() != row.preparedHeight()) heightMisses++;
                    // A known-height document can paint immediately, without cancelling the whole scroll frame.
                    if (row.getMeasuredHeight() == row.getHeight()) {
                        long layoutStarted = System.nanoTime();
                        row.layout(row.getLeft(), row.getTop(), row.getRight(), row.getBottom());
                        inPlaceLayouts++; inPlaceNanos += System.nanoTime() - layoutStarted;
                    } else changed = true;
                }
            } else if (row.mounted() && !row.pinned()) {
                Cost cost = new Cost(); cost.parts = row.partCount; cost.count(row);
                row.release(cost.retainable()); releases++;
                if (row.cached != null) {
                    cache.put(row, cost); cachedViews += cost.views; cachedCharacters += cost.characters; cachedParts += cost.parts;
                    trimCache();
                }
            }
        }
        maxBurstNanos = Math.max(maxBurstNanos, burstNanos);
        // Every scroll frame pushes the quiet window out; new or removed rows re-arm immediately.
        int sampled = scroll.getScrollY(); long restores = behavior.restores();
        if (sampled != lastScrollTop) {
            // A frame that also restored the reading anchor moved without a gesture; keep the gesture's direction.
            if (restores == lastRestores) direction = Integer.compare(sampled, lastScrollTop);
            lastScrollTop = sampled; lastChangeUptime = SystemClock.uptimeMillis();
            arm(PREPARE_QUIET_MS);
        }
        lastRestores = restores;
        if (content.getChildCount() != lastRowCount) { lastRowCount = content.getChildCount(); arm(0); }
        if (changed) {
            drawDeferrals++;
            behavior.changing(state.following ? null : anchor); behavior.changed(); content.requestLayout();
            return false;
        }
        return true;
    }

    /** Row data or membership changed; prepare the nearest unbuilt rows once the scroller is quiet. */
    void dataChanged() { if (!disposed) arm(0); }

    private void arm(long delay) {
        main.removeCallbacks(chain);
        if (!disposed) main.postDelayed(chain, delay);
    }

    private void prepareTurn() {
        if (disposed || scroll.getHeight() <= 0) return;
        long wait = lastChangeUptime + PREPARE_QUIET_MS - SystemClock.uptimeMillis();
        if (wait > 0) { arm(wait); return; }
        // Restoring the anchor would cancel a running "latest" animation.
        if (behavior.animating()) { arm(PREPARE_QUIET_MS); return; }
        if (!prepareEnabled) return;
        prepareTurns++;
        if (prepareOne()) arm(0); else prepareIdle++;
    }

    /** Prepare the nearest candidate; returns whether another turn may find more work. */
    private boolean prepareOne() {
        Row row = nextCandidate();
        if (row == null) return false;
        NativeHistoryScroll.Anchor anchor = state.following ? null : behavior.anchor();
        if (stage == null) { stage = new LinearLayout(scroll.getContext()); stage.setOrientation(LinearLayout.VERTICAL); }
        long started = System.nanoTime();
        int height = row.prepare(stage);
        long elapsed = System.nanoTime() - started;
        if (height < 0) { row.unpreparable = true; preparedSkips++; return true; }
        prepares++; prepareNanos += elapsed; maxPrepareNanos = Math.max(maxPrepareNanos, elapsed);
        Cost cost = new Cost(); cost.parts = row.partCount; cost.count(row.cached.stagedViews());
        if (!cost.retainable()) {
            // Giant documents are never retained offscreen; do not rebuild this one again either.
            row.cached = null; row.prepared = false; row.stale = false; row.unpreparable = true; preparedSkips++;
            return true;
        }
        cache.put(row, cost); cachedViews += cost.views; cachedCharacters += cost.characters; cachedParts += cost.parts;
        trimCache();
        // Apply the real height now, in a quiet window, so entering the viewport later cancels no frame.
        row.requestLayout();
        if (!state.following) { preparedCorrections++; behavior.changing(anchor); behavior.changed(); content.requestLayout(); }
        return true;
    }

    /** Prepare up to budget rows immediately, ignoring the quiet gate; for deterministic tests. */
    int prepareNow(int budget) {
        long before = prepares;
        while (prepares - before < budget && prepareOne()) { }
        return (int) (prepares - before);
    }

    private Row nextCandidate() {
        int top = state.following ? Math.max(0, content.getHeight() - scroll.getHeight()) : scroll.getScrollY();
        int overscan = scroll.getHeight(), bottom = top + scroll.getHeight();
        int first = -1, last = -1;
        for (int i = 0; i < content.getChildCount(); i++) {
            View view = content.getChildAt(i); if (!(view instanceof Row)) continue;
            if (view.getBottom() >= top - overscan && view.getTop() <= bottom + overscan) { if (first < 0) first = i; last = i; }
        }
        if (first < 0) return null;
        // Prefer the direction of travel; at an edge, or when that side is already warm, look the other way.
        boolean downward = direction > 0;
        Row preferred = downward ? nearest(last + 1, 1, PREPARE_AHEAD) : nearest(first - 1, -1, PREPARE_AHEAD);
        if (preferred != null) return preferred;
        return downward ? nearest(first - 1, -1, PREPARE_AHEAD) : nearest(last + 1, 1, PREPARE_AHEAD);
    }

    private Row nearest(int from, int step, int limit) {
        for (int i = from, seen = 0; i >= 0 && i < content.getChildCount() && seen < limit; i += step) {
            View view = content.getChildAt(i); if (!(view instanceof Row)) continue;
            seen++;
            Row row = (Row) view;
            if (!row.mounted() && row.cached == null && !row.unpreparable && row.render != null && row.getWidth() > 0 && row.partCount <= 512) return row;
        }
        return null;
    }

    private void discard(Map.Entry<Row, Cost> entry) {
        Row row = entry.getKey();
        row.cached = null;
        if (row.prepared) { row.prepared = false; row.stale = false; preparedEvictions++; }
        cachedViews -= entry.getValue().views; cachedCharacters -= entry.getValue().characters; cachedParts -= entry.getValue().parts;
    }

    private void trimCache() {
        java.util.Iterator<Map.Entry<Row, Cost>> oldest = cache.entrySet().iterator();
        while ((cache.size() > CACHE_ROWS || cachedViews > CACHE_VIEWS || cachedCharacters > CACHE_CHARACTERS || cachedParts > CACHE_PARTS) && oldest.hasNext()) {
            discard(oldest.next()); oldest.remove();
        }
    }

    org.json.JSONObject metrics() {
        int preparedRows = 0;
        for (Row row : cache.keySet()) if (row.prepared) preparedRows++;
        org.json.JSONObject result = new org.json.JSONObject();
        try { result.put("mounts", mounts).put("releases", releases).put("drawDeferrals", drawDeferrals)
                .put("mountMs", mountNanos / 1_000_000d).put("maxMountMs", maxMountNanos / 1_000_000d)
                .put("maxBurstMs", maxBurstNanos / 1_000_000d).put("cacheHits", cacheHits).put("cachedRows", cache.size())
                .put("measureMs", measureNanos / 1_000_000d).put("maxMeasureMs", maxMeasureNanos / 1_000_000d)
                .put("inPlaceLayouts", inPlaceLayouts).put("inPlaceMs", inPlaceNanos / 1_000_000d)
                .put("cachedViews", cachedViews).put("cachedCharacters", cachedCharacters).put("cachedParts", cachedParts)
                .put("prepares", prepares).put("prepareMs", prepareNanos / 1_000_000d).put("maxPrepareMs", maxPrepareNanos / 1_000_000d)
                .put("preparedRows", preparedRows).put("preparedMounts", preparedMounts).put("stalePrepares", stalePrepares)
                .put("heightMisses", heightMisses).put("preparedEvictions", preparedEvictions).put("preparedSkips", preparedSkips)
                .put("preparedCorrections", preparedCorrections).put("prepareTurns", prepareTurns).put("prepareIdle", prepareIdle)
                .put("prepareEnabled", prepareEnabled); }
        catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return result;
    }

    void dispose() {
        disposed = true;
        main.removeCallbacks(chain);
        if (stage != null) stage.removeAllViews();
        for (Map.Entry<Row, Cost> entry : cache.entrySet()) discard(entry);
        cache.clear();
        if (observer.isAlive()) observer.removeOnPreDrawListener(this);
        ViewTreeObserver current = scroll.getViewTreeObserver();
        if (current != observer && current.isAlive()) current.removeOnPreDrawListener(this);
    }
}
