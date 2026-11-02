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
import android.widget.ScrollView;
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
import org.robolectric.util.ReflectionHelpers;

/** Mounted production tool Views versus original Web conditional mounts and update vectors. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeActivityUpdateTest {
    private JSONObject reference() throws Exception {
        try(java.io.InputStream input=getClass().getResourceAsStream("/web-activity-updates.json")) {
            assertNotNull("Run activity-update-reference.mjs",input);return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8));
        }
    }
    private <T> T field(Object object,String name) {return object==null?null:ReflectionHelpers.getField(object,name);}
    private Object entry(NativeActivityList list,String group) {
        Map<String,Object> entries=field(list,"entries");Object value=entries.get(group);if(value!=null)return value;
        value=entries.get(group+":0");if(value!=null)return value;
        for(Map.Entry<String,Object> item:entries.entrySet())if(item.getKey().startsWith("message:")&&item.getKey().endsWith(":"+group))return item.getValue();
        return null;
    }
    private Object item(NativeActivityList list,String group,String index) {
        Object entry=entry(list,group);if(entry==null)return null;
        if(entry.getClass().getSimpleName().equals("Item"))return index.equals("0")?entry:null;
        if(!entry.getClass().getSimpleName().equals("Group"))return null;
        Map<String,Object> items=field(entry,"items");return items.get(group+":"+index);
    }
    private View node(NativeActivityList list,String key) {
        String[] pieces=key.split(":");String role=pieces[0];
        if(role.equals("work")){NativeDisclosureRow toggle=field(list,"toggle");return toggle==null?list.summaryLabel:toggle;}
        if(role.equals("list")){View view=field(list,"list");return view.getParent()==list?view:null;}
        Object entry=entry(list,pieces[1]);if(entry==null)return null;
        if(role.equals("entry"))return field(entry,"view");
        if(role.equals("group"))return entry.getClass().getSimpleName().equals("Group")?field(entry,"heading"):null;
        Object item=item(list,pieces[1],pieces[2]);if(item==null)return null;
        if(role.equals("item"))return field(item,"view");if(role.equals("item-toggle"))return field(item,"row");
        NativeToolDetail detail=field(item,"detail");if(role.equals("detail"))return detail;if(detail==null)return null;
        Object block=field(detail,role.contains("output")?"output":"input");View source=field(block,"content");
        if(role.equals("input")||role.equals("output"))return source;
        if(role.endsWith("-text"))return source instanceof ScrollView||source instanceof NativeMarkdownFlow?text(source):null;
        if(!(source instanceof NativePatchView))return null;
        ViewGroup patch=(ViewGroup)source;
        if(role.startsWith("raw-toggle"))return patch.getChildAt(patch.getChildCount()-2);
        return text(patch.getChildAt(patch.getChildCount()-1));
    }
    private TextView text(View root) {
        if(root instanceof TextView&&((TextView)root).isTextSelectable())return (TextView)root;
        if(root instanceof ViewGroup)for(int i=0;i<((ViewGroup)root).getChildCount();i++){TextView text=text(((ViewGroup)root).getChildAt(i));if(text!=null)return text;}
        return null;
    }
    private List<NativeActivityModel.Activity> activities(NativeUi ui,JSONArray parts) {
        List<JSONObject> values=new ArrayList<>();for(int i=0;i<parts.length();i++)values.add(parts.optJSONObject(i));return NativeActivityModel.fromParts(ui,values,"codex");
    }
    private void layout(View root,int width,int height) {
        for(int i=0;i<8;i++) {
            root.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));root.layout(0,0,width,height);
            if(!root.getViewTreeObserver().dispatchOnPreDraw())return;
        }
        fail("Incremental tool layout must settle");
    }
    private Bitmap image(View view) {
        Bitmap bitmap=Bitmap.createBitmap(Math.max(1,view.getWidth()),Math.max(1,view.getHeight()),Bitmap.Config.ARGB_8888);view.draw(new Canvas(bitmap));return bitmap;
    }
    private String normalized(CharSequence value){return value.toString().replaceAll("\\s+"," ").trim();}
    private NativeActivityList.State copy(NativeActivityList.State state) {
        NativeActivityList.State next=new NativeActivityList.State();next.open=state.open;next.live=state.live;next.initialized=state.initialized;next.count=state.count;next.groups.addAll(state.groups);next.items.addAll(state.items);return next;
    }
    private void selections(View view,boolean restore,Map<TextView,int[]> selected) {
        if(view instanceof TextView&&((TextView)view).getText() instanceof Spannable) {
            TextView text=(TextView)view;
            if(restore){int[] range=selected.get(text);if(range!=null)Selection.setSelection((Spannable)text.getText(),range[0],range[1]);}
            else if(text.getSelectionStart()>=0&&text.getSelectionEnd()>=0){selected.put(text,new int[]{text.getSelectionStart(),text.getSelectionEnd()});Selection.removeSelection((Spannable)text.getText());}
        }
        if(view instanceof ViewGroup)for(int i=0;i<((ViewGroup)view).getChildCount();i++)selections(((ViewGroup)view).getChildAt(i),restore,selected);
    }
    private void click(NativeActivityList list,String key) {
        View view=node(list,key);assertNotNull("Missing mounted native click "+key,view);
        if(key.startsWith("item:"))view=node(list,key.replace("item:","item-toggle:"));assertTrue(view.performClick());
    }

    @Test public void originalWebGroupItemSourceIdentitySelectionAndConditionalMountsMatchAcrossDensitiesAndThemes() throws Exception {
        JSONObject reference=reference();JSONArray fixtures=reference.getJSONArray("cases"),targets=reference.getJSONArray("targets"),samples=new JSONArray();int nodes=0,retained=0,frames=0,selectionCases=0;
        for(String density:new String[]{"mdpi","420dpi","xxhdpi"})for(boolean dark:new boolean[]{false,true})for(int f=0;f<fixtures.length();f++) {
            JSONObject fixture=fixtures.getJSONObject(f);RuntimeEnvironment.setQualifiers("w360dp-h1000dp-"+density);
            try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui=new NativeUi(controller.get(),dark,"en");ui.cjkFonts=NativeCjkFallbackTest.runtimeFonts();FrameLayout root=new FrameLayout(ui.context);root.setPadding(ui.dp(18),ui.dp(25),ui.dp(18),0);root.setBackgroundColor(ui.elevated);controller.get().setContentView(root);
                NativeActivityList.State state=new NativeActivityList.State();JSONArray parts=new JSONArray();boolean live=true;NativeActivityList list=null;Map<String,View> previous=new HashMap<>();TextView selectionOwner=null;
                JSONArray steps=fixture.getJSONArray("steps"),snapshots=fixture.getJSONArray("snapshots");
                for(int step=0;step<steps.length();step++) {
                    JSONObject action=steps.getJSONObject(step),expected=snapshots.getJSONObject(step);if(action.has("parts"))parts=action.getJSONArray("parts");if(action.has("live"))live=action.getBoolean("live");
                    if(list==null){list=new NativeActivityList(ui,activities(ui,parts),"Working for 5s",live,state,value->{});root.addView(list,new FrameLayout.LayoutParams(-1,-2));}
                    else if(action.has("parts")||action.has("live"))list.bind(activities(ui,parts),"Working for 5s",live);
                    if(action.has("click"))click(list,action.getString("click"));layout(root,ui.dp(360),ui.dp(1000));
                    if(action.has("select")){TextView selected=text(node(list,action.getString("select")));assertNotNull(selected);Selection.setSelection((Spannable)selected.getText(),1,6);selectionOwner=selected;}
                    for(int t=0;t<targets.length();t++) {
                        String key=targets.getString(t),role=key.split(":")[0];View view=node(list,key);JSONObject browser=expected.getJSONObject("nodes").getJSONObject(key);String label=fixture.getString("id")+"/"+density+"/"+dark+"/"+step+"/"+key;
                        assertEquals(label+" presence",browser.getBoolean("present"),view!=null);assertEquals(label+" identity",browser.getBoolean("retained"),view!=null&&previous.get(key)==view);nodes++;if(view!=null&&previous.get(key)==view)retained++;
                        if(view!=null&&browser.has("text"))assertEquals(label+" text",browser.getString("text"),((TextView)view).getText().toString());
                        if(view!=null&&browser.has("label")) {
                            String title;
                            if(view instanceof NativeDisclosureRow){NativeDisclosureRow row=(NativeDisclosureRow)view;TextView stats=field(row,"stats");title=normalized(row.label.getText()+(stats==null?"":stats.getText().toString()));}
                            else title=normalized(((TextView)view).getText());
                            assertEquals(label+" label",browser.getString("label"),title);
                            if(browser.isNull("expanded"))assertFalse(view instanceof NativeDisclosureRow);
                            else assertEquals(label+" expansion",browser.getString("expanded"),Boolean.toString(view.isSelected()));
                        }
                        if(view!=null&&role.startsWith("raw-toggle"))assertEquals(label+" raw state",browser.getBoolean("expanded"),((TextView)view).getText().toString().startsWith("\u25bc"));
                        if(view!=null&&browser.has("type"))assertEquals(label+" source mode",browser.getString("type"),view instanceof NativeMarkdownFlow?"markdown":view instanceof ScrollView?"raw":"diff");
                        previous.put(key,view);
                    }
                    if(!expected.isNull("selection")) {
                        JSONObject selection=expected.getJSONObject("selection");View selectedNode=node(list,selection.getString("path"));TextView selected=text(selectedNode);assertEquals(selection.getBoolean("present"),selectedNode!=null);
                        // Android stores per-View caret spans; only the explicitly selected owner
                        // corresponds to Chrome's single global Selection, not a new View's focus caret.
                        boolean inside=selected!=null&&selected==selectionOwner&&selected.getSelectionStart()>=0;assertEquals(fixture.getString("id")+"/"+step+" selection inside (native "+(selected==null?"absent":selected.getSelectionStart()+":"+selected.getSelectionEnd())+")",selection.getBoolean("inside"),inside);
                        if(inside){assertEquals(selection.getInt("start"),selected.getSelectionStart());assertEquals(selection.getInt("end"),selected.getSelectionEnd());}selectionCases++;
                    }
                    NativeActivityList fresh=new NativeActivityList(ui,activities(ui,parts),"Working for 5s",live,copy(state),value->{});root.addView(fresh,new FrameLayout.LayoutParams(-1,-2));
                    for(int t=0;t<targets.length();t++){String key=targets.getString(t);if(key.startsWith("raw-toggle")){View a=node(list,key),b=node(fresh,key);if(a!=null&&b!=null&&((TextView)a).getText().toString().startsWith("\u25bc"))b.performClick();}}
                    layout(root,ui.dp(360),ui.dp(1000));assertEquals(fresh.getWidth(),list.getWidth());assertEquals(fixture.getString("id")+"/"+step+" fresh height",fresh.getHeight(),list.getHeight());
                    Map<TextView,int[]> selected=new HashMap<>();selections(list,false,selected);Bitmap actual=image(list),control=image(fresh);
                    try {
                        if(!actual.sameAs(control)) {
                            Path out=Path.of(System.getProperty("spool.ui.artifacts"));
                            try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("activity-update-mismatch-native.png"))){actual.compress(Bitmap.CompressFormat.PNG,100,stream);}
                            try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("activity-update-mismatch-fresh.png"))){control.compress(Bitmap.CompressFormat.PNG,100,stream);}
                            fail(fixture.getString("id")+"/"+density+"/"+dark+"/"+step+": complete incremental/fresh native tool frames differ");
                        }
                    }finally{actual.recycle();control.recycle();selections(list,true,selected);root.removeView(fresh);}
                    if(density.equals("mdpi")&&step==steps.length()-1&&(fixture.getString("id").equals("group-growth")||fixture.getString("id").equals("diff-key"))) {
                        Bitmap bitmap=image(root);try(java.io.OutputStream stream=Files.newOutputStream(Path.of(System.getProperty("spool.ui.artifacts"),"native-activity-update-"+fixture.getString("id")+"-api"+android.os.Build.VERSION.SDK_INT+"-"+(dark?"dark":"light")+".png"))){bitmap.compress(Bitmap.CompressFormat.PNG,100,stream);}finally{bitmap.recycle();}
                    }
                    frames++;samples.put(new JSONObject().put("id",fixture.getString("id")).put("density",ui.context.getResources().getDisplayMetrics().density).put("dark",dark).put("step",step));
                }
            }
        }
        Files.writeString(Path.of(System.getProperty("spool.ui.artifacts"),"native-activity-updates-api"+android.os.Build.VERSION.SDK_INT+".json"),new JSONObject().put("snapshots",samples.length()).put("nodeComparisons",nodes).put("retainedNodes",retained).put("selectionCases",selectionCases).put("freshNativeFrames",frames).put("cases",samples).toString(2));
    }

    @Test public void unrelatedLiveResultsRetainInputOutputSelectionAndScrollUntilTheirActualConditionalUnmount() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h1000dp-mdpi");try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);JSONArray parts=new JSONArray();for(int i=0;i<16;i++){parts.put(new JSONObject().put("role","assistant").put("kind","tool_use").put("id","use"+i).put("name","read_file").put("text","{\"file_path\":\"src/file"+i+".ts\"}"));parts.put(new JSONObject().put("role","tool").put("kind","tool_result").put("text","Stable output\n".repeat(60)));}
            NativeActivityList.State state=new NativeActivityList.State();NativeActivityList list=new NativeActivityList(ui,activities(ui,parts),"Working for 5s",true,state,value->{});controller.get().setContentView(list);click(list,"item:0:0");layout(list,360,1000);
            TextView input=(TextView)node(list,"input-text:0:0"),output=(TextView)node(list,"output-text:0:0");Object inputDocument=input.getText(),outputDocument=output.getText();Selection.setSelection((Spannable)outputDocument,1,6);ScrollView scroll=(ScrollView)output.getParent();scroll.scrollTo(0,120);assertEquals(120,scroll.getScrollY());AtomicInteger detached=new AtomicInteger();
            output.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener(){public void onViewAttachedToWindow(View view){}public void onViewDetachedFromWindow(View view){detached.incrementAndGet();}});
            for(int i=0;i<40;i++){parts.getJSONObject(parts.length()-1).put("text","Other changing output "+i);list.bind(activities(ui,parts),"Working for 6s",true);layout(list,360,1000);assertSame(input,node(list,"input-text:0:0"));assertSame(output,node(list,"output-text:0:0"));assertSame(inputDocument,input.getText());assertSame(outputDocument,output.getText());assertEquals(6,output.getSelectionEnd());assertEquals(120,scroll.getScrollY());}
            assertEquals(0,detached.get());click(list,"group:0");assertNull(node(list,"output-text:0:0"));assertEquals(1,detached.get());click(list,"group:0");assertNotSame(output,node(list,"output-text:0:0"));assertTrue(state.items.contains("0:0"));
        }
    }
}
