package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeViewedImagesTest {
    private List<JSONObject> parts(String json) throws Exception {
        JSONArray values = new JSONArray(json); List<JSONObject> parts = new ArrayList<>();
        for (int i = 0; i < values.length(); i++) parts.add(values.getJSONObject(i));
        return parts;
    }
    private <T> List<T> views(View view, Class<T> type) {
        List<T> out = new ArrayList<>(); if (type.isInstance(view)) out.add(type.cast(view));
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) out.addAll(views(((ViewGroup) view).getChildAt(i), type));
        return out;
    }
    @Test public void codexClaudeAndParallelImageToolsPairResultsByIDAndCountPixels() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<NativeActivityModel.Activity> values = NativeActivityModel.fromParts(ui, parts("""
                [{role:'assistant',kind:'tool_use',id:'a',name:'functions.view_image',text:'{"path":"/a.png"}'},
                 {role:'assistant',kind:'tool_use',id:'b',name:'Read',text:'{"file_path":"/b.jpg"}'},
                 {role:'tool',kind:'tool_result',id:'a',tool:{image_urls:['a-pixels']},text:''},
                 {role:'tool',kind:'tool_result',id:'b',images:['b-pixels'],text:''},
                 {role:'assistant',kind:'tool_use',name:'multi_tool_use.parallel',text:'{"tool_uses":[{"recipient_name":"functions.view_image","parameters":{"path":"/c.png"}},{"recipient_name":"functions.view_image","parameters":{"path":"/d.png"}}]}'},
                 {role:'assistant',kind:'tool_use',name:'Read',text:'{"file_path":"/notes.txt"}'}]
                """), "claude");
            assertEquals(4, values.size()); assertEquals("a-pixels", values.get(0).imageURLs.get(0)); assertEquals("b-pixels", values.get(1).imageURLs.get(0));
            assertEquals(List.of("/c.png", "/d.png"), values.get(2).images); assertEquals("read", values.get(3).kind);
            List<NativeActivityModel.Group> groups = NativeActivityModel.groups(ui, values);
            assertEquals(2, groups.size()); assertEquals("Viewed 4 images", groups.get(0).label);
            assertEquals("已查看 4 张图片", NativeActivityModel.groups(new NativeUi(controller.get(), false, "zh-CN"), values).get(0).label);
            List<NativeActivityModel.Activity> orphan = NativeActivityModel.fromParts(ui, parts("[{role:'assistant',kind:'tool_use',id:'x',name:'Read',text:'{}'},{role:'tool',kind:'tool_result',id:'unknown',images:['pixels'],text:''}]"), "claude");
            assertNull(orphan.get(0).result); assertEquals("image", orphan.get(1).kind);
        }
    }
    @Test public void thumbnailsWrapPreviewCollapseAndShowMissingImageWithoutRawToolDetails() throws Exception {
        RuntimeEnvironment.setQualifiers("w320dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); List<String> loads = new ArrayList<>(), opened = new ArrayList<>();
            Bitmap picture = Bitmap.createBitmap(60, 40, Bitmap.Config.ARGB_8888); picture.eraseColor(0xff52704a);
            NativeTranscriptImages images = new NativeTranscriptImages() {
                public void load(ImageView image, String path, String source, int edge, java.util.function.Consumer<Exception> completion) {
                    loads.add(source); if (source.equals("missing")) completion.accept(new java.io.IOException("Missing"));
                    else { image.setImageBitmap(picture); completion.accept(null); }
                }
                public void preview(String path, String source, String name) { opened.add(source); }
            };
            List<JSONObject> parts = parts("[{role:'tool',kind:'tool_result',images:['first','second','third','missing'],text:'base64 should stay hidden'}]");
            NativeActivityList.State state = new NativeActivityList.State(); state.initialized = true; state.open = true;
            NativeActivityList list = new NativeActivityList(ui, parts, "codex", "Completed", false, state, s -> {}, images);
            list.setBackgroundColor(ui.elevated);
            controller.get().setContentView(list);
            NativeDisclosureRow heading = views(list, NativeDisclosureRow.class).get(1); assertEquals("Viewed 4 images", heading.label.getText().toString());
            assertTrue(views(list, NativeViewedImages.class).isEmpty()); assertTrue(loads.isEmpty());
            heading.performClick();
            list.measure(View.MeasureSpec.makeMeasureSpec(284, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(700, View.MeasureSpec.AT_MOST)); list.layout(0,0,284,list.getMeasuredHeight());
            NativeViewedImages grid = views(list, NativeViewedImages.class).get(0);
            assertEquals(4, grid.getChildCount()); assertEquals(4, loads.size()); assertTrue(grid.getChildAt(3).getTop() > grid.getChildAt(0).getTop());
            assertEquals(View.VISIBLE, ((ViewGroup)grid.getChildAt(3)).getChildAt(1).getVisibility());
            grid.getChildAt(0).performClick(); assertEquals(List.of("first"), opened); assertTrue(views(list, NativeToolDetail.class).isEmpty());
            Bitmap screenshot = Bitmap.createBitmap(list.getWidth(), list.getHeight(), Bitmap.Config.ARGB_8888); list.draw(new Canvas(screenshot));
            Path output = Path.of(System.getProperty("spool.ui.artifacts"), "viewed-images-api" + android.os.Build.VERSION.SDK_INT + ".png"); Files.createDirectories(output.getParent());
            try (java.io.OutputStream stream = Files.newOutputStream(output)) { screenshot.compress(Bitmap.CompressFormat.PNG, 100, stream); } screenshot.recycle();
            heading.performClick(); assertTrue(views(list, NativeViewedImages.class).isEmpty()); heading.performClick(); assertEquals(4, loads.size());
            picture.recycle();
        }
    }
}
