package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
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
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 34, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeTurnActionsTest {
    private JSONObject action(String id, String user, String kind, String summary, long timestamp) throws Exception {
        return new JSONObject().put("id", id).put("user_id", user).put("kind", kind)
                .put("summary", summary).put("created_at", timestamp);
    }

    private void layout(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(2000, View.MeasureSpec.AT_MOST));
        view.layout(0, 0, view.getMeasuredWidth(), view.getMeasuredHeight());
    }

    private TextView text(NativeTurnActions actions, int index) {
        return (TextView) ((ViewGroup) actions.getChildAt(index)).getChildAt(1);
    }

    private void sameRows(View expected, View actual) {
        assertEquals(expected.getClass(), actual.getClass());
        assertEquals(expected.getVisibility(), actual.getVisibility());
        assertEquals(expected.getLeft(), actual.getLeft()); assertEquals(expected.getTop(), actual.getTop());
        assertEquals(expected.getWidth(), actual.getWidth()); assertEquals(expected.getHeight(), actual.getHeight());
        assertEquals(expected.getContentDescription(), actual.getContentDescription());
        if (expected instanceof TextView) {
            assertEquals(((TextView) expected).getText().toString(), ((TextView) actual).getText().toString());
            assertEquals(((TextView) expected).getLineCount(), ((TextView) actual).getLineCount());
        }
        if (expected instanceof ViewGroup) {
            ViewGroup a = (ViewGroup) expected, b = (ViewGroup) actual;
            assertEquals(a.getChildCount(), b.getChildCount());
            for (int i = 0; i < a.getChildCount(); i++) sameRows(a.getChildAt(i), b.getChildAt(i));
        }
    }

    @Test public void reusedRowsMatchFreshContentAndGeometryAcrossEmptyTurnsReorderingAndInPlaceEdits() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                JSONObject person = new JSONObject().put("id", "alice").put("nickname", "First operator");
                ui.people = id -> id.equals("alice") ? person : null;
                NativeTurnActions audit = new NativeTurnActions(ui);
                JSONObject first = action("one", "alice", "approve", "Computer Use · " + "Long request summary ".repeat(8), 1735787045000L);
                JSONObject second = action("two", "missing", "deny", "Shell · denied command", 1735787105000L);
                JSONArray values = new JSONArray().put(first).put(second);
                audit.bind(values); layout(audit);
                View row = audit.getChildAt(0); TextView original = text(audit, 0); Object document = original.getText();
                audit.bind(new JSONArray(values.toString()));
                assertSame(row, audit.getChildAt(0)); assertSame(document, original.getText());

                // Same JSON instances change; cached metadata must not hide a new decision or operator.
                person.put("nickname", "Renamed operator"); first.put("kind", "respond").put("summary", "New short answer").put("created_at", 0);
                values = new JSONArray().put(first).put(JSONObject.NULL).put(second);
                audit.bind(values); layout(audit);
                assertSame(original, text(audit, 0));
                assertEquals(ui.t("Answered by {name}", "name", "Renamed operator") + " · New short answer", original.getText().toString());
                assertEquals(original.getText().toString(), original.getContentDescription());
                NativeTurnActions fresh = new NativeTurnActions(ui); fresh.bind(values); layout(fresh); sameRows(fresh, audit);

                values = new JSONArray().put(second).put(first);
                audit.bind(values); layout(audit); fresh = new NativeTurnActions(ui); fresh.bind(values); layout(fresh); sameRows(fresh, audit);
                assertTrue(text(audit, 0).getText().toString().contains("missing"));
                audit.bind(null); assertEquals(View.GONE, audit.getVisibility()); assertEquals(0, audit.getChildCount());
                audit.bind(new JSONArray().put(first)); layout(audit);
                assertSame("Empty turns keep a bounded reusable row", row, audit.getChildAt(0));
                assertEquals(1, audit.getChildCount()); assertEquals(View.VISIBLE, audit.getVisibility());
                fresh = new NativeTurnActions(ui); fresh.bind(new JSONArray().put(first)); layout(fresh); sameRows(fresh, audit);
            }
        }
    }

    @Test public void reusedAvatarRejectsLateDecodesFromPreviousOperatorAndMissingPeople() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<Runnable> jobs = new ArrayList<>();
            ui.avatars = new NativeAvatars(jobs::add, (style, seed) ->
                    "<svg xmlns='http://www.w3.org/2000/svg' width='96' height='96'><rect width='96' height='96' fill='#123456'/></svg>");
            try {
                Map<String, JSONObject> people = new HashMap<>();
                people.put("alice", new JSONObject().put("id", "alice").put("nickname", "Alice"));
                people.put("bob", new JSONObject().put("id", "bob").put("nickname", "Bob"));
                ui.people = people::get;
                NativeTurnActions audit = new NativeTurnActions(ui);
                JSONObject action = action("one", "alice", "approve", "Request", 0);
                JSONArray values = new JSONArray().put(action);
                audit.bind(values);
                ImageView avatar = (ImageView) ((ViewGroup) audit.getChildAt(0)).getChildAt(0);
                action.put("user_id", "bob"); audit.bind(values); assertEquals(2, jobs.size());
                assertSame(avatar, ((ViewGroup) audit.getChildAt(0)).getChildAt(0));
                jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertNull("Alice's decode must not replace Bob's pending avatar", avatar.getDrawable());
                action.put("user_id", "missing"); audit.bind(values);
                jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertNull("Missing people must not inherit an older operator's avatar", avatar.getDrawable());
                assertNull(avatar.getTag());
                action.put("user_id", "bob"); audit.bind(values); assertNotNull(avatar.getDrawable());
                assertTrue(text(audit, 0).getText().toString().contains("Bob"));
                assertTrue(jobs.isEmpty());
                people.get("bob").put("nickname", "Changed Bob").put("avatar", NativeAvatars.options("pixel-art", "changed"));
                audit.bind(values); assertEquals(1, jobs.size());
                assertTrue(text(audit, 0).getText().toString().contains("Changed Bob"));
            } finally { ui.avatars.clear(); }
        }
    }
}
