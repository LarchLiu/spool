package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Intent;
import android.content.res.Configuration;
import android.os.Looper;
import java.io.ByteArrayInputStream;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadow.api.Shadow;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityProjectUploadTest.ShadowClient.class,
        MainActivityProjectUploadTest.ShadowFiles.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityProjectUploadTest {
    @Implements(value = spoolnet.Client.class, isInAndroidSdk = false)
    public static class ShadowClient extends MainActivityGoalTest.ShadowClient {
        @Implementation protected spoolnet.ProjectFiles openProjectFiles() { return Shadow.newInstanceOf(spoolnet.ProjectFiles.class); }
    }
    @Implements(value = spoolnet.ProjectFiles.class, isInAndroidSdk = false)
    public static class ShadowFiles {
        static final List<List<String>> uploads = new ArrayList<>();
        static final List<byte[]> bytes = new ArrayList<>();
        static int closes;
        static Runnable duringUpload = () -> {};
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__() {}
        @Implementation protected String upload(String path, String name, String csrf, String locale, spoolnet.DesktopFileSource source) throws Exception {
            uploads.add(List.of(path, name, csrf, locale)); java.io.ByteArrayOutputStream data = new java.io.ByteArrayOutputStream();
            duringUpload.run();
            byte[] chunk; while ((chunk = source.read(8192)).length > 0) data.write(chunk); bytes.add(data.toByteArray());
            return "{status:201,body:'{}'}";
        }
        @Implementation protected void close() { closes++; }
    }
    private void open(MainActivityGoalTest.Harness h) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openFiles", ReflectionHelpers.ClassParameter.from(String.class, ".")); drain(h);
    }
    private void drain(MainActivityGoalTest.Harness h) {
        while (!h.io.jobs.isEmpty()) { h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    }
    private void responses(MainActivityGoalTest.Harness h) {
        ShadowFiles.uploads.clear(); ShadowFiles.bytes.clear(); ShadowFiles.closes = 0;
        ShadowFiles.duringUpload = () -> {};
        ShadowClient.responder = call -> {
            try {
                if (call.get(1).equals("/api/navigation")) return new JSONObject().put("status", 200).put("body", new JSONObject()
                        .put("projects", h.<JSONArray>get("projects")).put("chats", h.<JSONObject>get("chat") == null ? new JSONArray() : new JSONArray().put(h.<JSONObject>get("chat"))).toString()).toString();
                if (call.get(1).equals("/api/profiles") || call.get(1).equals("/api/agents")) return "{status:200,body:'[]'}";
                return call.get(1).endsWith("/workspace") ? "{status:200,body:'{path:\"/repo/work tree\",available:true}'}" : "{status:200,body:'{entries:[]}'}";
            } catch (Exception error) { throw new AssertionError(error); }
        };
    }
    private void rotate(MainActivityGoalTest.Harness h) {
        Configuration config = new Configuration(h.activity.getResources().getConfiguration());
        config.orientation = Configuration.ORIENTATION_LANDSCAPE;
        config.screenWidthDp = 800; config.screenHeightDp = 360;
        h.activity.onConfigurationChanged(config);
    }
    @Test public void pickerAndQueuedResultsSurviveRotationWithTheCapturedDestination() throws Exception {
        for (boolean existing : new boolean[]{true, false}) for (boolean resultFirst : new boolean[]{false, true})
                try (var h = new MainActivityGoalTest.Harness(existing)) {
            responses(h); h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", true);
            open(h); NativeFileBrowser original = h.get("fileBrowser");
            NativeProjectUploadTest.provider(h.activity);
            byte[] bytes = {0, 1, (byte) 255};
            var uri = NativeProjectUploadTest.document(h.activity, "rotation", new ByteArrayInputStream(bytes));
            original.upload.accept("src"); var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult(); assertNotNull(picker);
            h.controller.pause().stop();
            if (resultFirst) h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri));
            rotate(h);
            NativeFileBrowser rebound = h.get("fileBrowser"); assertNotSame(original, rebound);
            assertTrue("The new panel must retain the pending selection or upload", rebound.uploadBusy);
            if (!resultFirst) h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri));
            h.controller.start().resume(); drain(h);
            assertEquals(List.of(List.of(existing ? "/api/chats/chat-one/upload?path=src&workspace=%2Frepo%2Fwork%20tree" : "/api/projects/project-one/upload?path=src",
                    "中文 & report.bin", "test-csrf", "en")), ShadowFiles.uploads);
            assertArrayEquals(bytes, ShadowFiles.bytes.get(0)); assertEquals(1, ShadowFiles.closes);
            assertFalse(rebound.uploadBusy);
            assertTrue(((android.widget.TextView) rebound.notices.getChildAt(0)).getText().toString().contains("Uploaded 1 files to src"));
        }
    }
    @Test public void activeTransferSurvivesRotationAndRefreshesTheNewPanel() throws Exception {
        try (var h = new MainActivityGoalTest.Harness(true)) {
            responses(h); h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", true);
            open(h); NativeFileBrowser original = h.get("fileBrowser");
            NativeProjectUploadTest.provider(h.activity);
            byte[] bytes = {1, 2, 3};
            var uri = NativeProjectUploadTest.document(h.activity, "active-rotation", new ByteArrayInputStream(bytes));
            original.upload.accept("src"); var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult(); assertNotNull(picker);
            ShadowFiles.duringUpload = () -> {
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                rotate(h);
                NativeFileBrowser rebound = h.get("fileBrowser"); assertNotSame(original, rebound);
                assertTrue(rebound.uploadBusy); assertEquals(0, ShadowFiles.closes);
                assertTrue(((android.widget.TextView) rebound.notices.getChildAt(0)).getText().toString().contains("中文 & report.bin"));
                ShadowClient.calls.clear();
            };
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri)); drain(h);
            assertEquals(1, ShadowFiles.uploads.size()); assertArrayEquals(bytes, ShadowFiles.bytes.get(0)); assertEquals(1, ShadowFiles.closes);
            assertFalse(h.<NativeFileBrowser>get("fileBrowser").uploadBusy);
            assertTrue("Completion must refresh the new panel's destination", ShadowClient.calls.stream().anyMatch(call -> call.get(1).contains("/files?path=src")));
        }
    }
    @Test public void rotationStillDiscardsSelectionsAfterSessionSelectionOrPanelChanges() throws Exception {
        for (int mode = 0; mode < 5; mode++) try (var h = new MainActivityGoalTest.Harness(true)) {
            responses(h); h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", true);
            open(h); NativeProjectUploadTest.provider(h.activity);
            var uri = NativeProjectUploadTest.document(h.activity, "stale-rotation", new ByteArrayInputStream(new byte[]{1}));
            h.<NativeFileBrowser>get("fileBrowser").upload.accept("src");
            var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult(); assertNotNull(picker);
            if (mode == 0) h.set("authEpoch", h.<Integer>get("authEpoch") + 1);
            if (mode == 1) h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            if (mode == 2) h.call("closeDrawer");
            rotate(h);
            if (mode == 3) h.call("closeDrawer");
            if (mode == 4) {
                h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", false);
                h.<NativeProjectUpload>get("projectUpload").permissionsChanged();
            }
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri)); drain(h);
            assertTrue(ShadowFiles.uploads.isEmpty());
            NativeFileBrowser browser = h.get("fileBrowser");
            if (browser != null) assertFalse(browser.uploadBusy);
        }
    }
    @Test public void activityPickerSurvivesStopAndUploadsWithIndependentPermissionAndSessionTransport() throws Exception {
        for (boolean existing : new boolean[]{true, false}) try (var h = new MainActivityGoalTest.Harness(existing)) {
            responses(h); h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", true).put("can_create", false);
            if (existing) h.<JSONObject>get("chat").put("can_control", false);
            open(h); NativeFileBrowser browser = h.get("fileBrowser"); assertTrue(browser.canUpload.getAsBoolean());
            NativeProjectUploadTest.provider(h.activity);
            var uri = NativeProjectUploadTest.document(h.activity, "upload", new ByteArrayInputStream(new byte[]{0, 1, (byte) 255}));
            browser.upload.accept("src"); var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult(); assertNotNull(picker);
            h.controller.pause().stop();
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri));
            assertTrue("Picker result must queue an upload", browser.uploadBusy);
            h.controller.start().resume(); drain(h);
            String status = browser.notices.getChildCount() == 0 ? "" : ((android.widget.TextView) browser.notices.getChildAt(0)).getText().toString();
            assertEquals("Requests: " + ShadowClient.calls + "; panel retained: " + (browser == h.get("fileBrowser")) + "; status: " + status, List.of(List.of(existing ? "/api/chats/chat-one/upload?path=src&workspace=%2Frepo%2Fwork%20tree" : "/api/projects/project-one/upload?path=src",
                    "中文 & report.bin", "test-csrf", "en")), ShadowFiles.uploads);
            assertArrayEquals(new byte[]{0, 1, (byte) 255}, ShadowFiles.bytes.get(0)); assertEquals(1, ShadowFiles.closes);
        }
    }
    @Test public void defaultPermissionAndClosedPanelDiscardPickerResults() throws Exception {
        try (var h = new MainActivityGoalTest.Harness(true)) {
            responses(h); open(h); NativeFileBrowser browser = h.get("fileBrowser"); assertFalse(browser.canUpload.getAsBoolean());
            browser.upload.accept("."); assertNull(Shadows.shadowOf(h.activity).getNextStartedActivityForResult());
            h.<JSONArray>get("projects").getJSONObject(0).put("can_upload", true);
            browser.upload.accept("."); var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult(); assertNotNull(picker);
            h.call("closeDrawer"); open(h);
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(android.net.Uri.parse("content://project-documents/old"))); drain(h);
            assertTrue(ShadowFiles.uploads.isEmpty()); assertFalse(h.<NativeFileBrowser>get("fileBrowser").uploadBusy);
        }
    }
}
