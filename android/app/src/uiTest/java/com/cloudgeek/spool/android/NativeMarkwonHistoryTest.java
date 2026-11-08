package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import androidx.recyclerview.widget.ConcatAdapter;
import androidx.recyclerview.widget.RecyclerView;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMarkwonHistoryTest {
    private static final class Fixture implements AutoCloseable {
        final ActivityController<Activity> controller;
        final NativeMarkwonHistory history;
        final NativeHistoryScroll.State state = new NativeHistoryScroll.State();
        final List<String> opened = new ArrayList<>();
        Fixture() { this("mdpi"); }
        Fixture(String density) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            controller = Robolectric.buildActivity(Activity.class).setup();
            history = new NativeMarkwonHistory(new NativeUi(controller.get(), false, "zh-CN"), state, opened::add);
            controller.get().setContentView(history);
        }
        void source(String source) { history.begin(); history.markdown("answer", source); history.end(); }
        void frame() { layout(history); }
        void ready() throws Exception { await(history); }
        @Override public void close() { history.dispose(); controller.close(); }
    }

    static void layout(View root) {
        root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
    }
    static void await(NativeMarkwonHistory history) throws Exception {
        for (int i = 0; i < 200; i++) {
            Shadows.shadowOf(Looper.getMainLooper()).idle(); layout(history);
            if (history.pending() == 0) break;
            Thread.sleep(10);
        }
        assertEquals("The owned background parser must finish", 0, history.pending());
        assertEquals("CommonMark/Markwon binary compatibility must hold", 0, history.metrics().getLong("parseErrors"));
        for (int i = 0; i < 4; i++) { Shadows.shadowOf(Looper.getMainLooper()).idle(); layout(history); }
    }
    static List<TextView> texts(View root) {
        List<TextView> result = new ArrayList<>();
        if (root instanceof TextView) result.add((TextView) root);
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) result.addAll(texts(((ViewGroup) root).getChildAt(i)));
        return result;
    }

    @Test public void officialBlockAdapterReusesNativeHoldersAcrossHundredsOfParagraphs() throws Exception {
        try (Fixture f = new Fixture()) {
            StringBuilder source = new StringBuilder();
            for (int i = 0; i < 400; i++) source.append("段落 ").append(i).append(" **加粗** and English text.\n\n");
            f.source(source.toString()); f.ready();
            assertTrue(f.history.getAdapter() instanceof ConcatAdapter);
            assertEquals(400, f.history.blocks());
            assertEquals(400, f.history.metrics().getLong("preparedBlocks"));
            assertEquals(400, f.history.metrics().getLong("precomputedBlocks"));
            assertEquals(0, f.history.metrics().getLong("precomputeFailures"));
            assertTrue("Only the native viewport is attached: " + f.history.metrics(), f.history.getChildCount() < 40);
            for (int i : new int[] {0, 80, 160, 240, 320, 0, 399}) { f.history.scrollToPosition(i); f.frame(); }
            assertTrue("Distant jumps must not construct the whole document: " + f.history.metrics(), f.history.metrics().getLong("creates") < 100);
            assertTrue("Holders must be reused repeatedly", f.history.metrics().getLong("binds") > 2 * f.history.metrics().getLong("creates"));
            assertTrue(f.history.metrics().getLong("recycles") > 0);
            assertTrue("No browser rendering surface", texts(f.history).size() < 40);
        }
    }

    @Test public void nativeSelectionLinksAndGfmSurviveRecyclingAndContentUpdates() throws Exception {
        try (Fixture f = new Fixture()) {
            String source = "[原生链接](https://example.com/old) **粗体** ~~删除~~\n\n"
                    + "- [x] 完成任务\n- [ ] 待办任务\n\n1. First\n2. Second\n\n| 列一 | 列二 |\n| --- | --- |\n| 中文 | English |\n\n"
                    + "```java\nint value = 1;\n```\n\n" + "更多段落。\n\n".repeat(80);
            f.source(source); f.ready(); f.history.scrollToPosition(0); f.frame();
            TextView text = texts(f.history).stream().filter(t -> t.getText().toString().contains("原生链接")).findFirst().orElseThrow();
            assertTrue(text.isTextSelectable()); assertTrue(text.getText() instanceof Spannable);
            Selection.setSelection((Spannable) text.getText(), 0, 4);
            assertEquals(4, text.getSelectionEnd());
            for (ClickableSpan link : ((Spanned) text.getText()).getSpans(0, 4, ClickableSpan.class)) link.onClick(text);
            assertEquals(List.of("https://example.com/old"), f.opened);
            f.history.scrollToPosition(60); f.frame(); f.history.scrollToPosition(0); f.frame();
            f.source(source.replace("example.com/old", "example.com/new")); f.ready();
            f.history.scrollToPosition(0); f.frame();
            text = texts(f.history).stream().filter(t -> t.getText().toString().contains("原生链接")).findFirst().orElseThrow();
            for (ClickableSpan link : ((Spanned) text.getText()).getSpans(0, 4, ClickableSpan.class)) link.onClick(text);
            assertEquals(List.of("https://example.com/old", "https://example.com/new"), f.opened);
        }
    }

    @Test public void unchangedAnswersRetainTheirOfficialAdapterCacheAndDoNotRebind() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("First **answer**.\n\nSecond paragraph."); f.ready();
            long before = f.history.metrics().getLong("binds");
            RecyclerView.Adapter<?> adapter = ((ConcatAdapter) f.history.getAdapter()).getAdapters().get(0);
            f.source("First **answer**.\n\nSecond paragraph."); f.ready();
            assertSame(adapter, ((ConcatAdapter) f.history.getAdapter()).getAdapters().get(0));
            assertEquals(before, f.history.metrics().getLong("binds"));
            assertEquals(2, f.history.metrics().getLong("preparedBlocks"));
        }
    }

    @Test public void backgroundPrecomputedTextRetainsNativeLineBreaksBaselinesAndHeightAtEveryDensity() throws Exception {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) try (Fixture f = new Fixture(density)) {
            f.source("中文 **加粗** English words with mixed punctuation，测试原生换行。 More words to wrap over several native lines. ".repeat(3));
            f.ready(); TextView actual = texts(f.history).get(0);
            TextView reference = (TextView) android.view.LayoutInflater.from(f.controller.get()).inflate(R.layout.markwon_block, f.history, false);
            androidx.core.widget.TextViewCompat.setTextMetricsParams(reference, androidx.core.widget.TextViewCompat.getTextMetricsParams(actual));
            reference.setLineSpacing(actual.getLineSpacingExtra(), actual.getLineSpacingMultiplier());
            reference.setText(new android.text.SpannableStringBuilder(actual.getText()));
            int width = actual.getWidth(); assertTrue(width > 0);
            int widthSpec = View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), heightSpec = View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED);
            actual.measure(widthSpec, heightSpec); reference.measure(widthSpec, heightSpec);
            assertEquals(0, f.history.metrics().getLong("precomputeFailures"));
            assertEquals(actual.getLineCount(), reference.getLineCount());
            assertEquals(actual.getMeasuredHeight(), reference.getMeasuredHeight());
            for (int line = 0; line < actual.getLineCount(); line++) {
                assertEquals(actual.getLayout().getLineStart(line), reference.getLayout().getLineStart(line));
                assertEquals(actual.getLayout().getLineEnd(line), reference.getLayout().getLineEnd(line));
                assertEquals(actual.getLayout().getLineBaseline(line), reference.getLayout().getLineBaseline(line));
            }
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void fontMetricChangesFallBackToTheCurrentNativeLayoutWithoutApplyingOldMetrics() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("Text with **style** and 中文."); f.ready();
            RecyclerView.Adapter adapter = ((ConcatAdapter) f.history.getAdapter()).getAdapters().get(0);
            RecyclerView.ViewHolder holder = adapter.createViewHolder(f.history, adapter.getItemViewType(0));
            TextView text = (TextView) holder.itemView; text.setTextSize(28);
            adapter.bindViewHolder(holder, 0); layout(text);
            assertTrue(text.getText().toString().contains("Text with style and 中文"));
            assertEquals(28, text.getTextSize(), .001); assertTrue(text.isTextSelectable());
            assertEquals(1, f.history.metrics().getLong("precomputeFallbacks"));
        }
    }

    @Test public void fileCitationsKeepTheirNativeActionWhileCodeStaysLiteral() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("See :codex-file-citation{path=\"src/Main.java\"} and `:codex-file-citation{path=\"literal.txt\"}`.");
            f.ready(); TextView text = texts(f.history).get(0);
            assertTrue(text.getText().toString().contains("Main.java"));
            assertTrue(text.getText().toString().contains(":codex-file-citation{path=\"literal.txt\"}"));
            ClickableSpan[] links = ((Spanned) text.getText()).getSpans(0, text.length(), ClickableSpan.class);
            assertEquals(1, links.length); links[0].onClick(text);
            assertEquals(List.of("src/Main.java"), f.opened);
        }
    }

    @Test public void newerSourcesWinAndDisposedOrRemovedParsesCannotAttach() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("Outdated paragraph.\n\n".repeat(1000));
            f.source("Latest **answer**."); f.ready();
            assertEquals(1, f.history.blocks());
            assertTrue(texts(f.history).stream().anyMatch(t -> t.getText().toString().contains("Latest answer")));
            f.source("Removed paragraph.\n\n".repeat(1000));
            f.history.begin(); f.history.end(); f.ready(); assertEquals(0, f.history.blocks());
            f.source("Disposed paragraph.\n\n".repeat(1000)); f.history.dispose();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNull(f.history.getAdapter()); assertEquals(0, f.history.pending());
        }
    }

    @Test public void prependedBlocksKeepTheSameReadingAnchorUntilLatestIsRequested() throws Exception {
        try (Fixture f = new Fixture()) {
            StringBuilder source = new StringBuilder();
            for (int i = 0; i < 80; i++) source.append("Paragraph ").append(i).append(".\n\n");
            f.source(source.toString()); f.ready(); f.history.scrollToPosition(30); f.frame();
            View first = f.history.getChildAt(0);
            f.history.preservePosition(first);
            String text = ((TextView) first).getText().toString(); int top = first.getTop();
            f.history.begin(); f.history.markdown("prefix", "An earlier page.\n\nMore earlier content.");
            f.history.markdown("answer", source.toString()); f.history.end(); f.ready();
            assertFalse(f.state.following);
            assertEquals(text, ((TextView) f.history.getChildAt(0)).getText().toString());
            assertEquals(top, f.history.getChildAt(0).getTop());
            f.history.latest(); assertTrue(f.state.following);
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void nativeHoldersReuseUserAndFooterWidgetsButKeepExpansionSelectionAndCopyScopedToTheirOwner() throws Exception {
        try (Fixture f = new Fixture()) {
            NativeUi ui = new NativeUi(f.controller.get(), false);
            java.util.concurrent.atomic.AtomicInteger userCreates = new java.util.concurrent.atomic.AtomicInteger(), footerCreates = new java.util.concurrent.atomic.AtomicInteger();
            f.history.begin();
            for (String owner : List.of("first", "second")) {
                String source = (owner + " prompt\n").repeat(18);
                f.history.chrome(owner + "/heading", true, children -> {
                    NativeUserRow user = (NativeUserRow) children.use("user", "user", () -> {
                        userCreates.incrementAndGet(); return new NativeUserRow(ui, null, source, 1000, null, s -> {});
                    });
                    user.bind(null, source, 1000, java.util.Collections.emptyList());
                });
                f.history.chrome(owner + "/footer", true, children -> {
                    NativeMessageFooter footer = (NativeMessageFooter) children.use("footer", "footer", () -> {
                        footerCreates.incrementAndGet(); return new NativeMessageFooter(ui, false, null, owner, 1000);
                    });
                    footer.bindText(owner); footer.bindTimestamp(owner.equals("first") ? 1000 : 2000);
                });
            }
            f.history.end(); f.ready();
            List<? extends RecyclerView.Adapter<?>> adapters = ((ConcatAdapter) f.history.getAdapter()).getAdapters();
            RecyclerView.Adapter first = adapters.get(0), firstFooter = adapters.get(1), second = adapters.get(2), secondFooter = adapters.get(3);
            assertEquals(first.getItemViewType(0), second.getItemViewType(0));
            assertNotEquals(first.getItemViewType(0), firstFooter.getItemViewType(0));
            int usersBefore = userCreates.get();
            RecyclerView.ViewHolder holder = first.createViewHolder(f.history, first.getItemViewType(0)); first.bindViewHolder(holder, 0); layout(holder.itemView);
            NativeUserRow original = (NativeUserRow) ((ViewGroup) holder.itemView).getChildAt(0);
            original.message.restoreExpanded(true);
            TextView selected = texts(original.message.content).get(0); Selection.setSelection((Spannable) selected.getText(), 1, 5);
            first.onViewRecycled(holder); second.bindViewHolder(holder, 0); layout(holder.itemView);
            assertSame(original, ((ViewGroup) holder.itemView).getChildAt(0)); assertEquals(usersBefore + 1, userCreates.get());
            assertFalse("A different message must not inherit expansion", original.message.content.expanded);
            assertEquals("second prompt\n".repeat(18).stripTrailing(), texts(original.message.content).get(0).getText().toString());
            assertEquals(-1, texts(original.message.content).get(0).getSelectionStart());
            second.onViewRecycled(holder); first.bindViewHolder(holder, 0); layout(holder.itemView);
            assertTrue("Returning restores this message's own expansion", original.message.content.expanded);
            assertEquals(usersBefore + 1, userCreates.get());

            int footersBefore = footerCreates.get();
            RecyclerView.ViewHolder footerHolder = firstFooter.createViewHolder(f.history, firstFooter.getItemViewType(0)); firstFooter.bindViewHolder(footerHolder, 0);
            NativeMessageFooter footer = (NativeMessageFooter) ((ViewGroup) footerHolder.itemView).getChildAt(0);
            footer.copy.performClick(); firstFooter.onViewRecycled(footerHolder); secondFooter.bindViewHolder(footerHolder, 0);
            assertSame(footer, ((ViewGroup) footerHolder.itemView).getChildAt(0)); assertEquals(footersBefore + 1, footerCreates.get());
            assertEquals("Copy message", footer.copy.getContentDescription().toString());
            footer.copy.performClick();
            android.content.ClipboardManager clipboard = (android.content.ClipboardManager) f.controller.get().getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            assertEquals("second", clipboard.getPrimaryClip().getItemAt(0).getText().toString());
        }
    }

    @Test public void foldedTurnSummaryDoesNotDeepCopyToolPartsButOpeningAlwaysUsesTheLatestParts() throws Exception {
        try (Fixture f = new Fixture()) {
            class CountedPart extends org.json.JSONObject {
                int keyWalks;
                @Override public java.util.Iterator<String> keys() { keyWalks++; return super.keys(); }
            }
            CountedPart tool = new CountedPart();
            tool.put("id", "tool").put("role", "assistant").put("kind", "tool_use").put("name", "read_file")
                    .put("tool", new org.json.JSONObject("{kind:'read',path:'first.md'}"));
            org.json.JSONArray parts = new org.json.JSONArray().put(tool)
                    .put(new org.json.JSONObject("{role:'assistant',kind:'text',phase:'final_answer',text:'Answer'}"));
            org.json.JSONObject turn = new org.json.JSONObject("{status:'completed',backend:'codex',started_at:1000,finished_at:2000}");
            NativeActivityList.State state = new NativeActivityList.State();
            NativeTurn response = new NativeTurn(new NativeUi(f.controller.get(), false), parts, 0, 2, turn, false, state, s -> {}, null, false);
            assertEquals("A folded summary must not snapshot any tool JSON", 0, tool.keyWalks);
            tool.getJSONObject("tool").put("path", "updated.md"); response.bind(parts, 0, 2, turn);
            assertEquals(0, tool.keyWalks);
            ((View) response.activity.summaryLabel.getParent()).performClick();
            assertTrue(state.open);
            assertTrue(texts(response.activity).stream().anyMatch(t -> t.getText().toString().contains("updated.md")));
            tool.getJSONObject("tool").put("path", "newest.md"); response.bind(parts, 0, 2, turn);
            assertTrue("Expanded documents retain mutation detection", tool.keyWalks > 0);
            assertTrue(texts(response.activity).stream().anyMatch(t -> t.getText().toString().contains("newest.md")));
        }
    }

    @Test public void toolSummaryWidgetsAreReusedAcrossTurnsWithoutSharingDisclosureStateOrOldParts() throws Exception {
        try (Fixture f = new Fixture()) {
            org.json.JSONArray first = new org.json.JSONArray("[{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'first.md'}}]");
            org.json.JSONArray second = new org.json.JSONArray("[{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'second.md'}}]");
            org.json.JSONObject turn = new org.json.JSONObject("{status:'completed',backend:'codex',started_at:1000,finished_at:2000}");
            NativeActivityList.State a = new NativeActivityList.State(), b = new NativeActivityList.State();
            NativeTurn response = new NativeTurn(new NativeUi(f.controller.get(), false), first, 0, 1, turn, false, a, s -> {}, null, false);
            NativeActivityList original = response.activity;
            View heading = (View) original.summaryLabel.getParent(); heading.performClick(); assertTrue(a.open);
            assertTrue(texts(original).stream().anyMatch(t -> t.getText().toString().contains("first.md")));
            response.rebindForTurn(b, null); response.bind(second, 0, 1, turn);
            assertSame(original, response.activity); assertSame(heading, original.summaryLabel.getParent());
            assertFalse("The next turn starts with its own folded state", b.open);
            assertFalse(texts(original).stream().anyMatch(t -> t.getText().toString().contains("first.md")));
            heading.performClick(); assertTrue(b.open);
            assertTrue(texts(original).stream().anyMatch(t -> t.getText().toString().contains("second.md")));
            heading.performClick(); assertFalse(b.open);
            response.rebindForTurn(a, null); response.bind(first, 0, 1, turn);
            assertTrue("The first turn keeps its previously opened state", a.open);
            assertSame(original, response.activity); assertSame(heading, original.summaryLabel.getParent());
            assertTrue(texts(original).stream().anyMatch(t -> t.getText().toString().contains("first.md")));
            assertFalse(texts(original).stream().anyMatch(t -> t.getText().toString().contains("second.md")));
        }
    }
}
