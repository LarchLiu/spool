package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.TimeZone;
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
public class NativePeopleTest {
    private String resource(String path) throws Exception { try (java.io.InputStream input = getClass().getResourceAsStream(path)) { return new String(input.readAllBytes(), StandardCharsets.UTF_8); } }
    private JSONObject fixtures() throws Exception { return new JSONObject(resource("/web-behavior.json")); }
    private JSONArray ids(JSONArray values) throws Exception { JSONArray result = new JSONArray(); for (int i = 0; i < values.length(); i++) result.put(values.getJSONObject(i).getString("id")); return result; }
    private void list(JSONArray expected, Iterable<String> actual) { List<String> values = new ArrayList<>(); actual.forEach(values::add); assertEquals(expected.length(), values.size()); for (int i = 0; i < expected.length(); i++) assertEquals(expected.optString(i), values.get(i)); }

    @Test public void inboxDraftAcknowledgementsConversationRetentionUnreadAndDedupMatchActualWeb() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"), online = fixture.getJSONArray("peopleOnline");
        NativePeopleModel model = new NativePeopleModel();
        JSONArray cases = fixture.getJSONArray("peopleCases");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject input = cases.getJSONObject(i), operation = input.getJSONObject("operation"), expected = input.getJSONObject("expected");
            switch (operation.getString("type")) {
                case "groups": model.groups(operation.getJSONArray("groups")); break;
                case "open": model.open(operation.getString("id"), profiles); break;
                case "close": model.close(); break;
                case "draft": model.draft(operation.getString("id"), operation.getString("text")); break;
                case "send": assertNotNull(model.begin(operation.getString("id"), operation.getString("request"))); break;
                case "ack": model.acknowledge(operation.getJSONObject("message")); model.receive(operation.getJSONObject("message"), "alice", true, profiles); break;
                case "receive": model.receive(operation.getJSONObject("message"), "alice", operation.getBoolean("visible"), profiles); break;
                case "reset": model.reset(); break;
                default: break;
            }
            assertEquals(operation.toString(), expected.getString("active"), model.active);
            List<String> messageIDs = new ArrayList<>(); for (JSONObject message : model.messages) messageIDs.add(message.getString("id")); list(expected.getJSONArray("messages"), messageIDs);
            list(expected.getJSONArray("people"), model.remembered.keySet()); list(expected.getJSONArray("conversations"), model.conversations("alice", online)); list(expected.getJSONArray("sending"), model.sending.keySet()); list(expected.getJSONArray("groups"), java.util.stream.IntStream.range(0, model.groups.length()).mapToObj(index -> model.groups.optJSONObject(index).optString("id")).toList());
            JSONObject drafts = expected.getJSONObject("drafts"); assertEquals(drafts.length(), model.drafts.size()); for (java.util.Iterator<String> keys = drafts.keys(); keys.hasNext();) { String id = keys.next(); assertEquals(drafts.getString(id), model.draft(id)); }
            JSONObject unread = expected.getJSONObject("unread"); assertEquals(unread.length(), model.unread.values().stream().filter(n -> n > 0).count()); for (java.util.Iterator<String> keys = unread.keys(); keys.hasNext();) { String id = keys.next(); assertEquals(unread.getInt(id), (int) model.unread.get(id)); }
            JSONObject names = expected.getJSONObject("names"); assertEquals(names.length(), model.groupNames.size()); for (java.util.Iterator<String> keys = names.keys(); keys.hasNext();) { String id = keys.next(); assertEquals(names.getString(id), model.groupNames.get(id)); }
        }
        model.open("bob", profiles); model.draft("bob", "  Keep exact whitespace  \n"); NativePeopleModel.Send send = model.begin("bob", "timeout"); assertEquals("  Keep exact whitespace  \n", send.text);
        model.disconnected(); assertFalse(model.sending.containsKey("bob")); assertEquals(send.text, model.draft("bob")); assertEquals("unknown", model.errors.get("bob"));
        assertNotNull(model.begin("bob", "retry")); assertNull(model.begin("bob", "duplicate"));
        model.reset(); for (int i = 0; i < 302; i++) model.receive(new JSONObject().put("id", "message-" + i).put("from", "bob").put("to", "alice").put("text", "session only"), "alice", false, profiles);
        assertEquals(300, model.messages.size()); assertEquals("message-2", model.messages.get(0).getString("id"));
        assertTrue(NativePeopleModel.tooLong("\ud83e\udd16".repeat(2001))); assertFalse(NativePeopleModel.tooLong("\ud83e\udd16".repeat(2000)));
    }

    @Test public void floatingFitMoveAndAllEightResizeEdgesMatchActualWeb() throws Exception {
        JSONArray cases = fixtures().getJSONArray("floatingCases");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject input = cases.getJSONObject(i), viewport = input.getJSONObject("viewport"), expected = input.getJSONObject("expected"), initial = input.optJSONObject("initial");
            NativeFloatingWindow.Bounds start = initial == null ? null : new NativeFloatingWindow.Bounds(initial.getDouble("x"), initial.getDouble("y"), initial.getDouble("width"), initial.getDouble("height"));
            NativeFloatingWindow.Bounds value = input.getString("operation").equals("fit") ? NativeFloatingWindow.fit(start, viewport.getDouble("width"), viewport.getDouble("height")) : NativeFloatingWindow.transform(start, input.getDouble("dx"), input.getDouble("dy"), viewport.getDouble("width"), viewport.getDouble("height"), input.getString("operation"));
            assertEquals(expected.getDouble("x"), value.x, .000001); assertEquals(expected.getDouble("y"), value.y, .000001); assertEquals(expected.getDouble("width"), value.width, .000001); assertEquals(expected.getDouble("height"), value.height, .000001);
        }
    }

    @Test public void floatingKeyboardHandlesRetainTheWebStepAndViewportLimits() {
        RuntimeEnvironment.setQualifiers("w412dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeFloatingWindow panel = new NativeFloatingWindow(ui, null, value -> {}); controller.get().setContentView(panel); layout(panel, 412, 800);
            double height = panel.bounds.height;
            assertTrue(panel.edges[0].dispatchKeyEvent(new android.view.KeyEvent(android.view.KeyEvent.ACTION_DOWN, android.view.KeyEvent.KEYCODE_DPAD_UP)));
            assertEquals(height + 10, panel.bounds.height, .000001);
            android.view.KeyEvent shifted = new android.view.KeyEvent(0, 0, android.view.KeyEvent.ACTION_DOWN, android.view.KeyEvent.KEYCODE_DPAD_LEFT, 0, android.view.KeyEvent.META_SHIFT_ON);
            double width = panel.bounds.width; assertTrue(panel.edges[1].dispatchKeyEvent(shifted)); assertEquals(width - 40, panel.bounds.width, .000001);
            for (int i = 0; i < 20; i++) panel.edges[1].dispatchKeyEvent(shifted); assertEquals(300, panel.bounds.width, .000001);
        }
    }

    private void layout(View view, int width, int height) { view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY)); view.layout(0, 0, width, height); }
    private void idle() { Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); }
    private JSONObject box(View view, View root) throws Exception { int x = 0, y = 0; for (View current = view; current != root; current = (View) current.getParent()) { x += current.getLeft(); y += current.getTop(); } return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight()); }
    private void snapshot(View view, String name) throws Exception { File target = new File(System.getProperty("spool.ui.artifacts"), name + ".png"); Bitmap bitmap = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888); view.draw(new Canvas(bitmap)); try (java.io.FileOutputStream output = new java.io.FileOutputStream(target)) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); } bitmap.recycle(); }
    private NativeAvatars avatars() throws Exception { JSONArray goldens = new JSONArray(resource("/web-fixtures.json")); return new NativeAvatars(Runnable::run, (style, seed) -> { for (int i = 0; i < goldens.length(); i++) if (goldens.getJSONObject(i).getString("name").equals(style)) return goldens.getJSONObject(i).getString("svg"); throw new java.io.IOException("Unknown fixture style"); }); }
    private JSONObject person(JSONArray profiles, String id) { for (int i = 0; i < profiles.length(); i++) if (profiles.optJSONObject(i).optString("id").equals(id)) return profiles.optJSONObject(i); return new JSONObject(java.util.Map.of("id", id, "nickname", id.equals("online-room") ? "Online group chat" : "Design room")); }

    @Test public void peoplePanelsRenderAtAllMobileWidthsAndThemesAndKeepDraftsWhileSwitching() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"), online = fixture.getJSONArray("peopleOnline");
        TimeZone previous = TimeZone.getDefault(); TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));
        try { for (int width : new int[] {320,360,412}) { RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi"); for (boolean dark : new boolean[] {false,true}) for (String screen : new String[] {"people","people-group"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark); ui.avatars = avatars();
                NativePeopleModel model = new NativePeopleModel(); model.groups(fixture.getJSONArray("peopleGroups")); model.draft("david", "Later message"); model.open(screen.equals("people") ? "bob" : "group-mobile", profiles); model.draft(model.active, "Check the mobile layout");
                for (int i = 0; i < fixture.getJSONArray("peopleMessages").length(); i++) { JSONObject message = new JSONObject(fixture.getJSONArray("peopleMessages").getJSONObject(i).toString()); if (screen.equals("people-group")) message.put("to", model.active).put("group", true); model.receive(message, "alice", true, profiles); }
                model.unread.put("online-room", 12); model.unread.put("carol", 2);
                java.util.concurrent.atomic.AtomicInteger sends = new java.util.concurrent.atomic.AtomicInteger();
                NativePeoplePanel panel = new NativePeoplePanel(ui, model, id -> person(profiles, id), null, value -> {}, id -> model.open(id, profiles), text -> model.draft(model.active, text), sends::incrementAndGet, () -> {}, () -> {}, () -> {});
                controller.get().setContentView(panel); panel.bind("alice", online, true); layout(panel, width, 800); idle(); layout(panel, width, 800);
                assertTrue("The Web dialog focuses its textarea after mounting", panel.editor.isFocused());
                for (int i = 0; i < panel.log.getChildCount(); i++) {
                    NativePeopleMessage message = (NativePeopleMessage) panel.log.getChildAt(i);
                    assertTrue(message.text.getWidth() > 0);
                    for (int line = 0; line < message.text.getLineCount(); line++) assertTrue("Text is not clipped by its bubble: " + message.text.getLayout().getLineMax(line) + " > " + message.text.getWidth(), message.text.getLayout().getLineMax(line) <= message.text.getWidth() + 1);
                }
                String suffix = width + (dark ? "-dark" : "-light"); snapshot(panel.sheet, screen + "-" + suffix);
                JSONArray rows = new JSONArray(), messages = new JSONArray(), edges = new JSONArray(); for (int i = 0; i < panel.conversations.items.getChildCount(); i++) rows.put(box(panel.conversations.items.getChildAt(i), panel.sheet)); for (int i = 0; i < panel.log.getChildCount(); i++) messages.put(box(panel.log.getChildAt(i), panel.sheet));
                for (View edge : panel.edges) edges.put(new JSONObject().put("x", edge.getLeft() - panel.sheet.getLeft()).put("y", edge.getTop() - panel.sheet.getTop()).put("width", edge.getWidth()).put("height", edge.getHeight()));
                JSONObject toolbar = box(panel.toolbar, panel.sheet); toolbar.put("height", toolbar.getInt("height") + 1); JSONObject composer = box(panel.composer, panel.sheet); composer.put("y", composer.getInt("y") - 1).put("height", composer.getInt("height") + 1);
                JSONObject geometry = new JSONObject().put("panel", box(panel.sheet, panel.sheet)).put("sidebar", box(panel.conversations, panel.sheet)).put("toolbar", toolbar).put("history", box(panel.history, panel.sheet)).put("composer", composer).put("editor", box(panel.editor, panel.sheet)).put("send", box(panel.send, panel.sheet)).put("rows", rows).put("messages", messages).put("edges", edges);
                java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-" + screen + "-" + suffix + ".json").toPath(), geometry.toString(2));
                View firstMessage = panel.log.getChildAt(0); panel.bind("alice", online, true); assertSame("Unchanged presence preserves transcript selection", firstMessage, panel.log.getChildAt(0));
                assertTrue(panel.send.isEnabled()); panel.send.performClick(); assertEquals(1, sends.get());
                panel.editor.setText("\ud83e\udd16".repeat(2001)); assertFalse(panel.send.isEnabled()); assertEquals(1, panel.notices.getChildCount());
                panel.editor.setText("Keep this draft"); model.open("carol", profiles); panel.bind("alice", online, true); panel.editor.setText("A separate draft"); model.open(screen.equals("people") ? "bob" : "group-mobile", profiles); panel.bind("alice", online, true); assertEquals("Keep this draft", panel.editor.getText().toString());
                android.view.inputmethod.InputConnection input = panel.editor.onCreateInputConnection(new android.view.inputmethod.EditorInfo()); assertNotNull(input);
                assertTrue(input.setComposingRegion(0, 4)); assertFalse("Composing cannot submit a partial IME word", panel.send.isEnabled());
                assertTrue(input.finishComposingText()); assertTrue("Finishing composition refreshes Send without another edit", panel.send.isEnabled());
                panel.bind("alice", online, false); assertFalse(panel.send.isEnabled()); assertTrue(panel.notices.getChildCount() > 0);
                if (screen.equals("people-group")) {
                    NativeGroupPicker picker = new NativeGroupPicker(ui, null, (name, members, completion) -> {}, group -> {});
                    JSONArray available = new JSONArray().put(profiles.getJSONObject(1)).put(profiles.getJSONObject(2)); picker.selected.add("bob"); picker.selected.add("carol"); picker.bind(available, null); picker.show(); idle();
                    picker.heading.requestFocus(); idle();
                    View checkbox = ((android.widget.LinearLayout) picker.memberRows.getChildAt(0)).getChildAt(0);
                    Bitmap glyph = Bitmap.createBitmap(20, 20, Bitmap.Config.ARGB_8888); checkbox.draw(new Canvas(glyph)); boolean ink = false;
                    for (int y = 0; y < 20; y++) for (int x = 0; x < 20; x++) ink |= glyph.getPixel(x, y) == (dark ? 0xff3b3b3b : 0xffffffff);
                    glyph.recycle(); assertTrue("The browser checkbox uses theme-dependent contrasting check ink", ink);
                    View pickerRoot = picker.modal.surface; snapshot(pickerRoot, "group-picker-" + suffix);
                    JSONArray members = new JSONArray(); for (int i = 0; i < picker.memberRows.getChildCount(); i++) members.put(box(picker.memberRows.getChildAt(i), pickerRoot));
                    JSONObject actionBox = box(picker.actions, pickerRoot); actionBox.put("y", actionBox.getInt("y") - 1).put("height", actionBox.getInt("height") + 1);
                    JSONObject pickerGeometry = new JSONObject().put("panel", box(pickerRoot, pickerRoot)).put("heading", box(picker.heading, pickerRoot)).put("name", box(picker.name, pickerRoot)).put("notice", box(picker.content.getChildAt(2), pickerRoot)).put("members", box((View) picker.memberRows.getParent(), pickerRoot)).put("rows", members).put("actions", actionBox).put("count", box(picker.count, pickerRoot)).put("submit", box(picker.submit, pickerRoot));
                    java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-group-picker-" + suffix + ".json").toPath(), pickerGeometry.toString(2)); picker.dismiss();
                }
                ui.avatars.clear();
            }
        }} } finally { TimeZone.setDefault(previous); }
    }

    @Test public void groupPickerPrunesDepartedMembersAndIgnoresClosedAsyncResults() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"); RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        List<NativeGroupPicker.Completion> pending = new ArrayList<>(); java.util.concurrent.atomic.AtomicInteger completed = new java.util.concurrent.atomic.AtomicInteger();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeGroupPicker picker = new NativeGroupPicker(ui, null, (name, members, completion) -> pending.add(completion), group -> completed.incrementAndGet()); picker.bind(new JSONArray().put(profiles.getJSONObject(1)).put(profiles.getJSONObject(2)), null); picker.show(); idle();
            picker.selected.add("bob"); picker.bind(new JSONArray().put(profiles.getJSONObject(1)).put(profiles.getJSONObject(2)), null); picker.submit.performClick(); assertFalse(picker.submit.isEnabled()); assertFalse(picker.name.isEnabled());
            pending.remove(0).done(null, "offline"); assertEquals(View.VISIBLE, picker.error.getVisibility()); assertTrue(picker.submit.isEnabled());
            picker.bind(new JSONArray().put(profiles.getJSONObject(2)), null); assertTrue(picker.selected.isEmpty()); assertFalse(picker.submit.isEnabled()); picker.selected.add("carol"); picker.bind(new JSONArray().put(profiles.getJSONObject(2)), null); picker.submit.performClick(); picker.dismiss(); pending.remove(0).done(fixture.getJSONArray("peopleGroups").getJSONObject(0), ""); assertEquals(0, completed.get());
        }
    }

    @Test public void onlineMenuMatchesMobileActionsUpdatesPresenceAndDismissesBeforeNavigation() throws Exception {
        JSONObject fixture = fixtures(); JSONArray profiles = fixture.getJSONArray("peopleProfiles"), online = fixture.getJSONArray("peopleOnline");
        for (int width : new int[] {320,360,412}) { RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi"); for (boolean dark : new boolean[] {false,true}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark); ui.avatars = avatars(); NativePeopleModel model = new NativePeopleModel(); model.groups(fixture.getJSONArray("peopleGroups")); model.unread.put("online-room", 12); model.unread.put("carol", 2);
                List<String> actions = new ArrayList<>();
                NativePeopleMenu menu = new NativePeopleMenu(ui, () -> actions.add("inbox"), actions::add, () -> actions.add("new"));
                android.widget.FrameLayout host = new android.widget.FrameLayout(controller.get()); NativeUi.Header header = ui.new Header("New chat", "Mobile project", () -> {}, () -> {}, () -> {});
                header.bindPeople(List.of(profiles.getJSONObject(0), profiles.getJSONObject(1), profiles.getJSONObject(2)), 14); host.addView(header, new android.widget.FrameLayout.LayoutParams(-1, 50)); controller.get().setContentView(host); layout(host, width, 800); idle();
                menu.bind("alice", online, model, id -> person(profiles, id)); menu.show(header.online); idle(); View root = menu.popover.panel();
                assertEquals("The Web menu shrink-wraps to its 210px minimum", 210, root.getWidth());
                for (View option : menu.options) {
                    android.widget.LinearLayout button = (android.widget.LinearLayout) option;
                    for (int i = 0; i < button.getChildCount(); i++) if (button.getChildAt(i) instanceof android.widget.TextView) {
                        android.widget.TextView text = (android.widget.TextView) button.getChildAt(i);
                        if (text.getText().toString().matches("[0-9]+\\+?")) { assertTrue("Fractional text sizing retains badge min-width", text.getWidth() >= 17); assertEquals(17, text.getHeight()); }
                    }
                }
                String suffix = width + (dark ? "-dark" : "-light"); snapshot(root, "online-menu-" + suffix);
                JSONArray rows = new JSONArray(), options = new JSONArray(); for (View row : menu.rows) rows.put(box(row, root)); for (View option : menu.options) options.put(box(option, root));
                JSONObject geometry = new JSONObject().put("panel", box(root, root)).put("rows", rows).put("options", options);
                java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-online-menu-" + suffix + ".json").toPath(), geometry.toString(2));
                assertEquals(List.of("inbox", "online-room", "new", "group-mobile", "bob", "carol"), menu.options.stream().map(view -> view.getTag().toString()).toList());
                menu.options.get(4).performClick(); assertFalse(menu.isShowing()); assertEquals(List.of("bob"), actions);
                menu.bind("alice", new JSONArray().put(online.getJSONObject(0)), model, id -> person(profiles, id)); menu.show(header.online); idle(); assertEquals(4, menu.options.size());
                menu.options.get(2).performClick(); assertEquals(List.of("bob", "new"), actions); assertFalse(menu.isShowing()); ui.avatars.clear();
            }
        }}
    }
}
