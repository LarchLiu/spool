package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Rect;
import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Render the production Activity's MCP approvals; shadow only the Go transport. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityApprovalTest {
    private JSONObject request() throws Exception {
        return new JSONObject().put("id", "approval-one").put("title", "Computer Use")
                .put("reason", "允许 Computer Use 使用“计算器”吗？").put("detail", "应用：计算器");
    }

    private void show(MainActivityGoalTest.Harness h, JSONObject approval, boolean dark) throws Exception {
        long now = System.currentTimeMillis();
        JSONObject turn = new JSONObject().put("id", "turn-one").put("user_id", "alice").put("backend", "codex")
                .put("status", "running").put("started_at", now - 5000).put("part_start", 0).put("input_end", 1).put("part_end", 1)
                .put("pending_approval", approval);
        JSONObject chat = h.chat("chat-one").put("title", "Computer Use 授权").put("project_id", "project-one").put("status", "running")
                .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", new JSONArray()
                        .put(new JSONObject().put("role", "user").put("kind", "text").put("text", "请用计算器检查一下结果。"))));
        h.set("language", "zh-CN"); h.set("theme", dark ? "dark" : "light");
        h.set("connected", true); h.set("workspaceReady", true); h.set("chat", chat);
        h.call("render");
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        View root = h.get("root");
        for (int i = 0; i < 2; i++) {
            root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            root.layout(0, 0, 360, 800);
            root.getViewTreeObserver().dispatchOnPreDraw();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
        }
    }

    private TextView text(View view, String value) {
        if (view instanceof TextView && value.contentEquals(((TextView) view).getText())) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = text(((ViewGroup) view).getChildAt(i), value);
            if (found != null) return found;
        }
        return null;
    }

    private void screenshot(View root, String theme) throws Exception {
        screenshot(root, theme, null);
    }

    private void screenshot(View root, String theme, NativePopover popup) throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"), "mcp-approval");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(image);
        root.draw(canvas);
        if (popup != null) {
            NativePopupFrame frame = (NativePopupFrame) popup.panel().getParent();
            int[] origin = new int[2]; root.getLocationOnScreen(origin);
            canvas.save();
            canvas.translate(frame.window.left - origin[0], frame.window.top - origin[1]);
            frame.draw(canvas);
            canvas.restore();
        }
        try (FileOutputStream output = new FileOutputStream(new File(directory, "android-" + theme + ".png"))) {
            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    @Test public void appApprovalRendersAndBothDecisionsUseTheCurrentRequest() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                show(h, request(), dark);
                View root = h.get("root");
                Button deny = (Button) text(root, "拒绝"), allow = (Button) text(root, "仅允许一次");
                assertNotNull(deny); assertNotNull(allow); assertTrue(deny.isEnabled()); assertTrue(allow.isEnabled());
                assertSame("Approval decisions share one action row", deny.getParent(), allow.getParent());
                assertEquals(deny.getTop() + deny.getHeight() / 2f, allow.getTop() + allow.getHeight() / 2f, .5f);
                assertEquals(12, allow.getLeft() - deny.getRight());
                assertTrue("Deny retains its filled background", deny.getBackground() != null);
                assertNotNull(text(root, "允许 Computer Use 使用“计算器”吗？"));
                screenshot(root, dark ? "dark" : "light");
                assertTrue(deny.performClick());
                assertFalse(deny.isEnabled()); assertFalse(allow.isEnabled());
                h.io.run();
                JSONObject body = new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2));
                assertEquals("approval-one", body.getString("id")); assertEquals("turn-one", body.getString("turn_id"));
                assertFalse(body.getBoolean("allow"));
                assertEquals("/api/chats/chat-one/approvals", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            }
        }
    }

    @Test public void formValuesReachTheNativeApprovalAPI() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject approval = request().put("fields", new JSONArray()
                    .put(new JSONObject().put("name", "confirm").put("label", "Confirm").put("type", "boolean").put("required", true))
                    .put(new JSONObject().put("name", "count").put("label", "Count").put("type", "integer").put("required", true).put("default", 2)));
            show(h, approval, false);
            assertTrue(text(h.get("root"), "仅允许一次").performClick());
            h.io.run();
            JSONObject body = new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2));
            assertTrue(body.getBoolean("allow"));
            assertFalse(body.getJSONObject("content").getBoolean("confirm"));
            assertEquals(2, body.getJSONObject("content").getInt("count"));
        }
    }

    @Test public void dynamicSelectionOnlySubmitsWhenConfirmIsPressed() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                JSONObject approval = request().put("decisions", new JSONArray()
                        .put(NativeApprovalActions.choice("deny", "Deny", "deny"))
                        .put(NativeApprovalActions.choice("allow", "Allow once", "approve"))
                        .put(NativeApprovalActions.choice("session", "Allow for this session", "approve")));
                show(h, approval, dark);
                View root = h.get("root");
                NativeUi ui = h.get("ui");
                View dropdown = description(root, "选择审批选项");
                assertNotNull(dropdown); assertNotNull(text(root, "拒绝"));
                assertTrue(dropdown.performClick());
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertNotNull(ui.activePopover);
                assertTrue(ui.activePopover.isShowing());
                assertEquals("仅允许一次", ui.activePopover.content.getChildAt(0).getContentDescription());
                assertEquals("本会话内允许", ui.activePopover.content.getChildAt(1).getContentDescription());
                assertTrue(ui.activePopover.content.getChildAt(1).performClick());
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertTrue("Choosing a menu option sends no API request", MainActivityGoalTest.ShadowClient.calls.isEmpty());
                View confirm = text(root, "本会话内允许"); assertNotNull(confirm);
                assertNotNull(text(root, "拒绝"));
                root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
                screenshot(root, dark ? "split-dark" : "split-light");
                assertTrue(confirm.performClick()); h.io.run();
                JSONObject body = new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2));
                assertEquals("session", body.getString("decision")); assertTrue(body.getBoolean("allow"));
            }
        }
    }

    @Test public void computerUseDropdownShowsClickableChoicesInsideTheViewport() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                JSONObject approval = request().put("decisions", new JSONArray()
                        .put(NativeApprovalActions.choice("deny", "Deny", "deny"))
                        .put(NativeApprovalActions.choice("cancel", "Cancel request", "cancel"))
                        .put(NativeApprovalActions.choice("allow", "Allow once", "approve")));
                show(h, approval, dark);
                View root = h.get("root");
                assertTrue(description(root, "选择审批选项").performClick());
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                NativeUi ui = h.get("ui");
                NativePopover popup = ui.activePopover;
                assertNotNull(popup); assertTrue(popup.isShowing());
                assertEquals(2, popup.content.getChildCount());
                assertEquals("取消请求", popup.content.getChildAt(0).getContentDescription());
                assertEquals("仅允许一次", popup.content.getChildAt(1).getContentDescription());
                NativePopupFrame frame = (NativePopupFrame) popup.panel().getParent();
                assertEquals("Approval menu uses the same readable width as Web", ui.dp(300), frame.panel.width());
                Rect bounds = new Rect(frame.panel); bounds.offset(frame.window.left, frame.window.top);
                assertTrue(new Rect(0, 0, 360, 800).contains(bounds));
                View row = popup.content.getChildAt(0);
                Rect visible = new Rect(); assertTrue(row.getGlobalVisibleRect(visible));
                assertEquals(row.getWidth(), visible.width()); assertEquals(row.getHeight(), visible.height());
                screenshot(root, dark ? "dropdown-dark" : "dropdown-light", popup);
                assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());

                // Dispatch a touch through the PopupWindow rather than invoking
                // an off-screen row's click callback directly.
                View decor = frame.getRootView();
                int[] origin = new int[2], position = new int[2];
                decor.getLocationOnScreen(origin); row.getLocationOnScreen(position);
                float x = position[0] - origin[0] + row.getWidth() / 2f;
                float y = position[1] - origin[1] + row.getHeight() / 2f;
                long now = SystemClock.uptimeMillis();
                for (int action : new int[] {MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP}) {
                    MotionEvent event = MotionEvent.obtain(now, now + action * 16, action, x, y, 0);
                    try { assertTrue("Popup touch action " + action, decor.dispatchTouchEvent(event)); } finally { event.recycle(); }
                }
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertFalse(popup.isShowing());
                assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
                assertNotNull(text(root, "拒绝"));
                assertTrue(text(root, "取消请求").performClick()); h.io.run();
                JSONObject body = new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2));
                assertEquals("cancel", body.getString("decision")); assertFalse(body.getBoolean("allow"));
            }
        }
    }

    @Test public void oneConfirmOptionDoesNotRenderDropdownAndQuestionsPreserveTypes() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject approval = request().put("kind", "input").put("decisions", new JSONArray()
                    .put(NativeApprovalActions.choice("deny", "Cancel request", "cancel"))
                    .put(NativeApprovalActions.choice("allow", "Send answers", "submit")))
                    .put("fields", new JSONArray().put(new JSONObject().put("name", "token").put("label", "Token").put("type", "string").put("secret", true).put("default", "secret"))
                    .put(new JSONObject().put("name", "features").put("label", "Features").put("type", "array").put("options", new JSONArray().put(new JSONObject().put("value", "A").put("label", "A"))).put("default", new JSONArray().put("A"))));
            show(h, approval, false);
            View root = h.get("root");
            assertNull(description(root, "选择审批选项"));
            assertTrue(text(root, "提交回答").performClick()); h.io.run();
            JSONObject body = new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2));
            assertEquals("secret", body.getJSONObject("content").getString("token"));
            assertEquals("A", body.getJSONObject("content").getJSONArray("features").getString(0));
        }
    }

    private View description(View view, String value) {
        if (value.equals(view.getContentDescription() == null ? null : view.getContentDescription().toString())) return view;
        if (view instanceof ViewGroup) for (int i=0; i<((ViewGroup)view).getChildCount(); i++) {
            View found=description(((ViewGroup)view).getChildAt(i), value); if (found!=null) return found;
        }
        return null;
    }

    @Test public void approvalAuditRecordsRenderBelowTheReplyAndUpdateWithTheSameTurn() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                h.set("user", new JSONObject().put("id", "alice").put("nickname", "Alex"));
                show(h, request(), dark);
                JSONObject chat = h.get("chat"), turn = chat.getJSONArray("turns").getJSONObject(0);
                turn.remove("pending_approval");
                turn.put("actions", new JSONArray().put(new JSONObject().put("id", "approve-one").put("user_id", "alice").put("kind", "approve")
                        .put("summary", "Computer Use · 允许使用 Magpie").put("created_at", System.currentTimeMillis())));
                h.call("renderChat");
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                View root = h.get("root");
                assertNotNull("Live actions bind even when only audit metadata changes", auditText(root, "Alex 批准了操作 · Computer Use · 允许使用 Magpie"));
                turn.put("status", "completed").put("finished_at", System.currentTimeMillis()).put("part_end", 2);
                chat.put("status", "completed");
                chat.getJSONObject("transcript").getJSONArray("parts").put(new JSONObject().put("role", "assistant").put("kind", "text")
                        .put("phase", "final_answer").put("text", "已打开 Magpie，并检查了供应商设置。"));
                turn.getJSONArray("actions").put(new JSONObject().put("id", "deny-one").put("user_id", "alice").put("kind", "deny")
                        .put("summary", "命令执行 · git push origin dev").put("created_at", System.currentTimeMillis()));
                h.call("renderChat");
                root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
                TextView approved = auditText(root, "Alex 批准了操作 · Computer Use · 允许使用 Magpie");
                assertNotNull(approved);
                assertTrue("Decision time follows the summary", approved.getText().toString().matches(".*Magpie  [0-9]{2}:[0-9]{2}"));
                assertNotNull(auditText(root, "Alex 拒绝了操作 · 命令执行 · git push origin dev"));
                screenshot(root, dark ? "audit-dark" : "audit-light");
                NativeMessage reply = reply(root);
                assertNotNull(reply);
                assertTrue(reply.indexOfChild(reply.bubble) < reply.indexOfChild(reply.footer));
                assertTrue(reply.getChildAt(reply.indexOfChild(reply.footer) - 1) instanceof NativeTurnActions);
            }
        }
    }

    private NativeMessage reply(View view) {
        if (view instanceof NativeMessage && text(view, "已打开 Magpie，并检查了供应商设置。") != null) return (NativeMessage) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            NativeMessage found = reply(((ViewGroup) view).getChildAt(i)); if (found != null) return found;
        }
        return null;
    }

    private TextView auditText(View view, String prefix) {
        if (view instanceof TextView && ((TextView) view).getText().toString().startsWith(prefix)) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = auditText(((ViewGroup) view).getChildAt(i), prefix); if (found != null) return found;
        }
        return null;
    }
}
