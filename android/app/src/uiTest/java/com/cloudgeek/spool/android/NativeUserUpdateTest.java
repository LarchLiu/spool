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
import android.widget.ImageView;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;
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

/** Original Web user/attachment lifecycle vectors replayed against production native Views. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeUserUpdateTest {
    private <T> T field(Object value,String name){return ReflectionHelpers.getField(value,name);}
    private JSONObject reference() throws Exception {
        try(java.io.InputStream input=getClass().getResourceAsStream("/web-user-updates.json")){
            assertNotNull("Run user-update-reference.mjs",input);return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8));
        }
    }
    private List<NativeAttachmentCards.Item> items(JSONArray files,Bitmap bitmap) {
        List<NativeAttachmentCards.Item> values=new ArrayList<>();
        for(int i=0;i<files.length();i++){
            JSONObject f=files.optJSONObject(i);values.add(new NativeAttachmentCards.Item(f.optString("id"),f.optString("name"),f.optString("media_type"),f.optString("excerpt"),f.optString("preview_url",f.optString("id")),()->{},image->image.setImageBitmap(bitmap)));
        }
        return values;
    }
    private View node(NativeUserRow row,JSONArray files,String key) {
        String[] parts=key.split(":");String role=parts[0];NativeMessage message=row.message;
        switch(role){
            case "row":return row;
            case "message":return row.content;
            case "bubble":return message.bubble.getParent()==message?message.bubble:null;
            case "clip":return message.bubble.getParent()==message?message.content:null;
            case "markdown":return message.bubble.getParent()==message?message.content.getChildAt(0):null;
            case "paragraph":return message.bubble.getParent()==message?tag(message.content,"markdown:p"):null;
            case "toggle":return message.bubble.getParent()==message&&message.toggle.getParent()==message.bubble?message.toggle:null;
            case "footer":return message.footer;
            case "copy":return message.footer.copy;
            case "author":return message.footer.author.getParent()==message.footer?message.footer.author:null;
            case "time":return message.footer.time.getParent()==message.footer?message.footer.time:null;
            case "avatar":return row.getChildCount()>1?row.getChildAt(1):null;
            case "attachments":return row.attachments;
        }
        if(row.attachments==null)return null;
        NativeAttachmentCard card=null;
        for(int i=0;i<files.length();i++)if(files.optJSONObject(i).optString("id").equals(parts[1]))card=(NativeAttachmentCard)row.attachments.getChildAt(i);
        if(card==null)return null;
        if(role.equals("card"))return card;if(role.equals("open"))return card.open;if(role.equals("image"))return card.image;
        if(card.imageFile)return null;
        return field(card,role.equals("excerpt")?"excerptText":role);
    }
    private View tag(View root,String tag){if(tag.equals(root.getTag()))return root;if(root instanceof ViewGroup)for(int i=0;i<((ViewGroup)root).getChildCount();i++){View found=tag(((ViewGroup)root).getChildAt(i),tag);if(found!=null)return found;}return null;}
    private void layout(View root,int width,int height){for(int i=0;i<8;i++){root.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));root.layout(0,0,width,height);if(!root.getViewTreeObserver().dispatchOnPreDraw())return;}fail("User update layout must settle");}
    private Bitmap image(View root){Bitmap b=Bitmap.createBitmap(Math.max(1,root.getWidth()),Math.max(1,root.getHeight()),Bitmap.Config.ARGB_8888);root.draw(new Canvas(b));return b;}
    private void selections(View root,boolean restore,Map<TextView,int[]> selected){
        if(root instanceof TextView&&((TextView)root).getText() instanceof Spannable){TextView t=(TextView)root;if(restore){int[] range=selected.get(t);if(range!=null)Selection.setSelection((Spannable)t.getText(),range[0],range[1]);}else if(t.getSelectionStart()>=0){selected.put(t,new int[]{t.getSelectionStart(),t.getSelectionEnd()});Selection.removeSelection((Spannable)t.getText());}}
        if(root instanceof ViewGroup)for(int i=0;i<((ViewGroup)root).getChildCount();i++)selections(((ViewGroup)root).getChildAt(i),restore,selected);
    }

    @Test public void originalWebTextAttachmentAndFooterIdentityConditionalMountsAndSelectionMatchAcrossDensitiesAndThemes() throws Exception {
        JSONObject reference=reference();JSONArray fixtures=reference.getJSONArray("cases"),targets=reference.getJSONArray("targets"),samples=new JSONArray();int comparisons=0,retained=0,selectionCases=0;
        TimeZone saved=TimeZone.getDefault();TimeZone.setDefault(TimeZone.getTimeZone("UTC"));
        Bitmap pixel=Bitmap.createBitmap(1,1,Bitmap.Config.ARGB_8888);pixel.eraseColor(0xffffffff);
        try{
            for(String density:new String[]{"mdpi","420dpi","xxhdpi"})for(boolean dark:new boolean[]{false,true})for(int f=0;f<fixtures.length();f++){
                RuntimeEnvironment.setQualifiers("w360dp-h1000dp-"+density);JSONObject fixture=fixtures.getJSONObject(f);
                try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()){
                    NativeUi ui=new NativeUi(controller.get(),dark,"en");ui.cjkFonts=NativeCjkFallbackTest.runtimeFonts();FrameLayout root=new FrameLayout(ui.context);root.setPadding(ui.dp(18),ui.dp(25),ui.dp(18),0);root.setBackgroundColor(ui.elevated);controller.get().setContentView(root);
                    NativeUserRow row=null;String text="";JSONArray files=new JSONArray();JSONObject operator=new JSONObject("{id:'alice',nickname:'Original',avatar:{style:'clay',seed:'one'}}");long timestamp=0;TextView owner=null;Map<String,View> previous=new HashMap<>();JSONArray steps=fixture.getJSONArray("steps"),snapshots=fixture.getJSONArray("snapshots");
                    for(int step=0;step<steps.length();step++){
                        JSONObject action=steps.getJSONObject(step),expected=snapshots.getJSONObject(step);if(action.has("text"))text=action.getString("text");if(action.has("files"))files=action.getJSONArray("files");if(action.has("operator"))operator=action.getJSONObject("operator");if(action.has("timestamp"))timestamp=action.getLong("timestamp");
                        if(row==null){row=new NativeUserRow(ui,operator,text,timestamp,null,value->{});root.addView(row,new FrameLayout.LayoutParams(-1,-2));}
                        row.bind(operator,text,timestamp,items(files,pixel));layout(root,ui.dp(360),ui.dp(1000));
                        if(action.has("click")){View clicked=node(row,files,action.getString("click"));assertNotNull(clicked);assertTrue(clicked.performClick());layout(root,ui.dp(360),ui.dp(1000));
                            if(action.getString("click").equals("copy")){android.content.ClipboardManager clipboard=(android.content.ClipboardManager)ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);assertEquals(expected.getString("copied"),clipboard.getPrimaryClip().getItemAt(0).getText().toString());}
                        }
                        if(action.has("select")){owner=(TextView)node(row,files,action.getString("select"));assertNotNull(owner);Selection.setSelection((Spannable)owner.getText(),1,6);}
                        for(int n=0;n<targets.length();n++){
                            String key=targets.getString(n),role=key.split(":")[0],label=fixture.getString("id")+"/"+density+"/"+dark+"/"+step+"/"+key;View current=node(row,files,key);JSONObject web=expected.getJSONObject("nodes").getJSONObject(key);
                            assertEquals(label+" presence",web.getBoolean("present"),current!=null);assertEquals(label+" identity",web.getBoolean("retained"),current!=null&&previous.get(key)==current);comparisons++;if(current!=null&&previous.get(key)==current)retained++;
                            if(current!=null&&web.has("text"))assertEquals(label+" text",web.getString("text"),((TextView)current).getText().toString());
                            if(current!=null&&role.equals("clip"))assertEquals(label+" expansion",web.getBoolean("expanded"),row.message.content.expanded);
                            if(current!=null&&role.equals("copy")){assertEquals(label+" copy",web.getString("label"),current.getContentDescription());assertEquals(web.getBoolean("disabled"),!current.isEnabled());}
                            if(current!=null&&role.equals("card"))assertEquals(web.getBoolean("image"),((NativeAttachmentCard)current).imageFile);
                            if(current!=null&&role.equals("open")){assertEquals(web.getString("type"),((NativeAttachmentCard)current.getParent()).imageFile?"image":"file");assertEquals(web.getString("name"),current.getContentDescription());}
                            previous.put(key,current);
                        }
                        if(!expected.isNull("selection")){
                            JSONObject e=expected.getJSONObject("selection");TextView selected=(TextView)node(row,files,e.getString("path"));assertEquals(e.getBoolean("present"),selected!=null);boolean inside=selected!=null&&selected==owner&&selected.getSelectionStart()>=0;
                            assertEquals(fixture.getString("id")+"/"+step+" selection",e.getBoolean("inside"),inside);if(inside){assertEquals(e.getInt("start"),selected.getSelectionStart());assertEquals(e.getInt("end"),selected.getSelectionEnd());}selectionCases++;
                        }
                        NativeUserRow fresh=new NativeUserRow(ui,operator,text,timestamp,null,value->{});fresh.bind(operator,text,timestamp,items(files,pixel));fresh.message.content.expanded=row.message.content.expanded;root.addView(fresh,new FrameLayout.LayoutParams(-1,-2));layout(root,ui.dp(360),ui.dp(1000));
                        if(row.message.footer.copy.getContentDescription().equals(ui.t("Message copied")))fresh.message.footer.copy.performClick();
                        if(row.message.content.expanded&&fresh.message.toggle.getParent()!=null){fresh.message.content.expanded=false;fresh.message.toggle.performClick();layout(root,ui.dp(360),ui.dp(1000));}
                        Map<TextView,int[]> selected=new HashMap<>();selections(row,false,selected);selections(fresh,false,new HashMap<>());Bitmap actual=image(row),control=image(fresh);
                        try{
                            assertEquals(fixture.getString("id")+"/"+step+" fresh height",fresh.getHeight(),row.getHeight());
                            if(!actual.sameAs(control)){
                                Path out=Path.of(System.getProperty("spool.ui.artifacts"));try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("user-update-mismatch-native.png"))){actual.compress(Bitmap.CompressFormat.PNG,100,stream);}try(java.io.OutputStream stream=Files.newOutputStream(out.resolve("user-update-mismatch-fresh.png"))){control.compress(Bitmap.CompressFormat.PNG,100,stream);}fail(fixture.getString("id")+"/"+density+"/"+dark+"/"+step+": complete incremental/fresh user frames differ");
                            }
                        }finally{actual.recycle();control.recycle();selections(row,true,selected);root.removeView(fresh);}
                        if(density.equals("mdpi")&&step==steps.length()-1&&(fixture.getString("id").equals("keyed-files")||fixture.getString("id").equals("overflow-lifetime"))){Bitmap b=image(root);try(java.io.OutputStream stream=Files.newOutputStream(Path.of(System.getProperty("spool.ui.artifacts"),"native-user-update-"+fixture.getString("id")+"-api"+android.os.Build.VERSION.SDK_INT+"-"+(dark?"dark":"light")+".png"))){b.compress(Bitmap.CompressFormat.PNG,100,stream);}finally{b.recycle();}}
                        samples.put(new JSONObject().put("id",fixture.getString("id")).put("density",ui.context.getResources().getDisplayMetrics().density).put("dark",dark).put("step",step));
                    }
                }
            }
        }finally{pixel.recycle();TimeZone.setDefault(saved);}
        Files.writeString(Path.of(System.getProperty("spool.ui.artifacts"),"native-user-updates-api"+android.os.Build.VERSION.SDK_INT+".json"),new JSONObject().put("snapshots",samples.length()).put("nodeComparisons",comparisons).put("retainedNodes",retained).put("selectionCases",selectionCases).put("freshNativeFrames",samples.length()).put("cases",samples).toString(2));
    }

    @Test public void unchangedImageIsNotReloadedAndCallbacksAlwaysTargetLatestMountedAttachment() {
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()){
            NativeUi ui=new NativeUi(controller.get(),false);NativeAttachmentCards cards=new NativeAttachmentCards(ui,true);controller.get().setContentView(cards);AtomicInteger loads=new AtomicInteger(),old=new AtomicInteger(),latest=new AtomicInteger();
            cards.bind(List.of(new NativeAttachmentCards.Item("a","old.png","image/png","","image-one",old::incrementAndGet,image->loads.incrementAndGet())));NativeAttachmentCard card=(NativeAttachmentCard)cards.getChildAt(0);ImageView image=card.image;View open=card.open;
            for(int i=0;i<40;i++)cards.bind(List.of(new NativeAttachmentCards.Item("a","latest.png","image/png","","image-one",latest::incrementAndGet,target->loads.incrementAndGet())));
            assertSame(card,cards.getChildAt(0));assertSame(image,card.image);assertSame(open,card.open);assertEquals(1,loads.get());open.performClick();assertEquals(0,old.get());assertEquals(1,latest.get());
            cards.bind(List.of(new NativeAttachmentCards.Item("a","latest.png","image/png","","image-two",latest::incrementAndGet,target->loads.incrementAndGet())));assertEquals(2,loads.get());
            cards.bind(List.of(new NativeAttachmentCards.Item("a","note.txt","text/plain","New excerpt","",latest::incrementAndGet,null)));assertSame(card,cards.getChildAt(0));assertNotSame(open,card.open);open.performClick();assertEquals(1,latest.get());View fileOpen=card.open;
            cards.bind(List.of());fileOpen.performClick();assertEquals(1,latest.get());assertEquals(0,cards.getChildCount());
        }
    }
}
