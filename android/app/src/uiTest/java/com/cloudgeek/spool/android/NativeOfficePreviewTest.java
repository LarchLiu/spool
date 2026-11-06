package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import static org.robolectric.Shadows.shadowOf;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeOfficePreviewTest {
    private JSONObject fixture() throws Exception {
        try (java.io.InputStream input=getClass().getResourceAsStream("/office-preview.json")) { assertNotNull(input); return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8)); }
    }
    private void layout(View view,int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(800,View.MeasureSpec.EXACTLY)); view.layout(0,0,width,800);
    }
    private void settle(View view,int width) { for (int i=0;i<3;i++) { shadowOf(android.os.Looper.getMainLooper()).idle();layout(view,width); } }
    private TextView text(View view,String value) {
        if (view instanceof TextView && ((TextView)view).getText().toString().equals(value)) return (TextView)view;
        if (view instanceof ViewGroup) for (int i=0;i<((ViewGroup)view).getChildCount();i++) { TextView found=text(((ViewGroup)view).getChildAt(i),value);if (found!=null) return found; }
        return null;
    }
    private void snapshot(View view,String name) throws Exception {
        File directory=new File(System.getProperty("spool.ui.artifacts")); assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap bitmap=Bitmap.createBitmap(view.getWidth(),view.getHeight(),Bitmap.Config.ARGB_8888);view.draw(new Canvas(bitmap));
        try (FileOutputStream out=new FileOutputStream(new File(directory,name+".png"))) {assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG,100,out));} bitmap.recycle();
    }

    @Test public void actualGoWordAndExcelPagesRenderAsSelectableNativeViews() throws Exception {
        JSONObject fixture=fixture();RuntimeEnvironment.setQualifiers("w390dp-h800dp-mdpi");
        for (boolean dark:new boolean[]{false,true}) try (ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),dark,"zh-CN"); ArrayList<String> requests=new ArrayList<>();
            NativeOfficePreview word=new NativeOfficePreview(ui,(path,done)->{requests.add(path);done.done(fixture.optJSONObject("word"),null);},"/api/chats/chat/file?path=report.docx");
            controller.get().setContentView(word);settle(word,390);
            assertEquals(View.GONE,word.toolbar.getVisibility());assertTrue(text(word,"Office 预览").isTextSelectable());
            TextView merged=text(word,"合并标题"); assertNotNull(merged);assertTrue(merged.getWidth()>text(word,"名称").getWidth());
            assertNotNull(text(word,"正文 Bold italic\n<script>literal</script>"));snapshot(word,"office-word-"+(dark?"dark":"light"));
            word.close();
            NativeOfficePreview excel=new NativeOfficePreview(ui,(path,done)->{
                requests.add(path);android.net.Uri uri=android.net.Uri.parse(path);int sheet=Integer.parseInt(uri.getQueryParameter("sheet")),row=Integer.parseInt(uri.getQueryParameter("row"));
                done.done(fixture.optJSONArray("sheets").optJSONObject(sheet==1?2:sheet==2?3:row==0?0:1),null);
            },"/api/chats/chat/file?path=book.xlsx");
            controller.get().setContentView(excel);settle(excel,390);
            assertEquals("第 1–50 行",excel.range.getText().toString());assertFalse(excel.previous.isEnabled());assertTrue(excel.next.isEnabled());
            assertTrue(text(excel,"$2.50").isTextSelectable());assertNotNull(text(excel,"=SUM(A2:B2)"));snapshot(excel,"office-excel-"+(dark?"dark":"light"));
            excel.next.performClick();settle(excel,390);assertEquals("第 51–100 行",excel.range.getText().toString());assertNotNull(text(excel,"51"));assertTrue(excel.previous.isEnabled());
            excel.sheets.performClick();settle(excel,390);((View)text(ui.activePopover.content,"Summary").getParent()).performClick();settle(excel,390);assertNotNull(text(excel,"Summary worksheet"));assertFalse(excel.next.isEnabled());
            excel.sheets.performClick();settle(excel,390);((View)text(ui.activePopover.content,"Empty").getParent()).performClick();settle(excel,390);assertNotNull(text(excel,ui.t("This worksheet is empty.")));
            excel.close();assertTrue(excel.closed());assertEquals(0,excel.scroll.getChildCount());
            assertTrue(requests.stream().allMatch(path->path.startsWith("/api/chats/chat/file?")&&path.contains("office=1")));
        }
    }

    @Test public void supersededAndClosedOfficeResponsesCannotReplaceCurrentFile() throws Exception {
        JSONObject fixture=fixture();ArrayList<NativeFileBrowser.Completion> requests=new ArrayList<>();
        try (ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);
            NativeFilePreview panel=new NativeFilePreview(ui,"project","chat",null,target->{},address->{},file->{},()->{});
            panel.officeReader=(path,done)->requests.add(done);controller.get().setContentView(panel);
            JSONObject file=new JSONObject().put("path","a.docx").put("name","a.docx").put("kind","binary");panel.bind(file,new NativeFileTarget("a.docx",0,0),"","scope");
            NativeOfficePreview first=panel.office;assertNotNull(first);assertEquals(View.GONE,panel.scroll.getVisibility());
            panel.bind(file,new NativeFileTarget("a.docx",0,0),"","scope");assertSame(first,panel.office);assertEquals(1,requests.size());
            panel.bind(new JSONObject().put("path","b.xlsx").put("name","b.xlsx").put("kind","binary"),new NativeFileTarget("b.xlsx",0,0),"","scope");
            assertTrue(first.closed());NativeOfficePreview current=panel.office;
            requests.get(0).done(fixture.getJSONObject("word"),null);settle(panel,390);assertSame(current,panel.office);assertNull(text(panel,"Office 预览"));
            requests.get(1).done(fixture.getJSONArray("sheets").getJSONObject(0),null);settle(panel,390);assertNotNull(text(panel,"销售统计"));
            panel.releaseDocuments();assertTrue(current.closed());assertNull(panel.office);
            assertEquals("",NativeFileModel.previewKind(new JSONObject().put("name","old.doc").put("kind","binary")));
            assertEquals("",NativeFileModel.previewKind(new JSONObject().put("name","old.xls").put("kind","binary")));
        }
    }

    @Test public void failedOfficePreviewOffersRetryAndDoesNotKeepOldDocumentVisible() throws Exception {
        JSONObject fixture=fixture();ArrayList<NativeFileBrowser.Completion> pending=new ArrayList<>();
        try (ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);
            NativeOfficePreview preview=new NativeOfficePreview(ui,(path,done)->pending.add(done),"/api/projects/p/file?path=a.docx");
            controller.get().setContentView(preview);pending.get(0).done(null,new java.io.IOException("bad file"));settle(preview,390);
            assertNotNull(text(preview,ui.t("This Office file cannot be previewed. Download it to open locally.")));
            text(preview,ui.t("Retry")).performClick();assertEquals(2,pending.size());pending.get(1).done(fixture.getJSONObject("word"),null);settle(preview,390);assertNotNull(text(preview,"Office 预览"));preview.close();
        }
    }
}
