package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
public class NativeSidebarPreferencesTest {
    private JSONObject snapshot(long revision, String... pins) {
        return NativeSidebarPreferences.object("revision", revision, "pinned", new JSONArray(List.of(pins)), "collapsed", new JSONObject());
    }
    private static final class Request {
        JSONObject change; Consumer<JSONObject> done; Consumer<Exception> failed;
        Request(JSONObject change, Consumer<JSONObject> done, Consumer<Exception> failed) { this.change = change; this.done = done; this.failed = failed; }
    }
    private final List<Request> requests = new ArrayList<>();
    private final List<Exception> errors = new ArrayList<>();
    private NativeSidebarPreferences sync(JSONObject local) {
        NativeSidebarPreferences sync = new NativeSidebarPreferences(local, value -> {});
        sync.configure((change, done, failed) -> requests.add(new Request(change, done, failed)), () -> {}, errors::add);
        return sync;
    }

    @Test public void queuedChangesRebaseOverLiveSnapshotsAndIgnoreDelayedResponses() throws Exception {
        NativeSidebarPreferences sync = sync(snapshot(0));
        sync.togglePin("local"); assertTrue(requests.isEmpty());
        sync.accept(snapshot(1, "remote"));
        assertEquals("local", requests.get(0).change.getString("chat_id"));
        assertTrue(requests.get(0).change.getBoolean("pinned"));
        sync.toggleSection("projects"); sync.togglePin("next"); assertEquals(1, requests.size());
        JSONObject newer = snapshot(3, "other", "local", "remote");
        newer.getJSONObject("collapsed").put("recents", true);
        sync.accept(newer);
        requests.get(0).done.accept(snapshot(2, "local", "remote"));
        assertEquals("[\"next\",\"other\",\"local\",\"remote\"]", sync.snapshot().getJSONArray("pinned").toString());
        assertTrue(sync.snapshot().getJSONObject("collapsed").getBoolean("projects"));
        assertFalse("Remote section state is ignored", sync.snapshot().getJSONObject("collapsed").getBoolean("recents"));
        assertEquals("next", requests.get(1).change.getString("chat_id"));
        newer.put("revision", 4); newer.getJSONObject("collapsed").put("projects", true);
        requests.get(1).done.accept(newer);
        sync.accept(snapshot(1)); assertEquals(4, sync.snapshot().getLong("revision"));
    }

    @Test public void localMigrationCannotReplaceEstablishedSettingsAndKeepsSubsequentEdits() throws Exception {
        JSONObject local = snapshot(0, "local"); local.getJSONObject("collapsed").put("projects", true);
        NativeSidebarPreferences sync = sync(local);
        sync.toggleSection("recents"); sync.accept(snapshot(0));
        assertTrue(requests.get(0).change.has("initial"));
        assertEquals("local", sync.snapshot().getJSONArray("pinned").getString(0));
        requests.get(0).done.accept(snapshot(1, "remote"));
        assertEquals("remote", sync.snapshot().getJSONArray("pinned").getString(0));
        assertTrue(sync.snapshot().getJSONObject("collapsed").getBoolean("projects"));
        assertTrue(sync.snapshot().getJSONObject("collapsed").getBoolean("recents"));
        assertEquals("Section toggles never write to the server", 1, requests.size());
        assertFalse(requests.get(0).change.getJSONObject("initial").has("collapsed"));
        NativeSidebarPreferences other = sync(local); other.accept(snapshot(8, "server"));
        assertEquals(1, requests.size()); assertEquals("server", other.snapshot().getJSONArray("pinned").getString(0));
    }

    @Test public void failedWriteRollsBackOnlyItsOperationAndSharedWorkspaceKeepsPendingWrites() throws Exception {
        NativeSidebarPreferences sync = sync(snapshot(0)); sync.accept(snapshot(1));
        sync.togglePin("rejected"); sync.toggleSection("projects"); sync.togglePin("next");
        sync.accept(snapshot(2, "remote"));
        requests.get(0).failed.accept(new IOException("Could not save preferences"));
        assertEquals(1, errors.size());
        assertEquals("[\"next\",\"remote\"]", sync.snapshot().getJSONArray("pinned").toString());
        assertTrue(sync.snapshot().getJSONObject("collapsed").getBoolean("projects"));
        assertEquals(2, requests.size());
        android.content.Context context = org.robolectric.RuntimeEnvironment.getApplication();
        NativeSidebarState active = new NativeSidebarState(); active.bindPreferences(context, "server-a", "alice");
        active.sync = sync; active.refreshPreferences();
        NativeSidebarState cached = new NativeSidebarState(); cached.copyFrom(active);
        active.reset(); active.bindPreferences(context, "server-b", "bob");
        JSONObject accepted = snapshot(3, "remote"); accepted.getJSONObject("collapsed").put("projects", true);
        requests.get(1).done.accept(accepted);
        cached.refreshPreferences(); assertTrue(cached.collapsed.contains("projects"));
        assertTrue(active.pinned.isEmpty()); assertTrue(active.collapsed.isEmpty());
    }
}
