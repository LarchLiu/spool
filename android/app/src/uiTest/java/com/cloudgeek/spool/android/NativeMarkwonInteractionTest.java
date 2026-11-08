package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.widget.TextView;
import androidx.recyclerview.widget.ConcatAdapter;
import androidx.recyclerview.widget.RecyclerView;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMarkwonInteractionTest {
    static final class Fixture implements AutoCloseable {
        final ActivityController<Activity> controller;
        final NativeHistoryScroll.State state = new NativeHistoryScroll.State();
        final NativeUi ui;
        final NativeMarkwonHistory history;
        final java.util.List<String> opened = new java.util.ArrayList<>();
        Fixture() {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
            controller = Robolectric.buildActivity(Activity.class).setup();
            ui = new NativeUi(controller.get(), false, "en");
            history = new NativeMarkwonHistory(ui, state, opened::add);
            ui.beforeDisclosure = history::preservePosition;
            controller.get().setContentView(history);
        }
        void source(String source) { history.begin(); history.markdown("answer", source); history.end(); }
        void ready() throws Exception { NativeMarkwonHistoryTest.await(history); }
        @Override public void close() { history.dispose(); controller.close(); }
    }
    private TextView text(Fixture f, String contains) {
        return NativeMarkwonHistoryTest.texts(f.history).stream().filter(t -> t.getText().toString().contains(contains)).findFirst().orElseThrow();
    }
    @Test public void extendingLastParagraphPreservesSelectionInUnchangedParagraph() throws Exception {
        try (Fixture f = new Fixture()) {
            String first = "Unchanged paragraph for reading.";
            f.source(first + "\n\nStreaming tail."); f.ready();
            TextView reading = text(f, first);
            f.history.preservePosition(reading);
            Selection.setSelection((Spannable) reading.getText(), 2, 10);
            Object document = reading.getText();
            RecyclerView.Adapter<?> adapter = ((ConcatAdapter) f.history.getAdapter()).getAdapters().get(0);
            long identity = adapter.getItemId(0);
            f.source(first + "\n\nStreaming tail with more tokens."); f.ready();
            TextView after = text(f, first);
            assertEquals("Unchanged paragraph: sameDocument=" + (document == after.getText()) + ", sameStableId=" + (identity == adapter.getItemId(0)), 10, after.getSelectionEnd());
        }
    }
    @Test public void openingLastDisclosureKeepsItsHeadingInPlace() throws Exception {
        try (Fixture f = new Fixture()) {
            NativeDisclosureRow[] heading = new NativeDisclosureRow[1];
            View[] body = new View[1];
            f.history.begin(); f.history.markdown("earlier", "Earlier paragraph.\n\n".repeat(80));
            f.history.chrome("last/heading", true, children -> {
                heading[0] = (NativeDisclosureRow) children.use("toggle", "toggle", () -> new NativeDisclosureRow(f.ui, "Expand", "", "", true, false, false, () -> {
                    body[0].getLayoutParams().height = 1800; body[0].requestLayout();
                }));
                body[0] = children.use("body", "body", () -> f.ui.space(1));
            });
            f.history.chrome("last/footer", true, children -> children.use("gap", "gap", () -> f.ui.space(38)));
            f.history.end(); f.ready();
            int[] before = new int[2]; heading[0].getLocationOnScreen(before);
            assertTrue(f.state.following); assertTrue(before[1] >= 0 && before[1] < 800);
            heading[0].performClick(); f.ready();
            int[] after = new int[2]; heading[0].getLocationOnScreen(after);
            assertEquals("Expanding below a heading must preserve its screen position", before[1], after[1]);
            assertFalse(f.state.following);
        }
    }
    @Test public void nearBottomRetainsExistingNinetyPixelFollowBehavior() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("Paragraph.\n\n".repeat(100)); f.ready();
            assertTrue(f.state.following);
            f.history.scrollBy(0, -40);
            assertTrue("Moving 40 pixels up remains inside the existing 90dp follow threshold", f.state.following);
        }
    }
    @Test public void markdownImageAndBareUrlKeepTheirExistingOpenActions() throws Exception {
        try (Fixture f = new Fixture()) {
            String source = "![diagram](docs/diagram.png)\n\nVisit https://example.com/docs";
            View reference = NativeMarkdown.view(f.ui, source, false, s -> {});
            int before = links(reference);
            assertEquals("The production renderer exposes both actions", 2, before);
            f.source(source); f.ready();
            assertEquals("The trial must retain Markdown image and bare URL actions", before, links(f.history));
        }
    }

    @Test public void changingReferenceDefinitionsInvalidatesOnlyTheirRenderedBlock() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("First paragraph.\n\n[Reference][target]\n\nUnchanged last paragraph.\n\n[target]: https://example.com/old"); f.ready();
            TextView untouched = text(f, "Unchanged last"); Object document = untouched.getText();
            long prepared = f.history.metrics().getLong("preparedBlocks");
            f.source("First paragraph.\n\n[Reference][target]\n\nUnchanged last paragraph.\n\n[target]: https://example.com/new"); f.ready();
            TextView reference = text(f, "Reference");
            for (android.text.style.ClickableSpan link : ((android.text.Spanned) reference.getText()).getSpans(0, reference.length(), android.text.style.ClickableSpan.class)) link.onClick(reference);
            assertEquals(java.util.List.of("https://example.com/new"), f.opened);
            assertSame(document, text(f, "Unchanged last").getText());
            assertEquals("A changed reference must not prepare unrelated blocks", prepared + 1, f.history.metrics().getLong("preparedBlocks"));
        }
    }

    @Test public void insertionDeletionAndReorderingRetainTheReadersDocumentAndLogicalAnchor() throws Exception {
        try (Fixture f = new Fixture()) {
            String tail = "\n\nLater paragraph.".repeat(80);
            f.source("First.\n\nReading paragraph.\n\nAnother.\n\nLast." + tail); f.ready();
            f.history.scrollToPosition(1); f.ready();
            TextView reading = text(f, "Reading"); f.history.preservePosition(reading);
            Object document = reading.getText(); Selection.setSelection((Spannable) reading.getText(), 1, 7);
            int before = reading.getTop();
            f.source("New first.\n\nAnother.\n\nReading paragraph.\n\nLast." + tail); f.ready();
            assertSame(document, text(f, "Reading").getText()); assertEquals(7, text(f, "Reading").getSelectionEnd());
            assertEquals(before, text(f, "Reading").getTop());
            assertFalse(NativeMarkwonHistoryTest.texts(f.history).stream().anyMatch(t -> t.getText().toString().equals("First.")));
        }
    }

    @Test public void escapedLinksCodeAndNestedLabelsKeepNativeClickBoundaries() throws Exception {
        try (Fixture f = new Fixture()) {
            f.source("![diagram](docs/diagram.png) and https://example.com/a.\n\n"
                    + "`https://literal.example` and https\\://escaped.example\n\n"
                    + "[https://label.example](https://target.example) and :codex-file-citation{path=\"src/Main.java\"}");
            f.ready();
            for (TextView text : NativeMarkwonHistoryTest.texts(f.history))
                for (android.text.style.ClickableSpan link : ((android.text.Spanned) text.getText()).getSpans(0, text.length(), android.text.style.ClickableSpan.class)) link.onClick(text);
            assertEquals(4, f.opened.size());
            assertEquals(java.util.Set.of("docs/diagram.png", "https://example.com/a", "https://target.example", "src/Main.java"), new java.util.HashSet<>(f.opened));
        }
    }

    @Test public void unchangedLongHistoryReconcilesEachSegmentOnceWithoutRebinding() throws Exception {
        try (Fixture f = new Fixture()) {
            for (int pass = 0; pass < 2; pass++) {
                long reconciled = f.history.metrics().getLong("reconciledSegments"), binds = f.history.metrics().getLong("binds");
                f.history.begin();
                for (int i = 0; i < 600; i++) f.history.chrome("turn-" + i + "/heading", false,
                        children -> children.use("label", "label", () -> f.ui.text("Turn summary", 13, f.ui.text)));
                f.history.end(); f.ready();
                assertEquals(reconciled + 600, f.history.metrics().getLong("reconciledSegments"));
                if (pass > 0) assertEquals(binds, f.history.metrics().getLong("binds"));
            }
        }
    }
    private int links(View root) {
        int count = 0;
        for (TextView text : NativeMarkwonHistoryTest.texts(root)) if (text.getText() instanceof android.text.Spanned)
            count += ((android.text.Spanned) text.getText()).getSpans(0, text.length(), android.text.style.ClickableSpan.class).length;
        return count;
    }
}
