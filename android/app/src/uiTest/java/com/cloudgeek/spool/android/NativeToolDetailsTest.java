package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.widget.LinearLayout;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeToolDetailsTest {
    private JSONObject summary(String version) throws Exception {
        return new JSONObject().put("ref", "a".repeat(64)).put("href", "/chats/chat/tool-detail?ref=" + "a".repeat(64))
                .put("version", version).put("bytes", 900000).put("kind", "create").put("path", "src/a.go")
                .put("changes", new JSONObject().put("additions", 12).put("deletions", 3));
    }
    private JSONObject page(String text, int offset, int total) throws Exception {
        return new JSONObject().put("ref", "a".repeat(64)).put("version", "v1").put("text", text)
                .put("offset", offset).put("next", offset + text.length()).put("total", total).put("prefix", "hash");
    }
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120)); }
    private NativeToolDetails.Callback receive(List<JSONObject> results) {
        return new NativeToolDetails.Callback() {
            public void received(JSONObject page) { results.add(page); }
            public void failed(Exception error) { throw new AssertionError(error); }
        };
    }

    @Test public void summariesPreserveLabelsAndStatsAndDoNotLoadUntilItemOpens() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            AtomicInteger requests = new AtomicInteger();
            ui.toolDetails = new NativeToolDetails(Runnable::run, Runnable::run, path -> { requests.incrementAndGet(); return page("input", 0, 5); });
            JSONObject part = new JSONObject().put("role", "assistant").put("kind", "tool_use").put("name", "apply_patch").put("text", "").put("tool", summary("v1"));
            NativeActivityModel.Activity item = NativeActivityModel.display(ui, part, "codex");
            assertEquals("Created src/a.go", item.label);
            assertEquals("+12 -3", NativeActivityModel.changeStats(List.of(item)));
            NativeActivityList list = new NativeActivityList(ui, List.of(item), "Working", true, new NativeActivityList.State(), link -> {});
            controller.get().setContentView(list); idle();
            assertEquals(0, requests.get());
            // summary, gap, list -> single item -> disclosure row
            LinearLayout groups = (LinearLayout) list.getChildAt(2);
            LinearLayout single = (LinearLayout) groups.getChildAt(0);
            single.getChildAt(0).performClick(); idle();
            assertEquals(1, requests.get());
            single.getChildAt(0).performClick(); idle();
            assertEquals(1, requests.get());
            single.getChildAt(0).performClick(); idle();
            assertEquals("reopening must use bounded cache", 1, requests.get());
        }
    }

    @Test public void expandedBodyFollowsVersionUsingPrefixAndIgnoresDetachedCallbacks() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<Runnable> pending = new ArrayList<>(); List<String> paths = new ArrayList<>();
            ui.toolDetails = new NativeToolDetails(pending::add, Runnable::run, path -> {
                paths.add(path); return paths.size() == 1 ? page("abc", 0, 3) : page("def", 3, 6);
            });
            LinearLayout host = new LinearLayout(controller.get()); controller.get().setContentView(host);
            NativeToolBody body = new NativeToolBody(ui, summary("v1"), false, true, link -> {});
            host.addView(body); idle(); assertEquals(1, pending.size()); pending.remove(0).run();
            body.bind(summary("v2")); idle(); assertEquals(1, pending.size()); pending.remove(0).run();
            assertTrue(paths.get(1).contains("offset=3&prefix=hash"));
            body.bind(summary("v3")); idle(); assertEquals(1, pending.size());
            host.removeView(body); pending.remove(0).run(); idle();
            assertEquals("no hidden live polling", 3, paths.size());
            body.bind(summary("v4")); idle(); assertTrue(pending.isEmpty());
        }
    }

    @Test public void detailCacheHasBoundedLRUAndImmutableSnapshots() throws Exception {
        AtomicInteger requests = new AtomicInteger(); List<JSONObject> received = new ArrayList<>();
        NativeToolDetails data = new NativeToolDetails(Runnable::run, Runnable::run, path -> {
            requests.incrementAndGet(); return page("text", 0, 4);
        }, 10000, 2);
        data.load(summary("v1"), 0, "", receive(received));
        received.get(0).put("text", "poison");
        data.load(summary("v1"), 0, "", receive(received));
        assertEquals(1, requests.get()); assertEquals("text", received.get(1).optString("text"));
        data.load(summary("v2"), 0, "", receive(received));
        data.load(summary("v3"), 0, "", receive(received));
        data.load(summary("v1"), 0, "", receive(received));
        assertEquals(4, requests.get());
        assertEquals(4, data.metrics().getJSONObject("create").getInt("requests"));
        assertEquals(1, data.metrics().getJSONObject("create").getInt("cacheHits"));
        assertFalse(data.metrics().toString().contains("poison"));
        assertFalse(data.metrics().toString().contains("href"));
    }

    @Test public void appendReplaceAndOversizedBodiesStayWithinOneDisplayPage() throws Exception {
        JSONObject merged = NativeToolDetails.merge(page("abc", 0, 3), page("def", 3, 6));
        assertEquals("abcdef", merged.optString("text")); assertEquals(0, merged.optInt("offset"));
        JSONObject replacement = page("new", 0, 3).put("reset", true);
        assertSame(replacement, NativeToolDetails.merge(merged, replacement));
        JSONObject full = page("x".repeat(32768), 0, 32768);
        JSONObject bounded = NativeToolDetails.merge(full, page("y".repeat(32768), 32768, 65536));
        assertEquals(32768, bounded.optString("text").length()); assertEquals(32768, bounded.optInt("next"));
        assertEquals(65536, bounded.optInt("total"));
    }

    @Test public void thinkingToggleReusesPreviewTextAndUnchangedDisclosureIcons() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            JSONObject part = new JSONObject().put("role", "assistant").put("kind", "thinking").put("text", "**Considering options**\n\nSome details.");
            NativeActivityModel.Activity item = NativeActivityModel.display(ui, part, "codex");
            NativeActivityList list = new NativeActivityList(ui, List.of(item), "Working", true, new NativeActivityList.State(), link -> {});
            controller.get().setContentView(list); idle();
            LinearLayout groups = (LinearLayout) list.getChildAt(2);
            LinearLayout single = (LinearLayout) groups.getChildAt(0);
            NativeDisclosureRow row = (NativeDisclosureRow) single.getChildAt(0);
            CharSequence original = row.label.getText();
            row.performClick(); assertSame("opening must not parse/set the preview again", original, row.label.getText());
            android.widget.ImageView caret = ReflectionHelpers.getField(row, "caret");
            android.graphics.drawable.Drawable expanded = caret.getDrawable();
            row.setExpanded(true); assertSame(expanded, caret.getDrawable());
            row.performClick(); assertSame(original, row.label.getText());
            android.graphics.drawable.Drawable collapsed = caret.getDrawable();
            row.setExpanded(false); assertSame(collapsed, caret.getDrawable());
        }
    }

    @Test public void continuousLiveVersionsDoNotStarveTheFirstDetailResponse() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<Runnable> pending = new ArrayList<>(); List<String> paths = new ArrayList<>();
            ui.toolDetails = new NativeToolDetails(pending::add, Runnable::run, path -> {
                paths.add(path); return paths.size() == 1 ? page("abc", 0, 3) : page("def", 3, 6);
            });
            NativeToolBody body = new NativeToolBody(ui, summary("v1"), true, true, link -> {});
            controller.get().setContentView(body); idle();
            assertEquals(1, pending.size());
            for (int i = 2; i < 12; i++) { body.bind(summary("v"+i)); idle(); assertEquals(1, pending.size()); }
            pending.remove(0).run();
            assertEquals("first response must paint despite newer revisions", 1, paths.size());
            JSONObject visible = ReflectionHelpers.getField(body, "page"); assertEquals("abc", visible.optString("text"));
            idle(); assertEquals(1, pending.size()); pending.remove(0).run();
            assertTrue(paths.get(1).contains("offset=3&prefix=hash"));
            visible = ReflectionHelpers.getField(body, "page"); assertEquals("abcdef", visible.optString("text"));
        }
    }
}
