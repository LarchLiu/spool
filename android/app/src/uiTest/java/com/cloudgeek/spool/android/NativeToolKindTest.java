package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.view.View;
import android.view.ViewGroup;
import android.view.accessibility.AccessibilityNodeInfo;
import java.util.List;
import java.util.ArrayList;
import java.util.Arrays;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeToolKindTest {
    private JSONArray fixtures(String name) throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/" + name + ".json")) {
            assertNotNull(input); return new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    @Test public void compoundRowsMatchServerSummariesAndNeverOfferDetails() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            JSONArray cases = fixtures("tool-operations");
            for (int i = 0; i < cases.length(); i++) {
                JSONObject fixture = cases.getJSONObject(i);
                String id = fixture.getString("id"); JSONArray operations = fixture.getJSONArray("operations");
                JSONObject part = new JSONObject().put("id", "call").put("role", "assistant").put("kind", "tool_use").put("name", fixture.getString("name"))
                        .put("text", fixture.has("input") ? fixture.getJSONObject("input").toString() : fixture.optString("text"));
                JSONObject result = new JSONObject().put("id", "call").put("role", "tool").put("kind", "tool_result").put("text", "combined output");
                String original = part.toString();
                List<NativeActivityModel.Activity> activities = NativeActivityModel.fromParts(ui, Arrays.asList(part, result), "claude");
                if (operations.length() == 0) {
                    assertEquals(id, 1, activities.size()); assertFalse(id, activities.get(0).noDetails); continue;
                }
                JSONObject data = fixture.optJSONObject("input");
                JSONArray parsed = NativeToolKind.operations(NativeToolKind.command(part.optString("text"), data), 0);
                assertTrue(id, NativeChatState.equivalent(operations, parsed));
                assertEquals(id, operations.length(), activities.size());
                JSONObject lazy = new JSONObject(part.toString()).put("text", "").put("tool", new JSONObject().put("kind", "shell").put("operations", operations).put("href", "/chats/chat/tool-detail?ref=call"));
                List<NativeActivityModel.Activity> projected = NativeActivityModel.fromParts(ui, Arrays.asList(lazy, result), "claude");
                for (int j = 0; j < activities.size(); j++) {
                    NativeActivityModel.Activity item = activities.get(j);
                    assertTrue(id, item.noDetails); assertSame(part, item.use); assertSame(result, item.result);
                    assertEquals(id, item.kind, projected.get(j).kind); assertEquals(id, item.label, projected.get(j).label);
                    assertTrue(id, projected.get(j).noDetails);
                }
                NativeActivityList.State state = new NativeActivityList.State();
                NativeActivityList view = new NativeActivityList(ui, activities, "Working", true, state, ignored -> {});
                controller.get().setContentView(view);
                List<NativeDisclosureRow> rows = new ArrayList<>(); collectRows(view, rows);
                assertEquals(id, 2, rows.size());
                assertFalse(id, rows.get(1).isSelected());
                assertTrue(id, rows.get(1).isClickable());
                rows.get(1).performClick();
                rows.clear(); collectRows(view, rows);
                int staticRows = 0;
                for (NativeDisclosureRow row : rows) if (!row.isClickable()) {
                    staticRows++;
                    assertFalse(id, row.isFocusable());
                    AccessibilityNodeInfo info = row.createAccessibilityNodeInfo();
                    assertEquals(id, android.widget.TextView.class.getName(), info.getClassName());
                    assertFalse(id, info.getActionList().contains(AccessibilityNodeInfo.AccessibilityAction.ACTION_EXPAND));
                    assertFalse(id, row.performAccessibilityAction(AccessibilityNodeInfo.ACTION_EXPAND, null));
                    row.performClick();
                }
                assertEquals(id, activities.size(), staticRows);
                assertFalse(id, containsDetail(view));
                assertTrue(id, state.items.isEmpty());
                assertEquals(id, original, part.toString());
            }
        }
    }

    @Test public void derivedRowsKeepOutOfOrderResultsAndStaleExpansionNeverCreatesDetails() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            JSONObject one = new JSONObject("{\"id\":\"one\",\"role\":\"assistant\",\"kind\":\"tool_use\",\"name\":\"Bash\",\"text\":\"cat a; cat b\"}");
            JSONObject two = new JSONObject("{\"id\":\"two\",\"role\":\"assistant\",\"kind\":\"tool_use\",\"name\":\"exec_command\",\"text\":\"rg TODO src | head -20\"}");
            JSONObject resultTwo = new JSONObject("{\"id\":\"two\",\"role\":\"tool\",\"kind\":\"tool_result\",\"text\":\"second output\"}");
            JSONObject resultOne = new JSONObject("{\"id\":\"one\",\"role\":\"tool\",\"kind\":\"tool_result\",\"text\":\"first output\"}");
            List<NativeActivityModel.Activity> items = NativeActivityModel.fromParts(ui, Arrays.asList(one, two, resultTwo, resultOne), "codex");
            assertEquals(3, items.size()); assertSame(resultOne, items.get(0).result); assertSame(resultOne, items.get(1).result); assertSame(resultTwo, items.get(2).result);
            JSONObject single = new JSONObject(one.toString()).put("text", "cat a");
            NativeActivityList.State state = new NativeActivityList.State();
            NativeActivityList list = new NativeActivityList(ui, Arrays.asList(single, resultOne), "codex", "Working", true, state, ignored -> {});
            controller.get().setContentView(list);
            List<NativeDisclosureRow> rows = new ArrayList<>(); collectRows(list, rows);
            rows.get(1).performClick(); assertTrue(containsDetail(list));
            single.put("text", "cat a | head -20");
            list.bindParts(Arrays.asList(single, resultOne), "codex", "Working", true);
            assertFalse(containsDetail(list));
            rows.clear(); collectRows(list, rows); assertTrue(rows.get(1).isClickable()); assertFalse(rows.get(1).isSelected());
            rows.get(1).performClick(); rows.clear(); collectRows(list, rows); assertFalse(rows.get(2).isClickable());
        }
    }

    @Test public void mixedCommandsFoldPerCallAndRetainExpansionAcrossLiveUpdates() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            JSONObject one = new JSONObject().put("id", "one").put("role", "assistant").put("kind", "tool_use").put("name", "Bash")
                    .put("text", "git status --short; cat report.json; rg TODO src | head -20");
            JSONObject two = new JSONObject(one.toString()).put("id", "two");
            List<NativeActivityModel.Group> groups = NativeActivityModel.groups(ui, NativeActivityModel.fromParts(ui, Arrays.asList(one, two), "claude"));
            assertEquals(2, groups.size());
            assertTrue(groups.get(0).compound); assertTrue(groups.get(1).compound);
            assertEquals("Ran command, read files", groups.get(0).label); assertEquals(3, groups.get(0).activities.size());
            JSONObject progress = new JSONObject().put("role", "assistant").put("kind", "text").put("text", "Checking results");
            NativeActivityList.State state = new NativeActivityList.State();
            NativeActivityList list = new NativeActivityList(ui, Arrays.asList(one, progress, two), "claude", "Working", true, state, ignored -> {});
            controller.get().setContentView(list);
            List<NativeDisclosureRow> rows = new ArrayList<>(); collectRows(list, rows); assertEquals(3, rows.size());
            rows.get(1).performClick(); rows.clear(); collectRows(list, rows); assertEquals(6, rows.size());
            list.bindParts(Arrays.asList(one, progress, two, progress), "claude", "Working", true);
            rows.clear(); collectRows(list, rows); assertEquals(6, rows.size()); assertTrue(rows.get(1).isSelected());
            assertFalse(rows.get(rows.size()-1).isSelected()); assertFalse(containsDetail(list));
            rows.get(1).performClick(); rows.clear(); collectRows(list, rows); assertEquals(3, rows.size());
            NativeUi chinese = new NativeUi(controller.get(), false, "zh-CN");
            groups = NativeActivityModel.groups(chinese, NativeActivityModel.fromParts(chinese, Arrays.asList(one), "claude"));
            assertTrue(groups.get(0).label.contains("、"));
        }
    }

    private static void collectRows(View view, List<NativeDisclosureRow> rows) {
        if (view instanceof NativeDisclosureRow) rows.add((NativeDisclosureRow) view);
        else if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) collectRows(((ViewGroup) view).getChildAt(i), rows);
    }
    private static boolean containsDetail(View view) {
        if (view instanceof NativeToolDetail) return true;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) if (containsDetail(((ViewGroup) view).getChildAt(i))) return true;
        return false;
    }

    @Test public void codexAndClaudeRawToolsAndServerSummariesMatchSharedClassificationCases() throws Exception {
        JSONArray fixtures;
        try (java.io.InputStream input = getClass().getResourceAsStream("/tool-classification.json")) {
            assertNotNull(input); fixtures = new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            for (int i = 0; i < fixtures.length(); i++) {
                JSONObject fixture = fixtures.getJSONObject(i);
                String id = fixture.getString("id"), kind = fixture.getString("kind");
                JSONObject part = new JSONObject().put("role", "assistant").put("kind", "tool_use").put("name", fixture.getString("name"))
                        .put("text", fixture.has("input") ? fixture.getJSONObject("input").toString() : fixture.optString("text"));
                NativeActivityModel.Activity activity = NativeActivityModel.display(ui, part, "claude");
                assertEquals(id, kind, activity.kind);
                if (!fixture.has("path") && !fixture.has("query")) {
                    String label = kind.equals("shell") ? "Ran command" : kind.equals("list") ? "Listed files" : kind.equals("read") ? "Read files" : kind.equals("search") ? "Searched files" : kind.equals("web") ? "Searched the web" : "";
                    if (!label.isEmpty()) assertEquals(id, label, activity.label);
                }
                JSONObject summary = new JSONObject().put("kind", kind).put("path", fixture.optString("path")).put("query", fixture.optString("query"));
                if (!kind.equals("image")) {
                    part.put("text", "").put("tool", summary);
                    assertEquals(id + " lazy projection", activity.label, NativeActivityModel.display(ui, part, "claude").label);
                }
            }
        }
    }
}
