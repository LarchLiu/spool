package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.view.MotionEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
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
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeTranscriptViewsTest {
    private JSONObject reference() throws Exception {
        try(java.io.InputStream input=getClass().getResourceAsStream("/web-transcript.json")) {
            assertNotNull("Run transcript-browser-reference.mjs",input); return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8));
        }
    }
    private void layout(View view,int width,int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));
        view.layout(0,0,width,height); view.getViewTreeObserver().dispatchOnPreDraw();
    }
    private void idle(long millis) { Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(millis)); }

    @Test public void latestButtonMatchesActualWebBoundsGlyphAndLabelAcrossWidthsThemesLocalesAndResize() throws Exception {
        JSONArray cases=reference().getJSONArray("scenes"); double maximum=0; int values=0;
        for(int i=0;i<cases.length();i++) {
            JSONObject item=cases.getJSONObject(i); double density=item.getDouble("density");
            RuntimeEnvironment.setQualifiers("w"+item.getInt("width")+"dp-h"+item.getInt("height")+"dp-"+(density==1?"mdpi":density==3?"xxhdpi":"420dpi"));
            try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui=new NativeUi(controller.get(),item.getString("theme").equals("dark"),item.getString("locale"));
                LinearLayout page=ui.workspace(); View history=new View(controller.get()),footer=new View(controller.get());
                page.addView(history,new LinearLayout.LayoutParams(-1,0,1)); page.addView(footer,new LinearLayout.LayoutParams(-1,ui.dp(item.getInt("footerHeight"))));
                NativeComposerDock dock=new NativeComposerDock(ui,page,footer,()->{}); dock.following(false); controller.get().setContentView(dock);
                layout(dock,ui.dp(item.getInt("width")),ui.dp(item.getInt("height")));
                assertEquals(item.getString("label"),dock.latest.getContentDescription());
                assertEquals("android.widget.Button",dock.latest.createAccessibilityNodeInfo().getClassName()); assertTrue(dock.latest.isFocusable());
                View[] views={dock.latest,dock.arrow}; String[] keys={"button","arrow"};
                for(int n=0;n<views.length;n++) {
                    View view=views[n]; JSONObject expected=item.getJSONObject(keys[n]);
                    int x=view.getLeft()+(n==1?dock.latest.getLeft():0),y=view.getTop()+(n==1?dock.latest.getTop():0);
                    double[] actual={x,y,view.getWidth(),view.getHeight()}; String[] fields={"x","y","width","height"};
                    for(int f=0;f<fields.length;f++) { double error=Math.abs(actual[f]-expected.getDouble(fields[f])*density);maximum=Math.max(maximum,error);values++;assertTrue(item+"/"+keys[n]+"/"+fields[f]+" error="+error,error<=1); }
                }
                int footerTop=footer.getTop();
                dock.following(true); assertEquals(View.GONE,dock.latest.getVisibility()); layout(dock,ui.dp(item.getInt("width")),ui.dp(item.getInt("height")));
                assertEquals("The absolute button must consume no layout space",footerTop,footer.getTop());
            }
        }
        File out=new File(System.getProperty("spool.ui.artifacts"),"native-transcript-geometry-api"+android.os.Build.VERSION.SDK_INT+".json");
        java.nio.file.Files.writeString(out.toPath(),new JSONObject().put("scenes",cases.length()).put("geometryValues",values).put("maximumPhysicalPixelError",maximum).put("unchangedTolerance",1).toString(2));
    }

    @Test public void keyedReconciliationInsertsWithoutDetachingUnchangedViewsAndSnapshotsMutableJSON() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            LinearLayout parent=new LinearLayout(controller.get()); controller.get().setContentView(parent);NativeKeyedViews keyed=new NativeKeyedViews(parent);
            AtomicInteger created=new AtomicInteger(),detached=new AtomicInteger(); JSONObject data=new JSONObject().put("text","First").put("nested",new JSONObject().put("b",2).put("a",1));
            java.util.function.Supplier<View> create=()->{created.incrementAndGet();TextView v=new TextView(controller.get());v.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener(){public void onViewAttachedToWindow(View view){}public void onViewDetachedFromWindow(View view){detached.incrementAndGet();}});return v;};
            keyed.begin();View first=keyed.use("first",data,create);View second=keyed.use("second","second",create);keyed.end();layout(parent,360,800);
            JSONObject reordered=new JSONObject().put("nested",new JSONObject().put("a",1).put("b",2)).put("text","First");
            keyed.begin();View prefix=keyed.use("prefix","prefix",create);assertSame(first,keyed.use("first",reordered,create));assertSame(second,keyed.use("second","second",create));keyed.end();
            assertEquals(3,created.get());assertEquals(0,detached.get());assertSame(first,parent.getChildAt(1));assertSame(prefix,parent.getChildAt(0));
            data.put("text","Changed");keyed.begin();View replacement=keyed.use("first",data,create);keyed.end();assertNotSame(first,replacement);assertEquals(3,detached.get());assertEquals(1,parent.getChildCount());
            assertFalse(NativeChatState.equivalent(new JSONArray().put("a,b"), new JSONArray().put("a").put("b")));
        }
    }

    @Test public void latestSmoothScrollPreservesEditorFocusCompositionAndStopsOnTouchOrDispose() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);LinearLayout page=ui.workspace();ScrollView scroll=new ScrollView(controller.get());View content=new View(controller.get());content.setMinimumHeight(3000);scroll.addView(content);
            page.addView(scroll,new LinearLayout.LayoutParams(-1,0,1));NativeUi.Composer editor=ui.new Composer("Message",()->{},()->{},()->{},()->{});page.addView(editor);
            NativeHistoryScroll.State state=new NativeHistoryScroll.State();state.select("one");NativeHistoryScroll behavior=new NativeHistoryScroll(scroll,content,state,90);
            NativeComposerDock dock=new NativeComposerDock(ui,page,editor,behavior::latest);behavior.onFollowing(dock::following);controller.get().setContentView(dock);behavior.changing(null);behavior.changed();layout(dock,360,800);
            editor.input.requestFocus();android.view.inputmethod.InputConnection connection=editor.input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());assertNotNull(connection);connection.setComposingText("日本語",1);layout(dock,360,800);
            Object document=editor.input.getText();scroll.scrollTo(0,140);assertEquals(View.VISIBLE,dock.latest.getVisibility());dock.latest.performClick();assertEquals(View.GONE,dock.latest.getVisibility());
            idle(400);assertEquals(content.getHeight()-scroll.getHeight(),scroll.getScrollY());assertTrue(state.following);assertEquals(View.GONE,dock.latest.getVisibility());
            assertSame(document,editor.input.getText());assertTrue(editor.input.hasFocus());assertEquals(0,android.view.inputmethod.BaseInputConnection.getComposingSpanStart(editor.input.getText()));assertEquals(3,editor.input.getSelectionEnd());
            scroll.scrollTo(0,100);behavior.latest();android.animation.ValueAnimator animation=org.robolectric.util.ReflectionHelpers.getField(behavior,"animation");assertNotNull(animation);assertEquals(250,animation.getDuration());animation.setCurrentPlayTime(64);
            int intermediate=scroll.getScrollY();assertTrue("Animation must move: "+intermediate,intermediate>100);assertTrue("Animation must be intermediate: "+intermediate,intermediate<content.getHeight()-scroll.getHeight());
            MotionEvent down=MotionEvent.obtain(0,0,MotionEvent.ACTION_DOWN,5,5,0);scroll.dispatchTouchEvent(down);down.recycle();int stopped=scroll.getScrollY();idle(400);assertEquals(stopped,scroll.getScrollY());
            behavior.latest();animation=org.robolectric.util.ReflectionHelpers.getField(behavior,"animation");assertNotNull(animation);animation.setCurrentPlayTime(64);behavior.suspend();stopped=scroll.getScrollY();idle(400);assertEquals(stopped,scroll.getScrollY());
            behavior.latest();animation=org.robolectric.util.ReflectionHelpers.getField(behavior,"animation");assertNotNull(animation);animation.setCurrentPlayTime(64);behavior.dispose();stopped=scroll.getScrollY();idle(400);assertEquals(stopped,scroll.getScrollY());behavior.latest();idle(400);assertEquals(stopped,scroll.getScrollY());
        }
    }

    @Test public void clampingAfterShrinkReenablesFollowingEvenWhenRestoreSuppressesScrollCallbacks() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            ScrollView scroll=new ScrollView(controller.get());View content=new View(controller.get());content.setMinimumHeight(2000);content.setLayoutParams(new ScrollView.LayoutParams(-1,2000));scroll.addView(content);controller.get().setContentView(scroll);
            NativeHistoryScroll.State state=new NativeHistoryScroll.State();state.select("one");NativeHistoryScroll behavior=new NativeHistoryScroll(scroll,content,state,90);List<Boolean> follows=new ArrayList<>();behavior.onFollowing(follows::add);
            behavior.changing(null);behavior.changed();layout(scroll,360,800);scroll.scrollTo(0,500);assertFalse(state.following);
            behavior.changing(null);content.getLayoutParams().height=1000;content.setMinimumHeight(1000);content.requestLayout();behavior.changed();layout(scroll,360,800);
            assertEquals(200,scroll.getScrollY());assertTrue(state.following);assertEquals(Boolean.TRUE,follows.get(follows.size()-1));
            content.getLayoutParams().height=1600;content.setMinimumHeight(1600);content.requestLayout();layout(scroll,360,800);assertEquals(800,scroll.getScrollY());behavior.dispose();
        }
    }

    @Test public void renderLatestSurfaceForVisualInspection() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for(boolean dark:new boolean[]{false,true})try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),dark);LinearLayout page=ui.workspace();View history=new View(controller.get()),footer=new View(controller.get());page.addView(history,new LinearLayout.LayoutParams(-1,0,1));page.addView(footer,new LinearLayout.LayoutParams(-1,108));
            NativeComposerDock dock=new NativeComposerDock(ui,page,footer,()->{});dock.following(false);controller.get().setContentView(dock);layout(dock,360,800);
            Bitmap image=Bitmap.createBitmap(360,800,Bitmap.Config.ARGB_8888);dock.draw(new Canvas(image));
            try(FileOutputStream stream=new FileOutputStream(new File(System.getProperty("spool.ui.artifacts"),"native-latest-api"+android.os.Build.VERSION.SDK_INT+"-"+(dark?"dark":"light")+".png"))){image.compress(Bitmap.CompressFormat.PNG,100,stream);}image.recycle();
        }
    }
}
