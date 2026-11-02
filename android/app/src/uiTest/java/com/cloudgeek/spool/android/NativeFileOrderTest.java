package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import java.util.LinkedHashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeFileOrderTest {
    @Test public void naturalNumbersPunctuationCaseAndDirectoriesFollowActualWebOrder() throws Exception {
        JSONObject fixture;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-order.json")) {
            assertNotNull(input);
            fixture = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
        }
        JSONArray names = fixture.getJSONArray("names"), comparisons = fixture.getJSONArray("comparisons");
        for (int i = 0; i < names.length(); i++) for (int j = 0; j < names.length(); j++) {
            String left = names.getString(i), right = names.getString(j);
            assertEquals(left + " vs " + right, comparisons.getJSONArray(i).getInt(j),
                    Integer.signum(NativeFileOrder.compare(new NativeFileOrder.Key(left), new NativeFileOrder.Key(right))));
        }
        JSONArray cases = fixture.getJSONArray("cases");
        for (int c = 0; c < cases.length(); c++) {
            JSONObject sample = cases.getJSONObject(c);
            Map<String, Map<String, JSONObject>> directories = new LinkedHashMap<>(); directories.put(".", new LinkedHashMap<>());
            JSONArray paths = sample.getJSONArray("paths");
            for (int i = 0; i < paths.length(); i++) {
                String path = paths.getString(i); boolean folder = path.endsWith("/");
                String[] segments = path.replaceFirst("/$", "").split("/"); String parent = ".";
                for (int s = 0; s < segments.length; s++) {
                    String child = parent.equals(".") ? segments[s] : parent + "/" + segments[s];
                    boolean directory = s + 1 < segments.length || folder;
                    directories.get(parent).put(child, new JSONObject().put("name", segments[s]).put("path", child).put("kind", directory ? "directory" : "text"));
                    if (directory) directories.computeIfAbsent(child, key -> new LinkedHashMap<>());
                    parent = child;
                }
            }
            NativeFileModel model = new NativeFileModel("order");
            for (Map.Entry<String, Map<String, JSONObject>> entry : directories.entrySet()) {
                model.accept(entry.getKey(), new JSONObject().put("entries", new JSONArray(entry.getValue().values())));
                if (!entry.getKey().equals(".")) model.expanded.add(entry.getKey());
            }
            assertEquals(sample.getJSONArray("rows").toString(), rows(model).toString());
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeFileBrowser browser = new NativeFileBrowser(new NativeUi(controller.get(), false), "Files", model, null,
                        (path, done) -> fail("Order fixtures are already loaded"), file -> {}, address -> {}, () -> {}, value -> {});
                controller.get().setContentView(browser); model.beginSearch();
                JSONArray searches = sample.getJSONArray("searches");
                for (int i = 0; i < searches.length(); i++) {
                    JSONObject search = searches.getJSONObject(i); model.search(search.getString("query")); browser.renderTree();
                    assertEquals(search.getString("query"), search.getJSONArray("rows").toString(), rows(model).toString());
                    assertEquals(search.getString("query"), search.getString("focus"), model.focused);
                }
                browser.deactivate();
            }
        }
    }

    private JSONArray rows(NativeFileModel model) {
        JSONArray actual = new JSONArray();
        for (NativeFileModel.Row row : model.rows()) actual.put(row.entry.path + (row.entry.directory ? "/" : ""));
        return actual;
    }
}
