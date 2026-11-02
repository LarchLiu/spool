package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSidebarTest {
    private String resource(String name) throws Exception { try (java.io.InputStream input = getClass().getResourceAsStream(name)) { return new String(input.readAllBytes(), StandardCharsets.UTF_8); } }
    private JSONObject fixtures() throws Exception { return new JSONObject(resource("/web-behavior.json")); }
    private void layout(View view, int width, int height) { view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY)); view.layout(0, 0, width, height); }
    private void idle() { Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); }
    private JSONObject person(JSONArray people, String id) { for (int i = 0; i < people.length(); i++) if (people.optJSONObject(i).optString("id").equals(id)) return people.optJSONObject(i); return new JSONObject(java.util.Map.of("id", id, "nickname", id)); }
    private JSONObject box(View view, View root) throws Exception { int x = 0, y = 0; for (View current = view; current != root; current = (View) current.getParent()) { x += current.getLeft(); y += current.getTop(); } return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight()); }
    private JSONArray boxes(List<? extends View> views, View root) throws Exception { JSONArray boxes = new JSONArray(); for (View view : views) boxes.put(box(view, root)); return boxes; }
    private void save(JSONObject value, String name) throws Exception { java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-" + name + ".json").toPath(), value.toString(2)); }
    private void snapshot(View view, String name) throws Exception { Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888); view.draw(new Canvas(image)); try (java.io.FileOutputStream output = new java.io.FileOutputStream(new File(System.getProperty("spool.ui.artifacts"), name + ".png"))) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); } image.recycle(); }
    private NativeAvatars avatars() throws Exception { JSONArray goldens = new JSONArray(resource("/web-fixtures.json")); return new NativeAvatars(Runnable::run, (style, seed) -> { for (int i = 0; i < goldens.length(); i++) if (goldens.optJSONObject(i).optString("name").equals(style)) return goldens.optJSONObject(i).optString("svg"); throw new java.io.IOException("Unknown fixture"); }); }

    @Test public void orderingPaginationAndSelectionRevealMatchActualWebFunctions() throws Exception {
        JSONObject fixture = fixtures(); JSONArray chats = fixture.getJSONArray("sidebarChats"), projects = fixture.getJSONArray("sidebarProjects");
        JSONArray sorts = fixture.getJSONArray("sidebarSorts");
        for (int i = 0; i < sorts.length(); i++) {
            JSONObject input = sorts.getJSONObject(i); List<JSONObject> ordered = NativeSidebarState.sorted(chats, input.isNull("project") ? null : input.getString("project")); JSONArray expected = input.getJSONArray("expected"); assertEquals(expected.length(), ordered.size()); for (int j = 0; j < expected.length(); j++) assertEquals(expected.getString(j), ordered.get(j).getString("id"));
        }
        JSONArray tied = new JSONArray("[{\"id\":\"first\",\"updated_at\":10},{\"id\":\"older\",\"updated_at\":9},{\"id\":\"second\",\"updated_at\":10}]");
        List<JSONObject> stable = NativeSidebarState.sorted(tied, null);
        assertEquals("first", stable.get(0).getString("id")); assertEquals("second", stable.get(1).getString("id")); assertEquals("older", stable.get(2).getString("id")); assertEquals("older", tied.getJSONObject(1).getString("id"));
        JSONArray limits = fixture.getJSONArray("sidebarLimits"); for (int i = 0; i < limits.length(); i++) { JSONObject input = limits.getJSONObject(i); assertEquals(input.getInt("expected"), NativeSidebarState.nextLimit(input.getInt("total"), input.getInt("limit"))); }
        JSONArray reveals = fixture.getJSONArray("sidebarReveals"); for (int i = 0; i < reveals.length(); i++) { JSONObject input = reveals.getJSONObject(i); NativeSidebarState state = new NativeSidebarState(); state.projectLimit = input.getInt("projectLimit"); state.chatLimits.put(input.getString("project"), input.getInt("chatLimit")); state.select(input.getString("project"), input.getString("chat"), projects, chats); assertEquals(input.getInt("expectedProject"), state.projectLimit); assertEquals(input.getInt("expectedChat"), state.limit(input.getString("project"))); }
        NativeSidebarState state = new NativeSidebarState(); state.select("project-0", "chat-0", projects, chats); state.toggle("project-0"); state.select("project-0", "chat-0", projects, chats); assertFalse("Metadata refresh must not reopen a manually collapsed project", state.expanded.contains("project-0"));
        state.moreChats(null, 18); state.moreChats("project-0", 16); assertEquals(15, state.recentLimit); assertEquals(15, state.limit("project-0")); state.select("project-6", "missing", projects, chats); assertEquals(7, state.projectLimit); state.reset(); assertTrue(state.expanded.isEmpty()); assertEquals(5, state.recentLimit); assertEquals(5, state.projectLimit);
    }

    @Test public void mobileDrawersAndOperatorTooltipsRenderAtAllWidthsAndThemes() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"), projects = fixture.getJSONArray("sidebarProjects"), chats = fixture.getJSONArray("sidebarChats");
        for (int width : new int[] {320,360,412}) { RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi"); for (boolean dark : new boolean[] {false,true}) for (boolean empty : new boolean[] {false,true}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark); ui.avatars = avatars(); NativeSidebarState state = new NativeSidebarState();
                NativeSidebar sidebar = new NativeSidebar(ui, state, id -> person(profiles, id), () -> {}, id -> {}, id -> {}, () -> {}, () -> {}, () -> {});
                NativeDrawer overlay = new NativeDrawer(ui, false, () -> {}); overlay.sheet.addView(sidebar, new LinearLayout.LayoutParams(-1, -1)); controller.get().setContentView(overlay); overlay.show(); Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(250));
                sidebar.bind(profiles.getJSONObject(0), empty ? new JSONArray() : projects, empty ? new JSONArray() : chats, empty ? "" : "project-0", empty ? "" : "chat-0", "codex", false);
                layout(overlay, width, 800); idle(); layout(overlay, width, 800);
                String name = (empty ? "sidebar-empty-" : "sidebar-") + width + (dark ? "-dark" : "-light"); snapshot(sidebar, name);
                List<View> footer = new ArrayList<>(), operators = new ArrayList<>(), open = new ArrayList<>(), titles = new ArrayList<>(), agents = new ArrayList<>(), locals = new ArrayList<>(), spinners = new ArrayList<>(), projectToggle = new ArrayList<>(), projectNew = new ArrayList<>();
                for (int i = 0; i < sidebar.footer.getChildCount(); i++) footer.add(sidebar.footer.getChildAt(i));
                for (NativeChatLink row : sidebar.chatRows) { if (row.operators != null) operators.add(row.operators); open.add(row.open); titles.add(row.title); if (row.agent != null) agents.add(row.agent); if (row.local != null) locals.add(row.local); if (row.spinner != null) spinners.add(row.spinner); }
                for (NativeProjectRow row : sidebar.projectRows) { projectToggle.add(row.toggle); projectNew.add(row.create); }
                JSONObject brand = box(sidebar.brand, sidebar); brand.put("height", brand.getInt("height") + 1); JSONObject foot = box(sidebar.footer, sidebar); foot.put("y", foot.getInt("y") - 1).put("height", foot.getInt("height") + 1);
                save(new JSONObject().put("panel", box(sidebar, sidebar)).put("brand", brand).put("scroll", box(sidebar.scroll, sidebar)).put("footer", foot).put("headings", boxes(sidebar.headings, sidebar)).put("projects", boxes(sidebar.projectRows, sidebar)).put("chats", boxes(sidebar.chatRows, sidebar)).put("more", boxes(sidebar.moreButtons, sidebar)).put("footerItems", boxes(footer, sidebar)).put("operators", boxes(operators, sidebar)).put("open", boxes(open, sidebar)).put("titles", boxes(titles, sidebar)).put("agents", boxes(agents, sidebar)).put("locals", boxes(locals, sidebar)).put("spinners", boxes(spinners, sidebar)).put("projectToggle", boxes(projectToggle, sidebar)).put("projectNew", boxes(projectNew, sidebar)), name);
                assertEquals(64, sidebar.footer.getHeight() + 1); assertEquals(empty ? 0 : 5, sidebar.projectRows.size()); assertEquals(empty ? 0 : 10, sidebar.chatRows.size());
                if (!empty) {
                    View first = sidebar.chatRows.get(0); sidebar.bind(profiles.getJSONObject(0), projects, chats, "project-0", "chat-0", "codex", false); assertSame("Identical metadata keeps focus/tooltip anchors stable", first, sidebar.chatRows.get(0));
                    NativeChatOperators tooltip = sidebar.chatRows.get(0).operators; tooltip.performClick(); idle(); Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(300)); assertTrue("Touch-opened member tooltip stays open", tooltip.popup.isShowing()); View panel = tooltip.popup.panel();
                    String tooltipName = "sidebar-operators-" + width + (dark ? "-dark" : "-light"); snapshot(panel, tooltipName); List<View> members = new ArrayList<>(); for (int i = 0; i < tooltip.popup.content.getChildCount(); i++) members.add(tooltip.popup.content.getChildAt(i)); save(new JSONObject().put("panel", box(panel, panel)).put("rows", boxes(members, panel)), tooltipName);
                    assertEquals(4, members.size()); assertTrue(tooltip.getContentDescription().toString().contains("Mobile user, Mina, Lin, Kai")); tooltip.popup.dismiss();
                }
                sidebar.deactivate(); ui.avatars.clear();
            }
        }}
    }

    @Test @Config(sdk = 26) @GraphicsMode(GraphicsMode.Mode.LEGACY)
    public void minimumSdkSidebarBuildsAndHandlesNativeNavigation() throws Exception {
        liveNavigationPaginationPermissionAndIndependentRowActionsStayNative();
    }

    @Test public void liveNavigationPaginationPermissionAndIndependentRowActionsStayNative() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"), projects = fixture.getJSONArray("sidebarProjects"), chats = fixture.getJSONArray("sidebarChats"); RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); NativeSidebarState state = new NativeSidebarState(); List<String> actions = new ArrayList<>();
            NativeSidebar sidebar = new NativeSidebar(ui, state, id -> person(profiles, id), () -> actions.add("close"), id -> actions.add("chat:" + id), id -> actions.add("new:" + id), () -> actions.add("avatar"), () -> actions.add("settings"), () -> actions.add("logout")); controller.get().setContentView(sidebar); sidebar.bind(profiles.getJSONObject(0), projects, chats, "project-0", "chat-0", "claude", false); layout(sidebar, 298, 800); idle();
            sidebar.findViewWithTag("more:project-0").performClick(); assertEquals(15, state.limit("project-0")); assertEquals(5, state.recentLimit); sidebar.findViewWithTag("more:recents").performClick(); assertEquals(15, state.recentLimit); sidebar.findViewWithTag("more:project-0").performClick(); assertEquals(16, state.limit("project-0")); sidebar.findViewWithTag("more:project-0").performClick(); assertEquals(5, state.limit("project-0"));
            sidebar.projectRows.get(0).toggle.performClick(); assertFalse(state.expanded.contains("project-0")); sidebar.bind(profiles.getJSONObject(0), projects, chats, "project-0", "chat-0", "claude", false); assertFalse(state.expanded.contains("project-0")); sidebar.projectRows.get(0).toggle.performClick();
            assertFalse(sidebar.projectRows.get(1).create.isEnabled()); sidebar.projectRows.get(0).create.performClick(); assertEquals(List.of("new:project-0"), actions);
            sidebar.chatRows.get(0).operators.performClick(); assertEquals(1, actions.size()); sidebar.chatRows.get(0).open.performClick(); assertEquals(List.of("new:project-0", "chat:chat-0"), actions);
            sidebar.bind(profiles.getJSONObject(0), projects, chats, "project-0", "chat-0", "claude", true); for (NativeProjectRow row : sidebar.projectRows) assertFalse(row.create.isEnabled());
            sidebar.footer.getChildAt(0).performClick(); sidebar.footer.getChildAt(2).performClick(); sidebar.footer.getChildAt(3).performClick(); assertEquals(List.of("new:project-0", "chat:chat-0", "avatar", "settings", "logout"), actions);
            layout(sidebar, 298, 800); idle(); sidebar.scroll.scrollTo(0, 100); idle(); assertEquals(100, state.scrollY);
            JSONArray updated = new JSONArray(chats.toString()); updated.getJSONObject(0).put("title", "Updated metadata"); sidebar.bind(profiles.getJSONObject(0), projects, updated, "project-0", "chat-0", "claude", true); layout(sidebar, 298, 800); idle(); assertEquals("Metadata refresh preserves scroll position", 100, sidebar.scroll.getScrollY());
            sidebar.deactivate(); int before = actions.size(); sidebar.footer.getChildAt(0).performClick(); sidebar.chatRows.get(0).open.performClick(); sidebar.chatRows.get(0).operators.performClick(); assertEquals(before, actions.size()); assertFalse(sidebar.chatRows.get(0).operators.popup.isShowing());
        }
    }
}
