package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityPermissionsTest {
    private static JSONObject catalog(String selected, boolean autoAvailable) throws Exception {
        return new JSONObject().put("default", selected).put("current", selected).put("modes", new JSONArray()
                .put(new JSONObject().put("id", "ask").put("available", true))
                .put(new JSONObject().put("id", "auto").put("available", autoAvailable)));
    }

    private static String response(int status, JSONObject body) throws Exception {
        return new JSONObject().put("status", status).put("body", body.toString()).toString();
    }

    @Test public void menuChoiceSavesProjectPreferenceBeforeAnyMessage() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("permissionCatalog", catalog("ask", true));
            h.call("choosePermission");
            NativePermissionMenu menu = h.get("permissionMenu");
            assertTrue(menu.findViewWithTag("option:auto").performClick());
            assertEquals("ask", h.<String>get("permission"));
            assertTrue(h.<Boolean>get("permissionLoading"));
            assertFalse(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));
            MainActivityGoalTest.ShadowClient.response = response(200, catalog("auto", true));
            h.io.run();
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("POST", call.get(0));
            assertEquals("/api/permissions?project_id=project-one&backend=codex&chat_id=chat-one", call.get(1));
            assertEquals("auto", new JSONObject(call.get(2)).getString("mode"));
            assertEquals("test-csrf", call.get(3));
            assertEquals("auto", h.<String>get("permission"));
            assertFalse(h.<Boolean>get("permissionLoading"));
            assertTrue(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());

            // Existing turn metadata must not override the project's latest choice.
            h.set("permission", "ask");
            h.call("loadPermissions"); h.io.run();
            assertEquals("auto", h.<String>get("permission"));
            // The same read works for a new chat without a chat_id.
            h.set("chatID", ""); h.set("chat", null); h.set("permission", "");
            h.call("loadPermissions"); h.io.run();
            assertEquals("auto", h.<String>get("permission"));
            assertEquals("/api/permissions?project_id=project-one&backend=codex", MainActivityGoalTest.ShadowClient.calls.get(2).get(1));
        }
    }

    @Test public void rejectedSaveKeepsPreviousChoiceAndUnavailablePreferenceBlocksSending() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("permissionCatalog", catalog("ask", true));
            h.call("choosePermission");
            NativePermissionMenu menu = h.get("permissionMenu");
            menu.findViewWithTag("option:auto").performClick();
            MainActivityGoalTest.ShadowClient.response = response(500, new JSONObject().put("error", "Could not save preferences"));
            h.io.run();
            assertEquals("ask", h.<String>get("permission"));
            assertFalse(h.<Boolean>get("permissionLoading"));
            assertTrue(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));

            MainActivityGoalTest.ShadowClient.response = response(200, catalog("auto", false));
            h.call("loadPermissions"); h.io.run();
            assertEquals("auto", h.<String>get("permission"));
            assertFalse(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));
            h.composer().input.setText("Do not send unavailable mode");
            assertFalse(h.composer().send.isEnabled());
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void navigatingDuringSaveRefreshesTheNextChatAfterCommit() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("permissionCatalog", catalog("ask", true));
            h.call("choosePermission");
            NativePermissionMenu menu = h.get("permissionMenu");
            menu.findViewWithTag("option:auto").performClick();
            Runnable saving = h.io.jobs.remove(0);
            h.set("chatID", ""); h.set("chat", null); h.set("permission", "");
            h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            h.call("invalidatePermissions");
            h.call("loadPermissions");
            MainActivityGoalTest.ShadowClient.response = response(200, catalog("ask", true));
            h.io.run();
            assertEquals("ask", h.<String>get("permission"));
            assertFalse(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));
            MainActivityGoalTest.ShadowClient.response = response(200, catalog("auto", true));
            saving.run();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            h.io.run();
            assertEquals("auto", h.<String>get("permission"));
            assertTrue(ReflectionHelpers.<Boolean>callInstanceMethod(h.activity, "permissionReady"));
        }
    }
}
