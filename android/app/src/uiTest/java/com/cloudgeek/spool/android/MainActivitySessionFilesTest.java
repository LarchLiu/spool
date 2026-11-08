package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.content.Intent;
import android.os.Looper;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Production Activity navigation and binary handoffs against the native transport boundary. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivitySessionFilesTest {
    private void call(MainActivityGoalTest.Harness h, String name, String value) {
        ReflectionHelpers.callInstanceMethod(h.activity, name, ReflectionHelpers.ClassParameter.from(String.class, value));
    }
    private void run(MainActivityGoalTest.Harness h) {
        assertFalse(h.io.jobs.isEmpty()); h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
    }
    private void drain(MainActivityGoalTest.Harness h) { while (!h.io.jobs.isEmpty()) run(h); }
    private void responses() {
        MainActivityGoalTest.ShadowClient.responder = call -> {
            try {
                String path = call.get(1); JSONObject body;
                if (path.endsWith("/workspace")) body = new JSONObject().put("path", "/repo/" + path.split("/")[3]).put("kind", "worktree").put("branch", "feature/files").put("available", true);
                else if (path.contains("/files?")) body = new JSONObject().put("entries", new JSONArray());
                else if (path.contains("/file?")) {
                    String target = android.net.Uri.parse(path).getQueryParameter("path");
                    if (path.contains("download=1")) return new JSONObject().put("status", 200).put("body", "downloaded").put("mediaType", "text/plain").toString();
                    body = new JSONObject().put("path", target).put("name", target.substring(target.lastIndexOf('/') + 1))
                            .put("kind", "text").put("external", target.startsWith("/exports/")).put("content", "one\ntwo\nthree").put("size", 13);
                } else if (path.startsWith("/api/permissions")) body = new JSONObject().put("current", "ask").put("modes", new JSONArray().put(new JSONObject().put("id", "ask").put("available", true)));
                else body = new JSONObject();
                return new JSONObject().put("status", 200).put("body", body.toString()).toString();
            } catch (Exception error) { throw new AssertionError(error); }
        };
    }

    @Test public void messageLinksPreviewAndDownloadThroughTheSelectedChatWhileNewChatsUseProjectFiles() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            responses(); call(h, "openMessageLink", "/exports/report.md#L2"); drain(h);
            NativeFileBrowser browser = h.get("fileBrowser"); assertNotNull(browser);
            assertEquals("chat-one", browser.state.chat); assertTrue(browser.state.file.getBoolean("external"));
            assertEquals("report.md:2", browser.preview.name.getText().toString());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().allMatch(call -> call.get(1).startsWith("/api/chats/chat-one/")));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(1).contains("/files?path=%2Fexports")));
            browser.preview.download.performClick(); run(h);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().anyMatch(call -> call.get(1).equals("/api/chats/chat-one/file?path=%2Fexports%2Freport.md&download=1")));
            Intent picker = Shadows.shadowOf(h.activity).getNextStartedActivity(); assertNotNull(picker);
            assertEquals(Intent.ACTION_CREATE_DOCUMENT, picker.getAction()); assertEquals("report.md", picker.getStringExtra(Intent.EXTRA_TITLE));
            call(h, "newChat", "project-one"); drain(h);
            MainActivityGoalTest.ShadowClient.calls.clear(); call(h, "openFiles", "."); drain(h);
            NativeFileBrowser fresh = h.get("fileBrowser");
            assertNotSame(browser.state, fresh.state); assertEquals("", fresh.state.chat); assertNull(fresh.state.file);
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            assertEquals("/api/projects/project-one/files?path=.", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
        }
    }

    @Test @Config(sdk = {26, 34, 35}) public void markdownTapsReachTheFilePreviewAndExternalBrowserThroughTheRealActivity() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            responses();
            String answer = "[Read report](/exports/report.md#L2)\n\n[Open website](https://example.com/docs)";
            JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Show files"))
                    .put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", answer));
            JSONObject turn = new JSONObject("{id:'turn',user_id:'alice',backend:'codex',status:'completed',part_start:0,input_end:1,part_end:2}");
            h.<JSONObject>get("chat").put("project_id", "project-one").put("title", "Links")
                    .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts))
                    .put("history", new JSONObject().put("has_more", false));
            h.call("renderChat"); NativeMarkwonHistory history = h.get("markwonHistory"); NativeMarkwonHistoryTest.await(history);
            android.widget.TextView website = NativeMarkwonHistoryTest.texts(history).stream()
                    .filter(t -> t.getText().toString().equals("Open website")).findFirst().orElseThrow();
            NativeMarkwonInteractionTest.tapLink(history, website, "https://example.com/docs");
            Intent intent = Shadows.shadowOf(h.activity).getNextStartedActivity(); assertNotNull(intent);
            assertEquals(Intent.ACTION_VIEW, intent.getAction()); assertTrue(intent.hasCategory(Intent.CATEGORY_BROWSABLE));
            assertEquals("https://example.com/docs", intent.getDataString());
            assertNull("A single tap opens the browser only once", Shadows.shadowOf(h.activity).getNextStartedActivity());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());

            android.widget.TextView report = NativeMarkwonHistoryTest.texts(history).stream()
                    .filter(t -> t.getText().toString().equals("Read report")).findFirst().orElseThrow();
            NativeMarkwonInteractionTest.tapLink(history, report, "/exports/report.md#L2"); drain(h);
            NativeFileBrowser browser = h.get("fileBrowser"); assertNotNull(browser);
            assertEquals("chat-one", browser.state.chat);
            assertEquals("report.md:2", browser.preview.name.getText().toString());
            assertEquals("/exports/report.md", browser.state.file.getString("path"));
            assertEquals("one\ntwo\nthree", browser.state.file.getString("content"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream()
                    .anyMatch(call -> call.get(1).equals("/api/chats/chat-one/file?path=%2Fexports%2Freport.md")));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream()
                    .allMatch(call -> call.get(1).startsWith("/api/chats/chat-one/")));
        }
    }

    @Test public void switchingChatsInTheSameProjectRejectsQueuedFilesAndOldDownloadActions() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            responses();
            h.set("chats", new JSONArray().put(h.chat("chat-one").put("project_id", "project-one")).put(h.chat("chat-two").put("project_id", "project-one")));
            call(h, "openMessageLink", "README.md#L3"); drain(h);
            NativeFileBrowser old = h.get("fileBrowser"); NativeFileModel oldState = old.state;
            old.select(new NativeFileTarget("stale.md", 0, 0));
            List<Runnable> stale = new ArrayList<>(h.io.jobs); h.io.jobs.clear();
            call(h, "openChat", "chat-two"); drain(h);
            call(h, "openFiles", "."); drain(h);
            NativeFileBrowser current = h.get("fileBrowser");
            assertEquals("chat-two", current.state.chat); assertNotSame(oldState, current.state);
            stale.forEach(Runnable::run); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNull(current.state.file); assertNull(current.state.target); assertNull(current.split.preview);
            int count = MainActivityGoalTest.ShadowClient.calls.size(); old.preview.download.performClick();
            assertTrue(h.io.jobs.isEmpty()); assertEquals(count, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void stopPausesDirectoryPollingAndResumeFetchesCurrentMetadata() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            responses(); call(h, "openFiles", "."); drain(h);
            h.controller.pause().stop();
            int count = MainActivityGoalTest.ShadowClient.calls.size();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(30));
            assertEquals(count, MainActivityGoalTest.ShadowClient.calls.size()); assertTrue(h.io.jobs.isEmpty());
            h.controller.start().resume(); drain(h);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.subList(count, MainActivityGoalTest.ShadowClient.calls.size()).stream().anyMatch(call -> call.get(1).equals("/api/chats/chat-one/workspace")));
        }
    }

    @Test public void chatRevisionsRefreshWorkspaceInformationWithoutWaitingForThePoll() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            responses(); h.<JSONObject>get("chat").put("updated_at", 1);
            call(h, "openFiles", "."); drain(h); MainActivityGoalTest.ShadowClient.calls.clear();
            ReflectionHelpers.callInstanceMethod(h.activity, "receiveChatEvent", ReflectionHelpers.ClassParameter.from(String.class, "metadata"),
                    ReflectionHelpers.ClassParameter.from(JSONObject.class, new JSONObject().put("id", "chat-one").put("updated_at", 2)));
            assertFalse(h.io.jobs.isEmpty()); drain(h);
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            assertEquals("/api/chats/chat-one/workspace", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
        }
    }
}
