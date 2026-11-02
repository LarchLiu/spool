package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMarkdownUpdateTest {
    private JSONObject reference() throws Exception {
        try(java.io.InputStream input=getClass().getResourceAsStream("/web-markdown-updates.json")) {
            assertNotNull("Run markdown-update-reference.mjs",input);return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8));
        }
    }
    private void collect(View root,String tag,List<View> result) {
        if(("markdown:"+tag).equals(root.getTag()))result.add(root);
        if(root instanceof ViewGroup)for(int i=0;i<((ViewGroup)root).getChildCount();i++)collect(((ViewGroup)root).getChildAt(i),tag,result);
    }
    private View target(View root,JSONObject target) throws Exception {
        List<View> matches=new ArrayList<>();collect(root,target.getString("tag"),matches);int index=target.getInt("index");return index<matches.size()?matches.get(index):null;
    }
    private void layout(View root,int width,int height) {
        for(int i=0;i<8;i++) {
            root.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));root.layout(0,0,width,height);
            if(!root.getViewTreeObserver().dispatchOnPreDraw())return;
        }
        fail("Incremental Markdown geometry must settle");
    }
    private void selections(View root,boolean clear) {
        if(root instanceof TextView&&((TextView)root).getText() instanceof Spannable&&clear)Selection.removeSelection((Spannable)((TextView)root).getText());
        if(root instanceof ViewGroup)for(int i=0;i<((ViewGroup)root).getChildCount();i++)selections(((ViewGroup)root).getChildAt(i),clear);
    }
    private Bitmap image(View root) {
        Bitmap bitmap=Bitmap.createBitmap(root.getWidth(),Math.max(1,root.getHeight()),Bitmap.Config.ARGB_8888);root.draw(new Canvas(bitmap));return bitmap;
    }

    @Test public void originalWebNodeSelectionCheckboxAndFooterTransitionsMatchWithoutRecreatingUnchangedDocuments() throws Exception {
        JSONArray fixtures=reference().getJSONArray("cases"),samples=new JSONArray();int nodes=0,retained=0,frames=0;
        for(String density:new String[]{"mdpi","420dpi","xxhdpi"})for(int f=0;f<fixtures.length();f++) {
            JSONObject fixture=fixtures.getJSONObject(f);RuntimeEnvironment.setQualifiers("w360dp-h800dp-"+density);
            try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui=new NativeUi(controller.get(),false,"en");ui.cjkFonts=NativeCjkFallbackTest.runtimeFonts();FrameLayout root=new FrameLayout(ui.context);root.setBackgroundColor(ui.elevated);controller.get().setContentView(root);
                JSONArray sources=fixture.getJSONArray("sources"),snapshots=fixture.getJSONArray("snapshots");JSONObject targets=fixture.getJSONObject("targets");
                NativeMessage message=new NativeMessage(ui,sources.getString(0),false,null,0,false,value->{});root.addView(message,new FrameLayout.LayoutParams(-1,-2));
                Map<String,View> previous=new HashMap<>();Object selectedDocument=null;TextView selected=null;int selectedStart=-1,selectedEnd=-1;
                for(int step=0;step<sources.length();step++) {
                    String source=sources.getString(step);JSONObject snapshot=snapshots.getJSONObject(step);message.bindSource(source);layout(root,ui.dp(360),ui.dp(800));
                    for(java.util.Iterator<String> names=targets.keys();names.hasNext();) {
                        String name=names.next();
                        View view=target(message,targets.getJSONObject(name));JSONObject expected=snapshot.getJSONObject("nodes").getJSONObject(name);
                        assertEquals(fixture.getString("id")+"/"+step+"/"+name+" presence",expected.getBoolean("present"),view!=null);
                        assertEquals(fixture.getString("id")+"/"+step+"/"+name+" identity",expected.getBoolean("retained"),view!=null&&view==previous.get(name));nodes++;
                        if(view!=null&&view==previous.get(name))retained++;
                        if(view instanceof TextView)assertEquals(expected.getString("text"),((TextView)view).getText().toString());
                        if(view!=null&&!expected.isNull("checked"))assertEquals(expected.getBoolean("checked"),view.createAccessibilityNodeInfo().isChecked());
                        previous.put(name,view);
                    }
                    if(step>0&&selected!=null) {
                        JSONObject expected=snapshot.getJSONObject("selection");assertTrue(selected.isAttachedToWindow());
                        if(!fixture.optBoolean("selectionReset"))assertSame("Unchanged source/href/column updates retain the original document",selectedDocument,selected.getText());
                        if(expected.getBoolean("inside")){assertEquals(expected.getInt("start"),selected.getSelectionStart());assertEquals(expected.getInt("end"),selected.getSelectionEnd());}
                        else {assertEquals("Moved DOM selections are outside the retained text node",-1,selected.getSelectionStart());assertEquals(-1,selected.getSelectionEnd());}
                        selectedStart=selected.getSelectionStart();selectedEnd=selected.getSelectionEnd();
                    }
                    if(step>0)assertEquals("Message copied",message.footer.copy.getContentDescription());
                    message.footer.copy.performClick();android.content.ClipboardManager clipboard=(android.content.ClipboardManager)ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);assertEquals(snapshot.getString("copied"),clipboard.getPrimaryClip().getItemAt(0).getText().toString());
                    // Compare complete native Markdown frames with a fresh render, not a Web raster substitute.
                    NativeMessage fresh=new NativeMessage(ui,source,false,null,0,false,value->{});root.addView(fresh,new FrameLayout.LayoutParams(-1,-2));layout(root,ui.dp(360),ui.dp(800));
                    View actual=message.content.getChildAt(0),control=fresh.content.getChildAt(0);assertEquals(control.getWidth(),actual.getWidth());assertEquals(control.getHeight(),actual.getHeight());
                    selections(actual,true);Bitmap a=image(actual),b=image(control);
                    try {
                        if(!a.sameAs(b)) {
                            Path out=Path.of(System.getProperty("spool.ui.artifacts"));
                            try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("markdown-update-mismatch-native.png"))){a.compress(Bitmap.CompressFormat.PNG,100,stream);}
                            try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("markdown-update-mismatch-fresh.png"))){b.compress(Bitmap.CompressFormat.PNG,100,stream);}
                            fail(fixture.getString("id")+"/"+density+"/"+step+": incremental and fresh complete native Markdown frames differ");
                        }
                    }finally{a.recycle();b.recycle();}
                    frames++;root.removeView(fresh);
                    if(density.equals("mdpi")&&step==sources.length()-1&&(fixture.getString("id").equals("table")||fixture.getString("id").equals("inline-format"))) {
                        Bitmap bitmap=image(root);
                        try(java.io.OutputStream stream=Files.newOutputStream(Path.of(System.getProperty("spool.ui.artifacts"),"native-markdown-update-"+fixture.getString("id")+"-api"+android.os.Build.VERSION.SDK_INT+".png"))){bitmap.compress(Bitmap.CompressFormat.PNG,100,stream);}finally{bitmap.recycle();}
                    }
                    if(step==0&&!fixture.optString("select").isEmpty()) {selected=(TextView)previous.get(fixture.getString("select"));selectedDocument=selected.getText();selectedStart=1;selectedEnd=6;}
                    if(selected!=null){selectedStart=1;selectedEnd=6;Selection.setSelection((Spannable)selected.getText(),selectedStart,selectedEnd);}
                    samples.put(new JSONObject().put("id",fixture.getString("id")).put("density",ui.context.getResources().getDisplayMetrics().density).put("step",step));
                }
            }
        }
        Files.writeString(Path.of(System.getProperty("spool.ui.artifacts"),"native-markdown-updates-api"+android.os.Build.VERSION.SDK_INT+".json"),new JSONObject().put("snapshots",samples.length()).put("nodeComparisons",nodes).put("retainedNodes",retained).put("freshNativeFrames",frames).put("cases",samples).toString(2));
    }

    @Test public void changedReferenceDestinationBindsToRetainedTextOwnerAndDoesNotLeaveStaleClickableSpans() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);List<String> opened=new ArrayList<>();View root=NativeMarkdown.view(ui,"[Selectable label][r]\n\n[r]: https://example.com/old",false,opened::add);controller.get().setContentView(root);layout(root,360,800);
            TextView text=(TextView)((ViewGroup)root).getChildAt(0);Object document=text.getText();Selection.setSelection((Spannable)document,1,6);
            NativeMarkdown.bind(root,"[Selectable label][r]\n\n[r]: https://example.com/new");layout(root,360,800);assertSame(text,((ViewGroup)root).getChildAt(0));assertSame(document,text.getText());assertEquals(1,text.getSelectionStart());assertEquals(6,text.getSelectionEnd());
            NativeMarkdownLink[] links=((Spannable)text.getText()).getSpans(0,text.length(),NativeMarkdownLink.class);assertEquals(1,links.length);
            int before=opened.size();for(int action:new int[]{android.view.MotionEvent.ACTION_DOWN,android.view.MotionEvent.ACTION_UP}){android.view.MotionEvent event=android.view.MotionEvent.obtain(0,0,action,text.getTotalPaddingLeft()+2,text.getTotalPaddingTop()+text.getLayout().getLineBaseline(0)-2,0);try{assertTrue(text.getMovementMethod().onTouchEvent(text,(Spannable)text.getText(),event));}finally{event.recycle();}}
            assertEquals(before+1,opened.size());assertEquals("https://example.com/new",opened.get(before));
        }
    }

    @Test public void repeatedStreamingReusesAllUnchangedParagraphDocumentsWithoutDetachOrHiddenRetiredChildren() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);StringBuilder prefix=new StringBuilder();for(int i=0;i<80;i++)prefix.append("Stable paragraph ").append(i).append(".\n\n");
            ViewGroup root=(ViewGroup)NativeMarkdown.view(ui,prefix+"Stream 0",false,value->{});controller.get().setContentView(root);List<View> views=new ArrayList<>();List<Object> documents=new ArrayList<>();AtomicInteger detached=new AtomicInteger();
            for(int i=0;i<root.getChildCount();i++){View view=root.getChildAt(i);views.add(view);documents.add(((TextView)view).getText());view.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener(){public void onViewAttachedToWindow(View view){}public void onViewDetachedFromWindow(View view){detached.incrementAndGet();}});}
            Selection.setSelection((Spannable)((TextView)views.get(0)).getText(),1,6);
            for(int step=1;step<=40;step++) {NativeMarkdown.bind(root,prefix+"Stream "+step);assertEquals(81,root.getChildCount());for(int i=0;i<81;i++){assertSame(views.get(i),root.getChildAt(i));if(i<80)assertSame(documents.get(i),((TextView)root.getChildAt(i)).getText());}}
            assertEquals(0,detached.get());assertEquals(6,((TextView)views.get(0)).getSelectionEnd());NativeMarkdown.bind(root,"");assertEquals(0,root.getChildCount());assertEquals(81,detached.get());
            NativeMarkdown.bind(root,"New document");assertEquals(1,root.getChildCount());assertNotSame(views.get(0),root.getChildAt(0));
        }
    }

    @Test public void repeatedCopyAndSourceRebindKeepTheOriginalWebFeedbackDeadline() throws Exception {
        JSONArray labels=reference().getJSONObject("copyFeedback").getJSONArray("labels");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false,"en");List<String> copied=new ArrayList<>();java.util.concurrent.atomic.AtomicBoolean fail=new java.util.concurrent.atomic.AtomicBoolean();
            NativeMessageFooter footer=new NativeMessageFooter(ui,false,null,"First copied source",0,0,java.util.TimeZone.getTimeZone("UTC"),value->{if(fail.get())throw new IllegalStateException("Clipboard unavailable");copied.add(value);});controller.get().setContentView(footer);
            footer.copy.performClick();assertEquals(labels.getString(0),footer.copy.getContentDescription());org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1000));
            footer.bindText("Latest copied source");footer.copy.performClick();assertEquals(labels.getString(1),footer.copy.getContentDescription());assertEquals("Latest copied source",copied.get(copied.size()-1));
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(999));assertEquals("Message copied",footer.copy.getContentDescription());org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1));assertEquals(labels.getString(2),footer.copy.getContentDescription());
            fail.set(true);footer.copy.performClick();org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1000));footer.copy.performClick();org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1000));assertEquals("Copy message",footer.copy.getContentDescription());
        }
    }
}
