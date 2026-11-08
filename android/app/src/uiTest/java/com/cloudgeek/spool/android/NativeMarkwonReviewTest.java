package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.Selection;
import android.text.Spannable;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.HorizontalScrollView;
import android.widget.TextView;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 34, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMarkwonReviewTest {
    private static TextView text(View root, String value) {
        return NativeMarkwonHistoryTest.texts(root).stream()
                .filter(text -> text.getText().toString().equals(value)).findFirst().orElseThrow();
    }

    private static void tapText(View root, TextView text) {
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(400));
        int[] origin = new int[2], child = new int[2];
        root.getLocationOnScreen(origin); text.getLocationOnScreen(child);
        float x = child[0] - origin[0] + text.getTotalPaddingLeft() - text.getScrollX()
                + (text.getLayout().getPrimaryHorizontal(0) + text.getLayout().getPrimaryHorizontal(1)) / 2f;
        float y = child[1] - origin[1] + text.getTotalPaddingTop() - text.getScrollY()
                + (text.getLayout().getLineTop(0) + text.getLayout().getLineBottom(0)) / 2f;
        long now = android.os.SystemClock.uptimeMillis();
        NativeMarkwonInteractionTest.touch(root, now, now, MotionEvent.ACTION_DOWN, x, y);
        NativeMarkwonInteractionTest.touch(root, now, now + 40, MotionEvent.ACTION_UP, x, y);
    }

    private static HorizontalScrollView horizontal(View root) {
        if (root instanceof HorizontalScrollView) return (HorizontalScrollView) root;
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) {
            HorizontalScrollView found = horizontal(((ViewGroup) root).getChildAt(i));
            if (found != null) return found;
        }
        return null;
    }

    private static void draw(NativeMarkwonInteractionTest.Fixture f) throws Exception {
        Bitmap bitmap = Bitmap.createBitmap(360, 800, Bitmap.Config.ARGB_8888);
        try {
            for (int i = 0; i < 3; i++) {
                f.history.draw(new Canvas(bitmap)); f.ready();
            }
        } finally { bitmap.recycle(); }
    }

    @Test public void tableCellLinksRespondToActualTouches() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.source("| File | Website |\n| --- | --- |\n| [Guide](docs/guide.md) | [Visit](https://example.com) |");
            f.ready(); draw(f);
            tapText(f.history, text(f.history, "Guide"));
            tapText(f.history, text(f.history, "Visit"));
            assertEquals(List.of("docs/guide.md", "https://example.com"), f.opened);
        }
    }

    @Test public void selectingRenderedTableCopiesCellContents() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.source("| Name | Value |\n| --- | --- |\n| Alpha | Beta |"); f.ready(); draw(f);
            android.content.ClipboardManager clipboard = (android.content.ClipboardManager) f.controller.get()
                    .getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            for (String value : List.of("Name", "Value", "Alpha", "Beta")) {
                TextView cell = text(f.history, value);
                assertTrue(cell.isTextSelectable());
                Selection.setSelection((Spannable) cell.getText(), 0, cell.length());
                assertTrue(cell.onTextContextMenuItem(android.R.id.copy));
                assertEquals(value, clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            }
        }
    }

    @Test public void linkedImageOpensTheEnclosingLinkOnce() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.source("[![Badge](docs/badge.png)](https://example.com/project)"); f.ready();
            TextView text = NativeMarkwonHistoryTest.texts(f.history).get(0);
            NativeMarkwonInteractionTest.tapLink(f.history, text, "https://example.com/project");
            assertEquals(List.of("https://example.com/project"), f.opened);
        }
    }

    @Test public void wideTablesRemainDrawableAtPhoneWidths() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.source("|" + " Heading |".repeat(48) + "\n|" + " --- |".repeat(48)
                    + "\n|" + " Value |".repeat(48)); f.ready(); draw(f);
            assertTrue(f.history.getChildCount() > 0);
            HorizontalScrollView scroll = horizontal(f.history);
            assertNotNull(scroll);
            assertTrue(scroll.getChildAt(0).getWidth() > scroll.getWidth());
            scroll.fullScroll(View.FOCUS_RIGHT); draw(f);
            assertTrue("Wide tables must expose the last columns through horizontal scrolling", scroll.getScrollX() > 0);
            assertEquals(48, NativeMarkwonHistoryTest.texts(f.history).stream().filter(t -> t.getText().toString().equals("Value")).count());
        }
    }

    @Test public void tableReferencesUpdateAndStreamingPreservesAnUnchangedTable() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            String prefix = "An introduction before the table, with different source offsets.\n\n";
            String table = "| File | Literal URL |\n| --- | --- |\n| [Guide][ref] | https://example.com/a |\n| Stable | &amp; `code` |";
            f.source(prefix + table + "\n\nStreaming tail.\n\n[ref]: docs/old.md#L2"); f.ready(); draw(f);
            tapText(f.history, text(f.history, "Guide"));
            tapText(f.history, text(f.history, "https://example.com/a"));
            TextView stable = text(f.history, "Stable");
            Selection.setSelection((Spannable) stable.getText(), 1, 4); Object document = stable.getText();
            f.source(prefix + table + "\n\nStreaming tail with more tokens.\n\n[ref]: docs/old.md#L2"); f.ready(); draw(f);
            assertSame(document, text(f.history, "Stable").getText());
            assertEquals(4, text(f.history, "Stable").getSelectionEnd());
            f.source(prefix + table + "\n\nStreaming tail.\n\n[ref]: docs/new.md#L5"); f.ready(); draw(f);
            tapText(f.history, text(f.history, "Guide"));
            assertEquals(List.of("docs/old.md#L2", "https://example.com/a", "docs/new.md#L5"), f.opened);
            assertNotNull(text(f.history, "& code"));
        }
    }

    @Test public void tablesInQuotesAndListsRetainTheirContentLinksAndReadingAnchor() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            String quoted = "> | Name | Target |\n> | --- | --- |\n> | Quoted | [Open][ref] |";
            String listed = "- List container\n\n  | Name | Target |\n  | --- | --- |\n  | Listed | [Open][ref] |";
            for (String table : List.of(quoted, listed)) {
                f.opened.clear();
                String source = "Before table.\n\n" + table + "\n\nAfter table.\n\n[ref]: docs/nested.md";
                f.source(source); f.ready(); draw(f);
                assertNotNull(text(f.history, table == quoted ? "Quoted" : "Listed"));
                TextView link = text(f.history, "Open"); f.history.preservePosition(link);
                int[] before = new int[2]; link.getLocationOnScreen(before);
                tapText(f.history, link);
                assertEquals("Initial touch in " + table, List.of("docs/nested.md"), f.opened);
                f.source("A different length introduction.\n\n" + source.substring("Before table.\n\n".length()));
                f.ready(); draw(f);
                int[] after = new int[2]; text(f.history, "Open").getLocationOnScreen(after);
                assertEquals(before[1], after[1]);
                tapText(f.history, text(f.history, "Open"));
                assertEquals(List.of("docs/nested.md", "docs/nested.md"), f.opened);
            }
        }
    }

    @Test public void tableHoldersRecycleWithoutOldLinksSelectionOrHorizontalOffsets() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            StringBuilder source = new StringBuilder();
            for (int i = 0; i < 60; i++) source.append("| File | A | B | C | D |\n| --- | --- | --- | --- | --- |\n| [Guide")
                    .append(i).append("](docs/").append(i).append(".md) | Alpha | Beta | Gamma | Delta |\n\n");
            f.source(source.toString()); f.ready();
            f.history.scrollToPosition(0); f.ready(); draw(f);
            TextView first = text(f.history, "Guide0");
            Selection.setSelection((Spannable) first.getText(), 1, 4);
            HorizontalScrollView scroll = horizontal(f.history);
            scroll.fullScroll(View.FOCUS_RIGHT); draw(f); assertTrue(scroll.getScrollX() > 0);
            for (int position : new int[] {20, 40, 0}) {
                f.history.scrollToPosition(position); f.ready(); draw(f);
            }
            assertTrue(f.history.metrics().getLong("recycles") > 0);
            first = text(f.history, "Guide0");
            assertEquals(first.getSelectionStart(), first.getSelectionEnd());
            assertEquals(0, horizontal(f.history).getScrollX());
            tapText(f.history, first);
            assertEquals(List.of("docs/0.md"), f.opened);
            assertTrue(f.history.metrics().getInt("nativeCachedBlocks") <= 16);
            assertTrue(f.history.metrics().getInt("nativeCachedCells") <= 384);
            assertTrue(f.history.metrics().getInt("nativeCachedChars") <= 65536);
        }
    }

    @Test public void returningToATableReusesItsDocumentWithoutRebindingCells() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.history.setItemViewCacheSize(0);
            String table = "| File | A | B | C | D |\n| --- | --- | --- | --- | --- |\n| [Guide](docs/retained.md) | Alpha | Beta | Gamma | Delta |\n\n";
            f.source(table + "An intervening paragraph.\n\n".repeat(40)); f.ready(); draw(f);
            f.history.scrollToPosition(0); f.ready(); draw(f);
            TextView first = text(f.history, "Guide");
            Object document = first.getText();
            Selection.setSelection((Spannable) document, 0, first.length());
            horizontal(f.history).fullScroll(View.FOCUS_RIGHT); draw(f);
            f.history.scrollToPosition(30); f.ready(); draw(f);
            f.history.scrollToPosition(0); f.ready(); draw(f);
            assertSame(first, text(f.history, "Guide"));
            assertSame(document, text(f.history, "Guide").getText());
            assertEquals(first.getSelectionStart(), first.getSelectionEnd());
            assertEquals(0, horizontal(f.history).getScrollX());
            assertTrue(f.history.metrics().getLong("nativeBlockCacheHits") > 0);
            long contentSets = NativeTextView.contentSets;
            f.history.begin(); f.history.markdown("answer", table + "An intervening paragraph.\n\n".repeat(40)); f.history.end();
            f.ready(); draw(f);
            assertEquals("An unchanged table must not reset its cell documents", contentSets, NativeTextView.contentSets);
            tapText(f.history, first); assertEquals(List.of("docs/retained.md"), f.opened);
            f.history.dispose();
            assertEquals(0, f.history.metrics().getInt("nativeCachedBlocks"));
        }
    }

    @Test public void idlePreparationBuildsOffscreenCellsBeforeTheirFirstVisibleBind() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            f.source("| File | Value |\n| --- | --- |\n| [Prepared](docs/prepared.md) | Original text |\n\n"
                    + "An intervening paragraph.\n\n".repeat(40));
            f.ready();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(3));
            assertEquals(1, f.history.metrics().getLong("nativePreparedBlocks"));
            long sets = NativeTextView.contentSets;
            f.history.scrollToPosition(0); f.ready(); draw(f);
            assertEquals("Prepared cell documents must enter without text replacement", sets, NativeTextView.contentSets);
            assertTrue(f.history.metrics().getLong("nativeBlockCacheHits") > 0);
            tapText(f.history, text(f.history, "Prepared"));
            assertEquals(List.of("docs/prepared.md"), f.opened);
            TextView cell = text(f.history, "Original text");
            Selection.setSelection((Spannable) cell.getText(), 0, cell.length());
            assertTrue(cell.onTextContextMenuItem(android.R.id.copy));
            android.content.ClipboardManager clipboard = (android.content.ClipboardManager) f.controller.get()
                    .getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            assertEquals("Original text", clipboard.getPrimaryClip().getItemAt(0).getText().toString());
        }
    }

    @Test public void replacedOffscreenTablesDiscardTheirPreparedTextAndOldLinkTargets() throws Exception {
        try (NativeMarkwonInteractionTest.Fixture f = new NativeMarkwonInteractionTest.Fixture()) {
            String tail = "An intervening paragraph.\n\n".repeat(40);
            for (int i = 0; i < 4; i++) {
                f.source("| File | Value |\n| --- | --- |\n| [Updated][target] | Value " + i + " |\n\n" + tail
                        + "[target]: docs/version" + i + ".md\n");
                f.ready();
                org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(2));
                assertEquals("Replaced documents must release their prepared cells", 1, f.history.metrics().getInt("nativeCachedBlocks"));
            }
            f.history.scrollToPosition(0); f.ready(); draw(f);
            assertNotNull(text(f.history, "Value 3"));
            tapText(f.history, text(f.history, "Updated"));
            assertEquals(List.of("docs/version3.md"), f.opened);
        }
    }

}
