package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityToolDetailsTest {
    @Test public void nativeHistoryDetailUsesTheAuthenticatedApiAndDecodesItsBody() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            String locator = "native-tool-" + java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(
                    "{\"chat\":\"chat-one\",\"turn\":\"native-turn\",\"item\":\"tool-call\",\"kind\":\"tool_use\"}".getBytes(java.nio.charset.StandardCharsets.UTF_8));
            JSONObject summary = new JSONObject().put("href", "/chats/chat-one/tool-detail?ref=" + locator).put("ref", locator).put("version", "v1").put("kind", "shell");
            JSONObject page = new JSONObject().put("ref", locator).put("text", "pwd").put("offset", 0).put("next", 3).put("total", 3).put("prefix", "hash");
            MainActivityGoalTest.ShadowClient.response = new JSONObject().put("status", 200).put("body", page.toString()).toString();
            List<JSONObject> results = new ArrayList<>();
            h.<NativeUi>get("ui").toolDetails.load(summary, 0, "", new NativeToolDetails.Callback() {
                public void received(JSONObject value) { results.add(value); }
                public void failed(Exception error) { throw new AssertionError(error); }
            });
            h.io.run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            assertEquals(List.of("GET", "/api" + summary.getString("href") + "&offset=0&prefix=", "", "test-csrf", "en"), MainActivityGoalTest.ShadowClient.calls.get(0));
            assertEquals("pwd", results.get(0).getString("text"));
        }
    }
}
