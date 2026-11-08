package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
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
public class NativeActivityRecyclerTest {
    private static final class Fixture implements AutoCloseable {
        final ActivityController<Activity> controller;
        final NativeUi ui;
        final NativeHistoryScroll.State position = new NativeHistoryScroll.State();
        final NativeActivityList.State state = new NativeActivityList.State();
        final NativeMarkwonHistory history;
        NativeTurnPresentation value;
        NativeActivityList rows;
        String prefix = "";
        Fixture() {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
            controller = Robolectric.buildActivity(Activity.class).setup(); ui = new NativeUi(controller.get(), false, "en");
            history = new NativeMarkwonHistory(ui, position, s -> {}); ui.beforeDisclosure = history::preservePosition;
            controller.get().setContentView(history);
        }
        void show(JSONArray parts, NativeTranscriptImages images) throws Exception {
            JSONObject turn = new JSONObject("{status:'running',backend:'codex'}").put("part_end", parts.length());
            value = NativeTurnPresentation.turn(parts, turn);
            rows = NativeActivityList.virtualRows(ui, state, s -> {}, images, this::render);
            rows.bindVirtual(value, null); render(); ready();
        }
        void render() {
            history.begin();
            if (!prefix.isEmpty()) history.markdown("earlier", prefix);
            history.chrome("turn/heading", true, children -> {
                NativeTurn response = (NativeTurn) children.use("response", "response", () -> new NativeTurn(ui, value, state, s -> {}, null, false, this::render));
                response.bind(value);
            });
            rows.appendVirtualRows(history, "turn/activity", true);
            history.chrome("turn/footer", false, children -> children.use("gap", "gap", () -> ui.space(38)));
            history.end();
        }
        void ready() throws Exception { NativeMarkwonHistoryTest.await(history); }
        @Override public void close() { history.dispose(); controller.close(); }
    }
    private static <T> List<T> views(View root, Class<T> type) {
        List<T> result = new ArrayList<>(); if (type.isInstance(root)) result.add(type.cast(root));
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) result.addAll(views(((ViewGroup) root).getChildAt(i), type));
        return result;
    }
    private NativeDisclosureRow heading(View root, String label) {
        return views(root, NativeDisclosureRow.class).stream().filter(v -> v.label.getText().toString().equals(label)).findFirst().orElseThrow();
    }
    private int screenY(View view) { int[] location = new int[2]; view.getLocationOnScreen(location); return location[1]; }

    @Test public void hundredsOfExpandedToolsMountOnlyVisibleRowsAndReuseTheirHolders() throws Exception {
        try (Fixture f = new Fixture()) {
            f.prefix = "Earlier paragraph.\n\n".repeat(40);
            JSONArray parts = new JSONArray();
            for (int i = 0; i < 400; i++) parts.put(new JSONObject().put("role", "assistant").put("kind", "tool_use")
                    .put("id", "tool-" + i).put("name", "read_file").put("tool", new JSONObject().put("kind", "read").put("path", "file-" + i + ".md")));
            f.show(parts, null); assertTrue(f.history.blocks() > 400);
            for (int position : new int[] {40, 140, 240, 340, 40, 439}) {
                f.history.scrollToPosition(position); f.ready();
                assertTrue("Expanded tools must be separate visible rows", f.history.getChildCount() < 40);
                assertTrue("A holder must never recreate the full tool subtree", views(f.history, NativeDisclosureRow.class).size() < 40);
                assertTrue("Mounted native views stay bounded", views(f.history, View.class).size() < 500);
            }
            assertTrue("Native row containers are recycled: " + f.history.metrics(), f.history.metrics().getLong("creates") < 120);
            assertTrue(f.history.metrics().getLong("recycles") > 0);
            f.history.scrollToPosition(40); f.ready(); NativeDisclosureRow group = heading(f.history, "Read files");
            group.performClick(); f.ready();
            assertTrue(f.history.blocks() < 50); assertTrue(screenY(heading(f.history, "Read files")) >= 0);
            assertFalse(f.position.following);
            group = heading(f.history, "Read files"); int before = screenY(group);
            group.performClick(); f.ready(); assertTrue(f.history.blocks() > 400);
            assertEquals(before, screenY(heading(f.history, "Read files")));
        }
    }

    @Test public void delayedToolBodyKeepsItsHeadingAndStopsWithItsRecycledRow() throws Exception {
        try (Fixture f = new Fixture()) {
            List<Runnable> jobs = new ArrayList<>(); String body = "Delayed output\n".repeat(60);
            f.ui.toolDetails = new NativeToolDetails(jobs::add, Runnable::run, path -> new JSONObject()
                    .put("text", body).put("offset", 0).put("next", body.length()).put("total", body.length()).put("version", "v1"));
            f.prefix = "Earlier paragraph.\n\n".repeat(80);
            f.show(new JSONArray().put(new JSONObject().put("role", "assistant").put("kind", "tool_use").put("name", "exec_command")
                    .put("tool", new JSONObject().put("kind", "shell").put("version", "v1").put("ref", "a".repeat(64))
                            .put("href", "/chats/chat-one/tool-detail?ref=" + "a".repeat(64)))), null);
            assertTrue(jobs.isEmpty()); NativeDisclosureRow tool = heading(f.history, "Ran command"); int before = screenY(tool);
            tool.performClick(); f.ready(); assertFalse(f.position.following); assertEquals(before, screenY(tool));
            assertEquals(1, jobs.size()); jobs.remove(0).run(); f.ready(); assertEquals(before, screenY(tool));
            NativeToolDetail detail = views(f.history, NativeToolDetail.class).get(0);
            assertTrue(NativeMarkwonHistoryTest.texts(detail).stream().anyMatch(t -> t.getText().toString().contains("Delayed output")));
            f.history.scrollToPosition(0); f.ready(); assertFalse(detail.isAttachedToWindow());
            assertTrue(views(f.history, NativeToolDetail.class).isEmpty());
            f.history.scrollToPosition(f.history.blocks() - 1); f.ready(); assertTrue(f.state.items.contains("0:0"));
            assertEquals(1, views(f.history, NativeToolDetail.class).size());
        }
    }

    @Test public void imageDisclosureKeepsItsAnchorAndPreviewOwnerAcrossRecycling() throws Exception {
        try (Fixture f = new Fixture()) {
            List<String> opened = new ArrayList<>(), loaded = new ArrayList<>();
            NativeTranscriptImages images = new NativeTranscriptImages() {
                public void load(ImageView image, String path, String source, int edge, java.util.function.Consumer<Exception> done) { loaded.add(source); done.accept(null); }
                public void preview(String path, String source, String name) { opened.add(source); }
            };
            f.prefix = "Earlier paragraph.\n\n".repeat(80);
            f.show(new JSONArray("[{role:'tool',kind:'tool_result',images:['first','second'],text:''}]"), images);
            NativeDisclosureRow heading = heading(f.history, "Viewed 2 images");
            assertEquals(2, loaded.size()); NativeViewedImages grid = views(f.history, NativeViewedImages.class).get(0);
            grid.getChildAt(0).performClick(); assertEquals(List.of("first"), opened);
            heading.performClick(); f.ready();
            assertTrue(views(f.history, NativeViewedImages.class).isEmpty()); assertFalse(f.position.following);
            int before = screenY(heading);
            heading.performClick(); f.ready(); assertEquals(before, screenY(heading));
            f.history.scrollToPosition(0); f.ready(); grid.getChildAt(1).performClick(); assertEquals(1, opened.size());
            f.history.scrollToPosition(f.history.blocks() - 1); f.ready();
            grid = views(f.history, NativeViewedImages.class).get(0); grid.getChildAt(1).performClick();
            assertEquals(List.of("first", "second"), opened); assertTrue(f.state.imageGroups.get(0));
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void mixedRowTypesKeepReusableToolWidgetsWithoutSharingDetailsOrReparsingWarmPreviews() throws Exception {
        try (Fixture f = new Fixture()) {
            f.show(new JSONArray("[{role:'assistant',kind:'thinking',text:'**First thought**'},"
                    + "{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'notes.md'}},"
                    + "{role:'assistant',kind:'text',phase:'commentary',text:'Progress'},"
                    + "{role:'tool',kind:'tool_result',images:['image'],text:''}]"), null);
            List<? extends androidx.recyclerview.widget.RecyclerView.Adapter<?>> adapters =
                    ((androidx.recyclerview.widget.ConcatAdapter) f.history.getAdapter()).getAdapters();
            androidx.recyclerview.widget.RecyclerView.Adapter thought = adapters.get(1), read = adapters.get(2), progress = adapters.get(3), image = adapters.get(4);
            assertEquals(thought.getItemViewType(0), read.getItemViewType(0));
            assertNotEquals(read.getItemViewType(0), progress.getItemViewType(0));
            assertNotEquals(image.getItemViewType(0), progress.getItemViewType(0));
            androidx.recyclerview.widget.RecyclerView.ViewHolder holder = thought.createViewHolder(f.history, thought.getItemViewType(0));
            thought.bindViewHolder(holder, 0); NativeDisclosureRow original = views(holder.itemView, NativeDisclosureRow.class).get(0);
            original.performClick(); assertTrue(f.state.items.contains("0:0"));
            thought.onViewRecycled(holder); read.bindViewHolder(holder, 0);
            assertSame(original, views(holder.itemView, NativeDisclosureRow.class).get(0));
            assertEquals("Read notes.md", original.label.getText().toString());
            assertTrue(views(holder.itemView, NativeToolDetail.class).isEmpty());
            original.performClick(); assertTrue(f.state.items.contains("1:0")); assertTrue(f.state.items.contains("0:0"));
            java.util.Map<?, ?> cache = org.robolectric.util.ReflectionHelpers.getField(f.rows, "previews");
            Object preview = cache.values().iterator().next();
            read.onViewRecycled(holder); thought.bindViewHolder(holder, 0);
            assertSame(original, views(holder.itemView, NativeDisclosureRow.class).get(0)); assertEquals(1, cache.size());
            assertSame("The warm thinking preview is reused", preview, cache.values().iterator().next());
            assertTrue(original.label.getText().toString().contains("First thought"));
            assertEquals(1, views(holder.itemView, NativeToolDetail.class).size());
            assertFalse(NativeMarkwonHistoryTest.texts(holder.itemView).stream().anyMatch(t -> t.getText().toString().contains("notes.md")));
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void toolWidgetsTransferAcrossTurnsWithIndependentDetailsAndClickOwners() throws Exception {
        try (Fixture f = new Fixture()) {
            NativeActivityList.State other = new NativeActivityList.State();
            NativeActivityList first = NativeActivityList.virtualRows(f.ui, f.state, s -> {}, null, () -> {});
            NativeActivityList second = NativeActivityList.virtualRows(f.ui, other, s -> {}, null, () -> {});
            JSONObject turn = new JSONObject("{status:'running',backend:'codex',part_end:1}");
            first.bindVirtual(NativeTurnPresentation.turn(new JSONArray("[{role:'assistant',kind:'thinking',text:'First owner'}]"), turn), null);
            second.bindVirtual(NativeTurnPresentation.turn(new JSONArray("[{role:'assistant',kind:'thinking',text:'Second owner'}]"), turn), null);
            f.history.begin(); first.appendVirtualRows(f.history, "first", false); second.appendVirtualRows(f.history, "second", false); f.history.end();
            List<? extends androidx.recyclerview.widget.RecyclerView.Adapter<?>> adapters = ((androidx.recyclerview.widget.ConcatAdapter) f.history.getAdapter()).getAdapters();
            androidx.recyclerview.widget.RecyclerView.Adapter a = adapters.get(0), b = adapters.get(2);
            androidx.recyclerview.widget.RecyclerView.ViewHolder holder = a.createViewHolder(f.history, a.getItemViewType(0));
            a.bindViewHolder(holder, 0); NativeDisclosureRow heading = views(holder.itemView, NativeDisclosureRow.class).get(0);
            heading.performClick(); assertTrue(f.state.items.contains("0:0")); assertTrue(other.items.isEmpty());
            NativeToolDetail oldDetail = views(holder.itemView, NativeToolDetail.class).get(0);
            a.onViewRecycled(holder); b.bindViewHolder(holder, 0);
            assertSame(heading, views(holder.itemView, NativeDisclosureRow.class).get(0));
            assertTrue(heading.label.getText().toString().contains("Second owner"));
            assertNull(oldDetail.getParent()); assertTrue(views(holder.itemView, NativeToolDetail.class).isEmpty());
            heading.performClick(); assertTrue(other.items.contains("0:0")); assertTrue(f.state.items.contains("0:0"));
            assertNotSame(oldDetail, views(holder.itemView, NativeToolDetail.class).get(0));
            b.onViewRecycled(holder); a.bindViewHolder(holder, 0);
            assertSame(heading, views(holder.itemView, NativeDisclosureRow.class).get(0));
            heading.performClick(); assertFalse(f.state.items.contains("0:0")); assertTrue(other.items.contains("0:0"));
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void reusedGroupHeadingOnlyChangesTheCurrentTurnsGroup() throws Exception {
        try (Fixture f = new Fixture()) {
            NativeActivityList.State other = new NativeActivityList.State(); int[] changes = new int[2];
            NativeActivityList first = NativeActivityList.virtualRows(f.ui, f.state, s -> {}, null, () -> changes[0]++);
            NativeActivityList second = NativeActivityList.virtualRows(f.ui, other, s -> {}, null, () -> changes[1]++);
            JSONArray parts = new JSONArray("[{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'one'}},"
                    + "{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'two'}}]");
            NativeTurnPresentation value = NativeTurnPresentation.turn(parts, new JSONObject("{status:'running',backend:'codex',part_end:2}"));
            first.bindVirtual(value, null); second.bindVirtual(value, null);
            f.history.begin(); first.appendVirtualRows(f.history, "first", false); second.appendVirtualRows(f.history, "second", false); f.history.end();
            List<? extends androidx.recyclerview.widget.RecyclerView.Adapter<?>> adapters = ((androidx.recyclerview.widget.ConcatAdapter) f.history.getAdapter()).getAdapters();
            androidx.recyclerview.widget.RecyclerView.Adapter a = adapters.get(0), b = adapters.get(4);
            androidx.recyclerview.widget.RecyclerView.ViewHolder holder = a.createViewHolder(f.history, a.getItemViewType(0));
            a.bindViewHolder(holder, 0); NativeDisclosureRow heading = views(holder.itemView, NativeDisclosureRow.class).get(0);
            a.onViewRecycled(holder); b.bindViewHolder(holder, 0);
            assertSame(heading, views(holder.itemView, NativeDisclosureRow.class).get(0));
            heading.performClick(); assertTrue(f.state.groups.contains(0)); assertFalse(other.groups.contains(0));
            assertArrayEquals(new int[] {0, 1}, changes);
        }
    }

    @Test public void fixedProbePositionUsesAnExactOffsetAndRejectsMissingRows() throws Exception {
        try (Fixture f = new Fixture()) {
            f.prefix = "Earlier paragraph.\n\n".repeat(80);
            f.show(new JSONArray("[{role:'assistant',kind:'thinking',text:'Thought'}]"), null);
            assertTrue(f.history.probePosition(25)); f.ready();
            assertEquals(25, f.history.metrics().getInt("firstVisible")); assertEquals(0, f.history.metrics().getInt("firstTop"));
            assertFalse(f.position.following);
            assertFalse(f.history.probePosition(-1)); assertFalse(f.history.probePosition(f.history.blocks()));
        }
    }

    @Test public void fullTraversalRestoresVisibleContentAndGeometryAtTheSameRow() throws Exception {
        try (Fixture f = new Fixture()) {
            JSONArray parts = new JSONArray();
            for (int i = 0; i < 120; i++) parts.put(new JSONObject().put("role", "assistant").put("kind", "tool_use")
                    .put("id", "tool-" + i).put("name", "read_file").put("tool", new JSONObject().put("kind", "read").put("path", "file-" + i + ".md")));
            f.show(parts, null);
            f.history.probePosition(40); f.ready(); JSONObject original = f.history.probeVisibleContent("test");
            f.history.probePosition(0); f.ready(); assertFalse(f.history.metrics().getBoolean("canScrollUp"));
            f.history.probePosition(f.history.blocks() - 1); f.ready(); assertFalse(f.history.metrics().getBoolean("canScrollDown"));
            f.history.probePosition(40); f.ready();
            assertEquals(original.toString(), f.history.probeVisibleContent("test").toString());
            assertNotEquals(original.getString("sha256"), f.history.probeVisibleContent("another-run").getString("sha256"));
            NativeDisclosureRow row = views(f.history.findViewHolderForAdapterPosition(40).itemView, NativeDisclosureRow.class).get(0);
            row.label.setText("Incorrect recycled title");
            assertNotEquals(original.getString("sha256"), f.history.probeVisibleContent("test").getString("sha256"));
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void flatToolContainersPreserveNestedBordersSpacingAndExpandedDetails() throws Exception {
        try (Fixture f = new Fixture()) {
            f.show(new JSONArray("[{role:'assistant',kind:'tool_use',name:'read_file',text:'first',tool:{kind:'read',path:'one'}},"
                    + "{role:'assistant',kind:'tool_use',name:'read_file',text:'second',tool:{kind:'read',path:'two'}}]"), null);
            List<? extends androidx.recyclerview.widget.RecyclerView.Adapter<?>> adapters = ((androidx.recyclerview.widget.ConcatAdapter) f.history.getAdapter()).getAdapters();
            for (int at : new int[] {2, 3}) for (boolean expanded : new boolean[] {false, true}) {
                f.state.items.clear(); if (expanded) f.state.items.add("0:" + (at - 2));
                androidx.recyclerview.widget.RecyclerView.Adapter adapter = adapters.get(at);
                View[] roots = new View[2];
                for (int mode = 0; mode < 2; mode++) {
                    f.history.reuseActivityRows = mode == 1;
                    androidx.recyclerview.widget.RecyclerView.ViewHolder holder = adapter.createViewHolder(f.history, adapter.getItemViewType(0));
                    adapter.bindViewHolder(holder, 0); roots[mode] = holder.itemView;
                    roots[mode].measure(View.MeasureSpec.makeMeasureSpec(320, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                    roots[mode].layout(0, 0, 320, roots[mode].getMeasuredHeight());
                }
                assertEquals(roots[0].getHeight(), roots[1].getHeight());
                android.graphics.Bitmap a = android.graphics.Bitmap.createBitmap(320, roots[0].getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                android.graphics.Bitmap b = android.graphics.Bitmap.createBitmap(320, roots[1].getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                try { roots[0].draw(new android.graphics.Canvas(a)); roots[1].draw(new android.graphics.Canvas(b)); assertTrue(a.sameAs(b)); }
                finally { a.recycle(); b.recycle(); }
            }
        }
    }

    @Test public void standardExpandedScenarioRestoresTheSameImageAndDetailState() throws Exception {
        try (Fixture f = new Fixture()) {
            f.show(new JSONArray("[{role:'assistant',kind:'tool_use',name:'read_file',tool:{kind:'read',path:'one'}},"
                    + "{role:'tool',kind:'tool_result',images:['image-one']},"
                    + "{role:'assistant',kind:'tool_use',name:'exec_command',tool:{kind:'shell',label:'Run'}},"
                    + "{role:'tool',kind:'tool_result',images:['image-two']},"
                    + "{role:'assistant',kind:'thinking',text:'Thought'},"
                    + "{role:'tool',kind:'tool_result',images:['image-three']}]"), null);
            int group = f.rows.probeExpansion(); f.render(); f.ready();
            java.util.Set<String> details = new java.util.HashSet<>(f.state.items);
            int count = f.history.blocks(); assertEquals(1, details.size()); assertTrue(f.state.imageGroups.size() >= 2);
            assertEquals(f.state.count, f.state.groups.size());
            f.state.groups.clear(); f.state.items.clear(); f.state.imageGroups.clear();
            assertEquals(group, f.rows.probeExpansion()); f.render(); f.ready();
            assertEquals(details, f.state.items); assertEquals(count, f.history.blocks());
        }
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    @Test public void pooledImageHeadingUsesItsNewGroupIndex() throws Exception {
        try (Fixture f = new Fixture()) {
            f.show(new JSONArray("[{role:'tool',kind:'tool_result',images:['first']},"
                    + "{role:'assistant',kind:'thinking',text:'Between images'},"
                    + "{role:'tool',kind:'tool_result',images:['second']}]"), null);
            List<? extends androidx.recyclerview.widget.RecyclerView.Adapter<?>> adapters = ((androidx.recyclerview.widget.ConcatAdapter) f.history.getAdapter()).getAdapters();
            androidx.recyclerview.widget.RecyclerView.Adapter first = adapters.get(1), second = adapters.get(3);
            androidx.recyclerview.widget.RecyclerView.ViewHolder holder = first.createViewHolder(f.history, first.getItemViewType(0));
            first.bindViewHolder(holder, 0); NativeDisclosureRow heading = views(holder.itemView, NativeDisclosureRow.class).get(0);
            first.onViewRecycled(holder); second.bindViewHolder(holder, 0);
            assertSame(heading, views(holder.itemView, NativeDisclosureRow.class).get(0));
            heading.performClick(); assertFalse(f.state.imageGroups.get(2)); assertFalse(f.state.imageGroups.containsKey(0));
            assertTrue(views(holder.itemView, NativeViewedImages.class).isEmpty());
        }
    }
}
